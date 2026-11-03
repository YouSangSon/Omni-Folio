package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const legacyFIFOCostBasisPolicy = "fifo_exact_else_half_even_residual_8_v1"

type ledgerReplayPreview struct {
	LedgerRevision       int64              `json:"ledger_revision"`
	EventsSHA256         string             `json:"events_sha256"`
	LegacySnapshotSHA256 string             `json:"legacy_snapshot_sha256"`
	SnapshotSHA256       string             `json:"snapshot_sha256"`
	LegacySnapshot       *PortfolioSnapshot `json:"legacy_snapshot"`
	Snapshot             *PortfolioSnapshot `json:"snapshot"`
}

func requireCurrentLedgerReplay(ctx context.Context, q queryer) error {
	var policy string
	var revision int64
	if err := q.QueryRowContext(ctx, `SELECT policy,(SELECT revision FROM ledger_meta WHERE singleton=1) FROM ledger_replay_state WHERE singleton=1`).Scan(&policy, &revision); err != nil {
		return fmt.Errorf("ledger replay state: %w", err)
	}
	if policy != fifoCostBasisPolicy {
		return errors.New("ledger replay upgrade required; preview and activate the corrected replay before normal use")
	}
	return requireLedgerRevision(ctx, q, revision)
}

func requireLedgerRevision(ctx context.Context, q queryer, revision int64) error {
	var first, last, count int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MIN(sequence),0),COALESCE(MAX(sequence),0),COUNT(*) FROM events`).Scan(&first, &last, &count); err != nil {
		return err
	}
	if revision < 0 || count != revision || last != revision || (count > 0 && first != 1) {
		return errors.New("ledger replay revision does not match contiguous events")
	}
	return nil
}

func ledgerPrefixSHA256(ctx context.Context, q queryer, revision int64) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,counter_currency,counter_amount,corrects_source_event_id,receipt_id,recorded_at FROM events WHERE sequence<=? ORDER BY sequence`, revision)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	var count int64
	for rows.Next() {
		values := make([]any, 18)
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return "", err
		}
		count++
		if values[0] != count {
			return "", errors.New("ledger replay prefix sequence is not contiguous")
		}
		if err := encoder.Encode(values); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count != revision {
		return "", errors.New("ledger replay prefix revision mismatch")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ledgerReplayAt(ctx context.Context, q queryer, revision int64, recordedAt string) (*ledgerReplayPreview, error) {
	old, err := snapshotFromPolicy(ctx, q, legacyFIFOCostBasisPolicy, revision)
	if err != nil {
		return nil, fmt.Errorf("legacy ledger replay: %w", err)
	}
	corrected, err := snapshotFromPolicy(ctx, q, fifoCostBasisPolicy, revision)
	if err != nil {
		return nil, fmt.Errorf("corrected ledger replay: %w", err)
	}
	if !canonicalUTCString(recordedAt) {
		return nil, errors.New("captured ledger recorded_at is not canonical UTC")
	}
	// Duplicate-only imports advance metadata without adding a new event.
	old.RecordedAt, corrected.RecordedAt = recordedAt, recordedAt
	_, oldSHA, err := orderJSONHash(old)
	if err != nil {
		return nil, err
	}
	_, correctedSHA, err := orderJSONHash(corrected)
	if err != nil {
		return nil, err
	}
	eventsSHA, err := ledgerPrefixSHA256(ctx, q, revision)
	if err != nil {
		return nil, err
	}
	return &ledgerReplayPreview{revision, eventsSHA, oldSHA, correctedSHA, old, corrected}, nil
}

func previewLedgerReplay(ctx context.Context, db *sql.DB) (*ledgerReplayPreview, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return prepareLedgerReplay(ctx, tx)
}

func prepareLedgerReplay(ctx context.Context, tx *sql.Tx) (*ledgerReplayPreview, error) {
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	if err := proveLedgerReplayState(ctx, tx, true); err != nil {
		return nil, err
	}
	var policy string
	var legacyRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT policy,legacy_revision FROM ledger_replay_state WHERE singleton=1`).Scan(&policy, &legacyRevision); err != nil {
		return nil, err
	}
	if policy != legacyFIFOCostBasisPolicy || legacyRevision <= 0 {
		return nil, errors.New("ledger does not require a legacy replay transition")
	}
	revision, _, err := (&Service{}).proveLedgerEvents(ctx, tx)
	if err != nil {
		return nil, err
	}
	if revision != legacyRevision {
		return nil, errors.New("legacy replay preparation changed the ledger revision")
	}
	var recordedAt string
	if err := tx.QueryRowContext(ctx, `SELECT recorded_at FROM ledger_meta WHERE singleton=1`).Scan(&recordedAt); err != nil {
		return nil, err
	}
	preview, err := ledgerReplayAt(ctx, tx, revision, recordedAt)
	if err != nil {
		return nil, err
	}
	return preview, nil
}

func activateLedgerReplay(ctx context.Context, db *sql.DB, expectedRevision int64, expectedEventsSHA, expectedSnapshotSHA string, now time.Time) (*ledgerReplayPreview, error) {
	if !strategySHA256Pattern.MatchString(expectedEventsSHA) || !strategySHA256Pattern.MatchString(expectedSnapshotSHA) || expectedRevision <= 0 {
		return nil, errors.New("replay activation requires the exact preview revision, events SHA and corrected snapshot SHA")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	preview, err := prepareLedgerReplay(ctx, tx)
	if err != nil {
		return nil, err
	}
	if preview.LedgerRevision != expectedRevision || preview.EventsSHA256 != expectedEventsSHA || preview.SnapshotSHA256 != expectedSnapshotSHA {
		return nil, errors.New("ledger replay preview changed; review a new preview before activation")
	}
	recordedAt, err := time.Parse(time.RFC3339Nano, preview.Snapshot.RecordedAt)
	if err != nil || now.Before(recordedAt) {
		return nil, errors.New("ledger replay activation precedes the captured ledger")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_replay_transition VALUES(1,?,?,?,?,?,?)`, preview.LedgerRevision, preview.Snapshot.RecordedAt,
		preview.EventsSHA256, preview.LegacySnapshotSHA256, preview.SnapshotSHA256, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ledger_replay_state SET policy=? WHERE singleton=1`, fifoCostBasisPolicy); err != nil {
		return nil, err
	}
	if err := proveLedgerReplayState(ctx, tx, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return preview, nil
}

func proveLedgerReplayState(ctx context.Context, q queryer, archival bool) error {
	if err := requireLedgerReplaySchema(ctx, q); err != nil {
		return err
	}
	var policy string
	var legacyRevision, currentRevision, count int64
	if err := q.QueryRowContext(ctx, `SELECT policy,legacy_revision,(SELECT revision FROM ledger_meta WHERE singleton=1),(SELECT COUNT(*) FROM ledger_replay_transition) FROM ledger_replay_state WHERE singleton=1`).Scan(&policy, &legacyRevision, &currentRevision, &count); err != nil {
		return err
	}
	if legacyRevision < 0 || legacyRevision > currentRevision {
		return errors.New("ledger replay origin revision is invalid")
	}
	if err := requireLedgerRevision(ctx, q, currentRevision); err != nil {
		return err
	}
	if policy == legacyFIFOCostBasisPolicy {
		if !archival || legacyRevision == 0 || currentRevision != legacyRevision || count != 0 {
			return errors.New("legacy replay state is not eligible for current activation")
		}
		return nil
	}
	if policy != fifoCostBasisPolicy || (legacyRevision == 0 && count != 0) || (legacyRevision > 0 && count != 1) {
		return errors.New("ledger replay policy or transition count is invalid")
	}
	if legacyRevision == 0 {
		return nil
	}
	var transitionRevision int64
	var recordedAt, eventsSHA, oldSHA, correctedSHA, activatedAt string
	if err := q.QueryRowContext(ctx, `SELECT ledger_revision,ledger_recorded_at,events_sha256,legacy_snapshot_sha256,snapshot_sha256,activated_at FROM ledger_replay_transition WHERE singleton=1`).Scan(&transitionRevision, &recordedAt, &eventsSHA, &oldSHA, &correctedSHA, &activatedAt); err != nil {
		return err
	}
	if transitionRevision != legacyRevision || !canonicalUTCString(recordedAt) || !canonicalUTCString(activatedAt) {
		return errors.New("ledger replay transition metadata is invalid")
	}
	recordedTime, _ := time.Parse(time.RFC3339Nano, recordedAt)
	activatedTime, _ := time.Parse(time.RFC3339Nano, activatedAt)
	if activatedTime.Before(recordedTime) {
		return errors.New("ledger replay activation precedes its captured ledger")
	}
	preview, err := ledgerReplayAt(ctx, q, legacyRevision, recordedAt)
	if err != nil {
		return err
	}
	if preview.Snapshot.RecordedAt != recordedAt || preview.EventsSHA256 != eventsSHA || preview.LegacySnapshotSHA256 != oldSHA || preview.SnapshotSHA256 != correctedSHA {
		return errors.New("ledger replay transition proof mismatch")
	}
	return nil
}

func requireLedgerReplaySchema(ctx context.Context, q queryer) error {
	script, err := migrationFiles.ReadFile("migrations/022_ledger_replay_policy.sql")
	if err != nil {
		return err
	}
	objects := map[string]string{
		"ledger_replay_state": "TABLE", "ledger_replay_transition": "TABLE",
		"ledger_replay_transition_no_update": "TRIGGER", "ledger_replay_transition_no_delete": "TRIGGER",
		"ledger_replay_state_no_delete": "TRIGGER", "ledger_replay_state_transition": "TRIGGER", "events_replay_policy_guard": "TRIGGER",
		"ledger_replay_state_no_replace": "TRIGGER", "ledger_replay_transition_insert_guard": "TRIGGER",
	}
	normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	for name, kind := range objects {
		prefix := "CREATE " + kind + " " + name + " "
		_, after, found := strings.Cut(string(script), prefix)
		terminator := "END;"
		if kind == "TABLE" {
			terminator = ") STRICT;"
		}
		body, _, ended := strings.Cut(after, terminator)
		if !found || !ended {
			return errors.New("ledger replay migration definition is incomplete")
		}
		var actual string
		if err := q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name=? AND type=?`, name, strings.ToLower(kind)).Scan(&actual); err != nil {
			return err
		}
		expected := strings.TrimSuffix(prefix+body+terminator, ";")
		if normalize(actual) != normalize(expected) {
			return fmt.Errorf("ledger replay schema object %s changed", name)
		}
	}
	return nil
}

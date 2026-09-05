package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// ponytail: global serialization remains until strategy selection itself is account-scoped; use per-account leases only then.
	paperRunnerLeaseScope = "paper_strategy_selection"
	paperRunnerLeaseTTL   = 30 * time.Second
)

var errPaperRunnerLeaseHeld = errors.New("paper runner lease is held")

type paperRunnerLeaseRecord struct {
	Scope                    string `json:"scope"`
	FencingToken             int64  `json:"fencing_token"`
	OwnerID                  string `json:"owner_id"`
	AccountRef               string `json:"account_ref"`
	HeartbeatAtNS            int64  `json:"heartbeat_at_ns"`
	ExpiresAtNS              int64  `json:"expires_at_ns"`
	StrategySelectionEventID string `json:"strategy_selection_event_id"`
	SelectedResultSHA256     string `json:"selected_result_sha256"`
}

type paperRunnerClaim struct {
	Scope                    string
	FencingToken             int64
	OwnerID                  string
	AccountRef               string
	HeartbeatAtNS            int64
	LeaseExpiresAtNS         int64
	StrategySelectionEventID string
	SelectedResultSHA256     string
}

type paperRunnerLeaseRecoveryProof struct {
	SHA256 string
	Leases int
	Active int
}

type storedPaperRunnerLease struct {
	record     paperRunnerLeaseRecord
	recordSHA  string
	recordJSON string
}

func (s *Service) acquirePaperRunnerLease(ctx context.Context, accountRef string) (*paperRunnerClaim, error) {
	if s == nil || s.db == nil || s.now == nil || s.paperRunnerOwner == "" || !orderAlias(accountRef, "account") {
		return nil, errors.New("paper runner lease is not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperRunnerLeaseRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper runner lease recovery: %w", err)
	}
	row, err := loadPaperRunnerLease(ctx, tx)
	if err != nil {
		return nil, err
	}
	nowNS, err := paperRunnerTimeNS(s.now())
	if err != nil {
		return nil, err
	}
	_, selectionEventID, selectedResult, err := currentPaperPerformanceSelection(ctx, tx)
	if err != nil || selectedResult == noStrategySelection {
		return nil, errors.Join(errors.New("paper runner requires a current strategy selection"), err)
	}
	if row.record.OwnerID != "" {
		if nowNS < row.record.HeartbeatAtNS {
			return nil, errors.New("paper runner lease clock regressed")
		}
		if nowNS < row.record.ExpiresAtNS {
			if row.record.OwnerID == s.paperRunnerOwner && row.record.AccountRef == accountRef &&
				row.record.StrategySelectionEventID == selectionEventID && row.record.SelectedResultSHA256 == selectedResult {
				if err := tx.Commit(); err != nil {
					return nil, err
				}
				return paperRunnerClaimFromRecord(row.record), nil
			}
			return nil, fmt.Errorf("%w by another process", errPaperRunnerLeaseHeld)
		}
	}
	if row.record.FencingToken == math.MaxInt64 {
		return nil, errors.New("paper runner fencing token overflow")
	}
	expiresNS, err := paperRunnerExpiryNS(nowNS)
	if err != nil {
		return nil, err
	}
	next := paperRunnerLeaseRecord{
		Scope: paperRunnerLeaseScope, FencingToken: row.record.FencingToken + 1,
		OwnerID: s.paperRunnerOwner, AccountRef: accountRef, HeartbeatAtNS: nowNS, ExpiresAtNS: expiresNS,
		StrategySelectionEventID: selectionEventID, SelectedResultSHA256: selectedResult,
	}
	if err := updatePaperRunnerLeaseTx(ctx, tx, row.record, next); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paperRunnerClaimFromRecord(next), nil
}

func (s *Service) heartbeatPaperRunnerLease(ctx context.Context, claim *paperRunnerClaim) (*paperRunnerClaim, error) {
	if s == nil || s.db == nil || s.now == nil || claim == nil || claim.OwnerID != s.paperRunnerOwner {
		return nil, errors.New("paper runner lease is not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperRunnerLeaseRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper runner lease recovery: %w", err)
	}
	next, err := renewPaperRunnerLeaseTx(ctx, tx, claim, s.paperRunnerOwner, s.now())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return next, nil
}

func (s *Service) releasePaperRunnerLease(ctx context.Context, claim *paperRunnerClaim) error {
	if s == nil || s.db == nil || claim == nil || claim.OwnerID != s.paperRunnerOwner {
		return errors.New("paper runner lease is not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := provePaperRunnerLeaseRecovery(ctx, tx); err != nil {
		return fmt.Errorf("paper runner lease recovery: %w", err)
	}
	row, err := loadPaperRunnerLease(ctx, tx)
	if err != nil {
		return err
	}
	if !paperRunnerClaimMatchesRecord(claim, row.record) || row.record.OwnerID != s.paperRunnerOwner {
		return errors.New("paper runner lease was lost")
	}
	released := paperRunnerLeaseRecord{Scope: paperRunnerLeaseScope, FencingToken: row.record.FencingToken}
	if err := updatePaperRunnerLeaseTx(ctx, tx, row.record, released); err != nil {
		return err
	}
	return tx.Commit()
}

func validatePaperRunnerLeaseTx(ctx context.Context, q orderQuerier, claim *paperRunnerClaim, accountRef, ownerID string, now time.Time) error {
	row, err := loadPaperRunnerLease(ctx, q)
	if err != nil {
		return err
	}
	if claim == nil || ownerID == "" || claim.OwnerID != ownerID || !paperRunnerClaimMatchesRecord(claim, row.record) || claim.AccountRef != accountRef {
		return errors.New("paper runner lease was lost")
	}
	nowNS, err := paperRunnerTimeNS(now)
	if err != nil {
		return err
	}
	if nowNS < row.record.HeartbeatAtNS {
		return errors.New("paper runner lease clock regressed")
	}
	if nowNS >= row.record.ExpiresAtNS {
		return errors.New("paper runner lease expired")
	}
	_, eventID, selectedResult, err := currentPaperPerformanceSelection(ctx, q)
	if err != nil || eventID != claim.StrategySelectionEventID || selectedResult != claim.SelectedResultSHA256 {
		return errors.Join(errors.New("paper runner strategy selection changed"), err)
	}
	return nil
}

func renewPaperRunnerLeaseTx(ctx context.Context, tx *sql.Tx, claim *paperRunnerClaim, ownerID string, now time.Time) (*paperRunnerClaim, error) {
	if err := validatePaperRunnerLeaseTx(ctx, tx, claim, claimAccountRef(claim), ownerID, now); err != nil {
		return nil, err
	}
	return renewPaperRunnerLeaseRowTx(ctx, tx, claim, now)
}

func renewPaperRunnerLeaseAfterPolicyRollbackTx(ctx context.Context, tx *sql.Tx, claim *paperRunnerClaim, policyEventID, rollbackEventID, ownerID string, now time.Time) (*paperRunnerClaim, error) {
	row, err := loadPaperRunnerLease(ctx, tx)
	if err != nil {
		return nil, err
	}
	if claim == nil || ownerID == "" || claim.OwnerID != ownerID || !paperRunnerClaimMatchesRecord(claim, row.record) {
		return nil, errors.New("paper runner lease was lost")
	}
	nowNS, err := paperRunnerTimeNS(now)
	if err != nil {
		return nil, err
	}
	if nowNS < row.record.HeartbeatAtNS {
		return nil, errors.New("paper runner lease clock regressed")
	}
	if nowNS >= row.record.ExpiresAtNS {
		return nil, errors.New("paper runner lease expired")
	}
	var eventType, sourceEventID, previousResult, reasonCode, linkedPolicyID string
	if err := tx.QueryRowContext(ctx, `SELECT event_type,source_event_id,previous_selected_result_sha256,reason_code,paper_performance_policy_event_id
		FROM strategy_selection_events WHERE event_id=?`, rollbackEventID).
		Scan(&eventType, &sourceEventID, &previousResult, &reasonCode, &linkedPolicyID); err != nil {
		return nil, err
	}
	var decision, policyRollbackID string
	if err := tx.QueryRowContext(ctx, `SELECT decision,rollback_selection_event_id FROM paper_performance_policy_events WHERE policy_event_id=?`, policyEventID).
		Scan(&decision, &policyRollbackID); err != nil {
		return nil, err
	}
	_, currentEventID, _, err := currentPaperPerformanceSelection(ctx, tx)
	if err != nil || currentEventID != rollbackEventID || eventType != "ROLLBACK" ||
		sourceEventID != claim.StrategySelectionEventID || previousResult != claim.SelectedResultSHA256 ||
		reasonCode != "automatic_performance_rollback" || linkedPolicyID != policyEventID ||
		decision != "HALT_AND_ROLLBACK" || policyRollbackID != rollbackEventID {
		return nil, errors.Join(errors.New("paper runner automatic rollback lease transition is invalid"), err)
	}
	return renewPaperRunnerLeaseRowTx(ctx, tx, claim, now)
}

func renewPaperRunnerLeaseRowTx(ctx context.Context, tx *sql.Tx, claim *paperRunnerClaim, now time.Time) (*paperRunnerClaim, error) {
	nowNS, err := paperRunnerTimeNS(now)
	if err != nil {
		return nil, err
	}
	expiresNS, err := paperRunnerExpiryNS(nowNS)
	if err != nil {
		return nil, err
	}
	previous := paperRunnerRecordFromClaim(claim)
	next := previous
	next.HeartbeatAtNS, next.ExpiresAtNS = nowNS, expiresNS
	if err := updatePaperRunnerLeaseTx(ctx, tx, previous, next); err != nil {
		return nil, err
	}
	return paperRunnerClaimFromRecord(next), nil
}

func rejectLivePaperRunnerLease(ctx context.Context, q orderQuerier, now time.Time) error {
	if _, err := provePaperRunnerLeaseRecovery(ctx, q); err != nil {
		return fmt.Errorf("paper runner lease recovery: %w", err)
	}
	row, err := loadPaperRunnerLease(ctx, q)
	if err != nil {
		return err
	}
	if row.record.OwnerID == "" {
		return nil
	}
	nowNS, err := paperRunnerTimeNS(now)
	if err != nil {
		return err
	}
	if nowNS < row.record.HeartbeatAtNS {
		return errors.New("paper runner lease clock regressed")
	}
	if nowNS < row.record.ExpiresAtNS {
		return errors.New("paper runner is active; stop and release it before changing strategy selection")
	}
	if err := rejectIncompletePaperRunForExpiredClaim(ctx, q, row.record); err != nil {
		return err
	}
	return nil
}

func rejectIncompletePaperRunForExpiredClaim(ctx context.Context, q orderQuerier, lease paperRunnerLeaseRecord) error {
	var incomplete int
	err := q.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM paper_performance_events p
		WHERE p.account_ref=? AND p.strategy_selection_event_id=?
		  AND NOT EXISTS(
			SELECT 1 FROM paper_strategy_performance_events w
			JOIN paper_performance_policy_events e
			  ON e.account_ref=w.account_ref
			 AND e.strategy_selection_event_id=w.strategy_selection_event_id
			 AND e.strategy_performance_id=w.strategy_performance_id
			WHERE w.account_ref=p.account_ref
			  AND w.strategy_selection_event_id=p.strategy_selection_event_id
			  AND w.latest_performance_id=p.performance_id
		  )
	)`, lease.AccountRef, lease.StrategySelectionEventID).Scan(&incomplete)
	if err != nil {
		return err
	}
	if incomplete != 0 {
		return errors.New("expired paper runner has an incomplete paper performance chain")
	}
	return nil
}

func provePaperRunnerLeaseRecovery(ctx context.Context, q orderQuerier) (paperRunnerLeaseRecoveryProof, error) {
	// Policy recovery has legacy early returns. Require current migration
	// history before treating it as the complete runner prerequisite proof.
	if err := requireSchemaContext(ctx, q); err != nil {
		return paperRunnerLeaseRecoveryProof{}, err
	}
	if _, err := provePaperPerformancePolicyRecovery(ctx, q); err != nil {
		return paperRunnerLeaseRecoveryProof{}, err
	}
	if err := validatePaperRunnerLeaseSchema(ctx, q); err != nil {
		return paperRunnerLeaseRecoveryProof{}, err
	}
	var rows, active int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE owner_id IS NOT NULL) FROM paper_runner_leases`).Scan(&rows, &active); err != nil {
		return paperRunnerLeaseRecoveryProof{}, err
	}
	if rows != 1 {
		return paperRunnerLeaseRecoveryProof{}, errors.New("paper runner lease singleton is invalid")
	}
	row, err := loadPaperRunnerLease(ctx, q)
	if err != nil {
		return paperRunnerLeaseRecoveryProof{}, err
	}
	hash := sha256.New()
	if err := json.NewEncoder(hash).Encode([]any{"paper_runner_leases", row.record}); err != nil {
		return paperRunnerLeaseRecoveryProof{}, err
	}
	return paperRunnerLeaseRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Leases: rows, Active: active}, nil
}

func validatePaperRunnerLeaseSchema(ctx context.Context, q orderQuerier) error {
	migration, err := migrationFiles.ReadFile("migrations/021_paper_runner_leases.sql")
	if err != nil {
		return err
	}
	script := string(migration)
	const bindingIndexSQL = "create unique index strategy_selection_events_runner_binding_idx on strategy_selection_events(event_id, selected_result_sha256)"
	var bindingIndex string
	if err := q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='index' AND name='strategy_selection_events_runner_binding_idx' AND tbl_name='strategy_selection_events'`).Scan(&bindingIndex); err != nil || normalizePaperRunnerLeaseSQL(bindingIndex) != bindingIndexSQL {
		return errors.Join(errors.New("paper runner lease selection-binding index does not match migration 021"), err)
	}
	const tablePrefix = "CREATE TABLE paper_runner_leases"
	start := strings.Index(script, tablePrefix)
	if start < 0 {
		return errors.New("paper runner lease migration table is missing")
	}
	expectedTable := strings.SplitN(script[start:], "\n\nINSERT INTO", 2)[0]
	expectedTable = normalizePaperRunnerLeaseSQL(strings.TrimSuffix(strings.TrimSpace(expectedTable), ";"))
	var actualTable string
	if err := q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='paper_runner_leases'`).Scan(&actualTable); err != nil || normalizePaperRunnerLeaseSQL(actualTable) != expectedTable {
		return errors.Join(errors.New("paper runner lease table does not match migration 021"), err)
	}
	var strict int
	if err := q.QueryRowContext(ctx, `SELECT strict FROM pragma_table_list WHERE schema='main' AND type='table' AND name='paper_runner_leases'`).Scan(&strict); err != nil || strict != 1 {
		return errors.Join(errors.New("paper runner lease strict schema is missing"), err)
	}
	for _, name := range []string{"paper_runner_leases_no_delete", "paper_runner_leases_state_guard"} {
		expected, err := migrationTriggerDefinition(script, name)
		if err != nil {
			return err
		}
		var actual string
		if err := q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='trigger' AND tbl_name='paper_runner_leases' AND name=?`, name).Scan(&actual); err != nil || normalizePaperRunnerLeaseSQL(actual) != expected {
			return errors.Join(fmt.Errorf("paper runner lease trigger %s does not match migration 021", name), err)
		}
	}
	var triggerCount int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND tbl_name='paper_runner_leases'`).Scan(&triggerCount); err != nil || triggerCount != 2 {
		return errors.Join(errors.New("paper runner lease trigger inventory is invalid"), err)
	}
	return nil
}

func normalizePaperRunnerLeaseSQL(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func loadPaperRunnerLease(ctx context.Context, q orderQuerier) (storedPaperRunnerLease, error) {
	var row storedPaperRunnerLease
	var owner, account sql.NullString
	var heartbeat, expires sql.NullInt64
	var selectionEventID, selectedResult sql.NullString
	err := q.QueryRowContext(ctx, `SELECT scope,fencing_token,owner_id,account_ref,heartbeat_at_ns,expires_at_ns,
		strategy_selection_event_id,selected_result_sha256,record_sha256,record_json
		FROM paper_runner_leases WHERE scope=?`, paperRunnerLeaseScope).Scan(
		&row.record.Scope, &row.record.FencingToken, &owner, &account, &heartbeat, &expires,
		&selectionEventID, &selectedResult, &row.recordSHA, &row.recordJSON)
	if err != nil {
		return storedPaperRunnerLease{}, err
	}
	row.record.OwnerID, row.record.AccountRef = owner.String, account.String
	row.record.HeartbeatAtNS, row.record.ExpiresAtNS = heartbeat.Int64, expires.Int64
	row.record.StrategySelectionEventID, row.record.SelectedResultSHA256 = selectionEventID.String, selectedResult.String
	if err := validateStoredPaperRunnerLease(ctx, q, row); err != nil {
		return storedPaperRunnerLease{}, err
	}
	return row, nil
}

func validateStoredPaperRunnerLease(ctx context.Context, q orderQuerier, row storedPaperRunnerLease) error {
	var decoded paperRunnerLeaseRecord
	if err := json.Unmarshal([]byte(row.recordJSON), &decoded); err != nil {
		return err
	}
	canonical, actualSHA, err := orderJSONHash(decoded)
	if err != nil || string(canonical) != row.recordJSON || actualSHA != row.recordSHA || decoded != row.record ||
		row.record.Scope != paperRunnerLeaseScope || row.record.FencingToken < 0 {
		return errors.New("paper runner lease canonical record mismatch")
	}
	active := row.record.OwnerID != ""
	if !active {
		if row.record.AccountRef != "" || row.record.HeartbeatAtNS != 0 || row.record.ExpiresAtNS != 0 ||
			row.record.StrategySelectionEventID != "" || row.record.SelectedResultSHA256 != "" {
			return errors.New("paper runner released lease tuple is invalid")
		}
		return nil
	}
	if row.record.FencingToken <= 0 || !safeOrderID(row.record.OwnerID) || !orderAlias(row.record.AccountRef, "account") ||
		row.record.HeartbeatAtNS <= 0 || row.record.ExpiresAtNS-row.record.HeartbeatAtNS != int64(paperRunnerLeaseTTL) ||
		!safeOrderID(row.record.StrategySelectionEventID) || !strategySHA256Pattern.MatchString(row.record.SelectedResultSHA256) {
		return errors.New("paper runner active lease tuple is invalid")
	}
	var selected string
	if err := q.QueryRowContext(ctx, `SELECT selected_result_sha256 FROM strategy_selection_events WHERE event_id=?`, row.record.StrategySelectionEventID).Scan(&selected); err != nil || selected != row.record.SelectedResultSHA256 {
		return errors.Join(errors.New("paper runner lease strategy binding is invalid"), err)
	}
	return nil
}

func updatePaperRunnerLeaseTx(ctx context.Context, tx *sql.Tx, previous, next paperRunnerLeaseRecord) error {
	raw, hash, err := orderJSONHash(next)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE paper_runner_leases SET
		fencing_token=?,owner_id=?,account_ref=?,heartbeat_at_ns=?,expires_at_ns=?,strategy_selection_event_id=?,selected_result_sha256=?,record_sha256=?,record_json=?
		WHERE scope=? AND fencing_token=? AND owner_id IS ? AND account_ref IS ? AND heartbeat_at_ns IS ? AND expires_at_ns IS ?
		AND strategy_selection_event_id IS ? AND selected_result_sha256 IS ?`,
		next.FencingToken, nullable(next.OwnerID), nullable(next.AccountRef), nullableInt64(next.HeartbeatAtNS), nullableInt64(next.ExpiresAtNS),
		nullable(next.StrategySelectionEventID), nullable(next.SelectedResultSHA256), hash, string(raw), paperRunnerLeaseScope,
		previous.FencingToken, nullable(previous.OwnerID), nullable(previous.AccountRef), nullableInt64(previous.HeartbeatAtNS), nullableInt64(previous.ExpiresAtNS),
		nullable(previous.StrategySelectionEventID), nullable(previous.SelectedResultSHA256))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("paper runner lease was lost")
	}
	return nil
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func paperRunnerTimeNS(value time.Time) (int64, error) {
	value = value.UTC()
	ns := value.UnixNano()
	if ns <= 0 || !time.Unix(0, ns).UTC().Equal(value) {
		return 0, errors.New("paper runner clock is outside Unix nanosecond range")
	}
	return ns, nil
}

func paperRunnerExpiryNS(nowNS int64) (int64, error) {
	if nowNS > math.MaxInt64-int64(paperRunnerLeaseTTL) {
		return 0, errors.New("paper runner lease expiry overflow")
	}
	return nowNS + int64(paperRunnerLeaseTTL), nil
}

func paperRunnerClaimFromRecord(record paperRunnerLeaseRecord) *paperRunnerClaim {
	return &paperRunnerClaim{
		Scope: record.Scope, FencingToken: record.FencingToken, OwnerID: record.OwnerID, AccountRef: record.AccountRef,
		HeartbeatAtNS: record.HeartbeatAtNS, LeaseExpiresAtNS: record.ExpiresAtNS,
		StrategySelectionEventID: record.StrategySelectionEventID, SelectedResultSHA256: record.SelectedResultSHA256,
	}
}

func paperRunnerRecordFromClaim(claim *paperRunnerClaim) paperRunnerLeaseRecord {
	if claim == nil {
		return paperRunnerLeaseRecord{}
	}
	return paperRunnerLeaseRecord{
		Scope: claim.Scope, FencingToken: claim.FencingToken, OwnerID: claim.OwnerID, AccountRef: claim.AccountRef,
		HeartbeatAtNS: claim.HeartbeatAtNS, ExpiresAtNS: claim.LeaseExpiresAtNS,
		StrategySelectionEventID: claim.StrategySelectionEventID, SelectedResultSHA256: claim.SelectedResultSHA256,
	}
}

func paperRunnerClaimMatchesRecord(claim *paperRunnerClaim, record paperRunnerLeaseRecord) bool {
	return claim != nil && paperRunnerRecordFromClaim(claim) == record
}

func claimAccountRef(claim *paperRunnerClaim) string {
	if claim == nil {
		return ""
	}
	return claim.AccountRef
}

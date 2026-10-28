package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

func requirePaperPromotionJournalSchema(ctx context.Context, q orderQuerier) error {
	script, err := migrationFiles.ReadFile("migrations/027_paper_promotion.sql")
	if err != nil {
		return err
	}
	for _, table := range []string{"paper_promotion_claims", "paper_promotion_decisions"} {
		var shadows int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_temp_master WHERE name=? OR tbl_name=?`, table, table).Scan(&shadows); err != nil {
			return err
		}
		if shadows != 0 {
			return errors.New("paper promotion temporary schema shadows the journal")
		}
		for _, name := range []string{table, table + "_no_update", table + "_no_delete"} {
			kind := "TRIGGER"
			if name == table {
				kind = "TABLE"
			}
			expected := ""
			for _, block := range strings.Split(string(script), "\n\n") {
				definition := strings.Join(strings.Fields(block), " ")
				if strings.HasPrefix(definition, "CREATE "+kind+" "+name+" ") {
					expected = strings.TrimSuffix(definition, ";")
					break
				}
			}
			var actual string
			if expected == "" || q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name=? AND type=? AND tbl_name=?`, name, strings.ToLower(kind), table).Scan(&actual) != nil || strings.Join(strings.Fields(actual), " ") != expected {
				return errors.New("paper promotion physical journal schema changed")
			}
		}
		var objects int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE tbl_name=? AND type IN ('index','trigger')`, table).Scan(&objects); err != nil {
			return err
		}
		if objects != 3 {
			return errors.New("paper promotion physical journal inventory changed")
		}
	}
	return nil
}

// This proof cannot call the registry root: the registry uses it to validate automatic selections.
func provePaperPromotionRecovery(ctx context.Context, q orderQuerier) (paperPromotionRecoveryProof, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	proof := paperPromotionRecoveryProof{}
	var schema int
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&schema); err != nil {
		return proof, err
	}
	if schema < 27 {
		proof.SHA256 = hex.EncodeToString(hash.Sum(nil))
		return proof, nil
	}
	if err := requirePaperPromotionJournalSchema(ctx, q); err != nil {
		return proof, err
	}
	type storedClaim struct {
		sequence int64
		claim    paperPromotionClaim
		sha      string
	}
	claims := []storedClaim{}
	rows, err := q.QueryContext(ctx, `SELECT sequence,attempt_id,record_sha256,record_json,recorded_at FROM paper_promotion_claims ORDER BY sequence`)
	if err != nil {
		return proof, err
	}
	previous := "no_attempt"
	for rows.Next() {
		var item storedClaim
		var id, raw, at string
		if err := rows.Scan(&item.sequence, &id, &item.sha, &raw, &at); err != nil {
			rows.Close()
			return proof, err
		}
		if json.Unmarshal([]byte(raw), &item.claim) != nil {
			rows.Close()
			return proof, errors.New("paper promotion claim is invalid")
		}
		canonical, sha, err := orderJSONHash(item.claim)
		if err != nil || string(canonical) != raw || sha != item.sha || item.claim.AttemptID != id || item.claim.RecordedAt != at || item.sequence != int64(len(claims)+1) || item.claim.ExpectedPreviousAttemptID != previous {
			rows.Close()
			return proof, errors.New("paper promotion claim chain or hash mismatch")
		}
		claims = append(claims, item)
		previous = id
		if err := encoder.Encode([]any{"claim", item.sequence, item.sha, raw}); err != nil {
			rows.Close()
			return proof, err
		}
	}
	if err := closeRows(rows); err != nil {
		return proof, err
	}
	byID := map[string]storedClaim{}
	for _, item := range claims {
		if err := validateStoredPaperPromotionClaim(ctx, q, item.claim, item.sequence); err != nil {
			return proof, err
		}
		byID[item.claim.AttemptID] = item
	}
	rows, err = q.QueryContext(ctx, `SELECT sequence,attempt_id,selection_event_id,record_sha256,record_json,recorded_at FROM paper_promotion_decisions ORDER BY sequence`)
	if err != nil {
		return proof, err
	}
	type storedDecision struct {
		sequence  int64
		decision  paperPromotionDecision
		raw, sha  string
		selection sql.NullString
	}
	decisions := []storedDecision{}
	for rows.Next() {
		var item storedDecision
		var id, at string
		if err := rows.Scan(&item.sequence, &id, &item.selection, &item.sha, &item.raw, &at); err != nil {
			rows.Close()
			return proof, err
		}
		if json.Unmarshal([]byte(item.raw), &item.decision) != nil {
			rows.Close()
			return proof, errors.New("paper promotion decision is invalid")
		}
		canonical, sha, err := orderJSONHash(item.decision)
		if err != nil || string(canonical) != item.raw || sha != item.sha || id != item.decision.AttemptID || at != item.decision.RecordedAt || item.selection.String != item.decision.SelectionEventID || item.selection.Valid != (item.decision.SelectionEventID != "") || item.sequence != int64(len(decisions)+1) {
			rows.Close()
			return proof, errors.New("paper promotion decision hash or metadata mismatch")
		}
		decisions = append(decisions, item)
	}
	if err := closeRows(rows); err != nil {
		return proof, err
	}
	linked := map[string]string{}
	for _, item := range decisions {
		stored, ok := byID[item.decision.AttemptID]
		if !ok {
			return proof, errors.New("paper promotion decision has no claim")
		}
		if err := validateStoredPaperPromotionDecision(ctx, q, stored.claim, stored.sha, item.decision); err != nil {
			return proof, err
		}
		if item.selection.Valid {
			linked[item.selection.String] = item.decision.AttemptID
		}
		delete(byID, item.decision.AttemptID)
		if err := encoder.Encode([]any{"decision", item.sequence, item.sha, item.raw}); err != nil {
			return proof, err
		}
	}
	if len(byID) > 1 {
		return proof, errors.New("paper promotion has multiple pending claims")
	}
	rows, err = q.QueryContext(ctx, `SELECT event_id,paper_promotion_attempt_id FROM strategy_selection_events WHERE paper_promotion_attempt_id IS NOT NULL OR reason_code='auto_comparison'`)
	if err != nil {
		return proof, err
	}
	for rows.Next() {
		var event string
		var id sql.NullString
		if err := rows.Scan(&event, &id); err != nil {
			rows.Close()
			return proof, err
		}
		if !id.Valid || linked[event] != id.String {
			rows.Close()
			return proof, errors.New("automatic selection has no proven comparison")
		}
		delete(linked, event)
	}
	if err := closeRows(rows); err != nil {
		return proof, err
	}
	if len(linked) != 0 {
		return proof, errors.New("paper promotion decision has no automatic selection")
	}
	proof.SHA256 = hex.EncodeToString(hash.Sum(nil))
	proof.Claims = len(claims)
	proof.Decisions = len(decisions)
	return proof, nil
}

func validateStoredPaperPromotionClaim(ctx context.Context, q orderQuerier, c paperPromotionClaim, sequence int64) error {
	if c.SchemaVersion != paperPromotionClaimSchema || !safeOrderID(c.AttemptID) || !orderAlias(c.AccountRef, "account") || !safeOrderID(c.SessionID) || !safeOrderID(c.ExpectedCurrentEventID) || c.CandidateResultSHA256 == c.IncumbentResultSHA256 || !canonicalPaperTimes(c.RecordedAt) || c.OrderSequenceCutoff < 0 || c.ResearchSequenceCutoff < 1 {
		return errors.New("paper promotion claim contract is invalid")
	}
	for _, sha := range []string{c.CandidateResultSHA256, c.IncumbentResultSHA256, c.ExecutionPolicySHA256, c.InputDataSHA256, c.StateSHA256} {
		if !strategySHA256Pattern.MatchString(sha) {
			return errors.New("paper promotion claim hash is invalid")
		}
	}
	symbol, bars, err := parsePaperPromotionBars(ctx, []byte(c.InputCSV))
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(c.InputCSV))
	candidate, incumbent, _, err := paperPromotionInputs(ctx, q, c.CandidateResultSHA256, c.IncumbentResultSHA256, c.StartingCash, c.ExecutionPolicySHA256)
	if err != nil {
		return err
	}
	warmup := int(max(candidate.SlowWindow, incumbent.SlowWindow))
	if symbol != c.Symbol || hex.EncodeToString(hash[:]) != c.InputDataSHA256 || len(bars) != warmup+90 || bars[warmup].At != c.ComparisonStart || bars[len(bars)-1].At != c.ComparisonEnd {
		return errors.New("paper promotion input binding is invalid")
	}
	claimedAt, _ := parsePaperTime(c.RecordedAt)
	end, _ := paperResearchWholeSecond(c.ComparisonEnd)
	if end.After(claimedAt) {
		return errors.New("paper promotion input was not available at claim time")
	}
	var selected string
	var selectedAt string
	if err := q.QueryRowContext(ctx, `SELECT selected_result_sha256,recorded_at FROM strategy_selection_events WHERE event_id=?`, c.ExpectedCurrentEventID).Scan(&selected, &selectedAt); err != nil {
		return err
	}
	selectionAt, ok := canonicalUTCTime(selectedAt)
	if !ok || selected != c.IncumbentResultSHA256 || selectionAt.After(claimedAt) {
		return errors.New("paper promotion incumbent binding is invalid")
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM strategy_research_evidence WHERE result_sha256 IN (?,?) AND sequence<=? AND julianday(recorded_at)<=julianday(?)`, c.CandidateResultSHA256, c.IncumbentResultSHA256, c.ResearchSequenceCutoff, c.RecordedAt).Scan(&count); err != nil {
		return err
	}
	if count != 2 {
		return errors.New("paper promotion candidates were not registered at the claim cutoff")
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM strategy_research_evidence WHERE sequence=?`, c.ResearchSequenceCutoff).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("paper promotion research cutoff is missing")
	}
	if c.OrderSequenceCutoff != 0 {
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_events WHERE sequence=?`, c.OrderSequenceCutoff).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("paper promotion order cutoff is missing")
		}
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_events e JOIN order_idempotency o ON o.order_id=e.order_id JOIN paper_market_bar_observations b ON b.observation_id=json_extract(e.event_json,'$.paper_bar_observation_id') WHERE e.sequence<=? AND o.account_ref=? AND julianday(b.close_at)>julianday(?)`, c.OrderSequenceCutoff, c.AccountRef, c.RecordedAt).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("paper promotion accounting cutoff excludes a recorded fill")
	}
	session, found, err := loadPaperAccountingSession(ctx, q, c.AccountRef)
	if err != nil {
		return err
	}
	if !found || session.SessionID != c.SessionID || session.ExecutionPolicySHA256 != c.ExecutionPolicySHA256 {
		return errors.New("paper promotion accounting session binding is invalid")
	}
	sessionAt, ok := parsePaperTime(session.RecordedAt)
	if !ok || sessionAt.After(claimedAt) {
		return errors.New("paper promotion predates its account session")
	}
	accounts, err := replayPaperAccountingWithoutRegistry(ctx, q, &paperAccountingCutoff{OrderSequence: c.OrderSequenceCutoff, AsOf: c.RecordedAt})
	if err != nil {
		return err
	}
	account := accounts[c.AccountRef]
	if account.Cash != c.StartingCash {
		return errors.New("paper promotion starting cash does not match the account prefix")
	}
	for _, lots := range account.Lots {
		if len(lots) != 0 {
			return errors.New("paper promotion account prefix is not flat")
		}
	}
	return validatePaperPromotionFreshRange(ctx, q, c, sequence)
}

func validateStoredPaperPromotionDecision(ctx context.Context, q orderQuerier, c paperPromotionClaim, claimSHA string, d paperPromotionDecision) error {
	at, ok := parsePaperTime(d.RecordedAt)
	claimedAt, _ := parsePaperTime(c.RecordedAt)
	if !ok || !canonicalPaperTimes(d.RecordedAt) || at.Before(claimedAt) || d.ClaimSHA256 != claimSHA || d.CandidateResultSHA256 != c.CandidateResultSHA256 || d.ExpectedCurrentEventID != c.ExpectedCurrentEventID {
		return errors.New("paper promotion decision claim binding is invalid")
	}
	if d.Decision == "ABORTED" {
		if d.Reason != "explicit_abort" || d.Comparison != nil || d.SelectionEventID != "" {
			return errors.New("paper promotion abort is invalid")
		}
		return nil
	}
	comparison, scoreErr := scorePaperPromotionClaim(ctx, q, c)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if d.Decision == "ERROR" {
		// Cancellation is an operational failure; it does not permit a selection or reuse.
		if d.Reason != "comparison_failed" || d.Comparison != nil || d.SelectionEventID != "" {
			return errors.New("paper promotion failure is invalid")
		}
		return nil
	}
	if scoreErr != nil || d.Comparison == nil || !reflect.DeepEqual(comparison, *d.Comparison) {
		return errors.New("paper promotion comparison cannot be reproduced")
	}
	if d.Decision == "STALE" {
		if d.Reason != "account_or_selection_changed" || d.SelectionEventID != "" {
			return errors.New("paper promotion stale decision is invalid")
		}
		return nil
	}
	if d.Decision != comparison.Decision || d.Reason != comparison.Reason {
		return errors.New("paper promotion decision differs from its recomputed policy")
	}
	if d.Decision == "HOLD" {
		if d.SelectionEventID != "" {
			return errors.New("held paper comparison changed selection")
		}
		return nil
	}
	if d.Decision != "PROMOTE" || !safeOrderID(d.SelectionEventID) {
		return errors.New("paper promotion decision is unsupported")
	}
	var raw, sha string
	if err := q.QueryRowContext(ctx, `SELECT record_json,record_sha256 FROM strategy_selection_events WHERE event_id=? AND reason_code='auto_comparison' AND paper_promotion_attempt_id=?`, d.SelectionEventID, c.AttemptID).Scan(&raw, &sha); err != nil {
		return err
	}
	var event StrategySelectionEvent
	if json.Unmarshal([]byte(raw), &event) != nil {
		return errors.New("automatic selection is invalid")
	}
	canonical, actualSHA, err := orderJSONHash(event)
	if err != nil || string(canonical) != raw || actualSHA != sha || event.EventID != d.SelectionEventID || event.EventType != "SELECT" || event.CandidateResultSHA256 != c.CandidateResultSHA256 || event.ExpectedCurrentEventID != c.ExpectedCurrentEventID || event.PreviousSelectedResultSHA256 != c.IncumbentResultSHA256 || event.SelectedResultSHA256 != c.CandidateResultSHA256 || event.PaperPromotionAttemptID != c.AttemptID || event.RecordedAt != d.RecordedAt {
		return errors.New("automatic selection comparison link is invalid")
	}
	return nil
}

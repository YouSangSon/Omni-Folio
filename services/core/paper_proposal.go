package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"omni-folio/services/core/internal/strategydomain"
)

// The proposal is an untrusted target, not a replacement research artifact.
// This entry point never arms execution, creates a session, or invents a bar.
func (s *Service) admitPaperProposal(ctx context.Context, accountRef, selectionEventID string, raw []byte, barID string, fencingToken int64) (*PaperSignalEvent, *OrderState, error) {
	return s.processPaperProposal(ctx, accountRef, selectionEventID, raw, barID, fencingToken, true, nil)
}

// Read-only preflight grants no authority; admission repeats all checks in its
// write transaction and additionally requires the current execution lease.
func (s *Service) processPaperProposal(ctx context.Context, accountRef, selectionEventID string, raw []byte, barID string, fencingToken int64, admit bool, claim *paperRunnerClaim) (*PaperSignalEvent, *OrderState, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") || !safeOrderID(selectionEventID) || !safeOrderID(barID) || (admit && fencingToken <= 0) {
		return nil, nil, errors.New("paper proposal admission is not configured")
	}
	proposal, err := decodePaperProposal(raw)
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if claim != nil {
		if err := validatePaperRunnerLeaseTx(ctx, tx, claim, accountRef, s.paperRunnerOwner, s.now()); err != nil {
			return nil, nil, err
		}
	}
	if _, err := provePaperAccountingRecovery(ctx, tx); err != nil {
		return nil, nil, err
	}
	resultSHA := stringField(proposal, "strategy_result_sha256")
	policy, err := loadCurrentStrategyExecutionPolicy(ctx, tx, resultSHA, selectionEventID)
	if err != nil {
		return nil, nil, err
	}
	var artifact string
	if err := tx.QueryRowContext(ctx, `SELECT artifact_json FROM strategy_research_evidence WHERE result_sha256=?`, resultSHA).Scan(&artifact); err != nil {
		return nil, nil, err
	}
	evidence, err := decodeStrategyArtifact([]byte(artifact))
	if err != nil {
		return nil, nil, err
	}
	if evidence.StrategyVersion != "1.0.0" || evidence.ParameterSHA256 != stringField(proposal, "strategy_parameter_sha256") || evidence.Target != "paper_candidate" {
		return nil, nil, errors.New("paper proposal strategy binding is invalid")
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(artifact))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, nil, err
	}
	manifest, _ := mapField(root, "manifest")
	strategy, _ := mapField(manifest, "strategy")
	parameters, _ := mapField(strategy, "parameters")
	quantity := stringField(parameters, "quantity")
	fast, fastOK := parameters["fast_window"].(json.Number)
	slow, slowOK := parameters["slow_window"].(json.Number)
	fastN, fastErr := fast.Int64()
	slowN, slowErr := slow.Int64()
	if !exactKeys(parameters, "quantity", "fast_window", "slow_window") || !fastOK || !slowOK || fastErr != nil || slowErr != nil || fastN <= 0 || slowN <= fastN || slowN > 365 ||
		!validPaperTargetQuantity(quantity) || quantity == "0" || (stringField(proposal, "signal") == "golden_cross" && stringField(proposal, "target_quantity") != quantity) {
		return nil, nil, errors.New("paper proposal target is not the registered strategy target")
	}
	session, found, err := loadPaperAccountingSession(ctx, tx, accountRef)
	if err != nil {
		return nil, nil, err
	}
	if !found || session.ExecutionPolicySHA256 != policy.SHA256 {
		return nil, nil, errors.New("paper proposal session does not match")
	}
	if admit {
		if _, err := s.requireCurrentSyntheticExecutionLease(ctx, tx, accountRef, fencingToken, s.now().UTC()); err != nil {
			return nil, nil, err
		}
	}
	bar, _, err := loadPaperMarketBarByID(ctx, tx, barID)
	if err != nil {
		return nil, nil, err
	}
	asOf, _ := canonicalPaperTime(stringField(proposal, "data_as_of"))
	if bar.Symbol != stringField(proposal, "symbol") || bar.InputDataSHA256 != stringField(proposal, "input_sha256") || bar.CloseAt != asOf {
		return nil, nil, errors.New("paper proposal does not match its stored bar")
	}
	recordedAt, _ := parsePaperTime(bar.RecordedAt)
	signal := PaperSignal{
		SchemaVersion: capitalizedPaperSignalSchema, SignalID: paperEventID("proposal", selectionEventID, stringField(proposal, "proposal_sha256")),
		SignalBarObservationID: barID, StrategyResultSHA256: resultSHA, StrategySelectionEventID: selectionEventID,
		DataSHA256: bar.InputDataSHA256, Symbol: bar.Symbol, TargetQuantity: stringField(proposal, "target_quantity"),
		DataAsOf: bar.CloseAt, GeneratedAt: bar.RecordedAt, ExpiresAt: recordedAt.Add(30 * time.Second).Format(canonicalPaperTimeLayout),
	}
	existing, found, err := loadPaperSignalEvent(ctx, tx, accountRef, signal.SignalID)
	if err != nil {
		return nil, nil, err
	}
	if found {
		if !samePaperSignalInput(*existing, signal) {
			return nil, nil, errors.New("paper proposal conflicts with its committed signal")
		}
		if _, hasOrder, err := paperOrderBySignalFrom(ctx, tx, accountRef, signal.SignalID); err != nil {
			return nil, nil, err
		} else if !hasOrder {
			// A committed no-delta decision must never acquire a new order later.
			return existing, nil, nil
		}
		if !admit {
			return existing, nil, nil
		}
		return s.admitPaperSignalTx(ctx, tx, accountRef, signal, fencingToken)
	}
	rows, err := tx.QueryContext(ctx, `SELECT observation_id,close_at,close FROM paper_market_bar_observations
		WHERE source=? AND symbol=? AND venue=? AND interval=? AND timezone=? AND price_adjustment=?
		ORDER BY close_at DESC,sequence DESC LIMIT ?`, bar.Source, bar.Symbol, bar.Venue, bar.Interval, bar.Timezone, bar.PriceAdjustment, slowN+2)
	if err != nil {
		return nil, nil, err
	}
	var closes []string
	var previous string
	for rows.Next() {
		var id, at, close string
		if err := rows.Scan(&id, &at, &close); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if at == previous || (len(closes) == 0 && id != barID) {
			rows.Close()
			return nil, nil, errors.New("paper proposal history is ambiguous or superseded")
		}
		previous = at
		closes = append(closes, close)
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return nil, nil, rowErr
	}
	if len(closes) > int(slowN+1) {
		closes = closes[:slowN+1]
	}
	for left, right := 0, len(closes)-1; left < right; left, right = left+1, right-1 {
		closes[left], closes[right] = closes[right], closes[left]
	}
	if err := strategydomain.VerifySMACrossover(closes, fastN, slowN, stringField(proposal, "signal")); err != nil {
		return nil, nil, err
	}
	if stringField(proposal, "signal") == "none" {
		// No signal is not a zero target: validate timing but append no command.
		signal.TargetQuantity = "0"
		if err := validateCapitalizedPaperSignal(signal, s.now().UTC()); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	if err := validateCapitalizedPaperSignal(signal, s.now().UTC()); err != nil {
		return nil, nil, err
	}
	if !admit {
		return nil, nil, nil
	}
	event, state, err := s.admitPaperSignalTx(ctx, tx, accountRef, signal, fencingToken)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return event, state, nil
}

func decodePaperProposal(raw []byte) (map[string]any, error) {
	invalid := errors.New("paper proposal contract is invalid")
	if len(raw) == 0 || len(raw) > maxBodyBytes {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, invalid
	}
	p := make(map[string]any)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, invalid
		}
		name, ok := key.(string)
		if _, exists := p[name]; !ok || exists {
			return nil, invalid
		}
		var value any
		if err := d.Decode(&value); err != nil {
			return nil, invalid
		}
		if _, ok := value.(string); !ok && !(name == "target_quantity" && value == nil) {
			return nil, invalid
		}
		p[name] = value
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return nil, invalid
	}
	if err := ensureJSONEOF(d); err != nil {
		return nil, invalid
	}
	if !exactKeys(p, "schema_version", "mode", "strategy_result_sha256", "strategy_parameter_sha256", "input_sha256", "symbol", "data_as_of", "signal", "target_quantity", "proposal_sha256") ||
		stringField(p, "schema_version") != "paper-signal-proposal.v1" || stringField(p, "mode") != "paper_proposal_only" || !kiwoomStockPattern.MatchString(stringField(p, "symbol")) {
		return nil, invalid
	}
	for _, field := range []string{"strategy_result_sha256", "strategy_parameter_sha256", "input_sha256", "proposal_sha256"} {
		if !strategySHA256Pattern.MatchString(stringField(p, field)) {
			return nil, invalid
		}
	}
	at := stringField(p, "data_as_of")
	if parsed, err := time.Parse(time.RFC3339, at); err != nil || parsed.UTC().Format(time.RFC3339) != at {
		return nil, invalid
	}
	target := stringField(p, "target_quantity")
	switch stringField(p, "signal") {
	case "golden_cross":
		if len(target) > 64 || !orderIntegerPattern.MatchString(target) {
			return nil, invalid
		}
	case "death_cross":
		if target != "0" {
			return nil, invalid
		}
	case "none":
		if p["target_quantity"] != nil {
			return nil, invalid
		}
	default:
		return nil, invalid
	}
	claimed := stringField(p, "proposal_sha256")
	delete(p, "proposal_sha256")
	body, err := strategyCanonicalJSON(p)
	if err != nil {
		return nil, invalid
	}
	hash := sha256.Sum256(body)
	if hex.EncodeToString(hash[:]) != claimed {
		return nil, invalid
	}
	p["proposal_sha256"] = claimed
	return p, nil
}

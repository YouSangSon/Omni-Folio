package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"
)

type PaperInputStatus struct {
	Mode                    string  `json:"mode"`
	ObservedAt              string  `json:"observed_at"`
	ProposalSHA256          string  `json:"proposal_sha256"`
	InputSHA256             string  `json:"input_sha256"`
	DataAsOf                string  `json:"data_as_of"`
	Signal                  string  `json:"signal"`
	CurrentSelectionMatches bool    `json:"current_selection_matches"`
	BarState                string  `json:"bar_state"`
	BarRecordedAt           *string `json:"bar_recorded_at"`
	ProposalDeadline        *string `json:"proposal_deadline"`
	SignalRecorded          bool    `json:"signal_recorded"`
	OrderStatus             *string `json:"order_status"`
	Completion              string  `json:"completion"`
	ExecutionAuthorized     bool    `json:"execution_authorized"`
	Freshness               string  `json:"freshness"`
}

func runPaperInputStatusCommand(ctx context.Context, args []string, output io.Writer) error {
	if ctx == nil {
		return errors.New("paper input status context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("paper-input-status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "existing SQLite database")
	account := fs.String("account", "", "paper account reference")
	selection := fs.String("selection-event", "", "exact historical or current selection event")
	bundle := fs.String("bundle", "", "saved paper input bundle")
	if err := fs.Parse(args); err != nil {
		return errors.New("paper-input-status options are invalid")
	}
	if fs.NArg() != 0 || *dbPath == "" || *account == "" || *selection == "" || *bundle == "" {
		return errors.New("paper-input-status requires -db, -account, -selection-event and -bundle")
	}
	invalid := errors.New("paper input status could not be verified; input and database are unchanged")
	raw, err := readBoundedRegularFile(*bundle, maxPaperBundleBytes)
	if err != nil {
		return invalid
	}
	db, err := openReadOnlyExistingDB(ctx, *dbPath)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	defer db.Close()
	result, err := newService(db, time.Now, randomID).paperInputStatus(ctx, *account, *selection, raw)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	return json.NewEncoder(output).Encode(result)
}

// These are stored facts, never an ACK, replay decision or permission to arm.
func (s *Service) paperInputStatus(ctx context.Context, account, selection string, raw []byte) (*PaperInputStatus, error) {
	if s == nil || s.db == nil || s.now == nil || ctx == nil || !orderAlias(account, "account") || !safeOrderID(selection) {
		return nil, errors.New("paper input status identity is invalid")
	}
	proposalRaw, barsRaw, researchRaw, err := decodePaperInputBundle(raw)
	if err != nil {
		return nil, err
	}
	proposal, err := decodePaperProposal(proposalRaw)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := provePaperRunnerLeaseRecovery(ctx, tx); err != nil {
		return nil, err
	}
	registry, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	var selected, artifact string
	if err := tx.QueryRowContext(ctx, `SELECT selected_result_sha256 FROM strategy_selection_events WHERE event_id=?`, selection).Scan(&selected); err != nil {
		return nil, err
	}
	if selected != stringField(proposal, "strategy_result_sha256") {
		return nil, errors.New("paper input selection differs")
	}
	if err := tx.QueryRowContext(ctx, `SELECT artifact_json FROM strategy_research_evidence WHERE result_sha256=?`, selected).Scan(&artifact); err != nil {
		return nil, err
	}
	evidence, err := decodeStrategyArtifact([]byte(artifact))
	if err != nil {
		return nil, err
	}
	if evidence.Target != "paper_candidate" || evidence.ParameterSHA256 != stringField(proposal, "strategy_parameter_sha256") {
		return nil, errors.New("paper input research binding differs")
	}
	session, found, err := loadPaperAccountingSession(ctx, tx, account)
	if err != nil || !found || session.ExecutionPolicySHA256 != evidence.executionPolicy.SHA256 {
		return nil, errors.New("paper input session binding differs")
	}
	var research map[string]any
	if err := json.Unmarshal([]byte(artifact), &research); err != nil {
		return nil, err
	}
	if err := validatePaperResearchInput(researchRaw, stringField(research, "input_sha256"), stringField(proposal, "symbol"), stringField(proposal, "data_as_of")); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	bars, inputSHA, err := parsePaperSnapshot(barsRaw, func() time.Time { return now })
	if err != nil {
		return nil, err
	}
	last := bars[len(bars)-1]
	asOf, ok := canonicalPaperTime(stringField(proposal, "data_as_of"))
	if !ok || inputSHA != stringField(proposal, "input_sha256") || last.Symbol != stringField(proposal, "symbol") || last.CloseAt != asOf {
		return nil, errors.New("paper input CSV binding differs")
	}
	stored, _, err := loadPaperSnapshotRange(ctx, tx, bars[0], last)
	if err != nil {
		return nil, err
	}
	provided := make(map[string]bool, len(bars))
	for _, bar := range bars {
		provided[bar.CloseAt] = true
		matches := stored[bar.CloseAt]
		if len(matches) > 1 || (len(matches) == 1 && !samePaperSnapshotBar(matches[0], bar)) {
			return nil, errors.New("paper input conflicts with stored bars")
		}
	}
	for closeAt := range stored {
		if !provided[closeAt] {
			return nil, errors.New("paper input omits stored history")
		}
	}
	result := &PaperInputStatus{
		Mode: "paper_fixture_only", ObservedAt: now.Format(time.RFC3339Nano),
		ProposalSHA256: stringField(proposal, "proposal_sha256"), InputSHA256: inputSHA,
		DataAsOf: stringField(proposal, "data_as_of"), Signal: stringField(proposal, "signal"),
		CurrentSelectionMatches: registry.CurrentEventID == selection,
		BarState:                "not_observed_in_this_database", Completion: "unknown", Freshness: "unverified",
	}
	if len(stored[last.CloseAt]) == 0 {
		return result, nil
	}
	anchor := stored[last.CloseAt][0]
	if anchor.InputDataSHA256 != inputSHA {
		return nil, errors.New("paper input differs from its first receipt")
	}
	recorded, valid := parsePaperTime(anchor.RecordedAt)
	if !valid {
		return nil, errors.New("paper input first receipt time is invalid")
	}
	deadline := recorded.Add(30 * time.Second).Format(canonicalPaperTimeLayout)
	result.BarState, result.BarRecordedAt, result.ProposalDeadline = "matched", &anchor.RecordedAt, &deadline
	signalID := paperEventID("proposal", selection, result.ProposalSHA256)
	signal, found, err := loadPaperSignalEvent(ctx, tx, account, signalID)
	if err != nil {
		return nil, err
	}
	if found {
		expected := PaperSignal{
			SchemaVersion: capitalizedPaperSignalSchema, SignalID: signalID,
			SignalBarObservationID: anchor.ObservationID, StrategyResultSHA256: selected,
			StrategySelectionEventID: selection, DataSHA256: inputSHA, Symbol: last.Symbol,
			TargetQuantity: stringField(proposal, "target_quantity"), DataAsOf: asOf,
			GeneratedAt: anchor.RecordedAt, ExpiresAt: deadline,
		}
		if result.Signal == "none" || !samePaperSignalInput(*signal, expected) {
			return nil, errors.New("paper input signal binding differs")
		}
		result.SignalRecorded = true
	}
	orderID, hasOrder, err := paperOrderBySignalFrom(ctx, tx, account, signalID)
	if err != nil {
		return nil, err
	}
	if hasOrder && !found {
		return nil, errors.New("paper input order lacks its signal")
	}
	if hasOrder {
		order, err := loadOrderStateFrom(ctx, tx, orderID)
		if err != nil {
			return nil, err
		}
		result.OrderStatus = &order.Status
	}
	return result, nil
}

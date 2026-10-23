package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type ResearchReportView struct {
	SchemaVersion       string                `json:"schema_version"`
	Mode                string                `json:"mode"`
	MetricsVerification string                `json:"metrics_verification"`
	ObservedAt          string                `json:"observed_at"`
	Report              *StoredResearchReport `json:"report"`
}

type StoredResearchReport struct {
	RecordedAt      string                `json:"recorded_at"`
	FastWindow      int64                 `json:"fast_window"`
	SlowWindow      int64                 `json:"slow_window"`
	Quantity        string                `json:"quantity"`
	CandidateCount  int                   `json:"candidate_count"`
	FoldCount       int                   `json:"fold_count"`
	FinalHoldout    ResearchReportRange   `json:"final_holdout"`
	PromotionTarget string                `json:"promotion_target"`
	FailedGates     []string              `json:"failed_gates"`
	WalkForward     ResearchReportMetrics `json:"walk_forward"`
	Holdout         ResearchReportMetrics `json:"holdout"`
	BuyAndHold      ResearchReportMetrics `json:"buy_and_hold"`
}

type ResearchReportRange struct {
	FirstBarAt string `json:"first_bar_at"`
	LastBarAt  string `json:"last_bar_at"`
	BarCount   int64  `json:"bar_count"`
}

type ResearchReportMetrics struct {
	AfterCostReturn string `json:"after_cost_return"`
	MaxDrawdown     string `json:"max_drawdown"`
	RoundTrips      string `json:"round_trips"`
}

func (s *Service) handleResearchReport(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "research report accepts no query parameters"}})
		return
	}
	result, err := s.researchReport(r.Context())
	if err != nil {
		writeError(w, internalError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) researchReport(ctx context.Context) (*ResearchReportView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// SQLite uses BEGIN IMMEDIATE here: this read snapshot still takes the writer lock.
	view := &ResearchReportView{SchemaVersion: "research-report.v1", Mode: "research_artifact_only", MetricsVerification: "reported_not_recomputed", ObservedAt: s.now().UTC().Format(time.RFC3339Nano)}
	if _, err := replayStrategyRegistry(ctx, tx); err != nil {
		return nil, err
	}
	var artifact, recordedAt string
	err = tx.QueryRowContext(ctx, `SELECT artifact_json,recorded_at FROM strategy_research_evidence ORDER BY sequence DESC LIMIT 1`).Scan(&artifact, &recordedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// The captured bytes remain the same snapshot; release the lock before projecting them.
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if artifact != "" {
		view.Report, err = projectResearchReport(artifact, recordedAt)
		if err != nil {
			return nil, err
		}
	}
	return view, nil
}

func projectResearchReport(artifact, recordedAt string) (*StoredResearchReport, error) {
	invalid := errors.New("unsupported research report")
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(artifact))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, invalid
	}
	manifest, _ := mapField(result, "manifest")
	strategy, _ := mapField(manifest, "strategy")
	parameters, _ := mapField(strategy, "parameters")
	challenger, _ := mapField(result, "challenger")
	selected, _ := mapField(challenger, "parameters")
	evaluation, _ := mapField(result, "evaluation")
	promotion, _ := mapField(result, "promotion")
	baseline, _ := mapField(promotion, "baseline")
	fast, fastOK := researchReportInteger(parameters["fast_window"])
	slow, slowOK := researchReportInteger(parameters["slow_window"])
	quantity, quantityOK := researchReportDecimal(parameters["quantity"], 1)
	// Reader support is intentionally narrower than historical registry admission.
	if stringField(strategy, "version") != "1.0.0" || !exactKeys(parameters, "fast_window", "slow_window", "quantity") ||
		!exactKeys(selected, "fast_window", "slow_window") || !fastOK || !slowOK || fast >= slow || !quantityOK ||
		selected["fast_window"] != parameters["fast_window"] || selected["slow_window"] != parameters["slow_window"] ||
		stringField(baseline, "name") != "buy_and_hold" {
		return nil, invalid
	}
	folds, _ := evaluation["folds"].([]any)
	candidates, _ := result["candidates"].([]any)
	foldJSON, err := strategyCanonicalJSON(folds)
	if err != nil {
		return nil, invalid
	}
	challengerFoldJSON, err := strategyCanonicalJSON(challenger["walk_forward_folds"])
	if err != nil || !bytes.Equal(foldJSON, challengerFoldJSON) {
		return nil, invalid
	}
	holdoutRange, _ := mapField(evaluation, "final_holdout")
	first, last := stringField(holdoutRange, "first_bar_at"), stringField(holdoutRange, "last_bar_at")
	barCount, countOK := researchReportInteger(holdoutRange["bar_count"])
	if !canonicalUTCString(first) || !canonicalUTCString(last) || !countOK {
		return nil, invalid
	}
	firstTime, _ := time.Parse(time.RFC3339Nano, first)
	lastTime, _ := time.Parse(time.RFC3339Nano, last)
	if firstTime.After(lastTime) || (barCount == 1) != firstTime.Equal(lastTime) {
		return nil, invalid
	}
	walkForward, walkOK := researchReportMetrics(challenger["walk_forward_metrics"], "sum_of_completed_long_only_round_trips_across_walk_forward_folds")
	holdout, holdOK := researchReportMetrics(challenger["final_holdout_metrics"], "completed_long_only_round_trips")
	buyAndHold, baselineOK := researchReportMetrics(baseline["final_holdout_metrics"], "completed_long_only_round_trips")
	if !walkOK || !holdOK || !baselineOK {
		return nil, invalid
	}
	failed := make([]string, 0)
	// Registry replay has already checked these fixed gate names and their order.
	for _, gate := range promotion["failed_gates"].([]any) {
		failed = append(failed, gate.(string))
	}
	return &StoredResearchReport{RecordedAt: recordedAt, FastWindow: fast, SlowWindow: slow, Quantity: quantity,
		CandidateCount: len(candidates), FoldCount: len(folds), FinalHoldout: ResearchReportRange{first, last, barCount},
		PromotionTarget: stringField(promotion, "target"), FailedGates: failed, WalkForward: walkForward, Holdout: holdout, BuyAndHold: buyAndHold}, nil
}

func researchReportMetrics(value any, countDefinition string) (ResearchReportMetrics, bool) {
	metrics, ok := value.(map[string]any)
	if !ok || stringField(metrics, "trade_count_definition") != countDefinition {
		return ResearchReportMetrics{}, false
	}
	returns, returnOK := researchReportDecimal(metrics["after_cost_return"], -1)
	drawdown, drawdownOK := researchReportDecimal(metrics["max_drawdown"], 0)
	trips, tripsOK := researchReportDecimal(metrics["trade_count"], 0)
	if !returnOK || !drawdownOK || !tripsOK || bytes.ContainsRune([]byte(trips), '.') {
		return ResearchReportMetrics{}, false
	}
	// Fixed fees can make cash negative: reported losses and drawdown may exceed 100%.
	return ResearchReportMetrics{returns, drawdown, trips}, true
}

func researchReportDecimal(value any, minimumSign int) (string, bool) {
	raw, ok := value.(string)
	if !ok || len(raw) > 128 {
		return "", false
	}
	parsed, err := parseDecimal(raw)
	return raw, err == nil && parsed.Sign() >= minimumSign
}

func researchReportInteger(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Int64()
	return parsed, err == nil && parsed > 0 && parsed <= 9007199254740991 && number.String() == strconv.FormatInt(parsed, 10)
}

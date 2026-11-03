package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"omni-folio/services/core/internal/researchdomain"
)

type PaperPromotionReportView struct {
	SchemaVersion string                      `json:"schema_version"`
	Mode          string                      `json:"mode"`
	ObservedAt    string                      `json:"observed_at"`
	Attempt       *StoredPaperPromotionReport `json:"attempt"`
}

type StoredPaperPromotionReport struct {
	Symbol                   string                          `json:"symbol"`
	ClaimedAt                string                          `json:"claimed_at"`
	DecidedAt                *string                         `json:"decided_at"`
	ComparisonStart          string                          `json:"comparison_start"`
	ComparisonEnd            string                          `json:"comparison_end"`
	Candidate                PaperPromotionReportStrategy    `json:"candidate"`
	Incumbent                PaperPromotionReportStrategy    `json:"incumbent"`
	Decision                 string                          `json:"decision"`
	ReasonCode               string                          `json:"reason_code"`
	PromotionSelectionStatus string                          `json:"promotion_selection_status"`
	Comparison               *PaperPromotionReportComparison `json:"comparison"`
}

type PaperPromotionReportStrategy struct {
	FastWindow int64  `json:"fast_window"`
	SlowWindow int64  `json:"slow_window"`
	Quantity   string `json:"quantity"`
}

type PaperPromotionReportComparison struct {
	PolicyVersion       string                       `json:"policy_version"`
	MetricsVerification string                       `json:"metrics_verification"`
	Decision            string                       `json:"decision"`
	ReasonCode          string                       `json:"reason_code"`
	Windows             []PaperPromotionReportWindow `json:"windows"`
}

type PaperPromotionReportWindow struct {
	Start      string                      `json:"start"`
	End        string                      `json:"end"`
	Candidate  PaperPromotionReportMetrics `json:"candidate"`
	Incumbent  PaperPromotionReportMetrics `json:"incumbent"`
	BuyAndHold PaperPromotionReportMetrics `json:"buy_and_hold"`
}

type PaperPromotionReportMetrics struct {
	AfterCostReturn string `json:"after_cost_return"`
	MaxDrawdown     string `json:"max_drawdown"`
	RoundTrips      string `json:"round_trips"`
	Turnover        string `json:"turnover"`
}

func (s *Service) handlePaperPromotionReport(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "paper promotion report accepts no query parameters"}})
		return
	}
	report, err := s.paperPromotionReport(r.Context())
	if err != nil {
		writeError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Service) paperPromotionReport(ctx context.Context) (*PaperPromotionReportView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// BEGIN IMMEDIATE pins this snapshot; SQLite still takes the writer lock.
	view := &PaperPromotionReportView{SchemaVersion: "paper-promotion-report.v1", Mode: "paper_comparison_only", ObservedAt: s.now().UTC().Format(time.RFC3339Nano)}
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	registry, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	var attemptID string
	err = tx.QueryRowContext(ctx, `SELECT attempt_id FROM paper_promotion_claims ORDER BY sequence DESC LIMIT 1`).Scan(&attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return view, nil
	}
	if err != nil {
		return nil, err
	}
	claim, decision, found, err := loadPaperPromotion(ctx, tx, attemptID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("paper promotion report claim disappeared")
	}
	candidate, incumbent, _, err := paperPromotionInputs(ctx, tx, claim.CandidateResultSHA256, claim.IncumbentResultSHA256, claim.StartingCash, claim.ExecutionPolicySHA256)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	view.Attempt, err = projectPaperPromotionReport(*claim, decision, candidate, incumbent, registry.CurrentEventID)
	if err != nil {
		return nil, err
	}
	return view, nil
}

func projectPaperPromotionReport(claim paperPromotionClaim, decision *paperPromotionDecision, candidate, incumbent researchdomain.Strategy, currentEventID string) (*StoredPaperPromotionReport, error) {
	claimedAt, ok := parsePaperTime(claim.RecordedAt)
	if !ok {
		return nil, errors.New("invalid paper comparison claim time")
	}
	report := &StoredPaperPromotionReport{
		Symbol: claim.Symbol, ClaimedAt: claimedAt.UTC().Format(time.RFC3339Nano),
		ComparisonStart: claim.ComparisonStart, ComparisonEnd: claim.ComparisonEnd,
		Candidate: PaperPromotionReportStrategy{FastWindow: candidate.FastWindow, SlowWindow: candidate.SlowWindow, Quantity: candidate.Quantity},
		Incumbent: PaperPromotionReportStrategy{FastWindow: incumbent.FastWindow, SlowWindow: incumbent.SlowWindow, Quantity: incumbent.Quantity},
		Decision:  "PENDING", ReasonCode: "comparison_claimed", PromotionSelectionStatus: "not_promoted",
	}
	if decision == nil {
		return report, nil
	}
	decidedAt, ok := parsePaperTime(decision.RecordedAt)
	if !ok {
		return nil, errors.New("invalid paper comparison decision time")
	}
	publicTime := decidedAt.UTC().Format(time.RFC3339Nano)
	report.DecidedAt = &publicTime
	report.Decision, report.ReasonCode = decision.Decision, decision.Reason
	if decision.Decision == "PROMOTE" {
		report.PromotionSelectionStatus = "superseded"
		if decision.SelectionEventID == currentEventID {
			report.PromotionSelectionStatus = "current"
		}
	}
	if decision.Comparison == nil {
		return report, nil
	}
	comparison := decision.Comparison
	report.Comparison = &PaperPromotionReportComparison{
		PolicyVersion: comparison.PolicyVersion, MetricsVerification: "recomputed_from_stored_input",
		Decision: comparison.Decision, ReasonCode: comparison.Reason,
		Windows: make([]PaperPromotionReportWindow, 0, len(comparison.Windows)),
	}
	for _, window := range comparison.Windows {
		candidate, err := projectPaperPromotionReportMetrics(window.Candidate)
		if err != nil {
			return nil, err
		}
		incumbent, err := projectPaperPromotionReportMetrics(window.Incumbent)
		if err != nil {
			return nil, err
		}
		baseline, err := projectPaperPromotionReportMetrics(window.BuyAndHold)
		if err != nil {
			return nil, err
		}
		report.Comparison.Windows = append(report.Comparison.Windows, PaperPromotionReportWindow{Start: window.Start, End: window.End, Candidate: candidate, Incumbent: incumbent, BuyAndHold: baseline})
	}
	return report, nil
}

func projectPaperPromotionReportMetrics(value researchdomain.Metrics) (PaperPromotionReportMetrics, error) {
	returns, returnOK := researchReportDecimal(value.AfterCostReturn, -1)
	drawdown, drawdownOK := researchReportDecimal(value.MaxDrawdown, 0)
	trips, tripsOK := researchReportDecimal(value.TradeCount, 0)
	turnover, turnoverOK := researchReportDecimal(value.Turnover, 0)
	if !returnOK || !drawdownOK || !tripsOK || !turnoverOK || strings.Contains(trips, ".") || value.TradeCountDefinition != "completed_long_only_round_trips" {
		return PaperPromotionReportMetrics{}, errors.New("unsupported public paper comparison metrics")
	}
	return PaperPromotionReportMetrics{AfterCostReturn: returns, MaxDrawdown: drawdown, RoundTrips: trips, Turnover: turnover}, nil
}

package main

import (
	"context"
	"database/sql"
	"errors"
)

type PaperImprovementReceipt struct {
	SchemaVersion   string                 `json:"schema_version"`
	ResultSHA256    string                 `json:"result_sha256"`
	Registered      bool                   `json:"registered"`
	AttemptRecorded bool                   `json:"attempt_recorded"`
	Disposition     string                 `json:"disposition"`
	Comparison      *PaperPromotionReceipt `json:"comparison"`
}

func (s *Service) improvePaperCandidate(ctx context.Context, account, attemptID string, artifact, raw []byte) (*PaperImprovementReceipt, error) {
	if ctx == nil {
		return nil, errors.New("paper improvement context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil || !orderAlias(account, "account") || !safeOrderID(attemptID) {
		return nil, errors.New("paper improvement identifiers are invalid")
	}
	evidence, err := decodeStrategyArtifact(artifact)
	if err != nil {
		return nil, err
	}
	if evidence.Target == "paper_candidate" {
		if _, err := paperPromotionStrategy(*evidence); err != nil {
			return nil, err
		}
	}
	if _, _, err := parsePaperPromotionBars(ctx, raw); err != nil {
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
	selection, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	claim, _, found, err := loadPaperPromotion(ctx, tx, attemptID)
	if err != nil {
		return nil, err
	}
	expectedSelection := selection.CurrentEventID
	if found {
		if claim.AccountRef != account || claim.CandidateResultSHA256 != evidence.ResultSHA256 || claim.InputCSV != string(raw) {
			return nil, errors.New("paper improvement attempt conflicts with its saved request")
		}
		expectedSelection = claim.ExpectedCurrentEventID
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Registration is independently durable; only a claim binds the attempt ID.
	if _, err := s.registerStrategyEvidence(ctx, artifact); err != nil {
		return nil, err
	}
	receipt := &PaperImprovementReceipt{SchemaVersion: "paper-improvement.v1", ResultSHA256: evidence.ResultSHA256, Registered: true, Disposition: "comparison"}
	if !found {
		if evidence.Target == "no_promotion" {
			receipt.Disposition = "research_not_candidate"
			return receipt, nil
		}
		if selection.SelectedResultSHA256 == evidence.ResultSHA256 {
			receipt.Disposition = "already_selected"
			return receipt, nil
		}
	}
	receipt.Comparison, err = s.promotePaperCandidate(ctx, account, evidence.ResultSHA256, expectedSelection, attemptID, raw)
	if err != nil {
		return nil, err
	}
	receipt.AttemptRecorded = true
	return receipt, nil
}

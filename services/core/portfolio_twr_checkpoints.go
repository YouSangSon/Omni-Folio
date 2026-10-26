package main

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
)

const portfolioTWRCheckpointsPolicy = "twr_cash_flow_checkpoints.v1"
const twrCheckpointRateLimit = 2500
const twrCheckpointResponseLimit = 3 * maxBodyBytes

type PortfolioTWRCheckpoint struct {
	At             string `json:"at"`
	Kind           string `json:"kind"`
	CumulativeRate string `json:"cumulative_rate"`
}

type PortfolioTWRCheckpoints struct {
	PolicyVersion string                   `json:"policy_version"`
	Report        *PortfolioTWR            `json:"report"`
	Points        []PortfolioTWRCheckpoint `json:"points"`
}

func twrCheckpointRate(factor *big.Rat) (string, error) {
	// An 8192-bit prefix may exceed the scalar final-rate limit before a later loss.
	rate, err := formatDecimal(quantizeHalfEven(new(big.Rat).Sub(factor, big.NewRat(1, 1)), 12))
	if err != nil || len(rate) > twrCheckpointRateLimit {
		return "", errors.New("TWR checkpoint rate exceeds its declared bound")
	}
	return rate, nil
}

func (s *Service) portfolioTWRCheckpoints(ctx context.Context) (*PortfolioTWRCheckpoints, *appError) {
	points := []PortfolioTWRCheckpoint{}
	report, appErr := s.portfolioTWRRead(ctx, &points)
	if appErr != nil {
		return nil, appErr
	}
	if report.Status != "calculated" {
		points = []PortfolioTWRCheckpoint{}
	}
	return &PortfolioTWRCheckpoints{PolicyVersion: portfolioTWRCheckpointsPolicy, Report: report, Points: points}, nil
}

func (s *Service) handlePortfolioTWRCheckpoints(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "TWR checkpoints do not accept query parameters"}})
		return
	}
	report, appErr := s.portfolioTWRCheckpoints(r.Context())
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded)+1 > twrCheckpointResponseLimit {
		writeError(w, internalError(errors.New("TWR checkpoint response exceeds its declared bound")))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

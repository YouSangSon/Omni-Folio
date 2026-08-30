package riskdomain

import (
	"errors"
	"math/big"

	"omni-folio/services/core/internal/exact"
)

const PaperPerformancePolicyVersion = "paper-strategy-performance-safety.v1"

type PaperPerformanceInput struct {
	SampleCount      int
	CumulativeReturn string
	MaxDrawdown      string
}

type PaperPerformanceDecision struct {
	Decision   string
	ReasonCode string
}

func EvaluatePaperPerformancePolicy(input PaperPerformanceInput) (PaperPerformanceDecision, error) {
	cumulativeReturn, err := exact.ParseDecimal(input.CumulativeReturn)
	if err != nil {
		return PaperPerformanceDecision{}, err
	}
	maxDrawdown, err := exact.ParseDecimal(input.MaxDrawdown)
	if err != nil {
		return PaperPerformanceDecision{}, err
	}
	if input.SampleCount < 0 || maxDrawdown.Sign() < 0 {
		return PaperPerformanceDecision{}, errors.New("paper performance policy input is invalid")
	}
	if input.SampleCount < 2 {
		return PaperPerformanceDecision{Decision: "INSUFFICIENT", ReasonCode: "minimum_same_selection_samples_not_met"}, nil
	}
	if maxDrawdown.Cmp(big.NewRat(1, 10)) >= 0 {
		return PaperPerformanceDecision{Decision: "HALT_AND_ROLLBACK", ReasonCode: "max_drawdown_limit_reached"}, nil
	}
	if cumulativeReturn.Cmp(big.NewRat(-1, 20)) <= 0 {
		return PaperPerformanceDecision{Decision: "HALT_AND_ROLLBACK", ReasonCode: "cumulative_return_floor_reached"}, nil
	}
	return PaperPerformanceDecision{Decision: "HOLD", ReasonCode: "within_local_paper_safety_bounds"}, nil
}

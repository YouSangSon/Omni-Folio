package riskdomain

import "testing"

func TestEvaluatePaperPerformancePolicy(t *testing.T) {
	tests := []struct {
		name string
		in   PaperPerformanceInput
		want PaperPerformanceDecision
	}{
		{
			name: "one same selection point is insufficient before thresholds",
			in:   PaperPerformanceInput{SampleCount: 1, CumulativeReturn: "-0.2", MaxDrawdown: "0.2"},
			want: PaperPerformanceDecision{Decision: "INSUFFICIENT", ReasonCode: "minimum_same_selection_samples_not_met"},
		},
		{
			name: "two points strictly inside limits hold",
			in:   PaperPerformanceInput{SampleCount: 2, CumulativeReturn: "-0.04999999", MaxDrawdown: "0.09999999"},
			want: PaperPerformanceDecision{Decision: "HOLD", ReasonCode: "within_local_paper_safety_bounds"},
		},
		{
			name: "inclusive cumulative return floor halts",
			in:   PaperPerformanceInput{SampleCount: 2, CumulativeReturn: "-0.05", MaxDrawdown: "0.09999999"},
			want: PaperPerformanceDecision{Decision: "HALT_AND_ROLLBACK", ReasonCode: "cumulative_return_floor_reached"},
		},
		{
			name: "inclusive drawdown limit halts",
			in:   PaperPerformanceInput{SampleCount: 2, CumulativeReturn: "0.2", MaxDrawdown: "0.1"},
			want: PaperPerformanceDecision{Decision: "HALT_AND_ROLLBACK", ReasonCode: "max_drawdown_limit_reached"},
		},
		{
			name: "drawdown wins threshold tie",
			in:   PaperPerformanceInput{SampleCount: 2, CumulativeReturn: "-0.2", MaxDrawdown: "0.2"},
			want: PaperPerformanceDecision{Decision: "HALT_AND_ROLLBACK", ReasonCode: "max_drawdown_limit_reached"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := EvaluatePaperPerformancePolicy(test.in)
			if err != nil || got != test.want {
				t.Fatalf("EvaluatePaperPerformancePolicy(%+v) = %+v, %v; want %+v, nil", test.in, got, err, test.want)
			}
		})
	}
}

func TestEvaluatePaperPerformancePolicyRejectsInvalidInput(t *testing.T) {
	for _, in := range []PaperPerformanceInput{
		{SampleCount: -1, CumulativeReturn: "0", MaxDrawdown: "0"},
		{SampleCount: 1, CumulativeReturn: "0.0", MaxDrawdown: "0"},
		{SampleCount: 1, CumulativeReturn: "0", MaxDrawdown: "0.10"},
		{SampleCount: 1, CumulativeReturn: "0", MaxDrawdown: "-0.1"},
		{SampleCount: 2, CumulativeReturn: "-0", MaxDrawdown: "0"},
		{SampleCount: 2, CumulativeReturn: "1e-1", MaxDrawdown: "0"},
	} {
		if _, err := EvaluatePaperPerformancePolicy(in); err == nil {
			t.Fatalf("EvaluatePaperPerformancePolicy(%+v) accepted invalid input", in)
		}
	}
}

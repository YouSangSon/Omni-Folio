package main

import (
	"context"
	"errors"
	"math"
	"math/big"
	"sort"
)

const xirrMaxDates = 10000
const xirrSolverPolicy = "single_change_bracket_else_log_newton_0_1_v1"

type xirrCashFlow struct {
	day    int64
	amount *big.Rat
}

type xirrSolution struct {
	rate        *string
	candidate   *string
	signChanges int
	reason      string
}

func solvePortfolioXIRR(ctx context.Context, flows []xirrCashFlow) (xirrSolution, error) {
	result := xirrSolution{}
	byDay := map[int64]*big.Rat{}
	for _, flow := range flows {
		if flow.amount == nil {
			return result, errors.New("XIRR amount is missing")
		}
		if byDay[flow.day] == nil {
			byDay[flow.day] = new(big.Rat)
		}
		byDay[flow.day].Add(byDay[flow.day], flow.amount)
	}
	flows = nil
	for day, amount := range byDay {
		if amount.Sign() != 0 {
			flows = append(flows, xirrCashFlow{day, amount})
		}
	}
	if len(flows) > xirrMaxDates {
		result.reason = "cash_flow_limit"
		return result, nil
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].day < flows[j].day })
	if len(byDay) < 2 {
		result.reason = "insufficient_period"
		return result, nil
	}
	if len(flows) < 2 {
		result.reason = "one_sided_cash_flows"
		return result, nil
	}
	maxAmount := new(big.Rat)
	previousSign := flows[0].amount.Sign()
	for _, flow := range flows {
		if flow.amount.Sign() != previousSign {
			result.signChanges++
			previousSign = flow.amount.Sign()
		}
		if absolute := new(big.Rat).Abs(flow.amount); absolute.Cmp(maxAmount) > 0 {
			maxAmount.Set(absolute)
		}
	}
	if result.signChanges == 0 {
		result.reason = "one_sided_cash_flows"
		return result, nil
	}
	years := make([]float64, len(flows))
	logAmounts := make([]float64, len(flows))
	signs := make([]float64, len(flows))
	for i, flow := range flows {
		scaled, _ := new(big.Rat).Quo(new(big.Rat).Abs(flow.amount), maxAmount).Float64()
		if scaled == 0 || math.IsInf(scaled, 0) || math.IsNaN(scaled) {
			result.reason = "numerical_limit"
			return result, nil
		}
		years[i] = float64(flow.day-flows[0].day) / 365
		logAmounts[i] = math.Log(scaled)
		signs[i] = float64(flow.amount.Sign())
	}
	// Scale every evaluation by its largest term, including its cash amount.
	evaluate := func(x float64) (float64, float64, float64) {
		pivot := math.Inf(-1)
		for i := range years {
			pivot = math.Max(pivot, logAmounts[i]-years[i]*x)
		}
		value, derivative, absolute, compensation := 0.0, 0.0, 0.0, 0.0
		for i := range years {
			term := signs[i] * math.Exp(logAmounts[i]-years[i]*x-pivot)
			adjusted := term - compensation
			next := value + adjusted
			compensation = (next - value) - adjusted
			value = next
			derivative -= years[i] * term
			absolute += math.Abs(term)
		}
		return value, derivative, absolute
	}
	lower := math.Log1p(math.Nextafter(-1, 0))
	upper := math.Log(math.MaxFloat64) - 1
	x := math.Log1p(0.1)
	converged := false
	if result.signChanges == 1 {
		var err error
		x, converged, err = bracketedPortfolioXIRR(ctx, evaluate, lower, upper)
		if err != nil {
			return result, err
		}
	} else {
		// shortcut: Newton proposes a location; only a reliable local bracket certifies a candidate.
		for range 100 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			value, derivative, absolute := evaluate(x)
			if math.Abs(value) <= 1e-13*absolute {
				converged = true
				break
			}
			if derivative == 0 || math.IsNaN(derivative) || math.IsInf(derivative, 0) {
				break
			}
			next := x - value/derivative
			if next <= lower || next >= upper || math.IsNaN(next) || math.IsInf(next, 0) {
				break
			}
			x = next
		}
		if converged {
			radius := 1e-6 * (1 + math.Abs(x))
			var err error
			x, converged, err = bracketedPortfolioXIRR(ctx, evaluate, math.Max(lower, x-radius), math.Min(upper, x+radius))
			if err != nil {
				return result, err
			}
		}
	}
	rate := math.Expm1(x)
	if !converged || rate <= -1 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		result.reason = "numerical_limit"
		return result, nil
	}
	// Only the numerical rate is rounded; ledger and converted cash amounts remain exact.
	rounded := quantizeHalfEven(new(big.Rat).SetFloat64(rate), 12)
	if rounded.Cmp(big.NewRat(-1, 1)) <= 0 {
		result.reason = "numerical_limit"
		return result, nil
	}
	growth, _ := new(big.Rat).Add(big.NewRat(1, 1), rounded).Float64()
	if growth <= 0 || math.IsInf(growth, 0) || math.IsNaN(growth) {
		result.reason = "numerical_limit"
		return result, nil
	}
	residual, _, absolute := evaluate(math.Log(growth))
	if math.Abs(residual) > 1e-10*absolute {
		result.reason = "numerical_limit"
		return result, nil
	}
	text, err := formatDecimal(rounded)
	if err != nil {
		return result, err
	}
	if result.signChanges == 1 {
		result.rate = &text
	} else {
		result.candidate = &text
		result.reason = "uniqueness_not_proven"
	}
	return result, nil
}

func bracketedPortfolioXIRR(ctx context.Context, evaluate func(float64) (float64, float64, float64), lower, upper float64) (float64, bool, error) {
	left, _, leftAbs := evaluate(lower)
	right, _, rightAbs := evaluate(upper)
	if math.Abs(left) <= 1e-12*leftAbs || math.Abs(right) <= 1e-12*rightAbs || math.Signbit(left) == math.Signbit(right) {
		return 0, false, nil
	}
	for range 256 {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		x := lower + (upper-lower)/2
		value, _, absolute := evaluate(x)
		if math.Abs(value) <= 1e-13*absolute && upper-lower <= 1e-12*(1+math.Abs(x)) {
			return x, true, nil
		}
		if x == lower || x == upper {
			return x, math.Abs(value) <= 1e-12*absolute, nil
		}
		if math.Signbit(value) == math.Signbit(left) {
			lower = x
			left = value
		} else {
			upper = x
		}
	}
	return 0, false, nil
}

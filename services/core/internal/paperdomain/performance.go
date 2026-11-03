package paperdomain

import (
	"errors"
	"math/big"

	"omni-folio/services/core/internal/exact"
)

type PositionValuation struct {
	Quantity      string
	OpenCost      string
	MarketValue   string
	UnrealizedPnL string
}

type Valuation struct {
	Cash          string
	OpenCost      string
	MarketValue   string
	RealizedPnL   string
	UnrealizedPnL string
	TotalPnL      string
	Equity        string
	Positions     map[string]PositionValuation
}

type PerformancePoint struct {
	Equity            string
	PeakEquity        string
	PeriodReturnState string
	PeriodReturn      string
	CumulativeReturn  string
	Drawdown          string
	MaxDrawdown       string
}

func ValueAccount(startingCash string, state AccountState, closes map[string]string) (Valuation, error) {
	starting, err := exact.ParseDecimal(startingCash)
	if err != nil || starting.Sign() <= 0 {
		return Valuation{}, errors.New("paper valuation starting cash is invalid")
	}
	cash, err := exact.ParseDecimal(state.Cash)
	if err != nil || cash.Sign() < 0 {
		return Valuation{}, errors.New("paper valuation cash is invalid")
	}
	realized, err := exact.ParseDecimal(state.RealizedPnL)
	if err != nil {
		return Valuation{}, errors.New("paper valuation realized PnL is invalid")
	}
	for _, raw := range []string{state.Fees, state.Taxes, state.Slippage} {
		if raw == "" {
			continue
		}
		value, err := exact.ParseDecimal(raw)
		if err != nil || value.Sign() < 0 {
			return Valuation{}, errors.New("paper valuation account totals are invalid")
		}
	}

	quantities := make(map[string]*big.Rat, len(state.Lots))
	costs := make(map[string]*big.Rat, len(state.Lots))
	for symbol, lots := range state.Lots {
		quantity := new(big.Rat)
		cost := new(big.Rat)
		for _, lot := range lots {
			lotQuantity, err := exact.ParseDecimal(lot.Quantity)
			if err != nil || lotQuantity.Sign() <= 0 {
				return Valuation{}, errors.New("paper valuation lot quantity is invalid")
			}
			lotCost, err := exact.ParseDecimal(lot.Cost)
			if err != nil || lotCost.Sign() < 0 {
				return Valuation{}, errors.New("paper valuation lot cost is invalid")
			}
			quantity.Add(quantity, lotQuantity)
			cost.Add(cost, lotCost)
		}
		if quantity.Sign() > 0 {
			quantities[symbol] = quantity
			costs[symbol] = cost
		}
	}

	parsedCloses := make(map[string]*big.Rat, len(closes))
	for symbol, raw := range closes {
		close, err := exact.ParseDecimal(raw)
		if err != nil || close.Sign() <= 0 {
			return Valuation{}, errors.New("paper valuation close is invalid")
		}
		parsedCloses[symbol] = close
	}
	if len(parsedCloses) != len(quantities) {
		return Valuation{}, errors.New("paper valuation closes do not match positions")
	}
	for symbol := range quantities {
		if _, ok := parsedCloses[symbol]; !ok {
			return Valuation{}, errors.New("paper valuation close is missing")
		}
	}

	openCost := new(big.Rat)
	marketValue := new(big.Rat)
	positions := make(map[string]PositionValuation, len(quantities))
	for symbol, quantity := range quantities {
		cost := costs[symbol]
		market := new(big.Rat).Mul(quantity, parsedCloses[symbol])
		unrealized := new(big.Rat).Sub(market, cost)
		openCost.Add(openCost, cost)
		marketValue.Add(marketValue, market)

		quantityText, err := exact.FormatDecimal(quantity)
		if err != nil {
			return Valuation{}, err
		}
		costText, err := exact.FormatDecimal(cost)
		if err != nil {
			return Valuation{}, err
		}
		marketText, err := exact.FormatDecimal(market)
		if err != nil {
			return Valuation{}, err
		}
		unrealizedText, err := exact.FormatDecimal(unrealized)
		if err != nil {
			return Valuation{}, err
		}
		positions[symbol] = PositionValuation{
			Quantity: quantityText, OpenCost: costText, MarketValue: marketText, UnrealizedPnL: unrealizedText,
		}
	}

	equity := new(big.Rat).Add(cash, marketValue)
	unrealized := new(big.Rat).Sub(marketValue, openCost)
	total := new(big.Rat).Add(realized, unrealized)
	equityDelta := new(big.Rat).Sub(equity, starting)
	if total.Cmp(equityDelta) != 0 {
		return Valuation{}, errors.New("paper valuation PnL does not reconcile")
	}

	values := []*big.Rat{cash, openCost, marketValue, realized, unrealized, total, equity}
	formatted := make([]string, len(values))
	for index, value := range values {
		formatted[index], err = exact.FormatDecimal(value)
		if err != nil {
			return Valuation{}, err
		}
	}
	return Valuation{
		Cash: formatted[0], OpenCost: formatted[1], MarketValue: formatted[2], RealizedPnL: formatted[3],
		UnrealizedPnL: formatted[4], TotalPnL: formatted[5], Equity: formatted[6], Positions: positions,
	}, nil
}

func CalculatePerformance(startingCash string, equities []string) ([]PerformancePoint, error) {
	starting, err := exact.ParseDecimal(startingCash)
	if err != nil || starting.Sign() <= 0 {
		return nil, errors.New("paper performance starting cash is invalid")
	}
	parsed := make([]*big.Rat, len(equities))
	for index, raw := range equities {
		equity, err := exact.ParseDecimal(raw)
		if err != nil || equity.Sign() < 0 {
			return nil, errors.New("paper performance equity is invalid")
		}
		parsed[index] = equity
	}

	points := make([]PerformancePoint, 0, len(parsed))
	previous := new(big.Rat).Set(starting)
	peak := new(big.Rat).Set(starting)
	maxDrawdown := new(big.Rat)
	for _, equity := range parsed {
		if equity.Cmp(peak) > 0 {
			peak.Set(equity)
		}
		drawdown := new(big.Rat).Quo(new(big.Rat).Sub(peak, equity), peak)
		if drawdown.Cmp(maxDrawdown) > 0 {
			maxDrawdown.Set(drawdown)
		}

		equityText, err := exact.FormatDecimal(equity)
		if err != nil {
			return nil, err
		}
		peakText, err := exact.FormatDecimal(peak)
		if err != nil {
			return nil, err
		}
		cumulative, err := performanceRatio(new(big.Rat).Quo(new(big.Rat).Sub(equity, starting), starting))
		if err != nil {
			return nil, err
		}
		drawdownText, err := performanceRatio(drawdown)
		if err != nil {
			return nil, err
		}
		maxDrawdownText, err := performanceRatio(maxDrawdown)
		if err != nil {
			return nil, err
		}

		periodReturnState := "defined"
		periodReturn := ""
		if previous.Sign() == 0 {
			periodReturnState = "undefined_zero_denominator"
		} else {
			periodReturn, err = performanceRatio(new(big.Rat).Quo(new(big.Rat).Sub(equity, previous), previous))
			if err != nil {
				return nil, err
			}
		}
		points = append(points, PerformancePoint{
			Equity: equityText, PeakEquity: peakText, PeriodReturnState: periodReturnState,
			PeriodReturn: periodReturn, CumulativeReturn: cumulative,
			Drawdown: drawdownText, MaxDrawdown: maxDrawdownText,
		})
		previous.Set(equity)
	}
	return points, nil
}

func performanceRatio(value *big.Rat) (string, error) {
	return exact.FormatDecimal(exact.QuantizeHalfEven(value, 8))
}

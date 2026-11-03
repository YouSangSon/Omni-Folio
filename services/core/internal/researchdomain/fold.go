package researchdomain

import (
	"context"
	"errors"
	"math/big"
	"strconv"
	"strings"

	"omni-folio/services/core/internal/exact"
	"omni-folio/services/core/internal/paperdomain"
	"omni-folio/services/core/internal/strategydomain"
)

type Bar struct {
	At, Open, Close, Volume string
}

type Strategy struct {
	Quantity               string
	FastWindow, SlowWindow int64
}

type Execution struct {
	StartingCash string
	Policy       paperdomain.ExecutionPolicy
	DelayBars    int
}

type Metrics struct {
	AfterCostReturn      string `json:"after_cost_return"`
	MaxDrawdown          string `json:"max_drawdown"`
	TradeCount           string `json:"trade_count"`
	TradeCountDefinition string `json:"trade_count_definition"`
	FillCount            string `json:"fill_count"`
	Turnover             string `json:"turnover"`
	Fees                 string `json:"fees"`
	Taxes                string `json:"taxes"`
	SlippageCost         string `json:"slippage_cost"`
	PartialFillCount     string `json:"partial_fill_count"`
	LookaheadViolations  string `json:"lookahead_violations"`
}

type FillTrace struct {
	SignalAt   string `json:"signal_at"`
	EligibleAt string `json:"eligible_at"`
	FillAt     string `json:"fill_at"`
	Side       string `json:"side"`
	Quantity   string `json:"quantity"`
	Price      string `json:"price"`
}

type foldOrder struct {
	side                       string
	signalIndex, eligibleIndex int
	remaining                  *big.Int
}

// SimulateFold scores one fixed strategy using the paper fill contract.
// The caller owns timestamp ordering and OHLC consistency validation.
func SimulateFold(ctx context.Context, bars, warmup []Bar, strategy Strategy, execution Execution, buyAndHold bool) (Metrics, []FillTrace, error) {
	if err := ctx.Err(); err != nil {
		return Metrics{}, nil, err
	}
	if len(bars) == 0 || len(bars) > 500 || len(warmup) > 500-len(bars) {
		return Metrics{}, nil, errors.New("fold requires evaluation bars and at most 500 total bars")
	}
	if strategy.FastWindow < 1 || strategy.SlowWindow <= strategy.FastWindow || strategy.SlowWindow > 365 || !paperdomain.ValidCapitalizedQuantity(strategy.Quantity, false) {
		return Metrics{}, nil, errors.New("fold strategy is outside the paper SMA bounds")
	}
	startingCash, err := exact.ParseDecimal(execution.StartingCash)
	if err != nil || startingCash.Sign() <= 0 {
		return Metrics{}, nil, errors.New("fold starting cash is invalid")
	}
	if execution.DelayBars <= 0 {
		return Metrics{}, nil, errors.New("non-positive execution delay would permit lookahead")
	}
	series := make([]Bar, 0, len(warmup)+len(bars))
	series = append(series, warmup...)
	series = append(series, bars...)
	closes := make([]string, len(series))
	closeValues := make([]*big.Rat, len(series))
	for index, bar := range series {
		if len(bar.Close) > 21 {
			return Metrics{}, nil, errors.New("fold close is outside the paper SMA bounds")
		}
		_, fraction, _ := strings.Cut(bar.Close, ".")
		close, err := exact.ParseDecimal(bar.Close)
		if err != nil || len(fraction) > 8 || close.Sign() <= 0 || close.Cmp(big.NewRat(1_000_000_000_000, 1)) >= 0 {
			return Metrics{}, nil, errors.New("fold close is outside the paper SMA bounds")
		}
		open, err := exact.ParseDecimal(bar.Open)
		if err != nil || open.Sign() <= 0 {
			return Metrics{}, nil, errors.New("fold open is invalid")
		}
		volume, err := exact.ParseDecimal(bar.Volume)
		if err != nil || volume.Sign() < 0 {
			return Metrics{}, nil, errors.New("fold volume is invalid")
		}
		closes[index], closeValues[index] = bar.Close, close
	}
	// Validate policy even when this fold produces no orders or fills.
	if _, _, err := paperdomain.CalculateFill(execution.Policy, paperdomain.FillInput{
		Side: "BUY", Open: bars[0].Open, Volume: "0", RemainingQuantity: "0", ConsumedCapacity: "0",
	}); err != nil {
		return Metrics{}, nil, err
	}
	if !buyAndHold && len(series)-int(strategy.SlowWindow)-2 < execution.DelayBars {
		return Metrics{}, nil, errors.New("fold has insufficient bars for the SMA window and delayed liquidation")
	}

	cash := new(big.Rat).Set(startingCash)
	position := new(big.Int)
	quantity, _ := new(big.Int).SetString(strategy.Quantity, 10)
	fees, taxes, slippageCost, turnover := new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
	peak, maxDrawdown := new(big.Rat).Set(startingCash), new(big.Rat)
	trace := make([]FillTrace, 0)
	completedRoundTrips, partialFills := 0, 0
	liquidationSignalIndex := len(series) - execution.DelayBars - 1
	var pending *foldOrder
	schedule := func(side string, quantity *big.Int, index int) {
		pending = &foldOrder{side: side, signalIndex: index, eligibleIndex: index + execution.DelayBars, remaining: new(big.Int).Set(quantity)}
	}

	for index := len(warmup); index < len(series); index++ {
		if err := ctx.Err(); err != nil {
			return Metrics{}, nil, err
		}
		bar := series[index]
		if pending != nil && index >= pending.eligibleIndex {
			cashText, err := exact.FormatDecimal(cash)
			if err != nil {
				return Metrics{}, nil, err
			}
			// Only one order can be pending; no other fill consumes this bar's capacity.
			fill, ok, err := paperdomain.CalculateFill(execution.Policy, paperdomain.FillInput{
				Side: pending.side, Open: bar.Open, Volume: bar.Volume,
				RemainingQuantity: pending.remaining.String(), Cash: cashText,
				PositionQuantity: position.String(), ConsumedCapacity: "0",
			})
			if err != nil {
				return Metrics{}, nil, err
			}
			if ok {
				rawValues := []string{fill.Quantity, fill.Notional, fill.Fee, fill.Tax, fill.Slippage, fill.CashDelta}
				values := make([]*big.Rat, len(rawValues))
				for i, raw := range rawValues {
					values[i], err = exact.ParseDecimal(raw)
					if err != nil {
						return Metrics{}, nil, err
					}
				}
				pending.remaining.Sub(pending.remaining, values[0].Num())
				cash.Add(cash, values[5])
				turnover.Add(turnover, values[1])
				fees.Add(fees, values[2])
				taxes.Add(taxes, values[3])
				slippageCost.Add(slippageCost, values[4])
				if pending.side == "BUY" {
					position.Add(position, values[0].Num())
				} else {
					position.Sub(position, values[0].Num())
				}
				trace = append(trace, FillTrace{
					SignalAt: series[pending.signalIndex].At, EligibleAt: series[pending.eligibleIndex].At,
					FillAt: bar.At, Side: pending.side, Quantity: fill.Quantity, Price: fill.Price,
				})
				if pending.remaining.Sign() > 0 {
					partialFills++
				} else {
					if pending.side == "BUY" {
						completedRoundTrips++
					}
					pending = nil
				}
			}
		}
		equity := new(big.Rat).Add(cash, new(big.Rat).Mul(new(big.Rat).SetInt(position), closeValues[index]))
		if equity.Cmp(peak) > 0 {
			peak.Set(equity)
		}
		drawdown := new(big.Rat).Quo(new(big.Rat).Sub(peak, equity), peak)
		if drawdown.Cmp(maxDrawdown) > 0 {
			maxDrawdown.Set(drawdown)
		}
		if index == liquidationSignalIndex {
			if pending == nil && position.Sign() > 0 {
				schedule("SELL", position, index)
			}
			continue
		}
		if index >= liquidationSignalIndex || pending != nil {
			continue
		}
		if buyAndHold {
			if index == len(warmup) && position.Sign() == 0 {
				schedule("BUY", quantity, index)
			}
			continue
		}
		if index < int(strategy.SlowWindow) {
			continue
		}
		crossover, err := strategydomain.SMACrossover(closes[index-int(strategy.SlowWindow):index+1], strategy.FastWindow, strategy.SlowWindow)
		if err != nil {
			return Metrics{}, nil, err
		}
		if position.Sign() == 0 && crossover == "golden_cross" {
			schedule("BUY", quantity, index)
		} else if position.Sign() > 0 && crossover == "death_cross" {
			schedule("SELL", position, index)
		}
	}
	if pending != nil || position.Sign() != 0 {
		return Metrics{}, nil, errors.New("fold cannot complete every delayed long-only order")
	}
	values := []*big.Rat{
		exact.QuantizeHalfEven(new(big.Rat).Quo(new(big.Rat).Sub(cash, startingCash), startingCash), 28),
		exact.QuantizeHalfEven(maxDrawdown, 28),
		exact.QuantizeHalfEven(new(big.Rat).Quo(turnover, startingCash), 28),
		fees, taxes, slippageCost,
	}
	formatted := make([]string, len(values))
	for i, value := range values {
		formatted[i], err = exact.FormatDecimal(value)
		if err != nil {
			return Metrics{}, nil, err
		}
	}
	return Metrics{
		AfterCostReturn: formatted[0], MaxDrawdown: formatted[1], Turnover: formatted[2],
		Fees: formatted[3], Taxes: formatted[4], SlippageCost: formatted[5],
		TradeCount: strconv.Itoa(completedRoundTrips), TradeCountDefinition: "completed_long_only_round_trips",
		FillCount: strconv.Itoa(len(trace)), PartialFillCount: strconv.Itoa(partialFills), LookaheadViolations: "0",
	}, trace, nil
}

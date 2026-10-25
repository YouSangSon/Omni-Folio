package main

import (
	"context"
	"errors"
	"math/big"
	"time"
)

type twrHolding struct {
	holding   Holding
	quantity  *big.Rat
	lastSplit time.Time
}

type twrBalances struct {
	cash     map[string]*big.Rat
	holdings map[string]*twrHolding
}

func newTWRBalances() *twrBalances {
	return &twrBalances{map[string]*big.Rat{}, map[string]*twrHolding{}}
}

func (b *twrBalances) apply(e Transaction) error {
	amount, err := parseDecimal(e.Amount)
	if err != nil {
		return err
	}
	addRat(b.cash, e.Currency, amount)
	if e.Type == "FX_EXCHANGE" {
		counter, err := parseDecimal(e.CounterAmount)
		if err != nil {
			return err
		}
		addRat(b.cash, e.CounterCurrency, counter)
	}
	if e.Type != "BUY" && e.Type != "SELL" && e.Type != "SPLIT" {
		return nil
	}
	key := e.InstrumentID + "\x00" + e.Currency
	h := b.holdings[key]
	if h == nil {
		h = &twrHolding{holding: Holding{InstrumentID: e.InstrumentID, Symbol: e.Symbol, Currency: e.Currency}, quantity: new(big.Rat)}
		b.holdings[key] = h
	}
	quantity, err := parseDecimal(e.Quantity)
	if err != nil {
		return err
	}
	switch e.Type {
	case "BUY":
		h.quantity.Add(h.quantity, quantity)
	case "SELL":
		h.quantity.Sub(h.quantity, quantity)
	case "SPLIT":
		h.quantity.Mul(h.quantity, quantity)
		h.lastSplit, err = time.Parse(time.RFC3339Nano, e.OccurredAt)
		if err != nil {
			return err
		}
	}
	if h.quantity.Sign() < 0 {
		return errors.New("TWR projection has negative holding quantity")
	}
	return nil
}

func (b *twrBalances) zero() bool {
	for _, v := range b.cash {
		if v.Sign() != 0 {
			return false
		}
	}
	for _, h := range b.holdings {
		if h.quantity.Sign() != 0 {
			return false
		}
	}
	return true
}

func (b *twrBalances) matches(snapshot *PortfolioSnapshot) bool {
	cash := map[string]*big.Rat{}
	for _, m := range snapshot.Cash {
		v, err := parseDecimal(m.Amount)
		if err != nil {
			return false
		}
		addRat(cash, m.Currency, v)
	}
	for k, v := range b.cash {
		addRat(cash, k, new(big.Rat).Neg(v))
	}
	for _, v := range cash {
		if v.Sign() != 0 {
			return false
		}
	}
	quantities := map[string]*big.Rat{}
	for _, h := range snapshot.Holdings {
		key := h.InstrumentID + "\x00" + h.Currency
		v, err := parseDecimal(h.Quantity)
		if err != nil {
			return false
		}
		own := b.holdings[key]
		if own == nil || own.holding.Symbol != h.Symbol {
			return false
		}
		addRat(quantities, key, v)
	}
	for k, h := range b.holdings {
		addRat(quantities, k, new(big.Rat).Neg(h.quantity))
	}
	for _, v := range quantities {
		if v.Sign() != 0 {
			return false
		}
	}
	return true
}

type twrMarks struct {
	fx         []FXObservation
	fxTimes    map[string][3]time.Time
	prices     []SecurityPriceObservation
	priceTimes map[string][3]time.Time
}

func (m twrMarks) convert(amount *big.Rat, currency string, at, knownAt time.Time) (*big.Rat, bool, string) {
	if currency == "KRW" || amount.Sign() == 0 {
		return new(big.Rat).Set(amount), false, ""
	}
	fx := historicalXIRRFX(m.fx, m.fxTimes, currency, at, knownAt)
	if fx == nil {
		return nil, false, "missing_historical_fx"
	}
	if at.Sub(m.fxTimes[fx.ObservationID][0]) > cashFXMaxAge {
		return nil, true, "stale_historical_fx"
	}
	rate, err := parseDecimal(fx.Rate)
	if err != nil {
		return nil, true, "numerical_limit"
	}
	return new(big.Rat).Mul(amount, rate), true, ""
}

func (m twrMarks) value(ctx context.Context, b *twrBalances, at, knownAt time.Time) (*big.Rat, bool, string) {
	total := new(big.Rat)
	sample := false
	for _, currency := range sortedKeys(b.cash) {
		if ctx.Err() != nil {
			return nil, sample, "numerical_limit"
		}
		v, used, issue := m.convert(b.cash[currency], currency, at, knownAt)
		sample = sample || used
		if issue != "" {
			return nil, sample, issue
		}
		total.Add(total, v)
	}
	// Stable iteration makes missing-input responses reproducible.
	keys := make(map[string]*big.Rat, len(b.holdings))
	for k, h := range b.holdings {
		keys[k] = h.quantity
	}
	for _, key := range sortedKeys(keys) {
		if ctx.Err() != nil {
			return nil, sample, "numerical_limit"
		}
		h := b.holdings[key]
		if h.quantity.Sign() == 0 {
			continue
		}
		price, selection := latestEligibleHoldingPriceAt(m.prices, m.priceTimes, h.holding, at, knownAt)
		if price == nil {
			return nil, sample, holdingPriceIssue(selection, "").Code
		}
		sample = true
		observed := m.priceTimes[price.ObservationID][0]
		if !h.lastSplit.IsZero() && !observed.After(h.lastSplit) {
			return nil, sample, "split_price_unavailable"
		}
		if at.Sub(observed) > holdingPriceMaxAge {
			return nil, sample, "stale_security_price"
		}
		p, err := parseDecimal(price.Price)
		if err != nil {
			return nil, sample, "numerical_limit"
		}
		v, used, issue := m.convert(new(big.Rat).Mul(h.quantity, p), h.holding.Currency, at, knownAt)
		sample = sample || used
		if issue != "" {
			return nil, sample, issue
		}
		total.Add(total, v)
	}
	return total, sample, ""
}

// Check the unreduced product before allocating it, then preserve exact ratios.
func twrChain(factor, value, capital *big.Rat) *big.Rat {
	if factor.Sign() < 0 || value.Sign() < 0 || capital.Sign() <= 0 {
		return nil
	}
	const maxBits = 8192
	if factor.Num().BitLen()+value.Num().BitLen()+capital.Denom().BitLen() > maxBits ||
		factor.Denom().BitLen()+value.Denom().BitLen()+capital.Num().BitLen() > maxBits {
		return nil
	}
	return new(big.Rat).Mul(factor, new(big.Rat).Quo(value, capital))
}

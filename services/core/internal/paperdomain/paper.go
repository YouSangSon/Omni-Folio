package paperdomain

import (
	"errors"
	"math/big"

	"omni-folio/services/core/internal/exact"
)

const (
	FillPolicyVersion = "paper_bar_open_v1"
	MaxQuantity       = "4611686018427387903"
)

type ExecutionPolicy struct {
	Fee              string
	Tax              string
	SlippageBPS      string
	MaxParticipation string
}

type FillInput struct {
	Side              string
	Open              string
	Volume            string
	RemainingQuantity string
	Cash              string
	PositionQuantity  string
	ConsumedCapacity  string
}

type Fill struct {
	Quantity       string
	ReferencePrice string
	Price          string
	Notional       string
	Fee            string
	Tax            string
	Slippage       string
	CashDelta      string
}

func CalculateFill(policy ExecutionPolicy, input FillInput) (Fill, bool, error) {
	fee, err := exact.ParseDecimal(policy.Fee)
	if err != nil || fee.Sign() < 0 {
		return Fill{}, false, errors.New("paper fill fee is invalid")
	}
	taxRate, err := exact.ParseDecimal(policy.Tax)
	if err != nil || taxRate.Sign() < 0 || taxRate.Cmp(big.NewRat(1, 1)) > 0 {
		return Fill{}, false, errors.New("paper fill tax is invalid")
	}
	slippageBPS, err := exact.ParseDecimal(policy.SlippageBPS)
	if err != nil || slippageBPS.Sign() < 0 || slippageBPS.Cmp(big.NewRat(10000, 1)) >= 0 {
		return Fill{}, false, errors.New("paper fill slippage is invalid")
	}
	participation, err := exact.ParseDecimal(policy.MaxParticipation)
	if err != nil || participation.Sign() <= 0 || participation.Cmp(big.NewRat(1, 1)) > 0 {
		return Fill{}, false, errors.New("paper fill participation is invalid")
	}
	if input.Side != "BUY" && input.Side != "SELL" {
		return Fill{}, false, errors.New("paper fill side is invalid")
	}
	open, err := exact.ParseDecimal(input.Open)
	if err != nil || open.Sign() <= 0 {
		return Fill{}, false, errors.New("paper fill open is invalid")
	}
	volume, err := exact.ParseDecimal(input.Volume)
	if err != nil || volume.Sign() < 0 {
		return Fill{}, false, errors.New("paper fill volume is invalid")
	}
	remaining, err := fillInteger(input.RemainingQuantity, "remaining quantity")
	if err != nil {
		return Fill{}, false, err
	}
	consumed, err := fillInteger(input.ConsumedCapacity, "consumed capacity")
	if err != nil {
		return Fill{}, false, err
	}
	var available *big.Int
	if input.Side == "SELL" {
		available, err = fillInteger(input.PositionQuantity, "position quantity")
		if err != nil {
			return Fill{}, false, err
		}
	}

	capacity := floorPositiveRat(new(big.Rat).Mul(volume, participation))
	if consumed.Cmp(capacity) > 0 {
		return Fill{}, false, errors.New("paper fill consumed capacity exceeds capacity")
	}
	quantity := new(big.Int).Sub(capacity, consumed)
	if quantity.Cmp(remaining) > 0 {
		quantity.Set(remaining)
	}

	one := big.NewRat(1, 1)
	adjustment := new(big.Rat).Quo(slippageBPS, big.NewRat(10000, 1))
	priceFactor := new(big.Rat)
	if input.Side == "BUY" {
		priceFactor.Add(one, adjustment)
	} else {
		priceFactor.Sub(one, adjustment)
	}
	price := new(big.Rat).Mul(open, priceFactor)
	if input.Side == "SELL" && price.Sign() <= 0 {
		return Fill{}, false, errors.New("paper fill sell price is not positive")
	}
	if quantity.Sign() == 0 {
		return Fill{}, false, nil
	}

	if input.Side == "BUY" {
		cash, err := exact.ParseDecimal(input.Cash)
		if err != nil || cash.Sign() < 0 {
			return Fill{}, false, errors.New("paper fill cash is invalid")
		}
		affordableCash := new(big.Rat).Sub(cash, fee)
		if affordableCash.Sign() < 0 {
			return Fill{}, false, nil
		}
		affordable := floorPositiveRat(new(big.Rat).Quo(affordableCash, price))
		if quantity.Cmp(affordable) > 0 {
			quantity.Set(affordable)
		}
	} else if quantity.Cmp(available) > 0 {
		quantity.Set(available)
	}
	if quantity.Sign() == 0 {
		return Fill{}, false, nil
	}

	quantityRat := new(big.Rat).SetInt(quantity)
	notional := new(big.Rat).Mul(price, quantityRat)
	tax := new(big.Rat)
	if input.Side == "SELL" {
		tax.Mul(notional, taxRate)
	}
	slippage := new(big.Rat).Abs(new(big.Rat).Sub(price, open))
	slippage.Mul(slippage, quantityRat)
	cashDelta := new(big.Rat)
	if input.Side == "BUY" {
		cashDelta.Neg(new(big.Rat).Add(notional, fee))
	} else {
		cashDelta.Sub(notional, fee)
		cashDelta.Sub(cashDelta, tax)
		if cashDelta.Sign() < 0 {
			return Fill{}, false, errors.New("paper fill sell proceeds are negative")
		}
	}
	values := []*big.Rat{quantityRat, open, price, notional, fee, tax, slippage, cashDelta}
	formatted := make([]string, len(values))
	for index, value := range values {
		formatted[index], err = exact.FormatDecimal(value)
		if err != nil {
			return Fill{}, false, err
		}
	}
	return Fill{
		Quantity: formatted[0], ReferencePrice: formatted[1], Price: formatted[2], Notional: formatted[3],
		Fee: formatted[4], Tax: formatted[5], Slippage: formatted[6], CashDelta: formatted[7],
	}, true, nil
}

func ValidCapitalizedQuantity(raw string, allowZero bool) bool {
	if allowZero && raw == "0" {
		return true
	}
	if len(raw) == 0 || len(raw) > 32 || raw[0] == '0' {
		return false
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	value, ok := new(big.Int).SetString(raw, 10)
	maximum, _ := new(big.Int).SetString(MaxQuantity, 10)
	return ok && value.Cmp(maximum) <= 0
}

func fillInteger(raw, field string) (*big.Int, error) {
	value, err := exact.ParseDecimal(raw)
	if err != nil || value.Sign() < 0 || !value.IsInt() {
		return nil, errors.New("paper fill " + field + " must be a non-negative integer")
	}
	return new(big.Int).Set(value.Num()), nil
}

func floorPositiveRat(value *big.Rat) *big.Int {
	return new(big.Int).Quo(value.Num(), value.Denom())
}

type Lot struct {
	Quantity string
	Cost     string
}

type AccountState struct {
	AccountRef               string
	PaperAccountingSessionID string
	Cash                     string
	Lots                     map[string][]Lot
	Fees                     string
	Taxes                    string
	Slippage                 string
	RealizedPnL              string
	CapitalizedFills         int
}

type replayLot struct {
	quantity *big.Rat
	cost     *big.Rat
}

type Account struct {
	accountRef string
	sessionID  string
	cash       *big.Rat
	fees       *big.Rat
	taxes      *big.Rat
	slippage   *big.Rat
	pnl        *big.Rat
	lots       map[string][]replayLot
	fills      int
}

func NewAccount(accountRef, sessionID, startingCash string) (*Account, error) {
	cash, err := exact.ParseDecimal(startingCash)
	if err != nil || cash.Sign() < 0 {
		return nil, errors.New("paper accounting starting cash is invalid")
	}
	return &Account{
		accountRef: accountRef, sessionID: sessionID, cash: cash, fees: new(big.Rat), taxes: new(big.Rat),
		slippage: new(big.Rat), pnl: new(big.Rat), lots: map[string][]replayLot{},
	}, nil
}

func (a *Account) Cash() string {
	formatted, _ := exact.FormatDecimal(a.cash)
	return formatted
}

func (a *Account) SessionID() string { return a.sessionID }

func (a *Account) PositionQuantity(symbol string) string {
	position := new(big.Rat)
	for _, lot := range a.lots[symbol] {
		position.Add(position, lot.quantity)
	}
	formatted, _ := exact.FormatDecimal(position)
	return formatted
}

func (a *Account) ApplyFill(symbol, side string, fill Fill) error {
	// ponytail: cloning is O(lots) per fill; switch to measured preflight/in-place mutation if EOD replay volume makes it material.
	next := a.clone()
	if err := next.applyFill(symbol, side, fill); err != nil {
		return err
	}
	*a = *next
	return nil
}

func (a *Account) applyFill(symbol, side string, fill Fill) error {
	if side != "BUY" && side != "SELL" {
		return errors.New("paper fill side is invalid")
	}
	quantity, err := exact.ParseDecimal(fill.Quantity)
	if err != nil || quantity.Sign() <= 0 || !quantity.IsInt() {
		return errors.New("paper fill quantity is invalid")
	}
	fee, err := exact.ParseDecimal(fill.Fee)
	if err != nil || fee.Sign() < 0 {
		return errors.New("paper fill fee is invalid")
	}
	tax, err := exact.ParseDecimal(fill.Tax)
	if err != nil || tax.Sign() < 0 {
		return errors.New("paper fill tax is invalid")
	}
	slippage, err := exact.ParseDecimal(fill.Slippage)
	if err != nil || slippage.Sign() < 0 {
		return errors.New("paper fill slippage is invalid")
	}
	cashDelta, err := exact.ParseDecimal(fill.CashDelta)
	if err != nil {
		return errors.New("paper fill cash delta is invalid")
	}
	if side == "BUY" {
		if tax.Sign() != 0 {
			return errors.New("paper BUY tax must be zero")
		}
		if cashDelta.Sign() >= 0 {
			return errors.New("paper BUY cash delta must be negative")
		}
	} else if cashDelta.Sign() < 0 {
		return errors.New("paper SELL cash delta must be non-negative")
	}
	a.cash.Add(a.cash, cashDelta)
	a.fees.Add(a.fees, fee)
	a.taxes.Add(a.taxes, tax)
	a.slippage.Add(a.slippage, slippage)
	if a.cash.Sign() < 0 {
		return errors.New("paper fill makes cash negative")
	}
	if side == "BUY" {
		cost := new(big.Rat).Neg(cashDelta)
		a.lots[symbol] = append(a.lots[symbol], replayLot{quantity: quantity, cost: cost})
	} else {
		remaining := new(big.Rat).Set(quantity)
		allocated := new(big.Rat)
		lots := a.lots[symbol]
		for remaining.Sign() > 0 && len(lots) > 0 {
			take := new(big.Rat).Set(remaining)
			if take.Cmp(lots[0].quantity) > 0 {
				take.Set(lots[0].quantity)
			}
			cost, err := exact.FIFOAllocation(lots[0].cost, take, lots[0].quantity)
			if err != nil {
				return err
			}
			allocated.Add(allocated, cost)
			lots[0].quantity.Sub(lots[0].quantity, take)
			lots[0].cost.Sub(lots[0].cost, cost)
			remaining.Sub(remaining, take)
			if lots[0].quantity.Sign() == 0 {
				lots = lots[1:]
			}
		}
		if remaining.Sign() != 0 {
			return errors.New("paper SELL exceeds FIFO lots")
		}
		if len(lots) == 0 {
			delete(a.lots, symbol)
		} else {
			a.lots[symbol] = lots
		}
		a.pnl.Add(a.pnl, new(big.Rat).Sub(cashDelta, allocated))
	}
	a.fills++
	return nil
}

func (a *Account) clone() *Account {
	next := *a
	next.cash, next.fees, next.taxes = new(big.Rat).Set(a.cash), new(big.Rat).Set(a.fees), new(big.Rat).Set(a.taxes)
	next.slippage, next.pnl = new(big.Rat).Set(a.slippage), new(big.Rat).Set(a.pnl)
	next.lots = make(map[string][]replayLot, len(a.lots))
	for symbol, lots := range a.lots {
		for _, lot := range lots {
			next.lots[symbol] = append(next.lots[symbol], replayLot{quantity: new(big.Rat).Set(lot.quantity), cost: new(big.Rat).Set(lot.cost)})
		}
	}
	return &next
}

func (a *Account) State() (AccountState, error) {
	state := AccountState{
		AccountRef: a.accountRef, PaperAccountingSessionID: a.sessionID, Lots: map[string][]Lot{}, CapitalizedFills: a.fills,
	}
	values := []*big.Rat{a.cash, a.fees, a.taxes, a.slippage, a.pnl}
	formatted := make([]string, len(values))
	var err error
	for index, value := range values {
		formatted[index], err = exact.FormatDecimal(value)
		if err != nil {
			return AccountState{}, err
		}
	}
	state.Cash, state.Fees, state.Taxes, state.Slippage, state.RealizedPnL = formatted[0], formatted[1], formatted[2], formatted[3], formatted[4]
	for symbol, lots := range a.lots {
		for _, lot := range lots {
			quantity, err := exact.FormatDecimal(lot.quantity)
			if err != nil {
				return AccountState{}, err
			}
			cost, err := exact.FormatDecimal(lot.cost)
			if err != nil {
				return AccountState{}, err
			}
			state.Lots[symbol] = append(state.Lots[symbol], Lot{Quantity: quantity, Cost: cost})
		}
	}
	return state, nil
}

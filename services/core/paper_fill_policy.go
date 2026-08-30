package main

import (
	"errors"
	"math/big"
)

type paperFillInput struct {
	Side, Open, Volume, RemainingQuantity    string
	Cash, PositionQuantity, ConsumedCapacity string
}

type paperCalculatedFill struct {
	Quantity, ReferencePrice, Price, Notional string
	Fee, Tax, Slippage, CashDelta             string
}

func floorPositiveRat(value *big.Rat) *big.Int {
	return new(big.Int).Quo(value.Num(), value.Denom())
}

func calculatePaperFill(policy strategyExecutionPolicy, input paperFillInput) (paperCalculatedFill, bool, error) {
	fee, err := parseDecimal(policy.Fee)
	if err != nil || fee.Sign() < 0 {
		return paperCalculatedFill{}, false, errors.New("paper fill fee is invalid")
	}
	taxRate, err := parseDecimal(policy.Tax)
	if err != nil || taxRate.Sign() < 0 || taxRate.Cmp(big.NewRat(1, 1)) > 0 {
		return paperCalculatedFill{}, false, errors.New("paper fill tax is invalid")
	}
	slippageBPS, err := parseDecimal(policy.SlippageBPS)
	if err != nil || slippageBPS.Sign() < 0 || slippageBPS.Cmp(big.NewRat(10000, 1)) >= 0 {
		return paperCalculatedFill{}, false, errors.New("paper fill slippage is invalid")
	}
	participation, err := parseDecimal(policy.MaxParticipation)
	if err != nil || participation.Sign() <= 0 || participation.Cmp(big.NewRat(1, 1)) > 0 {
		return paperCalculatedFill{}, false, errors.New("paper fill participation is invalid")
	}
	if input.Side != "BUY" && input.Side != "SELL" {
		return paperCalculatedFill{}, false, errors.New("paper fill side is invalid")
	}
	open, err := parseDecimal(input.Open)
	if err != nil || open.Sign() <= 0 {
		return paperCalculatedFill{}, false, errors.New("paper fill open is invalid")
	}
	volume, err := parseDecimal(input.Volume)
	if err != nil || volume.Sign() < 0 {
		return paperCalculatedFill{}, false, errors.New("paper fill volume is invalid")
	}
	remaining, err := paperFillInteger(input.RemainingQuantity, "remaining quantity")
	if err != nil {
		return paperCalculatedFill{}, false, err
	}
	consumed, err := paperFillInteger(input.ConsumedCapacity, "consumed capacity")
	if err != nil {
		return paperCalculatedFill{}, false, err
	}

	capacity := floorPositiveRat(new(big.Rat).Mul(volume, participation))
	if consumed.Cmp(capacity) > 0 {
		return paperCalculatedFill{}, false, errors.New("paper fill consumed capacity exceeds capacity")
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
		return paperCalculatedFill{}, false, errors.New("paper fill sell price is not positive")
	}
	if quantity.Sign() == 0 {
		return paperCalculatedFill{}, false, nil
	}

	if input.Side == "BUY" {
		cash, err := parseDecimal(input.Cash)
		if err != nil || cash.Sign() < 0 {
			return paperCalculatedFill{}, false, errors.New("paper fill cash is invalid")
		}
		affordableCash := new(big.Rat).Sub(cash, fee)
		if affordableCash.Sign() < 0 {
			return paperCalculatedFill{}, false, nil
		}
		affordable := floorPositiveRat(new(big.Rat).Quo(affordableCash, price))
		if quantity.Cmp(affordable) > 0 {
			quantity.Set(affordable)
		}
	} else {
		position, err := parseDecimal(input.PositionQuantity)
		if err != nil || position.Sign() < 0 {
			return paperCalculatedFill{}, false, errors.New("paper fill position is invalid")
		}
		available := floorPositiveRat(position)
		if quantity.Cmp(available) > 0 {
			quantity.Set(available)
		}
	}
	if quantity.Sign() == 0 {
		return paperCalculatedFill{}, false, nil
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
			return paperCalculatedFill{}, false, errors.New("paper fill sell proceeds are negative")
		}
	}
	values := []*big.Rat{quantityRat, open, price, notional, fee, tax, slippage, cashDelta}
	formatted := make([]string, len(values))
	for index, value := range values {
		formatted[index], err = formatDecimal(value)
		if err != nil {
			return paperCalculatedFill{}, false, err
		}
	}
	return paperCalculatedFill{
		Quantity: formatted[0], ReferencePrice: formatted[1], Price: formatted[2], Notional: formatted[3],
		Fee: formatted[4], Tax: formatted[5], Slippage: formatted[6], CashDelta: formatted[7],
	}, true, nil
}

func paperFillInteger(raw, field string) (*big.Int, error) {
	value, err := parseDecimal(raw)
	if err != nil || value.Sign() < 0 || !value.IsInt() {
		return nil, errors.New("paper fill " + field + " must be a non-negative integer")
	}
	return new(big.Int).Set(value.Num()), nil
}

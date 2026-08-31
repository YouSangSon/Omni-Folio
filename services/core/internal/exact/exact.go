package exact

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var decimalPattern = regexp.MustCompile(`^(?:0|-?(?:[1-9][0-9]*(?:\.[0-9]*[1-9])?|0\.[0-9]*[1-9]))$`)

func ParseDecimal(raw string) (*big.Rat, error) {
	if !decimalPattern.MatchString(raw) {
		return nil, errors.New("non-canonical decimal")
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, errors.New("invalid decimal")
	}
	return value, nil
}

func FormatDecimal(value *big.Rat) (string, error) {
	if value.Sign() == 0 {
		return "0", nil
	}
	denominator := new(big.Int).Set(value.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	twos, fives := 0, 0
	zero := new(big.Int)
	for new(big.Int).Mod(denominator, two).Cmp(zero) == 0 {
		denominator.Div(denominator, two)
		twos++
	}
	for new(big.Int).Mod(denominator, five).Cmp(zero) == 0 {
		denominator.Div(denominator, five)
		fives++
	}
	if denominator.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("exact value %s has no finite decimal representation", value.RatString())
	}
	scale := max(twos, fives)
	formatted := value.FloatString(scale)
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	}
	return formatted, nil
}

func QuantizeHalfEven(value *big.Rat, scale int) *big.Rat {
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	scaled := new(big.Int).Mul(new(big.Int).Abs(value.Num()), factor)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaled, value.Denom(), remainder)
	comparison := new(big.Int).Lsh(new(big.Int).Set(remainder), 1).Cmp(value.Denom())
	if comparison > 0 || comparison == 0 && quotient.Bit(0) == 1 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if value.Sign() < 0 {
		quotient.Neg(quotient)
	}
	return new(big.Rat).SetFrac(quotient, factor)
}

func FIFOAllocation(cost, take, quantity *big.Rat) (*big.Rat, error) {
	if take.Cmp(quantity) == 0 {
		return new(big.Rat).Set(cost), nil
	}
	allocation := new(big.Rat).Mul(cost, new(big.Rat).Quo(take, quantity))
	if _, exact := allocation.FloatPrec(); exact {
		return allocation, nil
	}
	costScale, exact := cost.FloatPrec()
	if !exact {
		return nil, errors.New("lot cost is not a finite decimal")
	}
	allocation = QuantizeHalfEven(allocation, max(8, costScale))
	if allocation.Sign() < 0 || allocation.Cmp(cost) > 0 {
		return nil, errors.New("rounded allocation exceeds lot cost")
	}
	return allocation, nil
}

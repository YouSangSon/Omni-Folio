package strategydomain

import (
	"errors"
	"math/big"
	"strings"

	"omni-folio/services/core/internal/exact"
)

// VerifySMACrossover independently checks a claimed reference-v1 decision.
// Bounds keep every sum exact in Python Decimal(28); distinct averages differ
// by at least 1e-8/(365*364), greater than combined division rounding (<1e-16).
// Do not widen these bounds without a new arithmetic parity proof.
func VerifySMACrossover(closes []string, fast, slow int64, claimed string) error {
	invalid := errors.New("paper SMA decision is unverified")
	if fast < 1 || slow <= fast || slow > 365 || int64(len(closes)) != slow+1 {
		return invalid
	}
	values := make([]*big.Rat, len(closes))
	for i, raw := range closes {
		if len(raw) > 21 {
			return invalid
		}
		_, fraction, _ := strings.Cut(raw, ".")
		value, err := exact.ParseDecimal(raw)
		if err != nil || len(fraction) > 8 || value.Sign() <= 0 || value.Cmp(big.NewRat(1_000_000_000_000, 1)) >= 0 {
			return invalid
		}
		values[i] = value
	}
	average := func(end, window int64) *big.Rat {
		sum := new(big.Rat)
		for _, v := range values[end-window+1 : end+1] {
			sum.Add(sum, v)
		}
		return sum.Quo(sum, big.NewRat(window, 1))
	}
	previous := average(slow-1, fast).Cmp(average(slow-1, slow))
	current := average(slow, fast).Cmp(average(slow, slow))
	actual := "none"
	if previous <= 0 && current > 0 {
		actual = "golden_cross"
	}
	if previous >= 0 && current < 0 {
		actual = "death_cross"
	}
	if claimed != actual {
		return invalid
	}
	return nil
}

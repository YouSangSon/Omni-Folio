package exact

import (
	"math/big"
	"testing"
)

func TestDecimalParseAndFormat(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{raw: "0", want: "0"},
		{raw: "10.25", want: "10.25"},
		{raw: "-0.125", want: "-0.125"},
	} {
		value, err := ParseDecimal(test.raw)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", test.raw, err)
		}
		got, err := FormatDecimal(value)
		if err != nil || got != test.want {
			t.Fatalf("FormatDecimal(ParseDecimal(%q)) = %q, %v; want %q", test.raw, got, err, test.want)
		}
	}

	for _, raw := range []string{"", "01", "1.0", "-0", ".5", "1e2"} {
		if _, err := ParseDecimal(raw); err == nil || err.Error() != "non-canonical decimal" {
			t.Fatalf("ParseDecimal(%q) error = %v; want non-canonical decimal", raw, err)
		}
	}

	if _, err := FormatDecimal(big.NewRat(1, 3)); err == nil || err.Error() != "exact value 1/3 has no finite decimal representation" {
		t.Fatalf("FormatDecimal(1/3) error = %v", err)
	}
}

func TestQuantizeHalfEvenTies(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want string
	}{
		{raw: "1.234567845", want: "1.23456784"},
		{raw: "1.234567855", want: "1.23456786"},
		{raw: "-1.234567845", want: "-1.23456784"},
		{raw: "-1.234567855", want: "-1.23456786"},
	} {
		value, err := ParseDecimal(test.raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatDecimal(QuantizeHalfEven(value, 8))
		if err != nil || got != test.want {
			t.Fatalf("QuantizeHalfEven(%s, 8) = %q, %v; want %q", test.raw, got, err, test.want)
		}
	}
}

func TestFIFOAllocationExactRecurringResidualAndBounds(t *testing.T) {
	for _, test := range []struct {
		name, cost, take, quantity, want string
	}{
		{name: "exact", cost: "10", take: "1", quantity: "4", want: "2.5"},
		{name: "recurring", cost: "1", take: "1", quantity: "3", want: "0.33333333"},
		{name: "final residual", cost: "1", take: "3", quantity: "3", want: "1"},
		{name: "tiny lot", cost: "0.000000006", take: "6", quantity: "7", want: "0.000000005"},
		{name: "exact below eight places", cost: "1", take: "1", quantity: "2048", want: "0.00048828125"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cost := mustParse(t, test.cost)
			allocation, err := FIFOAllocation(cost, mustParse(t, test.take), mustParse(t, test.quantity))
			if err != nil {
				t.Fatal(err)
			}
			got, formatErr := FormatDecimal(allocation)
			if formatErr != nil || got != test.want {
				t.Fatalf("FIFOAllocation = %q, %v; want %q", got, formatErr, test.want)
			}
			if allocation.Sign() < 0 || allocation.Cmp(cost) > 0 {
				t.Fatalf("allocation %s is outside [0, %s]", allocation, cost)
			}
		})
	}

	if _, err := FIFOAllocation(big.NewRat(1, 3), big.NewRat(1, 1), big.NewRat(2, 1)); err == nil || err.Error() != "lot cost is not a finite decimal" {
		t.Fatalf("non-finite lot cost error = %v", err)
	}
}

func mustParse(t *testing.T, raw string) *big.Rat {
	t.Helper()
	value, err := ParseDecimal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

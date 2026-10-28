package strategydomain

import (
	"strings"
	"testing"
)

func TestVerifySMACrossover(t *testing.T) {
	for _, test := range []struct {
		closes []string
		signal string
	}{
		{[]string{"3", "2", "1", "4"}, "golden_cross"},
		{[]string{"2", "3", "4", "1"}, "death_cross"},
		{[]string{"1", "1", "1", "1"}, "none"},
		{[]string{"2", "1", "3", "4"}, "golden_cross"},
	} {
		if got, err := SMACrossover(test.closes, 2, 3); err != nil || got != test.signal {
			t.Fatalf("SMACrossover(%v) = %q, %v; want %q", test.closes, got, err, test.signal)
		}
		if err := VerifySMACrossover(test.closes, 2, 3, test.signal); err != nil {
			t.Fatal(err)
		}
		for _, wrong := range []string{"golden_cross", "death_cross", "none"} {
			if wrong != test.signal && VerifySMACrossover(test.closes, 2, 3, wrong) == nil {
				t.Fatal("forged crossover accepted")
			}
		}
	}
	for _, bad := range [][]string{{"0", "1", "2", "3"}, {"1000000000000", "1", "2", "3"}, {"1.000000001", "1", "2", "3"}, {"1", "2"}} {
		if VerifySMACrossover(bad, 2, 3, "golden_cross") == nil {
			t.Fatal("unsupported input admitted")
		}
	}
}

func TestSMACrossoverPreservesVerificationBounds(t *testing.T) {
	for _, test := range []struct {
		name       string
		closes     []string
		fast, slow int64
	}{
		{"zero fast window", []string{"1", "2", "3", "4"}, 0, 3},
		{"equal windows", []string{"1", "2", "3", "4"}, 3, 3},
		{"oversized window", make([]string, 367), 1, 366},
		{"missing close", []string{"1", "2", "3"}, 2, 3},
		{"extra close", []string{"1", "2", "3", "4", "5"}, 2, 3},
		{"noncanonical close", []string{"1.0", "2", "3", "4"}, 2, 3},
		{"negative close", []string{"-1", "2", "3", "4"}, 2, 3},
		{"long close", []string{strings.Repeat("1", 22), "2", "3", "4"}, 2, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, err := SMACrossover(test.closes, test.fast, test.slow); got != "" || err == nil || err.Error() != "paper SMA decision is unverified" {
				t.Fatalf("SMACrossover() = %q, %v; want original verification error", got, err)
			}
			if err := VerifySMACrossover(test.closes, test.fast, test.slow, "none"); err == nil || err.Error() != "paper SMA decision is unverified" {
				t.Fatalf("VerifySMACrossover() = %v; want original verification error", err)
			}
		})
	}
	if err := VerifySMACrossover([]string{"1", "1", "1", "1"}, 2, 3, "unrecognized"); err == nil || err.Error() != "paper SMA decision is unverified" {
		t.Fatalf("unrecognized claim error = %v", err)
	}
}

func TestSMACrossoverComparesExactAveragesAtUpperBounds(t *testing.T) {
	closes := make([]string, 366)
	for i := range closes {
		closes[i] = "999999999999.99999998"
	}
	closes[len(closes)-1] = "999999999999.99999999"
	if got, err := SMACrossover(closes, 364, 365); err != nil || got != "golden_cross" {
		t.Fatalf("SMACrossover() = %q, %v; want golden_cross", got, err)
	}
}

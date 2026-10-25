package main

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"
)

func TestPortfolioTWRIndependentChainFixtures(t *testing.T) {
	data, err := os.ReadFile("../../contracts/fixtures/portfolio-twr-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string
			Pairs    [][2]string
			Expected string
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			factor := big.NewRat(1, 1)
			for _, pair := range test.Pairs {
				v, _ := new(big.Rat).SetString(pair[0])
				c, _ := new(big.Rat).SetString(pair[1])
				if v == nil || c == nil {
					t.Fatal("invalid fixture")
				}
				factor = twrChain(factor, v, c)
				if factor == nil {
					t.Fatal("valid reference rejected")
				}
			}
			got, err := formatDecimal(quantizeHalfEven(new(big.Rat).Sub(factor, big.NewRat(1, 1)), 12))
			if err != nil || got != test.Expected {
				t.Fatalf("got %s %v want %s", got, err, test.Expected)
			}
		})
	}
}

func TestPortfolioTWRChainBudgetAndProjectionReconciliation(t *testing.T) {
	one := big.NewRat(1, 1)
	huge := new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 8193))
	for _, input := range [][3]*big.Rat{{one, one, new(big.Rat)}, {one, big.NewRat(-1, 1), one}, {huge, one, one}, {one, one, huge}} {
		if twrChain(input[0], input[1], input[2]) != nil {
			t.Fatal("invalid capital or oversized arithmetic accepted")
		}
	}
	b := newTWRBalances()
	b.cash["USD"] = big.NewRat(2, 1)
	b.cash["KRW"] = new(big.Rat)
	snapshot := &PortfolioSnapshot{Cash: []Money{{Currency: "USD", Amount: "2"}}}
	if !b.matches(snapshot) {
		t.Fatal("zero/missing cash normalization differs")
	}
	snapshot.Cash = []Money{{Currency: "USD", Amount: "1"}, {Currency: "KRW", Amount: "1"}}
	if b.matches(snapshot) {
		t.Fatal("different balances offset by a total were certified")
	}
	b = newTWRBalances()
	b.holdings["a\x00USD"] = &twrHolding{holding: Holding{InstrumentID: "a", Currency: "USD", Symbol: "A"}, quantity: big.NewRat(2, 1)}
	snapshot = &PortfolioSnapshot{Holdings: []Holding{{InstrumentID: "a", Currency: "USD", Symbol: "A", Quantity: "1"}}}
	if b.matches(snapshot) {
		t.Fatal("different holding quantity certified")
	}
	snapshot.Holdings[0].Quantity = "2"
	if !b.matches(snapshot) {
		t.Fatal("equal holding rejected")
	}
}

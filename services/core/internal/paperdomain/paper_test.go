package paperdomain

import (
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCalculateFillExactExamples(t *testing.T) {
	policy := ExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}
	tests := []struct {
		name  string
		input FillInput
		want  Fill
	}{
		{
			name:  "BUY applies adverse slippage fixed fee and participation cap",
			input: FillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0"},
			want:  Fill{Quantity: "2", ReferencePrice: "100", Price: "100.1", Notional: "200.2", Fee: "1", Tax: "0", Slippage: "0.2", CashDelta: "-201.2"},
		},
		{
			name:  "SELL applies adverse slippage tax and position cap",
			input: FillInput{Side: "SELL", Open: "120", Volume: "5", RemainingQuantity: "10", PositionQuantity: "2", ConsumedCapacity: "0"},
			want:  Fill{Quantity: "2", ReferencePrice: "120", Price: "119.88", Notional: "239.76", Fee: "1", Tax: "0.23976", Slippage: "0.24", CashDelta: "238.52024"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok, err := CalculateFill(policy, test.input)
			if err != nil || !ok || got != test.want {
				t.Fatalf("CalculateFill() = %+v, %t, %v; want %+v, true, nil", got, ok, err, test.want)
			}
		})
	}
}

func TestCalculateFillCapsAndNoFill(t *testing.T) {
	policy := ExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}
	tests := []struct {
		name  string
		input FillInput
		want  *Fill
	}{
		{"BUY affordable exactly once", FillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "101.1", ConsumedCapacity: "0"}, &Fill{Quantity: "1", ReferencePrice: "100", Price: "100.1", Notional: "100.1", Fee: "1", Tax: "0", Slippage: "0.1", CashDelta: "-101.1"}},
		{"BUY unaffordable", FillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "101.09", ConsumedCapacity: "0"}, nil},
		{"zero volume", FillInput{Side: "BUY", Open: "100", Volume: "0", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0"}, nil},
		{"exhausted capacity", FillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "2"}, nil},
		{"SELL position", FillInput{Side: "SELL", Open: "120", Volume: "5", RemainingQuantity: "10", PositionQuantity: "1", ConsumedCapacity: "0"}, &Fill{Quantity: "1", ReferencePrice: "120", Price: "119.88", Notional: "119.88", Fee: "1", Tax: "0.11988", Slippage: "0.12", CashDelta: "118.76012"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok, err := CalculateFill(policy, test.input)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if ok || got != (Fill{}) {
					t.Fatalf("CalculateFill() = %+v, %t; want zero fill, false", got, ok)
				}
				return
			}
			if !ok || got != *test.want {
				t.Fatalf("CalculateFill() = %+v, %t; want %+v, true", got, ok, *test.want)
			}
		})
	}
}

func TestCalculateFillRejectsInvalidInputs(t *testing.T) {
	policy := ExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}
	tests := []struct {
		name   string
		policy ExecutionPolicy
		input  FillInput
	}{
		{"invalid side", policy, FillInput{Side: "HOLD", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0"}},
		{"fractional remaining", policy, FillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "1.5", Cash: "10000", ConsumedCapacity: "0"}},
		{"fractional consumed capacity", policy, FillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0.5"}},
		{"fractional position", policy, FillInput{Side: "SELL", Open: "100", Volume: "5", RemainingQuantity: "10", PositionQuantity: "1.5", ConsumedCapacity: "0"}},
		{"zero-capacity fractional position", policy, FillInput{Side: "SELL", Open: "100", Volume: "0", RemainingQuantity: "10", PositionQuantity: "1.5", ConsumedCapacity: "0"}},
		{"consumed capacity above capacity", policy, FillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "3"}},
		{"slippage policy upper bound", ExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10000", MaxParticipation: "0.5"}, FillInput{Side: "SELL", Open: "100", Volume: "5", RemainingQuantity: "10", PositionQuantity: "10", ConsumedCapacity: "0"}},
		{"non-positive open", policy, FillInput{Side: "SELL", Open: "0", Volume: "5", RemainingQuantity: "10", PositionQuantity: "10", ConsumedCapacity: "0"}},
		{"negative sell proceeds", ExecutionPolicy{Fee: "1000", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}, FillInput{Side: "SELL", Open: "100", Volume: "5", RemainingQuantity: "10", PositionQuantity: "10", ConsumedCapacity: "0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := CalculateFill(test.policy, test.input); err == nil {
				t.Fatal("CalculateFill() accepted invalid input")
			}
		})
	}
}

func TestValidCapitalizedQuantity(t *testing.T) {
	for _, test := range []struct {
		raw       string
		allowZero bool
		want      bool
	}{
		{"0", true, true}, {"0", false, false}, {"1", false, true}, {MaxQuantity, false, true},
		{"4611686018427387904", false, false}, {"01", false, false}, {"1.5", false, false}, {"-1", false, false},
	} {
		if got := ValidCapitalizedQuantity(test.raw, test.allowZero); got != test.want {
			t.Errorf("ValidCapitalizedQuantity(%q, %t) = %t; want %t", test.raw, test.allowZero, got, test.want)
		}
	}
}

func TestAccountExactReplayAndFIFOResidual(t *testing.T) {
	account, err := NewAccount("paper-account", "paper-session", "500")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		side string
		fill Fill
	}{
		{"BUY", Fill{Quantity: "2", Fee: "1", Tax: "0", Slippage: "0.2", CashDelta: "-201.2"}},
		{"BUY", Fill{Quantity: "1", Fee: "1", Tax: "0", Slippage: "0.15", CashDelta: "-151.15"}},
		{"SELL", Fill{Quantity: "1", Fee: "1", Tax: "0.12", Slippage: "0.1", CashDelta: "120"}},
		{"SELL", Fill{Quantity: "2", Fee: "1", Tax: "0.3", Slippage: "0.2", CashDelta: "300"}},
	} {
		if err := account.ApplyFill("005930", step.side, step.fill); err != nil {
			t.Fatal(err)
		}
	}
	state, err := account.State()
	want := AccountState{
		AccountRef: "paper-account", PaperAccountingSessionID: "paper-session", Cash: "567.65",
		Lots: map[string][]Lot{}, Fees: "4", Taxes: "0.42", Slippage: "0.65", RealizedPnL: "67.65", CapitalizedFills: 4,
	}
	if err != nil || !reflect.DeepEqual(state, want) || account.Cash() != "567.65" || account.PositionQuantity("005930") != "0" {
		t.Fatalf("account state=%+v cash=%q position=%q err=%v; want %+v", state, account.Cash(), account.PositionQuantity("005930"), err, want)
	}

	residual, err := NewAccount("paper-account", "paper-session", "10000")
	if err != nil {
		t.Fatal(err)
	}
	if err := residual.ApplyFill("005930", "BUY", Fill{Quantity: "3", Fee: "1", Tax: "0", Slippage: "0.3", CashDelta: "-301.3"}); err != nil {
		t.Fatal(err)
	}
	wants := []Lot{{Quantity: "2", Cost: "200.86666667"}, {Quantity: "1", Cost: "100.433333335"}}
	for index := range 3 {
		if err := residual.ApplyFill("005930", "SELL", Fill{Quantity: "1", Fee: "1", Tax: "0.11988", Slippage: "0.12", CashDelta: "118.76012"}); err != nil {
			t.Fatal(err)
		}
		if index < len(wants) {
			got, err := residual.State()
			if err != nil || len(got.Lots["005930"]) != 1 || got.Lots["005930"][0] != wants[index] {
				t.Fatalf("residual %d state=%+v err=%v; want %+v", index+1, got, err, wants[index])
			}
		}
	}
	got, err := residual.State()
	want = AccountState{
		AccountRef: "paper-account", PaperAccountingSessionID: "paper-session", Cash: "10054.98036",
		Lots: map[string][]Lot{}, Fees: "4", Taxes: "0.35964", Slippage: "0.66", RealizedPnL: "54.98036", CapitalizedFills: 4,
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("residual final=%+v err=%v; want %+v", got, err, want)
	}
}

func TestAccountRejectsInvalidMutationAtomically(t *testing.T) {
	if _, err := NewAccount("paper-account", "paper-session", "-1"); err == nil || err.Error() != "paper accounting starting cash is invalid" {
		t.Fatalf("negative starting cash error = %v", err)
	}

	account, err := NewAccount("paper-account", "paper-session", "100")
	if err != nil {
		t.Fatal(err)
	}
	before, err := account.State()
	if err != nil {
		t.Fatal(err)
	}
	err = account.ApplyFill("005930", "BUY", Fill{Quantity: "2", Fee: "1", Tax: "0", Slippage: "0", CashDelta: "-101"})
	after, stateErr := account.State()
	if err == nil || err.Error() != "paper fill makes cash negative" || stateErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("negative cash error=%v stateErr=%v before=%+v after=%+v", err, stateErr, before, after)
	}

	if err := account.ApplyFill("005930", "BUY", Fill{Quantity: "1", Fee: "1", Tax: "0", Slippage: "0", CashDelta: "-51"}); err != nil {
		t.Fatal(err)
	}
	before, err = account.State()
	if err != nil {
		t.Fatal(err)
	}
	err = account.ApplyFill("005930", "SELL", Fill{Quantity: "2", Fee: "1", Tax: "0", Slippage: "0", CashDelta: "60"})
	after, stateErr = account.State()
	if err == nil || err.Error() != "paper SELL exceeds FIFO lots" || stateErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("oversell error=%v stateErr=%v before=%+v after=%+v", err, stateErr, before, after)
	}

	for _, test := range []struct {
		name string
		side string
		fill Fill
		want string
	}{
		{"invalid side", "HOLD", Fill{Quantity: "1", Fee: "0", Tax: "0", Slippage: "0", CashDelta: "0"}, "paper fill side is invalid"},
		{"zero quantity", "BUY", Fill{Quantity: "0", Fee: "0", Tax: "0", Slippage: "0", CashDelta: "0"}, "paper fill quantity is invalid"},
		{"negative quantity", "BUY", Fill{Quantity: "-1", Fee: "0", Tax: "0", Slippage: "0", CashDelta: "0"}, "paper fill quantity is invalid"},
		{"negative fee", "BUY", Fill{Quantity: "1", Fee: "-1", Tax: "0", Slippage: "0", CashDelta: "0"}, "paper fill fee is invalid"},
		{"negative tax", "BUY", Fill{Quantity: "1", Fee: "0", Tax: "-1", Slippage: "0", CashDelta: "0"}, "paper fill tax is invalid"},
		{"negative slippage", "BUY", Fill{Quantity: "1", Fee: "0", Tax: "0", Slippage: "-1", CashDelta: "0"}, "paper fill slippage is invalid"},
		{"BUY nonzero tax", "BUY", Fill{Quantity: "1", Fee: "0", Tax: "0.1", Slippage: "0", CashDelta: "-1"}, "paper BUY tax must be zero"},
		{"BUY zero cash delta", "BUY", Fill{Quantity: "1", Fee: "0", Tax: "0", Slippage: "0", CashDelta: "0"}, "paper BUY cash delta must be negative"},
		{"BUY positive cash delta", "BUY", Fill{Quantity: "1", Fee: "0", Tax: "0", Slippage: "0", CashDelta: "1"}, "paper BUY cash delta must be negative"},
		{"SELL negative cash delta", "SELL", Fill{Quantity: "1", Fee: "0", Tax: "0", Slippage: "0", CashDelta: "-1"}, "paper SELL cash delta must be non-negative"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, err := account.State()
			if err != nil {
				t.Fatal(err)
			}
			err = account.ApplyFill("005930", test.side, test.fill)
			after, stateErr := account.State()
			if err == nil || err.Error() != test.want || stateErr != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("error=%v stateErr=%v before=%+v after=%+v", err, stateErr, before, after)
			}
		})
	}
}

func TestPackageHasNoInfrastructureImports(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"errors": true, "math/big": true, "omni-folio/services/core/internal/exact": true,
	}
	seen := map[string]bool{}
	for name, file := range packages["paperdomain"].Files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !allowed[path] {
				t.Fatalf("paperdomain import %q is outside the exact allowlist", path)
			}
			seen[path] = true
		}
	}
	if !reflect.DeepEqual(seen, allowed) {
		t.Fatalf("paperdomain imports = %v; want exact allowlist %v", seen, allowed)
	}
}

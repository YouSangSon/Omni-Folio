package paperdomain

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPaperResearchFillContract(t *testing.T) {
	data, err := os.ReadFile("../../../../contracts/fixtures/paper-fill-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name   string `json:"name"`
			Policy struct {
				Fee              string `json:"fee"`
				Tax              string `json:"tax"`
				SlippageBPS      string `json:"slippage_bps"`
				MaxParticipation string `json:"max_participation"`
			} `json:"policy"`
			Input struct {
				Side              string `json:"side"`
				Open              string `json:"open"`
				Volume            string `json:"volume"`
				RemainingQuantity string `json:"remaining_quantity"`
				Cash              string `json:"cash"`
				PositionQuantity  string `json:"position_quantity"`
				ConsumedCapacity  string `json:"consumed_capacity"`
			} `json:"input"`
			Expected *struct {
				Quantity       string `json:"quantity"`
				ReferencePrice string `json:"reference_price"`
				Price          string `json:"price"`
				Notional       string `json:"notional"`
				Fee            string `json:"fee"`
				Tax            string `json:"tax"`
				Slippage       string `json:"slippage"`
				CashDelta      string `json:"cash_delta"`
			} `json:"expected"`
			Error bool `json:"error"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("paper fill contract has no cases")
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			got, ok, err := CalculateFill(ExecutionPolicy{
				Fee: test.Policy.Fee, Tax: test.Policy.Tax, SlippageBPS: test.Policy.SlippageBPS,
				MaxParticipation: test.Policy.MaxParticipation,
			}, FillInput{
				Side: test.Input.Side, Open: test.Input.Open, Volume: test.Input.Volume,
				RemainingQuantity: test.Input.RemainingQuantity, Cash: test.Input.Cash,
				PositionQuantity: test.Input.PositionQuantity, ConsumedCapacity: test.Input.ConsumedCapacity,
			})
			if (err != nil) != test.Error {
				t.Fatalf("CalculateFill() = %+v, %t, %v; want error=%t", got, ok, err, test.Error)
			}
			if test.Expected == nil {
				if ok || got != (Fill{}) {
					t.Fatalf("CalculateFill() = %+v, %t, %v; want zero fill, false", got, ok, err)
				}
				return
			}
			want := Fill{
				Quantity: test.Expected.Quantity, ReferencePrice: test.Expected.ReferencePrice,
				Price: test.Expected.Price, Notional: test.Expected.Notional, Fee: test.Expected.Fee,
				Tax: test.Expected.Tax, Slippage: test.Expected.Slippage, CashDelta: test.Expected.CashDelta,
			}
			if !ok || got != want {
				t.Fatalf("CalculateFill() = %+v, %t, %v; want %+v, true, nil", got, ok, err, want)
			}
		})
	}
}

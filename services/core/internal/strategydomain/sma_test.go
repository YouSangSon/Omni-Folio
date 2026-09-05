package strategydomain

import "testing"

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

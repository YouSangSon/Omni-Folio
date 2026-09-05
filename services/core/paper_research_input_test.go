package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestValidatePaperResearchInputAcceptsBoundResearchWithUniqueExtraColumns(t *testing.T) {
	raw := []byte("dataset,bar_at,symbol,open,high,low,close,volume,note\n" +
		"train,2026-01-01T00:00:00Z,005930,10,11,9,10,100,a\n" +
		"train,2026-01-02T00:00:00Z,005930,11,12,10,11,0,b\n")
	if err := validatePaperResearchInput(raw, researchInputSHA(raw), "005930", "2026-01-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// The immutable research sample may be reused for later proposals; validation creates no consumption state.
	if err := validatePaperResearchInput(raw, researchInputSHA(raw), "005930", "2026-06-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePaperResearchInputRejectsMalformedOrUnboundData(t *testing.T) {
	valid := []byte("bar_at,symbol,open,high,low,close,volume\n2026-01-01T00:00:00Z,005930,10,11,9,10,100\n")
	tests := map[string]struct {
		raw, sha, symbol, asOf string
	}{
		"empty":              {"", researchInputSHA(nil), "005930", "2026-01-02T00:00:00Z"},
		"header only":        {"bar_at,symbol,open,high,low,close,volume\n", researchInputSHA([]byte("bar_at,symbol,open,high,low,close,volume\n")), "005930", "2026-01-02T00:00:00Z"},
		"missing column":     {strings.Replace(string(valid), ",volume", "", 1), "", "005930", "2026-01-02T00:00:00Z"},
		"duplicate header":   {strings.Replace(string(valid), "volume", "symbol", 1), "", "005930", "2026-01-02T00:00:00Z"},
		"malformed CSV":      {"bar_at,symbol,open,high,low,close,volume\n\"unterminated", "", "005930", "2026-01-02T00:00:00Z"},
		"wrong field count":  {string(valid) + "2026-01-02T00:00:00Z,005930,10\n", "", "005930", "2026-01-03T00:00:00Z"},
		"bad target symbol":  {string(valid), researchInputSHA(valid), "AAPL", "2026-01-02T00:00:00Z"},
		"mismatched symbol":  {string(valid), researchInputSHA(valid), "000660", "2026-01-02T00:00:00Z"},
		"mixed symbol":       {string(valid) + "2026-01-02T00:00:00Z,000660,10,11,9,10,100\n", "", "005930", "2026-01-03T00:00:00Z"},
		"noncanonical price": {strings.Replace(string(valid), ",10,11,", ",010,11,", 1), "", "005930", "2026-01-02T00:00:00Z"},
		"invalid OHLC":       {strings.Replace(string(valid), ",10,11,9,10,", ",10,9,9,10,", 1), "", "005930", "2026-01-02T00:00:00Z"},
		"negative volume":    {strings.Replace(string(valid), ",100\n", ",-1\n", 1), "", "005930", "2026-01-02T00:00:00Z"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			raw := []byte(test.raw)
			sha := test.sha
			if sha == "" {
				sha = researchInputSHA(raw)
			}
			if err := validatePaperResearchInput(raw, sha, test.symbol, test.asOf); err == nil {
				t.Fatal("invalid research input was accepted")
			}
		})
	}
}

func TestValidatePaperResearchInputBindsExactBytesAndCapsInput(t *testing.T) {
	raw := []byte("bar_at,symbol,open,high,low,close,volume\n2026-01-01T00:00:00Z,005930,10,11,9,10,100\n")
	changed := append(append([]byte(nil), raw...), '\n')
	if err := validatePaperResearchInput(changed, researchInputSHA(raw), "005930", "2026-01-02T00:00:00Z"); err == nil {
		t.Fatal("changed bytes were accepted under the claimed original hash")
	}
	if err := validatePaperResearchInput(make([]byte, maxMarketDataBytes+1), researchInputSHA(make([]byte, maxMarketDataBytes+1)), "005930", "2026-01-02T00:00:00Z"); err == nil {
		t.Fatal("oversized research input was accepted")
	}
	rows := strings.Repeat("2026-01-01T00:00:00Z,005930,10,11,9,10,100\n", maxMarketDataRows+1)
	tooMany := []byte("bar_at,symbol,open,high,low,close,volume\n" + rows)
	if err := validatePaperResearchInput(tooMany, researchInputSHA(tooMany), "005930", "2026-01-02T00:00:00Z"); err == nil {
		t.Fatal("research input over the row ceiling was accepted")
	}
}

func TestValidatePaperResearchInputEnforcesOrderedUTCFrontier(t *testing.T) {
	header := "bar_at,symbol,open,high,low,close,volume\n"
	row1 := "2026-01-01T00:00:00Z,005930,10,11,9,10,100\n"
	row2 := "2026-01-02T00:00:00Z,005930,10,11,9,10,100\n"
	tests := map[string]struct{ body, asOf string }{
		"unordered":         {row2 + row1, "2026-01-03T00:00:00Z"},
		"duplicate":         {row1 + row1, "2026-01-03T00:00:00Z"},
		"offset research":   {strings.Replace(row1, "T00:00:00Z", "T09:00:00+09:00", 1), "2026-01-03T00:00:00Z"},
		"fraction research": {strings.Replace(row1, "T00:00:00Z", "T00:00:00.1Z", 1), "2026-01-03T00:00:00Z"},
		"equal frontier":    {row1 + row2, "2026-01-02T00:00:00Z"},
		"before frontier":   {row1 + row2, "2026-01-01T00:00:00Z"},
		"offset proposal":   {row1, "2026-01-03T09:00:00+09:00"},
		"fraction proposal": {row1, "2026-01-03T00:00:00.1Z"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			raw := []byte(header + test.body)
			if err := validatePaperResearchInput(raw, researchInputSHA(raw), "005930", test.asOf); err == nil {
				t.Fatal("invalid research frontier was accepted")
			}
		})
	}
}

func researchInputSHA(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

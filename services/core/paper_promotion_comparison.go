package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"omni-folio/services/core/internal/paperdomain"
	"omni-folio/services/core/internal/researchdomain"
)

const paperPromotionPolicyVersion = "paper-forward-comparison.v1"

type paperPromotionComparison struct {
	PolicyVersion string                 `json:"policy_version"`
	Windows       []paperPromotionWindow `json:"windows"`
	Decision      string                 `json:"decision"`
	Reason        string                 `json:"reason"`
}

type paperPromotionWindow struct {
	Start      string                 `json:"start"`
	End        string                 `json:"end"`
	Candidate  researchdomain.Metrics `json:"candidate"`
	Incumbent  researchdomain.Metrics `json:"incumbent"`
	BuyAndHold researchdomain.Metrics `json:"buy_and_hold"`
}

func parsePaperPromotionBars(ctx context.Context, raw []byte) (string, []researchdomain.Bar, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if len(raw) == 0 || len(raw) > maxMarketDataBytes || !utf8.Valid(raw) {
		return "", nil, errors.New("paper promotion bars require UTF-8 CSV of at most 1 MiB")
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = 7
	header, err := reader.Read()
	if err != nil || !slices.Equal(header, []string{"bar_at", "symbol", "open", "high", "low", "close", "volume"}) {
		return "", nil, errors.New("paper promotion bars require the canonical seven-column header")
	}
	var symbol string
	var previous time.Time
	bars := make([]researchdomain.Bar, 0)
	for {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("paper promotion bars CSV: %w", err)
		}
		if len(bars) == maxMarketDataRows {
			return "", nil, errors.New("paper promotion bars must not exceed 500 rows")
		}
		if !kiwoomStockPattern.MatchString(row[1]) || symbol != "" && row[1] != symbol {
			return "", nil, errors.New("paper promotion bars require exactly one six-digit stock symbol")
		}
		at, err := time.Parse(time.RFC3339, row[0])
		if err != nil || at.UTC().Format(time.RFC3339) != row[0] || len(bars) > 0 && !at.After(previous) {
			return "", nil, errors.New("paper promotion bars require strictly increasing whole-second UTC timestamps")
		}
		whole, fraction, _ := strings.Cut(row[5], ".")
		if len(whole) > 12 || len(fraction) > 8 || len(row[5]) > 21 {
			return "", nil, errors.New("paper promotion close is outside the paper SMA bounds")
		}
		if err := validateMarketDataValues(row[2], row[3], row[4], row[5], row[6]); err != nil {
			return "", nil, fmt.Errorf("paper promotion bars: %w", err)
		}
		symbol, previous = row[1], at
		bars = append(bars, researchdomain.Bar{At: row[0], Open: row[2], Close: row[5], Volume: row[6]})
	}
	if len(bars) == 0 {
		return "", nil, errors.New("paper promotion bars require at least one row")
	}
	return symbol, bars, nil
}

func paperPromotionStrategy(evidence StrategyEvidence) (researchdomain.Strategy, error) {
	invalid := errors.New("paper promotion requires a bound long_only_sma_crossover 1.0.0 with capitalized shares and bounded windows")
	if evidence.StrategyName != "long_only_sma_crossover" || evidence.StrategyVersion != "1.0.0" ||
		len(evidence.artifactJSON) == 0 || len(evidence.artifactJSON) > maxBodyBytes || !utf8.ValidString(evidence.artifactJSON) {
		return researchdomain.Strategy{}, invalid
	}
	decoder := json.NewDecoder(strings.NewReader(evidence.artifactJSON))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return researchdomain.Strategy{}, invalid
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return researchdomain.Strategy{}, invalid
	}
	manifest, _ := mapField(result, "manifest")
	strategy, _ := mapField(manifest, "strategy")
	parameters, _ := mapField(strategy, "parameters")
	fast, fastOK := researchReportInteger(parameters["fast_window"])
	slow, slowOK := researchReportInteger(parameters["slow_window"])
	quantity := stringField(parameters, "quantity")
	if !exactKeys(strategy, "name", "version", "parameters", "parameter_hash") ||
		stringField(strategy, "name") != evidence.StrategyName || stringField(strategy, "version") != evidence.StrategyVersion ||
		!exactKeys(parameters, "fast_window", "slow_window", "quantity") || !fastOK || !slowOK || slow <= fast || slow > 365 ||
		!paperdomain.ValidCapitalizedQuantity(quantity, false) {
		return researchdomain.Strategy{}, invalid
	}
	canonicalParameters, err := strategyCanonicalJSON(parameters)
	if err != nil {
		return researchdomain.Strategy{}, err
	}
	hash := sha256.Sum256(canonicalParameters)
	parameterSHA := hex.EncodeToString(hash[:])
	if stringField(strategy, "parameter_hash") != parameterSHA || evidence.ParameterSHA256 != parameterSHA {
		return researchdomain.Strategy{}, invalid
	}
	return researchdomain.Strategy{Quantity: quantity, FastWindow: fast, SlowWindow: slow}, nil
}

func comparePaperPromotion(ctx context.Context, bars []researchdomain.Bar, candidate, incumbent researchdomain.Strategy, execution researchdomain.Execution) (paperPromotionComparison, error) {
	if err := ctx.Err(); err != nil {
		return paperPromotionComparison{}, err
	}
	for _, strategy := range []researchdomain.Strategy{candidate, incumbent} {
		if strategy.FastWindow <= 0 || strategy.SlowWindow <= strategy.FastWindow || strategy.SlowWindow > 365 || !paperdomain.ValidCapitalizedQuantity(strategy.Quantity, false) {
			return paperPromotionComparison{}, errors.New("paper promotion strategy is outside the paper SMA bounds")
		}
	}
	if candidate.Quantity != incumbent.Quantity {
		return paperPromotionComparison{}, errors.New("paper promotion strategies must use identical quantities")
	}
	commonWarmup := int(max(candidate.SlowWindow, incumbent.SlowWindow))
	if len(bars) != commonWarmup+3*30 {
		return paperPromotionComparison{}, errors.New("paper promotion requires exactly the common SMA warmup and three 30-bar windows")
	}
	comparison := paperPromotionComparison{PolicyVersion: paperPromotionPolicyVersion, Windows: make([]paperPromotionWindow, 0, 3)}
	for index := range 3 {
		start := commonWarmup + index*30
		windowBars, warmup := bars[start:start+30], bars[start-commonWarmup:start]
		window := paperPromotionWindow{Start: windowBars[0].At, End: windowBars[len(windowBars)-1].At}
		var err error
		window.Candidate, _, err = researchdomain.SimulateFold(ctx, windowBars, warmup, candidate, execution, false)
		if err != nil {
			return paperPromotionComparison{}, fmt.Errorf("paper promotion window %d candidate: %w", index+1, err)
		}
		window.Incumbent, _, err = researchdomain.SimulateFold(ctx, windowBars, warmup, incumbent, execution, false)
		if err != nil {
			return paperPromotionComparison{}, fmt.Errorf("paper promotion window %d incumbent: %w", index+1, err)
		}
		window.BuyAndHold, _, err = researchdomain.SimulateFold(ctx, windowBars, warmup, candidate, execution, true)
		if err != nil {
			return paperPromotionComparison{}, fmt.Errorf("paper promotion window %d buy-and-hold: %w", index+1, err)
		}
		comparison.Windows = append(comparison.Windows, window)
	}
	var err error
	comparison.Decision, comparison.Reason, err = evaluatePaperPromotionWindows(comparison.Windows)
	if err != nil {
		return paperPromotionComparison{}, err
	}
	return comparison, nil
}

func evaluatePaperPromotionWindows(windows []paperPromotionWindow) (string, string, error) {
	if len(windows) != 3 {
		return "", "", errors.New("paper promotion decision requires three windows")
	}
	for _, window := range windows {
		for _, trades := range []string{window.Candidate.TradeCount, window.Incumbent.TradeCount} {
			order, err := paperPromotionMetricOrder(trades, "1")
			if err != nil {
				return "", "", err
			}
			if order < 0 {
				return "HOLD", "insufficient_round_trips", nil
			}
		}
		for _, gate := range []struct {
			candidate, bound, reason string
			rejectedOrder            int
		}{
			{window.Candidate.AfterCostReturn, "0", "candidate_negative_return", -1},
			{window.Candidate.AfterCostReturn, window.Incumbent.AfterCostReturn, "candidate_return_below_incumbent", -1},
			{window.Candidate.MaxDrawdown, window.Incumbent.MaxDrawdown, "candidate_drawdown_above_incumbent", 1},
			{window.Candidate.Turnover, window.Incumbent.Turnover, "candidate_turnover_above_incumbent", 1},
		} {
			order, err := paperPromotionMetricOrder(gate.candidate, gate.bound)
			if err != nil {
				return "", "", err
			}
			if order == gate.rejectedOrder {
				return "HOLD", gate.reason, nil
			}
		}
	}
	holdout := windows[2]
	for _, gate := range []struct{ bound, reason string }{
		{holdout.Incumbent.AfterCostReturn, "holdout_not_better_than_incumbent"},
		{holdout.BuyAndHold.AfterCostReturn, "holdout_not_better_than_buy_and_hold"},
	} {
		order, err := paperPromotionMetricOrder(holdout.Candidate.AfterCostReturn, gate.bound)
		if err != nil {
			return "", "", err
		}
		if order <= 0 {
			return "HOLD", gate.reason, nil
		}
	}
	return "PROMOTE", "all_forward_gates_passed", nil
}

func paperPromotionMetricOrder(left, right string) (int, error) {
	l, err := parseDecimal(left)
	if err != nil {
		return 0, err
	}
	r, err := parseDecimal(right)
	if err != nil {
		return 0, err
	}
	return l.Cmp(r), nil
}

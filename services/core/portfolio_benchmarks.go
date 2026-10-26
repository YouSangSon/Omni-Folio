package main

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"
)

const portfolioBenchmarksPolicy = "retrospective_latest_index_24h_native_and_krw_v1"
const portfolioBenchmarksOwnerFXPolicy = "retrospective_latest_index_24h_native_and_krw_v2"

// shortcut: compare up to32 latest IDs; add explicit selection or paging when owners need more.
const maxPortfolioBenchmarks = 32

type BenchmarkRateResult struct {
	Status string  `json:"status"`
	Rate   *string `json:"rate"`
	Issue  *string `json:"issue"`
}

type BenchmarkFXMark struct {
	ObservedAt    string `json:"observed_at"`
	Rate          string `json:"rate"`
	Source        string `json:"source,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	Sample        *bool  `json:"sample,omitempty"`
}

func publicBenchmarkFX(fx *FXObservation) *BenchmarkFXMark {
	mark := &BenchmarkFXMark{ObservedAt: fx.ObservedAt, Rate: fx.Rate}
	if fx.Source == ownerFXSource {
		mark.Source, mark.SourceVersion, mark.Sample = fx.Source, fx.ownerVersion, &fx.ownerSample
	}
	return mark
}

type PortfolioBenchmark struct {
	Series                     *BenchmarkSeriesReceipt `json:"series"`
	Sample                     bool                    `json:"sample"`
	Start                      *BenchmarkIndexPoint    `json:"start"`
	End                        *BenchmarkIndexPoint    `json:"end"`
	StartFX                    *BenchmarkFXMark        `json:"start_fx"`
	EndFX                      *BenchmarkFXMark        `json:"end_fx"`
	Native                     BenchmarkRateResult     `json:"native"`
	KRW                        BenchmarkRateResult     `json:"krw"`
	DifferencePercentagePoints *string                 `json:"difference_percentage_points"`
}

type PortfolioBenchmarks struct {
	PolicyVersion            string               `json:"policy_version"`
	MaxObservationAgeSeconds int64                `json:"max_observation_age_seconds"`
	Portfolio                *PortfolioTWR        `json:"portfolio"`
	Benchmarks               []PortfolioBenchmark `json:"benchmarks"`
	Issues                   []APIError           `json:"issues"`
}

func unavailableBenchmarkRate(issue string) BenchmarkRateResult {
	return BenchmarkRateResult{Status: "unavailable", Issue: &issue}
}

func benchmarkRate(growth *big.Rat) BenchmarkRateResult {
	rate, err := formatDecimal(quantizeHalfEven(new(big.Rat).Sub(growth, big.NewRat(1, 1)), 12))
	if err != nil || len(rate) > 128 {
		return unavailableBenchmarkRate("numerical_limit")
	}
	return BenchmarkRateResult{Status: "calculated", Rate: &rate}
}

func benchmarkDifference(portfolio, benchmark string) (*string, error) {
	p, err := parseDecimal(portfolio)
	if err != nil {
		return nil, err
	}
	b, err := parseDecimal(benchmark)
	if err != nil {
		return nil, err
	}
	// Difference of published rates, in percentage points; no second rounding.
	text, err := formatDecimal(new(big.Rat).Mul(new(big.Rat).Sub(p, b), big.NewRat(100, 1)))
	if err != nil || len(text) > 132 {
		return nil, errors.New("benchmark difference exceeds its declared bounds")
	}
	return &text, nil
}

func latestBenchmarkPoint(points []BenchmarkIndexPoint, at time.Time) *BenchmarkIndexPoint {
	key := strings.TrimSuffix(at.UTC().Format(time.RFC3339Nano), "Z")
	index := sort.Search(len(points), func(i int) bool {
		return strings.TrimSuffix(points[i].ObservedAt, "Z") > key
	})
	if index == 0 {
		return nil
	}
	point := points[index-1]
	return &point
}

func comparePortfolioBenchmark(record benchmarkSeriesRecord, portfolio *PortfolioTWR, marks twrMarks, asOf time.Time) (PortfolioBenchmark, error) {
	row := PortfolioBenchmark{Series: benchmarkReceipt(&record), Sample: record.Input.Sample, Native: unavailableBenchmarkRate("portfolio_unavailable"), KRW: unavailableBenchmarkRate("portfolio_unavailable")}
	if portfolio.Status != "calculated" {
		return row, nil
	}
	if portfolio.PeriodStart == nil || portfolio.CumulativeRate == nil {
		return row, errors.New("calculated TWR omitted its comparison inputs")
	}
	start, err := time.Parse(time.RFC3339Nano, *portfolio.PeriodStart)
	if err != nil {
		return row, err
	}
	row.Start = latestBenchmarkPoint(record.Input.Points, start)
	row.End = latestBenchmarkPoint(record.Input.Points, asOf)
	issue := ""
	if row.Start == nil || row.End == nil {
		issue = "missing_benchmark_point"
	} else {
		startObserved, _ := time.Parse(time.RFC3339Nano, row.Start.ObservedAt)
		endObserved, _ := time.Parse(time.RFC3339Nano, row.End.ObservedAt)
		if start.Sub(startObserved) > cashFXMaxAge || asOf.Sub(endObserved) > cashFXMaxAge {
			issue = "stale_benchmark_point"
		} else if row.Start.Level == "0" {
			issue = "zero_benchmark_start"
		}
	}
	if issue != "" {
		row.Native, row.KRW = unavailableBenchmarkRate(issue), unavailableBenchmarkRate(issue)
		return row, nil
	}
	startLevel, err := parseDecimal(row.Start.Level)
	if err != nil {
		return row, err
	}
	endLevel, err := parseDecimal(row.End.Level)
	if err != nil {
		return row, err
	}
	row.Native = benchmarkRate(new(big.Rat).Quo(endLevel, startLevel))
	if record.Input.Currency == "KRW" {
		row.KRW = row.Native
	} else {
		startFX := historicalXIRRFX(marks.fx, marks.fxTimes, record.Input.Currency, start, asOf)
		endFX := historicalXIRRFX(marks.fx, marks.fxTimes, record.Input.Currency, asOf, asOf)
		if startFX != nil {
			row.StartFX = publicBenchmarkFX(startFX)
			row.Sample = row.Sample || fxIsSample(startFX)
		}
		if endFX != nil {
			row.EndFX = publicBenchmarkFX(endFX)
			row.Sample = row.Sample || fxIsSample(endFX)
		}
		if startFX == nil || endFX == nil {
			row.KRW = unavailableBenchmarkRate("missing_historical_fx")
		} else if start.Sub(marks.fxTimes[fxTimeKey(*startFX)][0]) > cashFXMaxAge || asOf.Sub(marks.fxTimes[fxTimeKey(*endFX)][0]) > cashFXMaxAge {
			row.KRW = unavailableBenchmarkRate("stale_historical_fx")
		} else {
			startRate, err := parseDecimal(startFX.Rate)
			if err != nil {
				return row, err
			}
			endRate, err := parseDecimal(endFX.Rate)
			if err != nil {
				return row, err
			}
			// Convert each endpoint before taking the ratio; never convert a rounded native rate.
			row.KRW = benchmarkRate(new(big.Rat).Quo(new(big.Rat).Mul(endLevel, endRate), new(big.Rat).Mul(startLevel, startRate)))
		}
	}
	if row.KRW.Rate != nil {
		row.DifferencePercentagePoints, err = benchmarkDifference(*portfolio.CumulativeRate, *row.KRW.Rate)
		if err != nil {
			return row, err
		}
	}
	return row, nil
}

func (s *Service) portfolioBenchmarks(ctx context.Context) (*PortfolioBenchmarks, *appError) {
	if s == nil || s.db == nil || s.now == nil {
		return nil, internalError(errors.New("benchmark comparison service unavailable"))
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer tx.Rollback()
	asOf := s.now().UTC()
	inputs, err := readValuationFXInputs(ctx, tx, asOf)
	if err != nil {
		return nil, internalError(err)
	}
	prices, err := readValuationPriceInputs(ctx, tx, asOf)
	if err != nil {
		return nil, internalError(err)
	}
	portfolio, appErr := s.portfolioTWRTx(ctx, tx, asOf, inputs, prices)
	if appErr != nil {
		return nil, appErr
	}
	// Integrity proof covers the full journal even when TWR or a calculation is unavailable.
	proof, err := proveBenchmarkSeriesRecovery(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	marks := twrMarks{fx: inputs.observations, fxTimes: inputs.times}
	r := &PortfolioBenchmarks{PolicyVersion: portfolioBenchmarksPolicy, MaxObservationAgeSeconds: int64(cashFXMaxAge / time.Second), Portfolio: portfolio, Benchmarks: []PortfolioBenchmark{}, Issues: []APIError{}}
	cutoff := asOf.Format(time.RFC3339Nano)
	rows, err := tx.QueryContext(ctx, `SELECT b.sequence,b.series_json,b.series_sha256,b.recorded_at
        FROM benchmark_series_versions b JOIN (
            SELECT benchmark_id,MAX(sequence) AS sequence FROM benchmark_series_versions
            WHERE rtrim(recorded_at,'Z')<=rtrim(?,'Z') GROUP BY benchmark_id
        ) latest ON latest.sequence=b.sequence
        ORDER BY b.benchmark_id LIMIT ?`, cutoff, maxPortfolioBenchmarks+1)
	if err != nil {
		return nil, internalError(err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(r.Benchmarks) == maxPortfolioBenchmarks {
			r.Benchmarks = []PortfolioBenchmark{}
			r.Issues = append(r.Issues, APIError{Code: "benchmark_limit", Message: "benchmark comparison exceeds the declared index count limit"})
			break
		}
		var record benchmarkSeriesRecord
		var payload string
		if err := rows.Scan(&record.Sequence, &payload, &record.DataSHA256, &record.RecordedAt); err != nil {
			return nil, internalError(err)
		}
		// The whole journal was proved in this transaction; decode only the selected versions again.
		input, _, _, err := decodeBenchmarkSeries(ctx, []byte(payload))
		if err != nil {
			return nil, internalError(err)
		}
		record.Input = *input
		row, err := comparePortfolioBenchmark(record, portfolio, marks, asOf)
		if err != nil {
			return nil, internalError(err)
		}
		r.Benchmarks = append(r.Benchmarks, row)
	}
	if err := rows.Err(); err != nil {
		return nil, internalError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, internalError(err)
	}
	if len(r.Benchmarks) == 0 && len(r.Issues) == 0 {
		code := "no_benchmarks"
		if proof.Versions != 0 {
			code = "no_known_benchmarks"
		}
		r.Issues = append(r.Issues, APIError{Code: code, Message: "no benchmark version is available at the report cutoff"})
	}
	if usesOwnerFX(portfolio.Issues) {
		r.PolicyVersion = portfolioBenchmarksOwnerFXPolicy
	}
	for _, row := range r.Benchmarks {
		if row.StartFX != nil && row.StartFX.Source == ownerFXSource || row.EndFX != nil && row.EndFX.Source == ownerFXSource {
			r.PolicyVersion = portfolioBenchmarksOwnerFXPolicy
		}
	}
	if usesOwnerPrice(portfolio.Issues) {
		r.PolicyVersion = "retrospective_latest_index_24h_native_and_krw_v3"
	}
	if err := ctx.Err(); err != nil {
		return nil, internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return r, nil
}

func (s *Service) handlePortfolioBenchmarks(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "portfolio benchmark comparison does not accept query parameters"}})
		return
	}
	report, appErr := s.portfolioBenchmarks(r.Context())
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

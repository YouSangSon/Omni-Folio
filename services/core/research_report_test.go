package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResearchReportEmptyAndHTTPBoundary(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/v1/research/report", 200},
		{"POST", "/v1/research/report", 405},
		{"GET", "/v1/research/report?account=private", 400},
	} {
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var body map[string]any
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || body["schema_version"] != "research-report.v1" || body["mode"] != "research_artifact_only" || body["metrics_verification"] != "reported_not_recomputed" || body["report"] != nil || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("empty contract: %s", w.Body.String())
			}
		}
	}
}

func TestResearchReportPythonArtifactAndLatestRegistration(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	artifact := strategyArtifact(t, nil)
	first, err := svc.registerStrategyEvidence(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.selectPaperCandidate(context.Background(), first.ResultSHA256, noStrategySelectionEvent); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/report", nil))
	if w.Code != 200 {
		t.Fatalf("report: %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	report := body["report"].(map[string]any)
	if report["fast_window"] != float64(2) || report["slow_window"] != float64(5) || report["quantity"] != "10" || report["fold_count"] != float64(2) || report["promotion_target"] != "paper_candidate" {
		t.Fatalf("wrong candidate: %v", report)
	}
	if report["walk_forward"].(map[string]any)["after_cost_return"] != "0.014734027" || report["holdout"].(map[string]any)["round_trips"] != "1" || report["buy_and_hold"].(map[string]any)["after_cost_return"] != "0.007727027" {
		t.Fatalf("wrong reported metrics: %v", report)
	}
	for _, private := range []string{first.ResultSHA256, first.ArtifactSHA256, "experiment_id", "input_sha256", "final_holdout_trades", "data_version", "account_ref"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private evidence exposed: %s", private)
		}
	}
	// A failed later research result must be visible even while the first remains selected.
	svc.now = func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }
	later := strategyArtifact(t, func(config map[string]any) {
		config["experiment_id"] = "later-private-experiment"
		config["promotion"].(map[string]any)["minimum_holdout_after_cost_return"] = "100"
	})
	if _, err := svc.registerStrategyEvidence(context.Background(), later); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/report", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"promotion_target":"no_promotion"`) || strings.Contains(w.Body.String(), "later-private") {
		t.Fatalf("newest failed research not shown: %d %s", w.Code, w.Body.String())
	}
}

func TestResearchReportRejectsUnsupportedNewestWithoutFallback(t *testing.T) {
	valid := strategyArtifact(t, nil)
	for name, mutate := range map[string]func(map[string]any){
		"metric injection": func(a map[string]any) {
			a["challenger"].(map[string]any)["final_holdout_metrics"].(map[string]any)["after_cost_return"] = "private-secret"
		},
		"huge decimal": func(a map[string]any) {
			a["challenger"].(map[string]any)["final_holdout_metrics"].(map[string]any)["after_cost_return"] = strings.Repeat("9", 129)
		},
		"wrong count definition": func(a map[string]any) {
			a["challenger"].(map[string]any)["walk_forward_metrics"].(map[string]any)["trade_count_definition"] = "fills"
		},
		"parameter mismatch": func(a map[string]any) {
			a["challenger"].(map[string]any)["parameters"].(map[string]any)["fast_window"] = json.Number("3")
		},
		"fold mismatch": func(a map[string]any) {
			a["challenger"].(map[string]any)["walk_forward_folds"].([]any)[0].(map[string]any)["name"] = "private-secret"
		},
		"unsupported version": func(a map[string]any) {
			a["manifest"].(map[string]any)["strategy"].(map[string]any)["version"] = "private-secret"
		},
		"reversed range": func(a map[string]any) {
			a["evaluation"].(map[string]any)["final_holdout"].(map[string]any)["first_bar_at"] = "2027-01-01T00:00:00Z"
		},
		"negative drawdown": func(a map[string]any) {
			a["challenger"].(map[string]any)["final_holdout_metrics"].(map[string]any)["max_drawdown"] = "-0.1"
		},
		"fractional round trips": func(a map[string]any) {
			a["challenger"].(map[string]any)["final_holdout_metrics"].(map[string]any)["trade_count"] = "0.5"
		},
		"unsupported timestamp": func(a map[string]any) {
			a["evaluation"].(map[string]any)["final_holdout"].(map[string]any)["first_bar_at"] = "2026-04-01T09:00:00+09:00"
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			if _, err := svc.registerStrategyEvidence(context.Background(), valid); err != nil {
				t.Fatal(err)
			}
			artifact := rehashedStrategyArtifact(t, valid, mutate)
			if _, err := svc.registerStrategyEvidence(context.Background(), artifact); err != nil {
				t.Fatalf("existing writer contract changed: %v", err)
			}
			w := httptest.NewRecorder()
			svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/report", nil))
			if w.Code != 500 || strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "report") {
				t.Fatalf("invalid report leaked or fell back: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestResearchReportFractionalResearchQuantityAndRegistryCorruption(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	artifact := strategyArtifact(t, func(config map[string]any) { config["strategy"].(map[string]any)["quantity"] = "0.5" })
	if _, err := svc.registerStrategyEvidence(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/report", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"quantity":"0.5"`) {
		t.Fatalf("research quantity rejected: %d %s", w.Code, w.Body.String())
	}
	var trigger string
	if err := svc.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name='strategy_research_evidence' AND sql LIKE '%BEFORE UPDATE%'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER "` + trigger + `"`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE strategy_research_evidence SET artifact_json='private-corrupt-evidence'`); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/report", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "private-corrupt") || strings.Contains(w.Body.String(), "report") {
		t.Fatalf("registry corruption hidden/leaked: %d %s", w.Code, w.Body.String())
	}
}

func TestResearchReportOpenAPIClosedDTOs(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	path := doc["paths"].(map[string]any)["/v1/research/report"].(map[string]any)
	if len(path) != 1 || path["get"] == nil {
		t.Fatal("report must be GET-only")
	}
	for name, dto := range map[string]any{"ResearchReportView": ResearchReportView{}, "StoredResearchReport": StoredResearchReport{}, "ResearchReportRange": ResearchReportRange{}, "ResearchReportMetrics": ResearchReportMetrics{}} {
		encoded, _ := json.Marshal(dto)
		var fields map[string]any
		_ = json.Unmarshal(encoded, &fields)
		schema := doc["components"].(map[string]any)["schemas"].(map[string]any)[name].(map[string]any)
		properties := schema["properties"].(map[string]any)
		if schema["additionalProperties"] != false || len(properties) != len(fields) || len(schema["required"].([]any)) != len(fields) {
			t.Fatalf("%s is not closed", name)
		}
		for key := range fields {
			if properties[key] == nil {
				t.Fatalf("%s undocumented: %s", name, key)
			}
		}
	}
}

func TestResearchReportExtremeReportedLossAndReadOnly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(a map[string]any) {
		metrics := a["challenger"].(map[string]any)["final_holdout_metrics"].(map[string]any)
		metrics["after_cost_return"], metrics["max_drawdown"] = "-1.01902", "1.01902"
	})
	if _, err := svc.registerStrategyEvidence(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := svc.db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/report", nil))
	if err := svc.db.QueryRow("SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"after_cost_return":"-1.01902"`) || before != after {
		t.Fatalf("extreme reported loss or read-only broken: %d %s changes %d/%d", w.Code, w.Body.String(), before, after)
	}
}

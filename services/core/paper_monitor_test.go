package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPaperMonitorEmptyReadOnlyView(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/monitor", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("monitor status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["schema_version"] != "paper-monitor.v1" || body["mode"] != "paper_fixture_only" || body["session_count"] != float64(0) || body["pending_policy_count"] != float64(0) || body["strategy_selected"] != false || body["latest_policy"] != nil {
		t.Fatalf("empty monitor claimed operational evidence: %v", body)
	}
	runner := body["runner"].(map[string]any)
	if runner["state"] != "unowned" || runner["heartbeat_at"] != nil || runner["expires_at"] != nil {
		t.Fatalf("empty monitor claimed runner ownership: %v", runner)
	}
}

func TestPaperMonitorPolicyHaltAndCorruptionRemainDistinct(t *testing.T) {
	for _, close := range []string{"100", "10"} {
		t.Run(close, func(t *testing.T) {
			svc, window := g38EPerformanceWindow(t, []string{"100", close})
			ctx := context.Background()
			claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, window.StrategySelectionEventID, window.StrategyPerformanceID)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/monitor", nil))
			var view PaperMonitor
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.LatestPolicy == nil {
				t.Fatalf("policy missing: %d %s", w.Code, w.Body.String())
			}
			wantDecision, wantState, wantMatch := "HOLD", "lease_recorded", true
			if close == "10" {
				wantDecision, wantState, wantMatch = "HALT_AND_ROLLBACK", "selection_changed", false
			}
			if view.LatestPolicy.Decision != wantDecision || view.LatestPolicy.MatchesCurrentSelection != wantMatch || view.Runner.State != wantState || view.PendingPolicyCount != 1 {
				t.Fatalf("historical policy/unfinished prefix hidden: %+v", view)
			}
			for _, private := range []string{k2aAccountRef, claim.OwnerID, policy.PolicyEventID, window.StrategySelectionEventID, "account_ref", "fencing_token", "cash", "equity"} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatalf("private field leaked: %s", private)
				}
			}
			// Preserve exact schema after tampering so rejection proves historical
			// semantic recovery, not merely a missing trigger.
			var trigger string
			if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='paper_performance_policy_events_no_update'`).Scan(&trigger); err != nil {
				t.Fatal(err)
			}
			policy.SampleCount++
			g38ERewritePolicyEvent(t, svc, *policy)
			if _, err := svc.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			w = httptest.NewRecorder()
			svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/monitor", nil))
			if w.Code != 500 || strings.Contains(w.Body.String(), "latest_policy") || strings.Contains(w.Body.String(), k2aAccountRef) || strings.Contains(w.Body.String(), "sample") {
				t.Fatalf("corrupt policy exposed partial/raw result: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPaperMonitorClosedHTTPContract(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/v1/paper/monitor", 405}, {"GET", "/v1/paper/monitor?account=private", 400}, {"GET", "/v1/paper/monitor?as_of=2026-01-01", 400},
	} {
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s returned %d", tc.method, tc.path, w.Code)
		}
	}
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	path := doc["paths"].(map[string]any)["/v1/paper/monitor"].(map[string]any)
	if len(path) != 1 || path["get"] == nil {
		t.Fatal("monitor must be GET-only")
	}
	view := PaperMonitor{Runner: PaperMonitorRunner{}, LatestPolicy: &PaperMonitorPolicy{}}
	for name, dto := range map[string]any{"PaperMonitor": view, "PaperMonitorRunner": view.Runner, "PaperMonitorPolicy": view.LatestPolicy} {
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

func TestPaperMonitorStoredPolicyAndLeaseAreObservationsOnly(t *testing.T) {
	svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
	ctx := context.Background()
	raw := paperSnapshotCSV(rows...)
	if _, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID,
		localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, "golden_cross"), raw, research); err != nil {
		t.Fatal(err)
	}
	before := paperAdmissionCountsForTest(t, svc)
	claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.releasePaperRunnerLease(context.Background(), claim); err != nil {
			t.Error(err)
		}
	})
	for _, tt := range []struct {
		now   string
		state string
	}{
		{"2026-05-10T07:00:00Z", "lease_recorded"},
		{"2026-05-10T06:59:59Z", "clock_regressed"},
		{"2026-05-10T07:00:30Z", "expired"},
	} {
		svc.now = func() time.Time { return mustTime(tt.now) }
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/monitor", nil))
		var view PaperMonitor
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil {
			t.Fatalf("read failed: %d %s", w.Code, w.Body.String())
		}
		if view.Runner.State != tt.state || view.ObservedAt != tt.now || !view.StrategySelected || view.SessionCount != 1 || view.PendingPolicyCount != 0 || view.LatestPolicy == nil || view.LatestPolicy.Decision != "INSUFFICIENT" || !view.LatestPolicy.MatchesCurrentSelection {
			t.Fatalf("wrong lease/policy observation: %+v", view)
		}
		if *view.Runner.HeartbeatAt != "2026-05-10T07:00:00Z" || *view.Runner.ExpiresAt != "2026-05-10T07:00:30Z" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("lease time or caching changed")
		}
		if paperAdmissionCountsForTest(t, svc) != before {
			t.Fatal("monitor changed trading records")
		}
		stored, err := loadPaperRunnerLease(ctx, svc.db)
		if err != nil || stored.record.FencingToken != claim.FencingToken || stored.record.HeartbeatAtNS != claim.HeartbeatAtNS {
			t.Fatal("monitor renewed or changed ownership")
		}
	}
}

func TestPaperMonitorIncompletePolicyBecomesHistoricalAfterRollback(t *testing.T) {
	svc, _, _, _, selected, rows := localPaperRestartFixture(t)
	ctx := context.Background()
	if _, err := svc.importPaperSnapshot(ctx, paperSnapshotCSV(rows...)); err != nil {
		t.Fatal(err)
	}
	point, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, "2026-05-09T06:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	assertPending := func(want int, decision string, match bool) {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/monitor", nil))
		var view PaperMonitor
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil {
			t.Fatalf("read failed: %d %s", w.Code, w.Body.String())
		}
		if view.PendingPolicyCount != want || (view.LatestPolicy == nil) != (decision == "") {
			t.Fatalf("wrong incomplete evidence: %+v", view)
		}
		if decision != "" && (view.LatestPolicy.Decision != decision || view.LatestPolicy.MatchesCurrentSelection != match || view.LatestPolicy.AsOf != "2026-05-09T06:30:00.000000000Z") {
			t.Fatalf("wrong historical policy: %+v", view.LatestPolicy)
		}
	}
	assertPending(1, "", false)
	window, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, selected.CurrentEventID, point.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	assertPending(1, "", false)
	if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, selected.CurrentEventID, window.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	assertPending(0, "INSUFFICIENT", true)
	if _, err := svc.rollbackPaperCandidate(ctx, selected.CurrentEventID, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	assertPending(0, "INSUFFICIENT", false)
}

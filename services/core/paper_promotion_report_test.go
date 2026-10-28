package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"omni-folio/services/core/internal/researchdomain"
)

func TestPaperPromotionReportEmptyReadOnlyObservation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/promotion-report", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("paper promotion report status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	attempt, present := body["attempt"]
	if body["schema_version"] != "paper-promotion-report.v1" || body["mode"] != "paper_comparison_only" || !present || attempt != nil {
		t.Fatalf("empty report claimed a comparison: %v", body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("paper promotion report must not be cached")
	}
}

func TestPaperPromotionReportHTTPBoundaryAndSharedEmptyFixture(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-10-27T15:00:00Z") }
	w := paperPromotionReportRequestForTest(t, svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusOK)
	assertPaperPromotionReportCase(t, w.Body.Bytes(), "empty")
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodHead, "/v1/paper/promotion-report", http.StatusOK},
		{http.MethodPost, "/v1/paper/promotion-report", http.StatusMethodNotAllowed},
		{http.MethodPut, "/v1/paper/promotion-report", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/v1/paper/promotion-report", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/paper/promotion-report?account=private-account", http.StatusBadRequest},
		{http.MethodGet, "/v1/paper/promotion-report?as_of=2026-01-01", http.StatusBadRequest},
	} {
		w := paperPromotionReportRequestForTest(t, svc, tc.method, tc.path, tc.status)
		if tc.status == http.StatusBadRequest {
			var body APIError
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != "invalid_query" || strings.Contains(w.Body.String(), "private-account") {
				t.Fatalf("query contract: %s, %v", w.Body.String(), err)
			}
		}
	}
	const origin = "http://localhost:8081"
	request := httptest.NewRequest(http.MethodOptions, "/v1/paper/promotion-report", nil)
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	w = httptest.NewRecorder()
	withCORS(svc.routes(), origin).ServeHTTP(w, request)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != origin || w.Body.Len() != 0 {
		t.Fatalf("existing CORS preflight: %d %s", w.Code, w.Body.String())
	}
}

func TestPaperPromotionReportStoredStatesMatchSharedFixtures(t *testing.T) {
	for _, name := range []string{"pending", "error", "aborted", "stale", "held"} {
		t.Run(name, func(t *testing.T) {
			f := newPaperPromotionFixture(t)
			ctx := context.Background()
			attemptID := "private-report-" + name
			if name == "held" {
				selected, err := f.svc.selectPaperCandidate(ctx, f.challenger.ResultSHA256, f.selection.CurrentEventID)
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := f.svc.promotePaperCandidate(ctx, k2aAccountRef, f.incumbent.ResultSHA256, selected.CurrentEventID, attemptID, f.bars)
				if err != nil || receipt == nil || receipt.Decision != "HOLD" || receipt.Reason != "candidate_return_below_incumbent" {
					t.Fatalf("real HOLD comparison: %+v, %v", receipt, err)
				}
			} else {
				claim := paperPromotionReportClaimForTest(t, f, attemptID)
				switch name {
				case "error":
					receipt, err := f.svc.finishPaperPromotion(ctx, *claim, paperPromotionComparison{}, errors.New("private-scorer-diagnostic"), false)
					if err != nil || receipt == nil || receipt.Decision != "ERROR" {
						t.Fatalf("failed scorer: %+v, %v", receipt, err)
					}
				case "aborted":
					if _, err := f.svc.abortPaperPromotion(ctx, attemptID); err != nil {
						t.Fatal(err)
					}
				case "stale":
					comparison, err := scorePaperPromotionClaim(ctx, f.svc.db, *claim)
					if err != nil || comparison.Decision != "PROMOTE" {
						t.Fatalf("real stale comparison: %+v, %v", comparison, err)
					}
					authority, err := f.svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, true)
					if err != nil || authority == nil || !authority.Armed {
						t.Fatalf("arm after claim: %+v, %v", authority, err)
					}
					receipt, err := f.svc.finishPaperPromotion(ctx, *claim, comparison, nil, false)
					if err != nil || receipt == nil || receipt.Decision != "STALE" || receipt.Comparison == nil || receipt.Comparison.Decision != "PROMOTE" {
						t.Fatalf("stale final decision lost the comparison: %+v, %v", receipt, err)
					}
				}
			}
			w := paperPromotionReportRequestForTest(t, f.svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusOK)
			assertPaperPromotionReportCase(t, w.Body.Bytes(), name)
			assertPaperPromotionReportPrivateFieldsAbsent(t, f, attemptID, w.Body.Bytes())
			if name == "pending" {
				calls := 0
				f.svc.now = func() time.Time {
					calls++
					return mustTime("2026-10-26T15:00:00.1234Z")
				}
				w = paperPromotionReportRequestForTest(t, f.svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusOK)
				view := decodePaperPromotionReportForTest(t, w.Body.Bytes())
				if calls != 1 || view.ObservedAt != "2026-10-26T15:00:00.1234Z" || view.Attempt.ClaimedAt != "2026-10-27T15:00:00Z" || view.Attempt.DecidedAt != nil {
					t.Fatalf("historical claim or one captured canonical observation changed: %+v, calls=%d", view, calls)
				}
			}
		})
	}
}

func TestPaperPromotionReportExactPromotionEventRemainsSupersededAfterReselection(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	const attemptID = "private-report-promoted"
	receipt, err := f.svc.promotePaperCandidate(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, attemptID, f.bars)
	if err != nil || receipt == nil || receipt.Decision != "PROMOTE" || receipt.SelectionEventID == "" {
		t.Fatalf("real promotion: %+v, %v", receipt, err)
	}
	w := paperPromotionReportRequestForTest(t, f.svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusOK)
	assertPaperPromotionReportCase(t, w.Body.Bytes(), "promoted-current")
	assertPaperPromotionReportPrivateFieldsAbsent(t, f, attemptID, w.Body.Bytes())
	if strings.Contains(w.Body.String(), receipt.SelectionEventID) {
		t.Fatal("promotion selection ID exposed")
	}
	rolledBack, err := f.svc.rollbackPaperCandidate(ctx, receipt.SelectionEventID, receipt.SelectionEventID)
	if err != nil || rolledBack == nil || rolledBack.SelectedResultSHA256 != f.incumbent.ResultSHA256 {
		t.Fatalf("rollback: %+v, %v", rolledBack, err)
	}
	w = paperPromotionReportRequestForTest(t, f.svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusOK)
	assertPaperPromotionReportCase(t, w.Body.Bytes(), "promoted-superseded")
	reselected, err := f.svc.selectPaperCandidate(ctx, f.challenger.ResultSHA256, rolledBack.CurrentEventID)
	if err != nil || reselected == nil || reselected.SelectedResultSHA256 != f.challenger.ResultSHA256 || reselected.CurrentEventID == receipt.SelectionEventID {
		t.Fatalf("manual reselection: %+v, %v", reselected, err)
	}
	w = paperPromotionReportRequestForTest(t, f.svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusOK)
	assertPaperPromotionReportCase(t, w.Body.Bytes(), "promoted-superseded")
}

func TestPaperPromotionReportLatestPendingAndHistoricalCorruptionFailClosed(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	const olderAttempt = "private-earlier-promotion"
	receipt, err := f.svc.promotePaperCandidate(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, olderAttempt, f.bars)
	if err != nil || receipt == nil || receipt.Decision != "PROMOTE" {
		t.Fatalf("real earlier promotion: %+v, %v", receipt, err)
	}
	fresh := paperPromotionForwardBars(t, mustTime("2026-11-01T00:00:00Z"), 12*time.Hour)
	f.svc.now = func() time.Time { return mustTime("2026-12-20T15:00:00Z") }
	claim, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.incumbent.ResultSHA256, receipt.SelectionEventID, "private-newer-pending", fresh)
	if err != nil || claim == nil || prior != nil {
		t.Fatalf("later fresh pending claim: %+v, %+v, %v", claim, prior, err)
	}
	w := paperPromotionReportRequestForTest(t, f.svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusOK)
	view := decodePaperPromotionReportForTest(t, w.Body.Bytes())
	if view.Attempt == nil || view.Attempt.Decision != "PENDING" || view.Attempt.ReasonCode != "comparison_claimed" || view.Attempt.Comparison != nil || view.Attempt.DecidedAt != nil ||
		view.Attempt.PromotionSelectionStatus != "not_promoted" || view.Attempt.ClaimedAt != "2026-12-20T15:00:00Z" ||
		view.Attempt.ComparisonStart != "2026-11-04T00:00:00Z" || view.Attempt.ComparisonEnd != "2026-12-18T12:00:00Z" ||
		view.Attempt.Candidate.FastWindow != 3 || view.Attempt.Incumbent.FastWindow != 2 {
		t.Fatalf("latest claim was hidden by an earlier successful decision: %+v", view)
	}
	var raw, trigger string
	if err := f.svc.db.QueryRow(`SELECT record_json FROM paper_promotion_decisions WHERE attempt_id=?`, olderAttempt).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='paper_promotion_decisions_no_update' AND type='trigger'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	var decision paperPromotionDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil || decision.Comparison == nil || len(decision.Comparison.Windows) == 0 {
		t.Fatalf("stored completed comparison: %v", err)
	}
	decision.Comparison.Windows[0].Candidate.AfterCostReturn = "999"
	canonical, sha, err := orderJSONHash(decision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.db.Exec(`DROP TRIGGER paper_promotion_decisions_no_update`); err != nil {
		t.Fatal(err)
	}
	_, updateErr := f.svc.db.Exec(`UPDATE paper_promotion_decisions SET record_json=?,record_sha256=? WHERE attempt_id=?`, string(canonical), sha, olderAttempt)
	_, triggerErr := f.svc.db.Exec(trigger)
	if updateErr != nil || triggerErr != nil {
		t.Fatalf("rehash comparison or restore exact trigger: %v / %v", updateErr, triggerErr)
	}
	w = paperPromotionReportRequestForTest(t, f.svc, http.MethodGet, "/v1/paper/promotion-report", http.StatusInternalServerError)
	assertPaperPromotionReportInternalError(t, w.Body.Bytes())
	assertPaperPromotionReportPrivateFieldsAbsent(t, f, olderAttempt, w.Body.Bytes())
	if strings.Contains(w.Body.String(), "private-newer-pending") || strings.Contains(w.Body.String(), sha) {
		t.Fatal("corrupt history exposed a partial latest claim or hash")
	}
}

func TestPaperPromotionReportCancelledContextAndStorageErrorsAreReadOnly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	before := paperPromotionReportStateForTest(t, svc)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if view, err := svc.paperPromotionReport(ctx); !errors.Is(err, context.Canceled) || view != nil {
		t.Fatalf("cancelled read: %+v, %v", view, err)
	}
	if after := paperPromotionReportStateForTest(t, svc); after != before {
		t.Fatal("cancelled report changed database state")
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/promotion-report", nil))
	if w.Code != http.StatusInternalServerError || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("storage error: %d %s", w.Code, w.Body.String())
	}
	assertPaperPromotionReportInternalError(t, w.Body.Bytes())
}

func TestPaperPromotionReportPublicMetricBoundsPreserveExactRatios(t *testing.T) {
	valid := researchdomain.Metrics{AfterCostReturn: "-1.019020000000000001", MaxDrawdown: "1.01902", TradeCount: "3", TradeCountDefinition: "completed_long_only_round_trips", Turnover: "2.000000000000000001"}
	want := PaperPromotionReportMetrics{AfterCostReturn: valid.AfterCostReturn, MaxDrawdown: valid.MaxDrawdown, RoundTrips: valid.TradeCount, Turnover: valid.Turnover}
	if got, err := projectPaperPromotionReportMetrics(valid); err != nil || got != want {
		t.Fatalf("exact ratios were rounded or incorrectly bounded: %+v, %v", got, err)
	}
	boundary := valid
	boundary.AfterCostReturn, boundary.MaxDrawdown, boundary.TradeCount, boundary.Turnover = strings.Repeat("9", 128), strings.Repeat("9", 128), strings.Repeat("9", 128), strings.Repeat("9", 128)
	if got, err := projectPaperPromotionReportMetrics(boundary); err != nil || got.AfterCostReturn != boundary.AfterCostReturn || got.RoundTrips != boundary.TradeCount {
		t.Fatalf("public 128-character scalar boundary: %+v, %v", got, err)
	}
	for name, mutate := range map[string]func(*researchdomain.Metrics){
		"return cap":      func(m *researchdomain.Metrics) { m.AfterCostReturn = strings.Repeat("9", 129) },
		"drawdown cap":    func(m *researchdomain.Metrics) { m.MaxDrawdown = strings.Repeat("9", 129) },
		"round-trip cap":  func(m *researchdomain.Metrics) { m.TradeCount = strings.Repeat("9", 129) },
		"turnover cap":    func(m *researchdomain.Metrics) { m.Turnover = strings.Repeat("9", 129) },
		"private metric":  func(m *researchdomain.Metrics) { m.AfterCostReturn = "private-metric" },
		"negative zero":   func(m *researchdomain.Metrics) { m.AfterCostReturn = "-0" },
		"noncanonical":    func(m *researchdomain.Metrics) { m.Turnover = "1.0" },
		"drawdown sign":   func(m *researchdomain.Metrics) { m.MaxDrawdown = "-0.1" },
		"turnover sign":   func(m *researchdomain.Metrics) { m.Turnover = "-0.1" },
		"fractional trip": func(m *researchdomain.Metrics) { m.TradeCount = "0.5" },
		"negative trip":   func(m *researchdomain.Metrics) { m.TradeCount = "-1" },
		"count meaning":   func(m *researchdomain.Metrics) { m.TradeCountDefinition = "fills" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := valid
			mutate(&invalid)
			if got, err := projectPaperPromotionReportMetrics(invalid); err == nil || got != (PaperPromotionReportMetrics{}) {
				t.Fatalf("unsupported public scalar accepted: %+v, %v", got, err)
			}
		})
	}
}

func TestPaperPromotionReportOpenAPIClosedPublicShapesAndReferences(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	path := doc["paths"].(map[string]any)["/v1/paper/promotion-report"].(map[string]any)
	if !exactKeys(path, "get") {
		t.Fatal("OpenAPI promotion report must have only the GET operation")
	}
	get := path["get"].(map[string]any)
	if get["operationId"] != "getPaperPromotionReport" || get["parameters"] != nil || get["requestBody"] != nil {
		t.Fatal("OpenAPI report added a query or request body contract")
	}
	responses := get["responses"].(map[string]any)
	for status, ref := range map[string]string{"200": "PaperPromotionReportView", "400": "Error", "500": "Error"} {
		response := responses[status].(map[string]any)
		schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if schema["$ref"] != "#/components/schemas/"+ref {
			t.Fatalf("HTTP %s references the wrong response schema: %v", status, schema)
		}
	}
	cache := responses["200"].(map[string]any)["headers"].(map[string]any)["Cache-Control"].(map[string]any)["schema"].(map[string]any)
	if cache["const"] != "no-store" {
		t.Fatal("OpenAPI report may not be cached")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	view := decodePaperPromotionReportForTest(t, paperPromotionReportCaseForTest(t, "promoted-current"))
	for name, dto := range map[string]any{
		"PaperPromotionReportView":       view,
		"StoredPaperPromotionReport":     view.Attempt,
		"PaperPromotionReportStrategy":   view.Attempt.Candidate,
		"PaperPromotionReportComparison": view.Attempt.Comparison,
		"PaperPromotionReportWindow":     view.Attempt.Comparison.Windows[0],
		"PaperPromotionReportMetrics":    view.Attempt.Comparison.Windows[0].Candidate,
	} {
		encoded, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		schema := schemas[name].(map[string]any)
		if schema["additionalProperties"] != false || !reflect.DeepEqual(jsonKeySet(schema["properties"].(map[string]any)), jsonKeySet(fields)) || !reflect.DeepEqual(jsonStringSet(schema["required"]), jsonKeySet(fields)) {
			t.Fatalf("%s must match the closed public DTO: %v", name, schema)
		}
	}
	properties := func(name string) map[string]any { return schemas[name].(map[string]any)["properties"].(map[string]any) }
	for schema, fields := range map[string][]string{"StoredPaperPromotionReport": {"comparison_start", "comparison_end"}, "PaperPromotionReportWindow": {"start", "end"}} {
		for _, field := range fields {
			bound := properties(schema)[field].(map[string]any)
			if bound["$ref"] != "#/components/schemas/LedgerActivityTimestamp" || bound["maxLength"] != float64(20) {
				t.Fatalf("%s.%s must use the whole-second bar timestamp contract", schema, field)
			}
		}
	}
	for name, ref := range map[string]string{"candidate": "PaperPromotionReportStrategy", "incumbent": "PaperPromotionReportStrategy"} {
		if properties("StoredPaperPromotionReport")[name].(map[string]any)["$ref"] != "#/components/schemas/"+ref {
			t.Fatalf("attempt %s has the wrong schema reference", name)
		}
	}
	for _, tc := range []struct{ schema, field, target string }{
		{"PaperPromotionReportView", "attempt", "StoredPaperPromotionReport"},
		{"StoredPaperPromotionReport", "comparison", "PaperPromotionReportComparison"},
	} {
		oneOf := properties(tc.schema)[tc.field].(map[string]any)["oneOf"].([]any)
		if len(oneOf) != 2 || oneOf[0].(map[string]any)["$ref"] != "#/components/schemas/"+tc.target || oneOf[1].(map[string]any)["type"] != "null" {
			t.Fatalf("%s.%s must retain its nullable public reference", tc.schema, tc.field)
		}
	}
	windows := properties("PaperPromotionReportComparison")["windows"].(map[string]any)
	if windows["minItems"] != float64(3) || windows["maxItems"] != float64(3) || windows["items"].(map[string]any)["$ref"] != "#/components/schemas/PaperPromotionReportWindow" {
		t.Fatal("comparison must document exactly three public windows")
	}
	for _, field := range []string{"candidate", "incumbent", "buy_and_hold"} {
		if properties("PaperPromotionReportWindow")[field].(map[string]any)["$ref"] != "#/components/schemas/PaperPromotionReportMetrics" {
			t.Fatalf("window %s has the wrong metric schema", field)
		}
	}
}

func paperPromotionReportClaimForTest(t *testing.T, f paperPromotionFixture, attemptID string) *paperPromotionClaim {
	t.Helper()
	claim, prior, err := f.svc.claimPaperPromotion(context.Background(), k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, attemptID, f.bars)
	if err != nil || claim == nil || prior != nil {
		t.Fatalf("claim: %+v, %+v, %v", claim, prior, err)
	}
	return claim
}

type paperPromotionReportStateForTestValue struct {
	Changes                                 int64
	Claims, Decisions, Selection, Authority string
	Schema                                  string
}

func paperPromotionReportStateForTest(t *testing.T, svc *Service) paperPromotionReportStateForTestValue {
	t.Helper()
	var state paperPromotionReportStateForTestValue
	err := svc.db.QueryRow(`SELECT total_changes(),
		(SELECT json_group_array(record_json) FROM (SELECT record_json FROM paper_promotion_claims ORDER BY sequence)),
		(SELECT json_group_array(record_json) FROM (SELECT record_json FROM paper_promotion_decisions ORDER BY sequence)),
		(SELECT json_group_array(record_json) FROM (SELECT record_json FROM strategy_selection_events ORDER BY sequence)),
		(SELECT json_group_array(record_json) FROM (SELECT record_json FROM execution_authority_events ORDER BY sequence)),
		(SELECT json_group_array(json_array(type,name,tbl_name,sql)) FROM (SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name))`).Scan(&state.Changes, &state.Claims, &state.Decisions, &state.Selection, &state.Authority, &state.Schema)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func paperPromotionReportRequestForTest(t *testing.T, svc *Service, method, path string, status int) *httptest.ResponseRecorder {
	t.Helper()
	before := paperPromotionReportStateForTest(t, svc)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(method, path, nil))
	if after := paperPromotionReportStateForTest(t, svc); after != before {
		t.Fatal("report changed database journal, current selection, or execution authority")
	}
	if w.Code != status {
		t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
	}
	if status != http.StatusMethodNotAllowed && w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("report response must not be cached")
	}
	return w
}

func paperPromotionReportCaseForTest(t *testing.T, name string) json.RawMessage {
	t.Helper()
	var cases struct {
		Cases []struct {
			Name     string          `json:"name"`
			Response json.RawMessage `json:"response"`
		} `json:"cases"`
	}
	decodeFixture(t, "paper-promotion-report-cases.json", &cases)
	for _, tc := range cases.Cases {
		if tc.Name == name {
			return tc.Response
		}
	}
	t.Fatalf("missing shared paper promotion report case %q", name)
	return nil
}

func assertPaperPromotionReportCase(t *testing.T, raw []byte, name string) {
	t.Helper()
	want := decodePaperPromotionReportForTest(t, paperPromotionReportCaseForTest(t, name))
	got := decodePaperPromotionReportForTest(t, raw)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report differs from shared %s fixture: got=%s want=%s", name, raw, paperPromotionReportCaseForTest(t, name))
	}
}

func decodePaperPromotionReportForTest(t *testing.T, raw []byte) *PaperPromotionReportView {
	t.Helper()
	var view PaperPromotionReportView
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&view); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("report has trailing JSON: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if !exactKeys(body, "schema_version", "mode", "observed_at", "attempt") || view.SchemaVersion != "paper-promotion-report.v1" || view.Mode != "paper_comparison_only" || !canonicalUTCString(view.ObservedAt) {
		t.Fatalf("invalid closed report root: %s", raw)
	}
	if view.Attempt == nil {
		return &view
	}
	attempt := body["attempt"].(map[string]any)
	if !exactKeys(attempt, "symbol", "claimed_at", "decided_at", "comparison_start", "comparison_end", "candidate", "incumbent", "decision", "reason_code", "promotion_selection_status", "comparison") {
		t.Fatalf("invalid closed attempt: %v", attempt)
	}
	for _, field := range []string{"candidate", "incumbent"} {
		if !exactKeys(attempt[field].(map[string]any), "fast_window", "slow_window", "quantity") {
			t.Fatalf("invalid closed %s strategy", field)
		}
	}
	if view.Attempt.Comparison == nil {
		return &view
	}
	comparison := attempt["comparison"].(map[string]any)
	if !exactKeys(comparison, "policy_version", "metrics_verification", "decision", "reason_code", "windows") || len(view.Attempt.Comparison.Windows) != 3 {
		t.Fatalf("invalid closed comparison: %v", comparison)
	}
	for _, value := range comparison["windows"].([]any) {
		window := value.(map[string]any)
		if !exactKeys(window, "start", "end", "candidate", "incumbent", "buy_and_hold") {
			t.Fatalf("invalid closed comparison window: %v", window)
		}
		for _, field := range []string{"candidate", "incumbent", "buy_and_hold"} {
			if !exactKeys(window[field].(map[string]any), "after_cost_return", "max_drawdown", "round_trips", "turnover") {
				t.Fatalf("invalid closed %s metrics", field)
			}
		}
	}
	return &view
}

func assertPaperPromotionReportInternalError(t *testing.T, raw []byte) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || !exactKeys(body, "code", "message") || body["code"] != "internal_error" || body["message"] != "internal server error" {
		t.Fatalf("report leaked an internal error or partial result: %s, %v", raw, err)
	}
}

func assertPaperPromotionReportPrivateFieldsAbsent(t *testing.T, f paperPromotionFixture, attemptID string, raw []byte) {
	t.Helper()
	claim, _, found, err := loadPaperPromotion(context.Background(), f.svc.db, attemptID)
	if err != nil || !found {
		t.Fatalf("redaction fixture: %v", err)
	}
	encodedInput, err := json.Marshal(claim.InputCSV)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{
		k2aAccountRef, f.session.SessionID, attemptID, f.selection.CurrentEventID, f.incumbent.ResultSHA256, f.challenger.ResultSHA256,
		claim.InputDataSHA256, claim.ExecutionPolicySHA256, claim.StateSHA256, string(encodedInput), "private-scorer-diagnostic",
		`"` + claim.StartingCash + `"`, `"account_ref"`, `"session_id"`, `"attempt_id"`, `"selection_event_id"`, `"result_sha256"`,
		`"input_csv"`, `"starting_cash"`, `"cash"`, `"fee"`, `"tax"`, `"fees"`, `"taxes"`, `"slippage_cost"`, `"slippage_bps"`, `"armed"`, `"lease_owner"`,
	} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatalf("private comparison field or value exposed: %s", private)
		}
	}
}

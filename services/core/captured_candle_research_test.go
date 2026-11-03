package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCapturedCandleResearchActualProducerAndPythonCannotEnterPaperAuthority(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	rawCSV, err := os.ReadFile(filepath.Join(root, "contracts/fixtures/strategy-market-bars.csv"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(rawCSV)).ReadAll()
	if err != nil || len(records) != 121 {
		t.Fatalf("synthetic research CSV: %d rows, %v", len(records)-1, err)
	}
	rows := make([]map[string]string, 0, len(records)-1)
	for index := len(records) - 1; index > 0; index-- {
		row := records[index]
		rows = append(rows, map[string]string{
			"dt": strings.ReplaceAll(row[0][:10], "-", ""), "open_pric": row[2], "high_pric": row[3],
			"low_pric": row[4], "cur_prc": row[5], "trde_qty": row[6],
		})
	}
	transport, calls := candleCaptureTLS(t, func(w http.ResponseWriter, r *http.Request) {
		var body kiwoomCandleRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Header.Get("api-id") != "ka10081" || body.StockCode != "005930" || body.BaseDate != "20300101" {
			t.Errorf("synthetic capture request: %+v, %v", body, err)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"stk_cd": "005930", "stk_dt_pole_chart_qry": rows, "return_code": 0}); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(t.TempDir(), "synthetic-capture.json")
	var captureReceipt bytes.Buffer
	if err := runKiwoomMockCandles(context.Background(), captureArgs(path, "1d"), &captureReceipt, func(context.Context) ([]byte, error) {
		return mockSyncSecret("9876543210"), nil
	}, transport, mockSyncNow, mockSyncPacer()); err != nil {
		t.Fatal(err)
	}
	capture, rawCapture := readCapture(t, path)
	if len(capture.Bars) != 120 || calls.Load() != 2 {
		t.Fatalf("actual producer did not retain 120 synthetic observations: %d rows, %d calls", len(capture.Bars), calls.Load())
	}
	assertCaptureReceipt(t, captureReceipt.Bytes(), rawCapture, 120)
	command := exec.Command("python3", "-B", "-m", "omni_research.capture_cli", "--capture", path,
		"--config", filepath.Join(root, "contracts/fixtures/strategy-improvement-paper-config.json"))
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+filepath.Join(root, "services/research"))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	rawReport, err := command.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("actual Python capture CLI: %v %s", err, stderr.String())
	}
	if len(rawReport) == 0 || len(rawReport) >= maxBodyBytes || !json.Valid(rawReport) {
		t.Fatalf("report must be valid JSON below the Go body limit: %d bytes", len(rawReport))
	}
	var report struct {
		SchemaVersion string `json:"schema_version"`
		Source        struct {
			SHA256  string              `json:"sha256"`
			Capture kiwoomCandleCapture `json:"capture"`
		} `json:"source"`
		Analysis struct {
			Comparisons map[string]bool `json:"threshold_comparisons"`
		} `json:"analysis"`
	}
	if err := json.Unmarshal(rawReport, &report); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(rawCapture)
	if report.SchemaVersion != "captured-candle-research.v1" || report.Source.SHA256 != hex.EncodeToString(sum[:]) || len(report.Source.Capture.Bars) != 120 {
		t.Fatal("Python report lost the native capture provenance")
	}
	if len(report.Analysis.Comparisons) != 3 {
		t.Fatal("report did not compute all research threshold comparisons")
	}
	for _, passed := range report.Analysis.Comparisons {
		if !passed {
			t.Fatal("synthetic fixture must pass numerical thresholds before authority refusal")
		}
	}
	if _, err := decodeStrategyArtifact(rawReport); err == nil {
		t.Fatal("capture report passed the strategy artifact decoder")
	}
	if _, err := decodePaperProposal(rawReport); err == nil {
		t.Fatal("capture report passed the paper proposal decoder")
	}
	svc, _ := testService(t, nil, nil)
	forward := paperPromotionForwardBars(t, mustTime("2030-02-01T00:00:00Z"), 24*time.Hour)
	if _, _, err := parsePaperPromotionBars(context.Background(), forward); err != nil {
		t.Fatalf("valid forward bars required before refusal: %v", err)
	}
	const attempt = "captured-research-refusal"
	if !orderAlias(k2aAccountRef, "account") || !safeOrderID(attempt) {
		t.Fatal("valid identifiers required before refusal")
	}
	before := paperImprovementStateForTest(t, svc)
	if _, err := svc.registerStrategyEvidence(context.Background(), rawReport); err == nil {
		t.Fatal("capture report registered durable research evidence")
	}
	if after := paperImprovementStateForTest(t, svc); after != before {
		t.Fatalf("registration refusal changed full state: before=%+v after=%+v", before, after)
	}
	if _, err := svc.improvePaperCandidate(context.Background(), k2aAccountRef, attempt, rawReport, forward); err == nil {
		t.Fatal("capture report reached paper improvement")
	}
	if after := paperImprovementStateForTest(t, svc); after != before {
		t.Fatalf("improvement refusal changed full state: before=%+v after=%+v", before, after)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, rawCapture) {
		t.Fatal("research changed the original producer capture")
	}
}

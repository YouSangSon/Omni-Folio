package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalPaperWorkflowAccountExamplesCanInitialize(t *testing.T) {
	_, dbPath, _, evidence, selected, _ := localPaperRestartFixture(t)
	doc, err := os.ReadFile("../../docs/local-paper-workflow.md")
	if err != nil {
		t.Fatal(err)
	}
	accounts := map[string]bool{}
	words := strings.Fields(string(doc))
	for i, word := range words {
		if word == "-account" && i+1 < len(words) {
			accounts[words[i+1]] = true
		}
	}
	if len(accounts) == 0 {
		t.Fatal("workflow has no executable account example")
	}
	for account := range accounts {
		var output bytes.Buffer
		if err := runLocalPaperCommand(context.Background(), []string{
			"paper-init", "-db", dbPath, "-account", account,
			"-result-sha256", evidence.ResultSHA256,
			"-expected-current-event", selected.CurrentEventID,
		}, &output); err != nil {
			t.Fatalf("documented account %q cannot initialize: %v", account, err)
		}
		var session PaperAccountingSession
		if err := json.Unmarshal(output.Bytes(), &session); err != nil || session.AccountRef != account {
			t.Fatal("documented initialization did not return its account session")
		}
	}
}

func TestLocalPaperStepImportsAdmitsFillsAndHalts(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-05-01T07:00:00Z") }
	research, err := os.ReadFile("../../contracts/fixtures/strategy-market-bars.csv")
	if err != nil {
		t.Fatal(err)
	}
	research = []byte(strings.ReplaceAll(string(research), ",SMA,", ",005930,"))
	researchSHA := fmt.Sprintf("%x", sha256.Sum256(research))
	evidence, err := svc.registerStrategyEvidence(context.Background(), rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(a map[string]any) {
		a["input_sha256"] = researchSHA
		a["manifest"].(map[string]any)["data"].(map[string]any)["input_sha256"] = researchSHA
	}))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.openPaperAccountingSession(context.Background(), k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	var artifact map[string]any
	if err := json.Unmarshal([]byte(evidence.artifactJSON), &artifact); err != nil {
		t.Fatal(err)
	}
	params := artifact["manifest"].(map[string]any)["strategy"].(map[string]any)["parameters"].(map[string]any)
	slow := int(params["slow_window"].(float64))
	rows := [][]string{{"bar_at", "symbol", "venue", "timezone", "interval", "open", "high", "low", "close", "volume", "open_at", "source_available_at", "fetched_at"}}
	for i := slow; i >= 0; i-- {
		day := mustTime("2026-05-09T00:00:00Z").AddDate(0, 0, -i).Format("2006-01-02")
		price := "99"
		if i == 0 {
			price = "100"
		}
		rows = append(rows, []string{day + "T06:30:00Z", "005930", "KRX", "Asia/Seoul", "1d", price, price, price, price, "100", day + "T00:00:00Z", day + "T06:31:00Z", day + "T06:32:00Z"})
	}
	encode := func() []byte {
		var b bytes.Buffer
		w := csv.NewWriter(&b)
		if err := w.WriteAll(rows); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	proposal := func(raw []byte, kind string) []byte {
		signal := PaperSignal{StrategyResultSHA256: evidence.ResultSHA256, Symbol: "005930", DataAsOf: rows[len(rows)-1][0], DataSHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
		return paperProposalForTest(t, svc, signal, kind)
	}
	svc.now = func() time.Time { return mustTime("2026-05-10T07:00:00Z") }
	raw := encode()
	initialRaw, initialProposal := raw, proposal(raw, "golden_cross")
	if _, err := svc.db.Exec(`CREATE TRIGGER fail_local_paper_order BEFORE INSERT ON order_idempotency BEGIN SELECT RAISE(ABORT,'test order failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selected.CurrentEventID, initialProposal, raw, research); err == nil {
		t.Fatal("injected order failure was ignored")
	}
	failedState, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	if err != nil || failedState.Armed || paperAdmissionCountsForTest(t, svc) != (paperAdmissionCounts{}) {
		t.Fatal("failed run leaked authority or partial order")
	}
	if _, err := svc.db.Exec(`DROP TRIGGER fail_local_paper_order`); err != nil {
		t.Fatal(err)
	}
	result, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selected.CurrentEventID, proposal(raw, "golden_cross"), raw, research)
	if err != nil || result == nil || result.Order == nil || result.Order.Status != "OPEN" {
		t.Fatalf("first step: %+v %v", result, err)
	}
	firstID := result.Order.OrderID
	assertHalted := func() {
		state, e := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		if e != nil || state.Armed || state.LeaseOwner != "" {
			t.Fatalf("owned execution leaked: %+v %v", state, e)
		}
	}
	assertHalted()
	rows = append(rows, strings.Split("2026-05-10T06:30:00Z,005930,KRX,Asia/Seoul,1d,101,101,101,101,100,2026-05-10T00:00:00Z,2026-05-10T06:31:00Z,2026-05-10T06:32:00Z", ","))
	raw = encode()
	before := paperAdmissionCountsForTest(t, svc)
	if _, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selected.CurrentEventID, proposal(raw, "death_cross"), raw, research); err == nil {
		t.Fatal("forged direction accepted")
	}
	if before != paperAdmissionCountsForTest(t, svc) {
		t.Fatal("invalid proposal filled a prior order")
	}
	assertHalted()
	result, err = svc.executeLocalPaper(context.Background(), k2aAccountRef, selected.CurrentEventID, proposal(raw, "none"), raw, research)
	if err != nil || result == nil || result.Order != nil {
		t.Fatalf("second step: %+v %v", result, err)
	}
	filled, err := svc.loadOrderState(context.Background(), firstID)
	if err != nil || filled.Status != "FILLED" {
		t.Fatalf("prior fill: %+v %v", filled, err)
	}
	assertHalted()
	// A fresh CLI service must not turn an already committed decision into a
	// second order. This also exercises regular-file reads and JSON delivery.
	var dbPath string
	if err := svc.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	barsPath := filepath.Join(dir, "bars.csv")
	proposalPath := filepath.Join(dir, "proposal.json")
	researchPath := filepath.Join(dir, "research.csv")
	if err := os.WriteFile(researchPath, research, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(barsPath, initialRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proposalPath, initialProposal, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"paper-execute", "-db", dbPath, "-account", k2aAccountRef, "-expected-current-event", selected.CurrentEventID, "-bars", barsPath, "-proposal", proposalPath, "-research-bars", researchPath}
	var output bytes.Buffer
	if err := runLocalPaperCommand(context.Background(), args, &output); err == nil {
		t.Fatal("missing explicit arm accepted")
	}
	if output.Len() != 0 {
		t.Fatal("invalid CLI reported a result")
	}
	before = paperAdmissionCountsForTest(t, svc)
	if err := runLocalPaperCommand(context.Background(), append(args, "-arm-paper"), &output); err != nil {
		t.Fatal(err)
	}
	var replay LocalPaperStepResult
	if err := json.Unmarshal(output.Bytes(), &replay); err != nil || replay.Order == nil || replay.Order.OrderID != firstID || replay.Order.Status != "FILLED" || before != paperAdmissionCountsForTest(t, svc) {
		t.Fatalf("CLI replay=%+v err=%v", replay, err)
	}
	assertHalted()
}

func TestLocalPaperCLIInitializeAndImport(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	evidence, selected := selectedPaperStrategy(t, svc)
	args := []string{"paper-init", "-db", dbPath, "-account", k2aAccountRef, "-result-sha256", evidence.ResultSHA256, "-expected-current-event", selected.CurrentEventID}
	var output bytes.Buffer
	if err := runLocalPaperCommand(context.Background(), args, &output); err != nil {
		t.Fatal(err)
	}
	var initial PaperAccountingSession
	if err := json.Unmarshal(output.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := runLocalPaperCommand(context.Background(), args, &output); err != nil {
		t.Fatal(err)
	}
	var again PaperAccountingSession
	if err := json.Unmarshal(output.Bytes(), &again); err != nil || again != initial {
		t.Fatalf("init reset session: %v", err)
	}
	state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	if err != nil || state.Armed {
		t.Fatal("init granted execution")
	}
	path := filepath.Join(t.TempDir(), "bars.csv")
	raw := []byte(paperSnapshotHeader + paperSnapshotRow("2026-01-09", "005930", "1005") + "\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := runLocalPaperCommand(context.Background(), []string{"paper-import-bars", "-db", dbPath, "-bars", path}, &output); err != nil {
		t.Fatal(err)
	}
	var snapshot PaperSnapshotImport
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil || snapshot.Added != 1 {
		t.Fatalf("CLI snapshot=%+v err=%v", snapshot, err)
	}
}

func TestLocalPaperMixedAccountCannotArm(t *testing.T) {
	svc, signal, _ := paperProposalFixture(t)
	mustRecordK2AOrder(t, svc, "mixed-local-paper")
	if _, err := svc.startLocalPaperExecution(context.Background(), k2aAccountRef, signal.StrategySelectionEventID); err == nil {
		t.Fatal("mixed-mode account armed")
	}
	state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	if err != nil || state.Armed || state.FencingToken != 0 {
		t.Fatal("failed arm left authority")
	}
}

func TestLocalPaperCLIRejectsMixedInitialization(t *testing.T) {
	svc, path := testService(t, nil, nil)
	evidence, selected := selectedPaperStrategy(t, svc)
	mustRecordK2AOrder(t, svc, "mixed-before-init")
	var output bytes.Buffer
	err := runLocalPaperCommand(context.Background(), []string{"paper-init", "-db", path, "-account", k2aAccountRef, "-result-sha256", evidence.ResultSHA256, "-expected-current-event", selected.CurrentEventID}, &output)
	if err == nil {
		t.Fatal("mixed initialization accepted")
	}
	_, found, err := loadPaperAccountingSession(context.Background(), svc.db, k2aAccountRef)
	if err != nil || found || output.Len() != 0 {
		t.Fatal("rejected initialization left a session")
	}
}

func TestLocalPaperOwnedHaltAndTakeover(t *testing.T) {
	svc, signal, _ := paperProposalFixture(t)
	ctx := context.Background()
	lease, err := svc.startLocalPaperExecution(ctx, k2aAccountRef, signal.StrategySelectionEventID)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return mustTime("2026-01-10T07:00:31Z") }
	if err := svc.haltOwnedSyntheticExecutionLease(ctx, k2aAccountRef, lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.acquireSyntheticExecutionLease(ctx, k2aAccountRef); err == nil {
		t.Fatal("clean halt silently rearmed")
	}
	lease, err = svc.startLocalPaperExecution(ctx, k2aAccountRef, signal.StrategySelectionEventID)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return mustTime("2026-01-10T07:01:02Z") }
	other := newService(svc.db, svc.now, randomID)
	next, err := other.acquireSyntheticExecutionLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.haltOwnedSyntheticExecutionLease(ctx, k2aAccountRef, lease.FencingToken); err == nil {
		t.Fatal("old owner halted takeover")
	}
	if _, err := other.requireCurrentSyntheticExecutionLease(ctx, svc.db, k2aAccountRef, next.FencingToken, svc.now()); err != nil {
		t.Fatal(err)
	}
	if err := other.haltOwnedSyntheticExecutionLease(ctx, k2aAccountRef, next.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
		t.Fatal(err)
	}
}

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLocalPaperRestartDrainsPartialFillsAcrossMissedBars(t *testing.T) {
	svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
	ctx := context.Background()
	raw := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, "golden_cross")
	first, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, raw, research)
	if err != nil || first.Order == nil || first.Order.Status != "OPEN" || first.Order.Quantity != "10" {
		t.Fatalf("initial order=%+v err=%v", first, err)
	}
	orderID := first.Order.OrderID

	for day := 10; day <= 14; day++ {
		rows = append(rows, localPaperRestartRow(fmt.Sprintf("2026-05-%02d", day), "101", "101", "4"))
	}
	raw = paperSnapshotCSV(rows...)
	proposal = localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, "none")

	reopened, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := newService(reopened, func() time.Time { return mustTime("2026-05-15T07:00:00Z") }, randomID)
	result, err := restarted.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, raw, research)
	if err != nil || result == nil || result.Order != nil {
		t.Fatalf("restart result=%+v err=%v", result, err)
	}
	state, err := restarted.loadOrderState(ctx, orderID)
	if err != nil || state.Status != "FILLED" || state.FilledQuantity != "10" {
		t.Fatalf("missed-bar convergence state=%+v err=%v", state, err)
	}
	accounts, err := replayPaperAccounting(ctx, restarted.db)
	account := accounts[k2aAccountRef]
	if err != nil || account.Cash != "8983.99" || account.CapitalizedFills != 5 ||
		len(account.Lots["005930"]) != 5 || paperAdmissionCountsForTest(t, restarted).Orders != 1 {
		t.Fatalf("missed-bar accounting=%+v err=%v", account, err)
	}
	beforeReplay := paperAdmissionCountsForTest(t, restarted)
	replayDB, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replayDB.Close() })
	replayService := newService(replayDB, func() time.Time { return mustTime("2026-05-15T07:00:00Z") }, randomID)
	replay, err := replayService.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, raw, research)
	replayedAccounts, accountErr := replayPaperAccounting(ctx, replayService.db)
	if err != nil || accountErr != nil || replay == nil || replay.Order != nil || replay.Snapshot.Added != 0 ||
		paperAdmissionCountsForTest(t, replayService) != beforeReplay || !samePaperAccountState(replayedAccounts[k2aAccountRef], account) {
		t.Fatalf("exact restart replay=%+v account=%+v errors=(%v,%v)", replay, replayedAccounts[k2aAccountRef], err, accountErr)
	}
	authority, err := loadExecutionAuthoritySnapshot(ctx, restarted.db, k2aAccountRef)
	if err != nil || authority.Armed || authority.LeaseOwner != "" {
		t.Fatalf("restart leaked authority=%+v err=%v", authority, err)
	}
}

func TestLocalPaperPolicyHaltCreatesNoNewOrder(t *testing.T) {
	svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
	ctx := context.Background()
	execute := func(kind string) *LocalPaperStepResult {
		t.Helper()
		raw := paperSnapshotCSV(rows...)
		proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, kind)
		result, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, raw, research)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	initial := execute("golden_cross")
	if initial.Order == nil || initial.Order.Status != "OPEN" || initial.Order.Quantity != "10" {
		t.Fatalf("initial order=%+v", initial.Order)
	}

	rows = append(rows, localPaperRestartRow("2026-05-10", "100", "100", "100"))
	svc.now = func() time.Time { return mustTime("2026-05-11T07:00:00Z") }
	filled := execute("none")
	state, err := svc.loadOrderState(ctx, initial.Order.OrderID)
	if err != nil || filled.Policy.Decision != "HOLD" || state.Status != "FILLED" || state.FilledQuantity != "10" {
		t.Fatalf("pre-halt fill=%+v policy=%+v err=%v", state, filled.Policy, err)
	}

	rows = append(rows, localPaperRestartRow("2026-05-11", "1", "1", "100"))
	svc.now = func() time.Time { return mustTime("2026-05-12T07:00:00Z") }
	before := paperAdmissionCountsForTest(t, svc)
	raw := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, "death_cross")
	halted, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, raw, research)
	if err != nil {
		t.Fatal(err)
	}
	after := paperAdmissionCountsForTest(t, svc)
	if halted.Order != nil || halted.Policy.Decision != "HALT_AND_ROLLBACK" ||
		halted.Policy.ReasonCode != "cumulative_return_floor_reached" || halted.Policy.RollbackSelectionEventID == "" || after != before {
		t.Fatalf("halt result=%+v admissions before=%+v after=%+v", halted, before, after)
	}
	authority, err := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
	selection, selectionErr := replayStrategyRegistry(ctx, svc.db)
	if err != nil || selectionErr != nil || authority.Armed || authority.LeaseOwner != "" ||
		selection.CurrentEventID != halted.Policy.RollbackSelectionEventID || selection.SelectedResultSHA256 != noStrategySelection {
		t.Fatalf("halt authority=%+v selection=%+v errors=(%v,%v)", authority, selection, err, selectionErr)
	}
	retryDB, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retryDB.Close() })
	retryService := newService(retryDB, func() time.Time { return mustTime("2026-05-12T07:00:00Z") }, randomID)
	if retry, retryErr := retryService.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, raw, research); retryErr == nil || retry != nil {
		t.Fatalf("old selection restarted after halt: result=%+v err=%v", retry, retryErr)
	}
	retryAuthority, err := loadExecutionAuthoritySnapshot(ctx, retryService.db, k2aAccountRef)
	if err != nil || retryAuthority.Armed || retryAuthority.LeaseOwner != "" || paperAdmissionCountsForTest(t, retryService) != after {
		t.Fatalf("halt retry authority=%+v admissions=%+v err=%v", retryAuthority, paperAdmissionCountsForTest(t, retryService), err)
	}
}

func TestLocalPaperNoCapacityReturnsWithoutProgress(t *testing.T) {
	svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
	ctx := context.Background()
	raw := paperSnapshotCSV(rows...)
	first, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID,
		localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, "golden_cross"), raw, research)
	if err != nil || first == nil || first.Order == nil {
		t.Fatalf("initial order=%+v err=%v", first, err)
	}
	rows = append(rows, localPaperRestartRow("2026-05-10", "101", "101", "0"))
	raw = paperSnapshotCSV(rows...)
	svc.now = func() time.Time { return mustTime("2026-05-11T07:00:00Z") }
	result, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID,
		localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, "none"), raw, research)
	state, stateErr := svc.loadOrderState(ctx, first.Order.OrderID)
	counts := paperAdmissionCountsForTest(t, svc)
	authority, authorityErr := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
	if err != nil || stateErr != nil || authorityErr != nil || result == nil || result.Order != nil ||
		state.Status != "OPEN" || state.FilledQuantity != "0" || counts.Orders != 1 || counts.Fills != 0 || authority.Armed || authority.LeaseOwner != "" {
		t.Fatalf("no-capacity result=%+v state=%+v counts=%+v authority=%+v errors=(%v,%v,%v)",
			result, state, counts, authority, err, stateErr, authorityErr)
	}
}

func localPaperRestartFixture(t *testing.T) (*Service, string, []byte, *StrategyEvidence, *StrategySelectionState, []string) {
	t.Helper()
	svc, dbPath := testService(t, nil, nil)
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
	slow := int(artifact["manifest"].(map[string]any)["strategy"].(map[string]any)["parameters"].(map[string]any)["slow_window"].(float64))
	rows := make([]string, 0, slow+1)
	for i := slow; i >= 0; i-- {
		day := mustTime("2026-05-09T00:00:00Z").AddDate(0, 0, -i).Format("2006-01-02")
		price := "99"
		if i == 0 {
			price = "100"
		}
		rows = append(rows, localPaperRestartRow(day, price, price, "100"))
	}
	svc.now = func() time.Time { return mustTime("2026-05-10T07:00:00Z") }
	return svc, dbPath, research, evidence, selected, rows
}

func localPaperRestartRow(day, open, close, volume string) string {
	return fmt.Sprintf("%sT06:30:00Z,005930,KRX,Asia/Seoul,1d,%s,%s,%s,%s,%s,%sT00:00:00Z,%sT06:31:00Z,%sT06:32:00Z", day, open, open, open, close, volume, day, day, day)
}

func localPaperRestartProposal(t *testing.T, svc *Service, resultSHA, row string, raw []byte, kind string) []byte {
	t.Helper()
	dataAsOf := strings.SplitN(row, ",", 2)[0]
	signal := PaperSignal{StrategyResultSHA256: resultSHA, Symbol: "005930", DataAsOf: dataAsOf, DataSHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	return paperProposalForTest(t, svc, signal, kind)
}

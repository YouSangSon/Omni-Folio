package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func paperInputStatusFixture(t *testing.T) (*Service, string, string, []byte, []byte, []byte) {
	t.Helper()
	svc, path, research, evidence, selected, rows := localPaperRestartFixture(t)
	bars := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "golden_cross")
	return svc, path, selected.CurrentEventID, proposal, bars, research
}

func paperInputStatusBundle(t *testing.T, proposal, bars, research []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": "paper-input-bundle.v1", "mode": "paper_bundle_only",
		"proposal": json.RawMessage(proposal), "bars_csv": string(bars), "research_csv": string(research),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPaperInputStatusCommandExistsWithoutArm(t *testing.T) {
	_, dbPath, selection, proposal, bars, research := paperInputStatusFixture(t)
	path := filepath.Join(t.TempDir(), "captured.json")
	if err := os.WriteFile(path, paperInputStatusBundle(t, proposal, bars, research), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runContext(context.Background(), []string{
		"paper-input-status", "-db", dbPath, "-account", k2aAccountRef,
		"-selection-event", selection, "-bundle", path,
	}); err != nil {
		t.Fatalf("read-only saved-input inspection is unavailable: %v", err)
	}
}

func TestPaperInputStatusExecutable(t *testing.T) {
	svc, dbPath, selection, proposal, bars, research := paperInputStatusFixture(t)
	path := filepath.Join(t.TempDir(), "captured.json")
	if err := os.WriteFile(path, paperInputStatusBundle(t, proposal, bars, research), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := provePaperRunnerLeaseRecovery(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	bin := buildLocalPaperExecutable(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, bin, "paper-input-status", "-db", dbPath,
		"-account", k2aAccountRef, "-selection-event", selection, "-bundle", path).CombinedOutput()
	if err != nil {
		t.Fatalf("native status command failed: %v\n%s", err, output)
	}
	var result PaperInputStatus
	if err := json.Unmarshal(output, &result); err != nil || result.BarState != "not_observed_in_this_database" || result.Completion != "unknown" || result.ExecutionAuthorized {
		t.Fatalf("native status contract differs: %v", err)
	}
	after, err := provePaperRunnerLeaseRecovery(context.Background(), svc.db)
	if err != nil || before.SHA256 != after.SHA256 {
		t.Fatalf("native inspection changed runner ownership: %v", err)
	}
	output, err = exec.CommandContext(ctx, bin, "paper-input-status", "--private-account-secret").CombinedOutput()
	if err == nil || bytes.Contains(output, []byte("private-account-secret")) || !bytes.Contains(output, []byte("paper-input-status options are invalid")) {
		t.Fatalf("native option error leaked or hid its category: %v", err)
	}
}

func inspectPaperInputForTest(t *testing.T, svc *Service, selection string, raw []byte) *PaperInputStatus {
	t.Helper()
	var sequence int
	var name, path string
	if err := svc.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	db, err := openPaperInputStatusDB(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := newService(db, svc.now, randomID).paperInputStatus(context.Background(), k2aAccountRef, selection, raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completion != "unknown" || result.ExecutionAuthorized || result.Freshness != "unverified" {
		t.Fatalf("inspection granted authority or completion: %+v", result)
	}
	return result
}

func TestPaperInputStatusRejectsRehashedStoredConflicts(t *testing.T) {
	svc, dbPath, selection, proposal, bars, research := paperInputStatusFixture(t)
	if _, err := svc.importPaperSnapshot(context.Background(), bars); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(bars)).ReadAll()
	if err != nil || len(records) < 4 {
		t.Fatalf("conflict fixture is invalid: %v", err)
	}
	encode := func(rows [][]string) []byte {
		var output bytes.Buffer
		writer := csv.NewWriter(&output)
		if err := writer.WriteAll(rows); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	changed := make([][]string, len(records))
	for i := range records {
		changed[i] = append([]string{}, records[i]...)
	}
	for i := 5; i <= 8; i++ {
		changed[1][i] = "98"
	}
	omitted := append(append([][]string{}, records[:2]...), records[3:]...)
	for name, input := range map[string]struct {
		bars  []byte
		cause string
	}{
		"changed stored bar":    {encode(changed), "conflicts with stored bars"},
		"omitted stored bar":    {encode(omitted), "omits stored history"},
		"changed receipt bytes": {bytes.ReplaceAll(bars, []byte("\n"), []byte("\r\n")), "differs from its first receipt"},
	} {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal(proposal, &body); err != nil {
				t.Fatal(err)
			}
			body["input_sha256"] = fmt.Sprintf("%x", sha256.Sum256(input.bars))
			raw := paperInputStatusBundle(t, hashPaperProposalForTest(t, body), input.bars, research)
			if _, err := svc.paperInputStatus(context.Background(), k2aAccountRef, selection, raw); err == nil || !strings.Contains(err.Error(), input.cause) {
				t.Fatalf("validly rehashed conflict did not reach its stored check: %v", err)
			}
			path := filepath.Join(t.TempDir(), "conflicting-account-secret.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := runPaperInputStatusCommand(context.Background(), []string{"-db", dbPath, "-account", k2aAccountRef, "-selection-event", selection, "-bundle", path}, &output)
			if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "account-secret") {
				t.Fatalf("stored conflict was emitted as absence or leaked: %v", err)
			}
		})
	}
}

func TestPaperInputStatusDistinguishesStoredFactsAndPastSelection(t *testing.T) {
	svc, _, selection, proposal, bars, research := paperInputStatusFixture(t)
	raw := paperInputStatusBundle(t, proposal, bars, research)
	result := inspectPaperInputForTest(t, svc, selection, raw)
	if result.BarState != "not_observed_in_this_database" || result.BarRecordedAt != nil || result.ProposalDeadline != nil || result.SignalRecorded || result.OrderStatus != nil || !result.CurrentSelectionMatches {
		t.Fatalf("unobserved input facts=%+v", result)
	}
	if _, err := svc.importPaperSnapshot(context.Background(), bars); err != nil {
		t.Fatal(err)
	}
	result = inspectPaperInputForTest(t, svc, selection, raw)
	if result.BarState != "matched" || result.BarRecordedAt == nil || result.ProposalDeadline == nil || result.SignalRecorded || result.OrderStatus != nil {
		t.Fatalf("import-only facts=%+v", result)
	}
	firstReceipt, deadline := *result.BarRecordedAt, *result.ProposalDeadline
	if _, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selection, proposal, bars, research); err != nil {
		t.Fatal(err)
	}
	result = inspectPaperInputForTest(t, svc, selection, raw)
	if !result.SignalRecorded || result.OrderStatus == nil || *result.OrderStatus != "OPEN" {
		t.Fatalf("admitted input facts=%+v", result)
	}
	if _, err := svc.rollbackPaperCandidate(context.Background(), selection, selection); err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return mustTime("2026-06-01T07:00:00Z") }
	result = inspectPaperInputForTest(t, svc, selection, raw)
	if result.CurrentSelectionMatches || !result.SignalRecorded || *result.BarRecordedAt != firstReceipt || *result.ProposalDeadline != deadline {
		t.Fatalf("past-selection inspection rewrote history: %+v", result)
	}
}

func TestPaperInputStatusNoneAndNoDeltaAreNotCompletion(t *testing.T) {
	for _, kind := range []string{"none", "death_cross"} {
		t.Run(kind, func(t *testing.T) {
			svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
			price := "99"
			if kind == "death_cross" {
				price = "98"
			}
			rows[len(rows)-1] = localPaperRestartRow("2026-05-09", price, price, "100")
			bars := paperSnapshotCSV(rows...)
			proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, kind)
			step, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selected.CurrentEventID, proposal, bars, research)
			if err != nil || step.Order != nil {
				t.Fatalf("no-order fixture failed: %v", err)
			}
			result := inspectPaperInputForTest(t, svc, selected.CurrentEventID, paperInputStatusBundle(t, proposal, bars, research))
			if result.BarState != "matched" || result.OrderStatus != nil || result.SignalRecorded != (kind == "death_cross") {
				t.Fatalf("none/no-delta facts were conflated: %+v", result)
			}
		})
	}
}

func TestPaperInputStatusHaltDoesNotCertifyUnadmittedInput(t *testing.T) {
	svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
	ctx := context.Background()
	bars := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "golden_cross")
	if _, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, bars, research); err != nil {
		t.Fatal(err)
	}
	rows = append(rows, localPaperRestartRow("2026-05-10", "100", "100", "12"),
		localPaperRestartRow("2026-05-11", "1", "1", "2"),
		localPaperRestartRow("2026-05-12", "100", "100", "100"))
	bars = paperSnapshotCSV(rows...)
	svc.now = func() time.Time { return mustTime("2026-05-13T07:00:00Z") }
	proposal = localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "none")
	step, err := svc.executeLocalPaper(ctx, k2aAccountRef, selected.CurrentEventID, proposal, bars, research)
	if err != nil || step.Policy == nil || step.Policy.Decision != "HALT_AND_ROLLBACK" {
		t.Fatalf("loss fixture did not halt: %v", err)
	}
	result := inspectPaperInputForTest(t, svc, selected.CurrentEventID, paperInputStatusBundle(t, proposal, bars, research))
	if result.CurrentSelectionMatches || result.SignalRecorded || result.OrderStatus != nil || result.BarState != "matched" {
		t.Fatalf("HALT was attributed as input completion: %+v", result)
	}
}

func TestPaperInputStatusTracksAnEarlierVerifiedBackup(t *testing.T) {
	svc, _, selection, proposal, bars, research := paperInputStatusFixture(t)
	backup := filepath.Join(t.TempDir(), "before-input.db")
	golden := writeCurrentSnapshot(t, svc.db)
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selection, proposal, bars, research); err != nil {
		t.Fatal(err)
	}
	raw := paperInputStatusBundle(t, proposal, bars, research)
	if result := inspectPaperInputForTest(t, svc, selection, raw); !result.SignalRecorded {
		t.Fatal("current database did not observe admission")
	}
	db, err := openPaperInputStatusDB(context.Background(), backup)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	older := newService(db, svc.now, randomID)
	result := inspectPaperInputForTest(t, older, selection, raw)
	if result.BarState != "not_observed_in_this_database" || result.SignalRecorded || result.OrderStatus != nil {
		t.Fatalf("inspection reused facts absent from the restored database: %+v", result)
	}
}

func TestPaperInputStatusReadOnlyURIAndClosedOutput(t *testing.T) {
	svc, _, selection, proposal, bars, research := paperInputStatusFixture(t)
	path := filepath.Join(t.TempDir(), "paper #?mode=rw.db")
	if _, err := svc.db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	db, err := openPaperInputStatusDB(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE forbidden_write(value TEXT)`); err == nil {
		db.Close()
		t.Fatal("inspection connection allowed a database write")
	}
	db.Close()
	bundle := filepath.Join(t.TempDir(), "captured.json")
	if err := os.WriteFile(bundle, paperInputStatusBundle(t, proposal, bars, research), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"-db", path, "-account", k2aAccountRef, "-selection-event", selection, "-bundle", bundle}
	if err := runPaperInputStatusCommand(context.Background(), args, &output); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(output.Bytes(), &value); err != nil || len(value) != 15 || value["completion"] != "unknown" || value["execution_authorized"] != false {
		t.Fatalf("inspection JSON contract differs: %v", err)
	}
	if bytes.Contains(output.Bytes(), []byte(k2aAccountRef)) || bytes.Contains(output.Bytes(), []byte(selection)) || bytes.Contains(output.Bytes(), research) {
		t.Fatal("inspection output leaked input identifiers or raw research")
	}
	if err := runPaperInputStatusCommand(context.Background(), append(args, "-arm-paper"), &bytes.Buffer{}); err == nil {
		t.Fatal("inspection accepted an execution flag")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if db, err := openPaperInputStatusDB(context.Background(), missing); err == nil {
		db.Close()
		t.Fatal("inspection created a database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing database path was changed")
	}
}

func TestPaperInputStatusRejectsMismatchesAndCorruption(t *testing.T) {
	svc, dbPath, selection, proposal, bars, research := paperInputStatusFixture(t)
	for name, raw := range map[string][]byte{
		"invalid bundle": []byte(`{"private":"account-secret"}`),
		"CSV bytes":      paperInputStatusBundle(t, proposal, bytes.ReplaceAll(bars, []byte("\n"), []byte("\r\n")), research),
		"research bytes": paperInputStatusBundle(t, proposal, bars, append(append([]byte{}, research...), '\n')),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.paperInputStatus(context.Background(), k2aAccountRef, selection, raw); err == nil {
				t.Fatal("inconsistent input was returned as absent")
			}
		})
	}
	raw := paperInputStatusBundle(t, proposal, bars, research)
	if _, err := svc.paperInputStatus(context.Background(), k2aAccountRef, "missing_selection", raw); err == nil {
		t.Fatal("missing selection was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.paperInputStatus(ctx, k2aAccountRef, selection, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER strategy_research_evidence_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE strategy_research_evidence SET artifact_json='account-secret'`); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "private-account-secret.json")
	if err := os.WriteFile(bundle, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := runPaperInputStatusCommand(context.Background(), []string{"-db", dbPath, "-account", k2aAccountRef, "-selection-event", selection, "-bundle", bundle}, &output)
	if err == nil || strings.Contains(err.Error(), "account-secret") || output.Len() != 0 {
		t.Fatalf("corruption was hidden or leaked: %v", err)
	}
}

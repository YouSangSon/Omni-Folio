package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestPaperPromotionImprovementCLIRequiresArgumentsAndBoundedRegularInputs(t *testing.T) {
	dir := t.TempDir()
	missingDB := filepath.Join(dir, "PRIVATE_DATABASE ?mode=memory#missing.db")
	artifact := filepath.Join(dir, "PRIVATE_ARTIFACT.json")
	bars := filepath.Join(dir, "PRIVATE_BARS.csv")
	for path, content := range map[string]string{artifact: "PRIVATE_ARTIFACT_INPUT", bars: "PRIVATE_CSV_INPUT"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	valid := []string{"-db", missingDB, "-account", k2aAccountRef, "-artifact", artifact, "-bars", bars, "-attempt-id", "required-improvement"}
	for index := 0; index < len(valid); index += 2 {
		args := append(append([]string(nil), valid[:index]...), valid[index+2:]...)
		var out bytes.Buffer
		err := runPaperImprovementCommand(context.Background(), args, &out)
		assertPaperImprovementCLIErrorRedactedForTest(t, err, &out, dir, "PRIVATE_")
	}
	for _, suffix := range [][]string{{"private-positional"}, {"-unknown", "PRIVATE_FLAG_VALUE"}} {
		var out bytes.Buffer
		err := runPaperImprovementCommand(context.Background(), append(append([]string(nil), valid...), suffix...), &out)
		assertPaperImprovementCLIErrorRedactedForTest(t, err, &out, dir, "PRIVATE_", "private-positional")
	}
	var out bytes.Buffer
	err := runPaperImprovementCommand(context.Background(), valid, &out)
	assertPaperImprovementCLIErrorRedactedForTest(t, err, &out, dir, "PRIVATE_")
	if err.Error() != "paper improvement database is unavailable" {
		t.Fatalf("missing database classification changed: %v", err)
	}
	fifo := filepath.Join(dir, "PRIVATE_FIFO")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "PRIVATE_FIFO_LINK")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		index, limit int
	}{
		{5, maxBodyBytes},
		{7, maxMarketDataBytes},
	} {
		oversized := filepath.Join(dir, valid[input.index-1]+"-oversized")
		if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), input.limit+1), 0600); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{fifo, link, dir, oversized, filepath.Join(dir, "PRIVATE_ABSENT_INPUT")} {
			args := append([]string(nil), valid...)
			args[input.index] = path
			var out bytes.Buffer
			err := runPaperImprovementCommand(context.Background(), args, &out)
			assertPaperImprovementCLIErrorRedactedForTest(t, err, &out, dir, "PRIVATE_")
		}
	}
	if _, err := os.Stat(missingDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid command/input created the missing database: %v", err)
	}
}

func TestPaperPromotionImprovementCLIExecutionErrorsExplainDurableRegistration(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	if _, err := f.svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, true); err != nil {
		t.Fatal(err)
	}
	artifact := paperImprovementArtifactForTest(t, f, "cli-registration-before-authority-failure")
	evidence, err := decodeStrategyArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "PRIVATE_RESEARCH_PATH.json")
	if err := os.WriteFile(path, artifact, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-db", f.dbPath, "-account", k2aAccountRef, "-artifact", path, "-bars", f.barsPath, "-attempt-id", "cli-authority-rejected"}
	before := paperImprovementStateForTest(t, f.svc)
	var out bytes.Buffer
	err = runPaperImprovementCommand(ctx, args, &out)
	assertPaperImprovementCLIErrorRedactedForTest(t, err, &out, path, f.dbPath, string(artifact), string(f.bars), "PRIVATE_")
	for _, advice := range []string{"strategy-status", "paper-promotion-status", "same attempt ID"} {
		if !strings.Contains(err.Error(), advice) {
			t.Fatalf("execution failure omitted recovery advice %q: %v", advice, err)
		}
	}
	assertPaperImprovementNoOperationalChange(t, before, paperImprovementStateForTest(t, f.svc))
	if paperImprovementEvidenceCountForTest(t, f.svc, evidence.ResultSHA256) != 1 {
		t.Fatal("CLI comparison failure lost the independently committed registration")
	}
	if _, _, found, err := loadPaperPromotion(ctx, f.svc.db, "cli-authority-rejected"); err != nil || found {
		t.Fatalf("authority failure recorded an attempt: found=%t, %v", found, err)
	}
	corruptDB := filepath.Join(t.TempDir(), "PRIVATE_CORRUPT_DATABASE.db")
	corruptBytes := []byte("PRIVATE_SQLITE_SENTINEL")
	if err := os.WriteFile(corruptDB, corruptBytes, 0600); err != nil {
		t.Fatal(err)
	}
	corruptArgs := append([]string(nil), args...)
	corruptArgs[1] = corruptDB
	out.Reset()
	err = runPaperImprovementCommand(ctx, corruptArgs, &out)
	assertPaperImprovementCLIErrorRedactedForTest(t, err, &out, corruptDB, "PRIVATE_", "file is not a database")
	if stored, err := os.ReadFile(corruptDB); err != nil || !bytes.Equal(stored, corruptBytes) {
		t.Fatalf("failed database open initialized or rewrote the file: %v", err)
	}
	if err := os.WriteFile(f.barsPath, []byte("PRIVATE_INVALID_CSV"), 0600); err != nil {
		t.Fatal(err)
	}
	before = paperImprovementStateForTest(t, f.svc)
	out.Reset()
	err = runPaperImprovementCommand(ctx, args, &out)
	assertPaperImprovementCLIErrorRedactedForTest(t, err, &out, path, f.dbPath, "PRIVATE_")
	if after := paperImprovementStateForTest(t, f.svc); after != before {
		t.Fatal("CLI CSV preflight failure changed database state")
	}
}

func TestPaperPromotionImprovementCLIWriterFailureKeepsRegistrationAndComparison(t *testing.T) {
	f := newPaperPromotionFixture(t)
	artifact := paperImprovementArtifactForTest(t, f, "cli-output-failure")
	evidence, err := decodeStrategyArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "research.json")
	if err := os.WriteFile(path, artifact, 0600); err != nil {
		t.Fatal(err)
	}
	const attemptID = "improvement-output-failed"
	args := []string{"-db", f.dbPath, "-account", k2aAccountRef, "-artifact", path, "-bars", f.barsPath, "-attempt-id", attemptID}
	outputErr := errors.New("PRIVATE_RECEIPT_OUTPUT_CLOSED")
	if err := runPaperImprovementCommand(context.Background(), args, ledgerExportFailWriter{outputErr}); !errors.Is(err, outputErr) {
		t.Fatalf("receipt writer error identity lost: %v", err)
	}
	status, err := f.svc.paperPromotionStatus(context.Background(), attemptID)
	if err != nil || status == nil || status.Decision != "PROMOTE" || status.SelectionEventID == "" || paperImprovementEvidenceCountForTest(t, f.svc, evidence.ResultSHA256) != 1 {
		t.Fatalf("output failure lost committed handoff: %+v, %v", status, err)
	}
	before := paperImprovementStateForTest(t, f.svc)
	var out bytes.Buffer
	if err := runPaperImprovementCommand(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	receipt := decodePaperImprovementReceiptForTest(t, out.Bytes())
	if receipt.ResultSHA256 != evidence.ResultSHA256 || !receipt.Registered || !receipt.AttemptRecorded || receipt.Disposition != "comparison" || !reflect.DeepEqual(receipt.Comparison, status) {
		t.Fatalf("output retry changed the saved receipt: %s", out.String())
	}
	if after := paperImprovementStateForTest(t, f.svc); after != before {
		t.Fatal("output retry added research, claims, or selection records")
	}
}

func assertPaperImprovementCLIErrorRedactedForTest(t *testing.T, err error, out *bytes.Buffer, private ...string) {
	t.Helper()
	if err == nil || out.Len() != 0 {
		t.Fatalf("failed improvement command exposed a receipt: %s, %v", out.String(), err)
	}
	for _, value := range private {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("improvement command exposed private input or path: %v", err)
		}
	}
}

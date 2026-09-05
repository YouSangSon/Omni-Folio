package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func paperStreamBundle(t *testing.T, svc *Service, result string, rows []string, research []byte, kind string) []byte {
	t.Helper()
	bars := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, result, rows[len(rows)-1], bars, kind)
	raw, err := json.Marshal(map[string]any{"schema_version": "paper-input-bundle.v1", "mode": "paper_bundle_only", "proposal": json.RawMessage(proposal), "bars_csv": string(bars), "research_csv": string(research)})
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func TestPaperStreamProcessesOrderedBundlesWithOneArm(t *testing.T) {
	svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
	first := paperStreamBundle(t, svc, evidence.ResultSHA256, rows, research, "golden_cross")
	rows = append(rows, localPaperRestartRow("2026-05-10", "101", "101", "100"))
	second := paperStreamBundle(t, svc, evidence.ResultSHA256, rows, research, "none")
	payload := bytes.Join([][]byte{first, first, second}, nil)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := writer.Write(payload); _ = writer.Close(); done <- err }()
	err = svc.runLocalPaperStream(context.Background(), k2aAccountRef, selected.CurrentEventID, reader)
	_ = reader.Close()
	_ = writer.Close()
	writeErr := <-done
	if err != nil || writeErr != nil {
		t.Fatalf("stream errors: %v %v", err, writeErr)
	}
	counts := paperAdmissionCountsForTest(t, svc)
	// A 'none' proposal is not a persisted signal; its new bar still fills.
	if counts.Orders != 1 || counts.Fills != 1 || counts.Signals != 1 {
		t.Fatalf("ordered stream counts=%+v", counts)
	}
	var arms int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM execution_authority_events WHERE reason_code='manual_arm'`).Scan(&arms); err != nil {
		t.Fatal(err)
	}
	state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	if err != nil || arms != 1 || state.Armed || state.LeaseOwner != "" {
		t.Fatalf("stream rearmed or leaked: arms=%d state=%+v err=%v", arms, state, err)
	}
}

func TestPaperStreamFrameBoundary(t *testing.T) {
	for _, test := range []struct {
		name    string
		raw     []byte
		frames  int
		invalid bool
	}{
		{"empty", nil, 0, false},
		{"ordered", []byte("one\ntwo\n"), 2, false},
		{"incomplete", []byte("one\ntwo"), 1, true},
		{"max", append(bytes.Repeat([]byte{'x'}, maxPaperBundleBytes-1), '\n'), 1, false},
		{"oversized", append(bytes.Repeat([]byte{'x'}, maxPaperBundleBytes), '\n'), 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner := bufio.NewScanner(bytes.NewReader(test.raw))
			scanner.Buffer(make([]byte, 4096), maxPaperBundleBytes+1)
			scanner.Split(splitPaperBundle)
			count := 0
			for scanner.Scan() {
				count++
			}
			if count != test.frames || (scanner.Err() != nil) != test.invalid {
				t.Fatalf("frames=%d err=%v", count, scanner.Err())
			}
		})
	}
}

func TestPaperStreamCancelPartialReadWithoutArm(t *testing.T) {
	svc, _, _, _, selected, _ := localPaperRestartFixture(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := writer.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := svc.runLocalPaperStream(ctx, k2aAccountRef, selected.CurrentEventID, reader); err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("partial read did not cancel: %v", err)
	}
	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM execution_authority_events").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial frame armed: count=%d err=%v", count, err)
	}
}

func TestPaperStreamInvalidFrameCleansCommittedPrefix(t *testing.T) {
	svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
	raw := paperStreamBundle(t, svc, evidence.ResultSHA256, rows, research, "golden_cross")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer writer.Close()
		_, _ = writer.Write(append(raw, []byte("{}\n")...))
	}()
	err = svc.runLocalPaperStream(context.Background(), k2aAccountRef, selected.CurrentEventID, reader)
	_ = writer.Close()
	<-done
	state, stateErr := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	global, globalErr := loadPaperRunnerLease(context.Background(), svc.db)
	if err == nil || stateErr != nil || globalErr != nil || state.Armed || state.LeaseOwner != "" || global.record.OwnerID != "" || paperAdmissionCountsForTest(t, svc).Orders != 1 {
		t.Fatalf("invalid suffix lost prefix or leaked: %+v %+v %v/%v/%v", state, global, err, stateErr, globalErr)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPaperBundleBoundary(t *testing.T) {
	svc, _, research, evidence, _, rows := localPaperRestartFixture(t)
	bars := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "golden_cross")
	bundle := map[string]any{"schema_version": "paper-input-bundle.v1", "mode": "paper_bundle_only", "proposal": json.RawMessage(proposal), "bars_csv": "한국\r\n😀", "research_csv": string(research)}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	gotProposal, gotBars, gotResearch, err := decodePaperInputBundle(raw)
	if err != nil || !bytes.Equal(gotProposal, proposal) || string(gotBars) != "한국\r\n😀" || !bytes.Equal(gotResearch, research) {
		t.Fatalf("byte roundtrip: %v", err)
	}
	escaped := bytes.Replace(raw, []byte("😀"), []byte(`\ud83d\ude00`), 1)
	if _, got, _, err := decodePaperInputBundle(escaped); err != nil || !bytes.Equal(got, gotBars) {
		t.Fatalf("valid surrogate pair changed: %q %v", got, err)
	}
	for name, bad := range map[string][]byte{
		"duplicate":        append([]byte(`{"mode":"paper_bundle_only",`), raw[1:]...),
		"unknown":          append([]byte(`{"authority":true,`), raw[1:]...),
		"trailing":         append(append([]byte{}, raw...), []byte(`{}`)...),
		"nested_duplicate": bytes.Replace(raw, []byte(`"proposal":{`), []byte(`"proposal":{"mode":"paper_proposal_only",`), 1),
		"oversize":         bytes.Repeat([]byte{' '}, maxPaperBundleBytes+1),
		"invalid_utf8":     bytes.Replace(raw, []byte("한국"), []byte{0xff}, 1),
		"lone_surrogate":   bytes.Replace(raw, []byte("한국"), []byte(`\ud800`), 1),
		"lone_low":         bytes.Replace(raw, []byte("한국"), []byte(`\udfff`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := decodePaperInputBundle(bad); err == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
	for key, value := range map[string]any{"schema_version": "other", "mode": "live", "research_csv": nil, "bars_csv": 12, "proposal": nil} {
		original := bundle[key]
		bundle[key] = value
		bad, _ := json.Marshal(bundle)
		if _, _, _, err := decodePaperInputBundle(bad); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
		delete(bundle, key)
		bad, _ = json.Marshal(bundle)
		if _, _, _, err := decodePaperInputBundle(bad); err == nil {
			t.Fatalf("missing %s accepted", key)
		}
		bundle[key] = original
	}
	bundle["bars_csv"] = strings.Repeat("x", maxBodyBytes+1)
	raw, _ = json.Marshal(bundle)
	if _, _, _, err := decodePaperInputBundle(raw); err == nil {
		t.Fatal("oversize CSV accepted")
	}
}

func TestPaperBundleRejectsUnboundInputs(t *testing.T) {
	for _, field := range []string{"bars_csv", "research_csv"} {
		t.Run(field, func(t *testing.T) {
			svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
			bars := paperSnapshotCSV(rows...)
			proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "golden_cross")
			bundle := map[string]any{"schema_version": "paper-input-bundle.v1", "mode": "paper_bundle_only", "proposal": json.RawMessage(proposal), "bars_csv": string(bars), "research_csv": string(research)}
			bundle[field] = bundle[field].(string) + "\n"
			raw, _ := json.Marshal(bundle)
			path := filepath.Join(t.TempDir(), "forged.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			before := paperAdmissionCountsForTest(t, svc)
			var output bytes.Buffer
			err := runLocalPaperCommand(context.Background(), []string{"paper-execute", "-db", dbPath, "-account", k2aAccountRef, "-expected-current-event", selected.CurrentEventID, "-arm-paper", "-bundle", path}, &output)
			if err == nil || output.Len() != 0 || before != paperAdmissionCountsForTest(t, svc) {
				t.Fatalf("unbound %s admitted: %v", field, err)
			}
			authority, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			if err != nil || authority.Armed || authority.LeaseOwner != "" {
				t.Fatalf("invalid input armed: %+v %v", authority, err)
			}
		})
	}
}

func TestPaperBundleMalformedInputDoesNotCreateDB(t *testing.T) {
	dir := t.TempDir()
	path, db := filepath.Join(dir, "bundle.json"), filepath.Join(dir, "absent.db")
	for _, raw := range [][]byte{[]byte(`{}`), bytes.Repeat([]byte{' '}, maxPaperBundleBytes+1)} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := runLocalPaperCommand(context.Background(), []string{"paper-execute", "-db", db, "-account", k2aAccountRef, "-expected-current-event", "selection_test", "-arm-paper", "-bundle", path}, &output); err == nil || output.Len() != 0 {
			t.Fatal("bad file accepted")
		}
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Fatal("bad bundle touched absent DB")
		}
	}
}

func TestPaperBundlePythonCLIToGoExecution(t *testing.T) {
	svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
	dir := t.TempDir()
	bars := bytes.ReplaceAll(paperSnapshotCSV(rows...), []byte("\n"), []byte("\r\n"))
	for name, raw := range map[string][]byte{"bars.csv": bars, "research.csv": research, "artifact.json": []byte(evidence.artifactJSON)} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-m", "omni_research.signal_cli", "--bars", filepath.Join(dir, "bars.csv"), "--research-bars", filepath.Join(dir, "research.csv"), "--artifact", filepath.Join(dir, "artifact.json"), "--bundle")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+filepath.Join(root, "services/research"))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	_, decodedBars, decodedResearch, err := decodePaperInputBundle(raw)
	if err != nil || !bytes.Equal(decodedBars, bars) || !bytes.Equal(decodedResearch, research) {
		t.Fatalf("producer snapshots changed: %v", err)
	}
	bundlePath := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(bundlePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bars.csv", "research.csv", "artifact.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("replaced after capture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"paper-execute", "-db", dbPath, "-account", k2aAccountRef, "-expected-current-event", selected.CurrentEventID, "-arm-paper", "-bundle", bundlePath}
	var output bytes.Buffer
	if err := runLocalPaperCommand(ctx, args, &output); err != nil {
		t.Fatal(err)
	}
	var result LocalPaperStepResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Order == nil || result.Order.Status != "OPEN" || result.Order.Quantity != "10" {
		t.Fatalf("bundle order=%+v", result.Order)
	}
	before := paperAdmissionCountsForTest(t, svc)
	output.Reset()
	if err := runLocalPaperCommand(ctx, args, &output); err != nil {
		t.Fatal(err)
	}
	if before != paperAdmissionCountsForTest(t, svc) {
		t.Fatal("bundle replay appended admission")
	}
	authority, err := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
	if err != nil || authority.Armed || authority.LeaseOwner != "" {
		t.Fatalf("authority leaked: %+v %v", authority, err)
	}
	for _, extra := range [][]string{{"-bars", "missing"}, {"-proposal", "missing"}, {"-research-bars", "missing"}, {"-arm-paper=false"}} {
		if err := runLocalPaperCommand(ctx, append(append([]string{}, args...), extra...), &output); err == nil {
			t.Fatal("ambiguous/unarmed bundle accepted")
		}
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

func verifyBenchmarkSeriesRestoreProof(path string) (benchmarkSeriesRecoveryProof, error) {
	db, err := openReadOnlyExistingDB(context.Background(), path)
	if err != nil {
		return benchmarkSeriesRecoveryProof{}, err
	}
	defer db.Close()
	return proveBenchmarkSeriesRecovery(context.Background(), db)
}

func requireBenchmarkManifestFields(fields, receipt map[string]json.RawMessage, current bool) error {
	for _, group := range []struct {
		fields map[string]json.RawMessage
		keys   []string
	}{
		{fields, []string{"benchmark_series_state_sha256", "benchmark_series_version_count", "benchmark_series_point_count"}},
		{receipt, []string{"benchmark_series_check", "candidate_benchmark_series_state_sha256", "candidate_benchmark_series_version_count", "candidate_benchmark_series_point_count"}},
	} {
		for _, key := range group.keys {
			value, present := group.fields[key]
			if current && (!present || bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
				return errors.New("backup omits benchmark series proof")
			}
			if !current && present {
				return errors.New("legacy backup cannot claim benchmark series proof")
			}
		}
	}
	return nil
}

func benchmarkManifestMatches(manifest BackupManifest, proof benchmarkSeriesRecoveryProof) bool {
	r := manifest.VerificationReceipt
	return manifest.BenchmarkSeriesStateSHA256 == proof.SHA256 && manifest.BenchmarkSeriesVersionCount == proof.Versions && manifest.BenchmarkSeriesPointCount == proof.Points &&
		r.BenchmarkSeriesCheck == "ok" && r.CandidateBenchmarkSeriesStateSHA256 == proof.SHA256 && r.CandidateBenchmarkSeriesVersionCount == proof.Versions && r.CandidateBenchmarkSeriesPointCount == proof.Points
}

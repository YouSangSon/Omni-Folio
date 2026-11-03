package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

func verifyOwnerFXSeriesRestoreProof(path string) (ownerFXSeriesRecoveryProof, error) {
	db, err := openReadOnlyExistingDB(context.Background(), path)
	if err != nil {
		return ownerFXSeriesRecoveryProof{}, err
	}
	defer db.Close()
	return proveOwnerFXSeriesRecovery(context.Background(), db)
}

func requireOwnerFXManifestFields(fields, receipt map[string]json.RawMessage, current bool) error {
	for _, group := range []struct {
		fields map[string]json.RawMessage
		keys   []string
	}{
		{fields, []string{"owner_fx_series_state_sha256", "owner_fx_series_version_count", "owner_fx_series_point_count"}},
		{receipt, []string{"owner_fx_series_check", "candidate_owner_fx_series_state_sha256", "candidate_owner_fx_series_version_count", "candidate_owner_fx_series_point_count"}},
	} {
		for _, key := range group.keys {
			value, present := group.fields[key]
			if current && (!present || bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
				return errors.New("backup omits owner FX series proof")
			}
			if !current && present {
				return errors.New("legacy backup cannot claim owner FX series proof")
			}
		}
	}
	return nil
}

func ownerFXManifestMatches(manifest BackupManifest, proof ownerFXSeriesRecoveryProof) bool {
	r := manifest.VerificationReceipt
	return manifest.OwnerFXSeriesStateSHA256 == proof.SHA256 && manifest.OwnerFXSeriesVersionCount == proof.Versions && manifest.OwnerFXSeriesPointCount == proof.Points &&
		r.OwnerFXSeriesCheck == "ok" && r.CandidateOwnerFXSeriesStateSHA256 == proof.SHA256 && r.CandidateOwnerFXSeriesVersionCount == proof.Versions && r.CandidateOwnerFXSeriesPointCount == proof.Points
}

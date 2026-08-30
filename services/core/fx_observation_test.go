package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFXObservationRecordReplayConflictAndExactSeries(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	first := FXObservationInput{
		Source: "local_fixture", SourceObservationID: "usdkrw_1", BaseCurrency: "USD", QuoteCurrency: "KRW",
		Rate: "1370.25", ObservedAt: "2026-01-10T15:00:00Z", FetchedAt: "2026-01-10T15:00:01Z",
	}
	stored, err := svc.recordFXObservation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.recordFXObservation(ctx, first)
	if err != nil || !reflect.DeepEqual(stored, replayed) {
		t.Fatalf("exact replay drifted: stored=%+v replayed=%+v err=%v", stored, replayed, err)
	}
	changed := first
	changed.Rate = "1371"
	if _, err := svc.recordFXObservation(ctx, changed); err == nil {
		t.Fatal("observation identity was rebound to a different rate")
	}
	sameSlot := first
	sameSlot.SourceObservationID = "usdkrw_other"
	if _, err := svc.recordFXObservation(ctx, sameSlot); err == nil {
		t.Fatal("source/pair/time slot accepted two identities")
	}
	second := first
	second.SourceObservationID = "usdkrw_2"
	second.Rate = "1380"
	second.ObservedAt = "2026-01-10T15:01:00Z"
	second.FetchedAt = "2026-01-10T15:01:01Z"
	if _, err := svc.recordFXObservation(ctx, second); err != nil {
		t.Fatal(err)
	}

	observations, proof, err := replayFXObservations(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 || observations[0].SourceObservationID != first.SourceObservationID || observations[1].SourceObservationID != second.SourceObservationID ||
		observations[0].Rate != "1370.25" || observations[1].Rate != "1380" || proof.Observations != 2 || len(proof.SHA256) != 64 {
		t.Fatalf("FX series/proof drifted: observations=%+v proof=%+v", observations, proof)
	}
	snapshot, err := snapshotFrom(ctx, svc.db)
	if err != nil || snapshot.ValuationStatus != "unavailable" {
		t.Fatalf("storage leaf changed valuation authority: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestFXObservationRejectsInvalidBoundaryAndDirectWrites(t *testing.T) {
	valid := FXObservationInput{
		Source: "local_fixture", SourceObservationID: "fx_obs_valid", BaseCurrency: "USD", QuoteCurrency: "KRW",
		Rate: "1370.25", ObservedAt: "2026-01-10T15:00:00Z", FetchedAt: "2026-01-10T15:00:01Z",
	}
	tests := map[string]func(*FXObservationInput){
		"empty id":       func(v *FXObservationInput) { v.SourceObservationID = "" },
		"unknown source": func(v *FXObservationInput) { v.Source = "kiwoom" },
		"lower currency": func(v *FXObservationInput) { v.BaseCurrency = "usd" },
		"same currency":  func(v *FXObservationInput) { v.QuoteCurrency = "USD" },
		"zero":           func(v *FXObservationInput) { v.Rate = "0" },
		"negative":       func(v *FXObservationInput) { v.Rate = "-1" },
		"exponent":       func(v *FXObservationInput) { v.Rate = "1e3" },
		"trailing zero":  func(v *FXObservationInput) { v.Rate = "1370.250" },
		"offset time":    func(v *FXObservationInput) { v.ObservedAt = "2026-01-11T00:00:00+09:00" },
		"offset fetch":   func(v *FXObservationInput) { v.FetchedAt = "2026-01-11T00:00:01+09:00" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			observation := valid
			mutate(&observation)
			if _, err := svc.recordFXObservation(context.Background(), observation); err == nil {
				t.Fatalf("invalid observation was accepted: %+v", observation)
			}
		})
	}

	svc, _ := testService(t, nil, nil)
	for _, statement := range []string{
		`INSERT INTO fx_observations(observation_id,source,source_observation_id,base_currency,quote_currency,rate,observed_at,fetched_at,record_sha256,recorded_at) VALUES('same','local_fixture','same','USD','USD','1','2026-01-10T15:00:00Z','2026-01-10T15:00:01Z','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-01-10T15:00:02Z')`,
		`INSERT INTO fx_observations(observation_id,source,source_observation_id,base_currency,quote_currency,rate,observed_at,fetched_at,record_sha256,recorded_at) VALUES('bad-rate','local_fixture','bad-rate','USD','KRW','1.0','2026-01-10T15:00:00Z','2026-01-10T15:00:01Z','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-01-10T15:00:02Z')`,
		`INSERT INTO fx_observations(observation_id,source,source_observation_id,base_currency,quote_currency,rate,observed_at,fetched_at,record_sha256,recorded_at) VALUES('bad-time','local_fixture','bad-time','USD','KRW','1','2026-01-11T00:00:00+09:00','2026-01-10T15:00:01Z','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-01-10T15:00:02Z')`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("schema accepted invalid observation: %s", statement)
		}
	}
}

func TestFXObservationRecoveryDetectsCorruptionAndMutation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	observation := FXObservationInput{
		Source: "local_fixture", SourceObservationID: "fx_obs_corruption", BaseCurrency: "USD", QuoteCurrency: "KRW",
		Rate: "1370", ObservedAt: "2026-01-10T15:00:00Z", FetchedAt: "2026-01-10T15:00:01Z",
	}
	if _, err := svc.recordFXObservation(context.Background(), observation); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE fx_observations SET rate=rate`,
		`DELETE FROM fx_observations`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("insert-only storage accepted mutation: %s", statement)
		}
	}
	if _, err := svc.db.Exec(`DROP TRIGGER fx_observations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE fx_observations SET rate='1371'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := replayFXObservations(context.Background(), svc.db); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("corrupt FX observation was certified: %v", err)
	}
}

func TestFXObservationBackupProof(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{
		Source: "local_fixture", SourceObservationID: "fx_obs_backup", BaseCurrency: "USD", QuoteCurrency: "KRW",
		Rate: "1370", ObservedAt: "2026-01-10T15:00:00Z", FetchedAt: "2026-01-10T15:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := t.TempDir() + "/fx.db"
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v6" || manifest.SchemaVersion != "omni-folio.sqlite.v10" ||
		manifest.FXObservationCount != 1 || len(manifest.FXObservationStateSHA256) != 64 ||
		manifest.VerificationReceipt.FXObservationCheck != "ok" ||
		manifest.VerificationReceipt.CandidateFXObservationStateSHA256 != manifest.FXObservationStateSHA256 {
		t.Fatalf("backup omitted FX observation proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	tampered := readJSONMap(t, manifestPath)
	tampered["fx_observation_state_sha256"] = strings.Repeat("0", 64)
	tamperedPath := t.TempDir() + "/tampered-manifest.json"
	writeJSONFile(t, tamperedPath, tampered)
	if err := verifyManifest(backup, golden, tamperedPath); err == nil {
		t.Fatal("manifest with a different FX observation digest was accepted")
	}
}

func TestFXObservationLatestDirectReadIsExactAndSanitized(t *testing.T) {
	svc, _ := testService(t, nil, map[string][]string{"fx_observation": {"fx_observation_001", "fx_observation_002"}})
	for _, input := range []FXObservationInput{
		{Source: "local_fixture", SourceObservationID: "provider-1", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1370", ObservedAt: "2026-01-10T15:00:00Z", FetchedAt: "2026-01-10T15:00:01Z"},
		{Source: "local_fixture", SourceObservationID: "provider-2", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1380", ObservedAt: "2026-01-10T15:01:00Z", FetchedAt: "2026-01-10T15:01:01Z"},
	} {
		if _, err := svc.recordFXObservation(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	request := func(path string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return w.Code, body
	}
	status, body := request("/v1/market-data/fx/latest?source=local_fixture&base_currency=USD&quote_currency=KRW&as_of=2026-01-10T15:00:30Z")
	if status != http.StatusOK || body["observation_id"] != "fx_observation_001" || body["rate"] != "1370" ||
		body["sample"] != true || body["state"] != "stale" || body["source_observation_id"] != nil {
		t.Fatalf("unexpected direct FX response: status=%d body=%#v", status, body)
	}
	unsafe := map[string]int{
		"/v1/market-data/fx/latest?source=local_fixture&base_currency=KRW&quote_currency=USD&as_of=2026-01-10T15:00:30Z":                      http.StatusNotFound,
		"/v1/market-data/fx/latest?source=local_fixture&base_currency=USD&quote_currency=KRW":                                                 http.StatusBadRequest,
		"/v1/market-data/fx/latest?source=local_fixture&source=local_fixture&base_currency=USD&quote_currency=KRW&as_of=2026-01-10T15:00:30Z": http.StatusBadRequest,
		"/v1/market-data/fx/latest?source=local_fixture&base_currency=USD&quote_currency=KRW&as_of=2026-01-10T15:00:30Z&inverse=true":         http.StatusBadRequest,
		"/v1/market-data/fx/latest?source=kiwoom&base_currency=USD&quote_currency=KRW&as_of=2026-01-10T15:00:30Z":                             http.StatusBadRequest,
		"/v1/market-data/fx/latest?source=local_fixture&base_currency=usd&quote_currency=KRW&as_of=2026-01-10T15:00:30Z":                      http.StatusBadRequest,
		"/v1/market-data/fx/latest?source=local_fixture&base_currency=USD&quote_currency=USD&as_of=2026-01-10T15:00:30Z":                      http.StatusBadRequest,
		"/v1/market-data/fx/latest?source=local_fixture&base_currency=USD&quote_currency=KRW&as_of=2026-01-11T00:00:30%2B09:00":               http.StatusBadRequest,
		"/v1/market-data/fx/latest?source=local_fixture;base_currency=USD&quote_currency=KRW&as_of=2026-01-10T15:00:30Z":                      http.StatusBadRequest,
	}
	for path, wantStatus := range unsafe {
		status, _ := request(path)
		if status != wantStatus {
			t.Fatalf("unsafe FX query status=%d want=%d path=%s", status, wantStatus, path)
		}
	}
}

func TestFXObservationOpenAPIIsClosedReadOnlyAndDirect(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	path := document["paths"].(map[string]any)["/v1/market-data/fx/latest"].(map[string]any)
	if len(path) != 1 || path["get"] == nil {
		t.Fatal("FX observation route must be GET-only")
	}
	schema := document["components"].(map[string]any)["schemas"].(map[string]any)["LocalFixtureFXObservation"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatal("FX observation response must be closed")
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"source_observation_id", "account_id", "portfolio_value", "inverse_rate", "cross_rate"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("FX observation contract exposes %s", forbidden)
		}
	}
}

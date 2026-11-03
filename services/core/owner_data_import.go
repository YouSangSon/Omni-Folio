package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

const ownerDataPriceKind = "security_price"
const ownerDataFXKind = "fx"
const ownerDataBenchmarkKind = "benchmark"
const ownerDataPreviewResponseLimit = 6*maxBodyBytes + 16*1024

var errOwnerDataRejected = errors.New("owner data input is not admissible")
var errOwnerDataConflict = errors.New("owner data input conflicts with stored identity or source")

type OwnerDataImportPreview struct {
	Kind    string                     `json:"kind"`
	Summary map[string]json.RawMessage `json:"summary"`
	Input   string                     `json:"input"`
}

type OwnerDataImportResult struct {
	Kind    string `json:"kind"`
	Receipt any    `json:"receipt"`
}

func ownerDataKind(raw []byte) (string, error) {
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if len(raw) == 0 || len(raw) > maxBodyBytes || json.Unmarshal(raw, &header) != nil {
		return "", errOwnerDataRejected
	}
	switch header.SchemaVersion {
	case ownerPriceSeriesSchema:
		return ownerDataPriceKind, nil
	case ownerFXSeriesSchema:
		return ownerDataFXKind, nil
	case benchmarkSeriesSchema:
		return ownerDataBenchmarkKind, nil
	default:
		return "", errOwnerDataRejected
	}
}

func previewOwnerData(ctx context.Context, raw []byte) (*OwnerDataImportPreview, error) {
	kind, err := ownerDataKind(raw)
	if err != nil {
		return nil, err
	}
	var receipt any
	switch kind {
	case ownerDataPriceKind:
		i, _, sha, err := decodeOwnerPriceSeries(ctx, raw)
		if err != nil {
			return nil, err
		}
		receipt = ownerPriceReceipt(&ownerPriceSeriesRecord{Input: *i, DataSHA256: sha})
	case ownerDataFXKind:
		i, _, sha, err := decodeOwnerFXSeries(ctx, raw)
		if err != nil {
			return nil, err
		}
		receipt = ownerFXReceipt(&ownerFXSeriesRecord{Input: *i, DataSHA256: sha})
	case ownerDataBenchmarkKind:
		i, _, sha, err := decodeBenchmarkSeries(ctx, raw)
		if err != nil {
			return nil, err
		}
		receipt = benchmarkReceipt(&benchmarkSeriesRecord{Input: *i, DataSHA256: sha})
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &summary); err != nil {
		return nil, err
	}
	// An input-only review must not claim that storage or admission succeeded.
	delete(summary, "storage_check")
	delete(summary, "recorded_at")
	return &OwnerDataImportPreview{Kind: kind, Summary: summary, Input: string(raw)}, nil
}

func ownerDataImportError(err error) *appError {
	switch {
	case errors.Is(err, errOwnerDataRejected), errors.Is(err, errOwnerPriceInput), errors.Is(err, errOwnerFXInput), errors.Is(err, errBenchmarkSeriesInput):
		return &appError{http.StatusBadRequest, APIError{Code: "owner_data_invalid", Message: "owner data input does not meet the declared contract"}}
	case errors.Is(err, errOwnerDataConflict):
		return &appError{http.StatusConflict, APIError{Code: "owner_data_conflict", Message: "owner data identity or source conflicts with stored data"}}
	default:
		// Database errors may contain private values; preserve uncertainty without echoing them.
		return &appError{http.StatusInternalServerError, APIError{Code: "owner_data_unavailable", Message: "owner data storage could not be confirmed; retry the same input"}}
	}
}

func readOwnerDataBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		return nil, errOwnerDataRejected
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, errOwnerDataRejected
	}
	return raw, nil
}

func (s *Service) handleOwnerDataPreview(w http.ResponseWriter, r *http.Request) {
	raw, err := readOwnerDataBody(w, r)
	if err != nil {
		writeError(w, ownerDataImportError(err))
		return
	}
	preview, err := previewOwnerData(r.Context(), raw)
	if err != nil {
		writeError(w, ownerDataImportError(err))
		return
	}
	encoded, err := json.Marshal(preview)
	if err != nil || len(encoded)+1 > ownerDataPreviewResponseLimit {
		writeError(w, ownerDataImportError(errors.New("owner review response exceeds its bound")))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func (s *Service) handleOwnerDataApply(w http.ResponseWriter, r *http.Request) {
	raw, err := readOwnerDataBody(w, r)
	if err != nil {
		writeError(w, ownerDataImportError(err))
		return
	}
	kind, err := ownerDataKind(raw)
	if err != nil {
		writeError(w, ownerDataImportError(err))
		return
	}
	var receipt any
	switch kind {
	case ownerDataPriceKind:
		receipt, err = s.importOwnerPriceSeries(r.Context(), raw)
	case ownerDataFXKind:
		receipt, err = s.importOwnerFXSeries(r.Context(), raw)
	case ownerDataBenchmarkKind:
		receipt, err = s.importBenchmarkSeries(r.Context(), raw)
	}
	if err != nil {
		writeError(w, ownerDataImportError(err))
		return
	}
	writeJSON(w, http.StatusOK, OwnerDataImportResult{Kind: kind, Receipt: receipt})
}

// Call after full journal proof and the exact-retry path, in the same write transaction.
func ownerDataRecordTime(ctx context.Context, q queryer, kind string, clock func() time.Time, fetched string) (string, error) {
	var query string
	switch kind {
	case ownerDataPriceKind:
		query = `SELECT recorded_at FROM owner_price_series_versions ORDER BY sequence DESC LIMIT 1`
	case ownerDataFXKind:
		query = `SELECT recorded_at FROM owner_fx_series_versions ORDER BY sequence DESC LIMIT 1`
	case ownerDataBenchmarkKind:
		query = `SELECT recorded_at FROM benchmark_series_versions ORDER BY sequence DESC LIMIT 1`
	default:
		return "", errors.New("owner data clock kind is invalid")
	}
	var previous string
	err := q.QueryRowContext(ctx, query).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	now := clock().UTC()
	recorded := now.Format(time.RFC3339Nano)
	if !canonicalUTCString(recorded) {
		return "", errors.New("owner data record clock is invalid")
	}
	if previous != "" {
		last, err := time.Parse(time.RFC3339Nano, previous)
		if err != nil || !canonicalUTCString(previous) || now.Before(last) {
			return "", errors.New("owner data record clock moved backwards")
		}
	}
	captured, err := time.Parse(time.RFC3339Nano, fetched)
	if err != nil || !canonicalUTCString(fetched) {
		return "", errors.New("owner data capture time is invalid")
	}
	if now.Before(captured) {
		return "", errOwnerDataRejected
	}
	return recorded, nil
}

func isOwnerDataImportPath(path string) bool {
	return path == "/v1/market-data/imports/preview" || path == "/v1/market-data/imports/apply"
}

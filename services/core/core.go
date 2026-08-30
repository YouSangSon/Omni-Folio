package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3"
)

const (
	maxBodyBytes       = 1 << 20
	maxImportRows      = 10_000
	latestSchema       = 10
	zeroTime           = "1970-01-01T00:00:00Z"
	csvSchema          = "omni-folio.csv.v1"
	mappingSchema      = "canonical-transaction.v4"
	backupFormat       = "omni-folio-backup.v6"
	backupSchema       = "omni-folio.sqlite.v10"
	legacyBackupFormat = "omni-folio-backup.v5"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var decimalPattern = regexp.MustCompile(`^(?:0|-?(?:[1-9][0-9]*(?:\.[0-9]*[1-9])?|0\.[0-9]*[1-9]))$`)

type Service struct {
	db                *sql.DB
	now               func() time.Time
	id                func(string) string
	ttl               time.Duration
	marketData        MarketDataPort
	executionOwner    string
	activityCursorKey [32]byte
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

type Transaction struct {
	EventID               string `json:"event_id"`
	SourceEventID         string `json:"source_event_id"`
	AccountID             string `json:"account_id"`
	Type                  string `json:"type"`
	OccurredAt            string `json:"occurred_at"`
	InstrumentID          string `json:"instrument_id,omitempty"`
	Symbol                string `json:"symbol,omitempty"`
	Quantity              string `json:"quantity,omitempty"`
	Price                 string `json:"price,omitempty"`
	Fee                   string `json:"fee,omitempty"`
	Currency              string `json:"currency"`
	Amount                string `json:"amount"`
	CounterCurrency       string `json:"counter_currency,omitempty"`
	CounterAmount         string `json:"counter_amount,omitempty"`
	CorrectsSourceEventID string `json:"corrects_source_event_id,omitempty"`
}

type PreviewRow struct {
	RowNumber        int               `json:"row_number"`
	Status           string            `json:"status"`
	Transaction      *Transaction      `json:"transaction,omitempty"`
	CorrectionTarget *CorrectionTarget `json:"correction_target,omitempty"`
	DuplicateOf      string            `json:"duplicate_of,omitempty"`
	Errors           []APIError        `json:"errors,omitempty"`
	Resolution       *Resolution       `json:"resolution,omitempty"`
}

type CorrectionTarget struct {
	SourceEventID string `json:"source_event_id"`
	Type          string `json:"type"`
	Currency      string `json:"currency"`
	Amount        string `json:"amount"`
}

type Resolution struct {
	Kind           string   `json:"kind"`
	SourceField    string   `json:"source_field"`
	SourceValue    string   `json:"source_value"`
	RequiredAction string   `json:"required_action"`
	CandidateIDs   []string `json:"candidate_ids"`
}

type PreviewTotals struct {
	TotalRows      int `json:"total_rows"`
	NewRows        int `json:"new_rows"`
	DuplicateRows  int `json:"duplicate_rows"`
	ErrorRows      int `json:"error_rows"`
	UnresolvedRows int `json:"unresolved_rows"`
}

type ImportPreview struct {
	PreviewID          string        `json:"preview_id"`
	FileSHA256         string        `json:"file_sha256"`
	SchemaVersion      string        `json:"schema_version"`
	MappingVersion     string        `json:"mapping_version"`
	LedgerRevision     string        `json:"ledger_revision"`
	PreviewFingerprint string        `json:"preview_fingerprint"`
	CanApply           bool          `json:"can_apply"`
	Rows               []PreviewRow  `json:"rows"`
	Totals             PreviewTotals `json:"totals"`
}

type ApplyRequest struct {
	PreviewID      string `json:"preview_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type ApplyReceipt struct {
	ReceiptID            string `json:"receipt_id"`
	PreviewID            string `json:"preview_id"`
	IdempotencyKey       string `json:"idempotency_key"`
	FileSHA256           string `json:"file_sha256"`
	LedgerRevisionBefore string `json:"ledger_revision_before"`
	LedgerRevisionAfter  string `json:"ledger_revision_after"`
	AppliedRows          int    `json:"applied_rows"`
	SkippedDuplicateRows int    `json:"skipped_duplicate_rows"`
	ReceivedAt           string `json:"received_at"`
	RecordedAt           string `json:"recorded_at"`
}

type Money struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type Holding struct {
	InstrumentID string `json:"instrument_id"`
	Symbol       string `json:"symbol"`
	Quantity     string `json:"quantity"`
	CostBasis    string `json:"cost_basis"`
	Currency     string `json:"currency"`
}

type Provenance struct {
	EventIDs   []string `json:"event_ids"`
	ReceiptIDs []string `json:"receipt_ids"`
}

type PortfolioSnapshot struct {
	PortfolioID     string     `json:"portfolio_id"`
	LedgerRevision  string     `json:"ledger_revision"`
	AsOf            string     `json:"as_of"`
	RecordedAt      string     `json:"recorded_at"`
	LiveEnabled     bool       `json:"live_enabled"`
	ValuationStatus string     `json:"valuation_status"`
	Cash            []Money    `json:"cash"`
	Holdings        []Holding  `json:"holdings"`
	RealizedPnL     []Money    `json:"realized_pnl"`
	Provenance      Provenance `json:"provenance"`
}

type BackupManifest struct {
	FormatVersion                string              `json:"format_version"`
	SchemaVersion                string              `json:"schema_version"`
	CreatedAt                    string              `json:"created_at"`
	SourceLedgerRevision         string              `json:"source_ledger_revision"`
	OrderStateSHA256             string              `json:"order_state_sha256"`
	OrderCount                   int                 `json:"order_count"`
	OrderEventCount              int                 `json:"order_event_count"`
	ExecutionAuthoritySHA256     string              `json:"execution_authority_sha256"`
	ExecutionAuthorityEventCount int                 `json:"execution_authority_event_count"`
	RiskReservationSHA256        string              `json:"risk_reservation_sha256"`
	RiskReservationCount         int                 `json:"risk_reservation_count"`
	BrokerStateSHA256            string              `json:"broker_state_sha256"`
	BrokerSnapshotCount          int                 `json:"broker_snapshot_count"`
	BrokerReconciliationCount    int                 `json:"broker_reconciliation_count"`
	StrategyRegistrySHA256       string              `json:"strategy_registry_sha256"`
	StrategyEvidenceCount        int                 `json:"strategy_evidence_count"`
	StrategySelectionEventCount  int                 `json:"strategy_selection_event_count"`
	SelectedStrategyResultSHA256 string              `json:"selected_strategy_result_sha256"`
	FXObservationStateSHA256     string              `json:"fx_observation_state_sha256"`
	FXObservationCount           int                 `json:"fx_observation_count"`
	DBSHA256                     string              `json:"db_sha256"`
	SizeBytes                    int64               `json:"size_bytes"`
	ExpectedSnapshotSHA256       string              `json:"expected_snapshot_sha256"`
	Encryption                   BackupEncryption    `json:"encryption"`
	VerificationReceipt          VerificationReceipt `json:"verification_receipt"`
}

type BackupEncryption struct {
	Encrypted bool   `json:"encrypted"`
	Algorithm string `json:"algorithm"`
}

type VerificationReceipt struct {
	ReceiptID                         string   `json:"receipt_id"`
	CandidateID                       string   `json:"candidate_id"`
	VerifiedAt                        string   `json:"verified_at"`
	Status                            string   `json:"status"`
	IntegrityCheck                    string   `json:"integrity_check"`
	GoldenSnapshotCheck               string   `json:"golden_snapshot_check"`
	OrderStateCheck                   string   `json:"order_state_check"`
	BrokerStateCheck                  string   `json:"broker_state_check"`
	StrategyRegistryCheck             string   `json:"strategy_registry_check"`
	FXObservationCheck                string   `json:"fx_observation_check"`
	CandidateDBSHA256                 string   `json:"candidate_db_sha256"`
	CandidateSnapshotSHA256           string   `json:"candidate_snapshot_sha256"`
	CandidateOrderStateSHA256         string   `json:"candidate_order_state_sha256"`
	CandidateBrokerStateSHA256        string   `json:"candidate_broker_state_sha256"`
	CandidateStrategyRegistrySHA256   string   `json:"candidate_strategy_registry_sha256"`
	CandidateFXObservationStateSHA256 string   `json:"candidate_fx_observation_state_sha256"`
	EligibleForActivation             bool     `json:"eligible_for_activation"`
	Errors                            []string `json:"errors"`
}

type appError struct {
	status int
	body   APIError
}

func (e *appError) Error() string { return e.body.Message }

func openDB(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func openExistingDB(path string) (*sql.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("database must already exist; run migrate first: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("database path must be a regular file")
	}
	return openDB(path)
}

func requireSchema(db *sql.DB) error {
	var first, latest, count int
	if err := db.QueryRow(`SELECT COALESCE(MIN(version), 0), COALESCE(MAX(version), 0), COUNT(*) FROM schema_migrations`).Scan(&first, &latest, &count); err != nil {
		return fmt.Errorf("database is not migrated; run migrate first: %w", err)
	}
	if first != 1 || latest != latestSchema || count != latestSchema {
		return fmt.Errorf("unsupported schema history %d..%d (%d migrations)", first, latest, count)
	}
	return nil
}

func migrate(db *sql.DB) error {
	var exists int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&exists); err != nil {
		return err
	}
	current := 0
	if exists != 0 {
		var first, count int
		if err := db.QueryRow(`SELECT COALESCE(MIN(version), 0), COALESCE(MAX(version), 0), COUNT(*) FROM schema_migrations`).Scan(&first, &current, &count); err != nil {
			return err
		}
		if first != 1 || current > latestSchema || count != current {
			return fmt.Errorf("unsupported schema version %d", current)
		}
	}
	files := []string{"001_init.sql", "002_orders.sql", "003_broker_snapshots.sql", "004_execution_authority.sql", "005_ledger_events.sql", "006_strategy_registry.sql", "007_paper_orders.sql", "008_cash_void.sql", "009_fx_exchange.sql", "010_fx_observations.sql"}
	for version := current + 1; version <= latestSchema; version++ {
		script, err := migrationFiles.ReadFile("migrations/" + files[version-1])
		if err != nil {
			return err
		}
		disableForeignKeys := version == 7
		checkForeignKeys := version >= 7
		if disableForeignKeys {
			if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				return err
			}
		}
		tx, err := db.Begin()
		if err != nil {
			if disableForeignKeys {
				_, _ = db.Exec(`PRAGMA foreign_keys=ON`)
			}
			return err
		}
		if _, err := tx.Exec(string(script)); err != nil {
			tx.Rollback()
			if disableForeignKeys {
				_, _ = db.Exec(`PRAGMA foreign_keys=ON`)
			}
			return err
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			if disableForeignKeys {
				_, _ = db.Exec(`PRAGMA foreign_keys=ON`)
			}
			return err
		}
		if checkForeignKeys {
			var violations int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
				tx.Rollback()
				_, _ = db.Exec(`PRAGMA foreign_keys=ON`)
				if err != nil {
					return err
				}
				return fmt.Errorf("migration %d foreign-key check failed: violations=%d", version, violations)
			}
		}
		if err := tx.Commit(); err != nil {
			if disableForeignKeys {
				_, _ = db.Exec(`PRAGMA foreign_keys=ON`)
			}
			return err
		}
		if disableForeignKeys {
			if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
				return err
			}
		}
	}
	return nil
}

func newService(db *sql.DB, now func() time.Time, id func(string) string) *Service {
	service := &Service{db: db, now: now, id: id, ttl: 15 * time.Minute, executionOwner: randomID("execution_owner")}
	// ponytail: local single-process cursors expire on restart; inject one shared key before multi-replica pagination.
	if _, err := rand.Read(service.activityCursorKey[:]); err != nil {
		panic(err)
	}
	return service
}

func randomID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b)
}

func revision(n int64) string { return fmt.Sprintf("rev_%010d", n) }

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("GET /v1/market-data/candles", s.handleMarketDataCandles)
	mux.HandleFunc("GET /v1/market-data/fx/latest", s.handleLatestFXObservation)
	mux.HandleFunc("GET /v1/portfolio/cash-valuation", s.handleCashValuation)
	mux.HandleFunc("GET /v1/broker-reconciliation/latest", s.handleLatestBrokerReconciliation)
	mux.HandleFunc("GET /v1/orders", s.handleLocalOrders)
	mux.HandleFunc("GET /v1/ledger/activities", s.handleLedgerActivities)
	mux.HandleFunc("POST /v1/imports/preview", s.handlePreview)
	mux.HandleFunc("POST /v1/imports/apply", s.handleApply)
	mux.HandleFunc("GET /v1/portfolio/snapshot", s.handleSnapshot)
	return mux
}

func (s *Service) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := requireSchema(s.db); err != nil {
		writeError(w, readinessError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func validateAllowedOrigin(origin string) error {
	if origin == "" {
		return nil
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("allow-origin must be an exact http(s) origin without path, query, credentials, or fragment")
	}
	return nil
}

func withCORS(next http.Handler, allowedOrigin string) http.Handler {
	if allowedOrigin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if r.Header.Get("Origin") != allowedOrigin {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		if r.Method == http.MethodOptions {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	var rev int64
	var verified sql.NullString
	if err := s.db.QueryRowContext(r.Context(), `SELECT revision, last_verified_at FROM ledger_meta WHERE singleton=1`).Scan(&rev, &verified); err != nil {
		writeError(w, internalError(err))
		return
	}
	var lastVerified *string
	trustState := "never_verified"
	if verified.Valid {
		lastVerified = &verified.String
		trustState = "verified"
	}
	writeJSON(w, http.StatusOK, struct {
		Service        string     `json:"service"`
		LiveEnabled    bool       `json:"live_enabled"`
		Mode           string     `json:"mode"`
		TrustState     string     `json:"trust_state"`
		LedgerRevision string     `json:"ledger_revision"`
		LastVerifiedAt *string    `json:"last_verified_at"`
		Issues         []APIError `json:"issues"`
	}{"omni-folio", false, "local_import_only", trustState, revision(rev), lastVerified, []APIError{}})
}

func (s *Service) handlePreview(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/csv" {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_content_type", Message: "Content-Type must be text/csv"}})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_body", Message: "CSV body is too large or unreadable"}})
		return
	}
	preview, appErr := s.preview(r.Context(), body)
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Service) handleApply(w http.ResponseWriter, r *http.Request) {
	var req ApplyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, err)
		return
	}
	receipt, appErr := s.apply(r.Context(), req)
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Service) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := snapshotFrom(r.Context(), s.db)
	if err != nil {
		writeError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Service) handleLatestBrokerReconciliation(w http.ResponseWriter, r *http.Request) {
	result, err := s.latestBrokerReconciliation(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, &appError{http.StatusNotFound, APIError{Code: "broker_reconciliation_not_found", Message: "broker reconciliation was not found"}})
		return
	}
	if err != nil {
		writeError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) *appError {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return &appError{http.StatusBadRequest, APIError{Code: "invalid_content_type", Message: "Content-Type must be application/json"}}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return &appError{http.StatusBadRequest, APIError{Code: "invalid_json", Message: "request body must match the contract"}}
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return &appError{http.StatusBadRequest, APIError{Code: "invalid_json", Message: "request body must contain one JSON object"}}
	}
	return nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) preview(ctx context.Context, body []byte) (*ImportPreview, *appError) {
	if len(body) == 0 {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "empty_csv", Message: "CSV body must not be empty"}}
	}
	if len(body) > maxBodyBytes {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "csv_too_large", Message: "CSV body must not exceed 1048576 bytes"}}
	}
	if !utf8.Valid(body) {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_encoding", Message: "CSV body must be UTF-8"}}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, internalError(err)
	}
	defer tx.Rollback()
	var rev int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM ledger_meta WHERE singleton=1`).Scan(&rev); err != nil {
		return nil, internalError(err)
	}
	rows, parseErr := s.parseCSV(ctx, tx, body)
	if parseErr != nil {
		return nil, parseErr
	}
	totals := PreviewTotals{TotalRows: len(rows)}
	for _, row := range rows {
		switch row.Status {
		case "new":
			totals.NewRows++
		case "duplicate":
			totals.DuplicateRows++
		case "error":
			totals.ErrorRows++
		case "unresolved":
			totals.UnresolvedRows++
		}
	}
	hash := sha256.Sum256(body)
	created := s.now().UTC()
	preview := &ImportPreview{
		PreviewID: s.id("preview"), FileSHA256: hex.EncodeToString(hash[:]), SchemaVersion: csvSchema, MappingVersion: mappingSchema,
		LedgerRevision: revision(rev), CanApply: totals.ErrorRows == 0 && totals.UnresolvedRows == 0,
		Rows: rows, Totals: totals,
	}
	preview.PreviewFingerprint = previewFingerprint(preview.FileSHA256, preview.SchemaVersion, preview.MappingVersion, preview.LedgerRevision)
	encoded, err := json.Marshal(preview)
	if err != nil {
		return nil, internalError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO previews(preview_id,file_sha256,ledger_revision,can_apply,preview_json,expires_at,created_at) VALUES(?,?,?,?,?,?,?)`,
		preview.PreviewID, preview.FileSHA256, rev, boolInt(preview.CanApply), string(encoded), created.Add(s.ttl).Format(time.RFC3339Nano), created.Format(time.RFC3339Nano)); err != nil {
		return nil, internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return preview, nil
}

func (s *Service) parseCSV(ctx context.Context, q queryer, body []byte) ([]PreviewRow, *appError) {
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_csv", Message: "body must be valid CSV with a header"}}
	}
	if len(records)-1 > maxImportRows {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "too_many_rows", Message: "CSV must not contain more than 10000 data rows"}}
	}
	required := []string{"source_event_id", "account_id", "type", "occurred_at", "symbol", "quantity", "price", "fee", "currency", "amount"}
	columns := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		if _, exists := columns[name]; exists {
			return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_csv_header", Message: "CSV header names must be unique"}}
		}
		columns[name] = i
	}
	for _, name := range required {
		if _, ok := columns[name]; !ok {
			return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_csv_header", Message: "CSV is missing required column " + name, Field: name}}
		}
	}
	value := func(record []string, name string) string {
		i, ok := columns[name]
		if !ok || i >= len(record) {
			return ""
		}
		return record[i]
	}
	seen := map[string]PreviewRow{}
	seenCorrectionTargets := map[string]bool{}
	result := make([]PreviewRow, 0, len(records)-1)
	for i, record := range records[1:] {
		row := PreviewRow{RowNumber: i + 2}
		tx, fieldErrors := s.normalize(record, value)
		if len(fieldErrors) != 0 {
			row.Status, row.Errors = "error", fieldErrors
			result = append(result, row)
			continue
		}
		if resolution := unresolved(tx); resolution != nil {
			row.Status, row.Transaction, row.Resolution = "unresolved", tx, resolution
			result = append(result, row)
			continue
		}
		key := tx.AccountID + "\x00" + tx.SourceEventID
		if prior, ok := seen[key]; ok {
			if !sameTransaction(prior.Transaction, tx) {
				row.Status, row.Transaction, row.Errors = "error", tx, []APIError{{Code: "source_event_conflict", Message: "source_event_id was already used with different transaction data", Field: "source_event_id"}}
			} else {
				tx.EventID = prior.Transaction.EventID
				row.Status, row.Transaction, row.CorrectionTarget, row.DuplicateOf = "duplicate", tx, prior.CorrectionTarget, tx.EventID
			}
			result = append(result, row)
			continue
		}
		stored, err := loadTransactionBySource(ctx, q, tx.AccountID, tx.SourceEventID)
		switch {
		case err == nil:
			if !sameTransaction(stored, tx) {
				row.Status, row.Transaction, row.Errors = "error", tx, []APIError{{Code: "source_event_conflict", Message: "source_event_id was already used with different transaction data", Field: "source_event_id"}}
				break
			}
			tx.EventID = stored.EventID
			row.Status, row.Transaction, row.DuplicateOf = "duplicate", tx, stored.EventID
			if tx.Type == "CASH_VOID" {
				target, validationErr, err := validateCashVoid(ctx, q, tx, false)
				if err != nil {
					return nil, internalError(err)
				}
				if validationErr != nil {
					return nil, internalError(errors.New("stored cash void failed validation"))
				}
				row.CorrectionTarget = target
			}
		case errors.Is(err, sql.ErrNoRows):
			row.Status, row.Transaction = "new", tx
			if tx.Type == "CASH_VOID" {
				target, validationErr, err := validateCashVoid(ctx, q, tx, true)
				if err != nil {
					return nil, internalError(err)
				}
				if validationErr != nil {
					row.Status, row.Errors = "error", []APIError{*validationErr}
				} else {
					targetKey := tx.AccountID + "\x00" + tx.CorrectsSourceEventID
					if seenCorrectionTargets[targetKey] {
						row.Status, row.Errors = "error", []APIError{*invalidCorrection()}
					} else {
						seenCorrectionTargets[targetKey] = true
						row.CorrectionTarget = target
					}
				}
			}
		case err != nil:
			return nil, internalError(err)
		}
		if row.Status != "error" {
			seen[key] = row
		}
		result = append(result, row)
	}
	return result, nil
}

func loadTransactionBySource(ctx context.Context, q queryer, accountID, sourceEventID string) (*Transaction, error) {
	var tx Transaction
	err := q.QueryRowContext(ctx, `SELECT event_id,source_event_id,account_id,type,occurred_at,COALESCE(instrument_id,''),COALESCE(symbol,''),COALESCE(quantity,''),COALESCE(price,''),COALESCE(fee,''),currency,amount,COALESCE(counter_currency,''),COALESCE(counter_amount,''),COALESCE(corrects_source_event_id,'') FROM events WHERE account_id=? AND source_event_id=?`, accountID, sourceEventID).Scan(
		&tx.EventID, &tx.SourceEventID, &tx.AccountID, &tx.Type, &tx.OccurredAt, &tx.InstrumentID, &tx.Symbol, &tx.Quantity, &tx.Price, &tx.Fee, &tx.Currency, &tx.Amount, &tx.CounterCurrency, &tx.CounterAmount, &tx.CorrectsSourceEventID,
	)
	return &tx, err
}

func sameTransaction(a, b *Transaction) bool {
	if a == nil || b == nil {
		return false
	}
	left, right := *a, *b
	left.EventID, right.EventID = "", ""
	return left == right
}

func validateCashVoid(ctx context.Context, q queryer, correction *Transaction, requireUnused bool) (*CorrectionTarget, *APIError, error) {
	target, err := loadTransactionBySource(ctx, q, correction.AccountID, correction.CorrectsSourceEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalidCorrection(), nil
	}
	if err != nil {
		return nil, nil, err
	}
	eligible := target.Type == "DEPOSIT" || target.Type == "WITHDRAWAL" || target.Type == "DIVIDEND" || target.Type == "FEE" || target.Type == "TAX"
	targetTime, targetTimeErr := time.Parse(time.RFC3339Nano, target.OccurredAt)
	correctionTime, correctionTimeErr := time.Parse(time.RFC3339Nano, correction.OccurredAt)
	targetAmount, targetAmountErr := parseDecimal(target.Amount)
	correctionAmount, correctionAmountErr := parseDecimal(correction.Amount)
	if !eligible || target.Currency != correction.Currency || targetTimeErr != nil || correctionTimeErr != nil || correctionTime.Before(targetTime) || targetAmountErr != nil || correctionAmountErr != nil || new(big.Rat).Add(targetAmount, correctionAmount).Sign() != 0 {
		return nil, invalidCorrection(), nil
	}
	if requireUnused {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE account_id=? AND corrects_source_event_id=?`, correction.AccountID, correction.CorrectsSourceEventID).Scan(&count); err != nil {
			return nil, nil, err
		}
		if count != 0 {
			return nil, invalidCorrection(), nil
		}
	}
	return &CorrectionTarget{SourceEventID: target.SourceEventID, Type: target.Type, Currency: target.Currency, Amount: target.Amount}, nil, nil
}

func invalidCorrection() *APIError {
	return &APIError{Code: "invalid_correction", Message: "cash void does not exactly reverse one eligible existing cash event", Field: "corrects_source_event_id"}
}

func (s *Service) normalize(record []string, value func([]string, string) string) (*Transaction, []APIError) {
	get := func(name string) string { return value(record, name) }
	tx := &Transaction{
		EventID: s.id("event"), SourceEventID: get("source_event_id"), AccountID: get("account_id"),
		Type: get("type"), OccurredAt: get("occurred_at"), Symbol: get("symbol"), Quantity: get("quantity"),
		Price: get("price"), Fee: get("fee"), Currency: get("currency"), Amount: get("amount"),
		CounterCurrency: get("counter_currency"), CounterAmount: get("counter_amount"), CorrectsSourceEventID: get("corrects_source_event_id"),
	}
	var errs []APIError
	for _, field := range []struct{ name, value string }{{"source_event_id", tx.SourceEventID}, {"account_id", tx.AccountID}} {
		if field.value == "" {
			errs = append(errs, APIError{"required", field.name + " is required", field.name})
		}
	}
	if occurredAt, err := time.Parse(time.RFC3339, tx.OccurredAt); err != nil {
		errs = append(errs, APIError{"invalid_datetime", "occurred_at must be an RFC 3339 timestamp", "occurred_at"})
	} else {
		tx.OccurredAt = occurredAt.UTC().Format(time.RFC3339Nano)
	}
	if len(tx.Currency) != 3 || tx.Currency != strings.ToUpper(tx.Currency) || strings.IndexFunc(tx.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
		errs = append(errs, APIError{"invalid_currency", "currency must be a three-letter uppercase code", "currency"})
	}
	amount, err := parseDecimal(tx.Amount)
	if err != nil {
		errs = append(errs, APIError{"invalid_decimal", "amount must be a canonical decimal", "amount"})
	}
	if tx.Type != "CASH_VOID" && tx.CorrectsSourceEventID != "" {
		errs = append(errs, APIError{"invalid_fields", "corrects_source_event_id is only valid for CASH_VOID", "corrects_source_event_id"})
	}
	if tx.Type != "FX_EXCHANGE" && (tx.CounterCurrency != "" || tx.CounterAmount != "") {
		errs = append(errs, APIError{"invalid_fields", "counter currency and amount are only valid for FX_EXCHANGE", "counter_currency"})
	}
	switch tx.Type {
	case "DEPOSIT", "WITHDRAWAL", "FEE", "TAX":
		if tx.Symbol != "" || tx.Quantity != "" || tx.Price != "" || tx.Fee != "" {
			errs = append(errs, APIError{"invalid_fields", tx.Type + " trade fields must be empty", "symbol"})
		}
		tx.Symbol, tx.Quantity, tx.Price, tx.Fee = "", "", "", ""
		if err == nil && ((tx.Type == "DEPOSIT" && amount.Sign() <= 0) || (tx.Type != "DEPOSIT" && amount.Sign() >= 0)) {
			direction := "negative"
			if tx.Type == "DEPOSIT" {
				direction = "positive"
			}
			errs = append(errs, APIError{"invalid_amount", tx.Type + " amount must be " + direction, "amount"})
		}
	case "DIVIDEND":
		if tx.Symbol == "" {
			errs = append(errs, APIError{"required", "symbol is required for DIVIDEND", "symbol"})
		} else {
			tx.InstrumentID = "instrument_" + strings.ToLower(tx.Symbol)
		}
		if tx.Quantity != "" || tx.Price != "" || tx.Fee != "" {
			errs = append(errs, APIError{"invalid_fields", "DIVIDEND quantity, price, and fee must be empty", "quantity"})
		}
		tx.Quantity, tx.Price, tx.Fee = "", "", ""
		if err == nil && amount.Sign() <= 0 {
			errs = append(errs, APIError{"invalid_amount", "DIVIDEND amount must be positive", "amount"})
		}
	case "SPLIT":
		if tx.Symbol == "" {
			errs = append(errs, APIError{"required", "symbol is required for SPLIT", "symbol"})
		} else {
			tx.InstrumentID = "instrument_" + strings.ToLower(tx.Symbol)
		}
		positiveDecimal(tx.Quantity, "quantity", &errs)
		if tx.Price != "" || tx.Fee != "" {
			errs = append(errs, APIError{"invalid_fields", "SPLIT price and fee must be empty", "price"})
		}
		tx.Price, tx.Fee = "", ""
		if err == nil && amount.Sign() != 0 {
			errs = append(errs, APIError{"invalid_amount", "SPLIT amount must be zero", "amount"})
		}
	case "CASH_VOID":
		if tx.Symbol != "" || tx.Quantity != "" || tx.Price != "" || tx.Fee != "" {
			errs = append(errs, APIError{"invalid_fields", "CASH_VOID trade fields must be empty", "symbol"})
		}
		tx.Symbol, tx.Quantity, tx.Price, tx.Fee = "", "", "", ""
		if tx.CorrectsSourceEventID == "" {
			errs = append(errs, APIError{"required", "corrects_source_event_id is required for CASH_VOID", "corrects_source_event_id"})
		}
		if err == nil && amount.Sign() == 0 {
			errs = append(errs, APIError{"invalid_amount", "CASH_VOID amount must be non-zero", "amount"})
		}
	case "FX_EXCHANGE":
		if tx.Symbol != "" || tx.Quantity != "" || tx.Price != "" || tx.Fee != "" {
			errs = append(errs, APIError{"invalid_fields", "FX_EXCHANGE trade fields must be empty", "symbol"})
		}
		tx.Symbol, tx.Quantity, tx.Price, tx.Fee = "", "", "", ""
		if err == nil && amount.Sign() >= 0 {
			errs = append(errs, APIError{"invalid_amount", "FX_EXCHANGE amount must be negative", "amount"})
		}
		if tx.CounterCurrency == "" {
			errs = append(errs, APIError{"required", "counter_currency is required for FX_EXCHANGE", "counter_currency"})
		} else if len(tx.CounterCurrency) != 3 || tx.CounterCurrency != strings.ToUpper(tx.CounterCurrency) || strings.IndexFunc(tx.CounterCurrency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0 {
			errs = append(errs, APIError{"invalid_currency", "counter_currency must be a three-letter uppercase code", "counter_currency"})
		} else if tx.CounterCurrency == tx.Currency {
			errs = append(errs, APIError{"invalid_currency", "FX_EXCHANGE currencies must be different", "counter_currency"})
		}
		if tx.CounterAmount == "" {
			errs = append(errs, APIError{"required", "counter_amount is required for FX_EXCHANGE", "counter_amount"})
		} else if counterAmount, counterErr := parseDecimal(tx.CounterAmount); counterErr != nil {
			errs = append(errs, APIError{"invalid_decimal", "counter_amount must be a canonical decimal", "counter_amount"})
		} else if counterAmount.Sign() <= 0 {
			errs = append(errs, APIError{"invalid_amount", "FX_EXCHANGE counter_amount must be positive", "counter_amount"})
		}
	case "BUY", "SELL":
		if tx.Symbol == "" {
			errs = append(errs, APIError{"required", "symbol is required for trades", "symbol"})
		} else {
			tx.InstrumentID = "instrument_" + strings.ToLower(tx.Symbol)
		}
		quantity, quantityErr := positiveDecimal(tx.Quantity, "quantity", &errs)
		price, priceErr := positiveDecimal(tx.Price, "price", &errs)
		fee, feeErr := nonNegativeDecimal(tx.Fee, "fee", &errs)
		if err == nil && quantityErr == nil && priceErr == nil && feeErr == nil {
			expected := new(big.Rat).Mul(quantity, price)
			expected.Add(expected, fee)
			if tx.Type == "BUY" {
				expected.Neg(expected)
			} else {
				expected.Sub(new(big.Rat).Mul(quantity, price), fee)
			}
			if expected.Cmp(amount) != 0 {
				errs = append(errs, APIError{"amount_mismatch", "amount must equal the signed trade cash impact including fee", "amount"})
			}
		}
	default:
		errs = append(errs, APIError{"invalid_type", "unsupported transaction type", "type"})
	}
	return tx, errs
}

func positiveDecimal(raw, field string, errs *[]APIError) (*big.Rat, error) {
	v, err := parseDecimal(raw)
	if err != nil {
		*errs = append(*errs, APIError{"invalid_decimal", field + " must be a canonical decimal", field})
	} else if v.Sign() <= 0 {
		err = errors.New("not positive")
		*errs = append(*errs, APIError{"invalid_decimal", field + " must be positive", field})
	}
	return v, err
}

func nonNegativeDecimal(raw, field string, errs *[]APIError) (*big.Rat, error) {
	v, err := parseDecimal(raw)
	if err != nil {
		*errs = append(*errs, APIError{"invalid_decimal", field + " must be a canonical decimal", field})
	} else if v.Sign() < 0 {
		err = errors.New("negative")
		*errs = append(*errs, APIError{"invalid_decimal", field + " must be non-negative", field})
	}
	return v, err
}

func parseDecimal(raw string) (*big.Rat, error) {
	if !decimalPattern.MatchString(raw) {
		return nil, errors.New("non-canonical decimal")
	}
	v, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, errors.New("invalid decimal")
	}
	return v, nil
}

func unresolved(tx *Transaction) *Resolution {
	if tx.AccountID != "account-main" {
		return &Resolution{
			Kind: "account", SourceField: "account_id", SourceValue: tx.AccountID,
			RequiredAction: "select_account", CandidateIDs: []string{"account-main"},
		}
	}
	if tx.Symbol != "" && tx.Symbol != "AAPL" {
		return &Resolution{
			Kind: "instrument", SourceField: "symbol", SourceValue: tx.Symbol,
			RequiredAction: "select_instrument", CandidateIDs: []string{"instrument_aapl"},
		}
	}
	return nil
}

func previewFingerprint(fileSHA, schemaVersion, mappingVersion, ledgerRevision string) string {
	binding, _ := json.Marshal(map[string]string{
		"file_sha256": fileSHA, "schema_version": schemaVersion,
		"mapping_version": mappingVersion, "ledger_revision": ledgerRevision,
	})
	hash := sha256.Sum256(binding)
	return hex.EncodeToString(hash[:])
}

func (s *Service) apply(ctx context.Context, req ApplyRequest) (*ApplyReceipt, *appError) {
	if req.PreviewID == "" || req.IdempotencyKey == "" {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "required", Message: "preview_id and idempotency_key are required"}}
	}
	received := s.now().UTC()
	requestBytes, _ := json.Marshal(req)
	requestHash := sha256.Sum256(requestBytes)
	requestSHA := hex.EncodeToString(requestHash[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, internalError(err)
	}
	defer tx.Rollback()
	var priorHash, priorJSON string
	err = tx.QueryRowContext(ctx, `SELECT request_sha256, receipt_json FROM receipts WHERE idempotency_key=?`, req.IdempotencyKey).Scan(&priorHash, &priorJSON)
	if err == nil {
		if priorHash != requestSHA {
			return nil, &appError{http.StatusConflict, APIError{Code: "idempotency_conflict", Message: "idempotency_key was already used with a different payload"}}
		}
		var receipt ApplyReceipt
		if err := json.Unmarshal([]byte(priorJSON), &receipt); err != nil {
			return nil, internalError(err)
		}
		return &receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, internalError(err)
	}
	var previewJSON, fileSHA, expiresAt string
	var previewRev int64
	var canApply int
	if err := tx.QueryRowContext(ctx, `SELECT preview_json,file_sha256,ledger_revision,can_apply,expires_at FROM previews WHERE preview_id=?`, req.PreviewID).Scan(&previewJSON, &fileSHA, &previewRev, &canApply, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &appError{http.StatusBadRequest, APIError{Code: "preview_not_found", Message: "preview_id does not exist"}}
		}
		return nil, internalError(err)
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !received.Before(expires) {
		return nil, &appError{http.StatusConflict, APIError{Code: "stale_preview", Message: "preview expired; create a new preview"}}
	}
	if canApply == 0 {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "preview_has_errors", Message: "preview contains invalid rows"}}
	}
	var currentRev int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM ledger_meta WHERE singleton=1`).Scan(&currentRev); err != nil {
		return nil, internalError(err)
	}
	if currentRev != previewRev {
		return nil, &appError{http.StatusConflict, APIError{Code: "stale_preview", Message: "ledger revision changed; create a new preview"}}
	}
	var preview ImportPreview
	if err := json.Unmarshal([]byte(previewJSON), &preview); err != nil {
		return nil, internalError(err)
	}
	expectedFingerprint := previewFingerprint(preview.FileSHA256, preview.SchemaVersion, preview.MappingVersion, preview.LedgerRevision)
	if preview.SchemaVersion != csvSchema || preview.MappingVersion != mappingSchema || preview.PreviewFingerprint != expectedFingerprint ||
		preview.FileSHA256 != fileSHA || preview.LedgerRevision != revision(previewRev) {
		return nil, &appError{http.StatusConflict, APIError{Code: "stale_preview", Message: "preview binding changed; create a new preview"}}
	}
	receiptID := s.id("receipt")
	recorded := s.now().UTC()
	applied := 0
	for _, row := range preview.Rows {
		if row.Status != "new" {
			continue
		}
		e := row.Transaction
		if e == nil {
			return nil, internalError(errors.New("preview new row has no transaction"))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,counter_currency,counter_amount,corrects_source_event_id,receipt_id,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.EventID, e.SourceEventID, e.AccountID, e.Type, e.OccurredAt, nullable(e.InstrumentID), nullable(e.Symbol), nullable(e.Quantity), nullable(e.Price), nullable(e.Fee), e.Currency, e.Amount, nullable(e.CounterCurrency), nullable(e.CounterAmount), nullable(e.CorrectsSourceEventID), receiptID, recorded.Format(time.RFC3339Nano)); err != nil {
			return nil, internalError(err)
		}
		applied++
	}
	after := currentRev + int64(applied)
	if _, err := tx.ExecContext(ctx, `UPDATE ledger_meta SET revision=?, recorded_at=?, last_verified_at=? WHERE singleton=1`, after, recorded.Format(time.RFC3339Nano), recorded.Format(time.RFC3339Nano)); err != nil {
		return nil, internalError(err)
	}
	if _, err := snapshotFrom(ctx, tx); err != nil {
		log.Printf("ledger invariant validation failed: %v", err)
		return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_ledger", Message: "ledger invariant validation failed"}}
	}
	receipt := &ApplyReceipt{
		ReceiptID: receiptID, PreviewID: req.PreviewID, IdempotencyKey: req.IdempotencyKey, FileSHA256: fileSHA,
		LedgerRevisionBefore: revision(currentRev), LedgerRevisionAfter: revision(after), AppliedRows: applied,
		SkippedDuplicateRows: preview.Totals.DuplicateRows, ReceivedAt: received.Format(time.RFC3339Nano), RecordedAt: recorded.Format(time.RFC3339Nano),
	}
	receiptBytes, _ := json.Marshal(receipt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO receipts(idempotency_key,request_sha256,receipt_id,receipt_json) VALUES(?,?,?,?)`, req.IdempotencyKey, requestSHA, receiptID, string(receiptBytes)); err != nil {
		return nil, internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return receipt, nil
}

type lot struct {
	quantity *big.Rat
	cost     *big.Rat
}

type position struct {
	instrument, symbol, currency string
	lots                         []lot
}

func snapshotFrom(ctx context.Context, q queryer) (*PortfolioSnapshot, error) {
	var rev int64
	var recorded string
	if err := q.QueryRowContext(ctx, `SELECT revision, recorded_at FROM ledger_meta WHERE singleton=1`).Scan(&rev, &recorded); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT event_id,type,occurred_at,COALESCE(instrument_id,''),COALESCE(symbol,''),COALESCE(quantity,''),COALESCE(price,''),COALESCE(fee,''),currency,amount,COALESCE(counter_currency,''),COALESCE(counter_amount,''),receipt_id FROM events ORDER BY occurred_at, sequence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cash := map[string]*big.Rat{}
	realized := map[string]*big.Rat{}
	positions := map[string]*position{}
	eventIDs := []string{}
	receiptIDs := []string{}
	seenReceipt := map[string]bool{}
	asOf := zeroTime
	for rows.Next() {
		var eventID, typ, occurredAt, instrument, symbol, quantityRaw, priceRaw, feeRaw, currency, amountRaw, counterCurrency, counterAmountRaw, receiptID string
		if err := rows.Scan(&eventID, &typ, &occurredAt, &instrument, &symbol, &quantityRaw, &priceRaw, &feeRaw, &currency, &amountRaw, &counterCurrency, &counterAmountRaw, &receiptID); err != nil {
			return nil, err
		}
		amount, err := parseDecimal(amountRaw)
		if err != nil {
			return nil, fmt.Errorf("event %s amount: %w", eventID, err)
		}
		addRat(cash, currency, amount)
		eventIDs = append(eventIDs, eventID)
		if !seenReceipt[receiptID] {
			seenReceipt[receiptID] = true
			receiptIDs = append(receiptIDs, receiptID)
		}
		if occurredAt > asOf {
			asOf = occurredAt
		}
		if typ == "FX_EXCHANGE" {
			counterAmount, err := parseDecimal(counterAmountRaw)
			if err != nil {
				return nil, fmt.Errorf("event %s counter amount: %w", eventID, err)
			}
			addRat(cash, counterCurrency, counterAmount)
			continue
		}
		switch typ {
		case "DEPOSIT", "WITHDRAWAL", "DIVIDEND", "FEE", "TAX", "CASH_VOID":
			continue
		}
		quantity, err := parseDecimal(quantityRaw)
		if err != nil {
			return nil, err
		}
		key := instrument + "\x00" + currency
		p := positions[key]
		if typ == "SPLIT" {
			if p == nil || len(p.lots) == 0 {
				return nil, fmt.Errorf("SPLIT %s has no open holding", eventID)
			}
			for i := range p.lots {
				p.lots[i].quantity.Mul(p.lots[i].quantity, quantity)
			}
			continue
		}
		price, err := parseDecimal(priceRaw)
		if err != nil {
			return nil, err
		}
		fee, err := parseDecimal(feeRaw)
		if err != nil {
			return nil, err
		}
		if p == nil {
			p = &position{instrument: instrument, symbol: symbol, currency: currency}
			positions[key] = p
		}
		if typ == "BUY" {
			cost := new(big.Rat).Add(new(big.Rat).Mul(quantity, price), fee)
			p.lots = append(p.lots, lot{new(big.Rat).Set(quantity), cost})
			continue
		}
		remaining := new(big.Rat).Set(quantity)
		allocatedCost := new(big.Rat)
		for remaining.Sign() > 0 && len(p.lots) > 0 {
			current := &p.lots[0]
			take := new(big.Rat).Set(remaining)
			if take.Cmp(current.quantity) > 0 {
				take.Set(current.quantity)
			}
			allocation := new(big.Rat).Mul(current.cost, new(big.Rat).Quo(take, current.quantity))
			allocatedCost.Add(allocatedCost, allocation)
			current.quantity.Sub(current.quantity, take)
			current.cost.Sub(current.cost, allocation)
			remaining.Sub(remaining, take)
			if current.quantity.Sign() == 0 {
				p.lots = p.lots[1:]
			}
		}
		if remaining.Sign() != 0 {
			return nil, fmt.Errorf("SELL %s exceeds available FIFO quantity", eventID)
		}
		addRat(realized, currency, new(big.Rat).Sub(amount, allocatedCost))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	snapshot := &PortfolioSnapshot{
		PortfolioID: "portfolio_main", LedgerRevision: revision(rev), AsOf: asOf, RecordedAt: recorded,
		LiveEnabled: false, ValuationStatus: "unavailable", Cash: []Money{}, Holdings: []Holding{}, RealizedPnL: []Money{},
		Provenance: Provenance{EventIDs: eventIDs, ReceiptIDs: receiptIDs},
	}
	for _, currency := range sortedKeys(cash) {
		amount, err := formatDecimal(cash[currency])
		if err != nil {
			return nil, err
		}
		snapshot.Cash = append(snapshot.Cash, Money{currency, amount})
	}
	for _, key := range sortedPositionKeys(positions) {
		p := positions[key]
		quantity, cost := new(big.Rat), new(big.Rat)
		for _, lot := range p.lots {
			quantity.Add(quantity, lot.quantity)
			cost.Add(cost, lot.cost)
		}
		if quantity.Sign() == 0 {
			continue
		}
		quantityString, err := formatDecimal(quantity)
		if err != nil {
			return nil, err
		}
		costString, err := formatDecimal(cost)
		if err != nil {
			return nil, err
		}
		snapshot.Holdings = append(snapshot.Holdings, Holding{p.instrument, p.symbol, quantityString, costString, p.currency})
	}
	for _, currency := range sortedKeys(realized) {
		amount, err := formatDecimal(realized[currency])
		if err != nil {
			return nil, err
		}
		snapshot.RealizedPnL = append(snapshot.RealizedPnL, Money{currency, amount})
	}
	return snapshot, nil
}

func addRat(values map[string]*big.Rat, key string, value *big.Rat) {
	if values[key] == nil {
		values[key] = new(big.Rat)
	}
	values[key].Add(values[key], value)
}

func formatDecimal(value *big.Rat) (string, error) {
	if value.Sign() == 0 {
		return "0", nil
	}
	den := new(big.Int).Set(value.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	twos, fives := 0, 0
	zero := new(big.Int)
	for new(big.Int).Mod(den, two).Cmp(zero) == 0 {
		den.Div(den, two)
		twos++
	}
	for new(big.Int).Mod(den, five).Cmp(zero) == 0 {
		den.Div(den, five)
		fives++
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		return "", fmt.Errorf("exact value %s has no finite decimal representation", value.RatString())
	}
	scale := max(twos, fives)
	formatted := value.FloatString(scale)
	if strings.Contains(formatted, ".") {
		formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	}
	return formatted, nil
}

func sortedKeys(values map[string]*big.Rat) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedPositionKeys(values map[string]*position) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := values[keys[i]], values[keys[j]]
		if a.instrument == b.instrument {
			return a.currency < b.currency
		}
		return a.instrument < b.instrument
	})
	return keys
}

func backupAndVerify(db *sql.DB, out, golden string) error {
	_, err := createBackup(db, out, golden, out+".manifest.json", time.Now, randomID)
	return err
}

func createBackup(db *sql.DB, out, golden, manifestPath string, now func() time.Time, id func(string) string) (_ *BackupManifest, resultErr error) {
	if _, err := os.Stat(out); err == nil {
		return nil, fmt.Errorf("backup target already exists: %s", out)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(manifestPath); err == nil {
		return nil, fmt.Errorf("manifest target already exists: %s", manifestPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	candidateCreated, manifestCreated := false, false
	defer func() {
		if resultErr == nil {
			return
		}
		var cleanupErr error
		if manifestCreated {
			if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
		if candidateCreated {
			if err := os.Remove(out); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		}
		if cleanupErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("discard failed backup candidate: %w", cleanupErr))
		}
	}()
	var sourceRevision int64
	if err := db.QueryRow(`SELECT revision FROM ledger_meta WHERE singleton=1`).Scan(&sourceRevision); err != nil {
		return nil, err
	}
	sourceOrders, err := proveOrderRecovery(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("source order recovery proof: %w", err)
	}
	sourceBroker, err := proveBrokerRecovery(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("source broker recovery proof: %w", err)
	}
	sourceStrategy, err := proveStrategyRegistryRecovery(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("source strategy registry recovery proof: %w", err)
	}
	sourceFX, err := proveFXObservationRecovery(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("source FX observation recovery proof: %w", err)
	}
	createdAt := now().UTC()
	quoted := strings.ReplaceAll(out, "'", "''")
	if _, err := db.Exec(`VACUUM INTO '` + quoted + `'`); err != nil {
		return nil, fmt.Errorf("consistent backup: %w", err)
	}
	candidateCreated = true
	candidateOrders, err := verifyRestoreProof(out, golden)
	if err != nil {
		return nil, err
	}
	candidateBroker, err := verifyBrokerRestoreProof(out)
	if err != nil {
		return nil, err
	}
	candidateStrategy, err := verifyStrategyRestoreProof(out)
	if err != nil {
		return nil, err
	}
	candidateFX, err := verifyFXObservationRestoreProof(out)
	if err != nil {
		return nil, err
	}
	if sourceOrders != candidateOrders {
		return nil, errors.New("backup order recovery proof does not match source")
	}
	if sourceBroker != candidateBroker {
		return nil, errors.New("backup broker recovery proof does not match source")
	}
	if !sameStrategyRegistryProof(sourceStrategy, candidateStrategy) {
		return nil, errors.New("backup strategy registry recovery proof does not match source")
	}
	if sourceFX != candidateFX {
		return nil, errors.New("backup FX observation recovery proof does not match source")
	}
	dbSHA, size, err := hashFile(out)
	if err != nil {
		return nil, err
	}
	snapshotSHA, _, err := hashFile(golden)
	if err != nil {
		return nil, err
	}
	verifiedAt := now().UTC()
	manifest := &BackupManifest{
		FormatVersion: backupFormat, SchemaVersion: backupSchema, CreatedAt: createdAt.Format(time.RFC3339Nano),
		SourceLedgerRevision: revision(sourceRevision), OrderStateSHA256: sourceOrders.SHA256, OrderCount: sourceOrders.Orders,
		OrderEventCount: sourceOrders.Events, ExecutionAuthoritySHA256: sourceOrders.ExecutionAuthoritySHA256,
		ExecutionAuthorityEventCount: sourceOrders.ExecutionAuthorityEvents, RiskReservationSHA256: sourceOrders.RiskReservationSHA256,
		RiskReservationCount: sourceOrders.RiskReservations, BrokerStateSHA256: sourceBroker.SHA256, BrokerSnapshotCount: sourceBroker.Snapshots,
		BrokerReconciliationCount: sourceBroker.Reconciliations,
		StrategyRegistrySHA256:    sourceStrategy.SHA256, StrategyEvidenceCount: sourceStrategy.Evidence,
		StrategySelectionEventCount: sourceStrategy.Events, SelectedStrategyResultSHA256: sourceStrategy.SelectedResultSHA256,
		FXObservationStateSHA256: sourceFX.SHA256, FXObservationCount: sourceFX.Observations,
		DBSHA256: dbSHA, SizeBytes: size, ExpectedSnapshotSHA256: snapshotSHA,
		Encryption: BackupEncryption{Encrypted: false, Algorithm: "none"},
		VerificationReceipt: VerificationReceipt{
			ReceiptID: id("backup_verification"), CandidateID: id("restore_candidate"), VerifiedAt: verifiedAt.Format(time.RFC3339Nano),
			Status: "verified", IntegrityCheck: "ok", GoldenSnapshotCheck: "ok", OrderStateCheck: "ok", BrokerStateCheck: "ok", StrategyRegistryCheck: "ok", FXObservationCheck: "ok", CandidateDBSHA256: dbSHA,
			CandidateSnapshotSHA256: snapshotSHA, CandidateOrderStateSHA256: candidateOrders.SHA256,
			CandidateBrokerStateSHA256: candidateBroker.SHA256, CandidateStrategyRegistrySHA256: candidateStrategy.SHA256,
			CandidateFXObservationStateSHA256: candidateFX.SHA256, EligibleForActivation: true, Errors: []string{},
		},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	manifestFile, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	manifestCreated = true
	if _, err := manifestFile.Write(encoded); err != nil {
		_ = manifestFile.Close()
		return nil, err
	}
	if err := manifestFile.Close(); err != nil {
		return nil, err
	}
	return manifest, nil
}

func verifyRestore(path, goldenPath string) error {
	if _, err := verifyRestoreProof(path, goldenPath); err != nil {
		return err
	}
	if _, err := verifyBrokerRestoreProof(path); err != nil {
		return err
	}
	if _, err := verifyStrategyRestoreProof(path); err != nil {
		return err
	}
	_, err := verifyFXObservationRestoreProof(path)
	return err
}

func verifyFXObservationRestoreProof(path string) (fxObservationRecoveryProof, error) {
	db, err := openExistingDB(path)
	if err != nil {
		return fxObservationRecoveryProof{}, err
	}
	defer db.Close()
	proof, err := proveFXObservationRecovery(context.Background(), db)
	if err != nil {
		return fxObservationRecoveryProof{}, fmt.Errorf("candidate FX observation recovery proof: %w", err)
	}
	return proof, nil
}

func verifyStrategyRestoreProof(path string) (strategyRegistryRecoveryProof, error) {
	db, err := openExistingDB(path)
	if err != nil {
		return strategyRegistryRecoveryProof{}, err
	}
	defer db.Close()
	proof, err := proveStrategyRegistryRecovery(context.Background(), db)
	if err != nil {
		return strategyRegistryRecoveryProof{}, fmt.Errorf("candidate strategy registry recovery proof: %w", err)
	}
	return proof, nil
}

func verifyBrokerRestoreProof(path string) (brokerRecoveryProof, error) {
	db, err := openExistingDB(path)
	if err != nil {
		return brokerRecoveryProof{}, err
	}
	defer db.Close()
	proof, err := proveBrokerRecovery(context.Background(), db)
	if err != nil {
		return brokerRecoveryProof{}, fmt.Errorf("candidate broker recovery proof: %w", err)
	}
	return proof, nil
}

func verifyRestoreProof(path, goldenPath string) (orderRecoveryProof, error) {
	db, err := openExistingDB(path)
	if err != nil {
		return orderRecoveryProof{}, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return orderRecoveryProof{}, err
	}
	if integrity != "ok" {
		return orderRecoveryProof{}, fmt.Errorf("integrity_check: %s", integrity)
	}
	if err := requireOrderRestoreSchema(db); err != nil {
		return orderRecoveryProof{}, err
	}
	actual, err := snapshotFrom(context.Background(), db)
	if err != nil {
		return orderRecoveryProof{}, err
	}
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		return orderRecoveryProof{}, err
	}
	var golden PortfolioSnapshot
	dec := json.NewDecoder(bytes.NewReader(goldenBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&golden); err != nil {
		return orderRecoveryProof{}, fmt.Errorf("golden snapshot: %w", err)
	}
	actualJSON, _ := json.Marshal(actual)
	goldenJSON, _ := json.Marshal(&golden)
	if !bytes.Equal(actualJSON, goldenJSON) {
		return orderRecoveryProof{}, errors.New("restored snapshot does not match golden")
	}
	proof, err := proveOrderRecovery(context.Background(), db)
	if err != nil {
		return orderRecoveryProof{}, fmt.Errorf("candidate order recovery proof: %w", err)
	}
	return proof, nil
}

func requireOrderRestoreSchema(db *sql.DB) error {
	if err := requireSchema(db); err != nil {
		return fmt.Errorf("restore schema: %w", err)
	}
	for _, table := range []string{"events", "order_idempotency", "order_events", "execution_authority_events", "risk_reservations", "broker_snapshots", "broker_snapshot_reconciliations", "strategy_research_evidence", "strategy_selection_events", "fx_observations"} {
		var strict int
		if err := db.QueryRow(`SELECT strict FROM pragma_table_list WHERE schema='main' AND type='table' AND name=?`, table).Scan(&strict); err != nil {
			return fmt.Errorf("restore order table %s: %w", table, err)
		}
		if strict != 1 {
			return fmt.Errorf("restore order table %s is not strict", table)
		}
	}
	var eventsDefinition string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='events'`).Scan(&eventsDefinition); err != nil {
		return fmt.Errorf("restore events definition: %w", err)
	}
	normalizedEvents := strings.ToLower(strings.Join(strings.Fields(eventsDefinition), " "))
	for _, required := range []string{
		"counter_currency text", "counter_amount text", "'fx_exchange'",
		"or (type = 'fx_exchange' and corrects_source_event_id is null",
		"amount <> '0' and amount <> '-0' and amount glob '-*'", "counter_currency <> currency",
		"counter_amount is not null and counter_amount <> '0' and counter_amount not glob '-*')",
	} {
		if !strings.Contains(normalizedEvents, required) {
			return errors.New("restore events lack the required FX exchange guard")
		}
	}
	for _, unique := range []struct {
		table   string
		columns []string
		origin  string
	}{
		{"order_idempotency", []string{"provider", "mode", "account_ref", "client_order_id"}, "pk"},
		{"order_idempotency", []string{"order_id"}, "u"},
		{"events", []string{"account_id", "source_event_id"}, "u"},
		{"events", []string{"account_id", "corrects_source_event_id"}, "u"},
		{"order_events", []string{"event_id"}, "u"},
		{"order_events", []string{"provider_execution_ref"}, "u"},
		{"execution_authority_events", []string{"event_id"}, "u"},
		{"execution_authority_events", []string{"account_ref", "fencing_token"}, "u"},
		{"risk_reservations", []string{"reservation_id"}, "u"},
		{"risk_reservations", []string{"order_id"}, "u"},
		{"risk_reservations", []string{"risk_event_id"}, "u"},
		{"risk_reservations", []string{"dispatch_event_id"}, "u"},
		{"broker_snapshots", []string{"snapshot_id"}, "u"},
		{"broker_snapshots", []string{"provider", "environment", "exchange", "account_ref", "fetched_at"}, "u"},
		{"broker_snapshot_reconciliations", []string{"reconciliation_id"}, "u"},
		{"broker_snapshot_reconciliations", []string{"snapshot_id", "ledger_account_id", "ledger_revision"}, "u"},
		{"strategy_research_evidence", []string{"result_sha256"}, "u"},
		{"strategy_selection_events", []string{"event_id"}, "u"},
		{"fx_observations", []string{"observation_id"}, "u"},
		{"fx_observations", []string{"source", "source_observation_id"}, "u"},
		{"fx_observations", []string{"source", "base_currency", "quote_currency", "observed_at"}, "u"},
	} {
		ok, err := hasUniqueIndex(db, unique.table, unique.columns, unique.origin)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("restore order table %s lacks required unique columns %s", unique.table, strings.Join(unique.columns, ","))
		}
	}
	var fxIndexDefinition string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='fx_observations_latest_idx' AND tbl_name='fx_observations'`).Scan(&fxIndexDefinition); err != nil {
		return fmt.Errorf("restore FX observation latest index: %w", err)
	}
	if strings.ToLower(strings.Join(strings.Fields(fxIndexDefinition), " ")) !=
		"create index fx_observations_latest_idx on fx_observations(source, base_currency, quote_currency, observed_at desc, sequence desc)" {
		return errors.New("restore FX observations lack the required latest index")
	}
	for _, table := range []string{"events", "order_events", "execution_authority_events", "risk_reservations", "broker_snapshots", "broker_snapshot_reconciliations", "strategy_research_evidence", "strategy_selection_events", "fx_observations"} {
		var sequenceType string
		var sequencePK, primaryKeyColumns, primaryKeyIndexes int
		if err := db.QueryRow(`SELECT type, pk FROM pragma_table_info(?) WHERE name='sequence'`, table).Scan(&sequenceType, &sequencePK); err != nil {
			return fmt.Errorf("restore %s sequence: %w", table, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE pk>0`, table).Scan(&primaryKeyColumns); err != nil {
			return fmt.Errorf("restore %s primary key: %w", table, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list(?) WHERE origin='pk'`, table).Scan(&primaryKeyIndexes); err != nil {
			return fmt.Errorf("restore %s primary-key index: %w", table, err)
		}
		if !strings.EqualFold(sequenceType, "INTEGER") || sequencePK != 1 || primaryKeyColumns != 1 || primaryKeyIndexes != 0 {
			return fmt.Errorf("restore %s sequence is not the integer primary key", table)
		}
	}
	var foreignKeys, matchingForeignKeys int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(("table"='order_idempotency' AND "from"='order_id' AND "to"='order_id') OR ("table"='risk_reservations' AND "from"='authority_reservation_id' AND "to"='reservation_id')), 0) FROM pragma_foreign_key_list(?)`, "order_events").Scan(&foreignKeys, &matchingForeignKeys); err != nil {
		return fmt.Errorf("restore order foreign key: %w", err)
	}
	if foreignKeys != 2 || matchingForeignKeys != 2 {
		return errors.New("restore order events lack required order or authority foreign keys")
	}
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(("table"='order_idempotency' AND "from"='order_id' AND "to"='order_id') OR ("table"='execution_authority_events' AND "from"='authority_event_id' AND "to"='event_id')), 0) FROM pragma_foreign_key_list(?)`, "risk_reservations").Scan(&foreignKeys, &matchingForeignKeys); err != nil {
		return fmt.Errorf("restore risk reservation foreign keys: %w", err)
	}
	if foreignKeys != 2 || matchingForeignKeys != 2 {
		return errors.New("restore risk reservations lack required order or authority foreign keys")
	}
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM("table"='broker_snapshots' AND "from"='snapshot_id' AND "to"='snapshot_id'), 0) FROM pragma_foreign_key_list(?)`, "broker_snapshot_reconciliations").Scan(&foreignKeys, &matchingForeignKeys); err != nil {
		return fmt.Errorf("restore broker reconciliation foreign key: %w", err)
	}
	if foreignKeys != 1 || matchingForeignKeys != 1 {
		return errors.New("restore broker reconciliations lack the required snapshot foreign key")
	}
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(("table"='strategy_research_evidence' AND "from"='candidate_result_sha256' AND "to"='result_sha256') OR ("table"='strategy_selection_events' AND "from"='source_event_id' AND "to"='event_id')), 0) FROM pragma_foreign_key_list(?)`, "strategy_selection_events").Scan(&foreignKeys, &matchingForeignKeys); err != nil {
		return fmt.Errorf("restore strategy selection foreign keys: %w", err)
	}
	if foreignKeys != 2 || matchingForeignKeys != 2 {
		return errors.New("restore strategy selection events lack required evidence or source foreign keys")
	}
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(("table"='events' AND "from"='account_id' AND "to"='account_id') OR ("table"='events' AND "from"='corrects_source_event_id' AND "to"='source_event_id')), 0) FROM pragma_foreign_key_list(?)`, "events").Scan(&foreignKeys, &matchingForeignKeys); err != nil {
		return fmt.Errorf("restore cash void foreign key: %w", err)
	}
	if foreignKeys != 2 || matchingForeignKeys != 2 {
		return errors.New("restore events lack the required cash-void foreign key")
	}
	foreignKeyRows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	if foreignKeyRows.Next() {
		foreignKeyRows.Close()
		return errors.New("restore candidate has broken foreign keys")
	}
	if err := foreignKeyRows.Err(); err != nil {
		foreignKeyRows.Close()
		return err
	}
	if err := foreignKeyRows.Close(); err != nil {
		return err
	}
	required := map[string]struct {
		table     string
		operation string
	}{
		"order_idempotency_no_update":               {"order_idempotency", "update"},
		"order_idempotency_no_delete":               {"order_idempotency", "delete"},
		"order_events_no_update":                    {"order_events", "update"},
		"order_events_no_delete":                    {"order_events", "delete"},
		"execution_authority_events_no_update":      {"execution_authority_events", "update"},
		"execution_authority_events_no_delete":      {"execution_authority_events", "delete"},
		"risk_reservations_no_update":               {"risk_reservations", "update"},
		"risk_reservations_no_delete":               {"risk_reservations", "delete"},
		"broker_snapshots_no_update":                {"broker_snapshots", "update"},
		"broker_snapshots_no_delete":                {"broker_snapshots", "delete"},
		"broker_snapshot_reconciliations_no_update": {"broker_snapshot_reconciliations", "update"},
		"broker_snapshot_reconciliations_no_delete": {"broker_snapshot_reconciliations", "delete"},
		"events_no_update":                          {"events", "update"},
		"events_no_delete":                          {"events", "delete"},
		"strategy_research_evidence_no_update":      {"strategy_research_evidence", "update"},
		"strategy_research_evidence_no_delete":      {"strategy_research_evidence", "delete"},
		"strategy_selection_events_no_update":       {"strategy_selection_events", "update"},
		"strategy_selection_events_no_delete":       {"strategy_selection_events", "delete"},
		"fx_observations_no_update":                 {"fx_observations", "update"},
		"fx_observations_no_delete":                 {"fx_observations", "delete"},
	}
	rows, err := db.Query(`SELECT name, tbl_name, sql FROM sqlite_master WHERE type='trigger'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, table, definition string
		if err := rows.Scan(&name, &table, &definition); err != nil {
			return err
		}
		expected, ok := required[name]
		normalized := strings.ToLower(strings.Join(strings.Fields(definition), " "))
		expectedSQL := fmt.Sprintf("create trigger %s before %s on %s begin select raise(abort, '%s is insert-only'); end",
			name, expected.operation, expected.table, expected.table)
		if ok && expected.table == table && normalized == expectedSQL {
			delete(required, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(required) != 0 {
		return errors.New("restore candidate is missing insert-only state triggers")
	}
	guardSQL := map[string]struct {
		table string
		sql   string
	}{
		"order_events_risk_reservation_guard":          {"order_events", "create trigger order_events_risk_reservation_guard before insert on order_events when new.event_type = 'risk_approved' begin select case when new.authority_reservation_id is null or not exists ( select 1 from risk_reservations where reservation_id = new.authority_reservation_id and order_id = new.order_id and risk_event_id = new.event_id and reservation_id = json_extract(new.event_json, '$.risk_reservation_id') and policy_version = json_extract(new.event_json, '$.risk_policy_version') and fencing_token = json_extract(new.event_json, '$.fencing_token') ) then raise(abort, 'risk approval requires an authority reservation') end; end"},
		"order_events_dispatch_reservation_guard":      {"order_events", "create trigger order_events_dispatch_reservation_guard before insert on order_events when new.event_type = 'submit_dispatched' begin select case when new.authority_reservation_id is null or not exists ( select 1 from risk_reservations where reservation_id = new.authority_reservation_id and order_id = new.order_id and dispatch_event_id = new.event_id and reservation_id = json_extract(new.event_json, '$.risk_reservation_id') and policy_version = json_extract(new.event_json, '$.risk_policy_version') and fencing_token = json_extract(new.event_json, '$.fencing_token') ) then raise(abort, 'submit dispatch requires an authority reservation') end; end"},
		"order_events_non_authority_reservation_guard": {"order_events", "create trigger order_events_non_authority_reservation_guard before insert on order_events when new.event_type not in ('risk_approved', 'submit_dispatched') and new.authority_reservation_id is not null begin select raise(abort, 'authority reservation is invalid for this event'); end"},
		"strategy_selection_events_state_guard":        {"strategy_selection_events", "create trigger strategy_selection_events_state_guard before insert on strategy_selection_events begin select case when new.expected_current_event_id != coalesce( (select event_id from strategy_selection_events order by sequence desc limit 1), 'no_event' ) then raise(abort, 'strategy selection expected current event is stale') end; select case when new.previous_selected_result_sha256 != coalesce( (select selected_result_sha256 from strategy_selection_events order by sequence desc limit 1), 'no_strategy' ) then raise(abort, 'strategy selection previous result is stale') end; select case when new.event_type = 'select' and ( new.selected_result_sha256 != new.candidate_result_sha256 or not exists ( select 1 from strategy_research_evidence where result_sha256 = new.candidate_result_sha256 and target = 'paper_candidate' ) ) then raise(abort, 'strategy selection requires paper_candidate evidence') end; select case when new.event_type = 'rollback' and new.source_event_id != coalesce( (select event_id from strategy_selection_events order by sequence desc limit 1), 'no_event' ) then raise(abort, 'strategy rollback source is stale') end; end"},
		"events_cash_void_guard":                       {"events", "create trigger events_cash_void_guard before insert on events when new.type = 'cash_void' begin select case when not exists ( select 1 from events target where target.account_id = new.account_id and target.source_event_id = new.corrects_source_event_id and target.type in ('deposit', 'withdrawal', 'dividend', 'fee', 'tax') and target.currency = new.currency and target.occurred_at <= new.occurred_at and new.amount = case when target.amount glob '-*' then substr(target.amount, 2) else '-' || target.amount end ) then raise(abort, 'cash void must exactly reverse an eligible event') end; end"},
	}
	for name, expected := range guardSQL {
		var definition string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=? AND tbl_name=?`, name, expected.table).Scan(&definition); err != nil {
			return fmt.Errorf("restore authority guard %s: %w", name, err)
		}
		normalized := strings.ToLower(strings.Join(strings.Fields(definition), " "))
		if normalized != expected.sql {
			return fmt.Errorf("restore authority guard %s does not match the required definition", name)
		}
	}
	return nil
}

func hasUniqueIndex(db *sql.DB, table string, expected []string, origin string) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_index_list(?) WHERE "unique"=1 AND partial=0 AND origin=?`, table, origin)
	if err != nil {
		return false, err
	}
	var indexes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return false, err
		}
		indexes = append(indexes, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, index := range indexes {
		columnRows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, index)
		if err != nil {
			return false, err
		}
		var columns []string
		for columnRows.Next() {
			var column sql.NullString
			if err := columnRows.Scan(&column); err != nil {
				columnRows.Close()
				return false, err
			}
			if column.Valid {
				columns = append(columns, column.String)
			}
		}
		if err := columnRows.Err(); err != nil {
			columnRows.Close()
			return false, err
		}
		if err := columnRows.Close(); err != nil {
			return false, err
		}
		if len(columns) == len(expected) {
			matches := true
			for i := range columns {
				matches = matches && columns[i] == expected[i]
			}
			if matches {
				return true, nil
			}
		}
	}
	return false, nil
}

func verifyManifest(path, goldenPath, manifestPath string) (resultErr error) {
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest BackupManifest
	dec := json.NewDecoder(bytes.NewReader(manifestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return fmt.Errorf("backup manifest: %w", err)
	}
	currentManifest := manifest.FormatVersion == backupFormat && manifest.SchemaVersion == backupSchema
	legacyManifest := manifest.FormatVersion == legacyBackupFormat &&
		(manifest.SchemaVersion == "omni-folio.sqlite.v8" || manifest.SchemaVersion == "omni-folio.sqlite.v9")
	if (!currentManifest && !legacyManifest) || manifest.Encryption.Encrypted || manifest.Encryption.Algorithm != "none" {
		return errors.New("unsupported backup manifest version or encryption")
	}
	if legacyManifest {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(manifestBytes, &fields); err != nil {
			return fmt.Errorf("backup manifest fields: %w", err)
		}
		if fields["fx_observation_state_sha256"] != nil || fields["fx_observation_count"] != nil {
			return errors.New("legacy backup manifest contains current FX observation fields")
		}
		var receiptFields map[string]json.RawMessage
		if err := json.Unmarshal(fields["verification_receipt"], &receiptFields); err != nil {
			return fmt.Errorf("backup verification receipt fields: %w", err)
		}
		if receiptFields["fx_observation_check"] != nil || receiptFields["candidate_fx_observation_state_sha256"] != nil {
			return errors.New("legacy backup receipt contains current FX observation fields")
		}
	}
	receipt := manifest.VerificationReceipt
	if receipt.Status != "verified" || receipt.IntegrityCheck != "ok" || receipt.GoldenSnapshotCheck != "ok" || receipt.OrderStateCheck != "ok" || receipt.BrokerStateCheck != "ok" || receipt.StrategyRegistryCheck != "ok" ||
		(currentManifest && receipt.FXObservationCheck != "ok") || !receipt.EligibleForActivation || len(receipt.Errors) != 0 {
		return errors.New("backup manifest is not eligible for activation")
	}
	dbSHA, size, err := hashFile(path)
	if err != nil {
		return err
	}
	snapshotSHA, _, err := hashFile(goldenPath)
	if err != nil {
		return err
	}
	if manifest.DBSHA256 != dbSHA || receipt.CandidateDBSHA256 != dbSHA || manifest.SizeBytes != size {
		return errors.New("backup database hash or size mismatch")
	}
	if manifest.ExpectedSnapshotSHA256 != snapshotSHA || receipt.CandidateSnapshotSHA256 != snapshotSHA {
		return errors.New("backup snapshot hash mismatch")
	}
	sourceDB, err := openExistingDB(path)
	if err != nil {
		return err
	}
	var firstSchema, sourceSchema, schemaCount int
	if err := sourceDB.QueryRow(`SELECT COALESCE(MIN(version), 0), COALESCE(MAX(version), 0), COUNT(*) FROM schema_migrations`).Scan(&firstSchema, &sourceSchema, &schemaCount); err != nil {
		_ = sourceDB.Close()
		return err
	}
	if err := sourceDB.Close(); err != nil {
		return err
	}
	declaredSchema := fmt.Sprintf("omni-folio.sqlite.v%d", sourceSchema)
	if firstSchema != 1 || schemaCount != sourceSchema || manifest.SchemaVersion != declaredSchema {
		return errors.New("backup manifest schema does not match candidate")
	}

	verificationPath := path
	temporaryDir := ""
	defer func() {
		if temporaryDir == "" {
			return
		}
		if err := os.RemoveAll(temporaryDir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove migrated restore candidate: %w", err))
		}
	}()
	if manifest.SchemaVersion != backupSchema {
		temporaryDir, err = os.MkdirTemp("", "omni-folio-restore-legacy.*")
		if err != nil {
			return err
		}
		verificationPath = filepath.Join(temporaryDir, "candidate.db")
		if err := copyFile(path, verificationPath); err != nil {
			return err
		}
		candidateDB, err := openExistingDB(verificationPath)
		if err != nil {
			return err
		}
		if err := migrate(candidateDB); err != nil {
			_ = candidateDB.Close()
			return fmt.Errorf("migrate restored legacy candidate: %w", err)
		}
		if err := candidateDB.Close(); err != nil {
			return err
		}
	}

	orders, err := verifyRestoreProof(verificationPath, goldenPath)
	if err != nil {
		return err
	}
	if manifest.OrderStateSHA256 != orders.SHA256 || receipt.CandidateOrderStateSHA256 != orders.SHA256 ||
		manifest.OrderCount != orders.Orders || manifest.OrderEventCount != orders.Events ||
		manifest.ExecutionAuthoritySHA256 != orders.ExecutionAuthoritySHA256 || manifest.ExecutionAuthorityEventCount != orders.ExecutionAuthorityEvents ||
		manifest.RiskReservationSHA256 != orders.RiskReservationSHA256 || manifest.RiskReservationCount != orders.RiskReservations {
		return errors.New("backup order recovery proof mismatch")
	}
	broker, err := verifyBrokerRestoreProof(verificationPath)
	if err != nil {
		return err
	}
	if manifest.BrokerStateSHA256 != broker.SHA256 || receipt.CandidateBrokerStateSHA256 != broker.SHA256 ||
		manifest.BrokerSnapshotCount != broker.Snapshots || manifest.BrokerReconciliationCount != broker.Reconciliations {
		return errors.New("backup broker recovery proof mismatch")
	}
	strategy, err := verifyStrategyRestoreProof(verificationPath)
	if err != nil {
		return err
	}
	if manifest.StrategyRegistrySHA256 != strategy.SHA256 || receipt.CandidateStrategyRegistrySHA256 != strategy.SHA256 ||
		manifest.StrategyEvidenceCount != strategy.Evidence || manifest.StrategySelectionEventCount != strategy.Events ||
		manifest.SelectedStrategyResultSHA256 != strategy.SelectedResultSHA256 {
		return errors.New("backup strategy registry recovery proof mismatch")
	}
	fx, err := verifyFXObservationRestoreProof(verificationPath)
	if err != nil {
		return err
	}
	if currentManifest {
		if manifest.FXObservationStateSHA256 != fx.SHA256 || receipt.CandidateFXObservationStateSHA256 != fx.SHA256 ||
			manifest.FXObservationCount != fx.Observations {
			return errors.New("backup FX observation recovery proof mismatch")
		}
	} else if fx.Observations != 0 || fx.SHA256 != hex.EncodeToString(sha256.New().Sum(nil)) {
		return errors.New("legacy backup unexpectedly contains FX observations")
	}
	db, err := openExistingDB(verificationPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var candidateRevision int64
	if err := db.QueryRow(`SELECT revision FROM ledger_meta WHERE singleton=1`).Scan(&candidateRevision); err != nil {
		return err
	}
	if manifest.SourceLedgerRevision != revision(candidateRevision) {
		return errors.New("backup ledger revision mismatch")
	}
	return nil
}

func copyFile(source, target string) (resultErr error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, in.Close()) }()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, out.Close()) }()
	_, err = io.Copy(out, in)
	return err
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err *appError) { writeJSON(w, err.status, err.body) }

func internalError(err error) *appError {
	log.Printf("internal error: %v", err)
	return &appError{http.StatusInternalServerError, APIError{Code: "internal_error", Message: "internal server error"}}
}

func readinessError(err error) *appError {
	log.Printf("readiness check failed: %v", err)
	return &appError{http.StatusServiceUnavailable, APIError{Code: "not_ready", Message: "service is not ready"}}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

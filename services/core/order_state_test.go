package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	k2aAccountRef = "kiwoom_account_AAAAAAAAAAAAAAAAAAAAAAAA"
	k2aOrderRef   = "kiwoom_order_BBBBBBBBBBBBBBBBBBBBBBBB"
)

func TestK2AIntentValidationAndClientOrderIdempotency(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	intent := k2aIntent("client-order-001")

	created, err := svc.recordOrderIntent(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "RECORDED" || created.OrderID == "" || created.ClientOrderID != intent.ClientOrderID {
		t.Fatalf("unexpected recorded order: %+v", created)
	}
	replayed, err := svc.recordOrderIntent(ctx, intent)
	if err != nil || !reflect.DeepEqual(created, replayed) {
		t.Fatalf("same client order did not replay: first=%+v replay=%+v err=%v", created, replayed, err)
	}

	conflict := intent
	conflict.LimitPrice = "1001"
	if _, err := svc.recordOrderIntent(ctx, conflict); err == nil {
		t.Fatal("same client_order_id with a different intent was accepted")
	}
	sell := k2aIntent("client-order-sell")
	sell.Side = "SELL"
	sell.LimitPrice = "1000.5"
	if state, err := svc.recordOrderIntent(ctx, sell); err != nil || state.Status != "RECORDED" {
		t.Fatalf("valid exact-decimal sell intent was rejected: state=%+v err=%v", state, err)
	}
	legacyPaper := k2aIntent("legacy-paper-signal")
	legacyPaper.Mode = "paper"
	legacyPaper.StrategyResultSHA256, legacyPaper.StrategySelectionEventID = strings.Repeat("a", 64), "legacy-selection"
	legacyPaper.SignalSchemaVersion, legacyPaper.SignalID = legacyPaperSignalSchema, "legacy-signal"
	legacyPaper.SignalDataSHA256 = strings.Repeat("b", 64)
	legacyPaper.SignalDataAsOf, legacyPaper.SignalGeneratedAt, legacyPaper.SignalExpiresAt = "2026-01-10T14:59:00Z", "2026-01-10T14:59:01Z", "2026-01-10T15:01:00Z"
	if err := validateOrderIntent(legacyPaper); err != nil {
		t.Fatalf("legacy paper-signal.v1 recovery contract broke: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*OrderIntent)
	}{
		{"empty client order ID", func(v *OrderIntent) { v.ClientOrderID = "" }},
		{"spaced client order ID", func(v *OrderIntent) { v.ClientOrderID = "bad id" }},
		{"raw account reference", func(v *OrderIntent) { v.AccountRef = "9876543210" }},
		{"wrong provider", func(v *OrderIntent) { v.Provider = "other" }},
		{"non synthetic mode", func(v *OrderIntent) { v.Mode = "mock" }},
		{"paper without strategy signal", func(v *OrderIntent) { v.Mode = "paper" }},
		{"non KRX exchange", func(v *OrderIntent) { v.Exchange = "NXT" }},
		{"invalid symbol", func(v *OrderIntent) { v.Symbol = "A005930" }},
		{"invalid side", func(v *OrderIntent) { v.Side = "SHORT" }},
		{"market order", func(v *OrderIntent) { v.OrderType = "MARKET" }},
		{"fractional quantity", func(v *OrderIntent) { v.Quantity = "1.5" }},
		{"leading-zero quantity", func(v *OrderIntent) { v.Quantity = "01" }},
		{"zero quantity", func(v *OrderIntent) { v.Quantity = "0" }},
		{"exponent price", func(v *OrderIntent) { v.LimitPrice = "1e3" }},
		{"trailing-zero price", func(v *OrderIntent) { v.LimitPrice = "1000.0" }},
		{"zero price", func(v *OrderIntent) { v.LimitPrice = "0" }},
		{"wrong currency", func(v *OrderIntent) { v.Currency = "USD" }},
		{"partial strategy binding", func(v *OrderIntent) { v.StrategyResultSHA256 = strings.Repeat("a", 64) }},
		{"malformed strategy binding", func(v *OrderIntent) {
			v.StrategyResultSHA256, v.StrategySelectionEventID = "not-a-sha", "bad event"
		}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := k2aIntent("invalid-client-" + string(rune('a'+i)))
			test.mutate(&candidate)
			if _, err := svc.recordOrderIntent(ctx, candidate); err == nil {
				t.Fatalf("invalid intent was accepted: %+v", candidate)
			}
		})
	}
}

func TestK2ARiskGateAndExplicitUnknownRecovery(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	ctx := context.Background()

	recorded := mustRecordK2AOrder(t, svc, "client-risk-gate")
	if _, err := svc.appendOrderEvent(ctx, k2aEvent("submit-too-early", recorded.OrderID, "SUBMIT_DISPATCHED")); err == nil {
		t.Fatal("submit was dispatched before risk approval")
	}
	rejected, err := svc.appendOrderEvent(ctx, k2aEvent("risk-rejected", recorded.OrderID, "RISK_REJECTED"))
	if err != nil || rejected.Status != "RISK_REJECTED" {
		t.Fatalf("risk rejection was not terminal: state=%+v err=%v", rejected, err)
	}
	if _, err := svc.appendOrderEvent(ctx, k2aEvent("submit-after-risk-reject", recorded.OrderID, "SUBMIT_DISPATCHED")); err == nil {
		t.Fatal("risk-rejected order was dispatched")
	}

	openBeforeUnknown := mustReadyAndDispatchK2AOrder(t, svc, "client-cancel-blocked", "cancel-blocked")
	openAck := k2aEvent("cancel-blocked-submit-ack", openBeforeUnknown.OrderID, "SUBMIT_ACKNOWLEDGED")
	openAck.ProviderOrderRef = "kiwoom_order_ZZZZZZZZZZZZZZZZZZZZZZZZ"
	mustAppendK2AEvent(t, svc, openAck, "OPEN")

	unknown := mustReadyAndDispatchK2AOrder(t, svc, "client-submit-unknown", "unknown")
	if unknown.Status != "SUBMIT_UNKNOWN" {
		t.Fatalf("dispatched submit was not conservatively unknown: %+v", unknown)
	}
	if _, err := svc.appendOrderEvent(ctx, k2aEvent("blind-resubmit", unknown.OrderID, "SUBMIT_DISPATCHED")); err == nil {
		t.Fatal("unknown submit was re-dispatched")
	}

	blocked := mustRecordK2AOrder(t, svc, "client-account-blocked")
	if _, err := svc.appendOrderEvent(ctx, k2aEvent("blocked-risk-approved", blocked.OrderID, "RISK_APPROVED")); err == nil {
		t.Fatal("direct risk approval bypassed execution authority")
	}
	if _, err := svc.appendOrderEvent(ctx, k2aEvent("blocked-submit", blocked.OrderID, "SUBMIT_DISPATCHED")); err == nil {
		t.Fatal("account-wide dispatch block did not hold while another submit was unknown")
	}
	mustAppendK2AEvent(t, svc, k2aEvent("risk-reducing-cancel", openBeforeUnknown.OrderID, "CANCEL_DISPATCHED"), "CANCEL_UNKNOWN")
	mustAppendK2AEvent(t, svc, k2aEvent("risk-reducing-cancel-ack", openBeforeUnknown.OrderID, "CANCEL_ACKNOWLEDGED"), "CANCELED")

	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openExistingDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := newService(reopened, time.Now, randomID)
	reloaded, err := restarted.loadOrderState(ctx, unknown.OrderID)
	if err != nil || reloaded.Status != "SUBMIT_UNKNOWN" {
		t.Fatalf("restart lost unknown submit: state=%+v err=%v", reloaded, err)
	}

	unsupported := k2aEvent("unsafe-not-found", unknown.OrderID, "PROVIDER_NOT_FOUND")
	if _, err := restarted.appendOrderEvent(ctx, unsupported); err == nil {
		t.Fatal("provider absence heuristically resolved an unknown submit")
	}
	ack := k2aEvent("submit-ack", unknown.OrderID, "SUBMIT_ACKNOWLEDGED")
	ack.Source = "reconciliation"
	ack.ProviderOrderRef = k2aOrderRef
	mustAppendK2AEvent(t, restarted, ack, "OPEN")

	fillRecovered := mustReadyAndDispatchK2AOrder(t, restarted, "client-fill-recovery", "fill-recovery")
	fill := k2aFill("fill-only-recovery", fillRecovered.OrderID, "kiwoom_execution_CCCCCCCCCCCCCCCCCCCCCCCC", "2", "1000")
	fill.Source = "reconciliation"
	fill.ProviderOrderRef = "kiwoom_order_DDDDDDDDDDDDDDDDDDDDDDDD"
	mustAppendK2AEvent(t, restarted, fill, "PARTIALLY_FILLED")

	rejectRecovered := mustReadyAndDispatchK2AOrder(t, restarted, "client-reject-recovery", "reject-recovery")
	reject := k2aEvent("explicit-submit-reject", rejectRecovered.OrderID, "SUBMIT_REJECTED")
	reject.Source = "reconciliation"
	mustAppendK2AEvent(t, restarted, reject, "REJECTED")
}

func TestK2APartialFillCancelRaceLateFillAndOverfill(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	state := mustReadyAndDispatchK2AOrder(t, svc, "client-cancel-race", "cancel-race")
	ack := k2aEvent("cancel-race-ack", state.OrderID, "SUBMIT_ACKNOWLEDGED")
	ack.ProviderOrderRef = k2aOrderRef
	mustAppendK2AEvent(t, svc, ack, "OPEN")

	partial := mustAppendK2AEvent(t, svc, k2aFill("fill-3", state.OrderID, "kiwoom_execution_EEEEEEEEEEEEEEEEEEEEEEEE", "3", "1000"), "PARTIALLY_FILLED")
	if partial.FilledQuantity != "3" {
		t.Fatalf("partial fill quantity=%q", partial.FilledQuantity)
	}
	mustAppendK2AEvent(t, svc, k2aEvent("cancel-dispatched", state.OrderID, "CANCEL_DISPATCHED"), "CANCEL_UNKNOWN")
	duringCancel := mustAppendK2AEvent(t, svc, k2aFill("fill-2", state.OrderID, "kiwoom_execution_FFFFFFFFFFFFFFFFFFFFFFFF", "2", "1001"), "CANCEL_UNKNOWN")
	if duringCancel.FilledQuantity != "5" {
		t.Fatalf("fill during cancel was lost: %+v", duringCancel)
	}
	canceled := mustAppendK2AEvent(t, svc, k2aEvent("cancel-ack", state.OrderID, "CANCEL_ACKNOWLEDGED"), "CANCELED")
	if canceled.FilledQuantity != "5" {
		t.Fatalf("cancel erased prior fills: %+v", canceled)
	}
	late := mustAppendK2AEvent(t, svc, k2aFill("late-fill-1", state.OrderID, "kiwoom_execution_GGGGGGGGGGGGGGGGGGGGGGGG", "1", "1002"), "CANCELED")
	if late.FilledQuantity != "6" {
		t.Fatalf("late fill after cancel was lost: %+v", late)
	}
	filled := mustAppendK2AEvent(t, svc, k2aFill("late-fill-4", state.OrderID, "kiwoom_execution_HHHHHHHHHHHHHHHHHHHHHHHH", "4", "1003"), "FILLED")
	if filled.FilledQuantity != "10" {
		t.Fatalf("full late fill quantity=%q", filled.FilledQuantity)
	}
	if _, err := svc.appendOrderEvent(ctx, k2aFill("overfill", state.OrderID, "kiwoom_execution_IIIIIIIIIIIIIIIIIIIIIIII", "1", "1004")); err == nil {
		t.Fatal("overfill was accepted")
	}
	after, err := svc.loadOrderState(ctx, state.OrderID)
	if err != nil || after.Status != "FILLED" || after.FilledQuantity != "10" {
		t.Fatalf("overfill mutated state: state=%+v err=%v", after, err)
	}

	rejectState := mustReadyAndDispatchK2AOrder(t, svc, "client-cancel-reject", "cancel-reject")
	rejectAck := k2aEvent("cancel-reject-submit-ack", rejectState.OrderID, "SUBMIT_ACKNOWLEDGED")
	rejectAck.ProviderOrderRef = "kiwoom_order_JJJJJJJJJJJJJJJJJJJJJJJJ"
	mustAppendK2AEvent(t, svc, rejectAck, "OPEN")
	rejectFill := k2aFill("cancel-reject-fill", rejectState.OrderID, "kiwoom_execution_KKKKKKKKKKKKKKKKKKKKKKKK", "2", "999")
	rejectFill.ProviderOrderRef = rejectAck.ProviderOrderRef
	mustAppendK2AEvent(t, svc, rejectFill, "PARTIALLY_FILLED")
	mustAppendK2AEvent(t, svc, k2aEvent("cancel-reject-dispatch", rejectState.OrderID, "CANCEL_DISPATCHED"), "CANCEL_UNKNOWN")
	restored := mustAppendK2AEvent(t, svc, k2aEvent("cancel-rejected", rejectState.OrderID, "CANCEL_REJECTED"), "PARTIALLY_FILLED")
	if restored.FilledQuantity != "2" {
		t.Fatalf("cancel rejection lost fill state: %+v", restored)
	}
	unfilled := mustReadyAndDispatchK2AOrder(t, svc, "client-unfilled-cancel-reject", "unfilled-cancel-reject")
	unfilledAck := k2aEvent("unfilled-cancel-reject-ack", unfilled.OrderID, "SUBMIT_ACKNOWLEDGED")
	unfilledAck.ProviderOrderRef = "kiwoom_order_QQQQQQQQQQQQQQQQQQQQQQQQ"
	mustAppendK2AEvent(t, svc, unfilledAck, "OPEN")
	mustAppendK2AEvent(t, svc, k2aEvent("unfilled-cancel-dispatch", unfilled.OrderID, "CANCEL_DISPATCHED"), "CANCEL_UNKNOWN")
	mustAppendK2AEvent(t, svc, k2aEvent("unfilled-cancel-rejected", unfilled.OrderID, "CANCEL_REJECTED"), "OPEN")
}

func TestK2AEventAndProviderExecutionIdempotencyConflicts(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	state := mustReadyAndDispatchK2AOrder(t, svc, "client-event-idempotency", "event-idempotency")

	rawAck := k2aEvent("raw-provider-order", state.OrderID, "SUBMIT_ACKNOWLEDGED")
	rawAck.ProviderOrderRef = "42"
	if _, err := svc.appendOrderEvent(ctx, rawAck); err == nil {
		t.Fatal("raw provider order ID was accepted")
	}
	invalidSource := k2aEvent("invalid-source", state.OrderID, "SUBMIT_ACKNOWLEDGED")
	invalidSource.Source = "provider"
	invalidSource.ProviderOrderRef = k2aOrderRef
	if _, err := svc.appendOrderEvent(ctx, invalidSource); err == nil {
		t.Fatal("unsupported event provenance was accepted")
	}
	ack := k2aEvent("provider-order-ack", state.OrderID, "SUBMIT_ACKNOWLEDGED")
	ack.ProviderOrderRef = k2aOrderRef
	mustAppendK2AEvent(t, svc, ack, "OPEN")
	wrongOrderRef := k2aFill("changed-provider-order", state.OrderID, "kiwoom_execution_RRRRRRRRRRRRRRRRRRRRRRRR", "1", "1000")
	wrongOrderRef.ProviderOrderRef = "kiwoom_order_SSSSSSSSSSSSSSSSSSSSSSSS"
	if _, err := svc.appendOrderEvent(ctx, wrongOrderRef); err == nil {
		t.Fatal("a fill changed the order's provider reference")
	}
	conflictingOrder := mustReadyAndDispatchK2AOrder(t, svc, "client-provider-ref-conflict", "provider-ref-conflict")
	conflictingAck := k2aEvent("provider-ref-conflict-ack", conflictingOrder.OrderID, "SUBMIT_ACKNOWLEDGED")
	conflictingAck.ProviderOrderRef = k2aOrderRef
	if _, err := svc.appendOrderEvent(ctx, conflictingAck); err == nil {
		t.Fatal("provider order reference was assigned to two orders")
	}
	invalidFills := []OrderEvent{
		k2aFill("raw-execution", state.OrderID, "99", "1", "1000"),
		k2aFill("zero-fill", state.OrderID, "kiwoom_execution_MMMMMMMMMMMMMMMMMMMMMMMM", "0", "1000"),
		k2aFill("fractional-fill", state.OrderID, "kiwoom_execution_NNNNNNNNNNNNNNNNNNNNNNNN", "1.0", "1000"),
		k2aFill("exponent-fill-price", state.OrderID, "kiwoom_execution_OOOOOOOOOOOOOOOOOOOOOOOO", "1", "1e3"),
		k2aFill("trailing-zero-fill-price", state.OrderID, "kiwoom_execution_PPPPPPPPPPPPPPPPPPPPPPPP", "1", "1000.0"),
	}
	for _, invalid := range invalidFills {
		if _, err := svc.appendOrderEvent(ctx, invalid); err == nil {
			t.Fatalf("invalid fill was accepted: %+v", invalid)
		}
	}

	fill := k2aFill("idempotent-fill", state.OrderID, "kiwoom_execution_LLLLLLLLLLLLLLLLLLLLLLLL", "2", "1000")
	first := mustAppendK2AEvent(t, svc, fill, "PARTIALLY_FILLED")
	replayed := mustAppendK2AEvent(t, svc, fill, "PARTIALLY_FILLED")
	if !reflect.DeepEqual(first, replayed) {
		t.Fatalf("same event replay changed state: first=%+v replay=%+v", first, replayed)
	}
	conflictingEvent := fill
	conflictingEvent.Quantity = "3"
	if _, err := svc.appendOrderEvent(ctx, conflictingEvent); err == nil {
		t.Fatal("same event_id with a different payload was accepted")
	}

	sameExecution := fill
	sameExecution.EventID = "same-execution-new-event"
	mustAppendK2AEvent(t, svc, sameExecution, "PARTIALLY_FILLED")
	conflictingExecution := sameExecution
	conflictingExecution.EventID = "same-execution-conflict"
	conflictingExecution.Price = "1001"
	if _, err := svc.appendOrderEvent(ctx, conflictingExecution); err == nil {
		t.Fatal("same provider_execution_ref with a different payload was accepted")
	}
	finalState, err := svc.loadOrderState(ctx, state.OrderID)
	if err != nil || finalState.FilledQuantity != "2" {
		t.Fatalf("idempotency conflict mutated fills: state=%+v err=%v", finalState, err)
	}
}

func TestK2AOrderTablesAreInsertOnly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	state := mustRecordK2AOrder(t, svc, "client-insert-only")

	for _, statement := range []string{
		`UPDATE order_events SET order_id=order_id WHERE order_id='` + state.OrderID + `'`,
		`DELETE FROM order_events WHERE order_id='` + state.OrderID + `'`,
		`UPDATE order_idempotency SET order_id=order_id WHERE order_id='` + state.OrderID + `'`,
		`DELETE FROM order_idempotency WHERE order_id='` + state.OrderID + `'`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("insert-only order storage accepted mutation: %s", statement)
		}
	}
	loaded, err := svc.loadOrderState(context.Background(), state.OrderID)
	if err != nil || loaded.Status != "RECORDED" {
		t.Fatalf("trigger test damaged order: state=%+v err=%v", loaded, err)
	}
}

func TestSchemaMigratesV1ToV7AndReadinessRequiresV7(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	script, err := migrationFiles.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)`, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO events(sequence,event_id,source_event_id,account_id,type,occurred_at,currency,amount,receipt_id,recorded_at) VALUES(1,'preserved','preserved','account-main','DEPOSIT','2026-01-01T00:00:00Z','KRW','1','receipt','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var version, migrations, preserved int
	if err := db.QueryRow(`SELECT MAX(version), COUNT(*) FROM schema_migrations`).Scan(&version, &migrations); err != nil {
		t.Fatal(err)
	}
	if version != 7 || migrations != 7 {
		t.Fatalf("schema version=(%d,%d), want latest=7 with seven migrations", version, migrations)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_id='preserved'`).Scan(&preserved); err != nil || preserved != 1 {
		t.Fatalf("v1 data was not preserved: count=%d err=%v", preserved, err)
	}
	for _, table := range []string{"order_idempotency", "order_events", "execution_authority_events", "risk_reservations", "broker_snapshots", "broker_snapshot_reconciliations", "strategy_research_evidence", "strategy_selection_events"} {
		var exists int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil || exists != 1 {
			t.Fatalf("migration did not create %s: exists=%d err=%v", table, exists, err)
		}
	}
	if err := migrate(db); err != nil {
		t.Fatal("repeated migration was not idempotent:", err)
	}

	svc := newService(db, time.Now, randomID)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("v7 schema was not ready: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPaperMigrationRejectsForeignKeyDamageAtomically(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "damaged-v6.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	files := []string{"001_init.sql", "002_orders.sql", "003_broker_snapshots.sql", "004_execution_authority.sql", "005_ledger_events.sql", "006_strategy_registry.sql"}
	for version, name := range files {
		script, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version+1, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO order_events(sequence,event_id,event_sha256,order_id,event_type,source,event_json,recorded_at) VALUES(1,'orphan-event','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','missing-order','INTENT_RECORDED','synthetic','{}','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(db); err == nil || !strings.Contains(err.Error(), "foreign-key check failed") {
		t.Fatalf("damaged v6 migration was accepted: %v", err)
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 6 {
		t.Fatalf("failed migration was not rolled back: version=%d err=%v", version, err)
	}
}

func k2aIntent(clientOrderID string) OrderIntent {
	return OrderIntent{
		ClientOrderID: clientOrderID,
		Provider:      "kiwoom",
		Mode:          "synthetic",
		AccountRef:    k2aAccountRef,
		Symbol:        "005930",
		Exchange:      "KRX",
		Side:          "BUY",
		OrderType:     "LIMIT",
		Quantity:      "10",
		LimitPrice:    "1000",
		Currency:      "KRW",
	}
}

func k2aEvent(eventID, orderID, eventType string) OrderEvent {
	return OrderEvent{EventID: eventID, OrderID: orderID, Type: eventType, Source: "synthetic"}
}

func k2aFill(eventID, orderID, executionRef, quantity, price string) OrderEvent {
	event := k2aEvent(eventID, orderID, "FILL_RECORDED")
	event.ProviderOrderRef = k2aOrderRef
	event.ProviderExecutionRef = executionRef
	event.Quantity = quantity
	event.Price = price
	event.OccurredAt = "2026-01-10T15:01:00Z"
	return event
}

func mustRecordK2AOrder(t *testing.T, svc *Service, clientOrderID string) *OrderState {
	t.Helper()
	state, err := svc.recordOrderIntent(context.Background(), k2aIntent(clientOrderID))
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func mustReadyAndDispatchK2AOrder(t *testing.T, svc *Service, clientOrderID, eventPrefix string) *OrderState {
	t.Helper()
	_ = eventPrefix
	state := mustRecordK2AOrder(t, svc, clientOrderID)
	lease := mustK2CLease(t, svc, state.AccountRef)
	return mustAuthorizeK2C(t, svc, state.OrderID, lease.FencingToken)
}

func mustAppendK2AEvent(t *testing.T, svc *Service, event OrderEvent, status string) *OrderState {
	t.Helper()
	state, err := svc.appendOrderEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != status {
		t.Fatalf("event %s produced status %s, want %s: %+v", event.Type, state.Status, status, state)
	}
	return state
}

func TestK2AReferenceFixturesRemainOpaque(t *testing.T) {
	for _, ref := range []string{k2aAccountRef, k2aOrderRef, "kiwoom_execution_CCCCCCCCCCCCCCCCCCCCCCCC"} {
		parts := strings.Split(ref, "_")
		if len(parts) != 3 || len(parts[2]) != 24 {
			t.Fatalf("test fixture is not shaped like an opaque Kiwoom alias: %q", ref)
		}
	}
}

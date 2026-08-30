package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestG38C2PaperIntentBinding(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	signal.TargetQuantity = "10"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	event, state, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil || event == nil || state == nil || state.Status != "OPEN" {
		t.Fatalf("capitalized order admission event=%+v state=%+v err=%v", event, state, err)
	}
	intent, err := loadOrderIntentFrom(context.Background(), svc.db, state.OrderID)
	if err != nil || intent.OrderType != "PAPER_MARKET" || intent.LimitPrice != "" || intent.Side != "BUY" || intent.Quantity != "10" ||
		intent.PaperAccountingSessionID != event.PaperAccountingSessionID || intent.PaperAccountingPolicyVersion != paperAccountingPolicyVersion ||
		intent.PaperSignalEventID != event.EventID || intent.ExecutionPolicySHA256 != event.ExecutionPolicySHA256 {
		t.Fatalf("capitalized intent=%+v event=%+v err=%v", intent, event, err)
	}
	var reservations, fills int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM risk_reservations`).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE event_type='FILL_RECORDED'`).Scan(&fills); err != nil {
		t.Fatal(err)
	}
	if reservations != 0 || fills != 0 {
		t.Fatalf("paper admission reused synthetic accounting: reservations=%d fills=%d", reservations, fills)
	}

	for _, test := range []struct {
		name   string
		mutate func(*OrderIntent)
	}{
		{name: "missing schema", mutate: func(intent *OrderIntent) { intent.SignalSchemaVersion = "" }},
		{name: "unknown schema", mutate: func(intent *OrderIntent) { intent.SignalSchemaVersion = "paper-signal.v4" }},
		{name: "session", mutate: func(intent *OrderIntent) { intent.PaperAccountingSessionID = "paper_accounting_session_wrong" }},
		{name: "signal", mutate: func(intent *OrderIntent) { intent.PaperSignalEventID = "paper_signal_event_wrong" }},
		{name: "account", mutate: func(intent *OrderIntent) { intent.AccountRef = "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB" }},
		{name: "symbol", mutate: func(intent *OrderIntent) { intent.Symbol = "000660" }},
		{name: "target", mutate: func(intent *OrderIntent) { intent.SignalTargetQuantity = "11" }},
		{name: "selection", mutate: func(intent *OrderIntent) { intent.StrategySelectionEventID = "selection_wrong" }},
		{name: "side", mutate: func(intent *OrderIntent) { intent.Side = "SELL" }},
		{name: "quantity", mutate: func(intent *OrderIntent) { intent.Quantity = "9" }},
		{name: "policy", mutate: func(intent *OrderIntent) { intent.ExecutionPolicySHA256 = strings.Repeat("0", 64) }},
	} {
		t.Run("direct "+test.name, func(t *testing.T) {
			candidateSvc, candidateSignal, _ := g38c2PaperSignalFixture(t, true)
			candidateSignal.TargetQuantity = "10"
			signalEvent, err := recordG38C2PaperSignalForTest(candidateSvc, k2aAccountRef, candidateSignal)
			if err != nil {
				t.Fatal(err)
			}
			candidate := capitalizedPaperOrderIntent(*signalEvent, "BUY", "10")
			candidate.ClientOrderID = "paper_direct_" + strings.ReplaceAll(test.name, " ", "_")
			test.mutate(&candidate)
			if err := insertPaperOrderIntentDirectForTest(candidateSvc, candidate, "order_direct_"+test.name); err == nil {
				t.Fatal("direct capitalized intent mismatch was accepted")
			}
			var count int
			if err := candidateSvc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected direct intent rows=%d err=%v", count, err)
			}
		})
	}

	for _, test := range []struct {
		name, target string
		quantity     any
	}{
		{name: "leading zero quantity", target: "10", quantity: "010"},
		{name: "suffix junk quantity", target: "10", quantity: "10x"},
		{name: "numeric JSON quantity", target: "10", quantity: float64(10)},
		{name: "oversized quantity", target: paperMaxQuantity, quantity: "4611686018427387904"},
		{name: "overflow numeric quantity", target: paperMaxQuantity, quantity: 1e40},
	} {
		t.Run("direct "+test.name, func(t *testing.T) {
			svc, signal, _ := g38c2PaperSignalFixture(t, true)
			signal.TargetQuantity = test.target
			signalEvent, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, signal)
			if err != nil {
				t.Fatal(err)
			}
			candidate := capitalizedPaperOrderIntent(*signalEvent, "BUY", test.target)
			var raw map[string]any
			encoded, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &raw); err != nil {
				t.Fatal(err)
			}
			raw["quantity"] = test.quantity
			if err := insertPaperOrderIntentJSONDirectForTest(svc, candidate, raw, "order_direct_"+strings.ReplaceAll(test.name, " ", "_")); err == nil {
				t.Fatal("direct capitalized intent accepted a non-canonical quantity")
			}
			var count int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected direct quantity rows=%d err=%v", count, err)
			}
		})
	}
}

func TestG38C2PaperTarget(t *testing.T) {
	t.Run("buy retry zero and active ceiling", func(t *testing.T) {
		svc, signal, _ := g38c2PaperSignalFixture(t, true)
		lease := mustK2CLease(t, svc, k2aAccountRef)
		signal.TargetQuantity = "10"
		firstEvent, firstOrder, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
		if err != nil || firstOrder == nil || firstOrder.Status != "OPEN" || firstOrder.Quantity != "10" {
			t.Fatalf("BUY target event=%+v order=%+v err=%v", firstEvent, firstOrder, err)
		}
		beforeRetry := paperAdmissionCountsForTest(t, svc)
		replayedEvent, replayedOrder, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
		if err != nil || replayedEvent.EventID != firstEvent.EventID || replayedOrder.OrderID != firstOrder.OrderID || paperAdmissionCountsForTest(t, svc) != beforeRetry {
			t.Fatalf("exact retry event=%+v order=%+v err=%v", replayedEvent, replayedOrder, err)
		}

		same := signal
		same.SignalID = "g38c2-same-active-target"
		if _, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, same, lease.FencingToken); err != nil || order != nil {
			t.Fatalf("same active target order=%+v err=%v", order, err)
		}
		beforeChanged := paperAdmissionCountsForTest(t, svc)
		changed := signal
		changed.SignalID, changed.TargetQuantity = "g38c2-changed-active-target", "11"
		if _, _, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, changed, lease.FencingToken); err == nil {
			t.Fatal("changed active target was accepted")
		}
		if after := paperAdmissionCountsForTest(t, svc); after != beforeChanged {
			t.Fatalf("changed active target leaked rows: before=%+v after=%+v", beforeChanged, after)
		}
	})

	t.Run("zero target records only signal", func(t *testing.T) {
		svc, signal, _ := g38c2PaperSignalFixture(t, true)
		lease := mustK2CLease(t, svc, k2aAccountRef)
		event, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
		counts := paperAdmissionCountsForTest(t, svc)
		if err != nil || event == nil || order != nil || counts.Signals != 1 || counts.Orders != 0 || counts.Authorizations != 0 || counts.Events != 0 {
			t.Fatalf("zero target event=%+v order=%+v counts=%+v err=%v", event, order, counts, err)
		}
	})

	t.Run("session admits later current strategy with the same execution policy", func(t *testing.T) {
		svc, signal, _ := g38c2PaperSignalFixture(t, true)
		session, found, err := loadPaperAccountingSession(context.Background(), svc.db, k2aAccountRef)
		if err != nil || !found {
			t.Fatalf("accounting session=%+v found=%t err=%v", session, found, err)
		}
		next, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, func(config map[string]any) {
			config["experiment_id"] = "g38c2-same-policy-current-strategy"
			config["strategy"].(map[string]any)["version"] = "1.0.8"
		}))
		if err != nil {
			t.Fatal(err)
		}
		selected, err := svc.selectPaperCandidate(context.Background(), next.ResultSHA256, signal.StrategySelectionEventID)
		if err != nil {
			t.Fatal(err)
		}
		signal.SignalID = "g38c2-same-policy-later-strategy"
		signal.StrategyResultSHA256 = next.ResultSHA256
		signal.StrategySelectionEventID = selected.CurrentEventID
		lease := mustK2CLease(t, svc, k2aAccountRef)
		event, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
		if err != nil || event == nil || order != nil || event.PaperAccountingSessionID != session.SessionID ||
			event.ExecutionPolicySHA256 != session.ExecutionPolicySHA256 {
			t.Fatalf("later strategy event=%+v order=%+v session=%+v err=%v", event, order, session, err)
		}
	})

	t.Run("concurrent admissions create one active order", func(t *testing.T) {
		primary, signal, _ := g38c2PaperSignalFixture(t, true)
		fixedNow := primary.now().UTC()
		primary.now = func() time.Time { return fixedNow }
		var databasePath string
		if err := primary.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
			t.Fatal(err)
		}
		secondDB, err := openExistingDB(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		defer secondDB.Close()
		secondary := newService(secondDB, func() time.Time { return fixedNow }, func(prefix string) string { return prefix + "_concurrent_secondary" })
		secondary.executionOwner = primary.executionOwner
		lease := mustK2CLease(t, primary, k2aAccountRef)
		primary.id = func(prefix string) string { return prefix + "_concurrent_primary" }
		signal.TargetQuantity = "10"
		first, second := signal, signal
		first.SignalID, second.SignalID = "g38c2-concurrent-first", "g38c2-concurrent-second"
		type result struct {
			order *OrderState
			err   error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for service, candidate := range map[*Service]PaperSignal{primary: first, secondary: second} {
			service, candidate := service, candidate
			go func() {
				<-start
				_, order, err := service.admitPaperSignal(context.Background(), k2aAccountRef, candidate, lease.FencingToken)
				results <- result{order: order, err: err}
			}()
		}
		close(start)
		orders := 0
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.order != nil {
				orders++
			}
		}
		counts := paperAdmissionCountsForTest(t, primary)
		if orders != 1 || counts.Signals != 2 || counts.Orders != 1 || counts.Authorizations != 1 || counts.Events != 4 || counts.Fills != 0 {
			t.Fatalf("concurrent admissions orders=%d counts=%+v", orders, counts)
		}
	})
}

func TestG38C2PaperAuthorization(t *testing.T) {
	t.Run("immutable local authorization and no Kiwoom", func(t *testing.T) {
		svc, signal, _ := g38c2PaperSignalFixture(t, true)
		signal.TargetQuantity = "10"
		lease := mustK2CLease(t, svc, k2aAccountRef)
		_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
		if err != nil || order == nil || order.Status != "OPEN" {
			t.Fatalf("authorized order=%+v err=%v", order, err)
		}
		var authorizations, reservations, events int
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_execution_authorizations`).Scan(&authorizations); err != nil {
			t.Fatal(err)
		}
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM risk_reservations`).Scan(&reservations); err != nil {
			t.Fatal(err)
		}
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=?`, order.OrderID).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if authorizations != 1 || reservations != 0 || events != 4 {
			t.Fatalf("authorization rows=%d reservations=%d events=%d", authorizations, reservations, events)
		}
		authorization, found, err := loadPaperExecutionAuthorizationByOrder(context.Background(), svc.db, order.OrderID)
		if err != nil || !found {
			t.Fatalf("paper authorization found=%t err=%v", found, err)
		}
		tx, err := svc.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		fill := OrderEvent{
			EventID: "event_g38c2_task3_fill_rejected", OrderID: order.OrderID, Type: "FILL_RECORDED", Source: "synthetic",
			Quantity: "1", Price: "100", OccurredAt: svc.now().UTC().Format(time.RFC3339Nano),
			PaperAuthorizationID: authorization.AuthorizationID,
		}
		if err := insertOrderEvent(context.Background(), tx, fill, svc.now().UTC().Format(time.RFC3339Nano)); err == nil {
			tx.Rollback()
			t.Fatal("Task 3 admitted a paper-authorized fill")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE event_type='FILL_RECORDED'`).Scan(&events); err != nil || events != 0 {
			t.Fatalf("rejected paper fill rows=%d err=%v", events, err)
		}
		client := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("paper order reached Kiwoom transport")
			return nil, nil
		}))
		if submitted, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken); err == nil || submitted != nil {
			t.Fatalf("paper order entered Kiwoom submit: state=%+v err=%v", submitted, err)
		}
		for _, statement := range []string{
			`UPDATE paper_execution_authorizations SET quantity='9'`,
			`DELETE FROM paper_execution_authorizations`,
		} {
			if _, err := svc.db.Exec(statement); err == nil {
				t.Fatalf("paper authorization mutation accepted: %s", statement)
			}
		}
	})

	for _, boundary := range []struct {
		name   string
		offset time.Duration
		accept bool
	}{
		{"direct authorization at expiry minus one nanosecond", -time.Nanosecond, true},
		{"direct authorization at expiry", 0, false},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			svc, signal, _ := g38c2PaperSignalFixture(t, true)
			signal.TargetQuantity = "10"
			signalEvent, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, signal)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := svc.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			state, err := svc.recordOrderIntentTx(context.Background(), tx, capitalizedPaperOrderIntent(*signalEvent, "BUY", "10"))
			if err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			lease := mustK2CLease(t, svc, k2aAccountRef)
			authority, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			if err != nil {
				t.Fatal(err)
			}
			expires, _ := canonicalUTCTime(lease.LeaseExpiresAt)
			authorization := paperExecutionAuthorization{
				AuthorizationID: paperEventID("authorization", state.OrderID), SchemaVersion: paperExecutionAuthorizationSchema,
				OrderID: state.OrderID, AccountRef: k2aAccountRef, PaperAccountingSessionID: signalEvent.PaperAccountingSessionID,
				ExecutionPolicySHA256: signalEvent.ExecutionPolicySHA256, PolicyVersion: paperAccountingPolicyVersion,
				Side: "BUY", Quantity: "10", AuthorityEventID: authority.EventID, FencingToken: lease.FencingToken,
				RiskEventID: paperEventID("risk", state.OrderID), DispatchEventID: paperEventID("dispatch", state.OrderID),
				AuthorizedAt: expires.Add(boundary.offset).Format(time.RFC3339Nano),
			}
			tx, err = svc.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = insertPaperExecutionAuthorization(context.Background(), tx, authorization)
			if boundary.accept && err != nil {
				tx.Rollback()
				t.Fatalf("authorization before lease expiry was rejected: %v", err)
			}
			if !boundary.accept && err == nil {
				tx.Rollback()
				t.Fatal("direct authorization at lease expiry was accepted")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_execution_authorizations`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("authorization boundary rows=%d err=%v", count, err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*Service, *ExecutionAuthorityState)
		token  func(*ExecutionAuthorityState) int64
	}{
		{name: "wrong fence", token: func(lease *ExecutionAuthorityState) int64 { return lease.FencingToken + 1 }},
		{name: "stale fence", token: func(lease *ExecutionAuthorityState) int64 { return lease.FencingToken - 1 }},
		{name: "foreign owner", mutate: func(svc *Service, _ *ExecutionAuthorityState) { svc.executionOwner = "foreign_owner" }},
		{name: "expired", mutate: func(svc *Service, lease *ExecutionAuthorityState) {
			expires, _ := canonicalUTCTime(lease.LeaseExpiresAt)
			svc.now = func() time.Time { return expires }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, signal, _ := g38c2PaperSignalFixture(t, true)
			signal.TargetQuantity, signal.ExpiresAt = "10", "2026-01-12T00:00:00.000000000Z"
			lease := mustK2CLease(t, svc, k2aAccountRef)
			if test.mutate != nil {
				test.mutate(svc, lease)
			}
			token := lease.FencingToken
			if test.token != nil {
				token = test.token(lease)
			}
			before := paperAdmissionCountsForTest(t, svc)
			if _, _, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, token); err == nil {
				t.Fatal("invalid lease authorized paper dispatch")
			}
			if after := paperAdmissionCountsForTest(t, svc); after != before {
				t.Fatalf("lease failure leaked rows: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestG38C2PaperAuthorizationBackup(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	signal.TargetQuantity = "10"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "paper-authorization.db")
	manifest, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id)
	if err != nil || manifest.SchemaVersion != "omni-folio.sqlite.v18" || manifest.PaperExecutionAuthorizationCount != 1 || manifest.PaperCapitalizedFillCount != 0 {
		t.Fatalf("paper authorization manifest=%+v err=%v", manifest, err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	restored, err := newService(restoredDB, time.Now, randomID).loadOrderState(context.Background(), order.OrderID)
	if err != nil || restored.Status != "OPEN" {
		t.Fatalf("restored paper order=%+v err=%v", restored, err)
	}
}

type paperAdmissionCounts struct {
	Signals, Orders, Authorizations, Events, Fills int
}

func paperAdmissionCountsForTest(t testing.TB, svc *Service) paperAdmissionCounts {
	t.Helper()
	var counts paperAdmissionCounts
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM paper_signal_events`:                           &counts.Signals,
		`SELECT COUNT(*) FROM order_idempotency WHERE mode='paper'`:          &counts.Orders,
		`SELECT COUNT(*) FROM paper_execution_authorizations`:                &counts.Authorizations,
		`SELECT COUNT(*) FROM order_events`:                                  &counts.Events,
		`SELECT COUNT(*) FROM order_events WHERE event_type='FILL_RECORDED'`: &counts.Fills,
	} {
		if err := svc.db.QueryRow(query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func insertPaperOrderIntentDirectForTest(svc *Service, intent OrderIntent, orderID string) error {
	raw, hash, err := orderJSONHash(intent)
	if err != nil {
		return err
	}
	_, err = svc.db.Exec(`INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at)
		VALUES(?,?,?,?,?,?,?,?)`, intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID, hash, orderID, string(raw), svc.now().UTC().Format(time.RFC3339Nano))
	return err
}

func insertPaperOrderIntentJSONDirectForTest(svc *Service, intent OrderIntent, raw map[string]any, orderID string) error {
	encoded, hash, err := orderJSONHash(raw)
	if err != nil {
		return err
	}
	_, err = svc.db.Exec(`INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at)
		VALUES(?,?,?,?,?,?,?,?)`, intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID, hash, orderID, string(encoded), svc.now().UTC().Format(time.RFC3339Nano))
	return err
}

func downgradePaperAuthorizationForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`DROP TRIGGER paper_execution_authorizations_no_update;
		DROP TRIGGER paper_execution_authorizations_no_delete;
		DROP TRIGGER paper_execution_authorizations_state_guard;
		DROP TRIGGER paper_signal_events_capitalized_quantity_guard;
		DROP TRIGGER order_idempotency_capitalized_paper_guard;
		DROP TRIGGER order_idempotency_legacy_paper_signal_guard;
		DROP TRIGGER order_events_risk_reservation_guard;
		DROP TRIGGER order_events_dispatch_reservation_guard;
		DROP TRIGGER order_events_non_authority_reservation_guard;
		DROP TRIGGER order_events_capitalized_paper_fill_guard;
		DROP TABLE paper_execution_authorizations;
		ALTER TABLE order_events DROP COLUMN paper_authorization_id;
		CREATE TRIGGER order_idempotency_legacy_paper_signal_guard BEFORE INSERT ON order_idempotency
		WHEN NEW.mode='paper' AND json_extract(NEW.intent_json, '$.signal_schema_version') IN ('paper-signal.v1','paper-signal.v2')
		AND EXISTS (SELECT 1 FROM paper_signal_events WHERE account_ref=NEW.account_ref)
		BEGIN SELECT RAISE(ABORT, 'legacy paper order cannot follow a v3 signal'); END;
		CREATE TRIGGER order_events_risk_reservation_guard BEFORE INSERT ON order_events WHEN NEW.event_type='RISK_APPROVED' BEGIN
			SELECT CASE WHEN NEW.authority_reservation_id IS NULL OR NOT EXISTS (SELECT 1 FROM risk_reservations WHERE reservation_id=NEW.authority_reservation_id AND order_id=NEW.order_id AND risk_event_id=NEW.event_id AND reservation_id=json_extract(NEW.event_json, '$.risk_reservation_id') AND policy_version=json_extract(NEW.event_json, '$.risk_policy_version') AND fencing_token=json_extract(NEW.event_json, '$.fencing_token')) THEN RAISE(ABORT, 'risk approval requires an authority reservation') END;
		END;
		CREATE TRIGGER order_events_dispatch_reservation_guard BEFORE INSERT ON order_events WHEN NEW.event_type='SUBMIT_DISPATCHED' BEGIN
			SELECT CASE WHEN NEW.authority_reservation_id IS NULL OR NOT EXISTS (SELECT 1 FROM risk_reservations WHERE reservation_id=NEW.authority_reservation_id AND order_id=NEW.order_id AND dispatch_event_id=NEW.event_id AND reservation_id=json_extract(NEW.event_json, '$.risk_reservation_id') AND policy_version=json_extract(NEW.event_json, '$.risk_policy_version') AND fencing_token=json_extract(NEW.event_json, '$.fencing_token')) THEN RAISE(ABORT, 'submit dispatch requires an authority reservation') END;
		END;
		CREATE TRIGGER order_events_non_authority_reservation_guard BEFORE INSERT ON order_events WHEN NEW.event_type NOT IN ('RISK_APPROVED','SUBMIT_DISPATCHED') AND NEW.authority_reservation_id IS NOT NULL BEGIN
			SELECT RAISE(ABORT, 'authority reservation is invalid for this event');
		END;
		DELETE FROM schema_migrations WHERE version=17`); err != nil {
		t.Fatal(err)
	}
}

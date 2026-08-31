package main

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestG38C1PaperAccountingSessionDerivesImmutableCapital(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
	ctx := context.Background()
	evidence, selected := selectedPaperStrategy(t, svc)

	policy, err := loadCurrentStrategyExecutionPolicy(ctx, svc.db, evidence.ResultSHA256, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	if session.SchemaVersion != "paper-accounting-session.v1" || session.AccountRef != k2aAccountRef ||
		session.StrategyResultSHA256 != evidence.ResultSHA256 || session.StrategySelectionEventID != selected.CurrentEventID ||
		session.StartingCash != "10000" || session.Currency != "KRW" || session.ExecutionPolicySHA256 != policy.SHA256 ||
		session.ExecutionPolicyJSON != policy.canonicalJSON {
		t.Fatalf("derived session=%+v policy=%+v", session, policy)
	}
	replayed, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
	if err != nil || !reflect.DeepEqual(replayed, session) {
		t.Fatalf("exact idempotency session=%+v replay=%+v err=%v", session, replayed, err)
	}
	if count := paperAccountingSessionCount(t, svc); count != 1 {
		t.Fatalf("session count=%d, want 1", count)
	}

	second, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, func(config map[string]any) {
		config["experiment_id"] = "paper-accounting-session-second"
		config["strategy"].(map[string]any)["version"] = "1.0.3"
	}))
	if err != nil {
		t.Fatal(err)
	}
	secondSelection, err := svc.selectPaperCandidate(ctx, second.ResultSHA256, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err = svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
	if err != nil || !reflect.DeepEqual(replayed, session) {
		t.Fatalf("initial session stopped being idempotent after strategy change: session=%+v replay=%+v err=%v", session, replayed, err)
	}
	if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, second.ResultSHA256, secondSelection.CurrentEventID); err == nil {
		t.Fatal("different initial strategy attempt reset account capital")
	}
}

func TestG38C1PaperAccountingSessionDecimalContract(t *testing.T) {
	t.Run("canonical fractional starting cash opens and stores exactly", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
			result["execution"].(map[string]any)["starting_cash"] = "0.01"
		})
		evidence, err := svc.registerStrategyEvidence(ctx, artifact)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, noStrategySelectionEvent)
		if err != nil {
			t.Fatal(err)
		}
		session, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
		if err != nil {
			t.Fatal(err)
		}
		var stored string
		if err := svc.db.QueryRow(`SELECT starting_cash FROM paper_accounting_sessions WHERE session_id=?`, session.SessionID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if session.StartingCash != "0.01" || stored != "0.01" {
			t.Fatalf("fractional starting cash session=%q stored=%q", session.StartingCash, stored)
		}
	})

	t.Run("table rejects noncanonical fractional starting cash", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		evidence, selected := selectedPaperStrategy(t, svc)
		session := directPaperAccountingSession(t, svc, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
		session.StartingCash = "1.0"
		err := insertPaperAccountingSessionDirect(svc, session)
		if err == nil {
			t.Fatal("paper accounting table accepted noncanonical starting_cash 1.0")
		} else if !strings.Contains(err.Error(), "starting_cash") {
			t.Fatalf("noncanonical starting_cash 1.0 failed outside the table predicate: %v", err)
		}
	})
}

func TestG38C1PaperAccountingSessionFailsClosed(t *testing.T) {
	t.Run("stale selection", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		second, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, func(config map[string]any) {
			config["experiment_id"] = "paper-accounting-stale"
			config["strategy"].(map[string]any)["version"] = "1.0.4"
		}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.selectPaperCandidate(ctx, second.ResultSHA256, selected.CurrentEventID)
		if err != nil {
			t.Fatal(err)
		}
		before := paperAccountingSideEffects(t, svc)
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err == nil {
			t.Fatal("stale selection opened an accounting session")
		}
		assertPaperAccountingSideEffectsUnchanged(t, before, paperAccountingSideEffects(t, svc))
	})

	t.Run("mismatched result", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		_, selected := selectedPaperStrategy(t, svc)
		other, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, func(config map[string]any) {
			config["experiment_id"] = "paper-accounting-mismatch"
			config["strategy"].(map[string]any)["version"] = "1.0.5"
		}))
		if err != nil {
			t.Fatal(err)
		}
		before := paperAccountingSideEffects(t, svc)
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, other.ResultSHA256, selected.CurrentEventID); err == nil {
			t.Fatal("mismatched result opened an accounting session")
		}
		assertPaperAccountingSideEffectsUnchanged(t, before, paperAccountingSideEffects(t, svc))
	})

	t.Run("no strategy", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		if _, err := svc.rollbackPaperCandidate(ctx, selected.CurrentEventID, selected.CurrentEventID); err != nil {
			t.Fatal(err)
		}
		before := paperAccountingSideEffects(t, svc)
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err == nil {
			t.Fatal("no strategy opened an accounting session")
		}
		assertPaperAccountingSideEffectsUnchanged(t, before, paperAccountingSideEffects(t, svc))
	})

	t.Run("corrupt registry", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		if _, err := svc.db.Exec(`DROP TRIGGER strategy_research_evidence_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE strategy_research_evidence SET artifact_sha256=? WHERE result_sha256=?`, strings.Repeat("0", 64), evidence.ResultSHA256); err != nil {
			t.Fatal(err)
		}
		before := paperAccountingSideEffects(t, svc)
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err == nil {
			t.Fatal("corrupt registry opened an accounting session")
		}
		assertPaperAccountingSideEffectsUnchanged(t, before, paperAccountingSideEffects(t, svc))
	})

	t.Run("corrupt order", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		state := mustRecordK2AOrder(t, svc, "paper-accounting-corrupt-order")
		if _, err := svc.db.Exec(`DROP TRIGGER order_idempotency_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_idempotency SET request_sha256=? WHERE order_id=?`, strings.Repeat("0", 64), state.OrderID); err != nil {
			t.Fatal(err)
		}
		before := paperAccountingSideEffects(t, svc)
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err == nil {
			t.Fatal("corrupt order log opened an accounting session")
		}
		assertPaperAccountingSideEffectsUnchanged(t, before, paperAccountingSideEffects(t, svc))
	})

	t.Run("prior account paper order", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		signal := paperEvaluationSignal(evidence.ResultSHA256, selected.CurrentEventID, "paper-accounting-prior-order")
		downgradePaperAuthorizationForTest(t, svc.db)
		if _, err := svc.recordOrderIntent(ctx, paperOrderIntent(k2aAccountRef, signal, "1", "1000")); err != nil {
			t.Fatal(err)
		}
		if err := migrate(svc.db); err != nil {
			t.Fatal(err)
		}
		before := paperAccountingSideEffects(t, svc)
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err == nil {
			t.Fatal("prior paper order opened an accounting session")
		}
		assertPaperAccountingSideEffectsUnchanged(t, before, paperAccountingSideEffects(t, svc))
	})
}

func TestG38C1PaperAccountingSessionDirectWriterAndReplayGuards(t *testing.T) {
	t.Run("direct stale and mismatched selection are rejected", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		stale := directPaperAccountingSession(t, svc, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
		second, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, func(config map[string]any) {
			config["experiment_id"] = "paper-accounting-direct-stale"
			config["strategy"].(map[string]any)["version"] = "1.0.6"
		}))
		if err != nil {
			t.Fatal(err)
		}
		secondSelection, err := svc.selectPaperCandidate(ctx, second.ResultSHA256, selected.CurrentEventID)
		if err != nil {
			t.Fatal(err)
		}
		if err := insertPaperAccountingSessionDirect(svc, stale); err == nil {
			t.Fatal("SQLite accepted a stale session selection")
		}
		mismatch := directPaperAccountingSession(t, svc, k2aAccountRef, second.ResultSHA256, secondSelection.CurrentEventID)
		mismatch.StrategyResultSHA256 = evidence.ResultSHA256
		mismatch.SessionID = paperAccountingSessionID(mismatch.AccountRef, mismatch.StrategyResultSHA256, mismatch.StrategySelectionEventID, mismatch.ExecutionPolicySHA256)
		if err := insertPaperAccountingSessionDirect(svc, mismatch); err == nil {
			t.Fatal("SQLite accepted a mismatched session result")
		}
	})

	t.Run("direct prior paper order is rejected", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		signal := paperEvaluationSignal(evidence.ResultSHA256, selected.CurrentEventID, "paper-accounting-direct-prior")
		downgradePaperAuthorizationForTest(t, svc.db)
		if _, err := svc.recordOrderIntent(ctx, paperOrderIntent(k2aAccountRef, signal, "1", "1000")); err != nil {
			t.Fatal(err)
		}
		if err := migrate(svc.db); err != nil {
			t.Fatal(err)
		}
		if err := insertPaperAccountingSessionDirect(svc, directPaperAccountingSession(t, svc, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)); err == nil {
			t.Fatal("SQLite accepted a session after a paper order")
		}
	})

	t.Run("update and delete are rejected", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		session, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{
			`UPDATE paper_accounting_sessions SET currency='USD'`,
			`DELETE FROM paper_accounting_sessions`,
		} {
			if _, err := svc.db.Exec(statement); err == nil {
				t.Fatalf("SQLite accepted session mutation: %s", statement)
			}
		}
		if proof, err := provePaperAccountingRecovery(ctx, svc.db); err != nil || proof.Sessions != 1 || proof.SHA256 == "" || session.SessionID == "" {
			t.Fatalf("session proof=%+v err=%v", proof, err)
		}
	})

	t.Run("order recovery corruption", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		ctx := context.Background()
		evidence, selected := selectedPaperStrategy(t, svc)
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
			t.Fatal(err)
		}
		state := mustRecordK2AOrder(t, svc, "paper-accounting-proof-corrupt-order")
		if _, err := svc.db.Exec(`DROP TRIGGER order_idempotency_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_idempotency SET request_sha256=? WHERE order_id=?`, strings.Repeat("0", 64), state.OrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := provePaperAccountingRecovery(ctx, svc.db); err == nil {
			t.Fatal("accounting recovery certified a corrupt order log")
		}
		if count := paperAccountingSessionCount(t, svc); count != 1 {
			t.Fatalf("order-proof corruption changed session count=%d", count)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*Service, *PaperAccountingSession)
	}{
		{
			name: "policy corruption",
			mutate: func(svc *Service, session *PaperAccountingSession) {
				if _, err := svc.db.Exec(`DROP TRIGGER paper_accounting_sessions_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`UPDATE paper_accounting_sessions SET execution_policy_json='{}' WHERE session_id=?`, session.SessionID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "record corruption",
			mutate: func(svc *Service, session *PaperAccountingSession) {
				if _, err := svc.db.Exec(`DROP TRIGGER paper_accounting_sessions_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`UPDATE paper_accounting_sessions SET record_sha256=? WHERE session_id=?`, strings.Repeat("0", 64), session.SessionID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "identity corruption",
			mutate: func(svc *Service, session *PaperAccountingSession) {
				if _, err := svc.db.Exec(`DROP TRIGGER paper_accounting_sessions_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`UPDATE paper_accounting_sessions SET session_id='paper_accounting_session_corrupt' WHERE session_id=?`, session.SessionID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			ctx := context.Background()
			evidence, selected := selectedPaperStrategy(t, svc)
			session, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(svc, session)
			if _, err := provePaperAccountingRecovery(ctx, svc.db); err == nil {
				t.Fatal("corrupt accounting session was certified")
			}
		})
	}
}

func TestG38C1PaperAccountingSessionConcurrentExactOpenCreatesOneRow(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
	ctx := context.Background()
	evidence, selected := selectedPaperStrategy(t, svc)

	const callers = 2
	start := make(chan struct{})
	var wg sync.WaitGroup
	sessions := make([]*PaperAccountingSession, callers)
	errs := make([]error, callers)
	for i := range sessions {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			sessions[index], errs[index] = svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
		}(i)
	}
	close(start)
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent open %d: %v", index, err)
		}
	}
	if sessions[0] == nil || sessions[1] == nil || sessions[0].SessionID != sessions[1].SessionID || paperAccountingSessionCount(t, svc) != 1 {
		t.Fatalf("concurrent sessions=%+v count=%d", sessions, paperAccountingSessionCount(t, svc))
	}
}

type paperAccountingSideEffectCounts struct {
	Sessions, Orders, OrderEvents, RiskReservations, AuthorityEvents, LedgerEvents, FXObservations, SecurityPriceObservations int
}

func paperAccountingSideEffects(t testing.TB, svc *Service) paperAccountingSideEffectCounts {
	t.Helper()
	count := func(table string) int {
		var value int
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&value); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return value
	}
	return paperAccountingSideEffectCounts{
		Sessions: count("paper_accounting_sessions"), Orders: count("order_idempotency"), OrderEvents: count("order_events"),
		RiskReservations: count("risk_reservations"), AuthorityEvents: count("execution_authority_events"), LedgerEvents: count("events"),
		FXObservations: count("fx_observations"), SecurityPriceObservations: count("security_price_observations"),
	}
}

func assertPaperAccountingSideEffectsUnchanged(t testing.TB, before, after paperAccountingSideEffectCounts) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("session failure changed durable side effects: before=%+v after=%+v", before, after)
	}
}

func paperAccountingSessionCount(t testing.TB, svc *Service) int {
	t.Helper()
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_accounting_sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func directPaperAccountingSession(t testing.TB, svc *Service, accountRef, resultSHA256, selectionEventID string) PaperAccountingSession {
	t.Helper()
	policy, err := loadCurrentStrategyExecutionPolicy(context.Background(), svc.db, resultSHA256, selectionEventID)
	if err != nil {
		t.Fatal(err)
	}
	session := PaperAccountingSession{
		SchemaVersion: "paper-accounting-session.v1", AccountRef: accountRef,
		StrategyResultSHA256: resultSHA256, StrategySelectionEventID: selectionEventID,
		ExecutionPolicySHA256: policy.SHA256, ExecutionPolicyJSON: policy.canonicalJSON,
		StartingCash: policy.StartingCash, Currency: "KRW", RecordedAt: svc.now().UTC().Format(time.RFC3339Nano),
	}
	session.SessionID = paperAccountingSessionID(session.AccountRef, session.StrategyResultSHA256, session.StrategySelectionEventID, session.ExecutionPolicySHA256)
	return session
}

func insertPaperAccountingSessionDirect(svc *Service, session PaperAccountingSession) error {
	recordJSON, recordSHA, err := orderJSONHash(session)
	if err != nil {
		return err
	}
	_, err = svc.db.Exec(`INSERT INTO paper_accounting_sessions(
		session_id,schema_version,account_ref,strategy_result_sha256,strategy_selection_event_id,
		execution_policy_sha256,execution_policy_json,starting_cash,currency,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, session.SessionID, session.SchemaVersion, session.AccountRef,
		session.StrategyResultSHA256, session.StrategySelectionEventID, session.ExecutionPolicySHA256,
		session.ExecutionPolicyJSON, session.StartingCash, session.Currency, recordSHA, string(recordJSON), session.RecordedAt)
	return err
}

func downgradePaperAccountingForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`DROP TRIGGER paper_accounting_sessions_no_update;
		DROP TRIGGER paper_accounting_sessions_no_delete;
		DROP TRIGGER paper_accounting_sessions_state_guard;
		DROP TABLE paper_accounting_sessions;
		DELETE FROM schema_migrations WHERE version=15`); err != nil {
		t.Fatal(err)
	}
}

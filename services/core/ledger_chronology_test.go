package main

import (
	"context"
	"reflect"
	"testing"
)

func TestLedgerChronologyFIFOLotsAndLatestTime(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "fractional-fifo", testCSV(
		"b0,account-main,BUY,2026-01-01T00:00:00Z,AAPL,1,10,0,USD,-10",
		"b1,account-main,BUY,2026-01-01T00:00:00.1Z,AAPL,1,20,0,USD,-20",
		"s1,account-main,SELL,2026-01-01T00:00:00.2Z,AAPL,1,30,0,USD,30",
	))
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AsOf != "2026-01-01T00:00:00.2Z" ||
		!reflect.DeepEqual(snapshot.Holdings, []Holding{{"instrument_aapl", "AAPL", "1", "20", "USD"}}) ||
		!reflect.DeepEqual(snapshot.RealizedPnL, []Money{{"USD", "20"}}) {
		t.Fatalf("fractional chronology changed oldest cost or latest time: %+v", snapshot)
	}
}

func TestLedgerChronologyAcceptsLaterSell(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "fractional-close", testCSV(
		"b0,account-main,BUY,2026-01-01T00:00:00Z,AAPL,1,10,0,USD,-10",
		"s1,account-main,SELL,2026-01-01T00:00:00.000000001Z,AAPL,1,20,0,USD,20",
	))
}

func TestLedgerChronologyBrokerReplay(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at)
		VALUES('chronological-sell','chronological-sell','account-main','SELL','2026-01-02T00:00:00.000000001Z','instrument_005930','005930','7','1000','0','KRW','7000','chronological-receipt','2026-01-10T15:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=3,recorded_at='2026-01-10T15:00:01Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	_, quantities, err := ledgerKRXQuantities(context.Background(), svc.db, "account-main")
	if err != nil || quantities["005930"] == nil || quantities["005930"].Sign() != 0 {
		t.Fatalf("valid chronological broker replay rejected: %v %v", quantities, err)
	}
}

func TestLedgerChronologyRejectsSellBeforeBuyAtomically(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	preview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"b1,account-main,BUY,2026-01-01T00:00:00.1Z,AAPL,1,10,0,USD,-10",
		"s0,account-main,SELL,2026-01-01T00:00:00Z,AAPL,1,20,0,USD,20",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{preview.PreviewID, "early-sell"}); appErr == nil {
		t.Fatal("sell funded by a later buy committed")
	}
	var count, rev int
	if err := svc.db.QueryRow(`SELECT (SELECT COUNT(*) FROM events), revision FROM ledger_meta WHERE singleton=1`).Scan(&count, &rev); err != nil || count != 0 || rev != 0 {
		t.Fatalf("rejected chronology mutated ledger: count=%d revision=%d err=%v", count, rev, err)
	}
}

func TestLedgerChronologyCashVoidFractionalBoundary(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "whole-cash", testCSV("d0,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10"))
	applyTestCSV(t, svc, "fractional-void", cashVoidCSV("v1,account-main,CASH_VOID,2026-01-01T00:00:00.000000001Z,,,,,USD,-10,d0"))
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || !reflect.DeepEqual(snapshot.Cash, []Money{{"USD", "0"}}) {
		t.Fatalf("valid cash reversal failed: %+v %v", snapshot, err)
	}
}

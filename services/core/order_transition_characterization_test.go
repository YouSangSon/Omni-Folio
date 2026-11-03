package main

import (
	"context"
	"reflect"
	"testing"
)

func TestArchitectureOrderEventReplayIsIdempotentAbovePureTransition(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	state := mustReadyAndDispatchK2AOrder(t, svc, "architecture-idempotency", "unused")
	event := k2aFill("architecture-idempotent-fill", state.OrderID, "kiwoom_execution_DDDDDDDDDDDDDDDDDDDDDDDD", "2", "1000")

	first := mustAppendK2AEvent(t, svc, event, "PARTIALLY_FILLED")
	replayed := mustAppendK2AEvent(t, svc, event, "PARTIALLY_FILLED")
	var rows int
	if err := svc.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM order_events WHERE event_id=?`, event.EventID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || !reflect.DeepEqual(first, replayed) || replayed.FilledQuantity != "2" {
		t.Fatalf("rows=%d first=%+v replayed=%+v", rows, first, replayed)
	}
}

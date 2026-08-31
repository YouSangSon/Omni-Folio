package main

import (
	"context"
	"testing"
	"time"
)

func TestArchitectureStrategyRollbackAndExecutionHaltCommitTogether(t *testing.T) {
	setup := func(t *testing.T) (*Service, *StrategySelectionState, *ExecutionAuthorityState) {
		t.Helper()
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
		_, selected := selectedPaperStrategy(t, svc)
		return svc, selected, mustK2CLease(t, svc, k2aAccountRef)
	}

	t.Run("commit", func(t *testing.T) {
		svc, selected, lease := setup(t)
		rolledBack, err := svc.rollbackPaperCandidate(context.Background(), selected.CurrentEventID, selected.CurrentEventID)
		if err != nil || rolledBack.SelectedResultSHA256 != noStrategySelection {
			t.Fatalf("rollback=%+v err=%v", rolledBack, err)
		}
		authority, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		if err != nil || authority.Armed || authority.LeaseOwner != "" || authority.LeaseExpiresAt != "" || authority.FencingToken != lease.FencingToken+1 {
			t.Fatalf("authority=%+v err=%v", authority, err)
		}
	})

	t.Run("rollback", func(t *testing.T) {
		svc, selected, lease := setup(t)
		svc.id = func(string) string { return selected.CurrentEventID }
		if _, err := svc.rollbackPaperCandidate(context.Background(), selected.CurrentEventID, selected.CurrentEventID); err == nil {
			t.Fatal("forced strategy event collision committed")
		}
		registry, registryErr := replayStrategyRegistry(context.Background(), svc.db)
		authority, authorityErr := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		if registryErr != nil || registry.CurrentEventID != selected.CurrentEventID || registry.SelectedResultSHA256 != selected.SelectedResultSHA256 {
			t.Fatalf("registry=%+v err=%v", registry, registryErr)
		}
		if authorityErr != nil || !authority.Armed || authority.LeaseOwner == "" || authority.FencingToken != lease.FencingToken {
			t.Fatalf("authority=%+v err=%v", authority, authorityErr)
		}
	})
}

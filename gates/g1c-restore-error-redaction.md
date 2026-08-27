# G1.7 Restore Error Redaction Gate

Scope: restore 후보와 golden portfolio snapshot이 다를 때 검증은 fail-closed하되, 반환 오류와 CLI 로그에 portfolio·거래 provenance를 포함하지 않는다. backup format, schema, API, UI, dependency는 바꾸지 않는다.

- [x] G1C1: mismatch 오류는 고정된 복구 문구만 반환하고 cash, holding, realized PnL, event ID와 receipt ID를 노출하지 않는다.
  CHECK: go test -count=1 -run '^TestVerifyRestoreMismatchRedactsPortfolioData$' ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: services/core
  EVIDENCE: 2026-08-27 KST RED에서 실제 snapshot JSON 노출을 재현했고, CLI `run(verify-restore)` 경로의 unsafe-line mutation은 다시 RED, 고정 오류 복구 후 focused test는 PASS했다.

- [x] G1C2: 전체 Go core suite가 race detector와 함께 통과한다.
  CHECK: go test -race -count=1 ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: services/core
  EVIDENCE: 2026-08-27 KST `go test -count=1 ./...`와 `go test -race -count=1 ./...`가 각각 PASS했다.

- [x] G1C3: monorepo check와 HTTP smoke가 기존 계약을 보존하고 소유한 생성물을 정리한다.
  CHECK: make check && make smoke
  EXPECT: smoke: health, status, preview, apply, snapshot, market data OK
  EVIDENCE: 2026-08-27 KST `make check && make smoke`가 Go, Flutter 47 tests, Python 13 tests, JSON 15 files와 HTTP smoke를 PASS했고 `govulncheck ./...`는 취약점을 찾지 못했다.

- [x] G1C4: 독립 보안 검토가 mismatch 오류·CLI logging 흐름과 공개 diff에서 민감값 비노출을 확인한다.
  EVIDENCE: 2026-08-27 KST 독립 reviewer가 모든 caller, CLI 전파, manifest hash gate 우회 후 mismatch 도달과 비민감 공개 diff를 확인했고 P0/P1/P2 없이 `MERGE_READY: yes`로 판정했다.

This gate does not prove encrypted backups, cloud log-pipeline redaction, credentialed broker recovery, candidate cleanup on failed verification, deployment, or live-order readiness.

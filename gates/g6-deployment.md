# G6 Deployment Promotion Gate

## Pass when

- PostgreSQL migration이 backup, row count, checksum, ledger invariant, order sequence 검증을 통과한다.
- API, worker, runner, execution gateway의 process identity와 최소 secret scope가 분리된다.
- runner lease/fencing, duplicate Job/CronJob, overlapping rollout에서 신규 주문이 fail-closed한다.
- load test가 Kubernetes와 독립 scaling 필요를 증명한다.
- non-root image, read-only filesystem, resource budgets, probes, secret encryption/RBAC가 검증된다.
- 신규 주문 차단 → reconciliation → lease handoff → rollout → smoke → rollback rehearsal가 통과한다.

## Evidence

- Not active. No Kubernetes manifests before this gate entry criteria are met.

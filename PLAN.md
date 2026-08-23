# Omni Folio Execution Plan

상태: G0 진행 중

## Now

- [x] 제품·안전 목표와 조사 기준선
- [x] Flutter/Go/Python 및 state-authority ADR
- [ ] G0 contracts와 monorepo 실행 명령
- [ ] G1 CSV preview → atomic apply → ledger snapshot/receipt
- [ ] G2 동일 fixture를 표시하는 Flutter client
- [ ] G3 동일 market fixture를 읽는 deterministic Python backtest
- [ ] local OCI/Compose smoke와 전체 check

## Next

- [ ] read-only broker adapter 하나와 reconciliation
- [ ] OHLCV/portfolio chart와 accessibility/performance budget
- [ ] paper order state machine과 unknown-submit recovery
- [ ] deterministic strategy candidate search와 champion/challenger evidence
- [ ] strategy/risk/paper runner와 자동 paper/shadow promotion evidence

## Later, only after gates

- [ ] 두 번째 broker adapter
- [ ] PostgreSQL maintenance migration
- [ ] Kubernetes deployment adapter
- [ ] limited live 자동매매

## Decision map

- Runtime와 monorepo: [`docs/adr/0001-runtime-and-monorepo.md`](docs/adr/0001-runtime-and-monorepo.md)
- 제품 목표: [`docs/goal-prompt.md`](docs/goal-prompt.md)
- 상세 계획과 review 기록: [`docs/omni-folio-plan.md`](docs/omni-folio-plan.md)
- Gate 상태: [`GATES.md`](GATES.md)

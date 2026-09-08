# G4V / K2B3 Credential-Free Limit Order Dispatch Port

## Scope

K2B2의 Kiwoom mock `LIMIT` 제출 동작을 유지하면서 application service가 `KiwoomClient`, 실행 환경, token, `KiwoomError`에 직접 의존하지 않도록 주문 전송 경계를 분리한다. 이 gate는 Go 내부 리팩터링이며 public route/schema/DB/UI, credential, 외부 네트워크 요청, live 또는 production 주문 권한을 추가하지 않는다.

## Acceptance

- `LimitOrderDispatchPort`는 provider, mode, preflight, submit을 함께 봉인한 value capability다. zero value와 intent의 provider/mode가 다른 capability는 credential preflight 전에 거절된다.
- application service는 credential과 provider 응답 형식을 해석하지 않고 `preflight -> durable authorize/dispatch -> broker write` 순서와 주문 상태 전이를 소유한다.
- broker write 시점에는 주문이 이미 durable `SUBMIT_UNKNOWN`이고 `pending_action=SUBMIT`이다.
- 전송은 한 번만 시도한다. ACK는 `SUBMIT_ACKNOWLEDGED`, 명시적 provider rejection만 `SUBMIT_REJECTED`, 나머지 응답·transport·auth 결과는 보수적으로 unknown을 유지한다.
- raw provider error/message/token/order number는 application error와 durable state에 노출되지 않는다.
- Kiwoom adapter factory는 mock client만 capability로 만들며 nil/production client는 invalid zero capability로 fail-closed한다.
- paper intent는 capability preflight나 transport 진입 전에 차단된다.

## TDD evidence

RED:

```text
cd services/core
go test ./... -run '^TestAuthorizedLimitOrderUsesProviderNeutralPortAndRedactsUnknownOutcome$' -count=1
```

`submitAuthorizedLimitOrder`가 아직 없어 compile failure가 발생해 누락된 application port를 재현했다.

Focused GREEN:

```text
cd services/core
go test ./... -run '^(TestAuthorizedLimitOrder.*|TestK2B2.*|TestG38C2PaperAuthorization)$' -count=1
```

core와 관련 internal package가 통과했다. fake capability의 submit hook이 durable-before-write를 직접 확인하고, mismatch/zero capability의 preflight·submit 호출 횟수가 0임을 검증한다.

## Final verification

2026-09-08 같은 source 상태에서 `make check`가 통과했다.

- Go format/vet와 전체 test: core 324.651s, exact/order/paper/risk/strategy domain 통과
- Flutter analyze 및 98 tests 통과
- Python research 34 tests 통과
- JSON contracts 17개 검증
- resource preflight와 `test-resource-cleanup` 통과

독립 architecture review의 첫 검토에서 provider/mode miswire와 typed-nil 가능성 두 건을 P2로 찾았다. open interface를 provider/mode-bound value capability로 바꾸고 zero/mismatch를 preflight 전에 거절한 뒤 재검토하여 P1/P2가 없음을 확인했다.

## Limits and next gate

- 새 증권사 adapter, credentialed observation, public order mutation/API/UI, SELL 정책, unknown-submit correlation을 추가하지 않았다.
- broker-coupled runner fencing, production risk, live order와 수익성은 증명하지 않았다.
- 다음 adapter는 같은 capability를 사용하되 해당 provider/mode factory와 provider-private error classification을 별도로 검증해야 한다.

# Security Policy

Omni-Folio는 계좌·거래·주문 데이터를 다루는 개인용 금융 프로젝트입니다. 현재 지원 대상은 기본 branch의 최신 revision이며, local loopback profile만 검증돼 있습니다.

## 취약점 보고

- credential, token, 실제 계좌번호, 거래내역, exploit payload를 공개 issue나 로그에 올리지 마세요.
- GitHub 저장소의 **Security → Report a vulnerability**가 활성화돼 있으면 private vulnerability report를 사용하세요.
- 해당 기능이 없으면 저장소 소유자 또는 collaborator에게 이미 합의된 비공개 채널로 최소 재현 정보만 전달하세요.
- 제3자 증권 API의 취약점은 해당 증권사의 공식 보안 신고 절차도 따르세요. 실제 계정으로 공격적인 검증을 하지 마세요.

보고에는 영향을 받는 revision, 실행 profile, 재현 단계, 예상/실제 결과, 민감값을 제거한 증거를 포함합니다.

## Credential 노출 시

1. 노출된 키·token을 즉시 폐기하고 증권사에서 재발급합니다.
2. 허용 IP, 연결된 계좌, 최근 로그인·주문·API 호출을 확인합니다.
3. 실주문과 runner를 중지하고 reconciliation이 끝날 때까지 다시 열지 않습니다.
4. Git history에서 문자열만 지우는 것으로 끝내지 않습니다. 이미 노출된 credential은 복구 불가능한 것으로 취급합니다.

## 저장소 보안 경계

- 실제 broker secret과 token은 Git, Flutter bundle, Python research, fixture, crash report에 들어가면 안 됩니다.
- `.env.example`에는 비밀값이 없는 local reference만 둡니다. 실제 secret은 개발 시 OS keychain, cloud 전환 시 secret manager가 소유합니다.
- 현재 Compose profile에는 인증·TLS가 없고 `127.0.0.1` 전용입니다. 외부 공개는 지원하지 않습니다.
- 실거래는 아직 구현·활성화돼 있지 않습니다. 향후에도 server-side 승인, allowlist, promotion evidence, kill switch를 매 주문마다 검증해야 합니다.

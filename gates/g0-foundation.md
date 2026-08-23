# G0 Foundation Gate

## Pass when

- Flutter client, Go core, Python research, contracts, infra 경계가 ADR과 실제 디렉터리에 일치한다.
- OpenAPI가 health/status와 첫 import slice의 decimal-string 계약을 정의한다.
- 각 서브프로젝트가 독립 실행·테스트 가능하고 root command로 함께 검증된다.
- client와 research가 Go 내부 package나 DB에 직접 의존하지 않는다.

## Evidence

- 2026-08-24 `make check`: Go format/vet/test, Flutter format/analyze/7 widget-unit tests, Python compile/13 tests, 15 JSON contracts parsed.
- `apps/client`, `services/core`, `services/research`, `contracts`, `infra` are independently runnable through the root `Makefile`.
- Flutter and Python boundary tests use wire fixtures only; neither imports Go packages nor opens the operational SQLite DB.

# Local paper workflow

이 명령은 **실제 증권사와 연결하지 않는 수동 1회 fixture 실행**입니다. 자동매매 daemon, 키움 모의투자 API, 실거래 또는 수익성 검증을 뜻하지 않습니다. `paper-run-loop`는 여전히 성과 안전정책만 실행합니다.

## 입력과 초기화

먼저 기존 `migrate`, `strategy-register`, `strategy-select`로 DB와 현재 연구 후보를 준비합니다. 아래 명령은 저장소 루트에서 실행합니다. `PAPER_RESULT_SHA`와 `PAPER_SELECTION_EVENT`에는 해당 명령의 실제 출력값을 넣습니다. `account_local_paper`는 실제 계좌번호가 아닌 별도 로컬 alias입니다.

```bash
cd services/core
go run . paper-init -db /absolute/path/paper.db \
  -account account_local_paper -result-sha256 "$PAPER_RESULT_SHA" \
  -expected-current-event "$PAPER_SELECTION_EVENT"
```

초기화는 등록 연구의 시작 현금을 한 번만 고정하며 주문 권한을 켜지 않습니다. 같은 요청은 같은 session을 반환하고, 다른 연구로 기존 원금을 덮어쓰지 않습니다. non-paper 주문 이력이 있는 alias는 사용할 수 없습니다.

연구 CSV는 후보 생성에 실제로 쓴 원본이어야 합니다. 7개 필수 열은 `bar_at,symbol,open,high,low,close,volume`이며 중복 없는 추가 열은 허용합니다. 연구와 신규 입력은 동일 KRX 6자리 종목이어야 합니다. 이 로컬 경로는 각 파일 1 MiB, 최대 500개 봉으로 제한합니다.

신규 `latest.csv`는 아래 순서의 **13개 열만** 허용합니다.

```csv
bar_at,symbol,venue,timezone,interval,open,high,low,close,volume,open_at,source_available_at,fetched_at
```

- `bar_at`은 봉 종료, `open_at`은 봉 시작입니다. 시각은 UTC 초 단위 `YYYY-MM-DDTHH:MM:SSZ`로 직접 제공하며 `open_at < bar_at <= source_available_at <= fetched_at <= 서버 시각`이어야 합니다.
- `venue=KRX`, `timezone=Asia/Seoul`, `interval=1d`로 고정하고 source는 `paper_fixture`, currency는 `KRW`, 조정 기준은 `unspecified`입니다. 거래일·휴장·실제 공급자 가용 시각을 검증하거나 추론하지 않습니다.
- CSV 전체에 하나의 종목, 오름차순 고유 종료 시각, 정확한 decimal OHLCV와 `slow_window + 1`개 이상의 봉을 넣습니다. 마지막 봉은 연구 표본 이후여야 합니다.
- 실제 실행의 성과 기준 시각은 session/선택 생성 이후의 새 종료 봉이어야 합니다. 초기화 전에 끝난 과거 파일은 가져오기만 가능하며, 이를 실행하려고 session이나 서버 시계를 과거로 돌리지 않습니다.
- rolling 파일에서 과거 행의 가격·명시 시각을 수정하지 않습니다. 기존 구간 안의 저장 봉을 누락하거나 과거 봉을 뒤늦게 끼워 넣으면 거절합니다. 새 봉은 현재 저장된 마지막 봉 뒤에만 추가합니다.

## 한 번 실행

저장소 루트에서 기존 Python 명령으로 제안을 먼저 만듭니다. `artifact.json`과 `research.csv`는 현재 선택에 사용한 원본입니다.

```bash
PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=services/research \
  python3 -m omni_research.signal_cli \
  --bars latest.csv --research-bars research.csv --artifact artifact.json > proposal.json

cd services/core
go run . paper-execute -db /absolute/path/paper.db \
  -account account_local_paper -expected-current-event "$PAPER_SELECTION_EVENT" \
  -bars /absolute/path/latest.csv -research-bars /absolute/path/research.csv \
  -proposal /absolute/path/proposal.json -arm-paper
```

`-arm-paper`는 이 1회 실행의 명시적 활성화입니다. 스케줄러가 중지된 계좌를 재활성화하는 용도로 호출하면 안 됩니다. 기존 수량·현금은 유지하고, 정상/실패 종료에서 자신이 소유한 execution lease만 중지한 뒤 global runner lease를 반환합니다.

순서는 원본 연구 검증 → 신규 CSV 원자 저장 → 제안 사전 검증 → 명시적 arm/lease → 기존 eligible 주문 체결 → 성과 안전정책 → 새 제안 재검증/접수입니다. 정책이 `HALT_AND_ROLLBACK`이면 새 주문을 접수하지 않습니다. 출력의 `order`가 없을 수 있으며, 이는 오류가 아니라 `none`, 수량 차이 없음 또는 정책 중지일 수 있습니다. 성공 JSON의 mode는 `paper_fixture_only`입니다.

검증만을 위한 별도 데이터 가져오기는 다음과 같습니다. 주문·session·권한은 생성하지 않습니다. 신규 제안 만료는 최초 저장 receipt +30초이므로 실행 직전 장시간 따로 가져와 두지 않습니다.

```bash
go run . paper-import-bars -db /absolute/path/paper.db -bars /absolute/path/latest.csv
```

## 실패와 재시도

CSV 한 파일은 전부 저장되거나 전부 취소됩니다. 전체 실행의 CSV, 체결, 성과 정책, 새 주문은 서로 다른 durable 단계이므로 뒤 단계 오류가 앞 단계의 확정 기록을 지우지는 않습니다. 잘못된 제안은 사전 검증에서 arm/체결 전에 거절하지만 이미 검증된 CSV는 남을 수 있습니다.

동일 파일의 재시도는 기존 봉의 최초 hash/receipt를 유지합니다. 이미 접수한 제안은 원래 결과를 반환하며 주문을 추가하지 않습니다. 아직 접수하지 못한 제안이 30초를 넘으면 새 주문을 만들지 않습니다. 같은 마지막 봉의 파일 내용·공백·줄바꿈을 바꿔 만료를 연장할 수 없습니다. 다음 실제 새 fixture 봉을 기다리고 새 제안을 생성합니다.

SIGINT/SIGTERM은 취소 context로 전달되며 cleanup은 별도의 제한 시간 context로 수행합니다. cleanup 오류는 성공으로 숨기지 않습니다. SIGKILL/host crash는 즉시 cleanup을 보장하지 못하며 lease TTL 이후 복구가 필요합니다. 다른 프로세스가 이미 takeover했다면 이전 소유자는 새 권한을 중지할 수 없습니다. 새 CLI 자체의 OS signal/kill 검증 matrix는 아직 [G3.8G2B gate](../gates/g3s-local-paper-workflow.md)에 남아 있습니다.

원본 CSV·연구 artifact·proposal 파일은 사용자가 보관합니다. DB는 hash와 검증된 관측/실행 기록을 보관하지만 원본 파일 byte 전체를 backup에 포함하지 않습니다. 원본 입력 파일은 테스트 임시 리소스가 아니므로 명령이 삭제하지 않습니다.

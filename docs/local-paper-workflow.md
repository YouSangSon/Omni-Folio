# Local paper workflow

이 문서는 **실제 증권사와 연결하지 않는 로컬 fixture 실행**을 다룹니다. 수동 1회 실행과 명시적으로 활성화한 pipe 연속 소비를 구분합니다. 키움 모의투자 API, 실거래, 상시 운영 준비 또는 수익성 검증을 뜻하지 않습니다. `paper-run-loop`는 여전히 성과 안전정책만 실행합니다.

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

입력은 일반 파일이어야 합니다. FIFO나 FIFO를 가리키는 링크는 작성자를 기다리지 않고 거절합니다. 파일 크기 제한이 파일 열기·스토리지 I/O 전체의 시간 제한을 뜻하지는 않습니다.

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

입력 준비 후 global claim을 갱신하고, 실행 중에는 체결·성과·접수 단계 사이에서 execution lease 발급 후 10초가 지났으면 두 lease를 함께 갱신합니다. 중지·만료·소유권 상실 뒤에는 갱신하지 않습니다. 단계 하나가 TTL 30초를 넘으면 여전히 실패할 수 있으며 background heartbeat나 상시 소비 기능은 아닙니다. 갱신 이력이 생긴 DB/backup은 갱신을 지원하는 바이너리로 읽고 복구해야 합니다. 이전 바이너리는 같은 owner의 겹치는 lease 이력을 거절하므로 단순 바이너리 downgrade를 지원한다고 가정하지 않습니다. [갱신 계약과 검증](../gates/g3v-paper-execution-heartbeat.md)

순서는 원본 연구 검증 → 신규 CSV 원자 저장 → 제안 사전 검증 → 명시적 arm/lease → 기존 eligible 주문 체결 → 성과 안전정책 → 새 제안 재검증/접수입니다. 정책이 `HALT_AND_ROLLBACK`이면 새 주문을 접수하지 않습니다. 출력의 `order`가 없을 수 있으며, 이는 오류가 아니라 `none`, 수량 차이 없음 또는 정책 중지일 수 있습니다. 성공 JSON의 mode는 `paper_fixture_only`입니다.

기존 주문은 마지막 저장 성과부터 제안의 마지막 봉까지 **봉마다 체결 → 성과 → 안전정책** 순서로 따라잡습니다. 각 봉의 체결량 증가가 없으면 그 봉의 체결 반복을 끝내지만 정책은 평가합니다. 중간 손실이 `HALT_AND_ROLLBACK`을 만들면 뒤 봉의 체결과 새 제안 접수를 중지합니다. 성과가 이미 저장됐는데 정책 단계가 실패했다면, 재시작 시 그 성과를 재작성하거나 체결을 추가하지 않고 미완료 정책부터 닫습니다. 매 체결은 실제 서버 시각의 lease와 회계 불변식을 다시 확인합니다.

첫 실행은 마지막 제안 봉만 성과 anchor로 삼고 이전 입력은 전략 warmup으로 유지합니다. 이미 저장된 최신 성과보다 앞선 과거 누락은 append-only 원칙상 소급 삽입하지 않습니다. 입력에 없던 시장 봉을 복원하거나 중간 신호를 새 주문으로 소급 생성하지 않으며, 기존 `paper-run-due`/`paper-run-loop` 성과 전용 명령은 여전히 최신 close만 처리합니다. 따라서 수동 import·체결·성과 명령을 따로 조합한 것을 이 봉별 자동 실행 경로와 동일하게 취급하지 않습니다. [봉별 복구 gate](../gates/g3ab-paper-chronological-recovery.md)

이미 접수된 동일 signal을 검증한 재시도만, 기존 주문 복구를 위해 서버에 저장된 최신 available close까지 봉별로 따라잡을 수 있습니다. 마지막에는 원래 결정만 반환하며 새 주문 결정을 만들지 않습니다. 신규 제안은 이 예외 없이 자신의 마지막 봉까지만 처리합니다. 같은 close의 완료 성과도 현재 selection 검사를 거치며 다른 전략의 cached 정책을 현재 전략의 승인으로 재사용하지 않습니다. 새 성과는 현재 전략 선택의 기록 시각 이후 봉에만 만듭니다. 성과 저장과 정책 완료 사이에 선택이 바뀌면 기존 성과를 새 선택에 재귀속하지 않고 안전하게 거절합니다.

검증만을 위한 별도 데이터 가져오기는 다음과 같습니다. 주문·session·권한은 생성하지 않습니다. 신규 제안 만료는 최초 저장 receipt +30초이므로 실행 직전 장시간 따로 가져와 두지 않습니다.

```bash
go run . paper-import-bars -db /absolute/path/paper.db -bars /absolute/path/latest.csv
```

## 원본 byte를 함께 전달하기

원본 파일이 전달 후 바뀔 수 있다면 Python 명령에 `--bundle`을 추가하고, 성공 종료한 **한 번의 출력**을 사용합니다. 예시는 저장소 루트 기준입니다.

```bash
PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=services/research \
  python3 -m omni_research.signal_cli \
  --bars latest.csv --research-bars research.csv --artifact artifact.json \
  --bundle > paper-input.json && \
  (cd services/core && go run . paper-execute -db /absolute/path/paper.db \
    -account account_local_paper -expected-current-event "$PAPER_SELECTION_EVENT" \
    -bundle /absolute/path/paper-input.json -arm-paper)
```

`/absolute/path/paper-input.json`은 앞에서 저장한 파일의 실제 절대 경로로 바꿉니다. `-bundle`은 `-bars`, `-research-bars`, `-proposal`과 함께 사용할 수 없습니다. 두 CSV는 각각 1 MiB, JSON 파일 전체는 4 MiB 이하입니다. 원본 UTF-8·줄바꿈을 보존하고 기존 Go 검증을 그대로 거칩니다. 파일 자체가 실행 승인이나 전달 완료 증거는 아닙니다. 출력 파일은 사용자가 보관하며 실패한 생성 결과로 실행하지 않습니다. [bundle gate](../gates/g3u-paper-input-bundle.md)

## 자동 제안 생성만 관찰하기

앞의 Python `signal_cli` 명령에 `--watch`를 추가하면 현재 유효한 제안을 한 줄 출력한 뒤, 각 검사 완료 1초 후 CSV를 다시 읽습니다. 동일 입력은 다시 출력하지 않습니다. 입력 파일은 각 1 MiB 이하의 일반 파일이어야 하며 작성자는 완성된 파일로 원자 교체해야 합니다. 실행 중 연구 artifact 변경, 연구 CSV hash 불일치, 같은/과거 마지막 봉의 byte 변경 또는 잘못된 입력은 오류로 종료합니다.

출력은 단일 JSON 파일이 아니라 NDJSON 스트림입니다. `--watch --bundle`은 각 줄에 원본 CSV도 담습니다. 이를 `proposal.json`에 계속 덮어쓰거나 1회용 `paper-execute`에 pipe로 연결하지 마세요. 생성기는 파일·DB·주문을 만들지 않으며, 재시작 시 같은 제안이 재출력되거나 검사 사이 중간 snapshot이 누락될 수 있습니다. [생성 전용 gate](../gates/g3t-paper-proposal-watch.md)

## 명시적으로 연속 fixture 소비하기

아래는 저장소 루트 기준 macOS/Linux pipe 경로입니다. 위 초기화·입력 조건을 먼저 만족해야 합니다. `OMNI_CORE_BIN`은 미리 빌드한 실행 파일의 절대 경로로 지정합니다. `go run` wrapper가 아니라 실제 바이너리를 실행해야 해당 PID에 보낸 signal의 종료 동작이 명확합니다.

```bash
set -o pipefail
PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=services/research \
  python3 -m omni_research.signal_cli \
  --bars latest.csv --research-bars research.csv --artifact artifact.json \
  --watch --bundle | \
  "$OMNI_CORE_BIN" paper-execute-stream -db /absolute/path/paper.db \
    -account account_local_paper -expected-current-event "$PAPER_SELECTION_EVENT" \
    -arm-paper
```

`-arm-paper`는 **이 프로세스 한 번의 실행**만 승인합니다. 첫 완전한 유효 bundle을 검증한 뒤 한 번만 arm하고 기존 fill→policy→signal 경로를 순서대로 실행합니다. 입력이 없을 때도 10초 간격으로 유효한 execution/global lease를 함께 갱신합니다. DB 단계와 heartbeat는 동시에 실행하지 않으며 단계 하나가 TTL을 넘으면 실패합니다. 중지·정책 rollback·소유권 상실·만료 후 재활성화나 자동 재시작을 하지 않습니다.

stdin은 취소 가능한 pipe만 허용합니다(일반 파일 redirect/TTY 거절). 각 UTF-8 JSON bundle은 마지막 LF를 포함해 4 MiB 이하이며, 중간 오류·크기 초과·LF 없는 마지막 조각은 실행하지 않고 종료합니다. 앞서 확정된 프레임의 DB 기록은 남습니다. 완전한 프레임 뒤 EOF는 그 프레임 처리 후 정상 종료하며, 입력 reader를 닫고 join한 뒤 소유 권한을 정리합니다. 명령은 stdout을 쓰지 않으며 DB 기록이 실행 증거입니다. stdin 원본 descriptor도 닫아 생산자에게 단절을 전달합니다. Python watch는 새 데이터가 없어도 다음 polling 반복에서 단절을 감지해 redacted 오류로 종료합니다.

pipe는 durable queue/acknowledgement가 아닙니다. 재연결은 새 명시적 실행이며 이미 확정된 proposal은 기존 journal의 멱등성으로 중복 주문을 막습니다. 소스 snapshot 누락 방지·장기 운영 부하·브로커 연결은 별도 검증 대상입니다. [연속 소비 gate](../gates/g3w-paper-input-stream.md)

양쪽 프로세스의 종료 상태를 확인합니다. 생산자가 입력 오류나 signal로 종료해도 소비자에게는 정상 EOF로 보일 수 있으므로 마지막 Go 명령의 exit code만 확인하지 않습니다. 반대로 정책 중지로 Go가 pipe를 닫으면 Python의 `output is closed` 오류는 예상된 종료일 수 있습니다. DB의 정책·rollback 기록으로 이유를 구분하고 자동 재활성화하지 않습니다. 실제 두 프로세스 연결 검증은 [pipeline gate](../gates/g3x-paper-pipeline.md)를 따릅니다.

## 실패와 재시도

CSV 한 파일은 전부 저장되거나 전부 취소됩니다. 전체 실행의 CSV, 체결, 성과 정책, 새 주문은 서로 다른 durable 단계이므로 뒤 단계 오류가 앞 단계의 확정 기록을 지우지는 않습니다. 잘못된 제안은 사전 검증에서 arm/체결 전에 거절하지만 이미 검증된 CSV는 남을 수 있습니다.

동일 파일의 재시도는 기존 봉의 최초 hash/receipt를 유지합니다. 이미 접수한 제안은 원래 결과를 반환하며 주문을 추가하지 않습니다. 아직 접수하지 못한 제안이 30초를 넘으면 새 주문을 만들지 않습니다. 같은 마지막 봉의 파일 내용·공백·줄바꿈을 바꿔 만료를 연장할 수 없습니다. 다음 실제 새 fixture 봉을 기다리고 새 제안을 생성합니다.

SIGINT/SIGTERM은 취소 context로 전달되며 cleanup은 별도의 제한 시간 context로 수행합니다. cleanup 오류는 성공으로 숨기지 않습니다. SIGKILL/host crash는 즉시 cleanup을 보장하지 못하며 lease TTL 이후 복구가 필요합니다. 다른 프로세스가 이미 takeover했다면 이전 소유자는 새 권한을 중지할 수 없습니다. 실제 실행 파일의 SIGINT/SIGTERM 종료와 SIGKILL 후 즉시 재실행 차단·실제 TTL 만료 후 중복 없는 복구는 [G3.8G2B gate](../gates/g3s-local-paper-workflow.md)에서 검증합니다. 이 증거는 실행 중인 프로세스의 강제 종료이며 실제 정전이나 모든 중단 시점을 재현한 것은 아닙니다.

원본 CSV·연구 artifact·proposal 파일은 사용자가 보관합니다. DB는 hash와 검증된 관측/실행 기록을 보관하지만 원본 파일 byte 전체를 backup에 포함하지 않습니다. 원본 입력 파일은 테스트 임시 리소스가 아니므로 명령이 삭제하지 않습니다.

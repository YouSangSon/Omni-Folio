# G3AN 저장한 일봉을 기존 연구 계산으로 연결

## 목적과 범위

G4AA가 저장한 mock 일봉을 기존 비용 차감 SMA 후보 탐색·expanding walk-forward·최종 holdout에 사용한다. 수집한 관측을 실제 분석으로 연결하는 단위이며, 정보 수집·탐지부터 근거 있는 추천·사전 승인 투자와 리밸런싱·기록·복기·개선까지의 전체 목표는 계속 남아 있다.

새 결과는 `captured-candle-research.v1`, scope는 `offline_capture_research_only`다. 수치 기준을 모두 통과해도 기존 전략 등록·`paper-improve`·paper proposal decoder가 받지 않는 별도 형식이다. 운영 Go allowlist·DB·HTTP·Flutter·인증 설정은 바꾸지 않는다. 기존 CSV v1/v2 입력·보고서·public `simulate_fold`·`run_experiment` 계약은 유지한다.

## 입력과 실행

```sh
PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=services/research python3 -B -m omni_research.capture_cli \
  --capture data/candles/005930-1d.json \
  --config contracts/fixtures/strategy-improvement-paper-config.json
```

`--capture`와 `--config`만 받는다. native 일봉과 v2 설정만 지원한다. 행 수는 train·validation·holdout 합계와 정확히 같아야 하며 위 설정은 120행이 필요하다. 공개 capture fixture는 1행이므로 이 연구에 충분하지 않다. 라이브러리는 `capture_research.run_capture_experiment(Path, raw_v2_config)`를 제공한다.

두 파일은 열린 descriptor가 regular file인지 확인한 뒤 각각 최대 1 MiB+1 byte를 한 번 읽는다. FIFO는 nonblocking open으로 기다리지 않고 거절한다. UTF-8·단일 JSON object·모든 깊이의 중복 key·비유한 수·예상 밖 key·타입을 검사한다. 일봉 요청·종목·adjustment 요청값·고정 time evidence·bounds, 달력상 유효한 고유 증가 날짜와 요청일 상한, fetch 시작의 고정 UTC+09 요청일, 나노초 단위 fetch 선후관계를 검증한다.

OHLCV는 Decimal 생성 전에 canonical 숫자 문자열과 원천 token 64자 상한을 확인한다. 양수·OHLC 관계·음수가 아닌 거래량과 기존 v2 close 상한·8자리 정밀도를 유지한다. 설정의 숫자 문자열도 지수 표기 등을 먼저 거절한 뒤 기존 config 검증을 사용하며 원천 64자 상한을 설정에 적용하지 않는다.

## 계산과 결과

`improve.py`가 후보 순위·체결·비용·validation·holdout·buy-and-hold·수치 비교를 계속 한 곳에서 계산한다. 내부 `Evaluation`은 후보·범위·fold·선택·지표와 행 인덱스 거래 기록을 담는다. 각 계산은 기존 `bars/config/parameters/warmup` 슬라이스·evaluation 시작·길이·청산·오류 동작을 유지한다. 기존 공개 API는 warmup+bars의 원래 시각 라벨로 복원한다.

새 보고서의 root는 `schema_version,scope,source,config,config_sha256,analysis,limitations,report_sha256`다. 모든 새 하위 object는 닫힌 JSON Schema로 정의한다. `source.capture`는 검증한 decoded capture 전체이며 `source.sha256`은 같은 원본 bytes의 hash다. hash는 제공자의 진위를 증명하지 않으며 decoded object만으로 원본 공백·escape·key 순서를 복원할 수 없으므로 원본 파일을 보관해야 한다. 설정 전체와 기존 canonical config hash를 보존하며 `config.promotion`은 공급한 연구 기준이다.

분석에는 기존 산술·평가정책, 모든 후보의 pre-holdout 지표, 단일 fold 목록, 최종 선택, validation 집계, holdout 범위·거래·지표, buy-and-hold와 세 기준 비교가 있다. 범위와 거래는 `{row,provider_time}`로 원래 0-based 행을 참조한다. 기존 상수 `lookahead_violations`는 새 지표에 포함하지 않는다. 새 보고서에는 기존 accepted artifact·manifest·운영 `promotion.target`·proposal·주문 지시가 없다.

정렬한 compact JSON과 newline이 1 MiB 이하인 것을 확인한 뒤 stdout으로 출력한다. 입력·보고서 오류는 부분 stdout 없이 실패한다. 출력 transport 오류도 실패로 보고한다. 명령은 파일을 발행하지 않으며 셸 리디렉션의 원자성·덮어쓰기 방지를 제공하지 않는다.

## 해석의 한계와 다음 연결

`provider_time`은 관측 라벨이다. timezone·봉 경계·공개 시각이 제공되지 않고 봉 완료도 미검증이다. `delay_bars`는 다음 관측 행을 세며 실제 가용 시각이나 경과 시간을 검증하지 않는다. adjustment 요청 `1`도 적용 결과나 가격 단위 비교 가능성을 증명하지 않는다. 각 fold는 가상 자본을 초기화하고 마지막에 강제 청산한다. 이를 연속 운영 계좌·연환산 성과·실전 수익성으로 해석하지 않는다.

운영 paper admission에는 별도 출처·가용 시각·비교 가능성 증거와 명시적인 계약이 필요하다. 지속 수집·새로운 기회/위험 탐지·보유자산을 고려한 추천·포트폴리오 리밸런싱·승인 범위 안의 실행과 복기 연결은 별도 후속 작업이다.

## 현재 검증 근거

- 실제 신규 CLI는 구현 전 `No module named omni_research.capture_cli`, exit 1·stdout 0 bytes로 실패했고 실제 CLI를 호출한 새 테스트도 실패했다. 기준 입력·stdout·stderr·exit는 `goal-20261103-capture-research/implementation-red-*`에 보존했다.
- 계산 추출 후 기존 연구 19개가 통과했고 v1/v2 native CLI stdout·stderr는 부모가 base `e2a3c86`에서 저장한 기준 bytes와 완전히 같았다. stdout SHA256은 각각 `004fd55c0bc939f3a9394902cf0b6972590ec15f33f6dba726e0344e8882682f`, `143c2690d0a139c95d5ed46d902e0e02644858444fb35e3dcd47b2d2fd5ce846`다.
- 캡처 집중 Python 13개가 통과했다. 기존 120행 CSV로 명시적으로 만든 합성 capture와 v2 30/60/30 설정을 사용해 모든 수치·선택의 일치, 최종 holdout 비개입, 원본 bytes·decoded source·config hash·결정성, 임의 warmup, malformed/중복/유형/날짜/요청/clock/가격/한도/FIFO/출력 실패를 확인했다. 이 입력은 실제 증권사 자료나 수익성 근거가 아니다.
- 실제 synthetic secret·local TLS Go capture 생산자가 120행 파일을 만들고 실제 Python CLI가 세 수치 기준을 모두 통과한 정상 JSON을 냈다. Go 등록·개선·proposal은 거절했고 `paperImprovementStateForTest` 전체가 연구 기록과 변경 횟수까지 같았다. 기존 함수와의 임의 warmup·지연·성공/오류 조합 360건도 결과·라벨·오류가 정확히 같았다.
- 관련 race 5개와 하위 15개가 통과했다. 새 통합 거절, 기존 실제 Go/Python v2 fold·proposal·registry 및 improvement preflight를 포함하며 race linker의 LC_DYSYMTAB 경고는 로그에 보존했다.
- 독립 소스 리뷰에서 차단 결함이 없었고 고정한 소스의 최종 전체 검사를 한 번 실행했다. Go format·vet·Core 전체 438.597초와 내부 6패키지, Python compile·전체 61개, JSON 계약 48개 구문, resource preflight·cleanup이 exit 0으로 끝났다. 최종 소스에서 기존 두 CLI stdout·stderr도 기준 bytes와 같았다. 시간은 이 로컬 검사 실행 시간이며 성능 개선율이 아니다.
- 고정된 native CLI QA 3개도 통과했다. 실제 성공 명령과 독립 보고서 검사는 exit 0이었다. 보고서 18,913 bytes의 원본·설정·body hash, 120행과 행·날짜 참조 18개, 모든 후보·fold·holdout·baseline 수치의 기존 v2 일치를 확인했다. 세 수치 기준을 모두 통과해도 scope는 offline-only다. 1ns 역전 입력은 예상한 exit 1·stdout 0 bytes로 거절됐고 원본과 파생 입력 hash는 변하지 않았다. 판정·원본 stream·영수증은 `docs/_internal/skill-outputs/goal-20261103-capture-research/cli-qa/probes/qa-report.md`와 같은 폴더의 `evidence.json`에 보존했다. Native child 실행 전의 기록기 폴더 설정 오류는 따로 보존했으며 product RED나 native 통과로 세지 않았다.
- JSON 검사는 구문·참조 대상과 테스트가 사용하는 일부 키워드·닫힌 객체 확인이다. 완전한 JSON Schema validator를 실행한 결과로 확대하지 않는다. 원격 CI·배포·실제 Keychain/제공자 인증 관찰은 별도다.
- 소유 프로세스와 임시 폴더가 비었음을 확인한 뒤 작업 전용 Go cache·root 328,792 KiB를 삭제했다. 원본 RED와 검증 로그·입력·소스 manifest는 보존했고 전역·공유 cache는 삭제하지 않았다. 저장소의 binary·Python bytecode·client build/coverage 잔여도 없었다.

Python 3.14.5·Go 1.24.5, `GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1 PYTHONDONTWRITEBYTECODE=1`과 저장소 Make resource 수명주기를 사용했다. 의존성은 기존 cache만 사용하도록 최종 검사의 `GOPROXY/GOSUMDB`를 `off`로 고정했다. 소스 고정·독립 리뷰 뒤 실행한 전체 검사는 위 1회이며 이후 변경은 검증 기록 문서뿐이다. Client·원격 전달과 main 반영은 별도로 확인한다.

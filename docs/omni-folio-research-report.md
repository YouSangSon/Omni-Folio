# Omni Folio: 개인 주식 앱 레퍼런스 조사

조사일: 2026-08-23 KST
판정: **GO, 첫 릴리스는 주문·자동매매 없는 읽기 전용 포트폴리오 MVP로 시작**
신뢰도: 높음. 공식 문서와 공식 GitHub 저장소를 우선했고, 가격과 호출 제한은 변경될 수 있으므로 구현 직전에 다시 확인해야 한다.

## 요약

여러 증권 API를 한 화면에 묶는 것은 가능하지만, 처음부터 주문과 자동매매까지 통합하면 인증, 세션, 호출 제한, 체결 상태, 시장 데이터 라이선스, 오주문 리스크가 한꺼번에 제품의 핵심 복잡도가 된다. 첫 버전은 계좌/거래를 읽어 오거나 CSV로 가져와 정확한 보유 수량, 손익, 배당, 수수료, TWR, XIRR, 벤치마크를 보여주는 개인용 포트폴리오 앱이 적절하다.

가장 참고 가치가 높은 조합은 다음과 같다.

- 제품 구조와 웹 UX: [Ghostfolio](https://github.com/ghostfolio/ghostfolio)
- 로컬 우선과 개인정보 보호: [Wealthfolio](https://github.com/wealthfolio/wealthfolio)
- 거래 원장과 수익률 계산: [Portfolio Performance](https://github.com/portfolio-performance/portfolio)와 [공식 계산 매뉴얼](https://help.portfolio-performance.info/en/concepts/system-overview/)
- 데이터 공급자 추상화: [OpenBB](https://github.com/OpenBB-finance/OpenBB)
- 위험지표 검증: [QuantStats](https://github.com/ranaroussi/quantstats)
- 퀀트 엔진 구조: [LEAN Algorithm Framework](https://www.quantconnect.com/docs/v2/writing-algorithms/algorithm-framework/overview)와 [NautilusTrader Architecture](https://nautilustrader.io/docs/latest/concepts/architecture/)
- 국내 브로커 1순위: [한국투자증권 Open API](https://apiportal.koreainvestment.com/) 또는 [키움 REST API](https://openapi.kiwoom.com/guide/apiguide?dummyVal=0)
- 미국 종이매매/주문 실험: [Alpaca Trading API](https://docs.alpaca.markets/us/docs/trading-api)

## 1. 증권사와 시장 데이터 API

### 국내 브로커

| 후보 | 확인된 범위 | 개인 앱 적합성 | 주의점 |
|---|---|---:|---|
| [한국투자증권 KIS](https://apiportal.koreainvestment.com/) | 국내·해외 주식, REST, WebSocket, 모의·실전 키, 공식 [Python 샘플](https://github.com/koreainvestment/open-trading-api) | **높음** | 토큰, TR별 요청 형식, 호출 제한을 중앙에서 제어해야 한다. 신규 고객 호출 제한 공지가 있으므로 배포 직전 재확인한다. |
| [키움 REST API](https://openapi.kiwoom.com/guide/apiguide?dummyVal=0) | OAuth, 국내·미국 주식, 계좌·시세·차트·주문·실시간, 모의 도메인, 공식 [CLI/샘플](https://github.com/Kiwoom-Securities/Kiwoom-REST-API) | **높음** | 허용 IP와 운영/모의 키 분리. 기존 Windows OCX가 아니라 REST API를 선택한다. |
| [LS증권 OPEN API](https://openapi.ls-sec.co.kr/howto-use) | 계좌별 App Key/Secret, 모의·실전, REST와 별도 WebSocket | 중간 | XingAPI 사용 신청이 선행되고 최대 계좌 수 등 계정 제약을 확인해야 한다. |

국내 MVP는 KIS 또는 키움 하나만 먼저 붙인다. 둘을 동시에 붙여도 사용자 가치는 거의 늘지 않지만 인증·종목 코드·오류 처리 표면은 두 배가 된다. 두 번째 증권사는 공통 모델과 첫 어댑터가 안정된 뒤 추가한다.

### 미국·글로벌 브로커와 데이터

| 후보 | 강점 | 적합한 용도 | 주의점 |
|---|---|---|---|
| [Alpaca](https://docs.alpaca.markets/us/docs/alpaca-api-platform) | REST, WebSocket/SSE, 무료 paper trading, 공식 SDK/OpenAPI, 미국 주식·ETF 중심 | 미국 주문 실험과 paper trading | 무료/유료 피드의 거래소 범위와 지연 차이를 UI에 표시한다. |
| [Interactive Brokers](https://www.interactivebrokers.com/campus/ibkr-api-page/web-api-trading/) | 글로벌 자산과 실제 계좌 범위 | 이미 IBKR을 쓰는 개인의 잔고·거래 동기화 | 한 사용자명의 brokerage session 제약과 별도 market-data 권한이 있어 첫 어댑터로는 무겁다. |
| [Twelve Data](https://twelvedata.com/pricing) | 다시장 REST/WS, 명시적인 credit 모델 | 개인용 시세 보완 | 개인/비상업 요금과 표시·비표시 데이터 권한을 구분한다. |
| [Finnhub](https://www.finnhub.io/pricing-stock-api-market-data) | 미국 시세, 펀더멘털, WebSocket | 워치리스트·기업 정보 | 국제 실시간 데이터는 상위 계약이 필요할 수 있다. |
| [Alpha Vantage](https://www.alphavantage.co/documentation/) | 조정주가, 배당·분할, 펀더멘털, 쉬운 시작 | EOD 프로토타입과 계산 검증 | 무료 기본 제한은 [25회/일](https://www.alphavantage.co/premium/)이므로 다종목 앱의 주 공급자로 부족하다. |
| [yfinance](https://github.com/ranaroussi/yfinance) | 매우 빠른 프로토타이핑 | 개발용 샘플 데이터 | Yahoo 공식 제품이 아니며 개인·연구 용도 안내가 있다. 운영 원천으로 고정하지 않는다. |

### API 설계 원칙

브로커와 데이터 공급자를 같은 인터페이스로 뭉개지 않는다. 브로커는 계좌·거래·주문 상태를, 시장 데이터 공급자는 호가·봉·기업행사를 소유한다.

```text
Broker adapter                 Market-data adapter
  accounts()                     instruments()
  transactions(cursor)           quotes(symbols)
  positions()                    bars(symbol, range)
  orders()                       corporate_actions()
          \                         /
           canonical ledger + instrument registry
                         |
                 deterministic read models
```

모든 응답에는 `provider`, `as_of`, `received_at`, `is_delayed`, `currency`, `raw_reference`를 남긴다. 화면에는 마지막 갱신 시각과 `live / delayed / stale / partial` 상태를 보여준다.

## 2. 참고할 오픈소스

2026-08-23 GitHub API 스냅샷에서 아래 저장소는 archived 상태가 아니며, 최근 push도 확인했다. 별 수는 인기도 신호일 뿐 코드 품질 보증이 아니다.

| 프로젝트 | 참고할 부분 | 라이선스/판단 |
|---|---|---|
| [Ghostfolio](https://github.com/ghostfolio/ghostfolio) | 다계좌, 거래 CRUD, import/export, 성과·리스크·차트, PWA, 모바일 우선 | AGPL-3.0. 구조와 UX를 참고하되 코드 복사는 배포 모델과 라이선스 검토 후 결정. |
| [Wealthfolio](https://github.com/wealthfolio/wealthfolio) | local-first, 계정 없는 로컬 저장, TWR/MWR, 멀티통화, 벤치마크, 데스크톱·iOS·웹 | AGPL-3.0. 개인 앱의 개인정보 경계에 가장 잘 맞는다. |
| [Portfolio Performance](https://github.com/portfolio-performance/portfolio) | 거래 유형, 현금 계정, 수수료·세금·배당, TTWROR/IRR, 자산배분 | EPL-1.0. Java UI를 재사용하기보다 도메인 모델과 테스트 사례를 참고. |
| [OpenBB](https://github.com/OpenBB-finance/OpenBB) | 여러 공급자를 표준 모델로 노출하는 provider 레이어 | AGPL 계열. 앱 전체 의존성보다 어댑터 설계 레퍼런스로 사용. |
| [QuantStats](https://github.com/ranaroussi/quantstats) | Sharpe, Sortino, drawdown, rolling stats, tear sheet | Apache-2.0. 계산 엔진의 교차 검증에 유용. |
| [PyPortfolioOpt](https://github.com/PyPortfolio/PyPortfolioOpt) | Efficient Frontier, Black-Litterman, HRP | MIT. 추천/리밸런싱을 실제로 만들 때만 추가. MVP에는 제외. |
| [Riskfolio-Lib](https://github.com/dcajasn/Riskfolio-Lib) | CVaR, drawdown risk, risk parity | BSD-3-Clause. 고급 분석 단계까지 보류. |
| [vectorbt](https://github.com/polakowo/vectorbt) | 대규모 벡터 백테스트 | 리서치 노트북과 결과 검증에 유용하다. 실시간 주문 엔진으로는 부적합. |
| [backtrader](https://github.com/mementum/backtrader) | 주문·수수료·슬리피지 시뮬레이션 | GPL-3.0이고 최근 활동성이 다른 후보보다 낮다. 신규 핵심 기반으로는 비추천. |
| [LEAN](https://github.com/QuantConnect/Lean) | 알고리즘 생명주기, Algorithm Framework, backtest/live/reconciliation, reality modeling | Apache-2.0. 구조 참고 가치는 높지만 전체 엔진 이식은 과함. |
| [NautilusTrader](https://github.com/nautechsystems/nautilus_trader) | event-driven backtest/live 공통 구조, message bus, 고성능 실행 모델 | LGPL-3.0. 설계 참고 가치는 높지만 개인 앱 MVP에는 무겁다. |

결론은 포크가 아니다. Ghostfolio/Wealthfolio의 화면과 import 흐름, Portfolio Performance의 계산 의미를 참고하되 Omni Folio에는 필요한 최소 원장과 어댑터만 구현한다.

### 자동매매·퀀트 엔진 레퍼런스

2026-08-23 기준 공식 문서와 공식 GitHub만 확인했다. GitHub API 기준 최근 push와 라이선스는 구현 직전 다시 확인한다.

| 프로젝트 | 공식 확인 근거 | 전략 API와 실행 모델 | Omni Folio에 넣을 경계 | 과한 기능 |
|---|---|---|---|---|
| [LEAN](https://github.com/QuantConnect/Lean) | [Algorithm Engine](https://www.quantconnect.com/docs/v2/writing-algorithms/key-concepts/algorithm-engine), [Algorithm Framework](https://www.quantconnect.com/docs/v2/writing-algorithms/algorithm-framework/overview), [Live Reconciliation](https://www.quantconnect.com/docs/v2/writing-algorithms/live-trading/reconciliation), GitHub Apache-2.0 | universe, alpha, portfolio construction, risk, execution처럼 전략 단계를 분리하고 backtest/live 주문·체결 모델을 함께 다룬다. | 전략이 직접 주문하지 않고 signal과 portfolio target을 만들며 risk와 execution이 별도로 처리하는 구조. live reconciliation과 fill/slippage/fee 모델. | 전체 C# 엔진, 클라우드 런타임, 다자산 옵션·선물 모델, QuantConnect 플랫폼 종속 기능을 그대로 이식하는 것. |
| [NautilusTrader](https://github.com/nautechsystems/nautilus_trader) | [Architecture](https://nautilustrader.io/docs/latest/concepts/architecture/), [Backtesting](https://nautilustrader.io/docs/latest/concepts/backtesting/), [Live Trading](https://nautilustrader.io/docs/latest/concepts/live/), GitHub LGPL-3.0 | event-driven trading platform으로 backtest, sandbox, live에서 같은 kernel과 전략 개념을 재사용하며 risk/execution/reconciliation 경계를 둔다. | 교체 가능한 clock/data/execution adapter, deterministic event ordering, startup reconciliation, order/execution event log. | Rust/Python 고성능 엔진 전체, 외부 message bus, order book/HFT 세부 구조를 요구 없이 복제하는 것. |
| [vectorbt](https://github.com/polakowo/vectorbt) | [Features](https://vectorbt.dev/getting-started/features/), [License](https://github.com/polakowo/vectorbt/blob/master/LICENSE.md), GitHub license auto-detection은 NOASSERTION | pandas/NumPy/Numba 중심의 벡터화 백테스트, 지표·signal·portfolio 분석에 강하다. | 리서치 단계의 빠른 파라미터 스윕과 결과 교차 검증. 내부 MVP는 단순 이벤트 백테스터부터 시작. | 실시간 주문 상태 머신, 브로커 체결 reconciliation, 장애 복구 엔진으로 쓰는 것. |
| [Backtrader](https://github.com/mementum/backtrader) | [Concepts](https://www.backtrader.com/docu/concepts/), [Live Trading](https://www.backtrader.com/docu/live/live/), GitHub GPL-3.0 | Cerebro 중심으로 data feed, strategy, broker, sizer, analyzer를 연결한다. live trading 문서도 있다. | 전략 생명주기와 수수료·슬리피지·analyzer 개념 참고. | GPL 코어 의존, 신규 앱 핵심 엔진 채택, 오래된 live broker 구조에 맞추는 것. |

Omni Folio는 일반 목적 엔진을 바로 새로 만들지 않는다. LEAN과 NautilusTrader를 짧게 POC해 KIS adapter, Python 전략 경험, backtest/live parity, 운영 복잡도, 라이선스를 비교하고 적합한 엔진이 있으면 재사용한다. 둘 다 맞지 않을 때만 아래 경계의 최소 일봉/분봉 엔진을 구현한다.

```text
market data -> universe -> signal/alpha -> portfolio target -> risk
     ^                                                     |
     |                                                     v
clock + event replay <- audit/run manifest <- execution <- order intent
```

최소 전략 API는 universe selection, `on_bar`/signal, portfolio construction, risk limits로 시작한다. 전략은 credential과 broker SDK에 접근하지 않는다. 첫 백테스트는 일봉/분봉 OHLCV 재생, 기업행사, 수수료, 세금, 슬리피지, 부분체결, 지연, 거래 정지/stale data를 모델링하고 strategy/version, parameter hash, data snapshot, engine version을 run manifest에 남긴다. tick replay, order book queue position, colocated latency, cross-venue smart routing은 데이터와 실제 필요가 생길 때까지 제외한다.

실전 자동매매 활성화 조건은 paper → shadow → 소액 canary 결과, 동일 전략·포트폴리오·리스크 코어, 주문 idempotency, 체결-원장 reconciliation, 가격·주문·익스포저·손실 한도, 허용 종목 목록, 전략과 독립된 kill switch, stale data/clock drift/provider 장애 차단, 사용자 명시 승인이다. [Alpaca paper trading](https://docs.alpaca.markets/us/docs/paper-trading)은 paper가 시장 충격, 주문 queue 위치, latency slippage 등을 완전히 재현하지 못한다고 명시하므로 paper 성과만으로 live를 승인하지 않는다.

### 자동 전략 개선 루프

자동 개선은 “최근 백테스트 1등을 실전에 반영”하는 기능이 아니다. QuantConnect의 공식 [walk-forward optimization 문서](https://www.quantconnect.com/docs/v2/writing-algorithms/optimization/walk-forward-optimization)는 최근 trailing window로 파라미터를 주기적으로 조정하는 방법과 함께, 갱신이 너무 잦으면 과적합 위험이 커지는 trade-off를 설명한다. [파라미터 최적화 문서](https://www.quantconnect.com/docs/v2/writing-algorithms/optimization/parameters)도 최적화에 사용한 기간을 다시 test로 쓰면 lookahead가 유입된다고 경고한다. Bailey 등 연구는 시도한 전략 구성이 많아질수록 높은 backtest 성과가 우연히 만들어질 가능성이 커진다는 문제를 보인다. ([SSRN 논문](https://papers.ssrn.com/sol3/papers.cfm?abstract_id=2308659))

따라서 첫 구현은 설명 가능한 SMA crossover의 작은 유한 grid만 사용한다. 불변 데이터 snapshot을 시간 순서대로 train/validation/final holdout으로 나누고, 신호 다음 bar부터만 체결 가능하게 한다. 후보 순위는 validation까지만 사용하며 final holdout은 승격 gate로만 사용한다. 수수료·세금·슬리피지·지연 후 수익, 최대 낙폭, 최소 거래 수, turnover/capacity와 기존 champion 또는 단순 benchmark를 함께 비교하고, 같은 입력은 같은 winner와 artifact hash를 내야 한다.

자동화 범위는 후보 생성, 평가, `paper_candidate`, paper/shadow 성능 감시와 rollback이다. 전략 소스를 스스로 고치거나 생성 코드를 동적으로 실행하지 않는다. FINRA도 자동 투자 도구의 가정과 한계를 이해하고 성과 보장을 경계하라고 안내하며, 2025년 auto-trading 안내에서는 검증되지 않은 수익성·AI 주장을 특히 경고한다. ([자동 투자 도구 안내](https://www.finra.org/investors/alerts/automated-investment-tools), [auto-trading 위험 안내](https://www.finra.org/investors/insights/auto-trading-unregistered-entities)) Omni Folio는 이를 제품 문구와 승격 정책에 반영해 수익을 약속하지 않고 canary/live 승격은 owner 승인과 공통 risk gate 밖에서 자동화하지 않는다.

## 3. 틀리면 안 되는 계산 로직

### 원장이 기준 데이터다

`Position`을 직접 수정하지 않는다. `Transaction`과 `CashFlow`를 append-only로 저장하고 보유 수량, 원가, 실현/미실현 손익, 일별 스냅샷은 언제든 재생성 가능한 read model로 만든다.

최소 거래 유형:

- BUY, SELL
- DEPOSIT, WITHDRAWAL
- DIVIDEND, INTEREST
- FEE, TAX
- SPLIT, SPINOFF 또는 범용 CORPORATE_ACTION
- TRANSFER_IN, TRANSFER_OUT
- FX_EXCHANGE

금액은 이진 부동소수점이 아니라 고정소수점/Decimal을 사용한다. 거래 원화, 종목 표시 통화, 보고 기준 통화를 분리하고 체결 시점 FX를 보존한다.

### 수익률은 하나가 아니다

- **TWR**: 외부 입출금 영향을 제거해 운용 성과와 벤치마크를 비교한다. [GIPS](https://www.gipsstandards.org/standards/gips-standards-for-firms/gips-standards-handbook-for-firms/)는 외부 현금흐름마다 구간을 나누고 기하 연결하는 방향을 설명한다.
- **MWR/XIRR**: 사용자가 실제로 돈을 넣고 뺀 시점을 반영한다. [Microsoft XIRR](https://support.microsoft.com/en-us/excel/functions/xirr-function)은 비정기 현금흐름에 최소 하나의 양수와 음수가 필요하다고 정의한다.
- 화면에는 두 값을 함께 보여주고 라벨을 숨기지 않는다. `내 돈의 결과`와 `포트폴리오 운용 결과`를 혼동시키지 않는다.

검증용 골든 케이스는 최소 네 개다: 현금흐름 없음, 중간 입금, 중간 출금, 배당·수수료·분할이 함께 있는 경우. Portfolio Performance 계산 결과와 Excel XIRR을 대조한다.

### 기업행사와 가격

분할·배당은 가격 조정 시계열과 원장 이벤트를 분리한다. `raw`와 `adjusted` 가격을 같은 시계열에서 섞지 않는다. 가격 캐시 키는 적어도 `(provider, instrument_id, interval, timestamp, adjustment, currency)`를 포함한다.

### 세금 lot

MVP는 FIFO와 이동평균 중 사용자가 선택하게 하되, 결과는 `예상 원가/손익`으로 표시한다. 국가별 세금 신고 자동화는 하지 않는다. 매도 시 어떤 lot가 소진됐는지 결과를 저장해 재계산과 감사가 가능해야 한다.

### 가져오기 중복 방지

1순위 키는 `(broker_account_id, source_trade_id)`다. 원천 ID가 없으면 정규화한 `(date, symbol, side, quantity, price, fee, currency)` 해시를 사용한다. import는 바로 반영하지 않고 `parse → normalize → preview diff → confirm → apply`를 거친다.

## 4. UI/UX 레퍼런스와 권장 구조

[TradingView Portfolios](https://www.tradingview.com/support/solutions/43000760937-tradingview-portfolios-track-your-assets-know-your-trades/)는 Overview, Holdings, Transactions, Analysis로 계층을 나누고, 가치 그래프와 벤치마크 성과를 전환한다. [Apple Stocks](https://support.apple.com/guide/iphone/check-stocks-iph1ac0b1bc/26/ios/26)은 워치리스트에서 종목명, 스파크라인, 가격, 변동을 한 행에 두고 상세에서 기간별 차트와 지표를 보여준다. 이 두 구조를 합치면 개인 앱의 핵심 흐름이 된다.

### MVP 정보 구조

```text
Overview
├─ Total value / invested / cash
├─ TWR / XIRR / benchmark delta
├─ Portfolio vs benchmark line chart
├─ Allocation and concentration
├─ Upcoming dividends / earnings
└─ Data freshness and sync errors

Holdings
├─ Search, sort, group by account/sector/currency
└─ Asset detail: price, position, lots, income, transactions

Transactions
├─ Manual entry
├─ CSV/API import review
└─ Duplicate/error recovery

Watchlist
├─ Price, change, sparkline
└─ Simple price alerts

Settings
├─ Broker/data connections
├─ Base currency and cost-basis method
└─ Export, backup, credential status
```

### 화면 우선순위

1. Overview
2. Transactions와 Import Review
3. Holdings와 Asset Detail
4. Connections/Settings
5. Watchlist와 가격 알림
6. 고급 Risk/Optimization

### 시각 시스템

로컬 `ui-ux-pro-max` 데이터의 첫 결과인 OLED dark-only와 금융 터미널식 글꼴은 채택하지 않았다. 재검색 결과의 Minimal/Swiss 방향만 사용하고 제품 유형과 어긋난 스크롤 스토리텔링, 손글씨 글꼴도 버린다.

- 기본: 밝은 모드와 어두운 모드 동등 지원, 시스템 설정 기본값
- 색: neutral/slate 표면 + blue accent. 상승/하락은 색과 함께 `+/-`, 화살표, 텍스트를 사용
- 글꼴: 한국어는 Noto Sans KR 또는 시스템 산세리프, 숫자는 `font-variant-numeric: tabular-nums`
- 밀도: 7/10. 모바일은 핵심 카드, 데스크톱은 정렬 가능한 표
- 모션: 2/10. 데이터 갱신은 레이아웃을 흔들지 않고 reduced-motion을 존중
- 차트: 성과/벤치마크는 line, 자산배분은 bar 또는 5개 이하 donut, drawdown은 area, 종목 상세만 candlestick
- 모든 차트에 요약 문장과 표 대안을 제공

기존 React/PWA + shadcn/Tailwind 권장은 **superseded**다. Flutter 하나로 iOS·Android·app-centric web을 제공하고, Flutter theme extension의 semantic token·Noto Sans KR/system font·tabular number를 사용한다. 차트의 선택 정보는 touch, keyboard, screen reader로 동등하게 접근 가능해야 하며 실시간 갱신에는 pause가 있어야 한다.

### 핵심 상태

모든 데이터 화면은 `loading`, `empty`, `error`, `partial`, `stale`, `success`를 명세한다.

- loading: 실제 레이아웃과 같은 skeleton, `aria-busy`
- empty: 첫 거래 추가 또는 CSV 가져오기 CTA 하나
- stale: 마지막 성공 시각과 다시 시도 버튼
- partial: 실패한 공급자/계좌만 표시하고 성공 데이터는 유지
- error: 원인, 영향 범위, 복구 행동을 함께 표시

## 5. 추천 MVP 아키텍처

```text
Flutter client (iOS / Android / app-centric web)
  └─ versioned HTTP/SSE API; local read cache only
       └─ Go modular monolith
            ├─ canonical ledger (SQLite single writer → PostgreSQL)
            ├─ portfolio calculator / import pipeline
            ├─ broker adapters: KIS first, Alpaca paper later
            ├─ market-data adapter: one provider first
            ├─ strategy/portfolio/risk/order authority
            ├─ execution log + reconciliation
            └─ scheduled sync + freshness/latency monitor

Python research/backtest
  └─ versioned signal/target and reproducible artifacts only
     (no broker credential, operating DB write, or broker order submit)
```

개인 한 명이 쓰는 첫 버전은 SQLite single-writer local로 충분하다. 별도 마이크로서비스, Kafka, Redis, 플러그인 SDK는 필요 없다. 두 번째 replica, 독립 확장, HA/PITR, 높은 동시 쓰기 또는 Kubernetes 전에 maintenance window에서 SQLite → PostgreSQL migration과 restore drill을 통과한다. 그 전에는 Kubernetes manifest도 만들지 않는다.

클라이언트에는 증권사 App Secret을 두지 않는다. 서버 또는 OS keychain에 저장하고 로그·오류·export에서 마스킹한다. 앱 background는 cache refresh와 push 보조만 하며 order submit, reconciliation, kill switch의 authority가 아니다. 실거래마다 서버는 만료 있는 owner 승인, broker/account/strategy allowlist, promotion evidence, healthy kill switch와 idempotency/reconciliation 조건을 재검증한다.

자동매매를 추가할 때는 전략·포트폴리오 구성·리스크·주문 실행을 분리한다. 주문 hot path는 `market data event → freshness check → strategy signal → portfolio target → pre-trade risk → idempotency key → broker submit → ack/execution ingest → ledger reconciliation → audit log`로 측정한다. source/provider/ingest/decision/risk/send/ack/fill UTC timestamp와 monotonic duration을 남겨 구간별 p50/p95/p99, freshness, queue depth를 본다. 시장 데이터 snapshot과 대체된 signal은 병합할 수 있지만 order/ack/fill/cancel/reject 이벤트는 유실하면 안 된다.

필수 제한은 price collar, max order quantity/value, gross/net exposure, max position/open orders/daily loss/order rate, trading hours, stale-data block, clock-drift block, reconciliation/provider 장애 차단, 전략 프로세스와 독립된 kill switch다. [FIA 자동매매 시스템 가이드](https://www.fia.org/sites/default/files/2020-03/Guide%20to%20the%20Development%20and%20Operation%20of%20Automated%20Trading%20Systems%20%28March%202015%29.pdf)도 pre-trade controls, market-data reasonability, kill switch, cancel-on-disconnect, reconciliation, audit trail을 핵심 통제로 다룬다.

소매 브로커 API에서는 고정된 밀리초 SLA나 HFT를 약속하지 않는다. [IBKR Web API](https://www.interactivebrokers.com/campus/ibkr-api-page/web-api-trading/)처럼 세션과 pacing 제한이 있고, [Alpaca order updates](https://docs.alpaca.markets/us/docs/websocket-streaming)는 비동기 fill/partial fill/cancel/reject 이벤트를 스트리밍한다. 초기 목표는 EOD·분봉 전략이며 실제 측정 뒤 전략별 `max_data_age`, `max_decision_time`, `max_ack_wait`을 정한다. 국내 구현은 [KIS 공식 저장소](https://github.com/koreainvestment/open-trading-api)의 REST/WebSocket, 모의·실전 분리, strategy builder/LEAN backtester 사례를 참고하되 호출 제한과 약관은 구현 직전 포털에서 다시 확인한다.

### Runtime 선택

- **기본:** Flutter client + Go modular monolith + Python research/backtest. Go가 원장·주문·risk·broker authority의 단일 owner다.
- **Java/Kotlin/JVM 대안:** broker SDK, 팀 역량 또는 기존 JVM estate가 확실히 우세할 때만 같은 authority 경계를 유지하며 재평가한다.
- **Rust:** Go profile에서 CPU/GC tail bottleneck이 실제로 확인된 좁은 component에만 사용한다.
- **Python-only:** research 속도와 별개로 broker credential과 order submit authority가 흩어지므로 비권장한다.

## 6. 단계별 범위

### MVP

- KIS 또는 키움 한 곳의 읽기 전용 계좌/거래 동기화
- CSV/manual import와 preview
- 보유, 현금, FIFO/평균단가, 배당, 수수료, 분할
- TWR, XIRR, 벤치마크, 최대 낙폭
- Overview, Holdings, Transactions, Import Review, Settings
- JSON/CSV export와 백업

### 다음 단계

- 두 번째 국내 증권사
- Alpaca paper trading 또는 IBKR read-only
- paper → shadow → 소액 canary → limited live 자동매매
- 워치리스트와 알림
- 배당 캘린더, 리밸런싱 제안
- Flutter offline read-cache polish

### 명시적으로 제외

- 첫 릴리스의 실전 주문과 실전 자동매매
- 세금 신고 자동화
- 옵션·선물·레버리지·공매도
- 소셜 기능과 타인 계정
- AI 매수/매도 추천
- 고급 최적화
- HFT, order book 시뮬레이션, co-location, exchange direct market access, sub-ms latency 목표

## 최종 판단

**참고할 오픈소스와 검증된 로직은 충분하다. 새로 만들어도 된다.** 다만 성공 기준은 증권 API 개수가 아니라, 거래를 두 번 가져와도 결과가 같고 입출금·환율·배당·분할 이후에도 수익률을 설명할 수 있는가다.

가장 작은 안전한 선택은 `local-first/read-only + 단일 브로커 + 단일 시장 데이터 공급자 + 정확한 원장`이다. 이 기반이 맞으면 주문과 자동매매는 공통 원장·주문 상태 머신·위험 한도 위에 얹는 확장이고, 기반이 틀리면 API를 많이 붙일수록 숫자와 주문 판단이 더 빠르게 틀린다.

## 조사 방법

- Web/Exa로 20개 이상의 공식 문서·저장소 중심 질의를 수행했다.
- 30개 이상의 후보를 선별하고, 이 보고서에는 구현 결정에 직접 영향을 주는 20여 개의 1차 출처를 포함했다.
- GitHub REST API로 핵심 저장소의 archived 여부, 라이선스, 최근 push를 2026-08-23에 재확인했다.
- Exa 무료 MCP 한도에 일부 질의가 걸려, 해당 영역은 일반 웹 검색과 공식 문서 직접 읽기로 보완했다.

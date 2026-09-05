import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/holding_valuation_page.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/paper_monitor.dart';

Json holdingValuationJson({
  String status = 'stale_sample',
  bool sample = true,
  List<Json>? totals,
  List<Json>? lines,
  List<Json>? issues,
}) => {
  'scope': 'holdings_only',
  'policy_version': 'native_holding_valuation_v1',
  'max_observation_age_seconds': 86400,
  'valuation_as_of': '2026-01-11T14:00:00Z',
  'ledger_revision': 'rev_0000000001',
  'ledger_as_of': '2026-01-10T10:00:00Z',
  'ledger_recorded_at': '2026-01-11T13:00:00Z',
  'status': status,
  'sample': sample,
  'totals':
      totals ??
      (status == 'stale_sample'
          ? const [
              {
                'currency': 'USD',
                'cost_basis': '31',
                'market_value': '45',
                'unrealized_pnl': '14',
              },
            ]
          : null),
  'lines':
      lines ??
      (status == 'empty'
          ? const []
          : const [
              {
                'symbol': 'AAPL',
                'quantity': '3',
                'cost_basis': '31',
                'currency': 'USD',
                'status': 'valued',
                'price': {
                  'source': 'local_fixture',
                  'venue': 'XNAS',
                  'currency': 'USD',
                  'price': '15',
                  'price_adjustment': 'unspecified',
                  'observed_at': '2026-01-10T14:00:00Z',
                  'fetched_at': '2026-01-10T14:00:01Z',
                  'recorded_at': '2026-01-11T13:00:00Z',
                  'sample': true,
                  'state': 'stale',
                },
                'market_value': '45',
                'unrealized_pnl': '14',
                'issue': null,
              },
            ]),
  'issues':
      issues ??
      (status == 'stale_sample'
          ? const [
              {'code': 'sample_data', 'message': 'local sample prices'},
            ]
          : const []),
};

Json unavailableValuationJson() => holdingValuationJson(
  status: 'unavailable',
  sample: true,
  totals: null,
  lines: const [
    {
      'symbol': 'AAPL',
      'quantity': '3',
      'cost_basis': '31',
      'currency': 'USD',
      'status': 'valued',
      'price': {
        'source': 'local_fixture',
        'venue': 'XNAS',
        'currency': 'USD',
        'price': '15',
        'price_adjustment': 'unspecified',
        'observed_at': '2026-01-10T14:00:00Z',
        'fetched_at': '2026-01-10T14:00:01Z',
        'recorded_at': '2026-01-11T13:00:00Z',
        'sample': true,
        'state': 'stale',
      },
      'market_value': '45',
      'unrealized_pnl': '14',
      'issue': null,
    },
    {
      'symbol': '005930',
      'quantity': '2',
      'cost_basis': '140000',
      'currency': 'KRW',
      'status': 'missing',
      'price': null,
      'market_value': null,
      'unrealized_pnl': null,
      'issue': {
        'code': 'missing_security_price',
        'message': 'eligible price unavailable',
        'field': '005930',
      },
    },
  ],
  issues: const [
    {
      'code': 'missing_security_price',
      'message': 'eligible price unavailable',
      'field': '005930',
    },
  ],
);

void main() {
  test('holding valuation GET has no query and parses exact strings', () async {
    late http.Request request;
    final api = RestOmniApi(
      baseUrl: 'http://example.test/',
      client: MockClient((value) async {
        request = value;
        return http.Response(jsonEncode(holdingValuationJson()), 200);
      }),
    );

    final valuation = await api.holdingValuation();

    expect(request.method, 'GET');
    expect(request.url.path, '/v1/portfolio/holding-valuation');
    expect(request.url.hasQuery, isFalse);
    expect(valuation.lines.single.marketValue, '45');
    expect(valuation.totals.single.unrealizedPnl, '14');
    expect(valuation.maxObservationAgeSeconds, 86400);
  });

  test('holding valuation parser rejects field and state drift', () {
    final valid = holdingValuationJson();
    final validLine = Json.from((valid['lines'] as List).single as Json);
    final validPrice = Json.from(validLine['price'] as Json);

    for (final invalid in <Json>[
      {...valid, 'account_id': 'private'},
      {
        ...valid,
        'lines': [
          {...validLine, 'instrument_id': 'private'},
        ],
      },
      {
        ...valid,
        'lines': [
          {
            ...validLine,
            'price': {...validPrice, 'observation_id': 'private'},
          },
        ],
      },
      {...valid, 'status': 'complete'},
      {...valid, 'sample': false},
      {...valid, 'max_observation_age_seconds': 86400.0},
      {...valid, 'totals': null},
      {
        ...valid,
        'lines': [
          {...validLine, 'market_value': 45},
        ],
      },
      {
        ...valid,
        'lines': [
          {...validLine, 'market_value': '0'},
        ],
      },
      {
        ...valid,
        'lines': [
          {...validLine, 'market_value': '-1'},
        ],
      },
      {
        ...valid,
        'lines': [
          {
            ...validLine,
            'price': {...validPrice, 'source': 'kiwoom_production'},
          },
        ],
      },
      {
        ...valid,
        'lines': [
          {...validLine, 'status': 'missing'},
        ],
      },
    ]) {
      expect(
        () => HoldingValuation.fromJson(invalid),
        throwsFormatException,
        reason: '$invalid',
      );
    }

    expect(
      HoldingValuation.fromJson(unavailableValuationJson()).totals,
      isEmpty,
    );
    expect(
      HoldingValuation.fromJson(
        holdingValuationJson(
          status: 'empty',
          sample: false,
          totals: null,
          lines: const [],
          issues: const [],
        ),
      ).lines,
      isEmpty,
    );

    final oneNanosecondStalePrice = {
      ...validPrice,
      'observed_at': '2026-01-10T13:59:59.999999999Z',
      'fetched_at': '2026-01-10T14:00:00Z',
    };
    final staleIssue = const {
      'code': 'stale_security_price',
      'message': 'price is older than policy',
      'field': 'AAPL',
    };
    expect(
      HoldingValuation.fromJson(
        holdingValuationJson(
          status: 'unavailable',
          sample: true,
          lines: [
            {
              ...validLine,
              'status': 'stale',
              'price': oneNanosecondStalePrice,
              'market_value': null,
              'unrealized_pnl': null,
              'issue': staleIssue,
            },
          ],
          issues: [staleIssue],
        ),
      ).lines.single.status,
      'stale',
    );
    expect(
      () => HoldingValuation.fromJson({
        ...valid,
        'lines': [
          {...validLine, 'price': oneNanosecondStalePrice},
        ],
      }),
      throwsFormatException,
    );
    expect(
      () => HoldingValuation.fromJson({
        ...valid,
        'lines': [
          {
            ...validLine,
            'price': {
              ...validPrice,
              'observed_at': '2026-01-10T14:00:00.000000002Z',
              'fetched_at': '2026-01-10T14:00:00.000000001Z',
            },
          },
        ],
      }),
      throwsFormatException,
    );
  });

  testWidgets('Holdings header opens the independent stored valuation', (
    tester,
  ) async {
    final api = _TestApi();
    await tester.pumpWidget(OmniFolioApp(api: api));
    await tester.pumpAndSettle();
    await tester.tap(find.text('보유'));
    await tester.pumpAndSettle();

    expect(find.text('저장 가격으로 평가 보기'), findsOneWidget);
    await tester.tap(find.text('저장 가격으로 평가 보기'));
    await tester.pumpAndSettle();

    expect(find.text('저장 가격 평가'), findsOneWidget);
    expect(find.text('AAPL'), findsOneWidget);
    expect(api.valuationCalls, 1);
  });

  testWidgets('empty Holdings still opens the independent stored valuation', (
    tester,
  ) async {
    final api = _TestApi(
      snapshotValue: PortfolioSnapshot(
        ledgerRevision: 'rev_0000000001',
        costBasisPolicy: 'fifo_exact_else_half_even_residual_8_v1',
        recordedAt: DateTime.utc(2026, 1, 11, 13),
        cash: const [],
        holdings: const [],
        realizedPnl: const [],
      ),
    );
    await tester.pumpWidget(OmniFolioApp(api: api));
    await tester.pumpAndSettle();
    await tester.tap(find.text('보유'));
    await tester.pumpAndSettle();

    expect(find.text('저장 가격으로 평가 보기'), findsOneWidget);
    await tester.tap(find.text('저장 가격으로 평가 보기'));
    await tester.pumpAndSettle();
    expect(api.valuationCalls, 1);
  });

  testWidgets(
    'sample and unavailable views explain authority and missing data',
    (tester) async {
      final api = _TestApi();
      await tester.pumpWidget(
        MaterialApp(home: HoldingValuationPage(api: api)),
      );
      await tester.pumpAndSettle();

      expect(find.text('샘플 저장 가격 · 실시간/현재 시세 아님'), findsOneWidget);
      expect(find.text('통화별 합계'), findsOneWidget);
      expect(find.text('미실현 이익 USD +14'), findsAtLeastNWidgets(1));
      expect(find.textContaining('최대 24시간'), findsOneWidget);

      api.valuationHandler = () async =>
          HoldingValuation.fromJson(unavailableValuationJson());
      await tester.tap(find.byTooltip('저장 가격 평가 새로고침'));
      await tester.pumpAndSettle();

      expect(find.text('일부 종목을 평가할 수 없어 합계를 표시하지 않습니다.'), findsOneWidget);
      expect(find.text('통화별 합계'), findsNothing);

      await tester.scrollUntilVisible(
        find.text('005930'),
        300,
        scrollable: find.descendant(
          of: find.byKey(const Key('holding-valuation-list')),
          matching: find.byType(Scrollable),
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('저장된 적격 가격이 없습니다.'), findsOneWidget);
      expect(find.text('005930'), findsOneWidget);
    },
  );

  testWidgets('refresh retains known-good data and redacts thrown details', (
    tester,
  ) async {
    final api = _TestApi();
    await tester.pumpWidget(MaterialApp(home: HoldingValuationPage(api: api)));
    await tester.pumpAndSettle();
    expect(find.text('AAPL'), findsOneWidget);

    api.valuationHandler = () async =>
        throw const ApiException('account-secret observation-secret');
    await tester.tap(find.byTooltip('저장 가격 평가 새로고침'));
    await tester.pumpAndSettle();

    expect(find.text('AAPL'), findsOneWidget);
    expect(find.text('새로고침하지 못했습니다. 이전 평가 결과를 유지합니다.'), findsOneWidget);
    expect(find.textContaining('account-secret'), findsNothing);
    expect(find.textContaining('observation-secret'), findsNothing);
  });

  testWidgets('empty known-good retains scope and refresh failure', (
    tester,
  ) async {
    final api = _TestApi()
      ..valuationHandler = () async => HoldingValuation.fromJson(
        holdingValuationJson(
          status: 'empty',
          sample: false,
          totals: null,
          lines: const [],
          issues: const [],
        ),
      );
    await tester.pumpWidget(MaterialApp(home: HoldingValuationPage(api: api)));
    await tester.pumpAndSettle();

    expect(find.textContaining('평가할 보유 종목이 없습니다'), findsOneWidget);
    expect(find.textContaining('현금 제외 · 전체 계좌 평가 아님'), findsOneWidget);
    expect(find.textContaining('rev_0000000001'), findsOneWidget);

    api.valuationHandler = () async => throw StateError('private-empty-error');
    await tester.tap(find.byTooltip('저장 가격 평가 새로고침'));
    await tester.pumpAndSettle();

    expect(find.text('새로고침하지 못했습니다. 이전 평가 결과를 유지합니다.'), findsOneWidget);
    expect(find.textContaining('평가할 보유 종목이 없습니다'), findsOneWidget);
    expect(find.textContaining('private-empty-error'), findsNothing);
  });

  testWidgets('retry is single-flight and an initial error is retryable', (
    tester,
  ) async {
    final api = _TestApi()
      ..valuationHandler = () async =>
          throw const ApiException('private-backend-detail');
    await tester.pumpWidget(MaterialApp(home: HoldingValuationPage(api: api)));
    await tester.pumpAndSettle();

    expect(find.text('저장 가격 평가를 불러오지 못했습니다'), findsOneWidget);
    expect(find.textContaining('private-backend-detail'), findsNothing);

    final retry = Completer<HoldingValuation>();
    api.valuationHandler = () => retry.future;
    await tester.tap(find.text('다시 시도'));
    await tester.tap(find.text('다시 시도'), warnIfMissed: false);
    await tester.pump();
    expect(api.valuationCalls, 2);
    retry.complete(HoldingValuation.fromJson(holdingValuationJson()));
    await tester.pumpAndSettle();
    expect(find.text('AAPL'), findsOneWidget);
  });

  testWidgets('320px at 200 percent keeps controls and semantic facts usable', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final semantics = tester.ensureSemantics();

    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.light(),
        darkTheme: ThemeData.dark(),
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(
            context,
          ).copyWith(textScaler: const TextScaler.linear(2)),
          child: child!,
        ),
        home: HoldingValuationPage(api: _TestApi()),
      ),
    );
    await tester.pumpAndSettle();

    final pageContext = tester.element(find.byType(HoldingValuationPage));
    expect(MediaQuery.textScalerOf(pageContext).scale(10), 20);
    await tester.scrollUntilVisible(
      find.text('AAPL'),
      400,
      scrollable: find.descendant(
        of: find.byKey(const Key('holding-valuation-list')),
        matching: find.byType(Scrollable),
      ),
    );
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.byTooltip('저장 가격 평가 새로고침'), findsOneWidget);
    expect(
      find.semantics.byLabel(
        RegExp(r'AAPL.*수량 3.*원가 USD 31.*평가금액 USD 45.*미실현 이익 USD \+14'),
      ),
      findsOneWidget,
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: ThemeData.light(),
        darkTheme: ThemeData.dark(),
        themeMode: ThemeMode.dark,
        home: HoldingValuationPage(api: _TestApi()),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      Theme.of(tester.element(find.byType(HoldingValuationPage))).brightness,
      Brightness.dark,
    );
    expect(tester.takeException(), isNull);
    semantics.dispose();
  });
}

class _TestApi implements OmniApi {
  _TestApi({this.snapshotValue});

  int valuationCalls = 0;
  Future<HoldingValuation> Function()? valuationHandler;
  final PortfolioSnapshot? snapshotValue;

  @override
  Future<HoldingValuation> holdingValuation() async {
    valuationCalls += 1;
    return valuationHandler?.call() ??
        HoldingValuation.fromJson(holdingValuationJson());
  }

  @override
  Future<ServiceStatus> status() async => ServiceStatus.fromJson({
    'live_enabled': false,
    'mode': 'local_import_only',
    'trust_state': 'verified',
    'ledger_revision': 'rev_0000000001',
    'last_verified_at': '2026-01-11T13:00:00Z',
    'issues': const [],
  });

  @override
  Future<PortfolioSnapshot> snapshot() async =>
      snapshotValue ??
      PortfolioSnapshot(
        ledgerRevision: 'rev_0000000001',
        costBasisPolicy: 'fifo_exact_else_half_even_residual_8_v1',
        recordedAt: DateTime.utc(2026, 1, 11, 13),
        cash: const [],
        holdings: const [
          Holding(
            symbol: 'AAPL',
            quantity: '3',
            costBasis: '31',
            currency: 'USD',
          ),
        ],
        realizedPnl: const [],
      );

  @override
  Future<BrokerReconciliation?> latestBrokerReconciliation() async => null;

  @override
  Future<LocalOrderLog> localOrders() async => const LocalOrderLog(orders: []);

  @override
  Future<PaperMonitor> paperMonitor() =>
      throw UnsupportedError('Not used by this fixture.');

  @override
  Future<LedgerActivityPage> ledgerActivities() async =>
      const LedgerActivityPage(
        source: 'local_ledger',
        brokerFreshness: 'unverified',
        ledgerRevision: 'rev_0000000001',
        recordedAt: '2026-01-11T13:00:00Z',
        events: [],
        nextCursor: null,
      );

  @override
  Future<MarketCandles> candles(String symbol) =>
      throw UnsupportedError('Not used.');

  @override
  Future<ImportPreview> preview(String csv) =>
      throw UnsupportedError('Not used.');

  @override
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey) =>
      throw UnsupportedError('Not used.');
}

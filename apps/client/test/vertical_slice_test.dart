import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';

Json fixture(String name) {
  final body = File('../../contracts/fixtures/$name').readAsStringSync();
  return jsonDecode(body) as Json;
}

class FakeApi implements OmniApi {
  FakeApi({
    required this.statusValue,
    required this.snapshotValue,
    required this.previewValue,
    required this.receiptValue,
    required this.candlesValue,
    this.fail = false,
    this.failSnapshot = false,
    this.applyFailures = 0,
  });

  final ServiceStatus statusValue;
  final PortfolioSnapshot snapshotValue;
  final ImportPreview previewValue;
  final ApplyReceipt receiptValue;
  MarketCandles candlesValue;
  Completer<MarketCandles>? candlesCompleter;
  final List<String> applyKeys = [];
  bool fail;
  bool failSnapshot;
  int applyFailures;

  @override
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey) async {
    applyKeys.add(idempotencyKey);
    if (applyFailures > 0) {
      applyFailures -= 1;
      throw const ApiException('서버를 다시 확인하세요.');
    }
    if (fail) throw const ApiException('서버를 다시 확인하세요.');
    return receiptValue;
  }

  @override
  Future<ImportPreview> preview(String csv) async {
    if (fail) throw const ApiException('서버를 다시 확인하세요.');
    return previewValue;
  }

  @override
  Future<PortfolioSnapshot> snapshot() async {
    if (fail || failSnapshot) {
      throw const ApiException('서버를 다시 확인하세요.');
    }
    return snapshotValue;
  }

  @override
  Future<MarketCandles> candles(String symbol) async {
    if (fail) throw const ApiException('서버를 다시 확인하세요.');
    return candlesCompleter?.future ?? candlesValue;
  }

  @override
  Future<ServiceStatus> status() async {
    if (fail) throw const ApiException('서버를 다시 확인하세요.');
    return statusValue;
  }
}

FakeApi goldenApi({bool neverVerified = false}) {
  final preview = ImportPreview.fromJson(fixture('golden-preview.json'));
  final snapshot = PortfolioSnapshot.fromJson(fixture('golden-snapshot.json'));
  final receipt = ApplyReceipt.fromJson(fixture('golden-apply.json'));
  return FakeApi(
    statusValue: ServiceStatus.fromJson({
      'live_enabled': false,
      'mode': 'local_import_only',
      'trust_state': neverVerified ? 'never_verified' : 'verified',
      'ledger_revision': 'rev_0000000003',
      'last_verified_at': neverVerified ? null : '2026-08-23T03:05:00Z',
      'issues': const [],
    }),
    snapshotValue: snapshot,
    previewValue: preview,
    receiptValue: receipt,
    candlesValue: marketCandles(),
  );
}

MarketCandles marketCandles({
  String state = 'stale',
  bool sample = true,
  String priceAdjustment = 'unspecified',
  String source = 'local_fixture',
  String? sourceAsOf = '2026-08-22T20:00:00Z',
  bool includeIssues = true,
}) => MarketCandles.fromJson({
  'symbol': 'AAPL',
  'venue': 'XNAS',
  'timezone': 'America/New_York',
  'interval': '1d',
  'price_adjustment': priceAdjustment,
  'source': source,
  'sample': sample,
  'state': state,
  'source_as_of': sourceAsOf,
  'fetched_at': '2026-08-24T03:00:00Z',
  'issues': !includeIssues
      ? const []
      : state == 'partial'
      ? [
          {'code': 'missing_session', 'message': '일부 세션이 없습니다.'},
        ]
      : source == 'local_fixture'
      ? [
          {
            'code': 'sample_data',
            'message': 'market data is a local sample and not live',
          },
        ]
      : const [],
  'bars': state == 'empty'
      ? const []
      : [
          {
            'at': '2026-08-21T20:00:00Z',
            'open': '100',
            'high': '110',
            'low': '90',
            'close': '105',
            'volume': '1200',
          },
          {
            'at': '2026-08-22T20:00:00Z',
            'open': '105',
            'high': '112',
            'low': '100',
            'close': '101',
            'volume': '900',
          },
        ],
});

Future<void> pumpUi(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 300));
}

void main() {
  test('golden preview and snapshot parse canonical decimal strings', () {
    final preview = ImportPreview.fromJson(fixture('golden-preview.json'));
    final snapshot = PortfolioSnapshot.fromJson(
      fixture('golden-snapshot.json'),
    );

    expect(preview.canApply, isTrue);
    expect(preview.schemaVersion, 'omni-folio.csv.v1');
    expect(preview.previewFingerprint, hasLength(64));
    expect(preview.unresolvedRows, 0);
    expect(snapshot.cash.single.amount, '778');
    expect(snapshot.holdings.single.costBasis, '300.6');

    expect(
      () => Money.fromJson({'currency': 'USD', 'amount': 778}),
      throwsFormatException,
    );
    expect(
      () => ImportPreview.fromJson({
        ...fixture('golden-preview.json'),
        'rows': [
          {'row_number': 2, 'status': 'new'},
          'not-an-object',
        ],
      }),
      throwsFormatException,
    );
    expect(
      () => ServiceStatus.fromJson({
        'live_enabled': false,
        'mode': 'local_import_only',
        'trust_state': 'verified',
        'ledger_revision': 'rev_0000000003',
        'last_verified_at': false,
        'issues': const [],
      }),
      throwsFormatException,
    );
    expect(
      () => ServiceStatus.fromJson({
        'live_enabled': true,
        'mode': 'live',
        'trust_state': 'verified',
        'ledger_revision': 'rev_0000000003',
        'last_verified_at': null,
        'issues': const [],
      }),
      throwsFormatException,
    );
    expect(
      () => ServiceStatus.fromJson({
        'live_enabled': false,
        'mode': 'local_import_only',
        'trust_state': 'verified',
        'ledger_revision': 'rev_0000000003',
        'last_verified_at': null,
        'issues': const [],
      }),
      throwsFormatException,
    );
    expect(
      () => PortfolioSnapshot.fromJson({
        ...fixture('golden-snapshot.json'),
        'valuation_status': 'live',
      }),
      throwsFormatException,
    );
    expect(marketCandles().bars.last.close, '101');
    expect(
      () => marketCandles(priceAdjustment: 'split_adjusted'),
      throwsFormatException,
    );
    expect(
      () => marketCandles(priceAdjustment: 'provider_adjusted'),
      throwsFormatException,
    );
    expect(() => marketCandles(state: 'success'), throwsFormatException);
    expect(() => marketCandles(sourceAsOf: null), throwsFormatException);
    expect(() => marketCandles(includeIssues: false), throwsFormatException);
    final providerCandles = MarketCandles.fromJson({
      'symbol': 'AAPL',
      'venue': 'XNAS',
      'timezone': 'America/New_York',
      'interval': '1d',
      'price_adjustment': 'provider_adjusted',
      'source': 'provider_neutral_fixture',
      'sample': false,
      'state': 'success',
      'source_as_of': null,
      'fetched_at': '2026-08-24T03:00:00Z',
      'issues': const [],
      'bars': [
        {
          'at': '2026-08-22T20:00:00Z',
          'open': '100',
          'high': '110',
          'low': '90',
          'close': '105',
          'volume': '1',
        },
      ],
    });
    expect(providerCandles.source, 'provider_neutral_fixture');
    expect(providerCandles.priceAdjustment, 'provider_adjusted');
    expect(
      () => MarketCandles.fromJson({
        'symbol': 'AAPL',
        'venue': 'XNAS',
        'timezone': 'America/New_York',
        'interval': '1d',
        'price_adjustment': 'unspecified',
        'source': 'local_fixture',
        'sample': false,
        'state': 'stale',
        'source_as_of': '2026-08-22T20:00:00Z',
        'fetched_at': '2026-08-24T03:00:00Z',
        'issues': const [
          {
            'code': 'sample_data',
            'message': 'market data is a local sample and not live',
          },
        ],
        'bars': [
          {
            'at': '2026-08-22T20:00:00Z',
            'open': '100',
            'high': '110',
            'low': '90',
            'close': '105',
            'volume': '1',
          },
        ],
      }),
      throwsFormatException,
    );
    expect(
      () => MarketCandles.fromJson({
        'symbol': 'AAPL',
        'venue': 'XNAS',
        'timezone': 'America/New_York',
        'interval': '1d',
        'price_adjustment': 'provider_adjusted',
        'source': 'provider_neutral_fixture',
        'sample': false,
        'state': 'success',
        'source_as_of': null,
        'fetched_at': '2026-08-24T03:00:00Z',
        'issues': const [],
        'bars': List.generate(
          501,
          (index) => {
            'at': DateTime.utc(
              2024,
              1,
              1,
            ).add(Duration(days: index)).toIso8601String(),
            'open': '100',
            'high': '110',
            'low': '90',
            'close': '105',
            'volume': '1',
          },
          growable: false,
        ),
      }),
      throwsFormatException,
    );
    expect(
      () => MarketCandles.fromJson({
        'symbol': 'AAPL',
        'venue': 'XNAS',
        'timezone': 'America/New_York',
        'interval': '1d',
        'price_adjustment': 'unspecified',
        'source': 'local_fixture',
        'sample': true,
        'state': 'stale',
        'source_as_of': '2026-08-22T20:00:00Z',
        'fetched_at': '2026-08-24T03:00:00Z',
        'issues': const [
          {
            'code': 'sample_data',
            'message': 'market data is a local sample and not live',
          },
        ],
        'bars': [
          {
            'at': '2026-08-22T20:00:00Z',
            'open': '100',
            'high': '110',
            'low': '90',
            'close': '105',
            'volume': '1',
          },
          {
            'at': '2026-08-21T20:00:00Z',
            'open': '100',
            'high': '110',
            'low': '90',
            'close': '105',
            'volume': '1',
          },
        ],
      }),
      throwsFormatException,
    );
    expect(
      () => MarketBar.fromJson({
        'at': '2026-08-22T20:00:00Z',
        'open': '100',
        'high': '99',
        'low': '90',
        'close': '105',
        'volume': '-1',
      }),
      throwsFormatException,
    );
    expect(
      () => MarketBar.fromJson({
        'at': '2026-08-22T20:00:00Z',
        'open': '1',
        'high': '9007199254740992',
        'low': '1',
        'close': '9007199254740993',
        'volume': '1',
      }),
      throwsFormatException,
    );
    expect(
      () => MarketBar.fromJson({
        'at': '2026-08-22T20:00:00Z',
        'open': '1${List.filled(400, '0').join()}',
        'high': '1${List.filled(400, '0').join()}',
        'low': '1',
        'close': '1',
        'volume': '1',
      }),
      throwsFormatException,
    );
  });

  test('POST network failures use the stable connection message', () async {
    final api = RestOmniApi(
      client: MockClient((_) async => throw StateError('socket detail')),
    );

    await expectLater(
      api.preview('header\nrow'),
      throwsA(
        isA<ApiException>().having(
          (error) => error.message,
          'message',
          apiConnectionError,
        ),
      ),
    );
    await expectLater(
      api.apply('preview', 'key'),
      throwsA(
        isA<ApiException>().having(
          (error) => error.message,
          'message',
          apiConnectionError,
        ),
      ),
    );
    await expectLater(
      api.candles('AAPL'),
      throwsA(
        isA<ApiException>().having(
          (error) => error.message,
          'message',
          apiConnectionError,
        ),
      ),
    );
    expect(defaultApiUrl, 'http://127.0.0.1:8080');
  });

  test('candles use the fixed daily interval and strict parser', () async {
    late Uri request;
    final api = RestOmniApi(
      client: MockClient((value) async {
        request = value.url;
        return http.Response(
          jsonEncode({
            'symbol': 'AAPL & TEST',
            'venue': 'XNAS',
            'timezone': 'America/New_York',
            'interval': '1d',
            'price_adjustment': 'unspecified',
            'source': 'local_fixture',
            'sample': true,
            'state': 'stale',
            'source_as_of': '2026-08-22T20:00:00Z',
            'fetched_at': '2026-08-24T03:00:00Z',
            'issues': const [
              {
                'code': 'sample_data',
                'message': 'market data is a local sample and not live',
              },
            ],
            'bars': [
              {
                'at': '2026-08-22T20:00:00Z',
                'open': '100',
                'high': '110',
                'low': '90',
                'close': '105',
                'volume': '1',
              },
            ],
          }),
          200,
        );
      }),
    );
    final candles = await api.candles('AAPL & TEST');
    expect(request.path, '/v1/market-data/candles');
    expect(request.queryParameters, {
      'symbol': 'AAPL & TEST',
      'interval': '1d',
    });
    expect(candles.fetchedAt, '2026-08-24T03:00:00Z');
  });

  test('candles reject a response for another symbol', () async {
    final api = RestOmniApi(
      client: MockClient(
        (_) async => http.Response(
          jsonEncode({
            'symbol': 'MSFT',
            'venue': 'XNAS',
            'timezone': 'America/New_York',
            'interval': '1d',
            'price_adjustment': 'provider_adjusted',
            'source': 'provider_neutral_fixture',
            'sample': false,
            'state': 'success',
            'source_as_of': null,
            'fetched_at': '2026-08-24T03:00:00Z',
            'issues': const [],
            'bars': [
              {
                'at': '2026-08-22T20:00:00Z',
                'open': '100',
                'high': '110',
                'low': '90',
                'close': '105',
                'volume': '1',
              },
            ],
          }),
          200,
        ),
      ),
    );
    await expectLater(api.candles('AAPL'), throwsFormatException);
  });

  testWidgets('live-disabled trust banner remains explicit', (tester) async {
    final api = goldenApi(neverVerified: true);
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);

    expect(find.textContaining('아직 확인되지 않음'), findsOneWidget);
    await tester.tap(find.text('연결'));
    await pumpUi(tester);
    expect(find.textContaining('실전 주문 꺼짐'), findsOneWidget);
    expect(find.textContaining('로컬 거래 내역 가져오기'), findsOneWidget);
  });

  testWidgets('missing snapshot links directly to transaction import', (
    tester,
  ) async {
    final api = goldenApi()..failSnapshot = true;
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);

    await tester.tap(find.text('거래 내역 가져오기'));
    await pumpUi(tester);

    expect(find.text('거래 내역 가져오기'), findsOneWidget);
    expect(find.byType(TextField), findsOneWidget);
  });

  testWidgets('error offers retry and recovers', (tester) async {
    final api = goldenApi()..fail = true;
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    expect(find.text('스냅샷을 불러오지 못했습니다'), findsOneWidget);

    api.fail = false;
    await tester.tap(find.text('다시 시도'));
    await pumpUi(tester);
    expect(find.textContaining('데이터 상태: 로컬 기록 확인 완료'), findsOneWidget);
  });

  testWidgets('refresh failure keeps the last known snapshot visible', (
    tester,
  ) async {
    final api = goldenApi();
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    expect(find.text('USD 778'), findsOneWidget);

    api.fail = true;
    await tester.tap(find.byTooltip('데이터 새로고침'));
    await pumpUi(tester);

    expect(find.text('USD 778'), findsOneWidget);
    expect(find.textContaining('일부 데이터 확인 필요'), findsOneWidget);
    expect(find.textContaining('마지막 정상 스냅샷은 유지됩니다'), findsOneWidget);
  });

  testWidgets('trust status and controls expose accessible semantics', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    addTearDown(
      tester.binding.platformDispatcher.clearPlatformBrightnessTestValue,
    );
    try {
      await tester.pumpWidget(OmniFolioApp(api: goldenApi()));
      await pumpUi(tester);

      expect(
        find.semantics.byLabel(
          RegExp(r'데이터 상태: 로컬 기록 확인 완료.*마지막 확인', dotAll: true),
        ),
        findsOneWidget,
      );
      expect(
        tester.getSemantics(find.text('현금 잔액')),
        matchesSemantics(label: '현금 잔액', isHeader: true),
      );
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
      await expectLater(tester, meetsGuideline(iOSTapTargetGuideline));
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(textContrastGuideline));

      tester.binding.platformDispatcher.platformBrightnessTestValue =
          Brightness.dark;
      await tester.pumpAndSettle();
      expect(
        Theme.of(tester.element(find.byType(Scaffold))).brightness,
        Brightness.dark,
      );
      await expectLater(tester, meetsGuideline(textContrastGuideline));
    } finally {
      semantics.dispose();
    }
  });

  testWidgets('reduced motion reaches the final navigation state immediately', (
    tester,
  ) async {
    tester.binding.platformDispatcher.accessibilityFeaturesTestValue =
        const FakeAccessibilityFeatures(
          disableAnimations: true,
          reduceMotion: true,
        );
    addTearDown(
      tester.binding.platformDispatcher.clearAccessibilityFeaturesTestValue,
    );

    await tester.pumpWidget(OmniFolioApp(api: goldenApi()));
    expect(find.byIcon(Icons.hourglass_empty), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsNothing);
    await pumpUi(tester);
    expect(
      MediaQuery.disableAnimationsOf(tester.element(find.byType(Scaffold))),
      isTrue,
    );
    expect(
      tester
          .widget<NavigationBar>(find.byType(NavigationBar))
          .animationDuration,
      Duration.zero,
    );

    await tester.tap(find.text('내역'));
    await tester.pump();

    expect(find.text('거래 내역 가져오기'), findsOneWidget);
  });

  testWidgets('error state remains usable on a small screen at 200% text', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 480);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);

    await tester.pumpWidget(OmniFolioApp(api: goldenApi()..fail = true));
    await pumpUi(tester);

    expect(find.text('스냅샷을 불러오지 못했습니다'), findsOneWidget);
    expect(find.text('다시 시도'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('applicable preview displays atomic apply receipt', (
    tester,
  ) async {
    final api = goldenApi();
    api.applyFailures = 1;
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('내역'));
    await pumpUi(tester);
    await tester.enterText(find.byType(TextField), 'header\nrow');
    await tester.tap(find.text('미리보기 만들기'));
    await pumpUi(tester);
    expect(find.text('적용 가능: 이 미리보기만 원장에 원자적으로 반영됩니다.'), findsOneWidget);

    final apply = find.text('원자적으로 적용');
    await tester.drag(find.byType(ListView), const Offset(0, -500));
    await tester.pump();
    await tester.tap(apply);
    await pumpUi(tester);
    expect(find.textContaining('중복 반영되지 않습니다'), findsOneWidget);
    await tester.drag(find.byType(ListView), const Offset(0, -200));
    await tester.pump();
    await tester.tap(apply);
    await pumpUi(tester);
    expect(find.text('적용 확인'), findsOneWidget);
    expect(find.textContaining('receipt_golden_001'), findsOneWidget);
    expect(api.applyKeys, hasLength(2));
    expect(api.applyKeys, everyElement('import-${api.previewValue.previewId}'));
  });

  testWidgets('editing CSV invalidates an existing preview', (tester) async {
    final api = goldenApi();
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('내역'));
    await pumpUi(tester);
    await tester.enterText(find.byType(TextField), 'header\nrow');
    await tester.tap(find.text('미리보기 만들기'));
    await pumpUi(tester);
    expect(find.text('원자적으로 적용'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'header\nchanged-row');
    await tester.pump();
    expect(find.text('원자적으로 적용'), findsNothing);
    expect(find.textContaining('이전 미리보기는 무효'), findsOneWidget);
  });

  testWidgets(
    'holding opens stale sample chart with semantics and exact table',
    (tester) async {
      final semantics = tester.ensureSemantics();
      await tester.pumpWidget(OmniFolioApp(api: goldenApi()));
      await pumpUi(tester);
      await tester.tap(find.text('보유'));
      await pumpUi(tester);
      await tester.tap(find.text('AAPL'));
      await pumpUi(tester);

      expect(find.text('샘플 데이터 · 실시간 아님'), findsOneWidget);
      expect(find.textContaining('가격 기준 조정 여부 확인 안 됨'), findsOneWidget);
      expect(find.text('시세 차트'), findsOneWidget);
      expect(find.textContaining('오래된 시세입니다'), findsOneWidget);
      expect(
        find.semantics.byLabel(RegExp(r'AAPL 1d 캔들 차트.*정확한 OHLCV 표 보기')),
        findsOneWidget,
      );
      await tester.drag(find.byType(ListView).last, const Offset(0, -500));
      await tester.pump();
      await tester.tap(find.text('정확한 OHLCV 표 보기'));
      await tester.pump();
      expect(find.byKey(const Key('ohlcv-table-rows')), findsOneWidget);
      await tester.drag(
        find.byKey(const Key('asset-detail-scroll')),
        const Offset(0, -300),
      );
      await tester.pump();
      expect(find.text('2026-08-22T20:00:00Z'), findsOneWidget);
      expect(find.text('101'), findsOneWidget);
      expect(
        tester.getSemantics(find.byKey(const Key('ohlcv-header-at'))),
        matchesSemantics(label: '시각', isHeader: true),
      );
      expect(
        find.semantics.byLabel('행 2026-08-22T20:00:00Z, 종가 101'),
        findsOneWidget,
      );
      semantics.dispose();
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
    },
  );

  testWidgets(
    'asset detail handles empty partial and retained refresh failure',
    (tester) async {
      final semantics = tester.ensureSemantics();
      final api = goldenApi();
      api.candlesValue = marketCandles(
        state: 'partial',
        sample: false,
        priceAdjustment: 'provider_adjusted',
        source: 'kiwoom',
      );
      await tester.pumpWidget(OmniFolioApp(api: api));
      await pumpUi(tester);
      await tester.tap(find.text('보유'));
      await pumpUi(tester);
      await tester.tap(find.text('AAPL'));
      await pumpUi(tester);
      expect(find.textContaining('일부 시세입니다'), findsOneWidget);
      expect(find.textContaining('missing_session'), findsOneWidget);

      api.fail = true;
      await tester.tap(find.byTooltip('차트 새로고침'));
      await pumpUi(tester);
      expect(find.textContaining('마지막 정상 시세는 유지됩니다'), findsOneWidget);
      expect(
        find.semantics.byLabel(RegExp(r'새로고침 실패:.*마지막 정상 시세는 유지됩니다.')),
        findsOneWidget,
      );
      expect(
        tester.getSemantics(find.byKey(const Key('market-refresh-error'))),
        matchesSemantics(isLiveRegion: true),
      );
      semantics.dispose();
      await tester.drag(find.byType(ListView).last, const Offset(0, -500));
      await tester.pump();
      expect(find.text('시세 차트'), findsOneWidget);
    },
  );

  testWidgets('asset detail error offers retry', (tester) async {
    final api = goldenApi();
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('보유'));
    await pumpUi(tester);
    await tester.tap(find.text('AAPL'));
    await pumpUi(tester);
    expect(find.text('시세 차트'), findsOneWidget);

    await tester.pageBack();
    await tester.pumpAndSettle();
    api.fail = true;
    await tester.tap(find.text('AAPL'));
    await pumpUi(tester);
    expect(find.text('시세를 불러오지 못했습니다'), findsOneWidget);
    api.fail = false;
    await tester.tap(find.text('다시 시도'));
    await pumpUi(tester);
    expect(find.text('시세 차트'), findsOneWidget);
  });

  testWidgets(
    'asset detail keeps a visible loading state until candles arrive',
    (tester) async {
      final semantics = tester.ensureSemantics();
      final api = goldenApi()..candlesCompleter = Completer<MarketCandles>();
      await tester.pumpWidget(OmniFolioApp(api: api));
      await pumpUi(tester);
      await tester.tap(find.text('보유'));
      await pumpUi(tester);
      await tester.tap(find.text('AAPL'));
      await pumpUi(tester);
      expect(find.semantics.byLabel('데이터를 불러오는 중'), findsOneWidget);

      api.candlesCompleter!.complete(api.candlesValue);
      await pumpUi(tester);
      expect(find.text('시세 차트'), findsOneWidget);
      semantics.dispose();
    },
  );

  testWidgets('asset detail success table works at 200 percent text', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 480);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    final api = goldenApi()
      ..candlesValue = marketCandles(
        state: 'success',
        sample: false,
        priceAdjustment: 'provider_adjusted',
        source: 'kiwoom',
      );
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('보유'));
    await pumpUi(tester);
    await tester.tap(find.text('AAPL'));
    await pumpUi(tester);
    expect(find.textContaining('원천 kiwoom'), findsOneWidget);
    expect(find.textContaining('가격 기준 공급자 조정 가격'), findsOneWidget);
    await tester.dragUntilVisible(
      find.text('정확한 OHLCV 표 보기'),
      find.byKey(const Key('asset-detail-scroll')),
      const Offset(0, -200),
    );
    await tester.pump();
    await tester.tap(find.text('정확한 OHLCV 표 보기'));
    await tester.pump();
    expect(find.text('OHLCV 표'), findsOneWidget);
    expect(find.text('2026-08-22T20:00:00Z'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

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
    required this.orderLogValue,
    this.reconciliationValue,
    this.fail = false,
    this.failSnapshot = false,
    this.failReconciliation = false,
    this.failLocalOrders = false,
    this.applyFailures = 0,
    this.failureMessage = '서버를 다시 확인하세요.',
  });

  final ServiceStatus statusValue;
  final PortfolioSnapshot snapshotValue;
  final ImportPreview previewValue;
  final ApplyReceipt receiptValue;
  MarketCandles candlesValue;
  LocalOrderLog orderLogValue;
  BrokerReconciliation? reconciliationValue;
  Completer<BrokerReconciliation?>? reconciliationCompleter;
  int reconciliationCalls = 0;
  Completer<LocalOrderLog>? localOrdersCompleter;
  int localOrderCalls = 0;
  Completer<MarketCandles>? candlesCompleter;
  int candleCalls = 0;
  final List<String> applyKeys = [];
  bool fail;
  bool failSnapshot;
  bool failReconciliation;
  bool failLocalOrders;
  int applyFailures;
  String failureMessage;

  @override
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey) async {
    applyKeys.add(idempotencyKey);
    if (applyFailures > 0) {
      applyFailures -= 1;
      throw ApiException(failureMessage);
    }
    if (fail) throw ApiException(failureMessage);
    return receiptValue;
  }

  @override
  Future<ImportPreview> preview(String csv) async {
    if (fail) throw ApiException(failureMessage);
    return previewValue;
  }

  @override
  Future<PortfolioSnapshot> snapshot() async {
    if (fail || failSnapshot) {
      throw ApiException(failureMessage);
    }
    return snapshotValue;
  }

  @override
  Future<MarketCandles> candles(String symbol) async {
    candleCalls += 1;
    if (fail) throw ApiException(failureMessage);
    return candlesCompleter?.future ?? candlesValue;
  }

  @override
  Future<BrokerReconciliation?> latestBrokerReconciliation() async {
    reconciliationCalls += 1;
    if (fail || failReconciliation) {
      throw ApiException(failureMessage);
    }
    return reconciliationCompleter?.future ?? reconciliationValue;
  }

  @override
  Future<LocalOrderLog> localOrders() async {
    localOrderCalls += 1;
    if (fail || failLocalOrders) {
      throw const ApiException('kiwoom_account_secret');
    }
    return localOrdersCompleter?.future ?? orderLogValue;
  }

  @override
  Future<ServiceStatus> status() async {
    if (fail) throw ApiException(failureMessage);
    return statusValue;
  }
}

FakeApi goldenApi({
  bool neverVerified = false,
  ImportPreview? preview,
  PortfolioSnapshot? snapshot,
}) {
  final previewValue =
      preview ?? ImportPreview.fromJson(fixture('golden-preview.json'));
  final snapshotValue =
      snapshot ?? PortfolioSnapshot.fromJson(fixture('golden-snapshot.json'));
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
    snapshotValue: snapshotValue,
    previewValue: previewValue,
    receiptValue: receipt,
    candlesValue: marketCandles(),
    orderLogValue: LocalOrderLog.fromJson(localOrderLogJson(orders: const [])),
  );
}

MarketCandles marketCandles({
  String state = 'stale',
  bool sample = true,
  String priceAdjustment = 'unspecified',
  String source = 'local_fixture',
  String? sourceAsOf = '2026-08-22T20:00:00Z',
  bool includeIssues = true,
  List<Json>? bars,
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
      : bars ??
            [
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

Json brokerReconciliationJson() => {
  'provider': 'kiwoom',
  'environment': 'mock',
  'exchange': 'KRX',
  'freshness': 'unverified',
  'fetched_at': '2026-01-10T15:00:59Z',
  'recorded_at': '2026-01-10T15:01:00Z',
  'ledger_revision': 'rev_0000000002',
  'all_positions_match': false,
  'position_differences': const [
    {
      'symbol': '000660',
      'broker_quantity': '2',
      'ledger_quantity': '0',
      'difference': '2',
      'match': false,
    },
    {
      'symbol': '005930',
      'broker_quantity': '10',
      'ledger_quantity': '7',
      'difference': '3',
      'match': false,
    },
  ],
};

Json localOrderLogJson({List<Json>? orders}) => {
  'source': 'local_order_log',
  'broker_freshness': 'unverified',
  'orders':
      orders ??
      const [
        {
          'mode': 'synthetic',
          'symbol': '005930',
          'side': 'BUY',
          'order_type': 'LIMIT',
          'quantity': '2',
          'limit_price': '70000',
          'filled_quantity': '0',
          'currency': 'KRW',
          'status': 'SUBMIT_UNKNOWN',
          'pending_action': 'SUBMIT',
          'last_recorded_at': '2026-01-10T15:01:00Z',
        },
      ],
};

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

  test('cash void preview requires a closed correction target', () {
    expect(
      () => ImportPreview.fromJson({
        ...fixture('golden-preview.json'),
        'rows': [
          {
            'row_number': 2,
            'status': 'new',
            'transaction': {
              'event_id': 'event-cash-void-001',
              'source_event_id': 'cash-void-001',
              'account_id': 'account-main',
              'type': 'CASH_VOID',
              'occurred_at': '2026-01-03T00:00:00Z',
              'currency': 'USD',
              'amount': '5',
              'corrects_source_event_id': 'fee-001',
            },
          },
        ],
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

  test('broker reconciliation parser rejects unsafe or inconsistent data', () {
    final parsed = BrokerReconciliation.fromJson(brokerReconciliationJson());
    expect(parsed.positionDifferences.last.difference, '3');
    expect(parsed.allPositionsMatch, isFalse);
    expect(
      () => BrokerReconciliation.fromJson({
        ...brokerReconciliationJson(),
        'account_ref': 'kiwoom_account_secret',
      }),
      throwsFormatException,
    );
    expect(
      () => BrokerReconciliation.fromJson({
        ...brokerReconciliationJson(),
        'position_differences': [
          {
            ...((brokerReconciliationJson()['position_differences'] as List)
                    .first
                as Json),
            'broker_quantity': 2,
          },
        ],
      }),
      throwsFormatException,
    );
    expect(
      () => BrokerReconciliation.fromJson({
        ...brokerReconciliationJson(),
        'all_positions_match': true,
      }),
      throwsFormatException,
    );
  });

  test(
    'local order lifecycle parser rejects identifiers and invalid state',
    () {
      final parsed = LocalOrderLog.fromJson(localOrderLogJson());
      expect(parsed.orders.single.status, 'SUBMIT_UNKNOWN');
      expect(parsed.orders.single.pendingAction, 'SUBMIT');
      final resolved = LocalOrderLog.fromJson(
        localOrderLogJson(
          orders: [
            {
              ...(localOrderLogJson()['orders'] as List).single as Json,
              'status': 'FILLED',
              'filled_quantity': '2',
              'pending_action': 'none',
            },
          ],
        ),
      );
      expect(resolved.orders.single.pendingAction, 'none');
      expect(
        () => LocalOrderLog.fromJson({
          ...localOrderLogJson(),
          'account_ref': 'kiwoom_account_secret',
        }),
        throwsFormatException,
      );
      expect(
        () => LocalOrderLog.fromJson({
          ...localOrderLogJson(),
          'orders': [
            {
              ...(localOrderLogJson()['orders'] as List).single as Json,
              'status': 'SUCCEEDED',
            },
          ],
        }),
        throwsFormatException,
      );
      expect(
        () => LocalOrderLog.fromJson({
          ...localOrderLogJson(),
          'orders': [
            {
              ...(localOrderLogJson()['orders'] as List).single as Json,
              'pending_action': 'RETRY',
            },
          ],
        }),
        throwsFormatException,
      );
      expect(
        () => LocalOrderLog.fromJson({
          ...localOrderLogJson(),
          'orders': [
            {
              ...(localOrderLogJson()['orders'] as List).single as Json,
              'status': 'FILLED',
              'filled_quantity': '2',
              'pending_action': 'SUBMIT',
            },
          ],
        }),
        throwsFormatException,
      );
    },
  );

  test('local orders use the fixed read-only path', () async {
    late http.Request request;
    final api = RestOmniApi(
      client: MockClient((value) async {
        request = value;
        return http.Response(jsonEncode(localOrderLogJson()), 200);
      }),
    );
    final result = await api.localOrders();
    expect(request.method, 'GET');
    expect(request.url.path, '/v1/orders');
    expect(request.url.query, isEmpty);
    expect(result.orders.single.filledQuantity, '0');
  });

  test(
    'latest broker reconciliation uses the fixed path and maps 404 to empty',
    () async {
      late Uri request;
      final api = RestOmniApi(
        client: MockClient((value) async {
          request = value.url;
          return http.Response(jsonEncode(brokerReconciliationJson()), 200);
        }),
      );
      final result = await api.latestBrokerReconciliation();
      expect(request.path, '/v1/broker-reconciliation/latest');
      expect(request.query, isEmpty);
      expect(result!.ledgerRevision, 'rev_0000000002');

      final empty = RestOmniApi(
        client: MockClient(
          (_) async => http.Response(
            jsonEncode({
              'code': 'broker_reconciliation_not_found',
              'message': 'broker reconciliation was not found',
            }),
            404,
          ),
        ),
      );
      expect(await empty.latestBrokerReconciliation(), isNull);
    },
  );

  testWidgets('live-disabled trust banner remains explicit', (tester) async {
    final api = goldenApi(neverVerified: true);
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);

    expect(find.textContaining('아직 확인되지 않음'), findsOneWidget);
    expect(find.textContaining('증권사 잔고 대조 기록 없음'), findsOneWidget);
    await tester.tap(find.text('연결'));
    await pumpUi(tester);
    expect(find.textContaining('실전 주문 꺼짐'), findsOneWidget);
    expect(find.textContaining('로컬 거래 내역 가져오기'), findsOneWidget);
    expect(find.text('아직 증권사 대조 기록이 없습니다'), findsOneWidget);
  });

  testWidgets('first-run empty snapshot links to import at 200 percent text', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 640);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    final semantics = tester.ensureSemantics();
    try {
      final api = goldenApi(
        neverVerified: true,
        snapshot: PortfolioSnapshot.fromJson({
          ...fixture('golden-snapshot.json'),
          'ledger_revision': 'rev_0000000000',
          'cash': const [],
          'holdings': const [],
          'realized_pnl': const [],
          'provenance': {'event_ids': const [], 'receipt_ids': const []},
        }),
      );
      await tester.pumpWidget(OmniFolioApp(api: api));
      await pumpUi(tester);

      final import = find.widgetWithText(ElevatedButton, '거래 내역 가져오기');
      expect(import, findsOneWidget);
      expect(tester.getSize(import).height, greaterThanOrEqualTo(48));
      expect(find.semantics.byLabel('거래 내역 가져오기'), findsOneWidget);
      expect(tester.takeException(), isNull);

      await tester.ensureVisible(import);
      await tester.pump();
      await tester.tap(import);
      await pumpUi(tester);

      expect(find.byType(TextField), findsOneWidget);
      expect(tester.takeException(), isNull);
    } finally {
      semantics.dispose();
    }
  });

  testWidgets('verified empty snapshot remains a verified portfolio', (
    tester,
  ) async {
    final api = goldenApi(
      snapshot: PortfolioSnapshot.fromJson({
        ...fixture('golden-snapshot.json'),
        'cash': const [],
        'holdings': const [],
        'realized_pnl': const [],
        'provenance': {'event_ids': const [], 'receipt_ids': const []},
      }),
    );
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);

    expect(find.textContaining('로컬 기록 확인 완료'), findsOneWidget);
    expect(find.text('거래 내역 가져오기'), findsNothing);
  });

  testWidgets('unverified snapshot keeps any retained financial data visible', (
    tester,
  ) async {
    PortfolioSnapshot snapshot({
      List<Json> cash = const [],
      List<Json> holdings = const [],
      List<Json> realizedPnl = const [],
    }) => PortfolioSnapshot.fromJson({
      ...fixture('golden-snapshot.json'),
      'cash': cash,
      'holdings': holdings,
      'realized_pnl': realizedPnl,
      'provenance': {'event_ids': const [], 'receipt_ids': const []},
    });

    final cases = <(String, PortfolioSnapshot)>[
      (
        'cash',
        snapshot(
          cash: const [
            {'currency': 'USD', 'amount': '1'},
          ],
        ),
      ),
      (
        'holding',
        snapshot(
          holdings: const [
            {
              'instrument_id': 'instrument_aapl',
              'symbol': 'AAPL',
              'quantity': '1',
              'cost_basis': '1',
              'currency': 'USD',
            },
          ],
        ),
      ),
      (
        'realized PnL',
        snapshot(
          realizedPnl: const [
            {'currency': 'USD', 'amount': '1'},
          ],
        ),
      ),
    ];

    for (final (name, value) in cases) {
      await tester.pumpWidget(
        OmniFolioApp(
          key: ValueKey(name),
          api: goldenApi(neverVerified: true, snapshot: value),
        ),
      );
      await pumpUi(tester);

      expect(find.textContaining('아직 확인되지 않음'), findsOneWidget);
      expect(
        find.text('거래 내역 가져오기'),
        findsNothing,
        reason: '$name must not be hidden behind the first-run CTA',
      );
    }
  });

  testWidgets(
    'overview stored reconciliation stays usable at 200 percent text',
    (tester) async {
      tester.view.physicalSize = const Size(375, 812);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final semantics = tester.ensureSemantics();
      try {
        final api = goldenApi()
          ..reconciliationValue = BrokerReconciliation.fromJson(
            brokerReconciliationJson(),
          );
        await tester.pumpWidget(OmniFolioApp(api: api));
        await pumpUi(tester);
        expect(api.reconciliationCalls, 1);

        await tester.drag(find.byType(ListView), const Offset(0, -900));
        await tester.pumpAndSettle();
        expect(find.textContaining('마지막 저장 기록 · 현재 상태 아님'), findsOneWidget);
        expect(find.textContaining('2개 중 2개 불일치'), findsOneWidget);
        expect(
          find.semantics.byLabel(
            RegExp(r'증권사 잔고 대조.*2개 중 2개 불일치', dotAll: true),
          ),
          findsOneWidget,
        );

        final details = find.text('연결에서 자세히 보기');
        await tester.ensureVisible(details);
        await tester.pump();
        await tester.tap(details);
        await pumpUi(tester);
        expect(api.reconciliationCalls, 1);

        await tester.drag(find.byType(ListView), const Offset(0, -900));
        await tester.pumpAndSettle();
        expect(find.text('증권사 10 · 원장 7 · 차이 3'), findsOneWidget);
        expect(tester.takeException(), isNull);
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets(
    'overview local order risk summary stays usable at 200 percent text',
    (tester) async {
      tester.view.physicalSize = const Size(375, 812);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final semantics = tester.ensureSemantics();
      try {
        final api = goldenApi()
          ..orderLogValue = LocalOrderLog.fromJson(
            localOrderLogJson(
              orders: const [
                {
                  'mode': 'synthetic',
                  'symbol': '005930',
                  'side': 'BUY',
                  'order_type': 'LIMIT',
                  'quantity': '2',
                  'limit_price': '70000',
                  'filled_quantity': '1',
                  'currency': 'KRW',
                  'status': 'SUBMIT_UNKNOWN',
                  'pending_action': 'SUBMIT',
                  'last_recorded_at': '2026-01-10T15:01:00Z',
                },
                {
                  'mode': 'synthetic',
                  'symbol': '005930',
                  'side': 'SELL',
                  'order_type': 'LIMIT',
                  'quantity': '1',
                  'limit_price': '71000',
                  'filled_quantity': '0',
                  'currency': 'KRW',
                  'status': 'FILLED',
                  'pending_action': 'CANCEL',
                  'last_recorded_at': '2026-01-10T15:02:00Z',
                },
              ],
            ),
          );

        await tester.pumpWidget(OmniFolioApp(api: api));
        await pumpUi(tester);

        expect(api.localOrderCalls, 1);
        expect(find.text('로컬 주문 기록 · 현재 브로커 상태 아님'), findsOneWidget);
        expect(find.text('확인 필요한 주문 2건'), findsOneWidget);
        expect(find.text('접수 미확정 1건 · 취소 미확정 1건'), findsOneWidget);
        expect(find.textContaining('재주문·추가 조작 금지'), findsOneWidget);
        expect(find.textContaining('마지막 로컬 기록'), findsOneWidget);
        expect(
          find.semantics.byLabel(
            RegExp(r'로컬 주문 기록.*현재 브로커 상태 아님.*확인 필요한 주문 2건', dotAll: true),
          ),
          findsOneWidget,
        );

        final details = find.text('주문 기록 자세히 보기');
        await tester.ensureVisible(details);
        await tester.pump();
        await tester.tap(details);
        await pumpUi(tester);
        expect(api.localOrderCalls, 1);
        await tester.drag(find.byType(ListView), const Offset(0, -1200));
        await tester.pumpAndSettle();
        expect(find.text('브로커 결과 미확정 · 재주문 금지'), findsOneWidget);
        expect(find.text('체결됨 · 취소 결과 확인 중'), findsOneWidget);
        expect(tester.takeException(), isNull);
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets(
    'overview hides an empty local history instead of claiming safety',
    (tester) async {
      await tester.pumpWidget(OmniFolioApp(api: goldenApi()));
      await pumpUi(tester);

      expect(find.text('로컬 기록에는 미확정 주문이 없습니다'), findsNothing);
      expect(find.text('로컬 주문 기록 · 현재 브로커 상태 아님'), findsNothing);
    },
  );

  testWidgets(
    'slow local orders do not block the portfolio or duplicate refreshes',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final pending = Completer<LocalOrderLog>();
      final api = goldenApi()..localOrdersCompleter = pending;

      await tester.pumpWidget(OmniFolioApp(api: api));
      await pumpUi(tester);

      expect(find.byType(ListView), findsOneWidget);
      expect(find.textContaining('데이터 상태: 로컬 기록 확인 완료'), findsOneWidget);
      await tester.drag(find.byType(ListView), const Offset(0, -500));
      await tester.pump();
      expect(find.text('USD 778'), findsOneWidget);
      expect(api.localOrderCalls, 1);

      await tester.tap(find.byTooltip('데이터 새로고침'));
      await pumpUi(tester);
      expect(api.localOrderCalls, 1);

      pending.complete(LocalOrderLog.fromJson(localOrderLogJson()));
      await pumpUi(tester);
      expect(api.localOrderCalls, 1);
      expect(find.textContaining('확인 필요한 주문 1건'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'slow reconciliation does not block the portfolio at 200 percent text',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final pending = Completer<BrokerReconciliation?>();
      final api = goldenApi()..reconciliationCompleter = pending;

      await tester.pumpWidget(OmniFolioApp(api: api));
      await pumpUi(tester);

      expect(find.byType(ListView), findsOneWidget);
      expect(find.textContaining('데이터 상태: 로컬 기록 확인 완료'), findsOneWidget);
      expect(find.textContaining('증권사 잔고 대조 확인 중'), findsOneWidget);
      await tester.drag(find.byType(ListView), const Offset(0, -500));
      await tester.pump();
      expect(find.text('USD 778'), findsOneWidget);
      expect(api.reconciliationCalls, 1);
      final refresh = find.byTooltip('데이터 새로고침');
      expect(
        tester.widget<IconButton>(find.byType(IconButton)).onPressed,
        isNotNull,
      );
      await tester.tap(refresh);
      await pumpUi(tester);
      expect(api.reconciliationCalls, 1);

      pending.complete(
        BrokerReconciliation.fromJson(brokerReconciliationJson()),
      );
      await pumpUi(tester);
      expect(api.reconciliationCalls, 1);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('pending reconciliation may complete after app disposal', (
    tester,
  ) async {
    final pending = Completer<BrokerReconciliation?>();
    final api = goldenApi()..reconciliationCompleter = pending;
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);

    await tester.pumpWidget(const SizedBox.shrink());
    pending.complete(null);
    await tester.pump();

    expect(tester.takeException(), isNull);
  });

  testWidgets('overview distinguishes no positions from a successful match', (
    tester,
  ) async {
    final api = goldenApi()
      ..reconciliationValue = BrokerReconciliation.fromJson({
        ...brokerReconciliationJson(),
        'all_positions_match': true,
        'position_differences': const [],
      });
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.drag(find.byType(ListView), const Offset(0, -900));
    await tester.pumpAndSettle();

    expect(find.text('대조할 보유 종목 없음'), findsOneWidget);
    expect(find.textContaining('0개 모두 일치'), findsNothing);
  });

  testWidgets(
    'connections show sanitized stored reconciliation with text semantics',
    (tester) async {
      final semantics = tester.ensureSemantics();
      try {
        final api = goldenApi()
          ..reconciliationValue = BrokerReconciliation.fromJson(
            brokerReconciliationJson(),
          );
        await tester.pumpWidget(OmniFolioApp(api: api));
        await pumpUi(tester);
        await tester.tap(find.text('연결'));
        await pumpUi(tester);

        expect(find.textContaining('마지막 저장 스냅샷 · 현재 상태 아님'), findsOneWidget);
        expect(find.textContaining('불일치 · 2/2종목'), findsOneWidget);
        expect(find.text('증권사 10 · 원장 7 · 차이 3'), findsOneWidget);
        expect(
          find.semantics.byLabel('005930 불일치, 증권사 수량 10, 원장 수량 7, 차이 3'),
          findsOneWidget,
        );
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets('reconciliation error offers a retry', (tester) async {
    final api = goldenApi()
      ..failReconciliation = true
      ..reconciliationValue = BrokerReconciliation.fromJson(
        brokerReconciliationJson(),
      );
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    expect(find.textContaining('증권사 잔고 대조 확인 실패'), findsOneWidget);
    await tester.tap(find.text('연결'));
    await pumpUi(tester);

    expect(find.text('대조 결과를 불러오지 못했습니다'), findsOneWidget);
    api.failReconciliation = false;
    await tester.tap(find.text('대조 결과 다시 불러오기'));
    await pumpUi(tester);
    expect(find.textContaining('불일치 · 2/2종목'), findsOneWidget);
  });

  testWidgets('reconciliation refresh failure retains the stored result', (
    tester,
  ) async {
    final api = goldenApi()
      ..reconciliationValue = BrokerReconciliation.fromJson(
        brokerReconciliationJson(),
      );
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('연결'));
    await pumpUi(tester);

    final pending = Completer<BrokerReconciliation?>();
    api.reconciliationCompleter = pending;
    final refresh = find.text('저장된 대조 다시 불러오기');
    await tester.ensureVisible(refresh);
    await tester.pump();
    await tester.tap(refresh);
    await tester.pump();

    expect(find.text('증권사 10 · 원장 7 · 차이 3'), findsOneWidget);
    expect(find.textContaining('저장된 대조를 다시 불러오는 중'), findsOneWidget);

    pending.completeError(const ApiException('서버를 다시 확인하세요.'));
    await pumpUi(tester);

    expect(find.text('증권사 10 · 원장 7 · 차이 3'), findsOneWidget);
    expect(find.textContaining('마지막 정상 대조 기록은 유지됩니다'), findsOneWidget);

    await tester.tap(find.text('홈'));
    await pumpUi(tester);
    await tester.drag(find.byType(ListView), const Offset(0, -900));
    await tester.pumpAndSettle();
    expect(find.textContaining('2개 중 2개 불일치'), findsOneWidget);
    expect(find.textContaining('마지막 정상 대조 기록을 유지합니다'), findsOneWidget);
  });

  testWidgets(
    'submit unknown forbids resubmit at 200 percent text with semantics',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final semantics = tester.ensureSemantics();
      try {
        final api = goldenApi()
          ..orderLogValue = LocalOrderLog.fromJson(localOrderLogJson());
        await tester.pumpWidget(OmniFolioApp(api: api));
        await pumpUi(tester);
        await tester.tap(find.text('연결'));
        await pumpUi(tester);
        await tester.scrollUntilVisible(
          find.textContaining('같은 주문을 다시 보내면 안 됩니다'),
          200,
        );
        await tester.drag(find.byType(ListView), const Offset(0, -500));
        await tester.pumpAndSettle();

        expect(find.text('브로커 결과 미확정 · 재주문 금지'), findsOneWidget);
        expect(find.textContaining('같은 주문을 다시 보내면 안 됩니다'), findsOneWidget);
        expect(find.textContaining('현재 브로커 상태가 아닙니다'), findsOneWidget);
        expect(find.textContaining('주문 보내기'), findsNothing);
        expect(find.textContaining('주문 취소'), findsNothing);
        expect(
          find.semantics.byLabel(
            '합성 테스트 주문, 005930 매수 지정가, 주문 수량 2, 체결 수량 0, KRW 70000, 브로커 결과 미확정, 재주문 금지',
          ),
          findsOneWidget,
        );
        expect(tester.takeException(), isNull);
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets(
    'filled order with pending cancel warns against additional action',
    (tester) async {
      final semantics = tester.ensureSemantics();
      try {
        final api = goldenApi()
          ..orderLogValue = LocalOrderLog.fromJson(
            localOrderLogJson(
              orders: const [
                {
                  'mode': 'synthetic',
                  'symbol': '005930',
                  'side': 'BUY',
                  'order_type': 'LIMIT',
                  'quantity': '10',
                  'limit_price': '70000',
                  'filled_quantity': '10',
                  'currency': 'KRW',
                  'status': 'FILLED',
                  'pending_action': 'CANCEL',
                  'last_recorded_at': '2026-01-10T15:02:00Z',
                },
              ],
            ),
          );
        await tester.pumpWidget(OmniFolioApp(api: api));
        await pumpUi(tester);
        await tester.tap(find.text('연결'));
        await pumpUi(tester);
        expect(find.text('체결됨 · 취소 결과 확인 중'), findsOneWidget);
        await tester.scrollUntilVisible(
          find.textContaining('취소 결과를 아직 확인하지 못했습니다'),
          200,
        );

        expect(find.text('체결됨 · 취소 결과 확인 중'), findsOneWidget);
        expect(find.textContaining('추가 조작 금지'), findsOneWidget);
        expect(find.textContaining('주문 취소'), findsNothing);
        expect(
          find.semantics.byLabel(
            '합성 테스트 주문, 005930 매수 지정가, 주문 수량 10, 체결 수량 10, KRW 70000, 체결됨, 취소 결과 확인 중, 추가 조작 금지',
          ),
          findsOneWidget,
        );
        expect(tester.takeException(), isNull);
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets('local order refresh failure retains sanitized stored result', (
    tester,
  ) async {
    final api = goldenApi()
      ..orderLogValue = LocalOrderLog.fromJson(localOrderLogJson());
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('연결'));
    await pumpUi(tester);

    api.failLocalOrders = true;
    final refresh = find.text('로컬 주문 기록 다시 불러오기');
    await tester.ensureVisible(refresh);
    await tester.pump();
    await tester.tap(refresh);
    await pumpUi(tester);

    expect(find.text('브로커 결과 미확정 · 재주문 금지'), findsOneWidget);
    expect(find.textContaining('마지막 정상 주문 기록은 유지됩니다'), findsOneWidget);
    expect(find.textContaining('kiwoom_account_secret'), findsNothing);
  });

  testWidgets(
    'empty local order refresh failure remains visible and retryable',
    (tester) async {
      final api = goldenApi();
      await tester.pumpWidget(OmniFolioApp(api: api));
      await pumpUi(tester);
      await tester.tap(find.text('연결'));
      await pumpUi(tester);

      api.failLocalOrders = true;
      final refresh = find.text('로컬 주문 기록 다시 불러오기');
      await tester.ensureVisible(refresh);
      await tester.pump();
      await tester.tap(refresh);
      await pumpUi(tester);

      expect(find.textContaining('마지막 정상 주문 기록은 유지됩니다'), findsOneWidget);
      expect(find.text('로컬 주문 기록 다시 불러오기'), findsOneWidget);
      expect(find.textContaining('kiwoom_account_secret'), findsNothing);
    },
  );

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

  testWidgets('missing snapshot still surfaces unresolved local orders', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 640);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    final api = goldenApi()
      ..failSnapshot = true
      ..orderLogValue = LocalOrderLog.fromJson(
        localOrderLogJson(
          orders: const [
            {
              'mode': 'paper',
              'symbol': '005930',
              'side': 'BUY',
              'order_type': 'LIMIT',
              'quantity': '1',
              'limit_price': '71000',
              'filled_quantity': '0',
              'currency': 'KRW',
              'status': 'SUBMIT_UNKNOWN',
              'pending_action': 'SUBMIT',
              'last_recorded_at': '2026-01-10T15:01:00Z',
            },
          ],
        ),
      );
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);

    expect(find.text('아직 스냅샷이 없습니다'), findsOneWidget);
    await tester.drag(find.byType(ListView), const Offset(0, -700));
    await tester.pumpAndSettle();
    expect(find.text('확인 필요한 주문 1건'), findsOneWidget);
    expect(find.textContaining('재주문·추가 조작 금지'), findsOneWidget);
    expect(tester.takeException(), isNull);
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

  testWidgets('overview never renders upstream error details', (tester) async {
    const secret = 'kiwoom_account_1234 provider_trace_5678';
    final api = goldenApi()
      ..failureMessage = secret
      ..fail = true;

    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);

    expect(find.textContaining(secret), findsNothing);
    expect(find.textContaining('데이터를 불러오지 못했습니다.'), findsOneWidget);
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

  testWidgets(
    'cash void preview preserves the original disclosure at 200 percent text',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final semantics = tester.ensureSemantics();

      final preview = ImportPreview.fromJson({
        ...fixture('golden-preview.json'),
        'mapping_version': 'canonical-transaction.v4',
        'rows': [
          {
            'row_number': 2,
            'status': 'new',
            'transaction': {
              'event_id': 'event-cash-void-001',
              'source_event_id': 'cash-void-001',
              'account_id': 'account-main',
              'type': 'CASH_VOID',
              'occurred_at': '2026-01-03T00:00:00Z',
              'currency': 'USD',
              'amount': '5',
              'corrects_source_event_id': 'fee-001',
            },
            'correction_target': {
              'source_event_id': 'fee-001',
              'type': 'FEE',
              'currency': 'USD',
              'amount': '-5',
            },
          },
        ],
        'totals': {
          'total_rows': 1,
          'new_rows': 1,
          'duplicate_rows': 0,
          'error_rows': 0,
          'unresolved_rows': 0,
        },
      });
      await tester.pumpWidget(OmniFolioApp(api: goldenApi(preview: preview)));
      await pumpUi(tester);
      await tester.tap(find.text('내역'));
      await pumpUi(tester);
      await tester.enterText(find.byType(TextField), 'header\nrow');
      await tester.ensureVisible(find.text('미리보기 만들기'));
      await tester.pump();
      await tester.tap(find.text('미리보기 만들기'));
      await pumpUi(tester);

      expect(find.text('원본 기록은 보존되고 반대 금액으로 상쇄됩니다.'), findsOneWidget);
      expect(find.text('원본 FEE · USD -5 · source fee-001'), findsOneWidget);
      expect(find.text('정정 CASH_VOID · USD 5'), findsOneWidget);
      await tester.ensureVisible(find.text('원본 기록은 보존되고 반대 금액으로 상쇄됩니다.'));
      await tester.pump();
      expect(
        find.semantics.byLabel(
          '정정 행 2, 원본 FEE, USD -5, source fee-001, 반전 USD 5',
        ),
        findsOneWidget,
      );
      expect(tester.takeException(), isNull);
      semantics.dispose();
    },
  );

  test('FX preview requires valid opposite cash legs', () {
    Map<String, dynamic> previewWith(Map<String, dynamic> transaction) => {
      ...fixture('golden-preview.json'),
      'mapping_version': 'canonical-transaction.v4',
      'rows': [
        {'row_number': 2, 'status': 'new', 'transaction': transaction},
      ],
      'totals': {
        'total_rows': 1,
        'new_rows': 1,
        'duplicate_rows': 0,
        'error_rows': 0,
        'unresolved_rows': 0,
      },
    };
    final validTransaction = <String, dynamic>{
      'event_id': 'event-fx-001',
      'source_event_id': 'fx-001',
      'account_id': 'account-main',
      'type': 'FX_EXCHANGE',
      'occurred_at': '2026-01-03T00:00:00Z',
      'currency': 'USD',
      'amount': '-100',
      'counter_currency': 'KRW',
      'counter_amount': '137000',
    };
    final complete = ImportPreview.fromJson(previewWith(validTransaction));
    expect(complete.rows.single.counterCurrency, 'KRW');
    expect(complete.rows.single.counterAmount, '137000');

    final invalidChanges = <Map<String, dynamic>>[
      {'counter_amount': null},
      {'amount': null},
      {'amount': '100'},
      {'counter_amount': '0'},
      {'counter_amount': '-137000'},
      {'counter_currency': 'USD'},
      {'type': 'DEPOSIT'},
    ];
    for (final changes in invalidChanges) {
      final invalid = {...validTransaction, ...changes};
      invalid.removeWhere((_, value) => value == null);
      expect(
        () => ImportPreview.fromJson(previewWith(invalid)),
        throwsFormatException,
        reason: 'accepted invalid FX fields: $changes',
      );
    }
  });

  testWidgets('FX preview discloses both cash legs at 200 percent text', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 640);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    final semantics = tester.ensureSemantics();
    final preview = ImportPreview.fromJson({
      ...fixture('golden-preview.json'),
      'mapping_version': 'canonical-transaction.v4',
      'rows': [
        {
          'row_number': 2,
          'status': 'new',
          'transaction': {
            'event_id': 'event-fx-001',
            'source_event_id': 'fx-001',
            'account_id': 'account-main',
            'type': 'FX_EXCHANGE',
            'occurred_at': '2026-01-03T00:00:00Z',
            'currency': 'USD',
            'amount': '-100',
            'counter_currency': 'KRW',
            'counter_amount': '137000',
          },
        },
      ],
      'totals': {
        'total_rows': 1,
        'new_rows': 1,
        'duplicate_rows': 0,
        'error_rows': 0,
        'unresolved_rows': 0,
      },
    });
    await tester.pumpWidget(OmniFolioApp(api: goldenApi(preview: preview)));
    await pumpUi(tester);
    await tester.tap(find.text('내역'));
    await pumpUi(tester);
    await tester.enterText(find.byType(TextField), 'header\nrow');
    await tester.ensureVisible(find.text('미리보기 만들기'));
    await tester.pump();
    await tester.tap(find.text('미리보기 만들기'));
    await pumpUi(tester);

    expect(find.text('환전 · USD 100 매도 → KRW 137000 매수'), findsOneWidget);
    expect(find.text('환율을 계산하거나 현재 시세를 뜻하지 않습니다.'), findsOneWidget);
    await tester.ensureVisible(find.text('환전 · USD 100 매도 → KRW 137000 매수'));
    await tester.pump();
    expect(
      find.semantics.byLabel('환전 행 2, USD 100 매도, KRW 137000 매수'),
      findsOneWidget,
    );
    expect(tester.takeException(), isNull);
    semantics.dispose();
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

  testWidgets('import operations never render upstream error details', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    const secret = 'account-main provider-request-42';
    final api = goldenApi()..failureMessage = secret;
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('내역'));
    await pumpUi(tester);
    await tester.enterText(find.byType(TextField), 'header\nrow');

    api.fail = true;
    await tester.tap(find.text('미리보기 만들기'));
    await pumpUi(tester);
    expect(find.textContaining(secret), findsNothing);
    expect(find.textContaining('미리보기를 완료하지 못했습니다.'), findsOneWidget);
    expect(
      tester.getSemantics(find.byKey(const Key('import-error'))),
      matchesSemantics(isLiveRegion: true),
    );

    api.fail = false;
    await tester.tap(find.text('미리보기 만들기'));
    await pumpUi(tester);
    api.applyFailures = 1;
    await tester.drag(find.byType(ListView), const Offset(0, -500));
    await tester.pump();
    await tester.tap(find.text('원자적으로 적용'));
    await pumpUi(tester);
    expect(find.textContaining(secret), findsNothing);
    expect(find.textContaining('거래 내역을 적용하지 못했습니다.'), findsOneWidget);
    semantics.dispose();
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
      await tester.fling(
        find.byType(ListView).last,
        const Offset(0, -900),
        2000,
      );
      await tester.pumpAndSettle();
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

  testWidgets('chart range selection updates chart and exact table together', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(375, 900);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    final semantics = tester.ensureSemantics();
    final api = goldenApi()
      ..candlesValue = marketCandles(
        bars: const [
          {
            'at': '2025-08-21T20:00:00Z',
            'open': '95',
            'high': '101',
            'low': '94',
            'close': '100',
            'volume': '800',
          },
          {
            'at': '2026-05-23T20:00:00Z',
            'open': '100',
            'high': '103',
            'low': '98',
            'close': '102',
            'volume': '900',
          },
          {
            'at': '2026-05-24T20:00:00Z',
            'open': '102',
            'high': '106',
            'low': '101',
            'close': '104',
            'volume': '1000',
          },
          {
            'at': '2026-07-23T20:00:00Z',
            'open': '104',
            'high': '108',
            'low': '103',
            'close': '107',
            'volume': '1100',
          },
          {
            'at': '2026-08-01T20:00:00Z',
            'open': '107',
            'high': '110',
            'low': '105',
            'close': '108',
            'volume': '1200',
          },
          {
            'at': '2026-08-22T20:00:00Z',
            'open': '108',
            'high': '112',
            'low': '106',
            'close': '111',
            'volume': '1300',
          },
        ],
      );

    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('보유'));
    await pumpUi(tester);
    await tester.tap(find.text('AAPL'));
    await pumpUi(tester);
    await tester.fling(
      find.byKey(const Key('asset-detail-scroll')),
      const Offset(0, -1200),
      2000,
    );
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('chart-range-30d')), findsOneWidget);
    expect(
      find.semantics.byLabel(RegExp(r'AAPL 1d 캔들 차트.*6개 봉')),
      findsOneWidget,
    );
    await tester.tap(find.byKey(const Key('chart-range-365d')));
    await tester.pump();
    expect(
      find.semantics.byLabel(RegExp(r'AAPL 1d 캔들 차트.*5개 봉')),
      findsOneWidget,
    );
    await tester.tap(find.byKey(const Key('chart-range-90d')));
    await tester.pump();
    expect(
      find.semantics.byLabel(RegExp(r'AAPL 1d 캔들 차트.*4개 봉')),
      findsOneWidget,
    );
    await tester.dragUntilVisible(
      find.byKey(const Key('chart-range-30d')),
      find.byKey(const Key('asset-detail-scroll')),
      const Offset(0, -200),
    );
    await tester.tap(find.byKey(const Key('chart-range-30d')));
    await tester.pump();

    expect(
      find.semantics.byLabel(
        RegExp(
          r'AAPL 1d 캔들 차트.*3개 봉.*2026-07-23T20:00:00Z부터 2026-08-22T20:00:00Z까지',
        ),
      ),
      findsOneWidget,
    );
    expect(find.textContaining('최근 30일 · 2026-07-23'), findsOneWidget);
    expect(
      tester.getSemantics(find.byKey(const Key('chart-range-30d'))),
      matchesSemantics(
        label: '최근 30일',
        isButton: true,
        hasSelectedState: true,
        isSelected: true,
        hasTapAction: true,
      ),
    );
    await tester.dragUntilVisible(
      find.text('정확한 OHLCV 표 보기'),
      find.byKey(const Key('asset-detail-scroll')),
      const Offset(0, -200),
    );
    await tester.tap(find.text('정확한 OHLCV 표 보기'));
    await tester.pump();
    expect(find.text('2026-05-24T20:00:00Z'), findsNothing);
    expect(find.text('2026-07-23T20:00:00Z'), findsOneWidget);
    expect(api.candleCalls, 1);
    expect(tester.takeException(), isNull);
    await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
    semantics.dispose();
  });

  testWidgets('finite chart range preserves a valid empty refresh state', (
    tester,
  ) async {
    final api = goldenApi();
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('보유'));
    await pumpUi(tester);
    await tester.tap(find.text('AAPL'));
    await pumpUi(tester);
    await tester.fling(
      find.byKey(const Key('asset-detail-scroll')),
      const Offset(0, -900),
      2000,
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('chart-range-30d')));
    await tester.pump();

    api.candlesValue = marketCandles(
      state: 'empty',
      sample: false,
      priceAdjustment: 'provider_adjusted',
      source: 'kiwoom',
      sourceAsOf: null,
      includeIssues: false,
    );
    await tester.tap(find.byTooltip('차트 새로고침'));
    await pumpUi(tester);

    expect(find.text('표시할 봉이 없습니다'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

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

  testWidgets('asset detail never renders upstream error details', (
    tester,
  ) async {
    const secret = 'kiwoom_account_1234 raw_provider_message';
    final pending = Completer<MarketCandles>();
    final api = goldenApi()
      ..failureMessage = secret
      ..candlesCompleter = pending;
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('보유'));
    await pumpUi(tester);
    await tester.tap(find.text('AAPL'));
    await tester.pump();

    pending.completeError(ApiException(secret));
    await pumpUi(tester);

    expect(find.textContaining(secret), findsNothing);
    expect(find.textContaining('시세를 불러오지 못했습니다.'), findsOneWidget);
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

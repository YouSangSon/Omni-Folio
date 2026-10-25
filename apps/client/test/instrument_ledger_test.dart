import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/instrument_ledger_page.dart';

const _holding = Holding(
  instrumentId: 'instrument_aapl',
  symbol: 'AAPL',
  quantity: '999',
  costBasis: '999',
  currency: 'USD',
);

Json _trade({
  String type = 'BUY',
  String quantity = '3',
  String price = '10',
  String fee = '1',
  String amount = '-31',
  String at = '2026-01-10T10:00:00.000000001Z',
}) => {
  'type': type,
  'occurred_at': at,
  'recorded_at': '2026-01-11T13:00:00Z',
  'symbol': 'AAPL',
  'quantity': quantity,
  'price': price,
  'fee': fee,
  'currency': 'USD',
  'amount': amount,
  'counter_currency': null,
  'counter_amount': null,
  'is_correction': false,
};

Json _view() => {
  'instrument_id': 'instrument_aapl',
  'symbol': 'AAPL',
  'currency': 'USD',
  'source': 'local_ledger',
  'broker_freshness': 'unverified',
  'ledger_revision': 'rev_0000000001',
  'ledger_as_of': '2026-01-10T10:00:00.000000001Z',
  'ledger_recorded_at': '2026-01-11T13:00:00Z',
  'cost_basis_policy': 'fifo_utc_nanosecond_exact_else_half_even_residual_8_v2',
  'average_cost_policy': 'remaining_fifo_cost_per_unit_half_even_8_v1',
  'quantity': '3',
  'cost_basis': '31',
  'average_cost': '10.33333333',
  'trade_limit': 50,
  'has_more': false,
  'trades': [_trade()],
};

class _Api implements OmniApi {
  @override
  Future<PortfolioBenchmarks> portfolioBenchmarks() async =>
      throw UnimplementedError();

  @override
  Future<PortfolioTwr> portfolioTwr() async => throw UnimplementedError();

  @override
  Future<PortfolioXirr> portfolioXirr() =>
      throw UnsupportedError('XIRR is loaded only from its detail page.');

  Future<InstrumentLedger> Function()? load;
  int calls = 0;
  @override
  Future<InstrumentLedger> instrumentLedger(
    String instrumentId,
    String currency,
  ) async {
    calls++;
    return load?.call() ?? InstrumentLedger.fromJson(_view());
  }

  @override
  Future<MarketCandles> candles(String symbol) =>
      Future.error(const ApiException('private source failure'));
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  test('instrument ledger validates authority, bounds, money and nanos', () {
    final view = InstrumentLedger.fromJson(_view());
    expect(view.averageCost, '10.33333333');
    expect(view.trades.single.occurredAt, endsWith('.000000001Z'));
    final zero = _view()
      ..addAll({
        'quantity': '1',
        'cost_basis': '0.000000005',
        'average_cost': '0',
      });
    expect(InstrumentLedger.fromJson(zero).averageCost, '0');
    final closed = _view()
      ..addAll({'quantity': '0', 'cost_basis': '0', 'average_cost': null});
    expect(InstrumentLedger.fromJson(closed).averageCost, isNull);
    for (final change in <Json>[
      {'source': 'broker'},
      {'broker_freshness': 'live'},
      {'average_cost_policy': 'unknown'},
      {'cost_basis_policy': 'legacy'},
      {'quantity': '-1'},
      {'quantity': '0'},
      {'average_cost': null},
      {'average_cost': '-1'},
      {'average_cost': '1.000000001'},
      {'currency': 'usd'},
      {'ledger_revision': 'latest'},
      {'trade_limit': 51},
      {'has_more': true},
      {'trades': <Json>[]},
      {'account_id': 'private'},
      {'ledger_recorded_at': '2026-01-01T00:00:00+00:00'},
      {
        'trades': [_trade()..['currency'] = 'KRW'],
      },
      {
        'trades': [_trade()..['symbol'] = ''],
      },
      {
        'trades': [_trade(at: '2026-01-10T10:00:00Z'), _trade()],
      },
    ]) {
      expect(
        () => InstrumentLedger.fromJson(_view()..addAll(change)),
        throwsFormatException,
        reason: '$change',
      );
    }
  });

  test('event clocks and display labels do not define instrument identity', () {
    final body = _view()
      ..['ledger_recorded_at'] = '2026-01-01T00:00:00Z'
      ..['trades'] = [_trade()..['symbol'] = 'aapl'];
    final view = InstrumentLedger.fromJson(body);
    expect(view.trades.single.recordedAt, '2026-01-11T13:00:00Z');
    expect(view.trades.single.symbol, 'aapl');
    expect(view.instrumentId, 'instrument_aapl');
    final opaque = 'instrument_ A ${List.filled(130, 'B').join()}';
    expect(
      InstrumentLedger.fromJson(
        _view()..['instrument_id'] = opaque,
      ).instrumentId,
      opaque,
    );
  });

  test(
    'REST uses exact identity query and rejects mismatched responses',
    () async {
      var body = _view();
      final client = MockClient((request) async {
        expect(request.url.path, '/v1/portfolio/instrument-ledger');
        expect(request.url.queryParameters, {
          'instrument_id': 'instrument_aapl',
          'currency': 'USD',
        });
        return http.Response(jsonEncode(body), 200);
      });
      addTearDown(client.close);
      final api = RestOmniApi(client: client);
      expect(
        (await api.instrumentLedger('instrument_aapl', 'USD')).costBasis,
        '31',
      );
      body = _view()..['instrument_id'] = 'instrument_other';
      await expectLater(
        api.instrumentLedger('instrument_aapl', 'USD'),
        throwsFormatException,
      );
    },
  );

  testWidgets('ledger detail remains reachable when candles fail', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: AssetDetailPage(
          api: _Api()
            ..load = () async =>
                InstrumentLedger.fromJson(_view()..['symbol'] = 'aapl'),
          holding: _holding,
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byTooltip('원장 평균단가·매매 보기'), findsOneWidget);
    await tester.tap(find.byTooltip('원장 평균단가·매매 보기'));
    await tester.pumpAndSettle();
    expect(find.text('현재 잔여 평균원가'), findsOneWidget);
    expect(find.textContaining('10.33333333'), findsOneWidget);
    expect(find.textContaining('999'), findsNothing);
  });

  testWidgets(
    'refresh keeps one observation, rejects mismatch and then shows closed holding',
    (tester) async {
      final api = _Api();
      await tester.pumpWidget(
        MaterialApp(
          home: InstrumentLedgerPage(api: api, holding: _holding),
        ),
      );
      await tester.pumpAndSettle();
      final pending = Completer<InstrumentLedger>();
      api.load = () => pending.future;
      await tester.tap(find.byTooltip('종목 원장 새로고침'));
      await tester.pump();
      expect(api.calls, 2);
      expect(
        tester
            .widget<IconButton>(
              find.widgetWithIcon(IconButton, Icons.refresh_rounded),
            )
            .onPressed,
        isNull,
      );
      expect(find.textContaining('10.33333333'), findsOneWidget);
      pending.completeError(const ApiException('account-private-secret'));
      await tester.pumpAndSettle();
      expect(find.textContaining('이전 원장 결과와 기준시점'), findsOneWidget);
      expect(find.textContaining('account-private-secret'), findsNothing);
      await tester.tap(find.text('원장 기준과 표시 방법'));
      await tester.pumpAndSettle();
      expect(find.textContaining('rev_0000000001'), findsOneWidget);
      api.load = () async => InstrumentLedger.fromJson(
        _view()..['instrument_id'] = 'instrument_other',
      );
      await tester.tap(find.byTooltip('종목 원장 새로고침'));
      await tester.pumpAndSettle();
      expect(find.textContaining('10.33333333'), findsOneWidget);
      api.load = () async => InstrumentLedger.fromJson(
        _view()
          ..addAll({'quantity': '0', 'cost_basis': '0', 'average_cost': null}),
      );
      await tester.tap(find.byTooltip('종목 원장 새로고침'));
      await tester.pumpAndSettle();
      expect(find.text('현재 보유 수량 없음'), findsOneWidget);
      expect(find.textContaining('10.33333333'), findsNothing);
    },
  );

  testWidgets('ledger details have an independent accessible button', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: InstrumentLedgerPage(api: _Api(), holding: _holding),
      ),
    );
    await tester.pumpAndSettle();
    final details = tester.getSemantics(find.text('원장 기준과 표시 방법'));
    expect(details.label, '원장 기준과 표시 방법');
    expect(details.flagsCollection.isButton, isTrue);
    expect(details.flagsCollection.isHeader, isFalse);
    await tester.tap(find.text('원장 기준과 표시 방법'));
    await tester.pumpAndSettle();
    expect(find.textContaining('rev_0000000001'), findsOneWidget);
  });

  testWidgets('initial failure retries and disposal ignores pending load', (
    tester,
  ) async {
    final api = _Api()
      ..load = () => Future.error(const ApiException('private'));
    await tester.pumpWidget(
      MaterialApp(
        home: InstrumentLedgerPage(api: api, holding: _holding),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('다시 시도'), findsOneWidget);
    final pending = Completer<InstrumentLedger>();
    api.load = () => pending.future;
    await tester.tap(find.text('다시 시도'));
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    pending.complete(InstrumentLedger.fromJson(_view()));
    await tester.pump();
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'small scaled dark and light layouts retain trades and disclosure',
    (tester) async {
      tester.view.physicalSize = const Size(320, 480);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final body = _view()
        ..addAll({
          'has_more': true,
          'trades': List.generate(
            50,
            (index) =>
                index == 0 ? _trade() : _trade(quantity: '1', amount: '-11'),
          ),
        });
      final api = _Api()..load = () async => InstrumentLedger.fromJson(body);
      for (final brightness in Brightness.values) {
        await tester.pumpWidget(
          MaterialApp(
            key: ValueKey(brightness),
            theme: ThemeData(brightness: brightness),
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context).copyWith(
                textScaler: const TextScaler.linear(2),
                disableAnimations: true,
              ),
              child: child!,
            ),
            home: InstrumentLedgerPage(api: api, holding: _holding),
          ),
        );
        await tester.pumpAndSettle();
        final scrollable = find.descendant(
          of: find.byKey(const Key('instrument-ledger-list')),
          matching: find.byType(Scrollable),
        );
        await tester.scrollUntilVisible(
          find.textContaining('최근 50건만 표시'),
          300,
          scrollable: scrollable,
        );
        expect(find.textContaining('전체 기록은 내역 탭'), findsOneWidget);
        await tester.scrollUntilVisible(
          find.textContaining('매수 · 3주'),
          300,
          scrollable: scrollable,
        );
        expect(
          find.semantics.byLabel(
            RegExp(r'매수 · 3주.*거래 가격 USD 10', dotAll: true),
          ),
          findsWidgets,
        );
        expect(tester.takeException(), isNull);
      }
    },
  );
}

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/portfolio_twr_page.dart';
import 'package:omni_folio_client/portfolio_xirr_page.dart';

import 'vertical_slice_test.dart' show FakeApi, fixture, goldenApi, pumpUi;

Json _report() => fixture('golden-portfolio-twr.json');
Json _state(String name) =>
    (fixture('golden-portfolio-twr-states.json')['reports'] as Json)[name]
        as Json;
FakeApi _api(Json report) => goldenApi()
  ..twrValue = PortfolioTwr.fromJson(report)
  ..xirrValue = PortfolioXirr.fromJson(fixture('golden-portfolio-xirr.json'));
Future<void> _open(WidgetTester tester, FakeApi api) async {
  await tester.pumpWidget(OmniFolioApp(api: api));
  await pumpUi(tester);
  expect(api.twrCalls, 0);
  expect(api.xirrCalls, 0);
  final entry = find.text('입출금을 반영한 수익률 보기');
  await tester.scrollUntilVisible(
    entry.hitTestable(),
    200,
    scrollable: find
        .descendant(
          of: find.byType(OverviewPage),
          matching: find.byType(Scrollable),
        )
        .first,
  );
  await tester.pump();
  await tester.tap(entry);
  await tester.pumpAndSettle();
  expect(api.twrCalls, 0);
  final twr = find.text('입출금 영향을 뺀 누적 수익률 보기');
  await tester.ensureVisible(twr);
  await tester.pumpAndSettle();
  await tester.tap(twr);
  await tester.pumpAndSettle();
}

Future<void> _direct(WidgetTester tester, FakeApi api) async {
  await tester.pumpWidget(MaterialApp(home: PortfolioTwrPage(api: api)));
  await tester.pumpAndSettle();
}

void main() {
  test('shared actual response has cumulative rate and known cash', () {
    final r = PortfolioTwr.fromJson(_report());
    expect(r.cumulativeRate, '0.21');
    expect(r.netContributions!.amount, '200');
    expect(r.boundaryCount, 2);
  });
  for (final name in [
    'empty',
    'valuation_unavailable',
    'historical_fx_unavailable',
    'zero_return',
    'total_loss',
    'full_withdrawal',
  ]) {
    test('shared actual state $name is accepted', () {
      final r = PortfolioTwr.fromJson(_state(name));
      expect(r.periodEnd, r.valuation.valuationAsOf);
    });
  }
  final invalid = <String, void Function(Json)>{
    'extra authority field': (j) => j['live_enabled'] = true,
    'missing nullable key': (j) => j.remove('net_contributions'),
    'unknown policy': (j) => j['policy_version'] = 'daily',
    'numeric money': (j) => (j['net_contributions'] as Json)['amount'] = 200,
    'wrong base currency': (j) =>
        (j['net_contributions'] as Json)['currency'] = 'USD',
    'noncanonical decimal': (j) => j['cumulative_rate'] = '0.210',
    'too precise': (j) => j['cumulative_rate'] = '0.1234567890123',
    'below total loss': (j) => j['cumulative_rate'] = '-1.1',
    'unknown count': (j) => j['external_event_count'] = null,
    'invalid boundaries': (j) => j['boundary_count'] = 3,
    'start after cutoff by nanosecond': (j) =>
        j['period_start'] = '2026-01-01T00:00:00.000000001Z',
    'different report clock': (j) => j['period_end'] = '2026-01-01T00:00:00.1Z',
    'sample flag lost': (j) => j['sample'] = true,
    'unavailable cannot carry rate': (j) {
      j['status'] = 'unavailable';
      j['issues'] = [
        {'code': 'capital_path_unavailable', 'message': 'private'},
      ];
    },
    'unknown issue': (j) => j['issues'] = [
      {'code': 'new_unsafe_issue', 'message': 'private'},
    ],
    'private field on issue': (j) => j['issues'] = [
      {'code': 'sample_data', 'message': 'private', 'field': 'private-id'},
    ],
  };
  for (final entry in invalid.entries) {
    test('TWR rejects ${entry.key}', () {
      final j = _report();
      entry.value(j);
      expect(() => PortfolioTwr.fromJson(j), throwsFormatException);
    });
  }
  test('unknown inputs cannot be fabricated as zero', () {
    final j = _state('valuation_unavailable')
      ..['external_event_count'] = 0
      ..['boundary_count'] = 0;
    expect(() => PortfolioTwr.fromJson(j), throwsFormatException);
  });
  for (final cause in ['cash_flow_limit', 'no_external_cash_flows']) {
    test('unavailable reason $cause must agree with known counts', () {
      final j = _state('full_withdrawal');
      j['issues'] = [
        {'code': cause, 'message': 'private'},
      ];
      expect(() => PortfolioTwr.fromJson(j), throwsFormatException);
    });
  }
  test('calculation input failure requires an external boundary', () {
    final j = _state('full_withdrawal')
      ..['period_start'] = null
      ..['external_event_count'] = 0
      ..['boundary_count'] = 0
      ..['issues'] = [
        {'code': 'missing_historical_fx', 'message': 'private'},
      ];
    expect(() => PortfolioTwr.fromJson(j), throwsFormatException);
  });
  test('unavailable has one primary reason', () {
    final j = _state('full_withdrawal');
    (j['issues'] as List).add({
      'code': 'missing_historical_fx',
      'message': 'private',
    });
    expect(() => PortfolioTwr.fromJson(j), throwsFormatException);
  });
  test('actual boundary cap is an accepted unavailable reason', () {
    final j = _state('full_withdrawal')
      ..['external_event_count'] = 1001
      ..['boundary_count'] = 1001
      ..['issues'] = [
        {'code': 'cash_flow_limit', 'message': 'private'},
      ];
    expect(PortfolioTwr.fromJson(j).status, 'unavailable');
  });
  test('TWR uses only its read endpoint', () async {
    final api = RestOmniApi(
      baseUrl: 'http://localhost',
      client: MockClient((r) async {
        expect(r.method, 'GET');
        expect(r.url.path, '/v1/portfolio/twr');
        expect(r.url.query, isEmpty);
        return http.Response(jsonEncode(_report()), 200);
      }),
    );
    expect((await api.portfolioTwr()).cumulativeRate, '0.21');
  });
  testWidgets('home to XIRR to TWR is on demand and back preserves XIRR', (
    tester,
  ) async {
    final api = _api(_report());
    await _open(tester, api);
    expect(api.twrCalls, 1);
    expect(find.text('약 21%'), findsOneWidget);
    expect(find.text('누적 수익률 (TWR)'), findsOneWidget);
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(find.byType(PortfolioXirrPage), findsOneWidget);
    expect(find.text('약 10%'), findsOneWidget);
    expect(api.xirrCalls, 1);
  });
  testWidgets('TWR actions have labels and accessible tap targets', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    try {
      await _open(tester, _api(_report()));
      expect(
        tester.getSemantics(find.byKey(const Key('portfolio-twr-rate'))).label,
        contains('21%'),
      );
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(iOSTapTargetGuideline));
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
    } finally {
      semantics.dispose();
    }
  });
  testWidgets(
    'pending request is single flight and failed refresh retains whole report',
    (tester) async {
      final api = _api(_report());
      await _direct(tester, api);
      final pending = Completer<PortfolioTwr>();
      api.twrCompleter = pending;
      final refresh = find.byTooltip('TWR 새로고침');
      await tester.tap(refresh);
      await tester.pump();
      final button = find.ancestor(
        of: refresh,
        matching: find.byType(IconButton),
      );
      expect(tester.widget<IconButton>(button).onPressed, isNull);
      expect(api.twrCalls, 2);
      expect(find.text('약 21%'), findsOneWidget);
      pending.completeError(const ApiException('private-upstream-sentinel'));
      await tester.pumpAndSettle();
      expect(find.textContaining('이전 결과와 기준 시점을 유지'), findsOneWidget);
      expect(find.textContaining('private-upstream-sentinel'), findsNothing);
      expect(find.text('약 21%'), findsOneWidget);
      expect(find.textContaining('2026-01-01T00:00:00Z'), findsOneWidget);
    },
  );
  testWidgets('initial error retries and hides raw details', (tester) async {
    final api = _api(_report())..failTwr = true;
    await _direct(tester, api);
    expect(find.text('TWR을 불러오지 못했습니다'), findsOneWidget);
    expect(find.textContaining('private-twr'), findsNothing);
    api.failTwr = false;
    final retry = find.widgetWithText(ElevatedButton, '다시 시도');
    expect(tester.getSize(retry).height, greaterThanOrEqualTo(48));
    await tester.tap(retry);
    await tester.pumpAndSettle();
    expect(find.text('약 21%'), findsOneWidget);
  });
  testWidgets('disposal detaches pending error', (tester) async {
    final gate = Completer<PortfolioTwr>();
    final api = _api(_report())..twrCompleter = gate;
    await _direct(tester, api);
    await tester.pumpWidget(const SizedBox());
    gate.completeError(const ApiException('private-disposed'));
    await pumpUi(tester);
    expect(tester.takeException(), isNull);
  });
  for (final name in [
    'empty',
    'historical_fx_unavailable',
    'zero_return',
    'total_loss',
    'full_withdrawal',
  ]) {
    testWidgets('UI keeps $name distinct', (tester) async {
      await _direct(tester, _api(_state(name)));
      if (name == 'zero_return') {
        expect(find.text('약 0%'), findsOneWidget);
      } else if (name == 'total_loss') {
        expect(find.text('약 -100%'), findsOneWidget);
      } else {
        expect(find.byKey(const Key('portfolio-twr-rate')), findsNothing);
      }
      if (name == 'historical_fx_unavailable') {
        expect(find.textContaining('예시 가격·환율 포함'), findsOneWidget);
        expect(find.text('입출금 경계의 직접 환율이 없습니다.'), findsOneWidget);
      }
      if (name == 'full_withdrawal') {
        expect(find.textContaining('전액 출금을 손실로 표시하지 않습니다'), findsOneWidget);
      }
      expect(tester.takeException(), isNull);
    });
  }
  for (final brightness in Brightness.values) {
    for (final size in [const Size(375, 812), const Size(812, 375)]) {
      testWidgets('TWR $brightness $size at200percent reduced motion', (
        tester,
      ) async {
        tester.view.physicalSize = size;
        tester.view.devicePixelRatio = 1;
        tester.platformDispatcher.textScaleFactorTestValue = 2;
        tester.platformDispatcher.platformBrightnessTestValue = brightness;
        tester.platformDispatcher.accessibilityFeaturesTestValue =
            FakeAccessibilityFeatures(disableAnimations: true);
        addTearDown(() {
          tester.view.resetPhysicalSize();
          tester.view.resetDevicePixelRatio();
          tester.platformDispatcher.clearTextScaleFactorTestValue();
          tester.platformDispatcher.clearPlatformBrightnessTestValue();
          tester.platformDispatcher.clearAccessibilityFeaturesTestValue();
        });
        await _open(tester, _api(_state('historical_fx_unavailable')));
        final details = find.text('TWR 계산 방법과 기준 보기');
        await tester.ensureVisible(details);
        await tester.pumpAndSettle();
        await tester.tap(details);
        await tester.pumpAndSettle();
        await tester.ensureVisible(find.textContaining('전액 출금은 전액 손실이 아닙니다.'));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
      });
    }
  }
}

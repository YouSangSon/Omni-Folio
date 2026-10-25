import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/portfolio_benchmarks_page.dart';
import 'package:omni_folio_client/portfolio_twr_page.dart';

import 'vertical_slice_test.dart' show FakeApi, fixture, goldenApi, pumpUi;

Json _report() => fixture('golden-portfolio-benchmarks.json');
Json _state(String name) =>
    (fixture('golden-portfolio-benchmark-states.json')['reports'] as Json)[name]
        as Json;
Json _usd(Json json) =>
    (json['benchmarks'] as List).firstWhere(
          (row) => row['series']['currency'] == 'USD',
        )
        as Json;
Json _krw(Json json) =>
    (json['benchmarks'] as List).firstWhere(
          (row) => row['series']['currency'] == 'KRW',
        )
        as Json;
FakeApi _api(Json json) => goldenApi()
  ..benchmarksValue = PortfolioBenchmarks.fromJson(json)
  ..twrValue = PortfolioTwr.fromJson(fixture('golden-portfolio-twr.json'))
  ..xirrValue = PortfolioXirr.fromJson(fixture('golden-portfolio-xirr.json'));
Future<void> _direct(WidgetTester tester, FakeApi api) async {
  await tester.pumpWidget(MaterialApp(home: PortfolioBenchmarksPage(api: api)));
  await tester.pumpAndSettle();
}

Future<void> _open(WidgetTester tester, FakeApi api) async {
  await tester.pumpWidget(OmniFolioApp(api: api));
  await pumpUi(tester);
  expect(api.benchmarksCalls, 0);
  final xirr = find.text('입출금을 반영한 수익률 보기');
  await tester.scrollUntilVisible(
    xirr.hitTestable(),
    150,
    scrollable: find
        .ancestor(of: xirr, matching: find.byType(Scrollable))
        .first,
  );
  await tester.tap(xirr);
  await tester.pumpAndSettle();
  final twr = find.text('입출금 영향을 뺀 누적 수익률 보기');
  await tester.ensureVisible(twr);
  await tester.tap(twr);
  await tester.pumpAndSettle();
  expect(api.benchmarksCalls, 0);
  final comparison = find.text('같은 기간의 지수와 비교하기');
  await tester.ensureVisible(comparison);
  await tester.tap(comparison);
  await tester.pumpAndSettle();
}

String? _text(WidgetTester tester, String key) =>
    tester.widget<Text>(find.byKey(Key(key))).data;

void main() {
  test(
    'shared producer response keeps native KRW and percentage points distinct',
    () {
      final r = PortfolioBenchmarks.fromJson(_report());
      expect(r.portfolio.cumulativeRate, '0.21');
      expect(r.portfolio.sample, isFalse);
      expect(r.sample, isTrue);
      final usd = r.benchmarks.singleWhere(
        (row) => row.series.currency == 'USD',
      );
      expect(usd.native.rate, '-0.1');
      expect(usd.krw.rate, '-0.19');
      expect(usd.differencePercentagePoints, '40');
      expect(usd.series.returnBasis, 'price_index');
      expect(usd.startFx!.value, '1000');
    },
  );
  for (final name
      in (fixture('golden-portfolio-benchmark-states.json')['reports'] as Json)
          .keys) {
    test('shared comparison state $name is accepted', () {
      PortfolioBenchmarks.fromJson(_state(name));
    });
  }
  final invalid = <String, void Function(Json)>{
    'unknown envelope': (r) => r['extra'] = true,
    'unknown row': (r) => _usd(r)['extra'] = true,
    'unknown mark': (r) => _usd(r)['start']['extra'] = true,
    'unknown policy': (r) => r['policy_version'] = 'future-policy',
    'floating age': (r) => r['max_observation_age_seconds'] = 86400.0,
    'duplicate index': (r) =>
        (r['benchmarks'] as List).add(jsonDecode(jsonEncode(_krw(r)))),
    'market authority': (r) =>
        _usd(r)['series']['authority'] = 'provider_verified',
    'basis': (r) => _usd(r)['series']['return_basis'] = 'adjusted_close',
    'currency': (r) => _usd(r)['series']['currency'] = 'EUR',
    'bad hash': (r) => _usd(r)['series']['data_sha256'] = 'not-a-hash',
    'sample loss': (r) => _usd(r)['sample'] = false,
    'KRW double FX': (r) => _krw(r)['start_fx'] = {
      'observed_at': '2025-01-01T00:00:00Z',
      'rate': '1',
    },
    'difference scaled twice': (r) =>
        _usd(r)['difference_percentage_points'] = '4000',
    'difference scale': (r) =>
        _usd(r)['difference_percentage_points'] = '40.00000000001',
    'missing calculated rate': (r) => _usd(r)['native']['rate'] = null,
    'unavailable zero': (r) => _usd(r)['native'] = {
      'status': 'unavailable',
      'rate': '0',
      'issue': 'numerical_limit',
    },
    'native FX failure': (r) => _usd(r)['native'] = {
      'status': 'unavailable',
      'rate': null,
      'issue': 'missing_historical_fx',
    },
    'future selected FX': (r) =>
        _usd(r)['end_fx']['observed_at'] = '2026-01-01T00:00:00.000000001Z',
    'missing FX with KRW value': (r) => _usd(r)['start_fx'] = null,
    'zero divisor': (r) => _usd(r)['start']['level'] = '0',
    'stale index certified': (r) {
      _usd(r)['series']['first_observed_at'] = '2024-12-30T23:59:59.999999999Z';
      _usd(r)['start']['observed_at'] = '2024-12-30T23:59:59.999999999Z';
    },
    'future record': (r) =>
        _usd(r)['series']['recorded_at'] = '2026-01-01T00:00:00.000000001Z',
    'rate precision': (r) => _usd(r)['native']['rate'] = '0.1000000000001',
    'incompatible TWR': (r) =>
        r['portfolio'] = _state('portfolio_empty')['portfolio'],
    'hidden list failure': (r) => r['issues'] = [
      {'code': 'no_benchmarks', 'message': 'missing'},
    ],
    'private query source': (r) => _usd(r)['series']['source_reference'] =
        'https://example.invalid/index?token=secret',
    'identity byte cap': (r) =>
        _usd(r)['series']['benchmark_id'] = List.filled(43, '한').join(),
    'unpaired scalar': (r) =>
        _usd(r)['series']['label'] = String.fromCharCode(0xd800),
  };
  for (final entry in invalid.entries) {
    test('rejects inconsistent benchmark ${entry.key}', () {
      final raw = _report();
      entry.value(raw);
      expect(() => PortfolioBenchmarks.fromJson(raw), throwsFormatException);
    });
  }
  test(
    'selected stale fixture FX remains sample even on failed conversion',
    () {
      final raw = _state('stale_fx');
      final row = _usd(raw);
      row['series']['sample'] = false;
      final r = PortfolioBenchmarks.fromJson(raw);
      expect(r.sample, isTrue);
      expect(r.benchmarks.single.krw.rate, isNull);
      expect(r.benchmarks.single.native.rate, '-0.1');
      row['sample'] = false;
      expect(() => PortfolioBenchmarks.fromJson(raw), throwsFormatException);
    },
  );
  test('difference supports132 chars without another percent conversion', () {
    final raw = _report();
    final row = _usd(raw);
    raw['benchmarks'] = [row];
    raw['portfolio']['cumulative_rate'] = '-1';
    for (final field in ['cash_value', 'total_value']) {
      raw['portfolio']['valuation'][field]['amount'] = '0';
    }
    row['native']['rate'] = row['krw']['rate'] = List.filled(128, '9').join();
    final difference = '-1${List.filled(130, '0').join()}';
    row['difference_percentage_points'] = difference;
    expect(
      PortfolioBenchmarks.fromJson(
        raw,
      ).benchmarks.single.differencePercentagePoints,
      difference,
    );
    row['difference_percentage_points'] = '${difference}0';
    expect(() => PortfolioBenchmarks.fromJson(raw), throwsFormatException);
  });
  test('comparison has one query-free GET', () async {
    final api = RestOmniApi(
      baseUrl: 'http://localhost',
      client: MockClient((request) async {
        expect(request.method, 'GET');
        expect(request.url.path, '/v1/portfolio/benchmarks');
        expect(request.url.hasQuery, isFalse);
        return http.Response(jsonEncode(_report()), 200);
      }),
    );
    expect((await api.portfolioBenchmarks()).benchmarks.length, 2);
  });
  test('list is bounded before accepting33 unique IDs', () {
    final raw = _report();
    final prototype = _krw(raw);
    final rows = <Json>[];
    for (var n = 0; n < 32; n++) {
      final row = jsonDecode(jsonEncode(prototype)) as Json;
      row['series']['benchmark_id'] = 'index-$n';
      rows.add(row);
    }
    raw['benchmarks'] = rows;
    expect(PortfolioBenchmarks.fromJson(raw).benchmarks.length, 32);
    final last = jsonDecode(jsonEncode(prototype)) as Json;
    last['series']['benchmark_id'] = 'index-32';
    rows.add(last);
    expect(() => PortfolioBenchmarks.fromJson(raw), throwsFormatException);
  });
  testWidgets('comparison is on demand and back preserves TWR', (tester) async {
    final api = _api(_report());
    await _open(tester, api);
    expect(api.benchmarksCalls, 1);
    expect(find.text('같은 기간 지수 비교'), findsOneWidget);
    expect(_text(tester, 'benchmark-difference-synthetic_usd'), '+40%p');
    expect(_text(tester, 'benchmark-native-synthetic_usd'), '약 -10%');
    expect(_text(tester, 'benchmark-krw-synthetic_usd'), '약 -19%');
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(find.byType(PortfolioTwrPage), findsOneWidget);
    expect(api.twrCalls, 1);
    expect(find.text('약 21%'), findsOneWidget);
  });
  testWidgets(
    'pending refresh is single flight and preserves the entire report on failure',
    (tester) async {
      final api = _api(_report());
      await _direct(tester, api);
      final pending = Completer<PortfolioBenchmarks>();
      api.benchmarksCompleter = pending;
      final refresh = find.byTooltip('지수 비교 새로고침');
      await tester.tap(refresh);
      await tester.pump();
      final button = find.ancestor(
        of: refresh,
        matching: find.byType(IconButton),
      );
      expect(tester.widget<IconButton>(button).onPressed, isNull);
      expect(api.benchmarksCalls, 2);
      final originalEnd = _text(tester, 'benchmark-period-end');
      expect(_text(tester, 'benchmark-difference-synthetic_usd'), '+40%p');
      pending.completeError(const FormatException('private-response-sentinel'));
      await tester.pumpAndSettle();
      expect(find.textContaining('이전 비교 전체와 기준 시점을 유지'), findsOneWidget);
      expect(find.textContaining('private-response-sentinel'), findsNothing);
      expect(_text(tester, 'benchmark-period-end'), originalEnd);
      expect(_text(tester, 'benchmark-difference-synthetic_usd'), '+40%p');
      expect(_text(tester, 'benchmark-native-synthetic_krw'), '약 10%');
      api.benchmarksCompleter = null;
      await tester.tap(refresh);
      await tester.pumpAndSettle();
      expect(api.benchmarksCalls, 3);
      expect(find.textContaining('이전 비교 전체와 기준 시점을 유지'), findsNothing);
    },
  );
  testWidgets('initial failure retries without exposing raw errors', (
    tester,
  ) async {
    final api = _api(_report())..failBenchmarks = true;
    await _direct(tester, api);
    expect(find.text('지수 비교를 불러오지 못했습니다'), findsOneWidget);
    expect(find.textContaining('private-benchmark-sentinel'), findsNothing);
    api.failBenchmarks = false;
    await tester.tap(find.text('다시 시도'));
    await tester.pumpAndSettle();
    expect(api.benchmarksCalls, 2);
    expect(_text(tester, 'benchmark-difference-synthetic_usd'), '+40%p');
  });
  testWidgets('late response after dispose is ignored', (tester) async {
    final pending = Completer<PortfolioBenchmarks>();
    final api = _api(_report())..benchmarksCompleter = pending;
    await tester.pumpWidget(
      MaterialApp(home: PortfolioBenchmarksPage(api: api)),
    );
    await tester.pump();
    expect(find.text('같은 기간의 지수 수익률을 확인하고 있습니다'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    pending.complete(PortfolioBenchmarks.fromJson(_report()));
    await tester.pump();
    expect(tester.takeException(), isNull);
  });
  for (final name in [
    'no_benchmarks',
    'no_known_benchmarks',
    'benchmark_limit',
    'missing_fx',
    'portfolio_empty',
    'portfolio_unavailable',
    'zero_start',
    'zero_end',
    'zero_return',
    'native_limit',
  ]) {
    testWidgets('UI keeps $name distinct', (tester) async {
      await _direct(tester, _api(_state(name)));
      if (name == 'no_benchmarks') {
        expect(
          _text(tester, 'benchmark-list-issue'),
          contains('아직 보관한 비교 지수가 없습니다'),
        );
      }
      if (name == 'no_known_benchmarks') {
        expect(_text(tester, 'benchmark-list-issue'), contains('현재 보고서 기준 시점'));
      }
      if (name == 'benchmark_limit') {
        expect(_text(tester, 'benchmark-list-issue'), contains('32개를 넘었습니다'));
      }
      if (name == 'zero_start') {
        expect(
          _text(tester, 'benchmark-native-synthetic_krw'),
          contains('시작 지수 값이 0'),
        );
      }
      if (name == 'zero_end') {
        expect(_text(tester, 'benchmark-native-synthetic_krw'), '약 -100%');
      }
      if (name == 'zero_return') {
        expect(_text(tester, 'benchmark-native-synthetic_krw'), '약 0%');
      }
      if (name == 'missing_fx') {
        expect(_text(tester, 'benchmark-native-synthetic_usd'), '약 -10%');
        expect(
          _text(tester, 'benchmark-krw-synthetic_usd'),
          contains('직접 USD→KRW 환율이 없습니다'),
        );
        expect(_text(tester, 'benchmark-difference-synthetic_usd'), '계산 불가');
      }
      if (name == 'native_limit') {
        expect(
          _text(tester, 'benchmark-native-synthetic_usd'),
          contains('표시 범위'),
        );
        expect(_text(tester, 'benchmark-krw-synthetic_usd'), '약 0%');
        expect(_text(tester, 'benchmark-difference-synthetic_usd'), '+21%p');
      }
      if (name == 'portfolio_empty' || name == 'portfolio_unavailable') {
        expect(find.byKey(const Key('benchmark-portfolio-rate')), findsNothing);
      }
      expect(tester.takeException(), isNull);
    });
  }
  testWidgets('actions and data have accessible labels and tap targets', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    try {
      await _open(tester, _api(_report()));
      expect(
        tester
            .getSemantics(find.byKey(const Key('benchmark-portfolio-rate')))
            .label,
        contains('21%'),
      );
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(iOSTapTargetGuideline));
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
    } finally {
      semantics.dispose();
    }
  });
  for (final brightness in Brightness.values) {
    for (final size in [const Size(375, 812), const Size(812, 375)]) {
      testWidgets('comparison $brightness $size at200percent reduced motion', (
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
        await _open(tester, _api(_state('missing_fx')));
        final details = find.textContaining('자료 기준과 관측 시각').last;
        await tester.ensureVisible(details);
        await tester.pumpAndSettle();
        await tester.tap(details);
        await tester.pumpAndSettle();
        await tester.ensureVisible(find.textContaining('자료 출처:').last);
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
      });
    }
  }
}

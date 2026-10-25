import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/portfolio_xirr_page.dart';
import 'package:omni_folio_client/research_report.dart' show researchPercent;

import 'vertical_slice_test.dart' show FakeApi, fixture, goldenApi, pumpUi;

Json _report() => fixture('golden-portfolio-xirr.json');
Json _issue(String code) => {
  'code': code,
  'message': 'private-upstream-sentinel',
};

Json _candidate() {
  final j = _report();
  j.addAll({
    'status': 'candidate',
    'annual_rate': null,
    'candidate_rate': '0.1',
    'sign_changes': 2,
    'uniqueness': 'not_proven',
    'external_event_count': 3,
    'issues': [_issue('uniqueness_not_proven')],
  });
  return j;
}

FakeApi _api(Json json) =>
    goldenApi()..xirrValue = PortfolioXirr.fromJson(json);

Future<void> _open(WidgetTester tester, FakeApi api) async {
  await tester.pumpWidget(OmniFolioApp(api: api));
  await pumpUi(tester);
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
}

Future<void> _show(WidgetTester tester, Finder target) async {
  await tester.ensureVisible(target);
  await tester.pumpAndSettle();
}

void main() {
  test('unique rate and nonconventional candidate remain different', () {
    final unique = PortfolioXirr.fromJson(_report());
    expect(unique.annualRate, '0.1');
    expect(unique.candidateRate, isNull);
    final candidate = PortfolioXirr.fromJson(_candidate());
    expect(candidate.annualRate, isNull);
    expect(candidate.candidateRate, '0.1');
  });

  test('valid nullable server states are accepted', () {
    final reports =
        fixture('golden-portfolio-xirr-states.json')['reports'] as Json;
    for (final entry in reports.entries) {
      final report = PortfolioXirr.fromJson(entry.value as Json);
      expect(report.annualRate, isNull);
      expect(report.candidateRate, isNull);
      expect(report.netContributions, isNull);
      expect(report.status, entry.key == 'empty' ? 'empty' : 'unavailable');
    }
  });

  test(
    'research rate contract remains bounded while XIRR formatting accepts finite large rates',
    () {
      final rate = (BigInt.from(3).pow(365) - BigInt.one).toString();
      expect(decimalPercent(rate), '${rate}00%');
      expect(() => researchPercent(rate), throwsFormatException);
    },
  );

  for (final mutate in <String, void Function(Json)>{
    'private field': (j) => j['account_id'] = 'private',
    'uncomputed historical FX presented as net cash': (j) => j.addAll({
      'status': 'unavailable',
      'annual_rate': null,
      'uniqueness': 'not_applicable',
      'issues': [
        {'code': 'missing_historical_fx', 'message': 'missing', 'field': 'USD'},
      ],
    }),
    'nonfinite rate': (j) => j['annual_rate'] = List.filled(310, '9').join(),
    'noncanonical rate': (j) => j['annual_rate'] = '0.10',
    'numeric rate': (j) => j['annual_rate'] = 0.1,
    'minus100 percent': (j) => j['annual_rate'] = '-1',
    'unsupported precision': (j) => j['annual_rate'] = '0.1234567890123',
    'invalid calendar date': (j) => j['period_start'] = '2025-02-30',
    'report cutoff mismatch': (j) => j['period_end'] = '2026-01-02',
    'foreign net capital': (j) =>
        (j['net_contributions'] as Json)['currency'] = 'USD',
    'unknown solver': (j) => j['solver_policy'] = 'guess',
    'unknown issue': (j) => j['issues'] = [_issue('private-code')],
    'candidate in headline': (j) => j.addAll({
      'status': 'candidate',
      'candidate_rate': '0.2',
      'sign_changes': 2,
      'uniqueness': 'not_proven',
    }),
    'lost sample provenance': (j) => j['sample'] = true,
    'unknown count reported as known': (j) => j['external_event_count'] = null,
  }.entries) {
    test('XIRR parser rejects ${mutate.key}', () {
      final j = _report();
      mutate.value(j);
      expect(() => PortfolioXirr.fromJson(j), throwsFormatException);
    });
  }

  test(
    'REST XIRR uses its read-only endpoint and validates the body',
    () async {
      final client = MockClient((request) async {
        expect(request.method, 'GET');
        expect(request.url.path, '/v1/portfolio/xirr');
        expect(request.url.query, isEmpty);
        return http.Response(jsonEncode(_report()), 200);
      });
      addTearDown(client.close);
      expect(
        (await RestOmniApi(client: client).portfolioXirr()).annualRate,
        '0.1',
      );
    },
  );

  testWidgets(
    'home opens report only on explicit action and keeps old result on error',
    (tester) async {
      final api = _api(_report());
      await tester.pumpWidget(OmniFolioApp(api: api));
      await pumpUi(tester);
      expect(api.xirrCalls, 0);
      await _open(tester, api);
      expect(api.xirrCalls, 1);
      expect(find.byKey(const Key('portfolio-xirr-rate')), findsOneWidget);
      expect(find.text('약 10%'), findsOneWidget);
      api.failXirr = true;
      await tester.tap(find.byTooltip('수익률 새로고침'));
      await tester.pumpAndSettle();
      expect(find.text('약 10%'), findsOneWidget);
      expect(find.textContaining('이전 결과와 기준 시점을 유지합니다.'), findsOneWidget);
      expect(find.textContaining('private-xirr-sentinel'), findsNothing);
      final details = find.text('계산 방법과 기준 보기');
      await _show(tester, details);
      await tester.tap(details);
      await tester.pumpAndSettle();
      expect(find.text('보고서 기준: 2026-01-01T00:00:00Z'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('candidate is only visible after explicit details expansion', (
    tester,
  ) async {
    await _open(tester, _api(_candidate()));
    expect(find.byKey(const Key('portfolio-xirr-rate')), findsNothing);
    expect(find.text('수익률 하나로 확정할 수 없습니다'), findsOneWidget);
    expect(find.byKey(const Key('portfolio-xirr-candidate')), findsNothing);
    final details = find.text('계산 방법과 기준 보기');
    await _show(tester, details);
    await tester.tap(details);
    await tester.pumpAndSettle();
    await _show(tester, find.byKey(const Key('portfolio-xirr-candidate')));
    expect(find.text('참고 후보: 약 10%'), findsOneWidget);
    expect(find.textContaining('private-upstream-sentinel'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('unavailable is not zero while a real zero remains visible', (
    tester,
  ) async {
    final j = _report()
      ..addAll({
        'status': 'unavailable',
        'annual_rate': null,
        'uniqueness': 'not_applicable',
        'issues': [_issue('numerical_limit')],
      });
    final api = _api(j);
    await _open(tester, api);
    expect(find.byKey(const Key('portfolio-xirr-rate')), findsNothing);
    expect(find.text('약 0%'), findsNothing);
    api.xirrValue = PortfolioXirr.fromJson(_report()..['annual_rate'] = '0');
    await tester.tap(find.byTooltip('수익률 새로고침'));
    await tester.pumpAndSettle();
    expect(find.text('약 0%'), findsOneWidget);
  });

  testWidgets('pending refresh is single-flight and disposal is safe', (
    tester,
  ) async {
    final gate = Completer<PortfolioXirr>();
    final api = _api(_report())..xirrCompleter = gate;
    await _open(tester, api);
    expect(api.xirrCalls, 1);
    expect(
      tester.widget<IconButton>(find.byTooltip('수익률 새로고침')).onPressed,
      isNull,
    );
    await tester.pageBack();
    await tester.pumpAndSettle();
    gate.completeError(const ApiException('private-disposed-error'));
    await pumpUi(tester);
    expect(tester.takeException(), isNull);
  });

  testWidgets('initial read failure can be retried without exposing details', (
    tester,
  ) async {
    final api = _api(_report())..failXirr = true;
    await _open(tester, api);
    expect(find.text('수익률을 불러오지 못했습니다'), findsOneWidget);
    expect(find.textContaining('private-xirr-sentinel'), findsNothing);
    api.failXirr = false;
    final retry = find.widgetWithText(ElevatedButton, '다시 시도');
    await _show(tester, retry);
    expect(tester.getSize(retry).height, greaterThanOrEqualTo(48));
    await tester.tap(retry);
    await tester.pumpAndSettle();
    expect(api.xirrCalls, 2);
    expect(find.text('약 10%'), findsOneWidget);
  });

  testWidgets(
    'large finite annual rate renders without overflow at200 percent',
    (tester) async {
      tester.view.physicalSize = const Size(375, 812);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(() {
        tester.view.resetPhysicalSize();
        tester.view.resetDevicePixelRatio();
        tester.platformDispatcher.clearTextScaleFactorTestValue();
      });
      final rate = (BigInt.from(3).pow(365) - BigInt.one).toString();
      await _open(tester, _api(_report()..['annual_rate'] = rate));
      expect(
        tester.widget<Text>(find.byKey(const Key('portfolio-xirr-rate'))).data,
        '약 ${rate}00%',
      );
      expect(tester.takeException(), isNull);
    },
  );

  for (final brightness in Brightness.values) {
    for (final size in [const Size(375, 812), const Size(812, 375)]) {
      testWidgets('XIRR $brightness $size at200 percent and reduced motion', (
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
        final j = _candidate()..['sample'] = true;
        (j['issues'] as List).add(_issue('sample_data'));
        await _open(tester, _api(j));
        expect(find.textContaining('예시 가격·환율 포함'), findsOneWidget);
        final details = find.text('계산 방법과 기준 보기');
        await _show(tester, details);
        expect(
          tester
              .getSize(
                find.ancestor(
                  of: details,
                  matching: find.byType(ExpansionTile),
                ),
              )
              .height,
          greaterThanOrEqualTo(48),
        );
        await tester.tap(details);
        await tester.pumpAndSettle();
        await _show(tester, find.byKey(const Key('portfolio-xirr-candidate')));
        expect(tester.takeException(), isNull);
      });
    }
  }
}

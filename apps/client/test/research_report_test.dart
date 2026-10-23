import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/research_report.dart';
import 'package:omni_folio_client/research_report_page.dart';

Map<String, dynamic> researchReportJson({bool empty = false}) => {
  'schema_version': 'research-report.v1',
  'mode': 'research_artifact_only',
  'metrics_verification': 'reported_not_recomputed',
  'observed_at': '2026-09-05T01:02:03Z',
  'report': empty
      ? null
      : {
          'recorded_at': '2026-09-05T01:00:00Z',
          'fast_window': 2,
          'slow_window': 5,
          'quantity': '10',
          'candidate_count': 4,
          'fold_count': 2,
          'final_holdout': {
            'first_bar_at': '2026-04-01T00:00:00Z',
            'last_bar_at': '2026-04-30T00:00:00Z',
            'bar_count': 30,
          },
          'promotion_target': 'paper_candidate',
          'failed_gates': <String>[],
          'walk_forward': {
            'after_cost_return': '0.014734027',
            'max_drawdown': '0.000112',
            'round_trips': '2',
          },
          'holdout': {
            'after_cost_return': '0.014734027',
            'max_drawdown': '0.000112',
            'round_trips': '1',
          },
          'buy_and_hold': {
            'after_cost_return': '0.007727027',
            'max_drawdown': '0.010119',
            'round_trips': '1',
          },
        },
};

void main() {
  test('closed report preserves exact metrics and the empty observation', () {
    final view = ResearchReport.fromJson(researchReportJson());
    expect(view.report!.walkForward.afterCostReturn, '0.014734027');
    expect(view.report!.quantity, '10');
    expect(
      ResearchReport.fromJson(researchReportJson(empty: true)).report,
      isNull,
    );
    final value = researchReportJson();
    value['report']['promotion_target'] = 'no_promotion';
    value['report']['failed_gates'] = ['walk_forward', 'buy_and_hold_baseline'];
    expect(
      ResearchReport.fromJson(value).report!.promotionTarget,
      ResearchPromotion.noPromotion,
    );
  });

  test(
    'rejects missing, private, unknown and inconsistent fields at every level',
    () {
      void reject(void Function(Map<String, dynamic>) change) {
        final value = researchReportJson();
        change(value);
        expect(() => ResearchReport.fromJson(value), throwsFormatException);
      }

      for (final key in researchReportJson().keys) {
        reject((j) => j.remove(key));
      }
      reject((j) => j['account_id'] = 'private');
      reject((j) => j['schema_version'] = 'research-report.v2');
      reject((j) => j['mode'] = 'live');
      reject((j) => j['metrics_verification'] = 'verified');
      reject((j) => j['report'] = []);
      reject((j) => j['report']['artifact_path'] = '/private');
      reject((j) => j['report']['final_holdout']['bars'] = []);
      reject((j) => j['report']['walk_forward']['account_id'] = 'private');
      reject((j) => j['report']['holdout'].remove('round_trips'));
      reject((j) => j['report']['buy_and_hold']['max_drawdown'] = '-0.1');
      for (final key in [
        'fast_window',
        'slow_window',
        'candidate_count',
        'fold_count',
      ]) {
        for (final invalid in [0, -1, 1.5, '2', 9007199254740992]) {
          reject((j) => j['report'][key] = invalid);
        }
      }
      reject((j) => j['report']['fold_count'] = 1);
      reject((j) => j['report']['fast_window'] = 5);
      reject((j) => j['report']['final_holdout']['bar_count'] = 0);
      reject((j) => j['report']['promotion_target'] = 'live');
      reject((j) => j['report']['failed_gates'] = ['walk_forward']);
      reject((j) => j['report']['promotion_target'] = 'no_promotion');
      for (final gates in [
        ['unknown'],
        ['walk_forward', 'walk_forward'],
        ['final_holdout', 'walk_forward'],
      ]) {
        reject((j) {
          j['report']['promotion_target'] = 'no_promotion';
          j['report']['failed_gates'] = gates;
        });
      }
      for (final value in [
        '-0',
        '01',
        '+1',
        '1e2',
        '1.0',
        '.1',
        '0.10',
        '',
        '1' * 129,
      ]) {
        reject((j) => j['report']['holdout']['after_cost_return'] = value);
      }
      for (final value in ['0', '-1', '0.0']) {
        reject((j) => j['report']['quantity'] = value);
      }
      for (final value in ['-1', '1.2', '01', '1' * 129]) {
        reject((j) => j['report']['walk_forward']['round_trips'] = value);
      }
    },
  );

  test(
    'UTC timestamps require canonical calendar and retain nanosecond order',
    () {
      for (final date in [
        '2026-02-30T00:00:00Z',
        '2026-04-01T24:00:00Z',
        '2026-04-01T00:00:00+00:00',
        '2026-04-01T00:00:00.0Z',
        '2026-04-01T00:00:00.10Z',
        '2026-04-01T00:00:00.1234567891Z',
      ]) {
        final value = researchReportJson()..['observed_at'] = date;
        expect(() => ResearchReport.fromJson(value), throwsFormatException);
      }
      final value = researchReportJson();
      value['report']['final_holdout']['first_bar_at'] =
          '2026-04-01T00:00:00.000000002Z';
      value['report']['final_holdout']['last_bar_at'] =
          '2026-04-01T00:00:00.000000001Z';
      expect(() => ResearchReport.fromJson(value), throwsFormatException);
      value['report']['final_holdout']['last_bar_at'] =
          '2026-04-01T00:00:00.000000002Z';
      expect(() => ResearchReport.fromJson(value), throwsFormatException);
      value['report']['final_holdout']['bar_count'] = 1;
      expect(ResearchReport.fromJson(value).report, isNotNull);
    },
  );

  test('percent shifts exact strings without rounding or loss bounds', () {
    for (final entry in {
      '0': '0%',
      '1': '100%',
      '-1': '-100%',
      '0.1': '10%',
      '-0.001': '-0.1%',
      '0.014734027': '1.4734027%',
      '-1.23456789123456789': '-123.456789123456789%',
      '0.${'0' * 124}1': '0.${'0' * 122}1%',
      '9' * 128: '${'9' * 128}00%',
    }.entries) {
      expect(researchPercent(entry.key), entry.value);
    }
    final value = researchReportJson();
    value['report']['holdout']['after_cost_return'] = '-2';
    value['report']['holdout']['max_drawdown'] = '3';
    expect(ResearchReport.fromJson(value).report!.holdout.maxDrawdown, '3');
  });

  test(
    'REST uses GET at fixed path without query and propagates errors',
    () async {
      final api = RestOmniApi(
        baseUrl: 'http://example.test/',
        client: MockClient((request) async {
          expect(request.method, 'GET');
          expect(
            request.url,
            Uri.parse('http://example.test/v1/research/report'),
          );
          return http.Response(jsonEncode(researchReportJson()), 200);
        }),
      );
      expect((await api.researchReport()).report, isNotNull);
      final failed = RestOmniApi(
        client: MockClient((_) async => http.Response('{}', 503)),
      );
      await expectLater(failed.researchReport(), throwsA(isA<ApiException>()));
    },
  );

  testWidgets('Connections loads the stored report only on demand', (
    tester,
  ) async {
    final api = _ResearchApi()
      ..next = ResearchReport.fromJson(researchReportJson(empty: true));
    final controller = PortfolioController(api);
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: DataPage(api: api, controller: controller),
        ),
      ),
    );
    expect(api.calls, 0);
    await tester.scrollUntilVisible(find.text('백테스트 보고서 보기'), 200);
    await tester.tap(find.text('백테스트 보고서 보기'));
    await tester.pumpAndSettle();
    expect(api.calls, 1);
    expect(find.text('저장된 연구 보고서 없음'), findsOneWidget);
  });

  testWidgets('initial redacted failure retries into empty with observation', (
    tester,
  ) async {
    final api = _ResearchApi()
      ..failure = const ApiException('private-path secret');
    await tester.pumpWidget(MaterialApp(home: ResearchReportPage(api: api)));
    await tester.pumpAndSettle();
    expect(find.text('연구 보고서를 불러오지 못했습니다'), findsOneWidget);
    expect(find.textContaining('secret'), findsNothing);
    api
      ..failure = null
      ..next = ResearchReport.fromJson(researchReportJson(empty: true));
    await tester.tap(find.text('다시 시도'));
    await tester.pumpAndSettle();
    expect(find.text('저장된 연구 보고서 없음'), findsOneWidget);
    expect(find.text('관측 시각 2026-09-05T01:02:03Z'), findsOneWidget);
  });

  testWidgets(
    'refresh retains full observation and timestamp; fetch is single flight',
    (tester) async {
      final api = _ResearchApi()
        ..next = ResearchReport.fromJson(researchReportJson());
      await tester.pumpWidget(MaterialApp(home: ResearchReportPage(api: api)));
      await tester.pumpAndSettle();
      final pending = Completer<ResearchReport>();
      api.pending = pending;
      await tester.tap(find.byTooltip('백테스트 보고서 새로고침'));
      await tester.pump();
      await tester.tap(find.byTooltip('백테스트 보고서 새로고침'));
      expect(api.calls, 2);
      pending.completeError(const ApiException('private-path secret'));
      await tester.pumpAndSettle();
      expect(find.text('관측 시각 2026-09-05T01:02:03Z'), findsOneWidget);
      expect(find.text('등록 시각 2026-09-05T01:00:00Z'), findsOneWidget);
      expect(find.textContaining('마지막 정상 관측을 그대로 유지합니다'), findsOneWidget);
      expect(find.textContaining('secret'), findsNothing);
      await tester.scrollUntilVisible(find.text('최종 평가 후보'), 250);
      expect(find.text('빠른 창 2 · 느린 창 5 · 수량 10'), findsOneWidget);
    },
  );

  testWidgets('unpromoted result, exact metrics and meaning stay accessible', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    try {
      final value = researchReportJson();
      value['report']['promotion_target'] = 'no_promotion';
      value['report']['failed_gates'] = [
        'walk_forward',
        'final_holdout',
        'buy_and_hold_baseline',
      ];
      final api = _ResearchApi()..next = ResearchReport.fromJson(value);
      await tester.pumpWidget(MaterialApp(home: ResearchReportPage(api: api)));
      await tester.pumpAndSettle();
      expect(find.text('저장된 연구 결과 · 지표 재계산 안 함'), findsOneWidget);
      expect(find.text('저장된 판정: 승격하지 않음'), findsOneWidget);
      await tester.scrollUntilVisible(find.text('구간마다 다시 선택한 후보의 검증 요약'), 200);
      expect(find.text('비용 차감 산술평균 수익률 1.4734027%'), findsOneWidget);
      expect(
        find.semantics.byLabel(
          RegExp(r'구간마다 다시 선택한 후보의 검증 요약[\s\S]*합산 완료 왕복 거래 2'),
        ),
        findsOneWidget,
      );
      await tester.scrollUntilVisible(find.text('매수 후 보유 비교'), 200);
      expect(find.textContaining('동일한 현금·수량·비용·지연'), findsOneWidget);
      expect(find.text('비용 차감 수익률 0.7727027%'), findsOneWidget);
    } finally {
      semantics.dispose();
    }
  });

  testWidgets(
    '320px, 200 percent, light/dark and reduced motion wrap long values',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final value = researchReportJson();
      value['report']['holdout']['after_cost_return'] = '9' * 128;
      value['report']['quantity'] = '9' * 128;
      final api = _ResearchApi()..next = ResearchReport.fromJson(value);
      for (final brightness in [Brightness.light, Brightness.dark]) {
        await tester.pumpWidget(
          MaterialApp(
            theme: ThemeData(brightness: brightness),
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context).copyWith(disableAnimations: true),
              child: child!,
            ),
            home: ResearchReportPage(key: ValueKey(brightness), api: api),
          ),
        );
        await tester.pumpAndSettle();
        await tester.scrollUntilVisible(find.text('최종 평가 후보'), 200);
        await tester.scrollUntilVisible(
          find.text('비용 차감 수익률 ${'9' * 128}00%'),
          200,
        );
        expect(tester.takeException(), isNull);
      }
    },
  );

  testWidgets('keyboard refresh and accessible tap targets remain usable', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    try {
      final api = _ResearchApi()
        ..next = ResearchReport.fromJson(researchReportJson(empty: true));
      await tester.pumpWidget(MaterialApp(home: ResearchReportPage(api: api)));
      await tester.pumpAndSettle();
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await tester.sendKeyEvent(LogicalKeyboardKey.tab);
      await tester.pump();
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await tester.pumpAndSettle();
      expect(api.calls, 2);
    } finally {
      semantics.dispose();
    }
  });

  testWidgets('loading completion after disposal is harmless', (tester) async {
    final pending = Completer<ResearchReport>();
    final api = _ResearchApi()..pending = pending;
    await tester.pumpWidget(MaterialApp(home: ResearchReportPage(api: api)));
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    await tester.pumpWidget(const MaterialApp(home: SizedBox()));
    pending.complete(ResearchReport.fromJson(researchReportJson()));
    await tester.pump();
    expect(tester.takeException(), isNull);
  });
}

class _ResearchApi implements OmniApi {
  ResearchReport? next;
  Completer<ResearchReport>? pending;
  Object? failure;
  int calls = 0;
  @override
  Future<ResearchReport> researchReport() async {
    calls++;
    if (failure case final error?) throw error;
    return pending?.future ?? next!;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

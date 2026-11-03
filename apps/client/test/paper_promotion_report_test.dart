import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/paper_promotion_report.dart';
import 'package:omni_folio_client/paper_promotion_report_page.dart';

typedef Json = Map<String, dynamic>;

Json _fixture(String name) {
  final fixture =
      jsonDecode(
            File(
              '../../contracts/fixtures/paper-promotion-report-cases.json',
            ).readAsStringSync(),
          )
          as Json;
  final entry = (fixture['cases'] as List).cast<Json>().singleWhere(
    (entry) => entry['name'] == name,
  );
  return entry['response'] as Json;
}

void main() {
  test(
    'all shared observations preserve decisions and exact metric strings',
    () {
      const expected = {
        'empty': null,
        'pending': 'PENDING',
        'promoted-current': 'PROMOTE',
        'promoted-superseded': 'PROMOTE',
        'held': 'HOLD',
        'stale': 'STALE',
        'error': 'ERROR',
        'aborted': 'ABORTED',
      };
      for (final entry in expected.entries) {
        final view = PaperPromotionReport.fromJson(_fixture(entry.key));
        expect(view.attempt?.decision, entry.value, reason: entry.key);
        expect(view.observedAt, '2026-10-27T15:00:00Z');
      }
      final promoted = PaperPromotionReport.fromJson(
        _fixture('promoted-current'),
      );
      expect(promoted.attempt!.selectionStatus, 'current');
      expect(promoted.attempt!.comparison!.windows.length, 3);
      expect(
        promoted.attempt!.comparison!.windows.first.candidate.afterCostReturn,
        '0.014734027',
      );
      expect(
        promoted.attempt!.comparison!.windows.last.buyAndHold.turnover,
        '0.045992',
      );
      expect(
        PaperPromotionReport.fromJson(
          _fixture('promoted-superseded'),
        ).attempt!.selectionStatus,
        'superseded',
      );
      expect(
        PaperPromotionReport.fromJson(
          _fixture('stale'),
        ).attempt!.comparison!.decision,
        'PROMOTE',
      );
    },
  );

  test('closed response rejects private, missing and unsupported fields', () {
    void reject(void Function(Json) change) {
      final value = _fixture('promoted-current');
      change(value);
      expect(() => PaperPromotionReport.fromJson(value), throwsFormatException);
    }

    for (final key in _fixture('promoted-current').keys) {
      reject((j) => j.remove(key));
    }
    for (final key in [
      'account_id',
      'session_id',
      'attempt_id',
      'selection_id',
      'hash',
      'raw_csv',
      'path',
      'cash',
      'cost',
    ]) {
      reject((j) => j[key] = 'private');
      reject((j) => j['attempt'][key] = 'private');
    }
    reject((j) => j['metrics_verification'] = 'recomputed_from_stored_input');
    reject((j) => j['schema_version'] = 'paper-promotion-report.v2');
    reject((j) => j['mode'] = 'live');
    reject((j) => j['attempt'] = []);
    reject((j) => j['attempt']['selection_id'] = 'private');
    reject((j) => j['attempt']['symbol'] = 'AAPL');
    for (final invalid in [
      '005930\n',
      '005930\r\n',
      '005930 ',
      ' 005930',
      '005930\u2028',
    ]) {
      reject((j) => j['attempt']['symbol'] = invalid);
    }
    reject((j) => j['attempt']['candidate']['account_id'] = 'private');
    reject((j) => j['attempt']['comparison']['raw_csv'] = 'private');
    reject(
      (j) => j['attempt']['comparison']['metrics_verification'] = 'reported',
    );
    reject((j) => j['attempt']['comparison']['policy_version'] = 'unknown');
    reject((j) => j['attempt']['comparison']['windows'][0]['path'] = 'private');
    reject(
      (j) => j['attempt']['comparison']['windows'][0]['candidate']['cash'] =
          'private',
    );
    reject(
      (j) => j['attempt']['comparison']['windows'][0]['candidate'].remove(
        'turnover',
      ),
    );
  });

  test('decision, reason, selection and comparison remain consistent', () {
    void reject(String name, void Function(Json) change) {
      final value = _fixture(name);
      change(value);
      expect(() => PaperPromotionReport.fromJson(value), throwsFormatException);
    }

    reject(
      'pending',
      (j) => j['attempt']['decided_at'] = '2026-10-27T15:00:00Z',
    );
    reject(
      'pending',
      (j) => j['attempt']['comparison'] = _fixture(
        'promoted-current',
      )['attempt']['comparison'],
    );
    reject('pending', (j) => j['attempt']['reason_code'] = 'comparison_failed');
    reject(
      'error',
      (j) => j['attempt']['comparison'] = _fixture(
        'promoted-current',
      )['attempt']['comparison'],
    );
    reject('aborted', (j) => j['attempt']['decided_at'] = null);
    reject('stale', (j) => j['attempt']['comparison'] = null);
    reject('stale', (j) => j['attempt']['reason_code'] = 'comparison_failed');
    reject(
      'held',
      (j) => j['attempt']['promotion_selection_status'] = 'current',
    );
    reject(
      'promoted-current',
      (j) => j['attempt']['promotion_selection_status'] = 'not_promoted',
    );
    reject('promoted-current', (j) => j['attempt']['decision'] = 'EXECUTE');
    reject(
      'promoted-current',
      (j) => j['attempt']['reason_code'] = 'candidate_negative_return',
    );
    reject(
      'held',
      (j) => j['attempt']['comparison']['reason_code'] =
          'candidate_negative_return',
    );
    reject(
      'promoted-current',
      (j) => j['attempt']['comparison']['reason_code'] =
          'candidate_negative_return',
    );
    for (final reason in [
      'insufficient_round_trips',
      'candidate_negative_return',
      'candidate_return_below_incumbent',
      'candidate_drawdown_above_incumbent',
      'candidate_turnover_above_incumbent',
      'holdout_not_better_than_incumbent',
      'holdout_not_better_than_buy_and_hold',
    ]) {
      final held = _fixture('held');
      held['attempt']['reason_code'] = reason;
      held['attempt']['comparison']['reason_code'] = reason;
      expect(PaperPromotionReport.fromJson(held).attempt!.reasonCode, reason);
    }
    final staleHold = _fixture('stale');
    staleHold['attempt']['comparison'] = _fixture(
      'held',
    )['attempt']['comparison'];
    expect(
      PaperPromotionReport.fromJson(staleHold).attempt!.comparison!.decision,
      'HOLD',
    );
  });

  test('strategies and canonical metrics validate without losing precision', () {
    void reject(void Function(Json) change) {
      final value = _fixture('promoted-current');
      change(value);
      expect(() => PaperPromotionReport.fromJson(value), throwsFormatException);
    }

    for (final invalid in [0, -1, 5, 1.5, '2']) {
      reject((j) => j['attempt']['candidate']['fast_window'] = invalid);
    }
    reject((j) => j['attempt']['candidate']['slow_window'] = 366);
    for (final invalid in [
      '0',
      '-1',
      '01',
      '1.5',
      '4611686018427387904',
      '9' * 20,
      '10\n',
      '10\r\n',
      '10 ',
      ' 10',
      '10\u2028',
    ]) {
      reject((j) => j['attempt']['candidate']['quantity'] = invalid);
    }
    reject((j) => j['attempt']['candidate']['quantity'] = '11');
    for (final invalid in [
      '-0',
      '01',
      '+1',
      '1e2',
      '1.0',
      '0.10',
      '.1',
      '',
      '1' * 129,
      '1\n',
      '1\r\n',
      '1 ',
      ' 1',
      '1\u2028',
    ]) {
      reject(
        (j) =>
            j['attempt']['comparison']['windows'][0]['candidate']['after_cost_return'] =
                invalid,
      );
    }
    for (final field in ['max_drawdown', 'turnover']) {
      for (final invalid in [
        '-1',
        '-0.1',
        '1.0',
        '1' * 129,
        '1\n',
        '1\r\n',
        '1 ',
        ' 1',
      ]) {
        reject(
          (j) => j['attempt']['comparison']['windows'][0]['candidate'][field] =
              invalid,
        );
      }
    }
    for (final invalid in [
      '-1',
      '1.2',
      '01',
      '1' * 129,
      '1\n',
      '1\r\n',
      '1 ',
      ' 1',
    ]) {
      reject(
        (j) =>
            j['attempt']['comparison']['windows'][0]['candidate']['round_trips'] =
                invalid,
      );
    }
    final valid = _fixture('promoted-current');
    valid['attempt']['candidate']['quantity'] = '4611686018427387903';
    valid['attempt']['incumbent']['quantity'] = '4611686018427387903';
    valid['attempt']['candidate']['slow_window'] = 365;
    final metrics = valid['attempt']['comparison']['windows'][0]['candidate'];
    metrics['after_cost_return'] = '-2';
    metrics['max_drawdown'] = '3';
    metrics['turnover'] = '9' * 128;
    metrics['round_trips'] = '9' * 128;
    final parsed = PaperPromotionReport.fromJson(valid);
    expect(parsed.attempt!.candidate.quantity, '4611686018427387903');
    expect(
      parsed.attempt!.comparison!.windows.first.candidate.turnover,
      '9' * 128,
    );
  });

  test(
    'timestamps and windows enforce nanosecond ordering but permit clock regression',
    () {
      void reject(void Function(Json) change) {
        final value = _fixture('promoted-current');
        change(value);
        expect(
          () => PaperPromotionReport.fromJson(value),
          throwsFormatException,
        );
      }

      for (final date in [
        '2026-02-30T00:00:00Z',
        '2026-04-01T24:00:00Z',
        '2026-04-01T00:00:00+00:00',
        '2026-04-01T00:00:00.0Z',
        '2026-04-01T00:00:00.10Z',
        '2026-04-01T00:00:00.1234567891Z',
        '2026-04-01T00:00:00Z\n',
        '2026-04-01T00:00:00Z\r\n',
        '2026-04-01T00:00:00Z ',
        ' 2026-04-01T00:00:00Z',
      ]) {
        reject((j) => j['observed_at'] = date);
      }
      reject(
        (j) => j['attempt']['comparison_start'] = '2026-05-07T00:00:00.1Z',
      );
      reject(
        (j) => j['attempt']['comparison_start'] = '2026-05-07T00:00:00Z\n',
      );
      reject((j) => j['attempt']['comparison_end'] = '2026-10-27T15:00:01Z');
      reject(
        (j) => j['attempt']['claimed_at'] = '2026-08-03T23:59:59.999999999Z',
      );
      reject((j) => j['attempt']['comparison']['windows'].removeLast());
      reject(
        (j) => j['attempt']['comparison']['windows'][0]['start'] =
            '2026-05-08T00:00:00Z',
      );
      reject(
        (j) => j['attempt']['comparison']['windows'][2]['end'] =
            '2026-08-03T00:00:00Z',
      );
      reject(
        (j) => j['attempt']['comparison']['windows'][0]['end'] =
            '2026-05-07T00:00:00Z',
      );
      reject(
        (j) => j['attempt']['comparison']['windows'][1]['start'] =
            '2026-06-05T00:00:00Z',
      );
      reject((j) {
        j['attempt']['claimed_at'] = '2026-10-27T15:00:00.000000002Z';
        j['attempt']['decided_at'] = '2026-10-27T15:00:00.000000001Z';
      });
      final regressed = _fixture('promoted-current');
      regressed['observed_at'] = '2020-01-01T00:00:00Z';
      regressed['attempt']['claimed_at'] = '2026-10-27T15:00:00.000000001Z';
      regressed['attempt']['decided_at'] = '2026-10-27T15:00:00.000000002Z';
      expect(
        PaperPromotionReport.fromJson(regressed).observedAt,
        '2020-01-01T00:00:00Z',
      );
    },
  );

  test('REST request uses only the fixed GET path', () async {
    http.Request? request;
    final api = RestOmniApi(
      baseUrl: 'http://example.test/',
      client: MockClient((value) async {
        request = value;
        return http.Response(jsonEncode(_fixture('empty')), 200);
      }),
    );
    expect((await api.paperPromotionReport()).attempt, isNull);
    expect(request!.method, 'GET');
    expect(
      request!.url,
      Uri.parse('http://example.test/v1/paper/promotion-report'),
    );
    expect(request!.url.hasQuery, isFalse);
  });

  testWidgets('connections fetches only when opened', (tester) async {
    final api = _PaperApi()
      ..next = PaperPromotionReport.fromJson(_fixture('empty'));
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
    await tester.scrollUntilVisible(find.text('모의 전략 비교 보기'), 200);
    await tester.pumpAndSettle();
    expect(find.text('모의 전략 비교 보기').hitTestable(), findsOneWidget);
    await tester.tap(find.text('모의 전략 비교 보기'));
    await tester.pumpAndSettle();
    expect(api.calls, 1);
    expect(find.text('모의 전략 비교'), findsOneWidget);
    expect(find.text('저장된 모의 전략 비교 기록이 없습니다.'), findsOneWidget);
    expect(find.text('관측 시각 2026-10-27T15:00:00Z'), findsOneWidget);
  });

  testWidgets('loading and redacted initial error retry to empty', (
    tester,
  ) async {
    final pending = Completer<PaperPromotionReport>();
    final api = _PaperApi()..pending = pending;
    await tester.pumpWidget(
      MaterialApp(home: PaperPromotionReportPage(api: api)),
    );
    await tester.pump();
    expect(find.byType(LinearProgressIndicator), findsOneWidget);
    expect(find.textContaining('현재 증권사 상태'), findsOneWidget);
    expect(find.text('전략 선택 결과는 자동매매 실행 권한을 뜻하지 않습니다.'), findsOneWidget);
    pending.completeError(
      const ApiException('account-secret raw database failure'),
    );
    await tester.pumpAndSettle();
    expect(find.text('모의 전략 비교를 불러오지 못했습니다.'), findsOneWidget);
    expect(find.text('저장된 모의 전략 비교 기록이 없습니다.'), findsNothing);
    expect(find.text('전략 선택 결과는 자동매매 실행 권한을 뜻하지 않습니다.'), findsOneWidget);
    expect(find.textContaining('account-secret'), findsNothing);
    api
      ..pending = null
      ..next = PaperPromotionReport.fromJson(_fixture('empty'));
    await tester.tap(find.text('다시 시도'));
    await tester.pumpAndSettle();
    expect(find.text('저장된 모의 전략 비교 기록이 없습니다.'), findsOneWidget);
    expect(api.calls, 2);
  });

  testWidgets(
    'failed refresh retains the entire prior observation and recovers',
    (tester) async {
      final api = _PaperApi()
        ..next = PaperPromotionReport.fromJson(_fixture('promoted-current'));
      await tester.pumpWidget(
        MaterialApp(home: PaperPromotionReportPage(api: api)),
      );
      await tester.pumpAndSettle();
      final pending = Completer<PaperPromotionReport>();
      api.pending = pending;
      await tester.tap(find.byTooltip('모의 전략 비교 새로고침'));
      await tester.pump();
      await tester.tap(find.byTooltip('모의 전략 비교 새로고침'));
      expect(api.calls, 2);
      expect(find.text('관측 시각 2026-10-27T15:00:00Z'), findsOneWidget);
      expect(find.text('이 비교에서 후보 선택'), findsOneWidget);
      pending.completeError(const ApiException('private-path provider-secret'));
      await tester.pumpAndSettle();
      expect(find.textContaining('마지막 정상 관측을 그대로 유지합니다'), findsOneWidget);
      expect(find.textContaining('provider-secret'), findsNothing);
      expect(find.text('이 비교의 선택 기록이 현재 선택으로 남아 있습니다.'), findsOneWidget);
      expect(find.text('비교 요청 기록 시각 2026-10-27T15:00:00Z'), findsOneWidget);
      await tester.scrollUntilVisible(find.text('비교한 전략'), 200);
      expect(find.text('종목 005930 · 이동평균 교차'), findsOneWidget);
      expect(
        find.text('후보: 단기 이동평균 2개 봉 · 장기 이동평균 5개 봉 · 수량 10주'),
        findsOneWidget,
      );
      await tester.scrollUntilVisible(find.text('구간 1 · 30개 봉'), 200);
      await tester.tap(find.text('구간 1 · 30개 봉'));
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(find.text('비용 차감 수익률 1.4734027%'), 200);
      expect(find.text('비용 차감 수익률 1.4734027%'), findsOneWidget);
      api
        ..pending = null
        ..next = PaperPromotionReport.fromJson(_fixture('held'));
      await tester.tap(find.byTooltip('모의 전략 비교 새로고침'));
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(find.text('이 비교에서 후보를 선택하지 않음'), -200);
      expect(find.text('이 비교에서 후보를 선택하지 않음'), findsOneWidget);
      expect(find.textContaining('마지막 정상 관측'), findsNothing);
      expect(api.calls, 3);
    },
  );

  testWidgets('all outcomes and exact selection status use safe wording', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    try {
      const outcomes = {
        'pending': '결정 기록 없음',
        'promoted-current': '이 비교에서 후보 선택',
        'promoted-superseded': '이 비교에서 후보 선택',
        'held': '이 비교에서 후보를 선택하지 않음',
        'stale': '적용 전 상태 검증을 통과하지 못해 선택에 반영하지 않았습니다.',
        'error': '비교 기록 처리 실패',
        'aborted': '비교 결정 중단',
      };
      for (final entry in outcomes.entries) {
        final api = _PaperApi()
          ..next = PaperPromotionReport.fromJson(_fixture(entry.key));
        await tester.pumpWidget(
          MaterialApp(
            home: PaperPromotionReportPage(key: ValueKey(entry.key), api: api),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text(entry.value), findsOneWidget, reason: entry.key);
        expect(find.textContaining('실행 중'), findsNothing);
        expect(find.textContaining('안전합니다'), findsNothing);
        expect(find.byType(ElevatedButton), findsNothing);
        expect(find.byType(OutlinedButton), findsNothing);
        expect(find.byType(FilledButton), findsNothing);
        if (entry.key == 'pending') {
          expect(find.text('비교 요청이 저장되었으나 결정 기록은 없습니다.'), findsOneWidget);
        }
        if (entry.key == 'promoted-current') {
          expect(find.text('이 비교의 선택 기록이 현재 선택으로 남아 있습니다.'), findsOneWidget);
        }
        if (entry.key == 'promoted-superseded') {
          expect(
            find.text(
              '이 비교의 선택 기록은 현재 선택이 아닙니다. 같은 전략을 다시 선택했더라도 다른 선택 기록입니다.',
            ),
            findsOneWidget,
          );
        }
        if (entry.key == 'stale') {
          expect(
            find.semantics.byLabel(
              RegExp('적용 전 상태 검증을 통과하지 못해 선택에 반영하지 않았습니다'),
            ),
            findsOneWidget,
          );
          expect(find.textContaining('계좌가 변경'), findsNothing);
        }
        expect(
          find.semantics.byLabel(
            RegExp(
              r'저장된 모의 비교[\s\S]*실제 수익이 아닙니다[\s\S]*합산하지 않습니다[\s\S]*전략 선택 결과는 자동매매 실행 권한을 뜻하지 않습니다',
            ),
          ),
          findsOneWidget,
        );
        if (entry.key == 'stale') {
          await tester.scrollUntilVisible(find.text('저장된 비교 지표'), 200);
          expect(find.textContaining('비교 지표 판정: 후보 기준 통과'), findsOneWidget);
          expect(find.text('이 비교에서 후보 선택'), findsNothing);
        }
      }
    } finally {
      semantics.dispose();
    }
  });

  testWidgets('window details display exact percentages and model scope', (
    tester,
  ) async {
    final value = _fixture('promoted-current');
    value['attempt']['comparison']['windows'][0]['candidate']['after_cost_return'] =
        '-1.23456789123456789';
    final api = _PaperApi()..next = PaperPromotionReport.fromJson(value);
    await tester.pumpWidget(
      MaterialApp(home: PaperPromotionReportPage(api: api)),
    );
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.text('구간 1 · 30개 봉'), 200);
    await tester.tap(find.text('구간 1 · 30개 봉'));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.text('비용 차감 수익률 -123.456789123456789%'),
      200,
    );
    expect(find.text('비용 차감 수익률 -123.456789123456789%'), findsOneWidget);
    expect(find.text('최대 낙폭 0.0112%'), findsOneWidget);
    expect(find.text('거래 회전율 3.8985%'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('매수 후 보유 모델'), 200);
    expect(find.text('매수 후 보유 모델'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('구간 3 · 마지막 검증 구간 · 30개 봉'), 200);
    expect(find.text('구간 3 · 마지막 검증 구간 · 30개 봉'), findsOneWidget);
    await tester.tap(find.text('구간 3 · 마지막 검증 구간 · 30개 봉'));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.text('비용 차감 수익률 1.4734027%'), 200);
    expect(find.text('비용 차감 수익률 1.4734027%'), findsOneWidget);
    final lastWindow = find.ancestor(
      of: find.text('구간 3 · 마지막 검증 구간 · 30개 봉'),
      matching: find.byType(ExpansionTile),
    );
    final lastBuyAndHold = find.descendant(
      of: lastWindow,
      matching: find.text('비용 차감 수익률 0.7727027%'),
    );
    await tester.scrollUntilVisible(lastBuyAndHold, 200);
    expect(lastBuyAndHold, findsOneWidget);
  });

  testWidgets(
    '320px and 200 percent support long values in light, dark and reduced motion',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final semantics = tester.ensureSemantics();
      try {
        final value = _fixture('promoted-current');
        value['attempt']['comparison']['windows'][0]['candidate']['after_cost_return'] =
            '9' * 128;
        for (final brightness in [Brightness.light, Brightness.dark]) {
          final api = _PaperApi()..next = PaperPromotionReport.fromJson(value);
          await tester.pumpWidget(
            MaterialApp(
              theme: ThemeData(brightness: brightness),
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context).copyWith(disableAnimations: true),
                child: child!,
              ),
              home: PaperPromotionReportPage(
                key: ValueKey(brightness),
                api: api,
              ),
            ),
          );
          await tester.pumpAndSettle();
          await tester.scrollUntilVisible(
            find.text('관측 시각 2026-10-27T15:00:00Z'),
            200,
          );
          expect(
            find.semantics.byLabel(RegExp('관측 시각 2026-10-27T15:00:00Z')),
            findsOneWidget,
          );
          await tester.scrollUntilVisible(find.text('구간 1 · 30개 봉'), 200);
          await tester.tap(find.text('구간 1 · 30개 봉'));
          await tester.pumpAndSettle();
          await tester.scrollUntilVisible(
            find.text('비용 차감 수익률 ${'9' * 128}00%'),
            200,
          );
          expect(tester.takeException(), isNull);
        }
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets('keyboard refresh and semantic tap targets work', (tester) async {
    final semantics = tester.ensureSemantics();
    try {
      final api = _PaperApi()
        ..next = PaperPromotionReport.fromJson(_fixture('empty'));
      await tester.pumpWidget(
        MaterialApp(home: PaperPromotionReportPage(api: api)),
      );
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

  testWidgets('reduced motion uses static loading and immediate details', (
    tester,
  ) async {
    final pending = Completer<PaperPromotionReport>();
    final api = _PaperApi()..pending = pending;
    await tester.pumpWidget(
      MaterialApp(
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context).copyWith(disableAnimations: true),
          child: child!,
        ),
        home: PaperPromotionReportPage(api: api),
      ),
    );
    await tester.pump();
    expect(find.text('모의 전략 비교 불러오는 중'), findsOneWidget);
    expect(find.byType(LinearProgressIndicator), findsNothing);
    pending.complete(
      PaperPromotionReport.fromJson(_fixture('promoted-current')),
    );
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.text('구간 1 · 30개 봉'), 200);
    await tester.tap(find.text('구간 1 · 30개 봉'));
    await tester.pump();
    expect(find.text('비용 차감 수익률 1.4734027%'), findsOneWidget);
  });

  testWidgets('completion after disposal does not update the page', (
    tester,
  ) async {
    final pending = Completer<PaperPromotionReport>();
    final api = _PaperApi()..pending = pending;
    await tester.pumpWidget(
      MaterialApp(home: PaperPromotionReportPage(api: api)),
    );
    await tester.pump();
    await tester.pumpWidget(const MaterialApp(home: SizedBox()));
    pending.complete(PaperPromotionReport.fromJson(_fixture('empty')));
    await tester.pump();
    expect(tester.takeException(), isNull);
  });
}

class _PaperApi implements OmniApi {
  PaperPromotionReport? next;
  Completer<PaperPromotionReport>? pending;
  int calls = 0;

  @override
  Future<PaperPromotionReport> paperPromotionReport() async {
    calls += 1;
    return pending?.future ?? next!;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

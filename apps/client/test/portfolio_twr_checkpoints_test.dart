import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/portfolio_twr_checkpoint_plot.dart';
import 'package:omni_folio_client/portfolio_twr_checkpoints_page.dart';
import 'package:omni_folio_client/portfolio_twr_page.dart';

import 'vertical_slice_test.dart' show fixture, pumpUi;

Json _reports() =>
    fixture('golden-portfolio-twr-checkpoints.json')['reports'] as Json;
Json _copy(Json value) => jsonDecode(jsonEncode(value)) as Json;
Json _wire(String name) => _copy(_reports()[name] as Json);
String _flowTime(int index) => index == 0
    ? '2025-01-01T00:00:00Z'
    : '2025-01-01T00:00:00.${index.toString().padLeft(9, '0').replaceFirst(RegExp(r'0+$'), '')}Z';
Json _rates(List<String> rates) {
  final wire = _wire(
    rates.last == '-1'
        ? 'total_loss'
        : rates.last == '0'
        ? 'zero_return'
        : 'compound',
  );
  final report = wire['report'] as Json;
  report['boundary_count'] = rates.length - 1;
  report['external_event_count'] = rates.length - 1;
  report['cumulative_rate'] = rates.last;
  wire['points'] = [
    for (var index = 0; index < rates.length; index++)
      {
        'at': index == rates.length - 1
            ? report['period_end']
            : _flowTime(index),
        'kind': index == rates.length - 1 ? 'valuation' : 'external_flow',
        'cumulative_rate': rates[index],
      },
  ];
  return wire;
}

class _Api implements OmniApi {
  _Api(this.data);
  Json data;
  Future<PortfolioTwrCheckpoints> Function()? handler;
  int calls = 0;
  @override
  Future<PortfolioTwrCheckpoints> portfolioTwrCheckpoints() async {
    calls++;
    return handler?.call() ?? PortfolioTwrCheckpoints.fromJson(data);
  }

  @override
  Future<PortfolioTwr> portfolioTwr() async =>
      PortfolioTwr.fromJson(data['report'] as Json);
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _ChunkClient extends http.BaseClient {
  _ChunkClient(this.chunks);
  final Stream<List<int>> chunks;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async =>
      http.StreamedResponse(chunks, 200);
}

class _WaitingClient extends http.BaseClient {
  final response = Completer<http.StreamedResponse>();
  bool aborted = false;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    (request as http.Abortable).abortTrigger!.then((_) {
      aborted = true;
      response.completeError(http.RequestAbortedException());
    });
    return response.future;
  }
}

void main() {
  test(
    'shared producer reports retain calculated, zero, loss, empty and unsupported states',
    () {
      for (final wire in _reports().values.cast<Json>()) {
        final result = PortfolioTwrCheckpoints.fromJson(wire);
        if (result.report.status == 'calculated') {
          expect(result.points.length, result.report.boundaryCount! + 1);
          expect(result.points.first.cumulativeRate, '0');
          expect(
            result.points.last.cumulativeRate,
            result.report.cumulativeRate,
          );
        } else {
          expect(result.points, isEmpty);
        }
        expect(() => result.points.clear(), throwsUnsupportedError);
      }
    },
  );

  test(
    'closed trace rejects contradictory phases, count, range, precision and metadata',
    () {
      final changes = <void Function(Json)>[
        (w) => w['extra'] = true,
        (w) => w['policy_version'] = 'future',
        (w) => w['points'] = null,
        (w) => (w['points'] as List).removeLast(),
        (w) => w['points'][0]['cumulative_rate'] = '0.1',
        (w) => w['points'][1]['kind'] = 'valuation',
        (w) => w['points'][1]['at'] = w['points'][0]['at'],
        (w) => w['points'][1]['at'] = '2027-01-01T00:00:00Z',
        (w) => w['points'][0]['at'] = '2024-12-31T23:59:59Z',
        (w) => w['points'][1]['at'] = '2025-07-01T00:00:00.0000000010Z',
        (w) => w['points'][1]['cumulative_rate'] = '-1.000000000001',
        (w) => w['points'][1]['cumulative_rate'] = '0.0000000000001',
        (w) => w['points'][1]['cumulative_rate'] = '9' * 2501,
        (w) => w['points'][1]['cumulative_rate'] = '-0',
        (w) => w['points'][1]['unknown'] = true,
        (w) => w['points'][2]['cumulative_rate'] = '0.2',
        (w) => w['points'][2]['at'] = '2025-12-31T23:59:59Z',
        (w) => w['report']['valuation']['valuation_as_of'] =
            '2026-01-02T00:00:00Z',
      ];
      for (final change in changes) {
        final altered = _wire('compound');
        change(altered);
        expect(
          () => PortfolioTwrCheckpoints.fromJson(altered),
          throwsFormatException,
        );
      }
      final unavailable = _wire('full_withdrawal')
        ..['points'] = _wire('compound')['points'];
      expect(
        () => PortfolioTwrCheckpoints.fromJson(unavailable),
        throwsFormatException,
      );
    },
  );

  test(
    'huge prefix, intermediate rounded minus one and exact point bounds stay valid',
    () {
      for (final rates in [
        ['0', '9' * 512, '-1'],
        ['0', '-1', '0'],
        ['0', '9' * 2500, '-1'],
        List<String>.filled(1001, '0'),
      ]) {
        final result = PortfolioTwrCheckpoints.fromJson(_rates(rates));
        final plot = TwrCheckpointPlot(result.points);
        expect(plot.points.length, rates.length);
        expect(
          plot.points.every(
            (p) =>
                p.x.isFinite &&
                p.y.isFinite &&
                p.x >= 0 &&
                p.x <= 1 &&
                p.y >= 0 &&
                p.y <= 1,
          ),
          isTrue,
        );
        expect(result.points.map((p) => p.cumulativeRate).toList(), rates);
        if (rates[1].length > 308) {
          expect(plot.points[1].y, 1);
        }
      }
      expect(
        () => PortfolioTwrCheckpoints.fromJson(
          _rates(List<String>.filled(1002, '0')),
        ),
        throwsFormatException,
      );
    },
  );

  test(
    'same instant phases and one nanosecond boundaries never sort or collapse',
    () {
      final equal = _wire('zero_return');
      equal['report']['period_start'] = equal['report']['period_end'];
      equal['points'][0]['at'] = equal['report']['period_end'];
      final flat = TwrCheckpointPlot(
        PortfolioTwrCheckpoints.fromJson(equal).points,
      );
      expect(flat.points, [(x: 0.5, y: 0.5), (x: 0.5, y: 0.5)]);
      final nano = _rates(['0', '0', '0']);
      const start = '2026-01-01T00:00:00.000000001Z',
          end = '2026-01-01T00:00:00.000000002Z';
      nano['report']['period_start'] = start;
      nano['report']['period_end'] = end;
      nano['report']['valuation']['valuation_as_of'] = end;
      nano['points'][0]['at'] = start;
      nano['points'][1]['at'] = end;
      nano['points'][2]['at'] = end;
      final plot = TwrCheckpointPlot(
        PortfolioTwrCheckpoints.fromJson(nano).points,
      );
      expect(plot.points.map((p) => p.x).toList(), [0, 1, 1]);
      expect(plot.points.map((p) => p.y).toList(), [0.5, 0.5, 0.5]);
    },
  );

  test(
    'independent Fraction prefix references normalize without floating overflow',
    () {
      final cases = fixture('portfolio-twr-cases.json')['cases'] as List;
      for (final c in cases.cast<Json>()) {
        final rates = ['0', ...(c['prefix_expected'] as List).cast<String>()];
        final trace = PortfolioTwrCheckpoints.fromJson(_rates(rates));
        final plot = TwrCheckpointPlot(trace.points);
        expect(plot.points.length, rates.length);
        expect(plot.points.every((p) => p.x.isFinite && p.y.isFinite), isTrue);
        expect(trace.points.last.cumulativeRate, c['expected']);
      }
    },
  );

  test(
    'GET fetches one closed observation without a query or separate valuation join',
    () async {
      var calls = 0;
      final wire = _wire('compound');
      final api = RestOmniApi(
        client: MockClient((request) async {
          calls++;
          expect(request.method, 'GET');
          expect(request.url.path, '/v1/portfolio/twr/checkpoints');
          expect(request.url.hasQuery, isFalse);
          return http.Response(jsonEncode(wire), 200);
        }),
      );
      final trace = await api.portfolioTwrCheckpoints();
      expect(calls, 1);
      expect(trace.report.cumulativeRate, '0.21');
      expect(trace.points.length, 3);
    },
  );

  test(
    'response stream accepts the exact byte bound and cancels one extra byte',
    () async {
      const limit = 3 * (1 << 20);
      final encoded = utf8.encode(jsonEncode(_wire('compound')));
      final exact = Uint8List(limit)
        ..fillRange(0, limit, 32)
        ..setRange(0, encoded.length, encoded);
      final api = RestOmniApi(client: _ChunkClient(Stream.value(exact)));
      expect((await api.portfolioTwrCheckpoints()).points.length, 3);
      var cancelled = false;
      Stream<List<int>> growing() async* {
        try {
          yield Uint8List(limit);
          yield Uint8List(1);
          fail('oversized stream was not cancelled');
        } finally {
          cancelled = true;
        }
      }

      await expectLater(
        RestOmniApi(client: _ChunkClient(growing())).portfolioTwrCheckpoints(),
        throwsFormatException,
      );
      expect(cancelled, isTrue);
      await expectLater(
        RestOmniApi(
          client: MockClient((_) async => http.Response('{}', 200)),
        ).portfolioTwrCheckpoints(),
        throwsFormatException,
      );
    },
  );

  testWidgets('waiting limit aborts the read transport', (tester) async {
    final client = _WaitingClient();
    final call = RestOmniApi(client: client).portfolioTwrCheckpoints();
    final checked = expectLater(call, throwsA(isA<ApiException>()));
    await tester.pump(const Duration(seconds: 41));
    await checked;
    expect(client.aborted, isTrue);
  });

  testWidgets(
    'TWR detail opens the same-observation curve and its point table',
    (tester) async {
      final api = _Api(_wire('compound'));
      await tester.pumpWidget(MaterialApp(home: PortfolioTwrPage(api: api)));
      await tester.pumpAndSettle();
      final action = find.text('입출금 구간별 성과 보기');
      await tester.scrollUntilVisible(action, 180);
      await pumpUi(tester);
      await tester.tap(action);
      await tester.pumpAndSettle();
      expect(api.calls, 1);
      expect(
        find.byKey(const Key('portfolio-twr-checkpoint-chart')),
        findsOneWidget,
      );
      final table = find.text('같은 계산점 표로 보기');
      await tester.scrollUntilVisible(table, 180);
      await pumpUi(tester);
      await tester.tap(table);
      await tester.pumpAndSettle();
      expect(api.calls, 1);
      expect(find.text('입출금 경계 1'), findsOneWidget);
      expect(find.text('서버 비율: 0'), findsOneWidget);
      await tester.scrollUntilVisible(find.text('조회 기말'), 120);
      await pumpUi(tester);
      expect(find.text('서버 비율: 0.21'), findsOneWidget);
    },
  );

  testWidgets(
    'refresh retains whole prior report on error and replaces it with unavailable',
    (tester) async {
      final api = _Api(_wire('compound'));
      await tester.pumpWidget(
        MaterialApp(home: PortfolioTwrCheckpointsPage(api: api)),
      );
      await tester.pumpAndSettle();
      api.handler = () async => throw const ApiException('private-sentinel');
      await tester.tap(find.byTooltip('구간별 성과 새로고침'));
      await tester.pumpAndSettle();
      expect(find.textContaining('이전 보고서와 원래 기준 시점'), findsOneWidget);
      expect(find.textContaining('private-sentinel'), findsNothing);
      expect(
        find.byKey(const Key('portfolio-twr-checkpoint-chart')),
        findsOneWidget,
      );
      expect(find.text('보고서 기준: 2026-01-01T00:00:00Z'), findsOneWidget);
      api.handler = null;
      api.data = _wire('full_withdrawal');
      await tester.tap(find.byTooltip('구간별 성과 새로고침'));
      await tester.pumpAndSettle();
      expect(
        find.byKey(const Key('portfolio-twr-checkpoint-chart')),
        findsNothing,
      );
      expect(find.byKey(const Key('checkpoint-terminal-rate')), findsNothing);
    },
  );

  testWidgets(
    'first failure retries; reduced-motion pending read is single-flight and dispose-safe',
    (tester) async {
      final api = _Api(_wire('compound'))
        ..handler = () async => throw const FormatException('private-sentinel');
      Widget page() => MaterialApp(
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context).copyWith(disableAnimations: true),
          child: child!,
        ),
        home: PortfolioTwrCheckpointsPage(api: api),
      );
      await tester.pumpWidget(page());
      await tester.pumpAndSettle();
      expect(find.text('구간별 성과를 불러오지 못했습니다.'), findsOneWidget);
      final pending = Completer<PortfolioTwrCheckpoints>();
      api.handler = () => pending.future;
      await tester.tap(find.text('구간별 성과 다시 불러오기'));
      await pumpUi(tester);
      expect(api.calls, 2);
      expect(find.text('구간별 성과를 불러오는 중'), findsOneWidget);
      expect(find.byType(LinearProgressIndicator), findsNothing);
      expect(
        tester
            .widget<IconButton>(
              find.byWidgetPredicate(
                (widget) =>
                    widget is IconButton && widget.tooltip == '구간별 성과 새로고침',
              ),
            )
            .onPressed,
        isNull,
      );
      await tester.pumpWidget(const SizedBox.shrink());
      pending.complete(PortfolioTwrCheckpoints.fromJson(_wire('compound')));
      await pumpUi(tester);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'curve keeps owner price, owner FX and sample declarations independent',
    (tester) async {
      final report = _copy(
        (fixture('golden-owner-price-reports.json')['reports'] as Json)['twr']
            as Json,
      );
      report['sample'] = true;
      (report['issues'] as List).add({
        'code': 'sample_data',
        'message': 'reference contains a historical sample',
      });
      final wire = {
        'policy_version': 'twr_cash_flow_checkpoints.v1',
        'report': report,
        'points': [
          {
            'at': report['period_start'],
            'kind': 'external_flow',
            'cumulative_rate': '0',
          },
          {
            'at': report['period_end'],
            'kind': 'valuation',
            'cumulative_rate': report['cumulative_rate'],
          },
        ],
      };
      await tester.pumpWidget(
        MaterialApp(home: PortfolioTwrCheckpointsPage(api: _Api(wire))),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('직접 입력 가격 사용'), findsOneWidget);
      expect(find.textContaining('직접 입력 환율 사용'), findsOneWidget);
      expect(find.textContaining('예시 가격·환율 포함'), findsOneWidget);
      final table = find.text('같은 계산점 표로 보기');
      await tester.scrollUntilVisible(table, 180);
      await pumpUi(tester);
      await tester.tap(table);
      await tester.pumpAndSettle();
      expect(find.textContaining('직접 입력 가격 사용'), findsOneWidget);
      expect(find.textContaining('직접 입력 환율 사용'), findsOneWidget);
      expect(find.textContaining('예시 가격·환율 포함'), findsOneWidget);
    },
  );

  for (final (dark, size) in [
    (false, const Size(375, 812)),
    (true, const Size(375, 812)),
    (false, const Size(812, 375)),
    (true, const Size(812, 375)),
  ]) {
    testWidgets(
      'checkpoint graph/table ${size.width}x${size.height} ${dark ? 'dark' : 'light'} at200percent',
      (tester) async {
        tester.view.physicalSize = size;
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final semantics = tester.ensureSemantics();
        try {
          await tester.pumpWidget(
            MaterialApp(
              theme: ThemeData(
                brightness: dark ? Brightness.dark : Brightness.light,
              ),
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context).copyWith(
                  textScaler: const TextScaler.linear(2),
                  disableAnimations: true,
                ),
                child: child!,
              ),
              home: PortfolioTwrCheckpointsPage(api: _Api(_wire('compound'))),
            ),
          );
          await tester.pumpAndSettle();
          expect(tester.takeException(), isNull);
          final table = find.text('같은 계산점 표로 보기');
          await tester.scrollUntilVisible(table, 180);
          await pumpUi(tester);
          await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
          await tester.tap(table);
          await tester.pumpAndSettle();
          await tester.scrollUntilVisible(find.text('조회 기말'), 180);
          await pumpUi(tester);
          expect(find.text('서버 비율: 0.21'), findsOneWidget);
          expect(tester.takeException(), isNull);
        } finally {
          semantics.dispose();
        }
      },
    );
  }

  testWidgets(
    'large prefixes paint without double overflow and expose lazy expandable source values',
    (tester) async {
      final rates = ['0', '9' * 512, '-1'];
      await tester.pumpWidget(
        MaterialApp(
          home: PortfolioTwrCheckpointsPage(api: _Api(_rates(rates))),
        ),
      );
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      final table = find.text('같은 계산점 표로 보기');
      await tester.scrollUntilVisible(table, 180);
      await pumpUi(tester);
      await tester.tap(table);
      await tester.pumpAndSettle();
      final source = find.text('큰 서버 계산값 원문 보기');
      await tester.scrollUntilVisible(source, 120);
      await pumpUi(tester);
      await tester.tap(source);
      await tester.pumpAndSettle();
      expect(find.text('9' * 512), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pumpWidget(
        MaterialApp(
          home: PortfolioTwrCheckpointsPage(
            api: _Api(_rates(List<String>.filled(1001, '0'))),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(
        find.byKey(const Key('portfolio-twr-checkpoint-chart')),
        findsOneWidget,
      );
      expect(tester.takeException(), isNull);
    },
  );
}

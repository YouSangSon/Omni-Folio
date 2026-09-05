import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/paper_monitor.dart';
import 'package:omni_folio_client/paper_monitor_page.dart';

Map<String, dynamic> paperMonitorJson({
  bool selected = false,
  int sessions = 0,
  int pending = 0,
  String runnerState = 'unowned',
  Map<String, dynamic>? latestPolicy,
}) => {
  'schema_version': 'paper-monitor.v1',
  'mode': 'paper_fixture_only',
  'observed_at': '2026-09-05T01:02:03Z',
  'strategy_selected': selected,
  'session_count': sessions,
  'pending_policy_count': pending,
  'runner': {
    'state': runnerState,
    'heartbeat_at': runnerState == 'unowned' ? null : '2026-09-05T01:00:00Z',
    'expires_at': runnerState == 'unowned' ? null : '2026-09-05T01:05:00Z',
  },
  'latest_policy': latestPolicy,
};

Map<String, dynamic> haltPolicy({bool matchesSelection = false}) => {
  'as_of': '2026-09-04T23:59:00Z',
  'recorded_at': '2026-09-05T00:00:00Z',
  'decision': 'HALT_AND_ROLLBACK',
  'reason_code': 'max_drawdown_limit_reached',
  'matches_current_selection': matchesSelection,
};

void main() {
  test('paper monitor parser accepts the closed v1 response', () {
    final monitor = PaperMonitor.fromJson(
      paperMonitorJson(
        selected: true,
        sessions: 4,
        pending: 2,
        runnerState: 'lease_recorded',
        latestPolicy: haltPolicy(),
      ),
    );

    expect(monitor.observedAt, '2026-09-05T01:02:03Z');
    expect(monitor.runner.state, 'lease_recorded');
    expect(monitor.latestPolicy?.decision, 'HALT_AND_ROLLBACK');
    expect(monitor.latestPolicy?.matchesCurrentSelection, isFalse);
  });

  test('paper monitor parser rejects open or inconsistent responses', () {
    final invalid = <Map<String, dynamic>>[
      {...paperMonitorJson(), 'account_id': 'secret'},
      {...paperMonitorJson(), 'schema_version': 'paper-monitor.v2'},
      {...paperMonitorJson(), 'mode': 'live'},
      {...paperMonitorJson(), 'observed_at': '2026-09-05T10:02:03+09:00'},
      {...paperMonitorJson(), 'session_count': -1},
      {...paperMonitorJson(), 'pending_policy_count': 1.5},
      {
        ...paperMonitorJson(),
        'runner': {
          'state': 'unowned',
          'heartbeat_at': '2026-09-05T01:00:00Z',
          'expires_at': null,
        },
      },
      {
        ...paperMonitorJson(runnerState: 'expired'),
        'runner': {
          'state': 'expired',
          'heartbeat_at': null,
          'expires_at': null,
        },
      },
      {
        ...paperMonitorJson(),
        'latest_policy': {...haltPolicy(), 'id': 'x'},
      },
      {
        ...paperMonitorJson(),
        'latest_policy': {...haltPolicy(), 'decision': 'BUY'},
      },
      {
        ...paperMonitorJson(),
        'latest_policy': {...haltPolicy(), 'reason_code': 'secret_reason'},
      },
      {
        ...paperMonitorJson(),
        'latest_policy': {
          ...haltPolicy(),
          'decision': 'INSUFFICIENT',
          'reason_code': 'within_local_paper_safety_bounds',
        },
      },
      {
        ...paperMonitorJson(),
        'latest_policy': {...haltPolicy(), 'decision': 'HOLD'},
      },
      {
        ...paperMonitorJson(),
        'latest_policy': {
          ...haltPolicy(),
          'reason_code': 'minimum_same_selection_samples_not_met',
        },
      },
    ];

    for (final value in invalid) {
      expect(() => PaperMonitor.fromJson(value), throwsFormatException);
    }
  });

  test('REST API reads only the fixed paper monitor path', () async {
    Uri? requested;
    final api = RestOmniApi(
      baseUrl: 'http://example.test/',
      client: MockClient((request) async {
        requested = request.url;
        return http.Response(jsonEncode(paperMonitorJson()), 200);
      }),
    );

    final monitor = await api.paperMonitor();

    expect(requested, Uri.parse('http://example.test/v1/paper/monitor'));
    expect(monitor.schemaVersion, 'paper-monitor.v1');
  });

  testWidgets('connections opens the monitor and fetches only on demand', (
    tester,
  ) async {
    final api = _PaperApi()..next = PaperMonitor.fromJson(paperMonitorJson());
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
    await tester.scrollUntilVisible(find.text('모의 자동매매 상태 보기'), 200);
    await tester.tap(find.text('모의 자동매매 상태 보기'));
    await tester.pumpAndSettle();

    expect(api.calls, 1);
    expect(find.text('모의 자동매매 상태'), findsOneWidget);
    expect(find.text('선택된 전략 없음'), findsOneWidget);
    expect(find.text('저장된 정책 결정 없음'), findsOneWidget);
  });

  testWidgets('initial error is redacted and retry recovers to empty', (
    tester,
  ) async {
    final api = _PaperApi()
      ..failure = const ApiException('account-secret raw database failure');
    await tester.pumpWidget(MaterialApp(home: PaperMonitorPage(api: api)));
    await tester.pumpAndSettle();

    expect(find.text('모의 자동매매 상태를 불러오지 못했습니다'), findsOneWidget);
    expect(find.textContaining('account-secret'), findsNothing);
    expect(find.text('다시 시도'), findsOneWidget);

    api
      ..failure = null
      ..next = PaperMonitor.fromJson(paperMonitorJson());
    await tester.tap(find.text('다시 시도'));
    await tester.pumpAndSettle();

    expect(find.text('저장된 정책 결정 없음'), findsOneWidget);
    expect(api.calls, 2);
  });

  testWidgets('refresh retains the full known-good observation on failure', (
    tester,
  ) async {
    final api = _PaperApi()
      ..next = PaperMonitor.fromJson(
        paperMonitorJson(
          selected: true,
          sessions: 8,
          pending: 3,
          runnerState: 'lease_recorded',
          latestPolicy: haltPolicy(),
        ),
      );
    await tester.pumpWidget(MaterialApp(home: PaperMonitorPage(api: api)));
    await tester.pumpAndSettle();

    api.failure = const ApiException('lease-owner-secret');
    await tester.tap(find.byTooltip('모의 자동매매 상태 새로고침'));
    await tester.pumpAndSettle();

    expect(find.textContaining('관측 시각 2026-09-05T01:02:03Z'), findsOneWidget);
    expect(find.text('모의 계좌 8개'), findsOneWidget);
    expect(find.text('미완료 정책 3건'), findsOneWidget);
    expect(find.textContaining('마지막 정상 관측을 그대로 유지합니다'), findsOneWidget);
    await tester.scrollUntilVisible(find.text('중단 및 롤백'), 200);
    expect(find.text('중단 및 롤백'), findsOneWidget);
    expect(find.textContaining('lease-owner-secret'), findsNothing);
  });

  testWidgets(
    'halt, selection mismatch, pending scope and ownership stay explicit',
    (tester) async {
      final semantics = tester.ensureSemantics();
      final api = _PaperApi()
        ..next = PaperMonitor.fromJson(
          paperMonitorJson(
            selected: true,
            sessions: 8,
            pending: 3,
            runnerState: 'lease_recorded',
            latestPolicy: haltPolicy(),
          ),
        );
      await tester.pumpWidget(MaterialApp(home: PaperMonitorPage(api: api)));
      await tester.pumpAndSettle();

      try {
        expect(find.text('전략 선택됨'), findsOneWidget);
        expect(find.text('미완료 정책 3건'), findsOneWidget);
        expect(find.textContaining('실행 대기 주문 수가 아닙니다'), findsOneWidget);
        expect(
          find.semantics.byLabel(RegExp(r'저장 스냅샷[\s\S]*실행 대기 주문 수가 아닙니다')),
          findsOneWidget,
        );
        expect(
          find.semantics.byLabel(RegExp('저장된 로컬 모의 데이터.*현재 증권사 상태 아님')),
          findsOneWidget,
        );
        await tester.scrollUntilVisible(find.text('만료 전 소유권 기록'), 200);
        expect(find.text('만료 전 소유권 기록'), findsOneWidget);
        expect(find.textContaining('실행 중입니다'), findsNothing);
        expect(find.textContaining('안전합니다'), findsNothing);
        await tester.scrollUntilVisible(find.text('중단 및 롤백'), 200);
        expect(find.text('중단 및 롤백'), findsOneWidget);
        expect(find.text('현재 선택과 불일치'), findsOneWidget);
        expect(
          find.semantics.byLabel(
            RegExp(r'최근 정책 결정[\s\S]*현재 안전이나 수익성을 뜻하지 않습니다'),
          ),
          findsOneWidget,
        );
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets('expired ownership warning is exposed to assistive technology', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    final api = _PaperApi()
      ..next = PaperMonitor.fromJson(paperMonitorJson(runnerState: 'expired'));
    try {
      await tester.pumpWidget(MaterialApp(home: PaperMonitorPage(api: api)));
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(find.text('만료된 소유권 기록'), 200);
      expect(
        find.semantics.byLabel(RegExp(r'만료된 소유권 기록[\s\S]*인계 가능 여부를 판단하지 않습니다')),
        findsOneWidget,
      );
    } finally {
      semantics.dispose();
    }
  });

  testWidgets('320px at 200 percent stays usable in light and dark themes', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(320, 640);
    tester.view.devicePixelRatio = 1;
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    final semantics = tester.ensureSemantics();
    final api = _PaperApi()
      ..next = PaperMonitor.fromJson(
        paperMonitorJson(
          selected: true,
          sessions: 8,
          pending: 3,
          runnerState: 'selection_changed',
          latestPolicy: haltPolicy(),
        ),
      );

    try {
      for (final mode in [ThemeMode.light, ThemeMode.dark]) {
        await tester.pumpWidget(
          MaterialApp(
            theme: ThemeData.light(useMaterial3: true),
            darkTheme: ThemeData.dark(useMaterial3: true),
            themeMode: mode,
            home: PaperMonitorPage(api: api),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.byKey(const Key('paper-monitor-scroll')), findsOneWidget);
        expect(
          find.semantics.byLabel(RegExp('관측 시각 2026-09-05T01:02:03Z')),
          findsOneWidget,
        );
        expect(tester.takeException(), isNull);
      }
    } finally {
      semantics.dispose();
    }
  });

  testWidgets('completion after disposal does not update the page', (
    tester,
  ) async {
    final pending = Completer<PaperMonitor>();
    final api = _PaperApi()..pending = pending;
    await tester.pumpWidget(MaterialApp(home: PaperMonitorPage(api: api)));
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsOneWidget);

    await tester.pumpWidget(const MaterialApp(home: SizedBox()));
    pending.complete(PaperMonitor.fromJson(paperMonitorJson()));
    await tester.pump();

    expect(tester.takeException(), isNull);
  });
}

class _PaperApi implements OmniApi {
  PaperMonitor? next;
  Completer<PaperMonitor>? pending;
  Object? failure;
  int calls = 0;

  @override
  Future<PaperMonitor> paperMonitor() async {
    calls += 1;
    if (failure case final failure?) throw failure;
    return pending?.future ?? next!;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

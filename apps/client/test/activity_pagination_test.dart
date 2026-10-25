import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';

typedef Json = Map<String, dynamic>;

Json _event(int sequence, {String? occurredAt, String? amount}) => {
  'type': 'DEPOSIT',
  'occurred_at':
      occurredAt ??
      DateTime.utc(2026, 1, 2)
          .subtract(Duration(seconds: sequence))
          .toIso8601String()
          .replaceFirst('.000Z', 'Z'),
  'recorded_at': '2026-01-10T15:01:00Z',
  'symbol': null,
  'quantity': null,
  'price': null,
  'fee': null,
  'currency': 'USD',
  'amount': amount ?? '${sequence + 1}',
  'counter_currency': null,
  'counter_amount': null,
  'is_correction': false,
};

Json _page({
  required List<Json> events,
  String revision = 'rev_0000000101',
  String recordedAt = '2026-01-10T15:01:00Z',
  String? nextCursor,
}) => {
  'source': 'local_ledger',
  'broker_freshness': 'unverified',
  'ledger_revision': revision,
  'recorded_at': recordedAt,
  'events': events,
  'next_cursor': nextCursor,
};

LedgerActivityPage _modelPage({
  required List<Json> events,
  String revision = 'rev_0000000101',
  String recordedAt = '2026-01-10T15:01:00Z',
  String? nextCursor,
}) => LedgerActivityPage.fromJson(
  _page(
    events: events,
    revision: revision,
    recordedAt: recordedAt,
    nextCursor: nextCursor,
  ),
);

Widget _activityApp(OmniApi api, {ThemeMode themeMode = ThemeMode.light}) =>
    MaterialApp(
      theme: ThemeData.light(useMaterial3: true),
      darkTheme: ThemeData.dark(useMaterial3: true),
      themeMode: themeMode,
      home: Scaffold(
        body: SafeArea(child: ActivityPage(api: api)),
      ),
    );

Future<void> _pumpUi(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 300));
}

Future<void> _tapVisible(WidgetTester tester, Finder finder) async {
  await tester.scrollUntilVisible(
    finder,
    400,
    scrollable: find.byType(Scrollable).first,
  );
  await tester.pump();
  await tester.tap(finder);
  await _pumpUi(tester);
}

class _ActivityApi implements OmniApi {
  @override
  Future<PortfolioTwr> portfolioTwr() async => throw UnimplementedError();

  @override
  Future<PortfolioXirr> portfolioXirr() =>
      throw UnsupportedError('XIRR is loaded only from its detail page.');

  _ActivityApi({required this.first, this.previewValue, this.receiptValue});

  LedgerActivityPage first;
  final ImportPreview? previewValue;
  final ApplyReceipt? receiptValue;
  final List<String?> cursors = [];
  final Map<String?, Completer<LedgerActivityPage>> pending = {};
  final Map<String?, LedgerActivityPage> pages = {};

  @override
  Future<LedgerActivityPage> ledgerActivities({String? cursor}) {
    cursors.add(cursor);
    final completer = pending[cursor];
    if (completer != null) return completer.future;
    return Future.value(pages[cursor] ?? first);
  }

  @override
  Future<ImportPreview> preview(String csv) async => previewValue!;

  @override
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey) async =>
      receiptValue!;

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

void main() {
  test(
    'trade cash accepts zero and negative net sales, rejects mismatches',
    () {
      for (final entry in {'1': '0', '1.1': '-0.1'}.entries) {
        final row = _event(1)
          ..addAll({
            'type': 'SELL',
            'symbol': 'AAPL',
            'quantity': '0.5',
            'price': '2',
            'fee': entry.key,
            'amount': entry.value,
          });
        expect(LedgerActivity.fromJson(row).amount, entry.value);
        row['amount'] = '100';
        expect(() => LedgerActivity.fromJson(row), throwsFormatException);
      }
    },
  );
  test(
    'RestOmniApi round-trips an opaque activity cursor through Uri query',
    () async {
      late http.Request request;
      final api = RestOmniApi(
        client: MockClient((value) async {
          request = value;
          return http.Response(jsonEncode(_page(events: [_event(0)])), 200);
        }),
      );

      await api.ledgerActivities(cursor: 'opaque /+=?&cursor');

      expect(request.method, 'GET');
      expect(request.url.path, '/v1/ledger/activities');
      expect(request.url.queryParameters, {'cursor': 'opaque /+=?&cursor'});
    },
  );

  test('activity pages reject oversize and nonterminal short pages', () {
    expect(
      () => _modelPage(events: List.generate(51, _event)),
      throwsFormatException,
    );
    expect(
      () => _modelPage(
        events: List.generate(49, _event),
        nextCursor: 'opaque-next',
      ),
      throwsFormatException,
    );
    expect(
      () => _modelPage(events: const [], nextCursor: 'opaque-next'),
      throwsFormatException,
    );
  });

  test(
    'activity ordering and continuation bounds preserve exact nanoseconds',
    () {
      expect(
        () => _modelPage(
          events: [
            _event(0, occurredAt: '2026-01-02T00:00:00.123456789Z'),
            _event(1, occurredAt: '2026-01-02T00:00:00.123456799Z'),
          ],
        ),
        throwsFormatException,
      );

      final first = _modelPage(
        events: List.generate(
          50,
          (index) => _event(
            index,
            occurredAt: index == 49
                ? '2026-01-02T00:00:00.123456789Z'
                : DateTime.utc(2026, 1, 3)
                      .subtract(Duration(seconds: index))
                      .toIso8601String()
                      .replaceFirst('.000Z', 'Z'),
          ),
        ),
        nextCursor: 'opaque-page-2',
      );
      final newerContinuation = _modelPage(
        events: [_event(50, occurredAt: '2026-01-02T00:00:00.123456799Z')],
      );
      expect(
        () => newerContinuation.requireContinuationOf(
          initial: first,
          upperBoundOccurredAt: first.events.last.occurredAt,
          requestedCursor: 'opaque-page-2',
        ),
        throwsFormatException,
      );

      final equalTimestampContinuation = _modelPage(
        events: [
          _event(
            50,
            occurredAt: '2026-01-02T00:00:00.123456789Z',
            amount: '999',
          ),
        ],
      );
      expect(
        () => equalTimestampContinuation.requireContinuationOf(
          initial: first,
          upperBoundOccurredAt: first.events.last.occurredAt,
          requestedCursor: 'opaque-page-2',
        ),
        returnsNormally,
      );

      final olderThanLowerBound = _modelPage(
        events: [_event(51, occurredAt: '2026-01-02T00:00:00.123456798Z')],
      );
      expect(
        () => olderThanLowerBound.requireContinuationOf(
          initial: first,
          upperBoundOccurredAt: '2026-01-02T00:00:01Z',
          requestedCursor: 'opaque-page-2',
          lowerBoundOccurredAt: '2026-01-02T00:00:00.123456799Z',
        ),
        throwsFormatException,
      );
      expect(
        () => equalTimestampContinuation.requireContinuationOf(
          initial: first,
          upperBoundOccurredAt: first.events.last.occurredAt,
          requestedCursor: 'opaque-page-2',
          lowerBoundOccurredAt: '2026-01-02T00:00:00.123456789Z',
        ),
        returnsNormally,
      );
    },
  );

  test(
    'continuations must be nonempty and match initial snapshot metadata',
    () {
      final first = _modelPage(
        events: List.generate(50, _event),
        nextCursor: 'opaque-page-2',
      );
      final invalid = [
        _modelPage(events: const []),
        _modelPage(events: [_event(50)], revision: 'rev_0000000102'),
        _modelPage(events: [_event(50)], recordedAt: '2026-01-10T15:01:01Z'),
      ];
      for (final page in invalid) {
        expect(
          () => page.requireContinuationOf(
            initial: first,
            upperBoundOccurredAt: first.events.last.occurredAt,
            requestedCursor: 'opaque-page-2',
          ),
          throwsFormatException,
        );
      }
    },
  );

  test(
    'continuations reject an exact cursor loop without comparing payloads',
    () {
      final first = _modelPage(
        events: List.generate(50, _event),
        nextCursor: 'opaque-page-2',
      );
      final repeatedCursor = _modelPage(
        events: List.generate(50, (index) => _event(index + 50)),
        nextCursor: 'opaque-page-2',
      );
      expect(
        () => repeatedCursor.requireContinuationOf(
          initial: first,
          upperBoundOccurredAt: first.events.last.occurredAt,
          requestedCursor: 'opaque-page-2',
        ),
        throwsFormatException,
      );

      final identicalPublicEvents = List<Json>.generate(
        50,
        (_) => _event(50, occurredAt: '2026-01-01T23:59:11Z', amount: '1'),
      );
      final randomizedCursor = _modelPage(
        events: identicalPublicEvents,
        nextCursor: 'new-random-nonce',
      );
      expect(
        () => randomizedCursor.requireContinuationOf(
          initial: first,
          upperBoundOccurredAt: first.events.last.occurredAt,
          requestedCursor: 'opaque-page-2',
        ),
        returnsNormally,
      );
    },
  );

  testWidgets(
    'real Rest transport pages 101 events across three bounded pages and returns to the preserved first page',
    (tester) async {
      const cursor2 = 'opaque /page-2?+=&';
      const cursor3a = 'opaque-page-3-a';
      final calls = <String?>[];
      var firstLoads = 0;
      final all = List<Json>.generate(101, _event);
      final api = RestOmniApi(
        client: MockClient((request) async {
          final cursor = request.url.queryParameters['cursor'];
          calls.add(cursor);
          if (cursor == null) {
            firstLoads += 1;
            return http.Response(
              jsonEncode(
                _page(
                  events: all.sublist(0, 50),
                  nextCursor: cursor2,
                  revision: firstLoads == 1
                      ? 'rev_0000000101'
                      : 'rev_0000000102',
                ),
              ),
              200,
            );
          }
          if (cursor == cursor2) {
            return http.Response(
              jsonEncode(
                _page(events: all.sublist(50, 100), nextCursor: cursor3a),
              ),
              200,
            );
          }
          if (cursor == cursor3a) {
            return http.Response(
              jsonEncode(_page(events: all.sublist(100))),
              200,
            );
          }
          return http.Response('{}', 400);
        }),
      );

      await tester.pumpWidget(_activityApp(api));
      await _pumpUi(tester);
      expect(find.text('입금'), findsNWidgets(50));
      expect(find.text('페이지 1 · 50건'), findsOneWidget);

      await _tapVisible(tester, find.text('더 오래된 거래'));
      expect(find.text('입금'), findsNWidgets(50));
      expect(find.text('페이지 2 · 50건'), findsOneWidget);

      await _tapVisible(tester, find.text('더 오래된 거래'));
      expect(find.text('입금'), findsOneWidget);
      expect(find.text('페이지 3 · 1건'), findsOneWidget);

      await _tapVisible(tester, find.text('더 최근 거래'));
      expect(find.text('페이지 2 · 50건'), findsOneWidget);
      await _tapVisible(tester, find.text('더 최근 거래'));
      expect(find.text('페이지 1 · 50건'), findsOneWidget);
      expect(find.textContaining('원장 rev_0000000101'), findsOneWidget);
      expect(
        firstLoads,
        1,
        reason: 'returning to page 1 must use its snapshot',
      );

      await _tapVisible(tester, find.text('더 오래된 거래'));
      await _tapVisible(tester, find.text('더 오래된 거래'));
      expect(find.text('페이지 3 · 1건'), findsOneWidget);
      expect(calls, [null, cursor2, cursor3a, cursor2, cursor2, cursor3a]);
    },
  );

  testWidgets(
    'continuation failure retains the whole page and refresh recovers',
    (tester) async {
      final firstEvents = List<Json>.generate(50, _event);
      var firstLoads = 0;
      final api = RestOmniApi(
        client: MockClient((request) async {
          final cursor = request.url.queryParameters['cursor'];
          if (cursor != null) {
            return http.Response(
              jsonEncode({'message': 'account-main cursor-secret'}),
              400,
            );
          }
          firstLoads += 1;
          return http.Response(
            jsonEncode(
              _page(
                events: firstLoads == 1
                    ? firstEvents
                    : [_event(0, amount: '999')],
                revision: firstLoads == 1 ? 'rev_0000000101' : 'rev_0000000102',
                nextCursor: firstLoads == 1 ? 'server-restart-invalid' : null,
              ),
            ),
            200,
          );
        }),
      );

      await tester.pumpWidget(_activityApp(api));
      await _pumpUi(tester);
      await _tapVisible(tester, find.text('더 오래된 거래'));

      expect(find.text('입금'), findsNWidgets(50));
      expect(find.text('페이지 1 · 50건'), findsOneWidget);
      expect(find.textContaining('account-main'), findsNothing);
      expect(find.textContaining('cursor-secret'), findsNothing);
      expect(find.text('거래 페이지를 불러오지 못해 현재 페이지를 유지합니다.'), findsOneWidget);

      await _tapVisible(tester, find.text('최근 거래 다시 불러오기'));
      expect(find.text('페이지 1 · 1건'), findsOneWidget);
      expect(find.text('USD 999'), findsOneWidget);
      expect(find.textContaining('원장 rev_0000000102'), findsOneWidget);
    },
  );

  testWidgets(
    'newer navigation rejects a page below the current nanosecond bound',
    (tester) async {
      var page2Loads = 0;
      final api = RestOmniApi(
        client: MockClient((request) async {
          final cursor = request.url.queryParameters['cursor'];
          if (cursor == null) {
            return http.Response(
              jsonEncode(
                _page(events: List.generate(50, _event), nextCursor: 'page-2'),
              ),
              200,
            );
          }
          if (cursor == 'page-2') {
            page2Loads += 1;
            return http.Response(
              jsonEncode(
                _page(
                  events: List.generate(
                    50,
                    (index) => _event(index + (page2Loads == 1 ? 50 : 101)),
                  ),
                  nextCursor: page2Loads == 1 ? 'page-3' : 'fresh-page-3',
                ),
              ),
              200,
            );
          }
          if (cursor == 'page-3') {
            return http.Response(jsonEncode(_page(events: [_event(100)])), 200);
          }
          return http.Response('{}', 400);
        }),
      );

      await tester.pumpWidget(_activityApp(api));
      await _pumpUi(tester);
      await _tapVisible(tester, find.text('더 오래된 거래'));
      await _tapVisible(tester, find.text('더 오래된 거래'));
      expect(find.text('페이지 3 · 1건'), findsOneWidget);
      expect(find.text('USD 101'), findsOneWidget);

      await _tapVisible(tester, find.text('더 최근 거래'));

      expect(find.text('페이지 3 · 1건'), findsOneWidget);
      expect(find.text('USD 101'), findsOneWidget);
      expect(find.text('거래 페이지를 불러오지 못해 현재 페이지를 유지합니다.'), findsOneWidget);
    },
  );

  testWidgets(
    'successful import queues a first-page refresh and stale continuation cannot overwrite it',
    (tester) async {
      final initial = _modelPage(
        events: List.generate(50, _event),
        nextCursor: 'page-2',
      );
      final page2Pending = Completer<LedgerActivityPage>();
      final refreshPending = Completer<LedgerActivityPage>();
      final preview = ImportPreview.fromJson({
        'preview_id': 'preview-1',
        'file_sha256': 'fixture-sha',
        'schema_version': 'canonical-transaction.v1',
        'mapping_version': 'canonical-transaction.v1',
        'ledger_revision': 'rev_0000000101',
        'preview_fingerprint': 'fixture-fingerprint',
        'can_apply': true,
        'totals': {
          'total_rows': 0,
          'new_rows': 0,
          'duplicate_rows': 0,
          'error_rows': 0,
          'unresolved_rows': 0,
        },
        'rows': const [],
      });
      final receipt = ApplyReceipt(
        receiptId: 'receipt-1',
        appliedRows: 1,
        skippedDuplicateRows: 0,
        ledgerRevisionAfter: 'rev_0000000102',
        recordedAt: DateTime.utc(2026, 1, 10, 15, 2),
      );
      final api =
          _ActivityApi(
              first: initial,
              previewValue: preview,
              receiptValue: receipt,
            )
            ..pending['page-2'] = page2Pending
            ..pending[null] = refreshPending;
      // Let the initial first-page request complete without the later refresh completer.
      api.pending.remove(null);

      await tester.pumpWidget(_activityApp(api));
      await _pumpUi(tester);
      await _tapVisible(tester, find.text('더 오래된 거래'));
      await tester.fling(find.byType(ListView), const Offset(0, 10000), 3000);
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), 'header\nrow');
      await tester.tap(find.text('미리보기 만들기'));
      await _pumpUi(tester);
      await _tapVisible(tester, find.text('원자적으로 적용'));
      api.pending[null] = refreshPending;

      page2Pending.complete(_modelPage(events: [_event(50, amount: '666')]));
      await _pumpUi(tester);
      expect(api.cursors, [null, 'page-2', null]);

      refreshPending.complete(
        _modelPage(
          events: [_event(0, amount: '777')],
          revision: 'rev_0000000102',
          recordedAt: '2026-01-10T15:02:00Z',
        ),
      );
      await _pumpUi(tester);

      await tester.scrollUntilVisible(
        find.text('USD 777'),
        400,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('USD 777'), findsOneWidget);
      expect(find.text('USD 666'), findsNothing);
      expect(find.text('페이지 1 · 1건'), findsOneWidget);
    },
  );

  testWidgets('duplicate pagination taps issue one request', (tester) async {
    final pending = Completer<LedgerActivityPage>();
    final api = _ActivityApi(
      first: _modelPage(
        events: List.generate(50, _event),
        nextCursor: 'page-2',
      ),
    )..pending['page-2'] = pending;

    await tester.pumpWidget(_activityApp(api));
    await _pumpUi(tester);
    final older = find.text('더 오래된 거래');
    await tester.scrollUntilVisible(
      older,
      400,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.tap(older);
    await tester.tap(older);
    await tester.pump();

    expect(api.cursors.where((cursor) => cursor == 'page-2'), hasLength(1));
    pending.complete(_modelPage(events: [_event(50)]));
    await _pumpUi(tester);
  });

  testWidgets(
    'refresh coalesces while a continuation is pending and ignores its stale failure',
    (tester) async {
      final page2Pending = Completer<LedgerActivityPage>();
      final refreshPending = Completer<LedgerActivityPage>();
      final api = _ActivityApi(
        first: _modelPage(
          events: List.generate(50, _event),
          nextCursor: 'page-2',
        ),
      )..pending['page-2'] = page2Pending;

      await tester.pumpWidget(_activityApp(api));
      await _pumpUi(tester);
      await tester.scrollUntilVisible(
        find.text('더 오래된 거래'),
        400,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.tap(find.text('더 오래된 거래'));
      await tester.pump();
      api.pending[null] = refreshPending;

      final refresh = find.text('최근 거래 다시 불러오기');
      await tester.tap(refresh);
      await tester.tap(refresh);
      page2Pending.completeError(const ApiException('private cursor failure'));
      await _pumpUi(tester);

      expect(api.cursors, [null, 'page-2', null]);
      expect(find.textContaining('private cursor failure'), findsNothing);
      expect(find.text('거래 페이지를 불러오지 못해 현재 페이지를 유지합니다.'), findsNothing);

      refreshPending.complete(
        _modelPage(
          events: [_event(0, amount: '888')],
          revision: 'rev_0000000102',
          recordedAt: '2026-01-10T15:02:00Z',
        ),
      );
      await _pumpUi(tester);
      expect(find.text('USD 888'), findsOneWidget);
      expect(find.text('페이지 1 · 1건'), findsOneWidget);
    },
  );

  testWidgets('pagination completion after disposal is ignored', (
    tester,
  ) async {
    final pending = Completer<LedgerActivityPage>();
    final api = _ActivityApi(
      first: _modelPage(
        events: List.generate(50, _event),
        nextCursor: 'page-2',
      ),
    )..pending['page-2'] = pending;

    await tester.pumpWidget(_activityApp(api));
    await _pumpUi(tester);
    await tester.scrollUntilVisible(
      find.text('더 오래된 거래'),
      400,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.tap(find.text('더 오래된 거래'));
    await tester.pumpWidget(const MaterialApp(home: SizedBox()));
    pending.complete(_modelPage(events: [_event(50)]));
    await _pumpUi(tester);

    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'pagination controls remain accessible at 320px, dark, 200 percent text, and reduced motion',
    (tester) async {
      tester.view.physicalSize = const Size(320, 640);
      tester.view.devicePixelRatio = 1;
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      tester.binding.platformDispatcher.accessibilityFeaturesTestValue =
          const FakeAccessibilityFeatures(
            disableAnimations: true,
            reduceMotion: true,
          );
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      addTearDown(
        tester.binding.platformDispatcher.clearAccessibilityFeaturesTestValue,
      );
      final semantics = tester.ensureSemantics();
      final api = _ActivityApi(
        first: _modelPage(
          events: List.generate(50, _event),
          nextCursor: 'page-2',
        ),
      );

      await tester.pumpWidget(_activityApp(api, themeMode: ThemeMode.dark));
      await _pumpUi(tester);
      final older = find.text('더 오래된 거래');
      await tester.scrollUntilVisible(
        older,
        400,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.pump();

      expect(
        tester.getSize(find.widgetWithText(OutlinedButton, '더 오래된 거래')).height,
        greaterThanOrEqualTo(48),
      );
      expect(find.semantics.byLabel('더 오래된 거래'), findsOneWidget);
      expect(
        tester.getTopLeft(older).dy,
        lessThan(tester.getTopLeft(find.text('입금').first).dy),
      );
      expect(
        Theme.of(tester.element(find.byType(ActivityPage))).brightness,
        Brightness.dark,
      );
      expect(tester.takeException(), isNull);
      semantics.dispose();
    },
  );
}

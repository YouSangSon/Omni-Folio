import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/watchlist.dart';
import 'package:omni_folio_client/watchlist_page.dart';

import 'vertical_slice_test.dart' show goldenApi;

const _now = '2026-10-28T12:00:00Z';
const _observed = '2026-10-28T10:00:00Z';
const _fetched = '2026-10-28T11:00:00Z';
const _recorded = '2026-10-28T11:30:00Z';

WatchlistEntry _entry(String venue, String symbol) => WatchlistEntry.fromJson({
  'venue': venue,
  'symbol': symbol,
  'currency': venue == 'XKRX' ? 'KRW' : 'USD',
});

final _kr = _entry('XKRX', '005930');
final _us = _entry('XNAS', 'AAPL');

Json _stateJson(int revision, List<WatchlistEntry> entries) => {
  'revision': revision,
  'updated_at': revision == 0 ? null : _recorded,
  'entries': entries.map((entry) => entry.toJson()).toList(),
};

WatchlistState _state(int revision, List<WatchlistEntry> entries) =>
    WatchlistState.fromJson(_stateJson(revision, entries));

Json _reportJson(
  int revision,
  List<WatchlistEntry> entries, {
  List<Json>? quotes,
  String observedAt = _now,
}) => {
  'policy_version': 'watchlist_stored_prices_24h.v1',
  'max_observation_age_seconds': 86400,
  'observed_at': observedAt,
  'watchlist': _stateJson(revision, entries),
  'quotes':
      quotes ??
      [
        for (final entry in entries)
          {...entry.toJson(), 'status': 'missing', 'price': null},
      ],
};

WatchlistReport _report(
  int revision,
  List<WatchlistEntry> entries, {
  List<Json>? quotes,
  String observedAt = _now,
}) => WatchlistReport.fromJson(
  _reportJson(revision, entries, quotes: quotes, observedAt: observedAt),
);

Json _priceQuote(
  WatchlistEntry entry, {
  String status = 'unknown',
  String source = 'owner_declared',
  bool sample = false,
}) => {
  ...entry.toJson(),
  'status': status,
  'price': {
    'source': source,
    'source_version': source == 'owner_declared' ? 'owner-v1' : null,
    'price': '71234.56789',
    'price_adjustment': source == 'owner_declared'
        ? 'unadjusted'
        : 'unspecified',
    'observed_at': status == 'stale' ? '2026-10-26T10:00:00Z' : _observed,
    'fetched_at': _fetched,
    'recorded_at': _recorded,
    'sample': sample,
  },
};

class _Api implements OmniApi {
  Future<WatchlistReport> Function()? read;
  Future<WatchlistState> Function(WatchlistChange)? write;
  int reads = 0;
  final requests = <WatchlistChange>[];

  @override
  Future<WatchlistReport> watchlist() {
    reads++;
    return read!();
  }

  @override
  Future<WatchlistState> updateWatchlist(WatchlistChange request) {
    requests.add(request);
    return write!(request);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _AppApi extends _Api {
  final base = goldenApi()..fail = true;

  @override
  Future<ServiceStatus> status() => base.status();
  @override
  Future<PortfolioSnapshot> snapshot() => base.snapshot();
  @override
  Future<CurrentPortfolioValuation> currentPortfolioValuation() =>
      base.currentPortfolioValuation();
  @override
  Future<BrokerReconciliation?> latestBrokerReconciliation() =>
      base.latestBrokerReconciliation();
  @override
  Future<LocalOrderLog> localOrders() => base.localOrders();
}

void main() {
  test(
    'a changed list cannot be acknowledged without advancing revision',
    () async {
      final api = _Api();
      api.read = () async => _report(2, [_kr]);
      api.write = (_) async => _state(2, [_kr, _us]);
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await controller.refresh();
      expect(await controller.add(_us), isFalse);
      expect(controller.pending, isNotNull);
      expect(controller.canEdit, isFalse);
      expect(controller.state!.sameEntries([_kr]), isTrue);
      expect(api.reads, 1);
    },
  );
  test(
    'reads and writes are single-flight and a failed post-ACK read hides old quotes',
    () async {
      final firstRead = Completer<WatchlistReport>();
      final acknowledgement = Completer<WatchlistState>();
      final nextRead = Completer<WatchlistReport>();
      final api = _Api();
      api.read = () => firstRead.future;
      api.write = (_) => acknowledgement.future;
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);

      final load = controller.refresh();
      controller.ensureLoaded();
      await controller.refresh();
      expect(api.reads, 1);
      expect(await controller.add(_kr), isFalse);
      firstRead.complete(_report(0, []));
      await load;

      api.read = () => nextRead.future;
      final saving = controller.add(_kr);
      expect(await controller.add(_us), isFalse);
      await controller.refresh();
      expect(api.requests, hasLength(1));
      expect(api.reads, 1);
      acknowledgement.complete(_state(1, [_kr]));
      await Future<void>.delayed(Duration.zero);
      expect(controller.busy, isTrue);
      expect(controller.state!.revision, 1);
      expect(controller.report, isNull);
      expect(await controller.add(_us), isFalse);
      nextRead.completeError(const FormatException('private-read-sentinel'));
      expect(await saving, isTrue);
      expect(controller.state!.sameEntries([_kr]), isTrue);
      expect(controller.report, isNull);
      expect(controller.message, contains('가격을 확인하지 못했습니다'));
      expect(controller.message, isNot(contains('private-read-sentinel')));

      api.read = () async => _report(1, [_kr]);
      await controller.refresh();
      expect(controller.report!.quotes.single.status, 'missing');
      expect(controller.canEdit, isTrue);
    },
  );

  test('disposed controller ignores a late read or write', () async {
    final lateRead = Completer<WatchlistReport>();
    final api = _Api()..read = () => lateRead.future;
    final controller = WatchlistController(api);
    var notifications = 0;
    controller.addListener(() => notifications++);
    final load = controller.refresh();
    controller.dispose();
    lateRead.complete(_report(0, []));
    await load;
    expect(notifications, 1);
    expect(controller.state, isNull);

    final lateWrite = Completer<WatchlistState>();
    final writer = _Api();
    writer.read = () async => _report(0, []);
    writer.write = (_) => lateWrite.future;
    final second = WatchlistController(writer);
    await second.refresh();
    final saving = second.add(_kr);
    second.dispose();
    lateWrite.complete(_state(1, [_kr]));
    expect(await saving, isFalse);
    expect(writer.reads, 1);
  });

  test(
    'uncertain retries keep exactly the immutable intent through rejection and invalid ACK',
    () async {
      var attempts = 0;
      final api = _Api()..read = () async => _report(0, []);
      api.write = (request) async {
        attempts++;
        switch (attempts) {
          case 1:
            throw TimeoutException('private-timeout');
          case 2:
            throw const ApiException(
              'private-rejection',
              statusCode: 403,
              code: 'watchlist_origin_rejected',
            );
          case 3:
            return _state(1, [_us]);
          case 4:
            return _state(4, [_kr]);
          case 5:
            throw const ApiException(
              'private-server',
              statusCode: 500,
              code: 'watchlist_invalid',
            );
          default:
            return _state(1, [_kr]);
        }
      };
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await controller.refresh();
      expect(await controller.add(_kr), isFalse);
      final pending = controller.pending!;
      final intent = jsonEncode(pending.toJson());
      expect(() => pending.entries.add(_us), throwsUnsupportedError);
      expect(await controller.add(_us), isFalse);
      await controller.retry();
      expect(controller.pending, same(pending));
      await controller.retry();
      expect(controller.pending, same(pending));
      expect(controller.message, isNot(contains('private')));
      await controller.retry();
      expect(controller.pending, same(pending));
      await controller.retry();
      expect(controller.pending, same(pending));
      api.read = () async => _report(1, [_kr]);
      expect(await controller.retry(), isTrue);
      expect(controller.pending, isNull);
      expect(
        api.requests.map((value) => jsonEncode(value.toJson())),
        everyElement(intent),
      );
      expect(api.reads, 2);
    },
  );

  test(
    'conflict reload preserves earlier uncertainty across same or rolled-back revision',
    () async {
      var attempt = 0;
      var current = _report(2, [_kr]);
      final api = _Api()..read = () async => current;
      api.write = (_) async {
        attempt++;
        if (attempt == 1) throw const FormatException('private-parser-error');
        throw const ApiException(
          'private-conflict',
          statusCode: 409,
          code: 'watchlist_changed',
        );
      };
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await controller.refresh();
      await controller.add(_us);
      final pending = controller.pending;
      await controller.retry();
      expect(controller.pending, same(pending));
      expect(controller.state!.revision, 2);
      expect(controller.message, isNot(contains('private')));

      current = _report(1, [_kr, _us]);
      await controller.refresh();
      expect(controller.pending, same(pending));
      expect(controller.message, contains('이전 버전'));
      expect(controller.canEdit, isFalse);

      current = _report(3, [_kr]);
      await controller.refresh();
      expect(controller.pending, isNull);
      expect(controller.canEdit, isTrue);
      expect(controller.message, contains('적용 여부는 확인할 수 없습니다'));
    },
  );

  test(
    'first confirmed rejection releases the request and conflict reloads the actual list',
    () async {
      for (final (status, code) in [
        (400, 'watchlist_invalid'),
        (403, 'watchlist_origin_rejected'),
      ]) {
        final api = _Api();
        api.read = () async => _report(0, []);
        api.write = (_) async =>
            throw ApiException('private', statusCode: status, code: code);
        final controller = WatchlistController(api);
        await controller.refresh();
        await controller.add(_kr);
        expect(controller.pending, isNull);
        expect(controller.canEdit, isTrue);
        expect(api.reads, 1);
        controller.dispose();
      }
      var reads = 0;
      final api = _Api();
      api.read = () async => ++reads == 1 ? _report(0, []) : _report(1, [_us]);
      api.write = (_) async => throw const ApiException(
        'private',
        statusCode: 409,
        code: 'watchlist_changed',
      );
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await controller.refresh();
      await controller.add(_kr);
      expect(controller.pending, isNull);
      expect(controller.state!.sameEntries([_us]), isTrue);
      expect(controller.message, contains('다른 곳에서 변경'));
    },
  );

  test(
    'a conflict with a failed reload blocks edits until current state is read',
    () async {
      var reads = 0;
      final api = _Api()
        ..read = () async {
          reads++;
          if (reads == 2) throw const ApiException('private');
          return _report(reads == 1 ? 0 : 1, reads == 1 ? [] : [_us]);
        }
        ..write = (_) async => throw const ApiException(
          'private',
          statusCode: 409,
          code: 'watchlist_changed',
        );
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await controller.refresh();
      await controller.add(_kr);
      expect(controller.pending, isNull);
      expect(controller.canEdit, isFalse);
      expect(controller.report!.watchlist.revision, 0);
      expect(controller.message, contains('목록 충돌'));
      await controller.refresh();
      expect(controller.canEdit, isTrue);
      expect(controller.state!.sameEntries([_us]), isTrue);
    },
  );

  test(
    'same expected revision resolves only if the desired entries are already present',
    () async {
      var current = _report(2, [_kr]);
      final api = _Api();
      api.read = () async => current;
      api.write = (_) async => throw TimeoutException('private');
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await controller.refresh();
      await controller.add(_us);
      current = _report(2, [_kr, _us]);
      await controller.refresh();
      expect(controller.pending, isNull);
      expect(controller.state!.sameEntries([_kr, _us]), isTrue);
      expect(controller.message, contains('적용 여부는 확인할 수 없습니다'));
    },
  );

  test('duplicates and the 50-entry limit do not send a write', () async {
    var current = _report(1, [_kr]);
    final api = _Api()..read = () async => current;
    final controller = WatchlistController(api);
    addTearDown(controller.dispose);
    await controller.refresh();
    expect(await controller.add(_kr), isFalse);
    expect(controller.message, contains('이미'));
    current = _report(
      2,
      List.generate(
        50,
        (index) => _entry('XKRX', index.toString().padLeft(6, '0')),
      ),
    );
    await controller.refresh();
    expect(await controller.add(_us), isFalse);
    expect(controller.message, contains('최대 50개'));
    expect(api.requests, isEmpty);
  });

  test(
    'failed refresh preserves observation; successful missing quote clears old price',
    () async {
      var read = 0;
      final api = _Api()
        ..read = () async {
          read++;
          if (read == 2) throw const ApiException('private-token');
          return read == 1
              ? _report(1, [_kr], quotes: [_priceQuote(_kr, sample: true)])
              : _report(1, [_kr], observedAt: '2026-10-28T13:00:00Z');
        };
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await controller.refresh();
      final old = controller.report;
      expect(old!.quotes.single.price!.price, '71234.56789');
      await controller.refresh();
      expect(controller.report, same(old));
      expect(controller.report!.observedAt, _now);
      expect(controller.message, isNot(contains('private-token')));
      await controller.refresh();
      expect(controller.report!.observedAt, '2026-10-28T13:00:00Z');
      expect(controller.report!.quotes.single.status, 'missing');
      expect(controller.report!.quotes.single.price, isNull);
    },
  );

  test(
    'strict report parser rejects quote mismatches and invalid price states',
    () {
      final mutations = <void Function(Json)>[
        (value) => value['policy_version'] = 'live',
        (value) => value['max_observation_age_seconds'] = 1,
        (value) => value['quotes'][0]['symbol'] = '000000',
        (value) => value['quotes'][0]['status'] = 'live',
        (value) => value['quotes'][0]['status'] = 'unknown',
        (value) => value['quotes'] = [],
      ];
      for (final mutate in mutations) {
        final wire = _reportJson(1, [_kr]);
        mutate(wire);
        expect(() => WatchlistReport.fromJson(wire), throwsFormatException);
      }
      final fixture = _reportJson(
        1,
        [_kr],
        quotes: [
          _priceQuote(
            _kr,
            status: 'stale',
            source: 'local_fixture',
            sample: true,
          ),
        ],
      );
      expect(
        WatchlistReport.fromJson(fixture).quotes.single.price!.sample,
        isTrue,
      );
      fixture['quotes'][0]['status'] = 'unknown';
      expect(() => WatchlistReport.fromJson(fixture), throwsFormatException);
    },
  );

  for (final (dark, size) in [
    (false, const Size(375, 812)),
    (true, const Size(375, 812)),
    (false, const Size(812, 375)),
    (true, const Size(812, 375)),
  ]) {
    testWidgets(
      'large-text ${dark ? 'dark' : 'light'} $size form is keyboard accessible',
      (tester) async {
        tester.view.physicalSize = size;
        tester.view.devicePixelRatio = 1;
        tester.view.viewInsets = FakeViewPadding(
          bottom: size.width < size.height ? 240 : 120,
        );
        addTearDown(() {
          tester.view.resetPhysicalSize();
          tester.view.resetDevicePixelRatio();
          tester.view.resetViewInsets();
        });
        final semantics = tester.ensureSemantics();
        final api = _Api()..read = () async => _report(0, []);
        final controller = WatchlistController(api);
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
              home: WatchlistPage(controller: controller),
            ),
          );
          await tester.pumpAndSettle();
          final field = find.byKey(const Key('watchlist-symbol'));
          await tester.scrollUntilVisible(
            field,
            180,
            scrollable: find.byType(Scrollable).first,
          );
          await tester.showKeyboard(field);
          await tester.enterText(field, '123');
          await tester.pumpAndSettle();
          expect(
            tester
                .getRect(
                  find.descendant(
                    of: field,
                    matching: find.byType(EditableText),
                  ),
                )
                .bottom,
            lessThanOrEqualTo(size.height - tester.view.viewInsets.bottom),
          );
          final submit = find.widgetWithText(FilledButton, '관심종목 저장');
          await tester.scrollUntilVisible(
            submit,
            180,
            scrollable: find.byType(Scrollable).first,
          );
          expect(tester.getSize(submit).height, greaterThanOrEqualTo(48));
          await tester.tap(submit);
          await tester.pumpAndSettle();
          expect(find.textContaining('숫자 6자리'), findsOneWidget);
          expect(api.requests, isEmpty);
          await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
          expect(tester.takeException(), isNull);
        } finally {
          semantics.dispose();
          await tester.pumpWidget(const SizedBox.shrink());
          controller.dispose();
        }
      },
    );
  }

  testWidgets(
    'owner, sample and stale notices are independent and removal is confirmed',
    (tester) async {
      final api = _Api()
        ..read = () async => _report(
          1,
          [_kr],
          quotes: [_priceQuote(_kr, status: 'stale', sample: true)],
        );
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await tester.pumpWidget(
        MaterialApp(home: WatchlistPage(controller: controller)),
      );
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.text('저장 가격: 71234.56789 KRW'),
        200,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.textContaining('소유자가 입력한 가격'), findsOneWidget);
      expect(find.text('예시 가격입니다.'), findsOneWidget);
      expect(find.text('오래된 가격입니다.'), findsOneWidget);
      expect(find.text('가격 관측 시각: 2026-10-26T10:00:00Z'), findsOneWidget);
      final remove = find.byTooltip('005930 삭제');
      await tester.scrollUntilVisible(
        remove,
        200,
        scrollable: find.byType(Scrollable).first,
      );
      expect(tester.getSize(remove).shortestSide, greaterThanOrEqualTo(48));
      await tester.tap(remove);
      await tester.pumpAndSettle();
      expect(find.textContaining('XKRX 005930 (KRW)'), findsOneWidget);
      await tester.tap(find.text('취소'));
      await tester.pumpAndSettle();
      expect(api.requests, isEmpty);
      await tester.tap(remove);
      await tester.pumpAndSettle();
      api.write = (_) async => _state(2, []);
      api.read = () async => _report(2, []);
      await tester.tap(find.text('삭제'));
      await tester.pumpAndSettle();
      expect(api.requests.single.entries, isEmpty);
      expect(controller.state!.entries, isEmpty);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'US input is trimmed and uppercased while an uncertain form is preserved',
    (tester) async {
      final api = _Api();
      api.read = () async => _report(0, []);
      api.write = (_) async => throw const FormatException('private');
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await tester.pumpWidget(
        MaterialApp(home: WatchlistPage(controller: controller)),
      );
      await tester.pumpAndSettle();
      final venue = find.byType(DropdownButtonFormField<String>);
      await tester.scrollUntilVisible(
        venue,
        180,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.tap(venue);
      await tester.pumpAndSettle();
      await tester.tap(find.text('미국 XNAS · USD').last);
      await tester.pumpAndSettle();
      final symbol = find.byKey(const Key('watchlist-symbol'));
      await tester.scrollUntilVisible(
        symbol,
        180,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.enterText(symbol, '  aapl  ');
      final save = find.widgetWithText(FilledButton, '관심종목 저장');
      await tester.scrollUntilVisible(
        save,
        180,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.tap(save);
      await tester.pumpAndSettle();
      expect(tester.widget<TextField>(symbol).controller!.text, '  aapl  ');
      expect(api.requests.single.entries.single.key, _us.key);
      expect(controller.pending, isNotNull);
      expect(find.textContaining('private'), findsNothing);
      final original = jsonEncode(api.requests.single.toJson());
      api.write = (_) async => _state(1, [_us]);
      api.read = () async => _report(1, [_us]);
      await tester.scrollUntilVisible(
        find.text('같은 변경 다시 시도'),
        180,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.tap(find.text('같은 변경 다시 시도'));
      await tester.pumpAndSettle();
      expect(jsonEncode(api.requests.last.toJson()), original);
      await tester.scrollUntilVisible(
        find.text('저장 가격 없음'),
        180,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('저장 가격 없음'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets('ambiguous and missing prices are never shown as zero', (
    tester,
  ) async {
    final api = _Api()
      ..read = () async => _report(
        1,
        [_kr, _us],
        quotes: [
          {..._kr.toJson(), 'status': 'missing', 'price': null},
          {..._us.toJson(), 'status': 'ambiguous', 'price': null},
        ],
      );
    final controller = WatchlistController(api);
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(home: WatchlistPage(controller: controller)),
    );
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.text('저장 가격 없음'),
      180,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.text('저장 가격 없음'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.text('출처 확인 필요'),
      180,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.text('출처 확인 필요'), findsOneWidget);
    expect(find.textContaining('저장 가격: 0'), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets(
    'root route stays available on portfolio failure and keeps a pending POST across back',
    (tester) async {
      tester.platformDispatcher.accessibilityFeaturesTestValue =
          FakeAccessibilityFeatures(disableAnimations: true);
      addTearDown(
        tester.platformDispatcher.clearAccessibilityFeaturesTestValue,
      );
      final gate = Completer<WatchlistState>();
      final api = _AppApi()..read = () async => _report(0, []);
      api.write = (_) => gate.future;
      await tester.pumpWidget(OmniFolioApp(api: api));
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('관심종목'));
      await tester.pumpAndSettle();
      final field = find.byKey(const Key('watchlist-symbol'));
      await tester.scrollUntilVisible(
        field,
        180,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.enterText(field, '005930');
      final save = find.widgetWithText(FilledButton, '관심종목 저장');
      await tester.scrollUntilVisible(
        save,
        180,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.tap(save);
      await tester.pump();
      expect(api.requests, hasLength(1));
      expect(find.text('관심종목 저장 확인 중'), findsOneWidget);
      expect(find.byType(LinearProgressIndicator), findsNothing);
      await tester.pageBack();
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('관심종목'));
      await tester.pumpAndSettle();
      expect(api.requests, hasLength(1));
      expect(find.text('관심종목 저장 확인 중'), findsOneWidget);
      expect(
        tester
            .widget<TextField>(find.byKey(const Key('watchlist-symbol')))
            .controller!
            .text,
        '005930',
      );
      gate.completeError(const ApiException('private-token'));
      await tester.pumpAndSettle();
      expect(find.text('같은 변경 다시 시도'), findsOneWidget);
      expect(find.textContaining('private-token'), findsNothing);
      final original = jsonEncode(api.requests.single.toJson());
      api.write = (_) async => _state(1, [_kr]);
      api.read = () async => _report(1, [_kr]);
      await tester.scrollUntilVisible(
        find.text('같은 변경 다시 시도'),
        180,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.tap(find.text('같은 변경 다시 시도'));
      await tester.pumpAndSettle();
      expect(jsonEncode(api.requests.last.toJson()), original);
      expect(api.requests, hasLength(2));
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}

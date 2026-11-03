import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/public_research.dart';
import 'package:omni_folio_client/public_research_page.dart';
import 'package:omni_folio_client/watchlist.dart';
import 'package:omni_folio_client/watchlist_page.dart';

import 'vertical_slice_test.dart' show fixture, goldenApi;

Map<String, dynamic> researchView({
  bool empty = false,
  String market = 'US',
  String symbol = 'AAPL',
}) {
  final feed =
      jsonDecode(
            File(
              '../../contracts/fixtures/public-research.json',
            ).readAsStringSync(),
          )
          as Map<String, dynamic>;
  feed['feed_schema_version'] = feed['schema_version'];
  feed['schema_version'] = 'public-research-view.v1';
  feed['observed_at'] = '2026-11-03T01:00:00Z';
  feed['market'] = market;
  feed['symbol'] = symbol;
  if (empty) feed['items'] = <dynamic>[];
  return feed;
}

final apple = PublicResearchIdentity(market: 'US', symbol: 'AAPL');

void main() {
  test(
    'public research preserves typed kinds, rights URLs and immutable identity',
    () {
      final view = PublicResearch.fromJson(researchView(), apple);
      expect(view.items.map((v) => v.kind).toSet(), {
        'filing',
        'news',
        'insight',
      });
      final source = view.items.first.sources.single;
      expect(source.sourceUrl, 'https://example.test/custom-feed');
      expect(source.rightsSourceUrl, 'https://example.test/fixture');
      expect(source.attribution, contains('synthetic test fixture'));
      expect(() => view.items.clear(), throwsUnsupportedError);
      for (final venue in ['XNAS', 'XNYS']) {
        expect(PublicResearchIdentity.fromVenue(venue, 'AAPL').market, 'US');
      }
      expect(PublicResearchIdentity.fromVenue('XKRX', '005930').market, 'KR');
      expect(
        () => PublicResearchIdentity(market: 'GLOBAL', symbol: 'AAPL'),
        throwsFormatException,
      );
      expect(
        () => PublicResearchIdentity(market: 'US', symbol: 'AAPL\n'),
        throwsFormatException,
      );
      expect(
        () => PublicResearch.fromJson(researchView(market: 'KR'), apple),
        throwsFormatException,
      );
    },
  );

  test('public research rejects text, evidence, score and shape drift', () {
    for (final mutate in <void Function(Map<String, dynamic>)>[
      (v) => v['private'] = 'secret',
      (v) => v['sample'] = 'true',
      (v) => v['since'] = '2026-02-30',
      (v) => v['observed_at'] = '2026-11-03T25:00:00Z',
      (v) => (v['items'] as List)[0]['confidence'] = 1.1,
      (v) => (v['items'] as List)[0]['stars'] = 0,
      (v) => (v['items'] as List)[0]['sources'][0]['rights_source_url'] =
          'https://private:secret@example.test/',
      (v) => (v['items'] as List)[0]['sources'][0]['permissions'] = ['store'],
      (v) =>
          (v['items'] as List)[1]['url'] = 'https://example.test/?token=secret',
      (v) => (v['items'] as List)[1]['title'] = '한' * 171,
      (v) => (v['items'] as List)[1]['summary'] = 'bad\u0000',
      (v) => (v['items'] as List).add((v['items'] as List)[0]),
    ]) {
      final json = researchView();
      mutate(json);
      expect(() => PublicResearch.fromJson(json, apple), throwsFormatException);
    }
  });

  test(
    'REST research request binds exact pair and redacts server failures',
    () async {
      final api = RestOmniApi(
        client: MockClient((request) async {
          expect(request.url.path, '/v1/research/feed');
          expect(request.url.queryParameters, {
            'market': 'US',
            'symbol': 'AAPL',
          });
          return http.Response(jsonEncode(researchView()), 200);
        }),
      );
      expect((await api.publicResearch(apple)).items.length, 3);
      final mismatch = RestOmniApi(
        client: MockClient(
          (_) async =>
              http.Response(jsonEncode(researchView(market: 'KR')), 200),
        ),
      );
      await expectLater(mismatch.publicResearch(apple), throwsFormatException);
      final failed = RestOmniApi(
        client: MockClient(
          (_) async => http.Response(
            '{"code":"research_feed_unavailable","message":"private-path-secret"}',
            503,
          ),
        ),
      );
      await expectLater(
        failed.publicResearch(apple),
        throwsA(isA<ApiException>()),
      );
    },
  );

  test(
    'REST accepts the bounded artifact after Go JSON HTML escaping',
    () async {
      final view = researchView();
      final original = view['items'][0] as Map<String, dynamic>;
      view['items'] = [
        for (var i = 0; i < 100; i++)
          {
            ...original,
            'id': i.toRadixString(16).padLeft(64, '0'),
            'reasons': ['&' * 2048],
          },
      ];
      final body = jsonEncode(view).replaceAll('&', r'\u0026');
      expect(utf8.encode(body).length, greaterThan(1024 * 1024 + 4096));
      final api = RestOmniApi(
        client: MockClient((_) async => http.Response(body, 200)),
      );
      expect((await api.publicResearch(apple)).items.length, 100);
      final oversized = RestOmniApi(
        client: MockClient(
          (_) async => http.Response(' ' * (6 * 1024 * 1024 + 4097), 200),
        ),
      );
      await expectLater(oversized.publicResearch(apple), throwsFormatException);
    },
  );

  testWidgets(
    'selectable research text is named before pointer or keyboard focus',
    (tester) async {
      final semantics = tester.ensureSemantics();
      try {
        final data = researchView();
        data['items'] = (data['items'] as List)
            .where((item) => item['kind'] == 'filing')
            .toList();
        final api = _ResearchApi()..next = PublicResearch.fromJson(data, apple);
        await tester.pumpWidget(
          MaterialApp(
            home: PublicResearchPage(api: api, identity: apple),
          ),
        );
        await tester.pumpAndSettle();
        await tester.scrollUntilVisible(
          find.text('Synthetic filing'),
          250,
          scrollable: find.byType(Scrollable).first,
        );
        await tester.pumpAndSettle();
        expect(find.semantics.byLabel('Synthetic filing'), findsOneWidget);
        expect(
          find.semantics.byLabel('https://example.test/filing'),
          findsOneWidget,
        );
        await tester.scrollUntilVisible(
          find.text('출처 sec'),
          250,
          scrollable: find.byType(Scrollable).first,
        );
        await tester.pumpAndSettle();
        expect(find.semantics.byLabel('공시'), findsOneWidget);
        expect(find.semantics.byLabel(RegExp(r'^출처 sec')), findsOneWidget);
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets(
    'research renders all kinds, source terms and retains success then clears on valid empty',
    (tester) async {
      final api = _ResearchApi()
        ..next = PublicResearch.fromJson(researchView(), apple);
      await tester.pumpWidget(
        MaterialApp(
          home: PublicResearchPage(api: api, identity: apple),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('합성 예시 자료'), findsOneWidget);
      await tester.scrollUntilVisible(
        find.text('Mimir 가공 분석'),
        250,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('Mimir 가공 분석'), findsOneWidget);
      await tester.scrollUntilVisible(
        find.text('출처 Synthetic publisher'),
        250,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('출처 Synthetic publisher').first);
      await tester.pumpAndSettle();
      await tester.tap(find.text('출처 Synthetic publisher').first);
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.text('https://example.test/fixture'),
        200,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('https://example.test/fixture'), findsOneWidget);
      expect(find.text('권리 근거 URL'), findsOneWidget);
      expect(find.text('약관 URL'), findsOneWidget);
      expect(find.text('발견 출처 URL'), findsWidgets);
      await tester.scrollUntilVisible(
        find.text('Synthetic filing'),
        250,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('공시'), findsOneWidget);
      await tester.scrollUntilVisible(
        find.text('Apple news'),
        250,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('뉴스'), findsOneWidget);
      api.failure = const ApiException('private path secret');
      await tester.tap(find.byTooltip('공개 연구 새로고침'));
      await tester.pumpAndSettle();
      await tester.drag(
        find.byKey(const Key('public-research-scroll')),
        const Offset(0, 3000),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('마지막 정상 관측'), findsOneWidget);
      expect(find.textContaining('관측 시각 2026-11-03T01:00:00Z'), findsOneWidget);
      expect(find.textContaining('private path'), findsNothing);
      api.failure = null;
      api.next = PublicResearch.fromJson(researchView(empty: true), apple);
      await tester.tap(find.byTooltip('공개 연구 새로고침'));
      await tester.pumpAndSettle();
      expect(find.text('이 파일에서 해당 시장·종목의 자료가 없습니다.'), findsOneWidget);
      expect(find.text('Mimir 가공 분석'), findsNothing);
    },
  );

  testWidgets(
    'first error retries and late identity responses never replace another subject',
    (tester) async {
      final api = _ResearchApi()
        ..failure = const ApiException(
          'secret',
          code: 'research_not_configured',
        );
      await tester.pumpWidget(
        MaterialApp(
          home: PublicResearchPage(api: api, identity: apple),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('연결되지 않았습니다'), findsOneWidget);
      api.failure = null;
      final pending = Completer<PublicResearch>();
      api.pending = pending;
      await tester.tap(find.text('다시 시도'));
      await tester.pump();
      final korean = PublicResearchIdentity(market: 'KR', symbol: 'AAPL');
      api.pending = null;
      api.next = PublicResearch.fromJson(
        researchView(empty: true, market: 'KR'),
        korean,
      );
      await tester.pumpWidget(
        MaterialApp(
          home: PublicResearchPage(api: api, identity: korean),
        ),
      );
      await tester.pumpAndSettle();
      pending.complete(PublicResearch.fromJson(researchView(), apple));
      await tester.pumpAndSettle();
      expect(find.text('KR AAPL 공개 연구'), findsOneWidget);
      expect(find.text('이 파일에서 해당 시장·종목의 자료가 없습니다.'), findsOneWidget);
      expect(find.text('Mimir 가공 분석'), findsNothing);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'explicit selector has no inferred market and requires a choice',
    (tester) async {
      final api = _ResearchApi()
        ..next = PublicResearch.fromJson(researchView(empty: true), apple);
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: TextButton(
                onPressed: () =>
                    selectPublicResearch(context, api, symbol: 'AAPL'),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<FilledButton>(find.byKey(const Key('research-open')))
            .onPressed,
        isNull,
      );
      expect(api.calls, isEmpty);
      await tester.tap(find.byKey(const Key('research-market')));
      await tester.pumpAndSettle();
      await tester.tap(find.text('미국 US').last);
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('research-open')));
      await tester.pumpAndSettle();
      expect(api.calls.single.market, 'US');
      expect(api.calls.single.symbol, 'AAPL');
    },
  );

  testWidgets(
    'holding detail requests explicit market and watchlist opens its venue directly',
    (tester) async {
      final api = _ResearchApi()
        ..next = PublicResearch.fromJson(researchView(empty: true), apple);
      final holding = PortfolioSnapshot.fromJson(
        fixture('golden-snapshot.json'),
      ).holdings.first;
      await tester.pumpWidget(
        MaterialApp(
          home: AssetDetailPage(api: api, holding: holding),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('공시·뉴스·분석 보기'));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('research-market')), findsOneWidget);
      expect(api.calls, isEmpty);
      await tester.tap(find.text('취소'));
      await tester.pumpAndSettle();
      final controller = WatchlistController(api);
      addTearDown(controller.dispose);
      await tester.pumpWidget(
        MaterialApp(home: WatchlistPage(controller: controller)),
      );
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.text('공시·뉴스·분석'),
        250,
        scrollable: find.byType(Scrollable).first,
      );
      await tester.tap(find.text('공시·뉴스·분석').first);
      await tester.pumpAndSettle();
      expect(api.calls.single.market, 'US');
    },
  );

  for (final brightness in Brightness.values) {
    testWidgets(
      'research handles narrow long text and large type in $brightness',
      (tester) async {
        tester.view.physicalSize = const Size(375, 812);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final data = researchView();
        data['items'][1]['title'] = '긴 한국어 공시 제목 ' * 10;
        final api = _ResearchApi()..next = PublicResearch.fromJson(data, apple);
        await tester.pumpWidget(
          MaterialApp(
            theme: ThemeData(brightness: brightness),
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context).copyWith(
                textScaler: const TextScaler.linear(2),
                disableAnimations: true,
              ),
              child: child!,
            ),
            home: PublicResearchPage(api: api, identity: apple),
          ),
        );
        await tester.pumpAndSettle();
        await tester.drag(
          find.byKey(const Key('public-research-scroll')),
          const Offset(0, -2500),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
      },
    );
  }
}

class _ResearchApi implements OmniApi {
  final base = goldenApi();
  PublicResearch? next;
  Completer<PublicResearch>? pending;
  Object? failure;
  final calls = <PublicResearchIdentity>[];

  @override
  Future<MarketCandles> candles(String symbol) => base.candles(symbol);

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);

  @override
  Future<PublicResearch> publicResearch(PublicResearchIdentity identity) async {
    calls.add(identity);
    if (failure case final error?) throw error;
    return pending?.future ?? next!;
  }

  @override
  Future<WatchlistReport> watchlist() async => WatchlistReport.fromJson({
    'policy_version': 'watchlist_stored_prices_24h.v1',
    'max_observation_age_seconds': 86400,
    'observed_at': '2026-11-03T01:00:00Z',
    'watchlist': {
      'revision': 1,
      'updated_at': '2026-11-03T00:00:00Z',
      'entries': [
        {'venue': 'XNAS', 'symbol': 'AAPL', 'currency': 'USD'},
      ],
    },
    'quotes': [
      {
        'venue': 'XNAS',
        'symbol': 'AAPL',
        'currency': 'USD',
        'status': 'missing',
        'price': null,
      },
    ],
  });
}

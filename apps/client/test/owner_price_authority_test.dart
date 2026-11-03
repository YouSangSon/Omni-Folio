import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/holding_valuation_page.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/portfolio_benchmarks_page.dart';
import 'package:omni_folio_client/portfolio_twr_page.dart';
import 'package:omni_folio_client/portfolio_xirr_page.dart';

import 'vertical_slice_test.dart' show FakeApi, fixture, goldenApi, pumpUi;

const _notice = '직접 입력 가격 사용 · 시세 제공자가 검증한 값이 아닙니다.';
const _fxNotice = '직접 입력 환율 사용 · 시세 제공자가 검증한 값이 아닙니다.';
Json _report(String name) =>
    (fixture('golden-owner-price-reports.json')['reports'] as Json)[name]
        as Json;
FakeApi _api() => goldenApi()
  ..currentValuationValue = CurrentPortfolioValuation.fromJson(
    _report('current'),
  )
  ..twrValue = PortfolioTwr.fromJson(_report('twr'))
  ..xirrValue = PortfolioXirr.fromJson(_report('xirr'))
  ..benchmarksValue = PortfolioBenchmarks.fromJson(_report('benchmarks'));

void main() {
  test(
    'shared producer reports keep price source separate from sample and FX',
    () {
      for (final name in [
        'holding',
        'sample_holding',
        'stale_holding',
        'split_holding',
      ]) {
        final value = HoldingValuation.fromJson(_report(name));
        expect(value.ownerDeclaredPrice, isTrue);
        expect(value.sample, name == 'sample_holding');
        expect(value.lines.single.price?.sourceVersion, 'price-v1');
        expect(value.lines.single.price?.priceAdjustment, 'unadjusted');
        expect(value.status, switch (name) {
          'holding' => 'complete',
          'sample_holding' => 'stale_sample',
          _ => 'unavailable',
        });
      }
      final current = CurrentPortfolioValuation.fromJson(_report('current'));
      expect(current.ownerDeclaredPrice && current.ownerDeclaredFx, isTrue);
      expect(current.missingInputCount, 0);
      expect(current.sample, isFalse);
      final twr = PortfolioTwr.fromJson(_report('twr'));
      final xirr = PortfolioXirr.fromJson(_report('xirr'));
      final comparison = PortfolioBenchmarks.fromJson(_report('benchmarks'));
      expect(
        twr.ownerDeclaredPrice &&
            xirr.ownerDeclaredPrice &&
            comparison.ownerDeclaredPrice,
        isTrue,
      );
      expect(twr.status, 'calculated');
      expect(xirr.status, 'calculated');
      expect(comparison.benchmarks.single.sample, isFalse);
      for (final name in ['current', 'twr', 'xirr']) {
        final legacy =
            (fixture('golden-owner-fx-reports.json')['reports'] as Json)[name]
                as Json;
        expect(
          (legacy['issues'] as List).any(
            (i) => i['code'] == 'owner_declared_price',
          ),
          isFalse,
        );
      }
    },
  );

  for (final name in ['current', 'twr', 'xirr', 'holding']) {
    for (final kind in [
      'missing notice',
      'duplicate notice',
      'notice field',
      'legacy policy',
    ]) {
      test('$name rejects $kind rather than hiding owner price authority', () {
        final json = _report(name);
        final issues = json['issues'] as List;
        final notice =
            issues.singleWhere((i) => i['code'] == 'owner_declared_price')
                as Json;
        switch (kind) {
          case 'missing notice':
            issues.remove(notice);
          case 'duplicate notice':
            issues.add(Json.from(notice));
          case 'notice field':
            notice['field'] = 'AAPL';
          case 'legacy policy':
            json['policy_version'] = (json['policy_version'] as String)
                .replaceFirst(
                  name == 'holding' ? '_v2' : '_v3',
                  name == 'holding' ? '_v1' : '_v2',
                );
        }
        expect(
          () => switch (name) {
            'current' => CurrentPortfolioValuation.fromJson(json),
            'twr' => PortfolioTwr.fromJson(json),
            'xirr' => PortfolioXirr.fromJson(json),
            _ => HoldingValuation.fromJson(json),
          },
          throwsFormatException,
        );
      });
    }
  }
  for (final kind in [
    'missing version',
    'null version',
    'unsafe version',
    'unknown field',
    'adjusted',
    'lost sample',
    'wrong state',
    'split with amounts',
    'split without mark',
  ]) {
    test('holding rejects $kind across the price trust boundary', () {
      final json = _report(
        kind.startsWith('split')
            ? 'split_holding'
            : kind == 'lost sample'
            ? 'sample_holding'
            : 'holding',
      );
      final line = (json['lines'] as List).single as Json;
      final price = line['price'] as Json;
      switch (kind) {
        case 'missing version':
          price.remove('source_version');
        case 'null version':
          price['source_version'] = null;
        case 'unsafe version':
          price['source_version'] = List.filled(43, '한').join();
        case 'unknown field':
          price['provider_verified'] = true;
        case 'adjusted':
          price['price_adjustment'] = 'split_adjusted';
        case 'lost sample':
          json['sample'] = false;
        case 'wrong state':
          price['state'] = 'stale';
        case 'split with amounts':
          line['market_value'] = '100';
        case 'split without mark':
          line['price'] = null;
      }
      expect(() => HoldingValuation.fromJson(json), throwsFormatException);
    });
  }
  test('comparison cannot lose nested price policy or notice', () {
    for (final kind in ['root', 'nested']) {
      final json = _report('benchmarks');
      if (kind == 'root') {
        json['policy_version'] =
            'retrospective_latest_index_24h_native_and_krw_v2';
      } else {
        ((json['portfolio'] as Json)['issues'] as List).removeWhere(
          (i) => i['code'] == 'owner_declared_price',
        );
      }
      expect(() => PortfolioBenchmarks.fromJson(json), throwsFormatException);
    }
  });

  for (final kind in ['wrong currency', 'duplicate total']) {
    test('complete owner holding rejects $kind', () {
      final json = _report('holding');
      final totals = json['totals'] as List;
      if (kind == 'wrong currency') {
        (totals.single as Json)['currency'] = 'KRW';
      } else {
        totals.add(Json.from(totals.single as Json));
      }
      expect(() => HoldingValuation.fromJson(json), throwsFormatException);
    });
  }

  testWidgets(
    'overview announces both source notices without counting them as failures',
    (tester) async {
      final semantics = tester.ensureSemantics();
      try {
        await tester.pumpWidget(OmniFolioApp(api: _api()));
        await pumpUi(tester);
        await tester.scrollUntilVisible(
          find.text(_notice),
          150,
          scrollable: find
              .descendant(
                of: find.byType(OverviewPage),
                matching: find.byType(Scrollable),
              )
              .first,
        );
        await tester.pumpAndSettle();
        expect(find.text(_notice), findsOneWidget);
        expect(find.text(_fxNotice), findsOneWidget);
        expect(tester.takeException(), isNull);
      } finally {
        semantics.dispose();
      }
    },
  );
  for (final page in ['holding', 'twr', 'xirr', 'comparison']) {
    for (final brightness in Brightness.values) {
      testWidgets(
        '$page price notice at200percent $brightness remains accessible',
        (tester) async {
          tester.view.physicalSize = const Size(375, 812);
          tester.view.devicePixelRatio = 1;
          tester.platformDispatcher.textScaleFactorTestValue = 2;
          addTearDown(() {
            tester.view.resetPhysicalSize();
            tester.view.resetDevicePixelRatio();
            tester.platformDispatcher.clearTextScaleFactorTestValue();
          });
          final semantics = tester.ensureSemantics();
          final holdingApi = RestOmniApi(
            baseUrl: 'http://example.test/',
            client: MockClient(
              (_) async => http.Response(jsonEncode(_report('holding')), 200),
            ),
          );
          try {
            final api = _api();
            final widget = switch (page) {
              'holding' => HoldingValuationPage(api: holdingApi),
              'twr' => PortfolioTwrPage(api: api),
              'xirr' => PortfolioXirrPage(api: api),
              _ => PortfolioBenchmarksPage(api: api),
            };
            await tester.pumpWidget(
              MaterialApp(
                theme: ThemeData(brightness: brightness),
                home: MediaQuery(
                  data: const MediaQueryData(
                    disableAnimations: true,
                    textScaler: TextScaler.linear(2),
                  ),
                  child: widget,
                ),
              ),
            );
            await tester.pumpAndSettle();
            await tester.ensureVisible(find.text(_notice));
            await tester.pumpAndSettle();
            expect(find.text(_notice), findsOneWidget);
            expect(find.textContaining('샘플 · 오래된 가격'), findsNothing);
            await expectLater(
              tester,
              meetsGuideline(androidTapTargetGuideline),
            );
            await expectLater(
              tester,
              meetsGuideline(labeledTapTargetGuideline),
            );
            expect(tester.takeException(), isNull);
          } finally {
            semantics.dispose();
          }
        },
      );
    }
  }
  for (final name in ['sample_holding', 'split_holding']) {
    testWidgets(
      '$name keeps source, reason, and known-good version after failed refresh',
      (tester) async {
        var fail = false;
        final api = RestOmniApi(
          baseUrl: 'http://example.test/',
          client: MockClient(
            (_) async => fail
                ? http.Response('private-price-error', 500)
                : http.Response(jsonEncode(_report(name)), 200),
          ),
        );
        await tester.pumpWidget(
          MaterialApp(home: HoldingValuationPage(api: api)),
        );
        await tester.pumpAndSettle();
        expect(find.text(_notice), findsOneWidget);
        final version = find.text('입력 버전: price-v1');
        await tester.scrollUntilVisible(version, 150);
        await tester.pumpAndSettle();
        expect(version, findsOneWidget);
        expect(
          find.textContaining(name == 'sample_holding' ? '예시 자료' : '분할'),
          findsWidgets,
        );
        fail = true;
        await tester.tap(find.byTooltip('저장 가격 평가 새로고침'));
        await tester.pumpAndSettle();
        expect(version, findsOneWidget);
        expect(find.textContaining('private-price-error'), findsNothing);
        await tester.scrollUntilVisible(
          find.text('새로고침하지 못했습니다. 이전 평가 결과를 유지합니다.'),
          -150,
        );
        await tester.pumpAndSettle();
        expect(find.text(_notice), findsOneWidget);
        expect(tester.takeException(), isNull);
      },
    );
  }
}

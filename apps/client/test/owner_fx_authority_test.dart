import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/portfolio_benchmarks_page.dart';
import 'package:omni_folio_client/portfolio_twr_page.dart';
import 'package:omni_folio_client/portfolio_xirr_page.dart';

import 'vertical_slice_test.dart'
    show FakeApi, fixture, goldenApi, pumpUi, currentPortfolioValuation;

const _notice = '직접 입력 환율 사용 · 시세 제공자가 검증한 값이 아닙니다.';
Json _report(String name) =>
    (fixture('golden-owner-fx-reports.json')['reports'] as Json)[name] as Json;
Json _row(Json report) => (report['benchmarks'] as List).single as Json;
FakeApi _api() => goldenApi()
  ..currentValuationValue = CurrentPortfolioValuation.fromJson(
    _report('current'),
  )
  ..twrValue = PortfolioTwr.fromJson(_report('twr'))
  ..xirrValue = PortfolioXirr.fromJson(_report('xirr'))
  ..benchmarksValue = PortfolioBenchmarks.fromJson(_report('benchmarks'));

void main() {
  test(
    'shared producer responses preserve owner authority apart from sample',
    () {
      final value = CurrentPortfolioValuation.fromJson(_report('current'));
      final twr = PortfolioTwr.fromJson(_report('twr'));
      final xirr = PortfolioXirr.fromJson(_report('xirr'));
      expect(value.ownerDeclaredFx, isTrue);
      expect(value.sample, isFalse);
      expect(value.status, 'complete');
      expect(value.missingInputCount, 0);
      expect(twr.ownerDeclaredFx && xirr.ownerDeclaredFx, isTrue);
      expect(twr.status, 'calculated');
      expect(xirr.status, 'calculated');
      for (final name in [
        'benchmarks',
        'owner_sample',
        'partial_marks',
        'stale_marks',
      ]) {
        final report = PortfolioBenchmarks.fromJson(_report(name));
        expect(report.ownerDeclaredFx, isTrue);
        expect(report.benchmarks.single.sample, name == 'owner_sample');
        expect(report.benchmarks.single.endFx?.sourceVersion, 'manual-v1');
      }
      expect(currentPortfolioValuation().ownerDeclaredFx, isFalse);
      expect(
        PortfolioTwr.fromJson(
          fixture('golden-portfolio-twr.json'),
        ).ownerDeclaredFx,
        isFalse,
      );
      expect(
        PortfolioXirr.fromJson(
          fixture('golden-portfolio-xirr.json'),
        ).ownerDeclaredFx,
        isFalse,
      );
      expect(
        PortfolioBenchmarks.fromJson(
          fixture('golden-portfolio-benchmarks.json'),
        ).ownerDeclaredFx,
        isFalse,
      );
    },
  );

  for (final name in ['current', 'twr', 'xirr']) {
    Object parse(Json json) => switch (name) {
      'current' => CurrentPortfolioValuation.fromJson(json),
      'twr' => PortfolioTwr.fromJson(json),
      _ => PortfolioXirr.fromJson(json),
    };
    for (final kind in [
      'missing notice',
      'duplicate notice',
      'notice field',
      'legacy policy',
    ]) {
      test('$name rejects $kind rather than hiding owner authority', () {
        final json = _report(name);
        final issues = json['issues'] as List;
        switch (kind) {
          case 'missing notice':
            issues.removeWhere((issue) => issue['code'] == 'owner_declared_fx');
          case 'duplicate notice':
            issues.add(Map<String, dynamic>.from(issues.single as Json));
          case 'notice field':
            (issues.single as Json)['field'] = 'USD';
          case 'legacy policy':
            json['policy_version'] = (json['policy_version'] as String)
                .replaceFirst('_v2', '_v1');
        }
        expect(() => parse(json), throwsFormatException);
      });
    }
  }
  for (final kind in [
    'missing source',
    'null source',
    'unknown source',
    'missing version',
    'null version',
    'version bytes',
    'missing sample',
    'null sample',
    'extra field',
    'mixed versions',
    'mixed categories',
    'mixed sample',
    'lost sample',
    'legacy policy',
    'lost nested notice',
  ]) {
    test('comparison rejects $kind across the owner FX trust boundary', () {
      final json = _report(
        kind == 'lost sample' ? 'owner_sample' : 'benchmarks',
      );
      final row = _row(json);
      final mark = row['start_fx'] as Json;
      switch (kind) {
        case 'missing source':
          mark.remove('source');
        case 'null source':
          mark['source'] = null;
        case 'unknown source':
          mark['source'] = 'provider_verified';
        case 'missing version':
          mark.remove('source_version');
        case 'null version':
          mark['source_version'] = null;
        case 'version bytes':
          mark['source_version'] = List.filled(43, '한').join();
        case 'missing sample':
          mark.remove('sample');
        case 'null sample':
          mark['sample'] = null;
        case 'extra field':
          mark['provider_verified'] = true;
        case 'mixed versions':
          mark['source_version'] = 'different';
        case 'mixed categories':
          mark.remove('source');
          mark.remove('source_version');
          mark.remove('sample');
        case 'mixed sample':
          mark['sample'] = true;
        case 'lost sample':
          row['sample'] = false;
        case 'legacy policy':
          json['policy_version'] =
              'retrospective_latest_index_24h_native_and_krw_v1';
        case 'lost nested notice':
          (json['portfolio']['issues'] as List).clear();
      }
      expect(() => PortfolioBenchmarks.fromJson(json), throwsFormatException);
    });
  }

  testWidgets(
    'unavailable overview counts missing input and announces both notices once',
    (tester) async {
      final semantics = tester.ensureSemantics();
      addTearDown(semantics.dispose);
      final json = _report('current')
        ..['status'] = 'unavailable'
        ..['sample'] = true
        ..['cash_value'] = null
        ..['holdings_value'] = null
        ..['total_value'] = null;
      (json['issues'] as List).addAll([
        {
          'code': 'missing_security_price',
          'message': 'unavailable',
          'field': 'AAPL',
        },
      ]);
      final value = CurrentPortfolioValuation.fromJson(json);
      expect(value.missingInputCount, 1);
      await tester.pumpWidget(
        OmniFolioApp(api: _api()..currentValuationValue = value),
      );
      await pumpUi(tester);
      final title = find.text('전체 평가를 계산할 수 없음');
      await tester.scrollUntilVisible(
        title,
        150,
        scrollable: find
            .descendant(
              of: find.byType(OverviewPage),
              matching: find.byType(Scrollable),
            )
            .first,
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('직접 환율 1건'), findsOneWidget);
      final label = tester.getSemantics(title).getSemanticsData().label;
      expect(_notice.allMatches(label).length, 1);
      expect('예시 자료 포함'.allMatches(label).length, 1);
      expect(tester.takeException(), isNull);
    },
  );

  for (final page in ['twr', 'xirr', 'comparison']) {
    for (final brightness in Brightness.values) {
      testWidgets('$page owner notice at200percent $brightness is accessible', (
        tester,
      ) async {
        tester.view.physicalSize = const Size(375, 812);
        tester.view.devicePixelRatio = 1;
        tester.platformDispatcher.textScaleFactorTestValue = 2;
        addTearDown(() {
          tester.view.resetPhysicalSize();
          tester.view.resetDevicePixelRatio();
          tester.platformDispatcher.clearTextScaleFactorTestValue();
        });
        final semantics = tester.ensureSemantics();
        addTearDown(semantics.dispose);
        final api = _api();
        final widget = switch (page) {
          'twr' => PortfolioTwrPage(api: api),
          'xirr' => PortfolioXirrPage(api: api),
          _ => PortfolioBenchmarksPage(api: api),
        };
        await tester.pumpWidget(
          MaterialApp(
            theme: ThemeData(brightness: brightness),
            home: widget,
          ),
        );
        await tester.pumpAndSettle();
        await tester.ensureVisible(find.text(_notice));
        await tester.pumpAndSettle();
        expect(find.text(_notice), findsOneWidget);
        expect(find.textContaining('예시 가격·환율 포함'), findsNothing);
        await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
        await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
        expect(tester.takeException(), isNull);
      });
    }
  }
  testWidgets(
    'sample owner comparison shows both warnings and the selected input version',
    (tester) async {
      final api = _api()
        ..benchmarksValue = PortfolioBenchmarks.fromJson(
          _report('owner_sample'),
        );
      await tester.pumpWidget(
        MaterialApp(home: PortfolioBenchmarksPage(api: api)),
      );
      await tester.pumpAndSettle();
      expect(find.text(_notice), findsOneWidget);
      expect(find.textContaining('예시'), findsWidgets);
      final details = find.textContaining('자료 기준과 관측 시각').last;
      await tester.ensureVisible(details);
      await tester.tap(details);
      await tester.pumpAndSettle();
      expect(find.text('환율 입력 버전: manual-v1'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );
}

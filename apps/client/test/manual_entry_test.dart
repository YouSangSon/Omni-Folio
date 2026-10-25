import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/import_apply.dart';
import 'package:omni_folio_client/manual_entry.dart';
import 'package:omni_folio_client/manual_entry_page.dart';
import 'package:omni_folio_client/models.dart';

import 'vertical_slice_test.dart' show FakeApi, fixture, goldenApi, pumpUi;

List<Json> _cases() =>
    ((jsonDecode(
                  File(
                    '../../contracts/fixtures/manual-entry-cases.json',
                  ).readAsStringSync(),
                )
                as Json)['cases']
            as List)
        .cast<Json>();

Map<String, String> _fields(Json entry) => (entry['fields'] as Json).map(
  (key, value) => MapEntry(key, value as String),
);

FakeApi _manualApi() {
  final base = goldenApi();
  final preview = fixture('golden-preview.json');
  preview['preview_id'] = 'preview_manual_001';
  preview['rows'] = [
    {
      'row_number': 2,
      'status': 'new',
      'transaction': {
        ..._cases().first['expected_transaction'] as Json,
        'event_id': 'event_manual_001',
        'instrument_id': 'instrument_aapl',
      },
    },
  ];
  preview['totals'] = {
    'total_rows': 1,
    'new_rows': 1,
    'duplicate_rows': 0,
    'error_rows': 0,
    'unresolved_rows': 0,
  };
  final receipt = fixture('golden-apply.json')
    ..['receipt_id'] = 'receipt_manual_001'
    ..['preview_id'] = 'preview_manual_001'
    ..['idempotency_key'] = 'import-preview_manual_001'
    ..['ledger_revision_after'] = 'rev_0000000001'
    ..['applied_rows'] = 1;
  return FakeApi(
    statusValue: base.statusValue,
    snapshotValue: base.snapshotValue,
    previewValue: ImportPreview.fromJson(preview),
    receiptValue: ApplyReceipt.fromJson(receipt),
    candlesValue: base.candlesValue,
    orderLogValue: base.orderLogValue,
    activityValue: base.activityValue,
    currentValuationValue: base.currentValuationValue,
  );
}

class _ControlledApi extends FakeApi {
  _ControlledApi(FakeApi base)
    : super(
        statusValue: base.statusValue,
        snapshotValue: base.snapshotValue,
        previewValue: base.previewValue,
        receiptValue: base.receiptValue,
        candlesValue: base.candlesValue,
        orderLogValue: base.orderLogValue,
        activityValue: base.activityValue,
        currentValuationValue: base.currentValuationValue,
      );

  Completer<ImportPreview>? previewCompleter;
  ApiException? applyError;

  @override
  Future<ImportPreview> preview(String csv) {
    if (previewCompleter == null) return super.preview(csv);
    previewInputs.add(csv);
    return previewCompleter!.future;
  }

  @override
  Future<ApplyReceipt> apply(String previewId, String key) async {
    if (applyError == null) return super.apply(previewId, key);
    applyKeys.add(key);
    throw applyError!;
  }
}

Future<void> _history(WidgetTester tester, FakeApi api) async {
  await tester.pumpWidget(OmniFolioApp(api: api));
  await pumpUi(tester);
  await tester.tap(find.text('내역'));
  await pumpUi(tester);
}

Future<void> _revealHistory(
  WidgetTester tester,
  Finder target, {
  double delta = 200,
}) async {
  if (target.evaluate().isNotEmpty) {
    await tester.ensureVisible(target);
    await tester.pump();
    delta = tester.getCenter(target).dy < tester.view.physicalSize.height / 2
        ? -200
        : 200;
  }
  await tester.scrollUntilVisible(
    target.hitTestable(),
    delta,
    maxScrolls: 30,
    scrollable: find
        .descendant(
          of: find.byType(ActivityPage),
          matching: find.byType(Scrollable),
        )
        .first,
  );
  await tester.pump();
}

Future<void> _tapHistory(WidgetTester tester, String label) async {
  final target = find.text(label);
  await _revealHistory(tester, target);
  await tester.tap(target);
}

Future<void> _openManual(WidgetTester tester) async {
  await _tapHistory(tester, '거래 직접 입력');
  await tester.pumpAndSettle();
}

Future<void> _revealForm(WidgetTester tester, Finder finder) async {
  await tester.ensureVisible(finder);
  await tester.pumpAndSettle();
}

Future<void> _fill(WidgetTester tester, String field, String value) async {
  final finder = find.byKey(ValueKey('manual-$field'));
  await _revealForm(tester, finder);
  await tester.enterText(finder, value);
  await pumpUi(tester);
}

Future<void> _prepareBuy(WidgetTester tester) async {
  await _fill(tester, 'occurred_at', '2026-01-05T09:30:00+09:00');
  await _fill(tester, 'quantity', '002.00');
  await _fill(tester, 'price', '10.000');
  await _fill(tester, 'fee', '0.10');
  final submit = find.text('검토용 초안 만들기');
  await _revealForm(tester, submit);
  await tester.tap(submit);
  await tester.pumpAndSettle();
}

void main() {
  for (final entry in _cases()) {
    test('manual shared fixture ${entry['name']}', () {
      expect(
        manualEntryErrors(entry['type'] as String, _fields(entry)),
        isEmpty,
      );
      expect(
        manualEntryCsv(
          sourceEventId: entry['source_event_id'] as String,
          type: entry['type'] as String,
          fields: _fields(entry),
        ),
        entry['expected_csv'],
      );
      final expected = entry['expected_transaction'] as Json;
      final event = LedgerActivity.fromJson({
        'type': expected['type'],
        'occurred_at': expected['occurred_at'],
        'recorded_at': '2026-01-10T15:01:00Z',
        for (final field in [
          'symbol',
          'quantity',
          'price',
          'fee',
          'currency',
          'amount',
          'counter_currency',
          'counter_amount',
        ])
          field: expected[field],
        'is_correction': expected['type'] == 'CASH_VOID',
      });
      expect(event.amount, expected['amount']);
    });
  }

  test(
    'manual fields reject invalid date, scale, numbers and FX direction',
    () {
      final good = _fields(_cases().first);
      for (final timestamp in [
        '2026-02-30T00:00:00Z',
        '2026-01-01T24:00:00Z',
        '2026-01-01T00:00:00',
        '2026-01-01T00:00:00.1234567891Z',
        '2026-01-01T00:00:00+24:00',
      ]) {
        expect(
          manualEntryErrors('BUY', {...good, 'occurred_at': timestamp}),
          contains('occurred_at'),
        );
      }
      for (final quantity in [
        '0',
        '-1',
        '1e3',
        '1,000',
        List.filled(65, '1').join(),
      ]) {
        expect(
          manualEntryErrors('BUY', {...good, 'quantity': quantity}),
          contains('quantity'),
        );
      }
      expect(
        manualEntryErrors('FX_EXCHANGE', {
          ...good,
          'amount': '1',
          'currency': 'USD',
          'counter_currency': 'usd',
          'counter_amount': '1',
        }),
        contains('counter_currency'),
      );
      expect(
        () => tradeCashAmount('BUY', '2.0', '1', '0'),
        throwsFormatException,
      );
      expect(
        () => manualEntryCsv(
          sourceEventId: 'regenerated-or-invalid',
          type: 'BUY',
          fields: good,
        ),
        throwsFormatException,
      );
    },
  );

  testWidgets('manual entry opens without sending a ledger change', (
    tester,
  ) async {
    final api = goldenApi();
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('내역'));
    await pumpUi(tester);
    final open = find.widgetWithText(OutlinedButton, '거래 직접 입력');
    expect(open, findsOneWidget);
    await tester.tap(open);
    await tester.pumpAndSettle();
    expect(find.text('거래 한 건 입력'), findsOneWidget);
    expect(api.previewInputs, isEmpty);
    expect(api.applyKeys, isEmpty);
  });

  testWidgets(
    'manual draft uses explicit preview, stable source and apply retry',
    (tester) async {
      final api = _manualApi()..applyFailures = 1;
      await _history(tester, api);
      await _openManual(tester);
      await _prepareBuy(tester);
      final csv = tester
          .widget<TextField>(find.byType(TextField))
          .controller!
          .text;
      expect(csv, contains('"account-main","BUY"'));
      expect(csv, contains('"2","10","0.1","USD","-20.1"'));
      expect(RegExp(r'"manual-[0-9a-f]{32}"').allMatches(csv), hasLength(1));
      expect(api.previewInputs, isEmpty);
      expect(api.applyKeys, isEmpty);
      await tester.ensureVisible(find.text('미리보기 만들기'));
      await _tapHistory(tester, '미리보기 만들기');
      await pumpUi(tester);
      await _tapHistory(tester, '원자적으로 적용');
      await pumpUi(tester);
      expect(api.previewInputs, [csv]);
      expect(api.applyKeys, ['import-preview_manual_001']);
      expect(find.textContaining('거래 내역의 반영 여부를 확인하지 못했습니다.'), findsOneWidget);
      await tester.ensureVisible(find.text('미리보기 만들기'));
      await _tapHistory(tester, '미리보기 만들기');
      await pumpUi(tester);
      expect(api.previewInputs, [csv, csv]);
      await _tapHistory(tester, '원자적으로 적용');
      await pumpUi(tester);
      expect(api.applyKeys, [
        'import-preview_manual_001',
        'import-preview_manual_001',
      ]);
      expect(
        tester.widget<TextField>(find.byType(TextField)).controller!.text,
        csv,
      );
      await _revealHistory(tester, find.text('적용 확인'));
      expect(find.text('적용 확인'), findsOneWidget);
      expect(find.textContaining('적용 1건'), findsOneWidget);
    },
  );

  testWidgets('manual cancellation preserves current input and receipt', (
    tester,
  ) async {
    final api = _manualApi();
    await _history(tester, api);
    await tester.enterText(find.byType(TextField), 'original csv');
    await _tapHistory(tester, '미리보기 만들기');
    await pumpUi(tester);
    await _tapHistory(tester, '원자적으로 적용');
    await pumpUi(tester);
    await tester.ensureVisible(find.text('거래 직접 입력'));
    await _openManual(tester);
    await _fill(tester, 'occurred_at', 'invalid');
    await tester.pageBack();
    await tester.pumpAndSettle();
    expect(
      tester.widget<TextField>(find.byType(TextField)).controller!.text,
      'original csv',
    );
    await _revealHistory(tester, find.text('적용 확인'));
    expect(find.text('적용 확인'), findsOneWidget);
    expect(api.previewInputs, ['original csv']);
    expect(api.applyKeys, hasLength(1));
  });

  testWidgets(
    'manual validation focuses summary and type switch drops hidden fields',
    (tester) async {
      final api = _manualApi();
      await _history(tester, api);
      await _openManual(tester);
      final submit = find.text('검토용 초안 만들기');
      await _revealForm(tester, submit);
      await tester.tap(submit);
      await tester.pumpAndSettle();
      expect(find.text('입력값을 확인하세요'), findsOneWidget);
      expect(api.previewInputs, isEmpty);
      final summary = find
          .ancestor(of: find.text('입력값을 확인하세요'), matching: find.byType(Focus))
          .first;
      expect(tester.widget<Focus>(summary).focusNode!.hasFocus, isTrue);
      await _fill(tester, 'occurred_at', '2026-01-05T09:30:00+09:00');
      await _fill(tester, 'price', '999');
      final dropdown = find.byType(DropdownButtonFormField<String>);
      await _revealForm(tester, dropdown);
      await tester.tap(dropdown);
      await pumpUi(tester);
      await tester.tap(find.text('입금').last);
      await pumpUi(tester);
      await _fill(tester, 'amount', '12.3400');
      await _revealForm(tester, submit);
      await tester.tap(submit);
      await tester.pumpAndSettle();
      final csv = tester
          .widget<TextField>(find.byType(TextField))
          .controller!
          .text;
      expect(csv, contains('"DEPOSIT"'));
      expect(csv, contains('"","","","","USD","12.34","","",""'));
      expect(csv, isNot(contains('999')));
    },
  );

  testWidgets('edited in-flight apply remains session-owned across tabs', (
    tester,
  ) async {
    final gate = Completer<ApplyReceipt>();
    final api = _manualApi()..applyCompleter = gate;
    await _history(tester, api);
    await tester.enterText(find.byType(TextField), 'original csv');
    await _tapHistory(tester, '미리보기 만들기');
    await pumpUi(tester);
    await _tapHistory(tester, '원자적으로 적용');
    await tester.pump();
    await _revealHistory(tester, find.byType(TextField), delta: -200);
    expect(tester.widget<TextField>(find.byType(TextField)).readOnly, isFalse);
    await tester.enterText(find.byType(TextField), 'edited csv');
    await tester.tap(find.text('홈'));
    await pumpUi(tester);
    expect(api.applyKeys, hasLength(1));
    gate.completeError(const ApiException('private-apply-sentinel'));
    await pumpUi(tester);
    await tester.tap(find.text('내역'));
    await pumpUi(tester);
    expect(
      tester
          .widget<OutlinedButton>(
            find.widgetWithText(OutlinedButton, '거래 직접 입력'),
          )
          .onPressed,
      isNull,
    );
    await tester.enterText(find.byType(TextField), 'new current csv');
    await _tapHistory(tester, '미리보기 만들기');
    await pumpUi(tester);
    expect(api.previewInputs, ['original csv', 'new current csv']);
    api.applyCompleter = null;
    await _tapHistory(tester, '이전 요청 재시도');
    await pumpUi(tester);
    expect(api.applyKeys, everyElement('import-preview_manual_001'));
    expect(api.applyKeys, hasLength(2));
    await _revealHistory(tester, find.byType(TextField), delta: -200);
    expect(
      tester.widget<TextField>(find.byType(TextField)).controller!.text,
      'new current csv',
    );
    await _revealHistory(tester, find.text('이전 입력 적용 확인'));
    expect(find.text('이전 입력 적용 확인'), findsOneWidget);
    expect(find.text('적용 확인'), findsNothing);
    expect(find.textContaining('private-apply-sentinel'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  test(
    'pending capture is unchanged by a different draft or new preview',
    () async {
      final api = _manualApi()..applyFailures = 1;
      final session = ImportApplyController(api);
      addTearDown(session.dispose);
      await session.apply(api.previewValue, 'original');
      final captured = session.pending!;
      final changed = fixture('golden-preview.json')
        ..['preview_id'] = 'preview_different';
      final preview = ImportPreview.fromJson(changed);
      await session.apply(preview, 'different');
      expect(api.applyKeys, hasLength(1));
      expect(session.pending, captured);
      await session.apply(preview, 'original');
      expect(api.applyKeys, everyElement(captured.key));
      expect(session.pending, isNull);
      expect(session.completion!.csv, 'original');
      expect(session.completion!.previewId, captured.previewId);
    },
  );

  testWidgets('edited apply success belongs to the previous input', (
    tester,
  ) async {
    final gate = Completer<ApplyReceipt>();
    final api = goldenApi()..applyCompleter = gate;
    await tester.pumpWidget(OmniFolioApp(api: api));
    await pumpUi(tester);
    await tester.tap(find.text('내역'));
    await pumpUi(tester);
    await tester.enterText(find.byType(TextField), 'original csv');
    await _tapHistory(tester, '미리보기 만들기');
    await pumpUi(tester);
    await _tapHistory(tester, '원자적으로 적용');
    await tester.pump();
    await _revealHistory(tester, find.byType(TextField), delta: -200);
    await tester.enterText(find.byType(TextField), 'edited csv');
    expect(api.applyKeys, hasLength(1));
    gate.complete(api.receiptValue);
    await pumpUi(tester);
    expect(
      tester.widget<TextField>(find.byType(TextField)).controller!.text,
      'edited csv',
    );
    await _revealHistory(tester, find.text('이전 입력 적용 확인'));
    expect(find.text('이전 입력 적용 확인'), findsOneWidget);
    expect(find.text('적용 확인'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('old preview failure does not replace edited draft advice', (
    tester,
  ) async {
    final api = _ControlledApi(_manualApi());
    final gate = Completer<ImportPreview>();
    api.previewCompleter = gate;
    await _history(tester, api);
    await tester.enterText(find.byType(TextField), 'original');
    await _tapHistory(tester, '미리보기 만들기');
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'edited');
    expect(api.previewInputs, ['original']);
    gate.completeError(const ApiException('private-preview-sentinel'));
    await pumpUi(tester);
    expect(
      tester.widget<TextField>(find.byType(TextField)).controller!.text,
      'edited',
    );
    expect(find.textContaining('미리보기를 완료하지 못했습니다.'), findsNothing);
    expect(find.textContaining('private-preview-sentinel'), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('confirmed rejection releases manual entry and fresh preview', (
    tester,
  ) async {
    final api = _ControlledApi(_manualApi())
      ..applyError = const ApiException(
        'private-rejection',
        statusCode: 409,
        code: 'stale_preview',
      );
    await _history(tester, api);
    await tester.enterText(find.byType(TextField), 'original');
    await _tapHistory(tester, '미리보기 만들기');
    await pumpUi(tester);
    await _tapHistory(tester, '원자적으로 적용');
    await pumpUi(tester);
    await _revealHistory(
      tester,
      find.textContaining('이전 반영 요청이 거절되었습니다.'),
      delta: -200,
    );
    expect(find.textContaining('이전 반영 요청이 거절되었습니다.'), findsOneWidget);
    final open = find.widgetWithText(OutlinedButton, '거래 직접 입력');
    expect(tester.widget<OutlinedButton>(open).onPressed, isNotNull);
    expect(find.text('이전 요청 재시도'), findsNothing);
    await tester.ensureVisible(find.text('미리보기 만들기'));
    await _tapHistory(tester, '미리보기 만들기');
    await pumpUi(tester);
    expect(api.previewInputs, ['original', 'original']);
    expect(find.textContaining('private-rejection'), findsNothing);
  });

  testWidgets('duplicate open and submit callbacks prepare only one draft', (
    tester,
  ) async {
    final api = _manualApi();
    await _history(tester, api);
    final open = tester
        .widget<OutlinedButton>(find.widgetWithText(OutlinedButton, '거래 직접 입력'))
        .onPressed!;
    open();
    open();
    await tester.pumpAndSettle();
    expect(find.byType(ManualEntryPage), findsOneWidget);
    await _fill(tester, 'occurred_at', '2026-01-05T09:30:00+09:00');
    await _fill(tester, 'quantity', '2');
    await _fill(tester, 'price', '10');
    final submit = find.widgetWithText(ElevatedButton, '검토용 초안 만들기');
    await _revealForm(tester, submit);
    final callback = tester.widget<ElevatedButton>(submit).onPressed!;
    callback();
    callback();
    await tester.pumpAndSettle();
    expect(find.byType(ManualEntryPage), findsNothing);
    expect(find.text('거래 내역 가져오기'), findsOneWidget);
    expect(api.previewInputs, isEmpty);
    expect(api.applyKeys, isEmpty);
    expect(tester.takeException(), isNull);
  });

  for (final brightness in Brightness.values) {
    for (final size in [const Size(375, 812), const Size(812, 375)]) {
      testWidgets(
        'manual layout $brightness $size at 200 percent with keyboard',
        (tester) async {
          tester.view.physicalSize = size;
          tester.view.devicePixelRatio = 1;
          tester.platformDispatcher.textScaleFactorTestValue = 2;
          tester.platformDispatcher.platformBrightnessTestValue = brightness;
          tester.platformDispatcher.accessibilityFeaturesTestValue =
              FakeAccessibilityFeatures(disableAnimations: true);
          addTearDown(() {
            tester.view.resetPhysicalSize();
            tester.view.resetDevicePixelRatio();
            tester.view.resetViewInsets();
            tester.platformDispatcher.clearTextScaleFactorTestValue();
            tester.platformDispatcher.clearPlatformBrightnessTestValue();
            tester.platformDispatcher.clearAccessibilityFeaturesTestValue();
          });
          await _history(tester, _manualApi());
          await tester.ensureVisible(find.text('거래 직접 입력'));
          await _openManual(tester);
          tester.view.viewInsets = FakeViewPadding(
            bottom: size.width < size.height ? 240 : 120,
          );
          await pumpUi(tester);
          await _fill(tester, 'occurred_at', '2026-01-05T09:30:00+09:00');
          final field = find.byKey(const ValueKey('manual-occurred_at'));
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
          final submit = find.widgetWithText(ElevatedButton, '검토용 초안 만들기');
          await _revealForm(tester, submit);
          expect(tester.getSize(submit).height, greaterThanOrEqualTo(48));
          await tester.tap(submit);
          await pumpUi(tester);
          expect(find.text('입력값을 확인하세요'), findsOneWidget);
          expect(tester.takeException(), isNull);
        },
      );
    }
  }

  for (final scenario in [
    (status: 409, code: 'stale_preview', rejected: true),
    (status: 400, code: 'preview_not_found', rejected: true),
    (status: 400, code: 'preview_has_errors', rejected: true),
    (status: 400, code: 'invalid_ledger', rejected: true),
    (status: 500, code: 'stale_preview', rejected: false),
    (status: 409, code: 'idempotency_conflict', rejected: false),
    (status: 400, code: 'unknown-private-code', rejected: false),
  ]) {
    test('apply rejection pair ${scenario.status}/${scenario.code}', () async {
      final client = MockClient(
        (_) async => http.Response(
          jsonEncode({
            'code': scenario.code,
            'message': 'private-error-message',
          }),
          scenario.status,
        ),
      );
      addTearDown(client.close);
      final session = ImportApplyController(RestOmniApi(client: client));
      addTearDown(session.dispose);
      await session.apply(_manualApi().previewValue, 'captured');
      expect(session.pending == null, scenario.rejected);
      expect(
        session.failure,
        scenario.rejected
            ? ImportApplyFailure.rejected
            : ImportApplyFailure.uncertain,
      );
      expect(session.completion, isNull);
      expect(session.busy, isFalse);
    });
  }

  test(
    'network and malformed success preserve an unresolved request',
    () async {
      for (final malformed in [false, true]) {
        final client = MockClient((_) async {
          if (!malformed) throw http.ClientException('private-network-error');
          return http.Response('{}', 200);
        });
        final session = ImportApplyController(RestOmniApi(client: client));
        try {
          await session.apply(_manualApi().previewValue, 'captured');
          expect(session.pending!.csv, 'captured');
          expect(session.failure, ImportApplyFailure.uncertain);
        } finally {
          session.dispose();
          client.close();
        }
      }
    },
  );
}

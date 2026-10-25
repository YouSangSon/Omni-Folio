import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';

import 'vertical_slice_test.dart' show goldenApi, pumpUi;

void main() {
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
    await pumpUi(tester);
    expect(find.text('거래 한 건 입력'), findsOneWidget);
    expect(api.previewInputs, isEmpty);
    expect(api.applyKeys, isEmpty);
  });
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
    await tester.tap(find.text('미리보기 만들기'));
    await pumpUi(tester);
    await tester.ensureVisible(find.text('원자적으로 적용'));
    await tester.tap(find.text('원자적으로 적용'));
    await tester.pump();
    await tester.ensureVisible(find.byType(TextField));
    await tester.enterText(find.byType(TextField), 'edited csv');
    gate.complete(api.receiptValue);
    await pumpUi(tester);
    expect(
      tester.widget<TextField>(find.byType(TextField)).controller!.text,
      'edited csv',
    );
    expect(find.text('이전 입력 적용 확인'), findsOneWidget);
    expect(find.text('적용 확인'), findsNothing);
    expect(tester.takeException(), isNull);
  });
}

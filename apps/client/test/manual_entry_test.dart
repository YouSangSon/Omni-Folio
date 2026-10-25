import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:omni_folio_client/app.dart';

import 'vertical_slice_test.dart' show goldenApi, pumpUi;

void main() {
  testWidgets('manual entry opens without sending a ledger change', (tester) async {
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
}

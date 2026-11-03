import 'dart:math';

import 'package:flutter/material.dart';

import 'manual_entry.dart';

class ManualEntryPage extends StatefulWidget {
  const ManualEntryPage({super.key});

  @override
  State<ManualEntryPage> createState() => _ManualEntryPageState();
}

class _ManualEntryPageState extends State<ManualEntryPage> {
  final _form = GlobalKey<FormState>();
  final _summaryFocus = FocusNode();
  final _summaryKey = GlobalKey();
  final _fields = <String, TextEditingController>{};
  final _focus = <String, FocusNode>{};
  late final String _sourceEventId;
  var _type = 'BUY';
  var _submitted = false;
  var _attempted = false;
  Map<String, String> _errors = {};

  @override
  void initState() {
    super.initState();
    final random = Random.secure();
    _sourceEventId =
        'manual-${List.generate(16, (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0')).join()}';
    for (final field
        in manualEntryTypes.keys.expand(manualEntryFields).toSet()) {
      _fields[field] = TextEditingController(
        text: switch (field) {
          'currency' => 'USD',
          'counter_currency' => 'KRW',
          'symbol' => 'AAPL',
          'fee' => '0',
          _ => '',
        },
      );
      _focus[field] = FocusNode();
    }
  }

  Map<String, String> get _values =>
      _fields.map((key, controller) => MapEntry(key, controller.text));

  @override
  void dispose() {
    for (final controller in _fields.values) {
      controller.dispose();
    }
    for (final node in _focus.values) {
      node.dispose();
    }
    _summaryFocus.dispose();
    super.dispose();
  }

  void _changed() {
    if (!_attempted) return;
    setState(() => _errors = manualEntryErrors(_type, _values));
  }

  void _submit() {
    if (_submitted) return;
    final errors = manualEntryErrors(_type, _values);
    setState(() {
      _attempted = true;
      _errors = errors;
    });
    if (!_form.currentState!.validate() || errors.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        _summaryFocus.requestFocus();
        final context = _summaryKey.currentContext;
        if (context != null) Scrollable.ensureVisible(context);
      });
      return;
    }
    final csv = manualEntryCsv(
      sourceEventId: _sourceEventId,
      type: _type,
      fields: _values,
    );
    _submitted = true;
    Navigator.of(context).pop(csv);
  }

  String? _helper(String field) => switch (field) {
    'occurred_at' => '예: 2026-10-25T09:30:00+09:00. 입력한 시간대와 소수초를 그대로 검토합니다.',
    'symbol' => '현재 기본 종목 해석은 AAPL만 지원합니다. 다른 종목은 미해결로 표시됩니다.',
    'quantity' when _type == 'SPLIT' => '2:1 분할은 2, 1:10 병합은 0.1처럼 입력하세요.',
    'fee' => '없으면 0을 입력하세요. 매도 비용이 대금보다 커도 원문대로 검토합니다.',
    'corrects_source_event_id' =>
      '원본 CSV의 source_event_id입니다. Receipt ID가 아닙니다.',
    'amount' when _type == 'CASH_VOID' =>
      '원본 현금 기록과 반대 부호의 금액을 입력하세요. 다음 검토에서 대상을 확인합니다.',
    _ => null,
  };

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('거래 한 건 입력')),
    body: SafeArea(
      child: Form(
        key: _form,
        autovalidateMode: _attempted
            ? AutovalidateMode.onUserInteraction
            : AutovalidateMode.disabled,
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(16),
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 640),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  const Text(
                    '이미 발생한 거래를 로컬 기록으로 준비합니다. 주문을 보내거나 원장을 바로 변경하지 않습니다.',
                  ),
                  const SizedBox(height: 12),
                  const Text(
                    '현재 CSV와 같은 로컬 기본 계좌(account-main)를 사용합니다. 실제 계좌번호나 인증키를 입력하지 마세요.',
                  ),
                  const SizedBox(height: 16),
                  if (_errors.isNotEmpty)
                    Focus(
                      key: _summaryKey,
                      focusNode: _summaryFocus,
                      child: Semantics(
                        container: true,
                        liveRegion: true,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Semantics(
                              header: true,
                              child: const Text('입력값을 확인하세요'),
                            ),
                            for (final error in _errors.entries)
                              TextButton(
                                onPressed: () {
                                  final node = _focus[error.key];
                                  node?.requestFocus();
                                  if (node?.context != null) {
                                    Scrollable.ensureVisible(node!.context!);
                                  }
                                },
                                child: Text(
                                  '${manualEntryLabel(_type, error.key)}: ${error.value}',
                                ),
                              ),
                            const SizedBox(height: 12),
                          ],
                        ),
                      ),
                    ),
                  DropdownButtonFormField<String>(
                    initialValue: _type,
                    isExpanded: true,
                    decoration: const InputDecoration(labelText: '기록 유형'),
                    items: [
                      for (final type in manualEntryTypes.entries)
                        DropdownMenuItem(
                          value: type.key,
                          child: Text(type.value),
                        ),
                    ],
                    onChanged: (type) {
                      if (type == null || _submitted) return;
                      setState(() {
                        _type = type;
                        _errors = _attempted
                            ? manualEntryErrors(type, _values)
                            : {};
                      });
                    },
                  ),
                  for (final field in manualEntryFields(_type)) ...[
                    const SizedBox(height: 16),
                    TextFormField(
                      key: ValueKey('manual-$field'),
                      controller: _fields[field],
                      focusNode: _focus[field],
                      autocorrect: false,
                      enableSuggestions: false,
                      maxLength: field == 'corrects_source_event_id' ? 128 : 64,
                      decoration: InputDecoration(
                        labelText: manualEntryLabel(_type, field),
                        helperText: _helper(field),
                        helperMaxLines: 5,
                        errorMaxLines: 4,
                      ),
                      keyboardType:
                          const {
                            'quantity',
                            'price',
                            'fee',
                            'amount',
                            'counter_amount',
                          }.contains(field)
                          ? const TextInputType.numberWithOptions(
                              decimal: true,
                              signed: true,
                            )
                          : TextInputType.text,
                      onChanged: (_) => _changed(),
                      validator: (_) =>
                          manualEntryErrors(_type, _values)[field],
                    ),
                  ],
                  const SizedBox(height: 16),
                  const Text(
                    '기존 CSV 초안 전체를 이 한 건으로 바꿉니다. 다음 화면에서 미리보기를 만든 뒤 직접 반영해야 합니다. 취소하면 기존 입력과 결과를 보존합니다.',
                  ),
                  const SizedBox(height: 12),
                  ElevatedButton(
                    onPressed: _submitted ? null : _submit,
                    child: const Text('검토용 초안 만들기'),
                  ),
                  const SizedBox(height: 8),
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(),
                    child: const Text('취소'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    ),
  );
}

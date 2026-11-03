import 'models.dart';

const manualEntryTypes = {
  'BUY': '매수 기록',
  'SELL': '매도 기록',
  'DEPOSIT': '입금',
  'WITHDRAWAL': '출금',
  'DIVIDEND': '배당',
  'FEE': '수수료',
  'TAX': '세금',
  'SPLIT': '주식 분할',
  'FX_EXCHANGE': '환전',
  'CASH_VOID': '현금 기록 취소',
};

List<String> manualEntryFields(String type) => [
  'occurred_at',
  'currency',
  if (const {'BUY', 'SELL', 'DIVIDEND', 'SPLIT'}.contains(type)) 'symbol',
  if (const {'BUY', 'SELL', 'SPLIT'}.contains(type)) 'quantity',
  if (const {'BUY', 'SELL'}.contains(type)) ...['price', 'fee'],
  if (!const {'BUY', 'SELL', 'SPLIT'}.contains(type)) 'amount',
  if (type == 'FX_EXCHANGE') ...['counter_currency', 'counter_amount'],
  if (type == 'CASH_VOID') 'corrects_source_event_id',
];

String manualEntryLabel(String type, String field) => switch (field) {
  'occurred_at' => '거래 시각 (시간대 포함)',
  'currency' => type == 'FX_EXCHANGE' ? '내보내는 통화' : '통화',
  'symbol' => '종목 코드',
  'quantity' => type == 'SPLIT' ? '분할 배율' : '수량',
  'price' => '단가',
  'fee' => '수수료',
  'amount' => type == 'CASH_VOID' ? '취소 금액 (부호 포함)' : '금액 (양수)',
  'counter_currency' => '받는 통화',
  'counter_amount' => '받는 금액 (양수)',
  'corrects_source_event_id' => '취소할 기록 구분값',
  _ => field,
};

Map<String, String> manualEntryErrors(String type, Map<String, String> fields) {
  if (!manualEntryTypes.containsKey(type)) {
    return {'type': '지원하는 기록 유형을 선택하세요.'};
  }
  final errors = <String, String>{};
  for (final field in manualEntryFields(type)) {
    final value = (fields[field] ?? '').trim();
    final limit = field == 'corrects_source_event_id' ? 128 : 64;
    if (value.isEmpty || value.length > limit) {
      errors[field] = '값을 $limit자 이내로 입력하세요.';
      continue;
    }
    if (field == 'occurred_at') {
      if (!_manualTimestampValid(value)) {
        errors[field] = '유효한 날짜·시간대와 최대 9자리 소수초를 입력하세요.';
      }
    } else if (field.endsWith('currency')) {
      if (!RegExp(r'^[A-Z]{3}$').hasMatch(value.toUpperCase())) {
        errors[field] = 'USD 또는 KRW처럼 영문 3자리로 입력하세요.';
      }
    } else if (!const {'symbol', 'corrects_source_event_id'}.contains(field)) {
      final signed = field == 'amount' && type == 'CASH_VOID';
      try {
        final number = _manualDecimal(value, signed: signed);
        if (field != 'fee' && number == '0') {
          errors[field] = '0이 아닌 금액이나 수량을 입력하세요.';
        }
      } on FormatException {
        errors[field] = signed
            ? '구분자 없이 부호·숫자·소수점으로 입력하세요.'
            : '구분자 없이 양수와 소수점으로 입력하세요.';
      }
    }
  }
  if (type == 'FX_EXCHANGE' &&
      (fields['currency']?.trim().isNotEmpty ?? false) &&
      (fields['counter_currency']?.trim().isNotEmpty ?? false) &&
      fields['currency']?.trim().toUpperCase() ==
          fields['counter_currency']?.trim().toUpperCase()) {
    errors['counter_currency'] = '내보내는 통화와 다른 통화를 입력하세요.';
  }
  return errors;
}

String manualEntryCsv({
  required String sourceEventId,
  required String type,
  required Map<String, String> fields,
}) {
  if (!RegExp(r'^manual-[0-9a-f]{32}$').hasMatch(sourceEventId) ||
      manualEntryErrors(type, fields).isNotEmpty) {
    throw const FormatException('Invalid manual entry');
  }
  String text(String field) => (fields[field] ?? '').trim();
  String number(String field, {bool signed = false}) =>
      _manualDecimal(text(field), signed: signed);
  final trade = const {'BUY', 'SELL'}.contains(type);
  final symbol = const {'BUY', 'SELL', 'DIVIDEND', 'SPLIT'}.contains(type)
      ? text('symbol')
      : '';
  final quantity = trade || type == 'SPLIT' ? number('quantity') : '';
  final price = trade ? number('price') : '';
  final fee = trade ? number('fee') : '';
  final amount = trade
      ? tradeCashAmount(type, quantity, price, fee)
      : type == 'SPLIT'
      ? '0'
      : const {'WITHDRAWAL', 'FEE', 'TAX', 'FX_EXCHANGE'}.contains(type)
      ? '-${number('amount')}'
      : number('amount', signed: type == 'CASH_VOID');
  final row = [
    sourceEventId,
    'account-main',
    type,
    text('occurred_at'),
    symbol,
    quantity,
    price,
    fee,
    text('currency').toUpperCase(),
    amount,
    type == 'FX_EXCHANGE' ? text('counter_currency').toUpperCase() : '',
    type == 'FX_EXCHANGE' ? number('counter_amount') : '',
    type == 'CASH_VOID' ? text('corrects_source_event_id') : '',
  ];
  String cell(String value) => '"${value.replaceAll('"', '""')}"';
  return 'source_event_id,account_id,type,occurred_at,symbol,quantity,price,fee,currency,amount,counter_currency,counter_amount,corrects_source_event_id\n'
      '${row.map(cell).join(',')}\n';
}

String _manualDecimal(String value, {bool signed = false}) {
  final pattern = signed
      ? r'^-?[0-9]+(?:\.[0-9]+)?$'
      : r'^[0-9]+(?:\.[0-9]+)?$';
  if (!RegExp(pattern).hasMatch(value)) {
    throw const FormatException('Invalid manual decimal');
  }
  final negative = value.startsWith('-');
  final parts = (negative ? value.substring(1) : value).split('.');
  var whole = parts.first.replaceFirst(RegExp(r'^0+'), '');
  if (whole.isEmpty) whole = '0';
  final fraction = parts.length == 2
      ? parts.last.replaceFirst(RegExp(r'0+$'), '')
      : '';
  final canonical = fraction.isEmpty ? whole : '$whole.$fraction';
  return negative && canonical != '0' ? '-$canonical' : canonical;
}

bool _manualTimestampValid(String value) {
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-](\d{2}):(\d{2}))$',
  ).firstMatch(value);
  if (match == null) return false;
  int part(int index) => int.parse(match.group(index)!);
  final year = part(1), month = part(2), day = part(3);
  final date = DateTime.utc(year, month, day);
  return date.year == year &&
      date.month == month &&
      date.day == day &&
      part(4) <= 23 &&
      part(5) <= 59 &&
      part(6) <= 59 &&
      (match.group(8) == 'Z' || (part(9) <= 23 && part(10) <= 59));
}

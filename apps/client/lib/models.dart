import 'dart:convert';

typedef Json = Map<String, dynamic>;

void _requireExactKeys(Json value, Set<String> keys) {
  if (value.length != keys.length || !value.keys.every(keys.contains)) {
    throw const FormatException('Unexpected object fields');
  }
}

String _text(Json value, String key) {
  final result = value[key];
  if (result is! String || result.isEmpty) {
    throw const FormatException('Missing text field');
  }
  return result;
}

String _decimal(Json value, String key) {
  final result = _text(value, key);
  if (!RegExp(
    r'^(?:0|-?(?:[1-9][0-9]*(?:\.[0-9]*[1-9])?|0\.[0-9]*[1-9]))$',
  ).hasMatch(result)) {
    throw const FormatException('Decimal fields must be canonical strings');
  }
  return result;
}

int _count(Json value, String key) {
  final result = value[key];
  if (result is! int || result < 0) {
    throw const FormatException('Invalid count');
  }
  return result;
}

bool _bool(Json value, String key) {
  final result = value[key];
  if (result is! bool) {
    throw const FormatException('Missing bool field');
  }
  return result;
}

DateTime? _optionalDate(Json value, String key) {
  final result = value[key];
  if (result == null) return null;
  return _rfc3339(result);
}

DateTime _rfc3339(Object? value) {
  if (value is! String ||
      !RegExp(
        r'^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$',
      ).hasMatch(value)) {
    throw const FormatException('Date must be RFC3339 text');
  }
  try {
    return DateTime.parse(value);
  } on FormatException {
    throw const FormatException('Invalid RFC3339 date');
  }
}

String _rfc3339Text(Object? value) {
  _rfc3339(value);
  return value! as String;
}

String? _optionalRfc3339Text(Json value, String key) {
  final result = value[key];
  return result == null ? null : _rfc3339Text(result);
}

List<Json> _jsonList(Json value, String key) {
  final result = value[key];
  if (result is! List<dynamic>) {
    throw const FormatException('Missing list field');
  }
  return result
      .map((item) {
        if (item is! Json) {
          throw const FormatException('List item must be an object');
        }
        return item;
      })
      .toList(growable: false);
}

List<Json> _optionalJsonList(Json value, String key) {
  final result = value[key];
  if (result == null) {
    return const [];
  }
  if (result is! List<dynamic>) {
    throw const FormatException('List field must be an array');
  }
  return result
      .map((item) {
        if (item is! Json) {
          throw const FormatException('List item must be an object');
        }
        return item;
      })
      .toList(growable: false);
}

class ServiceStatus {
  const ServiceStatus({
    required this.liveEnabled,
    required this.mode,
    required this.trustState,
    required this.ledgerRevision,
    required this.lastVerifiedAt,
    required this.issues,
  });

  factory ServiceStatus.fromJson(Json json) {
    final liveEnabled = _bool(json, 'live_enabled');
    final mode = _text(json, 'mode');
    final trustState = _text(json, 'trust_state');
    if (liveEnabled || mode != 'local_import_only') {
      throw const FormatException('Unsupported service mode');
    }
    if (!const {
      'never_verified',
      'verified',
      'stale',
      'partial',
      'error',
    }.contains(trustState)) {
      throw const FormatException('Unsupported trust state');
    }
    final lastVerifiedAt = _optionalDate(json, 'last_verified_at');
    final issues = _jsonList(
      json,
      'issues',
    ).map((item) => _text(item, 'message')).toList(growable: false);
    if ((trustState == 'verified' && lastVerifiedAt == null) ||
        (trustState == 'never_verified' && lastVerifiedAt != null)) {
      throw const FormatException('Inconsistent verification state');
    }
    return ServiceStatus(
      liveEnabled: liveEnabled,
      mode: mode,
      trustState: trustState,
      ledgerRevision: _text(json, 'ledger_revision'),
      lastVerifiedAt: lastVerifiedAt,
      issues: issues,
    );
  }

  final bool liveEnabled;
  final String mode;
  final String trustState;
  final String ledgerRevision;
  final DateTime? lastVerifiedAt;
  final List<String> issues;
}

class Money {
  const Money(this.currency, this.amount);
  factory Money.fromJson(Json json) =>
      Money(_text(json, 'currency'), _decimal(json, 'amount'));

  final String currency;
  final String amount;
}

class Holding {
  const Holding({
    required this.symbol,
    required this.quantity,
    required this.costBasis,
    required this.currency,
  });
  factory Holding.fromJson(Json json) => Holding(
    symbol: _text(json, 'symbol'),
    quantity: _decimal(json, 'quantity'),
    costBasis: _decimal(json, 'cost_basis'),
    currency: _text(json, 'currency'),
  );

  final String symbol;
  final String quantity;
  final String costBasis;
  final String currency;
}

class PortfolioSnapshot {
  const PortfolioSnapshot({
    required this.ledgerRevision,
    required this.costBasisPolicy,
    required this.recordedAt,
    required this.cash,
    required this.holdings,
    required this.realizedPnl,
  });
  factory PortfolioSnapshot.fromJson(Json json) {
    if (_bool(json, 'live_enabled') ||
        _text(json, 'valuation_status') != 'unavailable') {
      throw const FormatException('Live valuation is not supported');
    }
    if (_text(json, 'cost_basis_policy') !=
        'fifo_exact_else_half_even_residual_8_v1') {
      throw const FormatException('Unsupported cost basis policy');
    }
    return PortfolioSnapshot(
      ledgerRevision: _text(json, 'ledger_revision'),
      costBasisPolicy: _text(json, 'cost_basis_policy'),
      recordedAt: DateTime.parse(_text(json, 'recorded_at')),
      cash: _moneyList(json, 'cash'),
      holdings: _jsonList(
        json,
        'holdings',
      ).map(Holding.fromJson).toList(growable: false),
      realizedPnl: _moneyList(json, 'realized_pnl'),
    );
  }

  final String ledgerRevision;
  final String costBasisPolicy;
  final DateTime recordedAt;
  final List<Money> cash;
  final List<Holding> holdings;
  final List<Money> realizedPnl;
}

List<Money> _moneyList(Json value, String key) =>
    _jsonList(value, key).map(Money.fromJson).toList(growable: false);

String _currency(Json value, String key) {
  final result = _text(value, key);
  if (!RegExp(r'^[A-Z]{3}$').hasMatch(result)) {
    throw const FormatException('Invalid currency');
  }
  return result;
}

String _utcText(Json value, String key) {
  final result = _rfc3339Text(value[key]);
  if (!RegExp(
    r'^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d{0,8}[1-9])?Z$',
  ).hasMatch(result)) {
    throw const FormatException('Timestamp must be canonical UTC');
  }
  final parsed = DateTime.parse(result);
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})',
  ).firstMatch(result)!;
  final parts = [
    parsed.year,
    parsed.month,
    parsed.day,
    parsed.hour,
    parsed.minute,
    parsed.second,
  ];
  for (var index = 0; index < parts.length; index += 1) {
    if (parts[index] != int.parse(match.group(index + 1)!)) {
      throw const FormatException('Invalid UTC timestamp');
    }
  }
  return result;
}

String? _nullableText(Json value, String key) {
  final result = value[key];
  if (result == null) return null;
  if (result is! String || result.isEmpty) {
    throw const FormatException('Invalid nullable text');
  }
  return result;
}

String? _nullableDecimal(Json value, String key) {
  if (value[key] == null) return null;
  return _decimal(value, key);
}

class LedgerActivityPage {
  const LedgerActivityPage({
    required this.source,
    required this.brokerFreshness,
    required this.ledgerRevision,
    required this.recordedAt,
    required this.events,
    required this.nextCursor,
  });

  factory LedgerActivityPage.fromJson(Json json) {
    _requireExactKeys(json, const {
      'source',
      'broker_freshness',
      'ledger_revision',
      'recorded_at',
      'events',
      'next_cursor',
    });
    final source = _text(json, 'source');
    final freshness = _text(json, 'broker_freshness');
    final ledgerRevision = _text(json, 'ledger_revision');
    final nextCursor = _nullableText(json, 'next_cursor');
    if (source != 'local_ledger' ||
        freshness != 'unverified' ||
        !RegExp(r'^rev_[0-9]{10}$').hasMatch(ledgerRevision)) {
      throw const FormatException('Invalid ledger activity metadata');
    }
    final events = _jsonList(
      json,
      'events',
    ).map(LedgerActivity.fromJson).toList(growable: false);
    if (events.length > 100) {
      throw const FormatException('Too many ledger activities');
    }
    for (var index = 1; index < events.length; index += 1) {
      if (DateTime.parse(
        events[index].occurredAt,
      ).isAfter(DateTime.parse(events[index - 1].occurredAt))) {
        throw const FormatException('Ledger activities must be newest first');
      }
    }
    return LedgerActivityPage(
      source: source,
      brokerFreshness: freshness,
      ledgerRevision: ledgerRevision,
      recordedAt: _utcText(json, 'recorded_at'),
      events: events,
      nextCursor: nextCursor,
    );
  }

  final String source;
  final String brokerFreshness;
  final String ledgerRevision;
  final String recordedAt;
  final List<LedgerActivity> events;
  final String? nextCursor;
}

class LedgerActivity {
  const LedgerActivity({
    required this.type,
    required this.occurredAt,
    required this.recordedAt,
    required this.symbol,
    required this.quantity,
    required this.price,
    required this.fee,
    required this.currency,
    required this.amount,
    required this.counterCurrency,
    required this.counterAmount,
    required this.isCorrection,
  });

  factory LedgerActivity.fromJson(Json json) {
    _requireExactKeys(json, const {
      'type',
      'occurred_at',
      'recorded_at',
      'symbol',
      'quantity',
      'price',
      'fee',
      'currency',
      'amount',
      'counter_currency',
      'counter_amount',
      'is_correction',
    });
    final type = _text(json, 'type');
    final symbol = _nullableText(json, 'symbol');
    final quantity = _nullableDecimal(json, 'quantity');
    final price = _nullableDecimal(json, 'price');
    final fee = _nullableDecimal(json, 'fee');
    final currency = _currency(json, 'currency');
    final amount = _decimal(json, 'amount');
    final counterCurrency = _nullableText(json, 'counter_currency');
    final counterAmount = _nullableDecimal(json, 'counter_amount');
    final isCorrection = _bool(json, 'is_correction');
    final noTrade =
        symbol == null && quantity == null && price == null && fee == null;
    final noCounter = counterCurrency == null && counterAmount == null;
    final positive = !amount.startsWith('-') && amount != '0';
    final negative = amount.startsWith('-');
    final trade = const {'BUY', 'SELL'}.contains(type);
    var valid = isCorrection == (type == 'CASH_VOID');
    if (trade) {
      valid =
          valid &&
          symbol != null &&
          quantity != null &&
          quantity != '0' &&
          !quantity.startsWith('-') &&
          price != null &&
          price != '0' &&
          !price.startsWith('-') &&
          fee != null &&
          !fee.startsWith('-') &&
          noCounter &&
          ((type == 'BUY' && negative) || (type == 'SELL' && positive));
    } else if (type == 'DIVIDEND') {
      valid =
          valid &&
          symbol != null &&
          quantity == null &&
          price == null &&
          fee == null &&
          noCounter &&
          positive;
    } else if (type == 'SPLIT') {
      valid =
          valid &&
          symbol != null &&
          quantity != null &&
          quantity != '0' &&
          !quantity.startsWith('-') &&
          price == null &&
          fee == null &&
          noCounter &&
          amount == '0';
    } else if (type == 'DEPOSIT') {
      valid = valid && noTrade && noCounter && positive;
    } else if (const {'WITHDRAWAL', 'FEE', 'TAX'}.contains(type)) {
      valid = valid && noTrade && noCounter && negative;
    } else if (type == 'CASH_VOID') {
      valid = valid && noTrade && noCounter && amount != '0';
    } else if (type == 'FX_EXCHANGE') {
      valid =
          valid &&
          noTrade &&
          negative &&
          counterCurrency != null &&
          RegExp(r'^[A-Z]{3}$').hasMatch(counterCurrency) &&
          counterCurrency != currency &&
          counterAmount != null &&
          counterAmount != '0' &&
          !counterAmount.startsWith('-');
    } else {
      valid = false;
    }
    if (!valid) {
      throw const FormatException('Invalid ledger activity');
    }
    return LedgerActivity(
      type: type,
      occurredAt: _utcText(json, 'occurred_at'),
      recordedAt: _utcText(json, 'recorded_at'),
      symbol: symbol,
      quantity: quantity,
      price: price,
      fee: fee,
      currency: currency,
      amount: amount,
      counterCurrency: counterCurrency,
      counterAmount: counterAmount,
      isCorrection: isCorrection,
    );
  }

  final String type;
  final String occurredAt;
  final String recordedAt;
  final String? symbol;
  final String? quantity;
  final String? price;
  final String? fee;
  final String currency;
  final String amount;
  final String? counterCurrency;
  final String? counterAmount;
  final bool isCorrection;
}

class ImportPreview {
  const ImportPreview({
    required this.previewId,
    required this.fileSha256,
    required this.schemaVersion,
    required this.mappingVersion,
    required this.ledgerRevision,
    required this.previewFingerprint,
    required this.canApply,
    required this.totalRows,
    required this.newRows,
    required this.duplicateRows,
    required this.errorRows,
    required this.unresolvedRows,
    required this.rows,
  });
  factory ImportPreview.fromJson(Json json) {
    final totals = json['totals'];
    if (totals is! Json) throw const FormatException('Missing totals');
    return ImportPreview(
      previewId: _text(json, 'preview_id'),
      fileSha256: _text(json, 'file_sha256'),
      schemaVersion: _text(json, 'schema_version'),
      mappingVersion: _text(json, 'mapping_version'),
      ledgerRevision: _text(json, 'ledger_revision'),
      previewFingerprint: _text(json, 'preview_fingerprint'),
      canApply: _bool(json, 'can_apply'),
      totalRows: _count(totals, 'total_rows'),
      newRows: _count(totals, 'new_rows'),
      duplicateRows: _count(totals, 'duplicate_rows'),
      errorRows: _count(totals, 'error_rows'),
      unresolvedRows: _count(totals, 'unresolved_rows'),
      rows: _jsonList(
        json,
        'rows',
      ).map(PreviewRow.fromJson).toList(growable: false),
    );
  }

  final String previewId;
  final String fileSha256;
  final String schemaVersion;
  final String mappingVersion;
  final String ledgerRevision;
  final String previewFingerprint;
  final bool canApply;
  final int totalRows;
  final int newRows;
  final int duplicateRows;
  final int errorRows;
  final int unresolvedRows;
  final List<PreviewRow> rows;
}

class PreviewRow {
  const PreviewRow({
    required this.rowNumber,
    required this.status,
    this.symbol,
    this.transactionType,
    this.currency,
    this.amount,
    this.counterCurrency,
    this.counterAmount,
    this.correctionTarget,
    this.errors = const [],
  });
  factory PreviewRow.fromJson(Json json) {
    final transaction = json['transaction'];
    if (transaction != null && transaction is! Json) {
      throw const FormatException('Invalid transaction');
    }
    final transactionType = transaction is Json
        ? _text(transaction, 'type')
        : null;
    final targetJson = json['correction_target'];
    if (targetJson != null && targetJson is! Json) {
      throw const FormatException('Invalid correction target');
    }
    final correctionTarget = targetJson is Json
        ? CorrectionTarget.fromJson(targetJson)
        : null;
    final counterCurrency =
        transaction is Json && transaction['counter_currency'] is String
        ? _text(transaction, 'counter_currency')
        : null;
    final counterAmount =
        transaction is Json && transaction['counter_amount'] is String
        ? _decimal(transaction, 'counter_amount')
        : null;
    if ((counterCurrency == null) != (counterAmount == null) ||
        (transactionType == 'FX_EXCHANGE') != (counterCurrency != null)) {
      throw const FormatException(
        'FX_EXCHANGE requires one complete counter leg',
      );
    }
    final amount = transaction is Json ? _decimal(transaction, 'amount') : null;
    if (transactionType == 'FX_EXCHANGE' &&
        (amount == null ||
            !amount.startsWith('-') ||
            counterAmount == '0' ||
            counterAmount!.startsWith('-') ||
            counterCurrency == _text(transaction as Json, 'currency'))) {
      throw const FormatException('Invalid FX_EXCHANGE cash legs');
    }
    if ((transactionType == 'CASH_VOID') != (correctionTarget != null)) {
      throw const FormatException('CASH_VOID requires one correction target');
    }
    return PreviewRow(
      rowNumber: _count(json, 'row_number'),
      status: _text(json, 'status'),
      symbol: transaction is Json && transaction['symbol'] is String
          ? transaction['symbol'] as String
          : null,
      transactionType: transactionType,
      currency: transaction is Json ? _text(transaction, 'currency') : null,
      amount: amount,
      counterCurrency: counterCurrency,
      counterAmount: counterAmount,
      correctionTarget: correctionTarget,
      errors: _optionalJsonList(
        json,
        'errors',
      ).map((item) => _text(item, 'message')).toList(growable: false),
    );
  }

  final int rowNumber;
  final String status;
  final String? symbol;
  final String? transactionType;
  final String? currency;
  final String? amount;
  final String? counterCurrency;
  final String? counterAmount;
  final CorrectionTarget? correctionTarget;
  final List<String> errors;
}

class CorrectionTarget {
  const CorrectionTarget({
    required this.sourceEventId,
    required this.type,
    required this.currency,
    required this.amount,
  });

  factory CorrectionTarget.fromJson(Json json) {
    _requireExactKeys(json, const {
      'source_event_id',
      'type',
      'currency',
      'amount',
    });
    return CorrectionTarget(
      sourceEventId: _text(json, 'source_event_id'),
      type: _text(json, 'type'),
      currency: _text(json, 'currency'),
      amount: _decimal(json, 'amount'),
    );
  }

  final String sourceEventId;
  final String type;
  final String currency;
  final String amount;
}

class ApplyReceipt {
  const ApplyReceipt({
    required this.receiptId,
    required this.appliedRows,
    required this.skippedDuplicateRows,
    required this.ledgerRevisionAfter,
    required this.recordedAt,
  });
  factory ApplyReceipt.fromJson(Json json) => ApplyReceipt(
    receiptId: _text(json, 'receipt_id'),
    appliedRows: _count(json, 'applied_rows'),
    skippedDuplicateRows: _count(json, 'skipped_duplicate_rows'),
    ledgerRevisionAfter: _text(json, 'ledger_revision_after'),
    recordedAt: DateTime.parse(_text(json, 'recorded_at')),
  );

  final String receiptId;
  final int appliedRows;
  final int skippedDuplicateRows;
  final String ledgerRevisionAfter;
  final DateTime recordedAt;
}

class MarketIssue {
  const MarketIssue({required this.code, required this.message});

  factory MarketIssue.fromJson(Json json) =>
      MarketIssue(code: _text(json, 'code'), message: _text(json, 'message'));

  final String code;
  final String message;
}

class MarketBar {
  const MarketBar({
    required this.at,
    required this.open,
    required this.high,
    required this.low,
    required this.close,
    required this.volume,
  });

  factory MarketBar.fromJson(Json json) {
    final open = _positiveDecimal(json, 'open');
    final high = _positiveDecimal(json, 'high');
    final low = _positiveDecimal(json, 'low');
    final close = _positiveDecimal(json, 'close');
    final volume = _nonnegativeDecimal(json, 'volume');
    if (_comparePositiveDecimal(low, open) > 0 ||
        _comparePositiveDecimal(low, close) > 0 ||
        _comparePositiveDecimal(high, open) < 0 ||
        _comparePositiveDecimal(high, close) < 0) {
      throw const FormatException('OHLC values are inconsistent');
    }
    final at = _rfc3339Text(json['at']);
    return MarketBar(
      at: at,
      open: open,
      high: high,
      low: low,
      close: close,
      volume: volume,
    );
  }

  final String at;
  final String open;
  final String high;
  final String low;
  final String close;
  final String volume;
}

String _positiveDecimal(Json json, String key) {
  final value = _decimal(json, key);
  if (value == '0' || value.startsWith('-')) {
    throw const FormatException('Price must be positive');
  }
  return _drawableDecimal(value);
}

String _nonnegativeDecimal(Json json, String key) {
  final value = _decimal(json, key);
  if (value.startsWith('-')) {
    throw const FormatException('Volume must be nonnegative');
  }
  return _drawableDecimal(value);
}

String _drawableDecimal(String value) {
  final parsed = double.tryParse(value);
  if (parsed == null || !parsed.isFinite) {
    throw const FormatException('Decimal is outside the drawable range');
  }
  return value;
}

int _comparePositiveDecimal(String left, String right) {
  final leftParts = left.split('.');
  final rightParts = right.split('.');
  if (leftParts.first.length != rightParts.first.length) {
    return leftParts.first.length.compareTo(rightParts.first.length);
  }
  final integer = leftParts.first.compareTo(rightParts.first);
  if (integer != 0) return integer;
  final width = [
    leftParts.length == 1 ? 0 : leftParts.last.length,
    rightParts.length == 1 ? 0 : rightParts.last.length,
  ].reduce((a, b) => a > b ? a : b);
  final leftFraction = (leftParts.length == 1 ? '' : leftParts.last).padRight(
    width,
    '0',
  );
  final rightFraction = (rightParts.length == 1 ? '' : rightParts.last)
      .padRight(width, '0');
  return leftFraction.compareTo(rightFraction);
}

class MarketCandles {
  const MarketCandles({
    required this.symbol,
    required this.venue,
    required this.timezone,
    required this.interval,
    required this.priceAdjustment,
    required this.source,
    required this.sample,
    required this.state,
    required this.sourceAsOf,
    required this.fetchedAt,
    required this.issues,
    required this.bars,
  });

  factory MarketCandles.fromJson(Json json) {
    final source = _text(json, 'source');
    final state = _text(json, 'state');
    final priceAdjustment = _text(json, 'price_adjustment');
    if (!const {'empty', 'partial', 'stale', 'success'}.contains(state)) {
      throw const FormatException('Unsupported market state');
    }
    if (!const {'unspecified', 'provider_adjusted'}.contains(priceAdjustment)) {
      throw const FormatException('Unsupported market price adjustment');
    }
    if (_text(json, 'interval') != '1d') {
      throw const FormatException('Unsupported market interval');
    }
    final sample = _bool(json, 'sample');
    final sourceAsOf = _optionalRfc3339Text(json, 'source_as_of');
    final barJson = _jsonList(json, 'bars');
    if (barJson.length > 500) {
      throw const FormatException('Too many market bars');
    }
    final bars = barJson.map(MarketBar.fromJson).toList(growable: false);
    for (var index = 1; index < bars.length; index++) {
      if (!_rfc3339(bars[index - 1].at).isBefore(_rfc3339(bars[index].at))) {
        throw const FormatException('Bars must be strictly ordered');
      }
    }
    final issues = _jsonList(
      json,
      'issues',
    ).map(MarketIssue.fromJson).toList(growable: false);
    final valid = switch (state) {
      'empty' => bars.isEmpty && issues.isEmpty,
      'success' => bars.isNotEmpty && issues.isEmpty,
      'partial' => bars.isNotEmpty && issues.isNotEmpty,
      'stale' => bars.isNotEmpty,
      _ => false,
    };
    if (!valid) throw const FormatException('Inconsistent market state');
    if (source == 'local_fixture' &&
        (!sample ||
            priceAdjustment != 'unspecified' ||
            state != 'stale' ||
            sourceAsOf == null ||
            issues.isEmpty)) {
      throw const FormatException('Inconsistent local fixture provenance');
    }
    return MarketCandles(
      symbol: _text(json, 'symbol'),
      venue: _text(json, 'venue'),
      timezone: _text(json, 'timezone'),
      interval: '1d',
      priceAdjustment: priceAdjustment,
      source: source,
      sample: sample,
      state: state,
      sourceAsOf: sourceAsOf,
      fetchedAt: _rfc3339Text(json['fetched_at']),
      issues: issues,
      bars: bars,
    );
  }

  final String symbol;
  final String venue;
  final String timezone;
  final String interval;
  final String priceAdjustment;
  final String source;
  final bool sample;
  final String state;
  final String? sourceAsOf;
  final String fetchedAt;
  final List<MarketIssue> issues;
  final List<MarketBar> bars;
}

class LocalOrderView {
  const LocalOrderView({
    required this.mode,
    required this.symbol,
    required this.side,
    required this.orderType,
    required this.quantity,
    required this.limitPrice,
    required this.filledQuantity,
    required this.currency,
    required this.status,
    required this.pendingAction,
    required this.lastRecordedAt,
  });

  factory LocalOrderView.fromJson(Json json) {
    _requireExactKeys(json, const {
      'mode',
      'symbol',
      'side',
      'order_type',
      'quantity',
      'limit_price',
      'filled_quantity',
      'currency',
      'status',
      'pending_action',
      'last_recorded_at',
    });
    final mode = _text(json, 'mode');
    final symbol = _text(json, 'symbol');
    final side = _text(json, 'side');
    final orderType = _text(json, 'order_type');
    final quantity = _text(json, 'quantity');
    final limitPrice = _decimal(json, 'limit_price');
    final filledQuantity = _decimal(json, 'filled_quantity');
    final currency = _text(json, 'currency');
    final status = _text(json, 'status');
    final pendingAction = _text(json, 'pending_action');
    final pendingActionMatchesStatus = switch (pendingAction) {
      'SUBMIT' => status == 'SUBMIT_UNKNOWN',
      'CANCEL' => status == 'CANCEL_UNKNOWN' || status == 'FILLED',
      'none' => status != 'SUBMIT_UNKNOWN' && status != 'CANCEL_UNKNOWN',
      _ => false,
    };
    if (!const {'synthetic', 'paper'}.contains(mode) ||
        !RegExp(r'^[0-9]{6}$').hasMatch(symbol) ||
        !const {'BUY', 'SELL'}.contains(side) ||
        orderType != 'LIMIT' ||
        !RegExp(r'^[1-9][0-9]*$').hasMatch(quantity) ||
        limitPrice == '0' ||
        limitPrice.startsWith('-') ||
        filledQuantity.startsWith('-') ||
        _comparePositiveDecimal(filledQuantity, quantity) > 0 ||
        currency != 'KRW' ||
        !pendingActionMatchesStatus ||
        !const {
          'RECORDED',
          'READY',
          'RISK_REJECTED',
          'SUBMIT_UNKNOWN',
          'OPEN',
          'REJECTED',
          'PARTIALLY_FILLED',
          'CANCEL_UNKNOWN',
          'CANCELED',
          'FILLED',
        }.contains(status)) {
      throw const FormatException('Invalid local order lifecycle row');
    }
    return LocalOrderView(
      mode: mode,
      symbol: symbol,
      side: side,
      orderType: orderType,
      quantity: quantity,
      limitPrice: limitPrice,
      filledQuantity: filledQuantity,
      currency: currency,
      status: status,
      pendingAction: pendingAction,
      lastRecordedAt: _rfc3339Text(json['last_recorded_at']),
    );
  }

  final String mode;
  final String symbol;
  final String side;
  final String orderType;
  final String quantity;
  final String limitPrice;
  final String filledQuantity;
  final String currency;
  final String status;
  final String pendingAction;
  final String lastRecordedAt;
}

class LocalOrderLog {
  const LocalOrderLog({required this.orders});

  factory LocalOrderLog.fromJson(Json json) {
    _requireExactKeys(json, const {'source', 'broker_freshness', 'orders'});
    if (_text(json, 'source') != 'local_order_log' ||
        _text(json, 'broker_freshness') != 'unverified') {
      throw const FormatException('Invalid local order provenance');
    }
    final orders = _jsonList(
      json,
      'orders',
    ).map(LocalOrderView.fromJson).toList(growable: false);
    return LocalOrderLog(orders: orders);
  }

  final List<LocalOrderView> orders;
}

class BrokerPositionDifference {
  const BrokerPositionDifference({
    required this.symbol,
    required this.brokerQuantity,
    required this.ledgerQuantity,
    required this.difference,
    required this.match,
  });

  factory BrokerPositionDifference.fromJson(Json json) {
    _requireExactKeys(json, const {
      'symbol',
      'broker_quantity',
      'ledger_quantity',
      'difference',
      'match',
    });
    final symbol = _text(json, 'symbol');
    final brokerQuantity = _decimal(json, 'broker_quantity');
    final ledgerQuantity = _decimal(json, 'ledger_quantity');
    final difference = _decimal(json, 'difference');
    final match = _bool(json, 'match');
    if (!RegExp(r'^[0-9]{6}$').hasMatch(symbol) ||
        brokerQuantity.startsWith('-') ||
        ledgerQuantity.startsWith('-') ||
        match != (difference == '0')) {
      throw const FormatException('Invalid broker reconciliation row');
    }
    return BrokerPositionDifference(
      symbol: symbol,
      brokerQuantity: brokerQuantity,
      ledgerQuantity: ledgerQuantity,
      difference: difference,
      match: match,
    );
  }

  final String symbol;
  final String brokerQuantity;
  final String ledgerQuantity;
  final String difference;
  final bool match;
}

class BrokerReconciliation {
  const BrokerReconciliation({
    required this.provider,
    required this.environment,
    required this.exchange,
    required this.freshness,
    required this.fetchedAt,
    required this.recordedAt,
    required this.ledgerRevision,
    required this.allPositionsMatch,
    required this.positionDifferences,
  });

  factory BrokerReconciliation.fromJson(Json json) {
    _requireExactKeys(json, const {
      'provider',
      'environment',
      'exchange',
      'freshness',
      'fetched_at',
      'recorded_at',
      'ledger_revision',
      'all_positions_match',
      'position_differences',
    });
    final provider = _text(json, 'provider');
    final environment = _text(json, 'environment');
    final exchange = _text(json, 'exchange');
    final freshness = _text(json, 'freshness');
    final ledgerRevision = _text(json, 'ledger_revision');
    final allPositionsMatch = _bool(json, 'all_positions_match');
    final differences = _jsonList(
      json,
      'position_differences',
    ).map(BrokerPositionDifference.fromJson).toList(growable: false);
    var previousSymbol = '';
    for (final difference in differences) {
      if (difference.symbol.compareTo(previousSymbol) <= 0) {
        throw const FormatException(
          'Broker reconciliation symbols must be ordered',
        );
      }
      previousSymbol = difference.symbol;
    }
    if (provider != 'kiwoom' ||
        !const {'mock', 'production'}.contains(environment) ||
        exchange != 'KRX' ||
        freshness != 'unverified' ||
        !RegExp(r'^rev_[0-9]{10}$').hasMatch(ledgerRevision) ||
        allPositionsMatch != differences.every((item) => item.match)) {
      throw const FormatException('Invalid broker reconciliation');
    }
    return BrokerReconciliation(
      provider: provider,
      environment: environment,
      exchange: exchange,
      freshness: freshness,
      fetchedAt: _rfc3339Text(json['fetched_at']),
      recordedAt: _rfc3339Text(json['recorded_at']),
      ledgerRevision: ledgerRevision,
      allPositionsMatch: allPositionsMatch,
      positionDifferences: differences,
    );
  }

  final String provider;
  final String environment;
  final String exchange;
  final String freshness;
  final String fetchedAt;
  final String recordedAt;
  final String ledgerRevision;
  final bool allPositionsMatch;
  final List<BrokerPositionDifference> positionDifferences;
}

Json decodeObject(String body) {
  final value = jsonDecode(body);
  if (value is! Json) throw const FormatException('Expected JSON object');
  return value;
}

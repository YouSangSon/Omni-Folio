import 'dart:convert';

typedef Json = Map<String, dynamic>;

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
    return PortfolioSnapshot(
      ledgerRevision: _text(json, 'ledger_revision'),
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
  final DateTime recordedAt;
  final List<Money> cash;
  final List<Holding> holdings;
  final List<Money> realizedPnl;
}

List<Money> _moneyList(Json value, String key) =>
    _jsonList(value, key).map(Money.fromJson).toList(growable: false);

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
    this.errors = const [],
  });
  factory PreviewRow.fromJson(Json json) {
    final transaction = json['transaction'];
    return PreviewRow(
      rowNumber: _count(json, 'row_number'),
      status: _text(json, 'status'),
      symbol: transaction is Json && transaction['symbol'] is String
          ? transaction['symbol'] as String
          : null,
      errors: _optionalJsonList(
        json,
        'errors',
      ).map((item) => _text(item, 'message')).toList(growable: false),
    );
  }

  final int rowNumber;
  final String status;
  final String? symbol;
  final List<String> errors;
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

Json decodeObject(String body) {
  final value = jsonDecode(body);
  if (value is! Json) throw const FormatException('Expected JSON object');
  return value;
}

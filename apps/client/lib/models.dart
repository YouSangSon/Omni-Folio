import 'dart:convert';

typedef Json = Map<String, dynamic>;

// Correlates a server input-only review with the exact posted text, not a local hash proof.
class OwnerDataPreview {
  OwnerDataPreview._(this.kind, this.input, Json summary)
    : summary = Map.unmodifiable(summary);

  factory OwnerDataPreview.fromJson(Json json, String input) {
    _requireExactKeys(json, {'kind', 'input', 'summary'});
    final kind = _text(json, 'kind');
    if (json['input'] != input || json['summary'] is! Json) {
      throw const FormatException('Owner input review does not match request');
    }
    final summary = json['summary'] as Json;
    final source = decodeObject(input);
    final schema = switch (kind) {
      'security_price' => 'owner-price-series.v1',
      'fx' => 'owner-fx-series.v1',
      'benchmark' => 'benchmark-index-series.v1',
      _ => throw const FormatException('Unknown owner data kind'),
    };
    final identityKeys = switch (kind) {
      'security_price' => {
        'instrument_id',
        'symbol',
        'venue',
        'currency',
        'price_adjustment',
      },
      'fx' => {'base_currency', 'quote_currency'},
      _ => {
        'benchmark_id',
        'label',
        'currency',
        'return_basis',
        'source_reference',
      },
    };
    _requireExactKeys(summary, {
      ...identityKeys,
      'authority',
      'source_version',
      'sample',
      'data_sha256',
      'point_count',
      'first_observed_at',
      'last_observed_at',
      'fetched_at',
    });
    if (source['schema_version'] != schema ||
        summary['authority'] != 'owner_declared') {
      throw const FormatException('Invalid owner data authority');
    }
    for (final key in {
      ...identityKeys,
      'source_version',
      'sample',
      'fetched_at',
    }) {
      if (summary[key] != source[key]) {
        throw const FormatException('Owner metadata does not match input');
      }
    }
    _benchmarkText(summary, 'source_version', 128, identity: true);
    _bool(summary, 'sample');
    for (final key in identityKeys) {
      if (key.contains('currency')) {
        _currency(summary, key);
      } else {
        _benchmarkText(
          summary,
          key,
          key == 'source_reference'
              ? 512
              : key == 'label'
              ? 160
              : 128,
          identity: key != 'label',
          trimmed: key == 'label',
        );
      }
    }
    if (kind == 'security_price' &&
        (summary['price_adjustment'] != 'unadjusted' ||
            summary['symbol'] != (summary['symbol'] as String).toUpperCase() ||
            summary['venue'] != (summary['venue'] as String).toUpperCase())) {
      throw const FormatException('Invalid owner price identity');
    }
    if (kind == 'fx' && summary['base_currency'] == summary['quote_currency']) {
      throw const FormatException('Invalid FX direction');
    }
    if (kind == 'benchmark') {
      final uri = Uri.tryParse(summary['source_reference'] as String);
      if (!const {'KRW', 'USD'}.contains(summary['currency']) ||
          !const {
            'price_index',
            'gross_total_return',
            'net_total_return',
          }.contains(summary['return_basis']) ||
          uri == null ||
          uri.scheme != 'https' ||
          uri.host.isEmpty ||
          uri.userInfo.isNotEmpty ||
          uri.hasQuery ||
          uri.hasFragment) {
        throw const FormatException('Invalid benchmark metadata');
      }
    }
    final fetched = _utcText(summary, 'fetched_at');
    final count = _count(summary, 'point_count');
    final points = source['points'];
    if (points is! List ||
        points.length != count ||
        count > 10000 ||
        (kind == 'benchmark' && count == 0) ||
        !RegExp(r'^[0-9a-f]{64}$').hasMatch(_text(summary, 'data_sha256'))) {
      throw const FormatException('Invalid owner data point summary');
    }
    if (count == 0) {
      if (summary['first_observed_at'] != null ||
          summary['last_observed_at'] != null) {
        throw const FormatException('Empty owner series has observations');
      }
    } else {
      final first = _utcText(summary, 'first_observed_at');
      final last = _utcText(summary, 'last_observed_at');
      if (points.first is! Json ||
          points.last is! Json ||
          points.first['observed_at'] != first ||
          points.last['observed_at'] != last ||
          _utcNanoseconds(first) > _utcNanoseconds(last) ||
          ((count == 1) != (first == last)) ||
          _utcNanoseconds(last) > _utcNanoseconds(fetched)) {
        throw const FormatException(
          'Owner observation range does not match input',
        );
      }
    }
    return OwnerDataPreview._(kind, input, summary);
  }

  final String kind, input;
  final Json summary;
}

class OwnerDataReceipt {
  const OwnerDataReceipt._(this.review, this.recordedAt);
  factory OwnerDataReceipt.fromJson(Json json, OwnerDataPreview review) {
    _requireExactKeys(json, {'kind', 'receipt'});
    if (json['kind'] != review.kind || json['receipt'] is! Json) {
      throw const FormatException('Owner receipt kind does not match review');
    }
    final receipt = json['receipt'] as Json;
    _requireExactKeys(receipt, {
      ...review.summary.keys,
      'storage_check',
      'recorded_at',
    });
    if (receipt['storage_check'] != 'verified' ||
        review.summary.keys.any((key) => receipt[key] != review.summary[key])) {
      throw const FormatException('Owner receipt does not match review');
    }
    final recorded = _utcText(receipt, 'recorded_at');
    if (_utcNanoseconds(recorded) <
        _utcNanoseconds(review.summary['fetched_at'] as String)) {
      throw const FormatException('Owner receipt predates capture');
    }
    return OwnerDataReceipt._(review, recorded);
  }
  final OwnerDataPreview review;
  final String recordedAt;
}

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
  if (!_canonicalDecimalPattern.hasMatch(result)) {
    throw const FormatException('Decimal fields must be canonical strings');
  }
  return result;
}

final _canonicalDecimalPattern = RegExp(
  r'^(?:0|-?(?:[1-9][0-9]*(?:\.[0-9]*[1-9])?|0\.[0-9]*[1-9]))$',
);

String decimalPercent(String value) {
  if (!_canonicalDecimalPattern.hasMatch(value)) {
    throw const FormatException('Invalid decimal percent');
  }
  final negative = value.startsWith('-');
  final parts = (negative ? value.substring(1) : value).split('.');
  final fraction = parts.length == 2 ? parts[1] : '';
  var whole = '${parts[0]}${fraction.padRight(2, '0').substring(0, 2)}';
  whole = whole.replaceFirst(RegExp(r'^0+(?=[0-9])'), '');
  final remainder = fraction.length > 2 ? fraction.substring(2) : '';
  return '${negative ? '-' : ''}$whole${remainder.isEmpty ? '' : '.$remainder'}%';
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
    required this.instrumentId,
    required this.symbol,
    required this.quantity,
    required this.costBasis,
    required this.currency,
  });
  factory Holding.fromJson(Json json) => Holding(
    instrumentId: _text(json, 'instrument_id'),
    symbol: _text(json, 'symbol'),
    quantity: _decimal(json, 'quantity'),
    costBasis: _decimal(json, 'cost_basis'),
    currency: _text(json, 'currency'),
  );

  final String instrumentId;
  final String symbol;
  final String quantity;
  final String costBasis;
  final String currency;
}

class HoldingValuationIssue {
  const HoldingValuationIssue({
    required this.code,
    required this.message,
    required this.field,
  });

  factory HoldingValuationIssue.fromJson(Json json) {
    final keys = json.keys.toSet();
    if (keys.length < 2 ||
        keys.length > 3 ||
        !keys.containsAll(const {'code', 'message'}) ||
        !keys.every(const {'code', 'message', 'field'}.contains)) {
      throw const FormatException('Unexpected valuation issue fields');
    }
    return HoldingValuationIssue(
      code: _text(json, 'code'),
      message: _text(json, 'message'),
      field: _nullableText(json, 'field'),
    );
  }

  final String code;
  final String message;
  final String? field;
}

class HoldingValuationPrice {
  const HoldingValuationPrice({
    required this.source,
    this.sourceVersion,
    required this.venue,
    required this.currency,
    required this.price,
    required this.priceAdjustment,
    required this.observedAt,
    required this.fetchedAt,
    required this.recordedAt,
    required this.sample,
    required this.state,
  });

  factory HoldingValuationPrice.fromJson(Json json) {
    final owner = json['source'] == 'owner_declared';
    _requireExactKeys(json, {
      'source',
      'venue',
      'currency',
      'price',
      'price_adjustment',
      'observed_at',
      'fetched_at',
      'recorded_at',
      'sample',
      'state',
      if (owner) 'source_version',
    });
    final price = _decimal(json, 'price');
    final source = _text(json, 'source');
    final adjustment = _text(json, 'price_adjustment');
    final sample = _bool(json, 'sample');
    final state = _text(json, 'state');
    final venue = _text(json, 'venue');
    if (price == '0' ||
        price.startsWith('-') ||
        (owner
            ? price.length > 64 ||
                  adjustment != 'unadjusted' ||
                  !const {'unknown', 'stale'}.contains(state)
            : source != 'local_fixture' ||
                  adjustment != 'unspecified' ||
                  !sample ||
                  state != 'stale')) {
      throw const FormatException('Invalid holding price provenance');
    }
    if (owner &&
        (_benchmarkText(json, 'venue', 128, identity: true) != venue ||
            venue != venue.toUpperCase())) {
      throw const FormatException('Invalid owner price venue');
    }
    return HoldingValuationPrice(
      source: source,
      sourceVersion: owner
          ? _benchmarkText(json, 'source_version', 128, identity: true)
          : null,
      venue: venue,
      currency: _currency(json, 'currency'),
      price: price,
      priceAdjustment: adjustment,
      observedAt: _utcText(json, 'observed_at'),
      fetchedAt: _utcText(json, 'fetched_at'),
      recordedAt: _utcText(json, 'recorded_at'),
      sample: sample,
      state: state,
    );
  }

  final String source;
  final String? sourceVersion;
  final String venue;
  final String currency;
  final String price;
  final String priceAdjustment;
  final String observedAt;
  final String fetchedAt;
  final String recordedAt;
  final bool sample;
  final String state;
}

class HoldingValuationLine {
  const HoldingValuationLine({
    required this.symbol,
    required this.quantity,
    required this.costBasis,
    required this.currency,
    required this.status,
    required this.price,
    required this.marketValue,
    required this.unrealizedPnl,
    required this.issue,
  });

  factory HoldingValuationLine.fromJson(Json json) {
    _requireExactKeys(json, const {
      'symbol',
      'quantity',
      'cost_basis',
      'currency',
      'status',
      'price',
      'market_value',
      'unrealized_pnl',
      'issue',
    });
    final quantity = _decimal(json, 'quantity');
    final costBasis = _decimal(json, 'cost_basis');
    final status = _text(json, 'status');
    final priceJson = json['price'];
    final issueJson = json['issue'];
    if (priceJson != null && priceJson is! Json ||
        issueJson != null && issueJson is! Json) {
      throw const FormatException('Invalid holding valuation object');
    }
    final price = priceJson is Json
        ? HoldingValuationPrice.fromJson(priceJson)
        : null;
    final marketValue = _nullableDecimal(json, 'market_value');
    final unrealizedPnl = _nullableDecimal(json, 'unrealized_pnl');
    final issue = issueJson is Json
        ? HoldingValuationIssue.fromJson(issueJson)
        : null;
    final valued = status == 'valued';
    final stale = status == 'stale';
    final splitBasis = status == 'split_basis';
    final expectedIssueCode = switch (status) {
      'missing' => 'missing_security_price',
      'ambiguous' => 'ambiguous_security_price_venue',
      'stale' => 'stale_security_price',
      'split_basis' => 'split_price_unavailable',
      _ => null,
    };
    if (!const {
          'valued',
          'missing',
          'ambiguous',
          'stale',
          'split_basis',
        }.contains(status) ||
        quantity == '0' ||
        quantity.startsWith('-') ||
        costBasis.startsWith('-') ||
        (marketValue != null &&
            (marketValue == '0' || marketValue.startsWith('-'))) ||
        (valued !=
            (price != null &&
                marketValue != null &&
                unrealizedPnl != null &&
                issue == null)) ||
        (!valued && (marketValue != null || unrealizedPnl != null)) ||
        ((valued || stale || splitBasis) != (price != null)) ||
        (splitBasis && price?.source != 'owner_declared') ||
        (!valued &&
            (issue == null ||
                issue.code != expectedIssueCode ||
                issue.field != _text(json, 'symbol')))) {
      throw const FormatException('Inconsistent holding valuation line');
    }
    return HoldingValuationLine(
      symbol: _text(json, 'symbol'),
      quantity: quantity,
      costBasis: costBasis,
      currency: _currency(json, 'currency'),
      status: status,
      price: price,
      marketValue: marketValue,
      unrealizedPnl: unrealizedPnl,
      issue: issue,
    );
  }

  final String symbol;
  final String quantity;
  final String costBasis;
  final String currency;
  final String status;
  final HoldingValuationPrice? price;
  final String? marketValue;
  final String? unrealizedPnl;
  final HoldingValuationIssue? issue;
}

class HoldingValuationTotal {
  const HoldingValuationTotal({
    required this.currency,
    required this.costBasis,
    required this.marketValue,
    required this.unrealizedPnl,
  });

  factory HoldingValuationTotal.fromJson(Json json) {
    _requireExactKeys(json, const {
      'currency',
      'cost_basis',
      'market_value',
      'unrealized_pnl',
    });
    final costBasis = _decimal(json, 'cost_basis');
    final marketValue = _decimal(json, 'market_value');
    if (costBasis.startsWith('-') ||
        marketValue == '0' ||
        marketValue.startsWith('-')) {
      throw const FormatException('Invalid holding valuation total');
    }
    return HoldingValuationTotal(
      currency: _currency(json, 'currency'),
      costBasis: costBasis,
      marketValue: marketValue,
      unrealizedPnl: _decimal(json, 'unrealized_pnl'),
    );
  }

  final String currency;
  final String costBasis;
  final String marketValue;
  final String unrealizedPnl;
}

class HoldingValuation {
  const HoldingValuation({
    required this.valuationAsOf,
    required this.ledgerRevision,
    required this.ledgerAsOf,
    required this.ledgerRecordedAt,
    required this.status,
    required this.sample,
    required this.totals,
    required this.lines,
    required this.issues,
    this.maxObservationAgeSeconds = 86400,
  });

  factory HoldingValuation.fromJson(Json json) {
    _requireExactKeys(json, const {
      'scope',
      'policy_version',
      'max_observation_age_seconds',
      'valuation_as_of',
      'ledger_revision',
      'ledger_as_of',
      'ledger_recorded_at',
      'status',
      'sample',
      'totals',
      'lines',
      'issues',
    });
    final maxAge = _count(json, 'max_observation_age_seconds');
    final status = _text(json, 'status');
    final sample = _bool(json, 'sample');
    final valuationAsOf = _utcText(json, 'valuation_as_of');
    final ledgerAsOf = _utcText(json, 'ledger_as_of');
    final ledgerRecordedAt = _utcText(json, 'ledger_recorded_at');
    final totalsValue = json['totals'];
    if (totalsValue != null && totalsValue is! List<dynamic>) {
      throw const FormatException('Invalid holding valuation totals');
    }
    final totals = totalsValue == null
        ? const <HoldingValuationTotal>[]
        : _jsonList(
            json,
            'totals',
          ).map(HoldingValuationTotal.fromJson).toList(growable: false);
    final lines = _jsonList(
      json,
      'lines',
    ).map(HoldingValuationLine.fromJson).toList(growable: false);
    final issues = _jsonList(
      json,
      'issues',
    ).map(HoldingValuationIssue.fromJson).toList(growable: false);
    final ownerIssues = issues
        .where((i) => i.code == 'owner_declared_price')
        .toList();
    final owner = ownerIssues.isNotEmpty;
    final calculationIssues = issues
        .where((i) => i.code != 'owner_declared_price')
        .toList();
    if (ownerIssues.length > 1 ||
        ownerIssues.any((i) => i.field != null) ||
        owner != lines.any((l) => l.price?.source == 'owner_declared')) {
      throw const FormatException('Invalid holding price authority');
    }
    final valuationTime = _utcNanoseconds(valuationAsOf);
    if (_text(json, 'scope') != 'holdings_only' ||
        _text(json, 'policy_version') !=
            (owner
                ? 'native_holding_valuation_v2'
                : 'native_holding_valuation_v1') ||
        maxAge != 86400 ||
        !const {
          'empty',
          'unavailable',
          'stale_sample',
          'complete',
        }.contains(status) ||
        !RegExp(r'^rev_[0-9]{10}$').hasMatch(_text(json, 'ledger_revision')) ||
        _utcNanoseconds(ledgerAsOf) > valuationTime ||
        _utcNanoseconds(ledgerRecordedAt) > valuationTime) {
      throw const FormatException('Invalid holding valuation metadata');
    }
    for (final line in lines) {
      final price = line.price;
      if (price == null) continue;
      final observedAt = _utcNanoseconds(price.observedAt);
      final fetchedAt = _utcNanoseconds(price.fetchedAt);
      final recordedAt = _utcNanoseconds(price.recordedAt);
      final age = valuationTime - observedAt;
      final maxAge = BigInt.from(86400) * BigInt.from(1000000000);
      if (price.currency != line.currency ||
          fetchedAt < observedAt ||
          recordedAt < fetchedAt ||
          recordedAt > valuationTime ||
          (line.status == 'valued' && age > maxAge) ||
          (line.status == 'stale' && age <= maxAge) ||
          (price.source == 'owner_declared' &&
              price.state != (age > maxAge ? 'stale' : 'unknown'))) {
        throw const FormatException('Inconsistent holding price timing');
      }
    }
    final unvalued = lines.where((line) => line.status != 'valued').toList();
    final validState = switch (status) {
      'empty' =>
        lines.isEmpty && totalsValue == null && !sample && issues.isEmpty,
      'unavailable' =>
        lines.isNotEmpty &&
            unvalued.isNotEmpty &&
            totalsValue == null &&
            calculationIssues.length == unvalued.length &&
            _sameValuationIssues(unvalued, calculationIssues),
      'complete' =>
        lines.isNotEmpty &&
            unvalued.isEmpty &&
            totals.isNotEmpty &&
            !sample &&
            owner &&
            calculationIssues.isEmpty,
      'stale_sample' =>
        lines.isNotEmpty &&
            unvalued.isEmpty &&
            totals.isNotEmpty &&
            sample &&
            calculationIssues.length == 1 &&
            calculationIssues.single.code == 'sample_data' &&
            calculationIssues.single.field == null,
      _ => false,
    };
    if (!validState ||
        sample != lines.any((line) => line.price?.sample == true)) {
      throw const FormatException('Inconsistent holding valuation state');
    }
    if (status == 'stale_sample' || status == 'complete') {
      var previousCurrency = '';
      final totalCurrencies = <String>{};
      for (final total in totals) {
        if (total.currency.compareTo(previousCurrency) <= 0) {
          throw const FormatException('Valuation totals must be ordered');
        }
        previousCurrency = total.currency;
        totalCurrencies.add(total.currency);
      }
      if (!totalCurrencies.containsAll(lines.map((line) => line.currency)) ||
          !lines
              .map((line) => line.currency)
              .toSet()
              .containsAll(totalCurrencies)) {
        throw const FormatException('Valuation total currencies drifted');
      }
    }
    return HoldingValuation(
      valuationAsOf: valuationAsOf,
      ledgerRevision: _text(json, 'ledger_revision'),
      ledgerAsOf: ledgerAsOf,
      ledgerRecordedAt: ledgerRecordedAt,
      status: status,
      sample: sample,
      totals: totals,
      lines: lines,
      issues: issues,
    );
  }

  final int maxObservationAgeSeconds;
  final String valuationAsOf;
  final String ledgerRevision;
  final String ledgerAsOf;
  final String ledgerRecordedAt;
  final String status;
  final bool sample;
  final List<HoldingValuationTotal> totals;
  final List<HoldingValuationLine> lines;
  final List<HoldingValuationIssue> issues;
  bool get ownerDeclaredPrice =>
      issues.any((i) => i.code == 'owner_declared_price');
}

class CurrentPortfolioValuation {
  const CurrentPortfolioValuation({
    required this.baseCurrency,
    required this.valuationAsOf,
    required this.ledgerRevision,
    required this.ledgerAsOf,
    required this.ledgerRecordedAt,
    required this.status,
    required this.sample,
    required this.cashValue,
    required this.holdingsValue,
    required this.totalValue,
    required this.issues,
    this.maxObservationAgeSeconds = 86400,
  });

  factory CurrentPortfolioValuation.fromJson(Json json) {
    _requireExactKeys(json, const {
      'scope',
      'policy_version',
      'max_observation_age_seconds',
      'base_currency',
      'valuation_as_of',
      'ledger_revision',
      'ledger_as_of',
      'ledger_recorded_at',
      'status',
      'sample',
      'cash_value',
      'holdings_value',
      'total_value',
      'issues',
    });
    final maxAge = _count(json, 'max_observation_age_seconds');
    final baseCurrency = _currency(json, 'base_currency');
    final valuationAsOf = _utcText(json, 'valuation_as_of');
    final ledgerAsOf = _utcText(json, 'ledger_as_of');
    final ledgerRecordedAt = _utcText(json, 'ledger_recorded_at');
    final ledgerRevision = _text(json, 'ledger_revision');
    final status = _text(json, 'status');
    final sample = _bool(json, 'sample');
    final cashValue = _currentValuationMoney(json, 'cash_value');
    final holdingsValue = _currentValuationMoney(json, 'holdings_value');
    final totalValue = _currentValuationMoney(json, 'total_value');
    final issues = _jsonList(
      json,
      'issues',
    ).map(HoldingValuationIssue.fromJson).toList(growable: false);
    final ownerIssues = issues
        .where((i) => i.code == 'owner_declared_fx')
        .toList();
    if (ownerIssues.length > 1 || ownerIssues.any((i) => i.field != null)) {
      throw const FormatException('Invalid declared FX notice');
    }
    final owner = ownerIssues.isNotEmpty;
    final priceIssues = issues
        .where((i) => i.code == 'owner_declared_price')
        .toList();
    if (priceIssues.length > 1 || priceIssues.any((i) => i.field != null)) {
      throw const FormatException('Invalid owner price notice');
    }
    final ownerPrice = priceIssues.isNotEmpty;
    final calculationIssues = issues
        .where(
          (i) =>
              i.code != 'owner_declared_fx' && i.code != 'owner_declared_price',
        )
        .toList();
    if (_text(json, 'scope') != 'whole_portfolio' ||
        _text(json, 'policy_version') !=
            (ownerPrice
                ? 'current_base_portfolio_valuation_v3'
                : owner
                ? 'current_base_portfolio_valuation_v2'
                : 'current_base_portfolio_valuation_v1') ||
        maxAge != 86400 ||
        baseCurrency != 'KRW' ||
        !RegExp(r'^rev_[0-9]{10}$').hasMatch(ledgerRevision) ||
        _utcNanoseconds(ledgerAsOf) > _utcNanoseconds(valuationAsOf) ||
        _utcNanoseconds(ledgerRecordedAt) > _utcNanoseconds(valuationAsOf) ||
        !const {
          'empty',
          'complete',
          'stale_sample',
          'unavailable',
        }.contains(status)) {
      throw const FormatException('Invalid current valuation metadata');
    }
    final values = [cashValue, holdingsValue, totalValue];
    final allValues = values.every((value) => value != null);
    final noValues = values.every((value) => value == null);
    if (!allValues && !noValues) {
      throw const FormatException('Current valuation must be all-or-none');
    }
    if (allValues) {
      if (values.any((value) => value!.currency != baseCurrency) ||
          holdingsValue!.amount.startsWith('-') ||
          !_decimalSumEquals(
            cashValue!.amount,
            holdingsValue.amount,
            totalValue!.amount,
          )) {
        throw const FormatException('Invalid current valuation aggregate');
      }
    }
    final issueKeys = <String>{};
    final validCoverageIssues = calculationIssues.every((issue) {
      final key = '${issue.code}:${issue.field}';
      return const {
            'missing_direct_fx',
            'stale_direct_fx',
            'missing_security_price',
            'ambiguous_security_price_venue',
            'stale_security_price',
            'split_price_unavailable',
          }.contains(issue.code) &&
          (issue.code != 'split_price_unavailable' || ownerPrice) &&
          issue.field != null &&
          issueKeys.add(key);
    });
    final validState = switch (status) {
      'empty' => noValues && !sample && issues.isEmpty,
      'complete' =>
        allValues &&
            (ownerPrice
                ? holdingsValue!.amount != '0'
                : holdingsValue!.amount == '0') &&
            !sample &&
            calculationIssues.isEmpty,
      'stale_sample' =>
        allValues &&
            sample &&
            calculationIssues.length == 1 &&
            calculationIssues.single.code == 'sample_data' &&
            calculationIssues.single.field == null,
      'unavailable' =>
        noValues && calculationIssues.isNotEmpty && validCoverageIssues,
      _ => false,
    };
    if (!validState ||
        (ownerPrice && allValues && holdingsValue!.amount == '0')) {
      throw const FormatException('Inconsistent current valuation state');
    }
    return CurrentPortfolioValuation(
      baseCurrency: baseCurrency,
      valuationAsOf: valuationAsOf,
      ledgerRevision: ledgerRevision,
      ledgerAsOf: ledgerAsOf,
      ledgerRecordedAt: ledgerRecordedAt,
      status: status,
      sample: sample,
      cashValue: cashValue,
      holdingsValue: holdingsValue,
      totalValue: totalValue,
      issues: issues,
    );
  }

  final int maxObservationAgeSeconds;
  final String baseCurrency;
  final String valuationAsOf;
  final String ledgerRevision;
  final String ledgerAsOf;
  final String ledgerRecordedAt;
  final String status;
  final bool sample;
  final Money? cashValue;
  final Money? holdingsValue;
  final Money? totalValue;
  final List<HoldingValuationIssue> issues;
  bool get ownerDeclaredFx => issues.any((i) => i.code == 'owner_declared_fx');
  bool get ownerDeclaredPrice =>
      issues.any((i) => i.code == 'owner_declared_price');
  int get missingInputCount => issues
      .where(
        (i) =>
            i.code != 'owner_declared_fx' && i.code != 'owner_declared_price',
      )
      .length;
}

Money? _currentValuationMoney(Json json, String key) {
  final value = json[key];
  if (value == null) return null;
  if (value is! Json) {
    throw const FormatException('Invalid current valuation money');
  }
  _requireExactKeys(value, const {'currency', 'amount'});
  return Money(_currency(value, 'currency'), _decimal(value, 'amount'));
}

bool _decimalSumEquals(String left, String right, String total) {
  final scale = [
    left,
    right,
    total,
  ].map(_decimalScale).reduce((a, b) => a > b ? a : b);
  return _scaledDecimal(left, scale) + _scaledDecimal(right, scale) ==
      _scaledDecimal(total, scale);
}

int _decimalScale(String value) {
  final dot = value.indexOf('.');
  return dot < 0 ? 0 : value.length - dot - 1;
}

BigInt _scaledDecimal(String value, int scale) {
  final ownScale = _decimalScale(value);
  final coefficient = BigInt.parse(value.replaceFirst('.', ''));
  return coefficient * BigInt.from(10).pow(scale - ownScale);
}

bool _sameValuationIssues(
  List<HoldingValuationLine> lines,
  List<HoldingValuationIssue> issues,
) {
  for (var index = 0; index < lines.length; index += 1) {
    final lineIssue = lines[index].issue!;
    final issue = issues[index];
    if (lineIssue.code != issue.code || lineIssue.field != issue.field) {
      return false;
    }
  }
  return true;
}

BigInt _utcNanoseconds(String value) {
  final dot = value.indexOf('.');
  final whole = dot < 0 ? value : '${value.substring(0, dot)}Z';
  final seconds = DateTime.parse(whole).microsecondsSinceEpoch ~/ 1000000;
  final fraction = dot < 0
      ? BigInt.zero
      : BigInt.parse(
          value.substring(dot + 1, value.length - 1).padRight(9, '0'),
        );
  return BigInt.from(seconds) * BigInt.from(1000000000) + fraction;
}

class PortfolioSnapshot {
  const PortfolioSnapshot({
    required this.ledgerRevision,
    required this.costBasisPolicy,
    required this.recordedAt,
    required this.cash,
    required this.holdings,
    required this.realizedPnl,
    this.eventCount = 0,
    this.receiptCount = 0,
  });
  factory PortfolioSnapshot.fromJson(Json json) {
    if (_bool(json, 'live_enabled') ||
        _text(json, 'valuation_status') != 'unavailable') {
      throw const FormatException('Live valuation is not supported');
    }
    if (_text(json, 'cost_basis_policy') !=
        'fifo_utc_nanosecond_exact_else_half_even_residual_8_v2') {
      throw const FormatException('Unsupported cost basis policy');
    }
    final provenance = json['provenance'];
    if (provenance is! Json) {
      throw const FormatException('Missing provenance');
    }
    _requireExactKeys(provenance, {'event_ids', 'receipt_ids'});
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
      eventCount: _nonEmptyStringCount(provenance, 'event_ids'),
      receiptCount: _nonEmptyStringCount(provenance, 'receipt_ids'),
    );
  }

  final String ledgerRevision;
  final String costBasisPolicy;
  final DateTime recordedAt;
  final List<Money> cash;
  final List<Holding> holdings;
  final List<Money> realizedPnl;
  final int eventCount;
  final int receiptCount;
}

List<Money> _moneyList(Json value, String key) =>
    _jsonList(value, key).map(Money.fromJson).toList(growable: false);

int _nonEmptyStringCount(Json value, String key) {
  final result = value[key];
  if (result is! List<dynamic> ||
      result.any((item) => item is! String || item.isEmpty)) {
    throw const FormatException('Invalid provenance IDs');
  }
  return result.length;
}

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
        !RegExp(r'^rev_[0-9]{10}$').hasMatch(ledgerRevision) ||
        (nextCursor != null && nextCursor.length > 1024)) {
      throw const FormatException('Invalid ledger activity metadata');
    }
    final events = _jsonList(
      json,
      'events',
    ).map(LedgerActivity.fromJson).toList(growable: false);
    if (events.length > 50 || (nextCursor != null && events.length != 50)) {
      throw const FormatException('Invalid ledger activity page size');
    }
    for (var index = 1; index < events.length; index += 1) {
      if (_utcNanoseconds(
            events[index].occurredAt,
          ).compareTo(_utcNanoseconds(events[index - 1].occurredAt)) >
          0) {
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

  void requireContinuationOf({
    required LedgerActivityPage initial,
    required String upperBoundOccurredAt,
    required String requestedCursor,
    String? lowerBoundOccurredAt,
    Iterable<String> traversedCursors = const [],
  }) {
    final continuationCursor = nextCursor;
    if (events.isEmpty ||
        ledgerRevision != initial.ledgerRevision ||
        recordedAt != initial.recordedAt ||
        (continuationCursor != null &&
            (continuationCursor == requestedCursor ||
                traversedCursors.contains(continuationCursor))) ||
        _utcNanoseconds(
              events.first.occurredAt,
            ).compareTo(_utcNanoseconds(upperBoundOccurredAt)) >
            0 ||
        (lowerBoundOccurredAt != null &&
            _utcNanoseconds(
                  events.last.occurredAt,
                ).compareTo(_utcNanoseconds(lowerBoundOccurredAt)) <
                0)) {
      throw const FormatException('Invalid ledger activity continuation');
    }
  }
}

class InstrumentLedger {
  const InstrumentLedger({
    required this.instrumentId,
    required this.symbol,
    required this.currency,
    required this.ledgerRevision,
    required this.ledgerAsOf,
    required this.ledgerRecordedAt,
    required this.quantity,
    required this.costBasis,
    required this.averageCost,
    required this.hasMore,
    required this.trades,
  });

  factory InstrumentLedger.fromJson(Json json) {
    _requireExactKeys(json, const {
      'instrument_id',
      'symbol',
      'currency',
      'source',
      'broker_freshness',
      'ledger_revision',
      'ledger_as_of',
      'ledger_recorded_at',
      'cost_basis_policy',
      'average_cost_policy',
      'quantity',
      'cost_basis',
      'average_cost',
      'trade_limit',
      'has_more',
      'trades',
    });
    final instrumentId = _text(json, 'instrument_id');
    final symbol = _text(json, 'symbol');
    final currency = _currency(json, 'currency');
    final revision = _text(json, 'ledger_revision');
    final asOf = _utcText(json, 'ledger_as_of');
    final recordedAt = _utcText(json, 'ledger_recorded_at');
    final quantity = _decimal(json, 'quantity');
    final cost = _decimal(json, 'cost_basis');
    final average = _nullableDecimal(json, 'average_cost');
    final hasMore = _bool(json, 'has_more');
    final trades = _jsonList(
      json,
      'trades',
    ).map(LedgerActivity.fromJson).toList(growable: false);
    if (_text(json, 'source') != 'local_ledger' ||
        _text(json, 'broker_freshness') != 'unverified' ||
        _text(json, 'cost_basis_policy') !=
            'fifo_utc_nanosecond_exact_else_half_even_residual_8_v2' ||
        _text(json, 'average_cost_policy') !=
            'remaining_fifo_cost_per_unit_half_even_8_v1' ||
        !RegExp(r'^rev_[0-9]{10}$').hasMatch(revision) ||
        quantity.startsWith('-') ||
        cost.startsWith('-') ||
        (quantity == '0'
            ? (cost != '0' || average != null)
            : average == null) ||
        (average != null &&
            (average.startsWith('-') || _decimalScale(average) > 8)) ||
        _count(json, 'trade_limit') != 50 ||
        trades.isEmpty ||
        trades.length > 50 ||
        (hasMore && trades.length != 50)) {
      throw const FormatException('Invalid instrument ledger');
    }
    for (var index = 0; index < trades.length; index++) {
      final trade = trades[index];
      if (!const {'BUY', 'SELL'}.contains(trade.type) ||
          trade.currency != currency ||
          _utcNanoseconds(trade.occurredAt) > _utcNanoseconds(asOf) ||
          (index > 0 &&
              _utcNanoseconds(trade.occurredAt) >
                  _utcNanoseconds(trades[index - 1].occurredAt))) {
        throw const FormatException('Invalid instrument trade scope');
      }
    }
    return InstrumentLedger(
      instrumentId: instrumentId,
      symbol: symbol,
      currency: currency,
      ledgerRevision: revision,
      ledgerAsOf: asOf,
      ledgerRecordedAt: recordedAt,
      quantity: quantity,
      costBasis: cost,
      averageCost: average,
      hasMore: hasMore,
      trades: trades,
    );
  }

  final String instrumentId, symbol, currency;
  final String ledgerRevision, ledgerAsOf, ledgerRecordedAt;
  final String quantity, costBasis;
  final String? averageCost;
  final bool hasMore;
  final List<LedgerActivity> trades;
}

bool _tradeCashMatches(
  String type,
  String quantity,
  String price,
  String fee,
  String amount,
) => amount == tradeCashAmount(type, quantity, price, fee);

String tradeCashAmount(String type, String quantity, String price, String fee) {
  if (!const {'BUY', 'SELL'}.contains(type) ||
      [quantity, price, fee].any(
        (value) =>
            !_canonicalDecimalPattern.hasMatch(value) || value.startsWith('-'),
      ) ||
      quantity == '0' ||
      price == '0') {
    throw const FormatException('Invalid trade cash input');
  }
  final productScale = _decimalScale(quantity) + _decimalScale(price);
  final scale = [
    productScale,
    _decimalScale(fee),
  ].reduce((a, b) => a > b ? a : b);
  final gross =
      _scaledDecimal(quantity, _decimalScale(quantity)) *
      _scaledDecimal(price, _decimalScale(price)) *
      BigInt.from(10).pow(scale - productScale);
  final expected =
      (type == 'BUY' ? -gross : gross) - _scaledDecimal(fee, scale);
  if (expected == BigInt.zero) return '0';
  final digits = expected.abs().toString().padLeft(scale + 1, '0');
  var value = digits;
  if (scale > 0) {
    final whole = digits.substring(0, digits.length - scale);
    final fraction = digits
        .substring(digits.length - scale)
        .replaceFirst(RegExp(r'0+$'), '');
    value = fraction.isEmpty ? whole : '$whole.$fraction';
  }
  return expected.isNegative ? '-$value' : value;
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
          noCounter;
      if (valid) {
        valid = _tradeCashMatches(type, quantity, price, fee, amount);
      }
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

class TwrCheckpoint {
  const TwrCheckpoint._(this.at, this.kind, this.cumulativeRate);
  final String at, kind, cumulativeRate;
  BigInt get atNanoseconds => _utcNanoseconds(at);
}

class PortfolioTwrCheckpoints {
  PortfolioTwrCheckpoints._(this.report, List<TwrCheckpoint> points)
    : points = List.unmodifiable(points);

  factory PortfolioTwrCheckpoints.fromJson(Json json) {
    _requireExactKeys(json, {'policy_version', 'report', 'points'});
    if (json['policy_version'] != 'twr_cash_flow_checkpoints.v1' ||
        json['report'] is! Json) {
      throw const FormatException('Unsupported TWR checkpoints');
    }
    final report = PortfolioTwr.fromJson(json['report'] as Json);
    final raw = _jsonList(json, 'points');
    if (report.status != 'calculated') {
      if (raw.isNotEmpty) {
        throw const FormatException(
          'Uncalculated report has partial checkpoints',
        );
      }
      return PortfolioTwrCheckpoints._(report, []);
    }
    final count = report.boundaryCount;
    if (count == null || count < 1 || count > 1000 || raw.length != count + 1) {
      throw const FormatException(
        'Checkpoint count differs from TWR boundaries',
      );
    }
    final points = <TwrCheckpoint>[];
    BigInt? previous;
    for (var index = 0; index < raw.length; index++) {
      final value = raw[index];
      _requireExactKeys(value, {'at', 'kind', 'cumulative_rate'});
      final at = _utcText(value, 'at');
      final time = _utcNanoseconds(at);
      final rate = _decimal(value, 'cumulative_rate');
      final terminal = index == count;
      if (value['kind'] != (terminal ? 'valuation' : 'external_flow') ||
          rate.length > 2500 ||
          _decimalScale(rate) > 12 ||
          (rate.startsWith('-') &&
              _comparePositiveDecimal(rate.substring(1), '1') > 0) ||
          time < _utcNanoseconds(report.periodStart!) ||
          time > _utcNanoseconds(report.periodEnd) ||
          (previous != null &&
              (terminal ? time < previous : time <= previous)) ||
          (index == 0 && (at != report.periodStart || rate != '0')) ||
          (terminal &&
              (at != report.periodEnd || rate != report.cumulativeRate))) {
        throw const FormatException(
          'Checkpoint phase, range or rate contradicts TWR report',
        );
      }
      points.add(
        TwrCheckpoint._(at, terminal ? 'valuation' : 'external_flow', rate),
      );
      previous = time;
    }
    return PortfolioTwrCheckpoints._(report, points);
  }

  final PortfolioTwr report;
  final List<TwrCheckpoint> points;
}

class PortfolioTwr {
  const PortfolioTwr._({
    required this.valuation,
    required this.status,
    required this.sample,
    required this.periodStart,
    required this.periodEnd,
    required this.externalEventCount,
    required this.boundaryCount,
    required this.cumulativeRate,
    required this.netContributions,
    required this.issues,
  });

  factory PortfolioTwr.fromJson(Json json) {
    _requireExactKeys(json, const {
      'policy_version',
      'scope',
      'valuation',
      'status',
      'sample',
      'period_start',
      'period_end',
      'external_event_count',
      'boundary_count',
      'cumulative_rate',
      'net_contributions',
      'issues',
    });
    if (!const {
          'restated_external_first_exact_timestamp_twr_v1',
          'restated_external_first_exact_timestamp_twr_v2',
          'restated_external_first_exact_timestamp_twr_v3',
        }.contains(_text(json, 'policy_version')) ||
        _text(json, 'scope') != 'whole_portfolio_since_inception' ||
        json['valuation'] is! Json) {
      throw const FormatException('Unsupported TWR report');
    }
    final valuation = CurrentPortfolioValuation.fromJson(
      json['valuation'] as Json,
    );
    final status = _text(json, 'status');
    final sample = _bool(json, 'sample');
    final start = json['period_start'] == null
        ? null
        : _utcText(json, 'period_start');
    final end = _utcText(json, 'period_end');
    final count = json['external_event_count'] == null
        ? null
        : _count(json, 'external_event_count');
    final boundaries = json['boundary_count'] == null
        ? null
        : _count(json, 'boundary_count');
    final rate = _nullableDecimal(json, 'cumulative_rate');
    if (rate != null &&
        (rate.length > 128 ||
            _decimalScale(rate) > 12 ||
            (rate.startsWith('-') &&
                _comparePositiveDecimal(rate.substring(1), '1') > 0))) {
      throw const FormatException('Invalid TWR rate');
    }
    Money? net;
    if (json['net_contributions'] != null) {
      if (json['net_contributions'] is! Json) {
        throw const FormatException('Invalid TWR cash');
      }
      final money = json['net_contributions'] as Json;
      _requireExactKeys(money, const {'currency', 'amount'});
      net = Money.fromJson(money);
      if (net.currency != 'KRW') {
        throw const FormatException('Invalid TWR currency');
      }
    }
    final issues = _jsonList(
      json,
      'issues',
    ).map(HoldingValuationIssue.fromJson).toList(growable: false);
    const codes = {
      'sample_data',
      'owner_declared_fx',
      'owner_declared_price',
      'valuation_unavailable',
      'no_external_cash_flows',
      'cash_flow_limit',
      'numerical_limit',
      'capital_path_unavailable',
      'ambiguous_split_boundary',
      'split_price_unavailable',
      'missing_security_price',
      'stale_security_price',
      'ambiguous_security_price_venue',
      'missing_historical_fx',
      'stale_historical_fx',
    };
    final seen = <String>{};
    if (issues.any(
      (issue) =>
          !codes.contains(issue.code) ||
          !seen.add(issue.code) ||
          issue.field != null,
    )) {
      throw const FormatException('Invalid TWR issue');
    }
    final missingValuation = seen.contains('valuation_unavailable');
    final owner = seen.contains('owner_declared_fx');
    final ownerPrice = issues.any((i) => i.code == 'owner_declared_price');
    if (_text(json, 'policy_version') !=
            (ownerPrice
                ? 'restated_external_first_exact_timestamp_twr_v3'
                : owner
                ? 'restated_external_first_exact_timestamp_twr_v2'
                : 'restated_external_first_exact_timestamp_twr_v1') ||
        (valuation.ownerDeclaredFx && !owner) ||
        (valuation.ownerDeclaredPrice && !ownerPrice)) {
      throw const FormatException('Inconsistent TWR authority');
    }
    final reasons = seen.difference(const {
      'sample_data',
      'owner_declared_fx',
      'owner_declared_price',
    });
    final reason = reasons.isEmpty ? null : reasons.first;
    final unavailableInputs = switch (reason) {
      'valuation_unavailable' => count == null && boundaries == null,
      'no_external_cash_flows' => count == 0 && boundaries == 0,
      'cash_flow_limit' => boundaries != null && boundaries > 1000,
      null => false,
      _ =>
        count != null &&
            count > 0 &&
            boundaries != null &&
            boundaries >= 1 &&
            boundaries <= 1000,
    };
    final valid = switch (status) {
      'calculated' =>
        rate != null &&
            net != null &&
            count != null &&
            count > 0 &&
            boundaries != null &&
            boundaries <= 1000 &&
            valuation.totalValue != null &&
            issues.every(
              (issue) => const {
                'sample_data',
                'owner_declared_fx',
                'owner_declared_price',
              }.contains(issue.code),
            ),
      'empty' =>
        rate == null &&
            net == null &&
            count == 0 &&
            boundaries == 0 &&
            start == null &&
            !sample &&
            issues.isEmpty &&
            valuation.status == 'empty',
      'unavailable' =>
        rate == null &&
            net == null &&
            reasons.length == 1 &&
            unavailableInputs &&
            valuation.status != 'empty',
      _ => false,
    };
    if (!valid ||
        sample != seen.contains('sample_data') ||
        (valuation.sample && !sample) ||
        end != valuation.valuationAsOf ||
        (start != null && _utcNanoseconds(start) > _utcNanoseconds(end)) ||
        (missingValuation &&
            (valuation.totalValue != null ||
                count != null ||
                boundaries != null ||
                start != null)) ||
        (!missingValuation &&
            status != 'empty' &&
            (valuation.totalValue == null ||
                count == null ||
                boundaries == null)) ||
        ((count == null) != (boundaries == null)) ||
        (count == null && start != null) ||
        (count == 0 && (start != null || boundaries != 0)) ||
        (count != null &&
            count > 0 &&
            (start == null ||
                boundaries == null ||
                boundaries < 1 ||
                boundaries > count)) ||
        (boundaries != null &&
            boundaries > 1000 &&
            !seen.contains('cash_flow_limit'))) {
      throw const FormatException('Inconsistent TWR report');
    }
    return PortfolioTwr._(
      valuation: valuation,
      status: status,
      sample: sample,
      periodStart: start,
      periodEnd: end,
      externalEventCount: count,
      boundaryCount: boundaries,
      cumulativeRate: rate,
      netContributions: net,
      issues: List.unmodifiable(issues),
    );
  }
  final CurrentPortfolioValuation valuation;
  final String status;
  final bool sample;
  final String? periodStart;
  final String periodEnd;
  final int? externalEventCount;
  final int? boundaryCount;
  final String? cumulativeRate;
  final Money? netContributions;
  final List<HoldingValuationIssue> issues;
  bool get ownerDeclaredFx => issues.any((i) => i.code == 'owner_declared_fx');
  bool get ownerDeclaredPrice =>
      issues.any((i) => i.code == 'owner_declared_price');
}

String _benchmarkText(
  Json json,
  String key,
  int maxBytes, {
  bool identity = false,
  bool trimmed = false,
}) {
  final text = _text(json, key);
  final controls = RegExp(r'[\u0000-\u001f\u007f-\u009f]');
  final spaces = RegExp(
    r'[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]',
  );
  if (text.runes.any((rune) => rune >= 0xd800 && rune <= 0xdfff) ||
      utf8.encode(text).length > maxBytes ||
      controls.hasMatch(text) ||
      (identity && spaces.hasMatch(text)) ||
      (trimmed &&
          (spaces.hasMatch(text.substring(0, 1)) ||
              spaces.hasMatch(text.substring(text.length - 1))))) {
    throw const FormatException('Invalid benchmark text');
  }
  return text;
}

class BenchmarkSeriesSummary {
  const BenchmarkSeriesSummary._({
    required this.id,
    required this.label,
    required this.currency,
    required this.returnBasis,
    required this.sourceReference,
    required this.sourceVersion,
    required this.sample,
    required this.pointCount,
    required this.firstObservedAt,
    required this.lastObservedAt,
    required this.fetchedAt,
    required this.recordedAt,
  });
  factory BenchmarkSeriesSummary.fromJson(Json json) {
    _requireExactKeys(json, const {
      'storage_check',
      'authority',
      'benchmark_id',
      'label',
      'currency',
      'return_basis',
      'source_reference',
      'source_version',
      'sample',
      'data_sha256',
      'point_count',
      'first_observed_at',
      'last_observed_at',
      'fetched_at',
      'recorded_at',
    });
    final id = _benchmarkText(json, 'benchmark_id', 128, identity: true);
    final label = _benchmarkText(json, 'label', 160, trimmed: true);
    final currency = _text(json, 'currency');
    final basis = _text(json, 'return_basis');
    final reference = _benchmarkText(
      json,
      'source_reference',
      512,
      identity: true,
    );
    final uri = Uri.tryParse(reference);
    final version = _benchmarkText(json, 'source_version', 128, identity: true);
    final sha = _text(json, 'data_sha256');
    final count = _count(json, 'point_count');
    final first = _utcText(json, 'first_observed_at');
    final last = _utcText(json, 'last_observed_at');
    final fetched = _utcText(json, 'fetched_at');
    final recorded = _utcText(json, 'recorded_at');
    final firstTime = _utcNanoseconds(first);
    final lastTime = _utcNanoseconds(last);
    if (json['storage_check'] != 'verified' ||
        json['authority'] != 'owner_declared' ||
        !const {'KRW', 'USD'}.contains(currency) ||
        !const {
          'price_index',
          'gross_total_return',
          'net_total_return',
        }.contains(basis) ||
        !reference.startsWith('https://') ||
        uri == null ||
        uri.scheme != 'https' ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasQuery ||
        uri.hasFragment ||
        sha.length != 64 ||
        !RegExp(r'^[0-9a-f]{64}$').hasMatch(sha) ||
        count < 1 ||
        count > 10000 ||
        firstTime > lastTime ||
        ((count == 1) != (firstTime == lastTime)) ||
        lastTime > _utcNanoseconds(fetched) ||
        _utcNanoseconds(fetched) > _utcNanoseconds(recorded)) {
      throw const FormatException('Invalid benchmark series summary');
    }
    return BenchmarkSeriesSummary._(
      id: id,
      label: label,
      currency: currency,
      returnBasis: basis,
      sourceReference: reference,
      sourceVersion: version,
      sample: _bool(json, 'sample'),
      pointCount: count,
      firstObservedAt: first,
      lastObservedAt: last,
      fetchedAt: fetched,
      recordedAt: recorded,
    );
  }
  final String id, label, currency, returnBasis, sourceReference, sourceVersion;
  final bool sample;
  final int pointCount;
  final String firstObservedAt, lastObservedAt, fetchedAt, recordedAt;
}

class BenchmarkRate {
  const BenchmarkRate._(this.status, this.rate, this.issue);
  factory BenchmarkRate.fromJson(Json json) {
    _requireExactKeys(json, const {'status', 'rate', 'issue'});
    final status = _text(json, 'status');
    final rate = _nullableDecimal(json, 'rate');
    final issue = _nullableText(json, 'issue');
    const codes = {
      'portfolio_unavailable',
      'missing_benchmark_point',
      'stale_benchmark_point',
      'zero_benchmark_start',
      'missing_historical_fx',
      'stale_historical_fx',
      'numerical_limit',
    };
    final calculated = status == 'calculated' && rate != null && issue == null;
    final unavailable =
        status == 'unavailable' && rate == null && codes.contains(issue);
    if ((!calculated && !unavailable) ||
        (rate != null &&
            (rate.length > 128 ||
                _decimalScale(rate) > 12 ||
                (rate.startsWith('-') &&
                    _comparePositiveDecimal(rate.substring(1), '1') > 0)))) {
      throw const FormatException('Invalid benchmark rate');
    }
    return BenchmarkRate._(status, rate, issue);
  }
  final String status;
  final String? rate, issue;
}

typedef BenchmarkObservation = ({String observedAt, String value});
typedef BenchmarkFxObservation = ({
  String observedAt,
  String value,
  String? sourceVersion,
  bool sample,
});

BenchmarkFxObservation? _benchmarkFxObservation(dynamic raw) {
  if (raw == null) return null;
  if (raw is! Json) {
    throw const FormatException('Invalid FX observation');
  }
  final owner = raw.containsKey('source');
  _requireExactKeys(
    raw,
    owner
        ? {'observed_at', 'rate', 'source', 'source_version', 'sample'}
        : {'observed_at', 'rate'},
  );
  if (owner && _text(raw, 'source') != 'owner_declared') {
    throw const FormatException('Invalid FX source');
  }
  final point = _benchmarkObservation({
    'observed_at': raw['observed_at'],
    'rate': raw['rate'],
  }, 'rate')!;
  return (
    observedAt: point.observedAt,
    value: point.value,
    sourceVersion: owner
        ? _benchmarkText(raw, 'source_version', 128, identity: true)
        : null,
    sample: owner ? _bool(raw, 'sample') : true,
  );
}

BenchmarkObservation? _benchmarkObservation(dynamic raw, String valueKey) {
  if (raw == null) return null;
  if (raw is! Json) {
    throw const FormatException('Invalid benchmark observation');
  }
  _requireExactKeys(raw, {'observed_at', valueKey});
  final value = _decimal(raw, valueKey);
  if (value.length > 64 ||
      value.startsWith('-') ||
      (valueKey == 'rate' && value == '0')) {
    throw const FormatException('Invalid benchmark observation value');
  }
  return (observedAt: _utcText(raw, 'observed_at'), value: value);
}

class PortfolioBenchmark {
  const PortfolioBenchmark._({
    required this.series,
    required this.sample,
    required this.start,
    required this.end,
    required this.startFx,
    required this.endFx,
    required this.native,
    required this.krw,
    required this.differencePercentagePoints,
  });
  factory PortfolioBenchmark.fromJson(Json json, PortfolioTwr portfolio) {
    _requireExactKeys(json, const {
      'series',
      'sample',
      'start',
      'end',
      'start_fx',
      'end_fx',
      'native',
      'krw',
      'difference_percentage_points',
    });
    if (json['series'] is! Json ||
        json['native'] is! Json ||
        json['krw'] is! Json) {
      throw const FormatException('Invalid benchmark comparison');
    }
    final series = BenchmarkSeriesSummary.fromJson(json['series'] as Json);
    final sample = _bool(json, 'sample');
    final start = _benchmarkObservation(json['start'], 'level');
    final end = _benchmarkObservation(json['end'], 'level');
    final startFx = _benchmarkFxObservation(json['start_fx']);
    final endFx = _benchmarkFxObservation(json['end_fx']);
    final native = BenchmarkRate.fromJson(json['native'] as Json);
    final krw = BenchmarkRate.fromJson(json['krw'] as Json);
    final difference = _nullableDecimal(json, 'difference_percentage_points');
    final endAt = _utcNanoseconds(portfolio.periodEnd);
    final startAt = portfolio.periodStart == null
        ? null
        : _utcNanoseconds(portfolio.periodStart!);
    final maxAge = BigInt.from(86400) * BigInt.from(1000000000);
    bool stale(String? observedAt, BigInt at) =>
        observedAt != null && at - _utcNanoseconds(observedAt) > maxAge;
    bool equalRates() =>
        native.status == krw.status &&
        native.rate == krw.rate &&
        native.issue == krw.issue;
    if (_utcNanoseconds(series.recordedAt) > endAt ||
        sample !=
            (series.sample ||
                (startFx?.sample ?? false) ||
                (endFx?.sample ?? false)) ||
        (startFx != null &&
            endFx != null &&
            (startFx.sourceVersion != endFx.sourceVersion ||
                startFx.sample != endFx.sample))) {
      throw const FormatException('Inconsistent benchmark provenance');
    }
    for (final (observedAt, at) in [
      (start?.observedAt, startAt),
      (end?.observedAt, endAt),
      (startFx?.observedAt, startAt),
      (endFx?.observedAt, endAt),
    ]) {
      if (observedAt != null &&
          (at == null || _utcNanoseconds(observedAt) > at)) {
        throw const FormatException('Future benchmark observation');
      }
    }
    for (final mark in [start, end]) {
      if (mark != null &&
          (_utcNanoseconds(mark.observedAt) <
                  _utcNanoseconds(series.firstObservedAt) ||
              _utcNanoseconds(mark.observedAt) >
                  _utcNanoseconds(series.lastObservedAt))) {
        throw const FormatException(
          'Benchmark observation outside its version',
        );
      }
    }
    if (portfolio.status != 'calculated') {
      if (native.issue != 'portfolio_unavailable' ||
          !equalRates() ||
          start != null ||
          end != null ||
          startFx != null ||
          endFx != null) {
        throw const FormatException('Comparison without calculated TWR');
      }
    } else {
      if (startAt == null ||
          end == null ||
          end.observedAt != series.lastObservedAt) {
        throw const FormatException('Missing comparison period endpoint');
      }
      final pointIssue = start == null
          ? 'missing_benchmark_point'
          : stale(start.observedAt, startAt) || stale(end.observedAt, endAt)
          ? 'stale_benchmark_point'
          : start.value == '0'
          ? 'zero_benchmark_start'
          : null;
      if (pointIssue != null) {
        if (native.issue != pointIssue ||
            !equalRates() ||
            startFx != null ||
            endFx != null) {
          throw const FormatException('Inconsistent benchmark point failure');
        }
      } else {
        if (native.issue != null && native.issue != 'numerical_limit') {
          throw const FormatException('Invalid native benchmark failure');
        }
        if (series.currency == 'KRW') {
          if (!equalRates() || startFx != null || endFx != null) {
            throw const FormatException('KRW index was converted twice');
          }
        } else {
          final fxIssue = startFx == null || endFx == null
              ? 'missing_historical_fx'
              : stale(startFx.observedAt, startAt) ||
                    stale(endFx.observedAt, endAt)
              ? 'stale_historical_fx'
              : null;
          if ((fxIssue != null && krw.issue != fxIssue) ||
              (fxIssue == null &&
                  krw.issue != null &&
                  krw.issue != 'numerical_limit')) {
            throw const FormatException('Inconsistent benchmark FX failure');
          }
        }
      }
    }
    if (krw.rate == null) {
      if (difference != null) {
        throw const FormatException('Difference without KRW benchmark');
      }
    } else if (difference == null ||
        portfolio.cumulativeRate == null ||
        difference.length > 132 ||
        _decimalScale(difference) > 10 ||
        _scaledDecimal(difference, 10) !=
            _scaledDecimal(portfolio.cumulativeRate!, 12) -
                _scaledDecimal(krw.rate!, 12)) {
      throw const FormatException('Invalid percentage-point difference');
    }
    return PortfolioBenchmark._(
      series: series,
      sample: sample,
      start: start,
      end: end,
      startFx: startFx,
      endFx: endFx,
      native: native,
      krw: krw,
      differencePercentagePoints: difference,
    );
  }
  final BenchmarkSeriesSummary series;
  final bool sample;
  final BenchmarkObservation? start, end;
  final BenchmarkFxObservation? startFx, endFx;
  final BenchmarkRate native, krw;
  final String? differencePercentagePoints;
  bool get ownerDeclaredFx =>
      startFx?.sourceVersion != null || endFx?.sourceVersion != null;
}

class PortfolioBenchmarks {
  const PortfolioBenchmarks._(this.portfolio, this.benchmarks, this.issues);
  factory PortfolioBenchmarks.fromJson(Json json) {
    _requireExactKeys(json, const {
      'policy_version',
      'max_observation_age_seconds',
      'portfolio',
      'benchmarks',
      'issues',
    });
    if (!const {
          'retrospective_latest_index_24h_native_and_krw_v1',
          'retrospective_latest_index_24h_native_and_krw_v2',
          'retrospective_latest_index_24h_native_and_krw_v3',
        }.contains(json['policy_version']) ||
        _count(json, 'max_observation_age_seconds') != 86400 ||
        json['portfolio'] is! Json) {
      throw const FormatException('Unsupported portfolio benchmark policy');
    }
    final portfolio = PortfolioTwr.fromJson(json['portfolio'] as Json);
    final rawRows = _jsonList(json, 'benchmarks');
    final rawIssues = _jsonList(json, 'issues');
    if (rawRows.length > 32 || rawIssues.length > 1) {
      throw const FormatException('Benchmark response exceeds its bounds');
    }
    final rows = rawRows
        .map((row) => PortfolioBenchmark.fromJson(row, portfolio))
        .toList(growable: false);
    final issues = rawIssues
        .map((issue) {
          _requireExactKeys(issue, const {'code', 'message'});
          return HoldingValuationIssue.fromJson(issue);
        })
        .toList(growable: false);
    final ids = <String>{};
    final owner =
        portfolio.ownerDeclaredFx || rows.any((row) => row.ownerDeclaredFx);
    if (json['policy_version'] !=
        (portfolio.ownerDeclaredPrice
            ? 'retrospective_latest_index_24h_native_and_krw_v3'
            : owner
            ? 'retrospective_latest_index_24h_native_and_krw_v2'
            : 'retrospective_latest_index_24h_native_and_krw_v1')) {
      throw const FormatException('Inconsistent benchmark authority');
    }
    if (rows.any((row) => !ids.add(row.series.id)) ||
        (rows.isEmpty
            ? issues.length != 1 ||
                  !const {
                    'no_benchmarks',
                    'no_known_benchmarks',
                    'benchmark_limit',
                  }.contains(issues.single.code)
            : issues.isNotEmpty)) {
      throw const FormatException('Invalid benchmark list state');
    }
    return PortfolioBenchmarks._(
      portfolio,
      List.unmodifiable(rows),
      List.unmodifiable(issues),
    );
  }
  final PortfolioTwr portfolio;
  final List<PortfolioBenchmark> benchmarks;
  final List<HoldingValuationIssue> issues;
  bool get ownerDeclaredPrice => portfolio.ownerDeclaredPrice;
  bool get sample => portfolio.sample || benchmarks.any((row) => row.sample);
  bool get ownerDeclaredFx =>
      portfolio.ownerDeclaredFx || benchmarks.any((row) => row.ownerDeclaredFx);
}

class PortfolioXirr {
  const PortfolioXirr._({
    required this.valuation,
    required this.status,
    required this.sample,
    required this.periodStart,
    required this.periodEnd,
    required this.externalEventCount,
    required this.signChanges,
    required this.annualRate,
    required this.candidateRate,
    required this.netContributions,
    required this.issues,
  });

  factory PortfolioXirr.fromJson(Json json) {
    _requireExactKeys(json, const {
      'policy_version',
      'solver_policy',
      'scope',
      'valuation',
      'status',
      'sample',
      'period_start',
      'period_end',
      'external_event_count',
      'sign_changes',
      'uniqueness',
      'annual_rate',
      'candidate_rate',
      'net_contributions',
      'issues',
    });
    if (!const {
          'restated_external_cash_utc_date_actual_365_v1',
          'restated_external_cash_utc_date_actual_365_v2',
          'restated_external_cash_utc_date_actual_365_v3',
        }.contains(_text(json, 'policy_version')) ||
        _text(json, 'solver_policy') !=
            'single_change_bracket_else_log_newton_0_1_v1' ||
        _text(json, 'scope') != 'whole_portfolio_since_inception') {
      throw const FormatException('Unsupported XIRR policy');
    }
    if (json['valuation'] is! Json) {
      throw const FormatException('Missing XIRR valuation');
    }
    final valuation = CurrentPortfolioValuation.fromJson(
      json['valuation'] as Json,
    );
    final status = _text(json, 'status');
    final sample = _bool(json, 'sample');
    final start = _nullableText(json, 'period_start');
    final end = _text(json, 'period_end');
    void date(String value) {
      if (!RegExp(r'^\d{4}-\d{2}-\d{2}$').hasMatch(value)) {
        throw const FormatException('Invalid XIRR date');
      }
      _utcText({'at': '${value}T00:00:00Z'}, 'at');
    }

    date(end);
    if (start != null) date(start);
    final count = json['external_event_count'] == null
        ? null
        : _count(json, 'external_event_count');
    final changes = json['sign_changes'] == null
        ? null
        : _count(json, 'sign_changes');
    final rate = _nullableDecimal(json, 'annual_rate');
    final candidate = _nullableDecimal(json, 'candidate_rate');
    for (final value in [rate, candidate]) {
      if (value != null &&
          (value.length > 323 ||
              !double.parse(value).isFinite ||
              _decimalScale(value) > 12 ||
              (value.startsWith('-') &&
                  _comparePositiveDecimal(value.substring(1), '1') >= 0))) {
        throw const FormatException('Invalid XIRR numerical rate');
      }
    }
    Money? net;
    if (json['net_contributions'] != null) {
      if (json['net_contributions'] is! Json) {
        throw const FormatException('Invalid XIRR cash');
      }
      final money = json['net_contributions'] as Json;
      _requireExactKeys(money, const {'currency', 'amount'});
      net = Money.fromJson(money);
      if (net.currency != 'KRW') {
        throw const FormatException('Invalid XIRR currency');
      }
    }
    final issues = _jsonList(
      json,
      'issues',
    ).map(HoldingValuationIssue.fromJson).toList(growable: false);
    const codes = {
      'sample_data',
      'owner_declared_fx',
      'owner_declared_price',
      'valuation_unavailable',
      'missing_historical_fx',
      'stale_historical_fx',
      'insufficient_period',
      'one_sided_cash_flows',
      'cash_flow_limit',
      'numerical_limit',
      'uniqueness_not_proven',
    };
    final issueKeys = <String>{};
    if (issues.any(
      (issue) =>
          !codes.contains(issue.code) ||
          !issueKeys.add('${issue.code}:${issue.field}') ||
          (issue.field != null &&
              (!const {
                    'missing_historical_fx',
                    'stale_historical_fx',
                  }.contains(issue.code) ||
                  !RegExp(r'^[A-Z]{3}$').hasMatch(issue.field!))),
    )) {
      throw const FormatException('Invalid XIRR issue');
    }
    final hasSample = issues.any((issue) => issue.code == 'sample_data');
    final owner = issues.any((issue) => issue.code == 'owner_declared_fx');
    final ownerPrice = issues.any((i) => i.code == 'owner_declared_price');
    if (_text(json, 'policy_version') !=
            (ownerPrice
                ? 'restated_external_cash_utc_date_actual_365_v3'
                : owner
                ? 'restated_external_cash_utc_date_actual_365_v2'
                : 'restated_external_cash_utc_date_actual_365_v1') ||
        (valuation.ownerDeclaredFx && !owner) ||
        (valuation.ownerDeclaredPrice && !ownerPrice)) {
      throw const FormatException('Inconsistent XIRR authority');
    }
    final incompleteFlows = issues.any(
      (issue) => const {
        'valuation_unavailable',
        'missing_historical_fx',
        'stale_historical_fx',
      }.contains(issue.code),
    );
    final unknownInputs = incompleteFlows && (net != null || changes != null);
    final invalidValuationMissing =
        issues.any((issue) => issue.code == 'valuation_unavailable') &&
        (count != null || start != null || valuation.totalValue != null);
    if (unknownInputs || invalidValuationMissing) {
      throw const FormatException(
        'Uncomputed XIRR inputs were reported as known',
      );
    }
    final unique = _text(json, 'uniqueness');
    final valid = switch (status) {
      'calculated' =>
        rate != null &&
            candidate == null &&
            changes == 1 &&
            unique == 'unique' &&
            issues.every(
              (issue) => const {
                'sample_data',
                'owner_declared_fx',
                'owner_declared_price',
              }.contains(issue.code),
            ),
      'candidate' =>
        rate == null &&
            candidate != null &&
            changes != null &&
            changes > 1 &&
            unique == 'not_proven' &&
            issues.any((issue) => issue.code == 'uniqueness_not_proven') &&
            issues.every(
              (issue) => const {
                'sample_data',
                'owner_declared_fx',
                'owner_declared_price',
                'uniqueness_not_proven',
              }.contains(issue.code),
            ),
      'empty' =>
        rate == null &&
            candidate == null &&
            unique == 'not_applicable' &&
            valuation.status == 'empty' &&
            count == 0 &&
            start == null &&
            net == null &&
            issues.isEmpty &&
            !sample,
      'unavailable' =>
        rate == null &&
            candidate == null &&
            unique == 'not_applicable' &&
            issues.any(
              (issue) => !const {
                'sample_data',
                'owner_declared_fx',
                'owner_declared_price',
                'uniqueness_not_proven',
              }.contains(issue.code),
            ),
      _ => false,
    };
    if (!valid ||
        sample != hasSample ||
        (valuation.sample && !sample) ||
        end != valuation.valuationAsOf.substring(0, 10) ||
        (start != null && start.compareTo(end) > 0) ||
        (count == null && start != null) ||
        (count == 0 && start != null) ||
        (count != null && count > 0 && start == null) ||
        (changes != null && changes > 9999) ||
        (count != null && changes != null && changes > count) ||
        ((rate != null || candidate != null) &&
            (valuation.totalValue == null ||
                net == null ||
                count == null ||
                count == 0 ||
                start == end))) {
      throw const FormatException('Inconsistent XIRR report');
    }
    return PortfolioXirr._(
      valuation: valuation,
      status: status,
      sample: sample,
      periodStart: start,
      periodEnd: end,
      externalEventCount: count,
      signChanges: changes,
      annualRate: rate,
      candidateRate: candidate,
      netContributions: net,
      issues: List.unmodifiable(issues),
    );
  }

  final CurrentPortfolioValuation valuation;
  final String status;
  final bool sample;
  final String? periodStart;
  final String periodEnd;
  final int? externalEventCount;
  final int? signChanges;
  final String? annualRate;
  final String? candidateRate;
  final Money? netContributions;
  final List<HoldingValuationIssue> issues;
  bool get ownerDeclaredFx => issues.any((i) => i.code == 'owner_declared_fx');
  bool get ownerDeclaredPrice =>
      issues.any((i) => i.code == 'owner_declared_price');
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
    required this.instrumentId,
    required this.currency,
    required this.barTimeBasis,
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
    final instrumentId = json['instrument_id'];
    final currency = json['currency'];
    final barTimeBasis = json['bar_time_basis'] ?? 'unspecified';
    if ((instrumentId == null) != (currency == null) ||
        (instrumentId != null &&
            (instrumentId is! String ||
                instrumentId.isEmpty ||
                utf8.encode(instrumentId).length > 128 ||
                RegExp(
                  r'\s|[\u0000-\u001f\u007f-\u009f]',
                ).hasMatch(instrumentId) ||
                currency is! String ||
                !RegExp(r'^[A-Z]{3}$').hasMatch(currency))) ||
        barTimeBasis != 'unspecified') {
      throw const FormatException(
        'Invalid market source identity or time basis',
      );
    }
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
      instrumentId: instrumentId as String?,
      currency: currency as String?,
      barTimeBasis: barTimeBasis as String,
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

  final String? instrumentId;
  final String? currency;
  final String barTimeBasis;
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

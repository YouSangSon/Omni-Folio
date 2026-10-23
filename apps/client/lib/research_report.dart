typedef ResearchJson = Map<String, dynamic>;

enum ResearchPromotion { paperCandidate, noPromotion }

enum ResearchGate { walkForward, finalHoldout, buyAndHoldBaseline }

class ResearchReport {
  const ResearchReport({required this.observedAt, required this.report});

  factory ResearchReport.fromJson(ResearchJson json) {
    _keys(json, const {
      'schema_version',
      'mode',
      'metrics_verification',
      'observed_at',
      'report',
    });
    if (json['schema_version'] != 'research-report.v1' ||
        json['mode'] != 'research_artifact_only' ||
        json['metrics_verification'] != 'reported_not_recomputed') {
      throw const FormatException('Unsupported research report');
    }
    return ResearchReport(
      observedAt: _utc(json['observed_at']),
      report: json['report'] == null
          ? null
          : StoredResearchReport.fromJson(_object(json['report'])),
    );
  }

  final String observedAt;
  final StoredResearchReport? report;
}

class StoredResearchReport {
  const StoredResearchReport({
    required this.recordedAt,
    required this.fastWindow,
    required this.slowWindow,
    required this.quantity,
    required this.candidateCount,
    required this.foldCount,
    required this.finalHoldout,
    required this.promotionTarget,
    required this.failedGates,
    required this.walkForward,
    required this.holdout,
    required this.buyAndHold,
  });

  factory StoredResearchReport.fromJson(ResearchJson json) {
    _keys(json, const {
      'recorded_at',
      'fast_window',
      'slow_window',
      'quantity',
      'candidate_count',
      'fold_count',
      'final_holdout',
      'promotion_target',
      'failed_gates',
      'walk_forward',
      'holdout',
      'buy_and_hold',
    });
    final fast = _positiveInt(json['fast_window']);
    final slow = _positiveInt(json['slow_window']);
    final folds = _positiveInt(json['fold_count']);
    final quantity = _decimal(json['quantity'], negative: false);
    final promotion = switch (json['promotion_target']) {
      'paper_candidate' => ResearchPromotion.paperCandidate,
      'no_promotion' => ResearchPromotion.noPromotion,
      _ => throw const FormatException('Unsupported research promotion'),
    };
    final rawGates = json['failed_gates'];
    if (rawGates is! List) {
      throw const FormatException('Invalid research gates');
    }
    const names = ['walk_forward', 'final_holdout', 'buy_and_hold_baseline'];
    final gates = <ResearchGate>[];
    var previous = -1;
    for (final name in rawGates) {
      final index = names.indexOf(name is String ? name : '');
      if (index <= previous) {
        throw const FormatException('Invalid research gates');
      }
      gates.add(ResearchGate.values[index]);
      previous = index;
    }
    if (fast >= slow ||
        folds < 2 ||
        quantity == '0' ||
        (promotion == ResearchPromotion.paperCandidate) != gates.isEmpty) {
      throw const FormatException('Inconsistent research report');
    }
    return StoredResearchReport(
      recordedAt: _utc(json['recorded_at']),
      fastWindow: fast,
      slowWindow: slow,
      quantity: quantity,
      candidateCount: _positiveInt(json['candidate_count']),
      foldCount: folds,
      finalHoldout: ResearchHoldout.fromJson(_object(json['final_holdout'])),
      promotionTarget: promotion,
      failedGates: List.unmodifiable(gates),
      walkForward: ResearchMetrics.fromJson(_object(json['walk_forward'])),
      holdout: ResearchMetrics.fromJson(_object(json['holdout'])),
      buyAndHold: ResearchMetrics.fromJson(_object(json['buy_and_hold'])),
    );
  }

  final String recordedAt;
  final int fastWindow;
  final int slowWindow;
  final String quantity;
  final int candidateCount;
  final int foldCount;
  final ResearchHoldout finalHoldout;
  final ResearchPromotion promotionTarget;
  final List<ResearchGate> failedGates;
  final ResearchMetrics walkForward;
  final ResearchMetrics holdout;
  final ResearchMetrics buyAndHold;
}

class ResearchHoldout {
  const ResearchHoldout({
    required this.firstBarAt,
    required this.lastBarAt,
    required this.barCount,
  });

  factory ResearchHoldout.fromJson(ResearchJson json) {
    _keys(json, const {'first_bar_at', 'last_bar_at', 'bar_count'});
    final first = _utc(json['first_bar_at']);
    final last = _utc(json['last_bar_at']);
    // DateTime truncates nanoseconds; compare the validated padded UTC text.
    String ordered(String value) =>
        '${value.substring(0, 19)}${value.length == 20 ? '000000000' : value.substring(20, value.length - 1).padRight(9, '0')}';
    final count = _positiveInt(json['bar_count']);
    if (ordered(first).compareTo(ordered(last)) > 0 ||
        (count == 1) != (first == last)) {
      throw const FormatException('Reversed research holdout');
    }
    return ResearchHoldout(firstBarAt: first, lastBarAt: last, barCount: count);
  }

  final String firstBarAt;
  final String lastBarAt;
  final int barCount;
}

class ResearchMetrics {
  const ResearchMetrics({
    required this.afterCostReturn,
    required this.maxDrawdown,
    required this.roundTrips,
  });

  factory ResearchMetrics.fromJson(ResearchJson json) {
    _keys(json, const {'after_cost_return', 'max_drawdown', 'round_trips'});
    final trips = json['round_trips'];
    if (trips is! String ||
        trips.length > 128 ||
        !RegExp(r'^(0|[1-9][0-9]*)$').hasMatch(trips)) {
      throw const FormatException('Invalid research round trips');
    }
    return ResearchMetrics(
      afterCostReturn: _decimal(json['after_cost_return']),
      maxDrawdown: _decimal(json['max_drawdown'], negative: false),
      roundTrips: trips,
    );
  }

  final String afterCostReturn;
  final String maxDrawdown;
  final String roundTrips;
}

String researchPercent(String value) {
  _decimal(value);
  final negative = value.startsWith('-');
  final parts = (negative ? value.substring(1) : value).split('.');
  final fraction = parts.length == 2 ? parts[1] : '';
  var whole = '${parts[0]}${fraction.padRight(2, '0').substring(0, 2)}';
  whole = whole.replaceFirst(RegExp(r'^0+(?=[0-9])'), '');
  final remainder = fraction.length > 2 ? fraction.substring(2) : '';
  return '${negative ? '-' : ''}$whole${remainder.isEmpty ? '' : '.$remainder'}%';
}

ResearchJson _object(Object? value) {
  if (value is! ResearchJson) {
    throw const FormatException('Invalid research object');
  }
  return value;
}

void _keys(ResearchJson json, Set<String> expected) {
  if (json.length != expected.length || !json.keys.every(expected.contains)) {
    throw const FormatException('Unexpected research fields');
  }
}

int _positiveInt(Object? value) {
  if (value is! int || value < 1 || value > 9007199254740991) {
    throw const FormatException('Invalid research count');
  }
  return value;
}

String _decimal(Object? value, {bool negative = true}) {
  if (value is! String ||
      value.length > 128 ||
      value == '-0' ||
      !RegExp(
        negative
            ? r'^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$'
            : r'^(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$',
      ).hasMatch(value)) {
    throw const FormatException('Invalid research decimal');
  }
  return value;
}

String _utc(Object? value) {
  if (value is! String) {
    throw const FormatException('Invalid research timestamp');
  }
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.[0-9]{0,8}[1-9])?Z$',
  ).firstMatch(value);
  final parsed = DateTime.tryParse(value);
  if (match == null ||
      parsed == null ||
      !parsed.isUtc ||
      parsed.year != int.parse(match.group(1)!) ||
      parsed.month != int.parse(match.group(2)!) ||
      parsed.day != int.parse(match.group(3)!) ||
      parsed.hour != int.parse(match.group(4)!) ||
      parsed.minute != int.parse(match.group(5)!) ||
      parsed.second != int.parse(match.group(6)!)) {
    throw const FormatException('Invalid research timestamp');
  }
  return value;
}

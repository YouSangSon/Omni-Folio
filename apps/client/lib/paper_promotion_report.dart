typedef PaperPromotionJson = Map<String, dynamic>;

class PaperPromotionReport {
  const PaperPromotionReport({required this.observedAt, required this.attempt});

  factory PaperPromotionReport.fromJson(PaperPromotionJson json) {
    _keys(json, const {'schema_version', 'mode', 'observed_at', 'attempt'});
    if (json['schema_version'] != 'paper-promotion-report.v1' ||
        json['mode'] != 'paper_comparison_only') {
      throw const FormatException('Unsupported paper promotion report');
    }
    return PaperPromotionReport(
      observedAt: _utc(json['observed_at']),
      attempt: json['attempt'] == null
          ? null
          : PaperPromotionAttempt.fromJson(_object(json['attempt'])),
    );
  }

  final String observedAt;
  final PaperPromotionAttempt? attempt;
}

class PaperPromotionAttempt {
  const PaperPromotionAttempt({
    required this.symbol,
    required this.claimedAt,
    required this.decidedAt,
    required this.comparisonStart,
    required this.comparisonEnd,
    required this.candidate,
    required this.incumbent,
    required this.decision,
    required this.reasonCode,
    required this.selectionStatus,
    required this.comparison,
  });

  factory PaperPromotionAttempt.fromJson(PaperPromotionJson json) {
    _keys(json, const {
      'symbol',
      'claimed_at',
      'decided_at',
      'comparison_start',
      'comparison_end',
      'candidate',
      'incumbent',
      'decision',
      'reason_code',
      'promotion_selection_status',
      'comparison',
    });
    final symbol = json['symbol'];
    if (symbol is! String || !_matchesWhole(r'^[0-9]{6}$', symbol)) {
      throw const FormatException('Invalid paper promotion symbol');
    }
    final claimedAt = _utc(json['claimed_at']);
    final decidedAt = json['decided_at'] == null
        ? null
        : _utc(json['decided_at']);
    final start = _utc(json['comparison_start'], wholeSecond: true);
    final end = _utc(json['comparison_end'], wholeSecond: true);
    final candidate = PaperPromotionStrategy.fromJson(
      _object(json['candidate']),
    );
    final incumbent = PaperPromotionStrategy.fromJson(
      _object(json['incumbent']),
    );
    final decision = json['decision'];
    final reason = json['reason_code'];
    final status = json['promotion_selection_status'];
    final comparison = json['comparison'] == null
        ? null
        : PaperPromotionComparison.fromJson(_object(json['comparison']));
    final validDecision = switch (decision) {
      'PENDING' =>
        reason == 'comparison_claimed' &&
            decidedAt == null &&
            comparison == null &&
            status == 'not_promoted',
      'ERROR' =>
        reason == 'comparison_failed' &&
            decidedAt != null &&
            comparison == null &&
            status == 'not_promoted',
      'ABORTED' =>
        reason == 'explicit_abort' &&
            decidedAt != null &&
            comparison == null &&
            status == 'not_promoted',
      'STALE' =>
        reason == 'account_or_selection_changed' &&
            decidedAt != null &&
            comparison != null &&
            status == 'not_promoted',
      'PROMOTE' =>
        decidedAt != null &&
            comparison != null &&
            (status == 'current' || status == 'superseded') &&
            comparison.decision == decision &&
            comparison.reasonCode == reason,
      'HOLD' =>
        decidedAt != null &&
            comparison != null &&
            status == 'not_promoted' &&
            comparison.decision == decision &&
            comparison.reasonCode == reason,
      _ => false,
    };
    if (!validDecision ||
        candidate.quantity != incumbent.quantity ||
        _compareUtc(start, end) >= 0 ||
        _compareUtc(end, claimedAt) > 0 ||
        decidedAt != null && _compareUtc(claimedAt, decidedAt) > 0 ||
        comparison != null &&
            (comparison.windows.first.start != start ||
                comparison.windows.last.end != end)) {
      throw const FormatException('Inconsistent paper promotion report');
    }
    return PaperPromotionAttempt(
      symbol: symbol,
      claimedAt: claimedAt,
      decidedAt: decidedAt,
      comparisonStart: start,
      comparisonEnd: end,
      candidate: candidate,
      incumbent: incumbent,
      decision: decision as String,
      reasonCode: reason as String,
      selectionStatus: status as String,
      comparison: comparison,
    );
  }

  final String symbol;
  final String claimedAt;
  final String? decidedAt;
  final String comparisonStart;
  final String comparisonEnd;
  final PaperPromotionStrategy candidate;
  final PaperPromotionStrategy incumbent;
  final String decision;
  final String reasonCode;
  final String selectionStatus;
  final PaperPromotionComparison? comparison;
}

class PaperPromotionStrategy {
  const PaperPromotionStrategy({
    required this.fastWindow,
    required this.slowWindow,
    required this.quantity,
  });

  factory PaperPromotionStrategy.fromJson(PaperPromotionJson json) {
    _keys(json, const {'fast_window', 'slow_window', 'quantity'});
    final fast = json['fast_window'];
    final slow = json['slow_window'];
    final quantity = json['quantity'];
    if (fast is! int ||
        slow is! int ||
        fast < 1 ||
        fast >= slow ||
        slow > 365 ||
        quantity is! String ||
        quantity.length > 19 ||
        !_matchesWhole(r'^[1-9][0-9]*$', quantity) ||
        BigInt.parse(quantity) > BigInt.parse('4611686018427387903')) {
      throw const FormatException('Invalid paper promotion strategy');
    }
    return PaperPromotionStrategy(
      fastWindow: fast,
      slowWindow: slow,
      quantity: quantity,
    );
  }

  final int fastWindow;
  final int slowWindow;
  final String quantity;
}

class PaperPromotionComparison {
  const PaperPromotionComparison({
    required this.decision,
    required this.reasonCode,
    required this.windows,
  });

  factory PaperPromotionComparison.fromJson(PaperPromotionJson json) {
    _keys(json, const {
      'policy_version',
      'metrics_verification',
      'decision',
      'reason_code',
      'windows',
    });
    final decision = json['decision'];
    final reason = json['reason_code'];
    final rawWindows = json['windows'];
    final validReason = decision == 'PROMOTE'
        ? reason == 'all_forward_gates_passed'
        : decision == 'HOLD' && _holdReasons.contains(reason);
    if (json['policy_version'] != 'paper-forward-comparison.v1' ||
        json['metrics_verification'] != 'recomputed_from_stored_input' ||
        !validReason ||
        rawWindows is! List ||
        rawWindows.length != 3) {
      throw const FormatException('Invalid paper promotion comparison');
    }
    final windows = rawWindows
        .map((value) => PaperPromotionWindow.fromJson(_object(value)))
        .toList(growable: false);
    for (var i = 1; i < windows.length; i++) {
      if (_compareUtc(windows[i - 1].end, windows[i].start) >= 0) {
        throw const FormatException('Overlapping paper comparison windows');
      }
    }
    return PaperPromotionComparison(
      decision: decision as String,
      reasonCode: reason as String,
      windows: List.unmodifiable(windows),
    );
  }

  final String decision;
  final String reasonCode;
  final List<PaperPromotionWindow> windows;
}

const _holdReasons = {
  'insufficient_round_trips',
  'candidate_negative_return',
  'candidate_return_below_incumbent',
  'candidate_drawdown_above_incumbent',
  'candidate_turnover_above_incumbent',
  'holdout_not_better_than_incumbent',
  'holdout_not_better_than_buy_and_hold',
};

class PaperPromotionWindow {
  const PaperPromotionWindow({
    required this.start,
    required this.end,
    required this.candidate,
    required this.incumbent,
    required this.buyAndHold,
  });

  factory PaperPromotionWindow.fromJson(PaperPromotionJson json) {
    _keys(json, const {
      'start',
      'end',
      'candidate',
      'incumbent',
      'buy_and_hold',
    });
    final start = _utc(json['start'], wholeSecond: true);
    final end = _utc(json['end'], wholeSecond: true);
    if (_compareUtc(start, end) >= 0) {
      throw const FormatException('Invalid paper comparison window');
    }
    return PaperPromotionWindow(
      start: start,
      end: end,
      candidate: PaperPromotionMetrics.fromJson(_object(json['candidate'])),
      incumbent: PaperPromotionMetrics.fromJson(_object(json['incumbent'])),
      buyAndHold: PaperPromotionMetrics.fromJson(_object(json['buy_and_hold'])),
    );
  }

  final String start;
  final String end;
  final PaperPromotionMetrics candidate;
  final PaperPromotionMetrics incumbent;
  final PaperPromotionMetrics buyAndHold;
}

class PaperPromotionMetrics {
  const PaperPromotionMetrics({
    required this.afterCostReturn,
    required this.maxDrawdown,
    required this.roundTrips,
    required this.turnover,
  });

  factory PaperPromotionMetrics.fromJson(PaperPromotionJson json) {
    _keys(json, const {
      'after_cost_return',
      'max_drawdown',
      'round_trips',
      'turnover',
    });
    return PaperPromotionMetrics(
      afterCostReturn: _decimal(json['after_cost_return'], signed: true),
      maxDrawdown: _decimal(json['max_drawdown']),
      roundTrips: _decimal(json['round_trips'], integer: true),
      turnover: _decimal(json['turnover']),
    );
  }

  final String afterCostReturn;
  final String maxDrawdown;
  final String roundTrips;
  final String turnover;
}

PaperPromotionJson _object(Object? value) {
  if (value is! PaperPromotionJson) {
    throw const FormatException('Invalid paper promotion object');
  }
  return value;
}

void _keys(PaperPromotionJson json, Set<String> expected) {
  if (json.length != expected.length || !json.keys.every(expected.contains)) {
    throw const FormatException('Unexpected paper promotion fields');
  }
}

String _decimal(Object? value, {bool signed = false, bool integer = false}) {
  final pattern = integer
      ? r'^(0|[1-9][0-9]*)$'
      : signed
      ? r'^(?:0|-?(?:[1-9][0-9]*(?:\.[0-9]*[1-9])?|0\.[0-9]*[1-9]))$'
      : r'^(?:0|[1-9][0-9]*(?:\.[0-9]*[1-9])?|0\.[0-9]*[1-9])$';
  if (value is! String ||
      value.length > 128 ||
      !_matchesWhole(pattern, value)) {
    throw const FormatException('Invalid paper promotion metric');
  }
  return value;
}

bool _matchesWhole(String pattern, String value) =>
    RegExp(pattern).firstMatch(value)?.group(0) == value;

String _utc(Object? value, {bool wholeSecond = false}) {
  if (value is! String) throw const FormatException('Invalid UTC timestamp');
  final match = RegExp(
    wholeSecond
        ? r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})Z$'
        : r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.[0-9]{0,8}[1-9])?Z$',
  ).firstMatch(value);
  final parsed = DateTime.tryParse(value);
  if (match == null ||
      match.group(0) != value ||
      parsed == null ||
      !parsed.isUtc ||
      parsed.year != int.parse(match.group(1)!) ||
      parsed.month != int.parse(match.group(2)!) ||
      parsed.day != int.parse(match.group(3)!) ||
      parsed.hour != int.parse(match.group(4)!) ||
      parsed.minute != int.parse(match.group(5)!) ||
      parsed.second != int.parse(match.group(6)!)) {
    throw const FormatException('Invalid UTC timestamp');
  }
  return value;
}

// DateTime truncates nanoseconds; compare validated, zero-padded UTC text.
int _compareUtc(String left, String right) {
  String ordered(String value) =>
      '${value.substring(0, 19)}${value.length == 20 ? '000000000' : value.substring(20, value.length - 1).padRight(9, '0')}';
  return ordered(left).compareTo(ordered(right));
}

typedef PaperMonitorJson = Map<String, dynamic>;

class PaperMonitor {
  const PaperMonitor({
    required this.schemaVersion,
    required this.mode,
    required this.observedAt,
    required this.strategySelected,
    required this.sessionCount,
    required this.pendingPolicyCount,
    required this.runner,
    required this.latestPolicy,
  });

  factory PaperMonitor.fromJson(PaperMonitorJson json) {
    _exactKeys(json, const {
      'schema_version',
      'mode',
      'observed_at',
      'strategy_selected',
      'session_count',
      'pending_policy_count',
      'runner',
      'latest_policy',
    });
    final schemaVersion = _text(json, 'schema_version');
    final mode = _text(json, 'mode');
    final runner = json['runner'];
    final latestPolicy = json['latest_policy'];
    if (schemaVersion != 'paper-monitor.v1' ||
        mode != 'paper_fixture_only' ||
        runner is! PaperMonitorJson ||
        latestPolicy != null && latestPolicy is! PaperMonitorJson) {
      throw const FormatException('Unsupported paper monitor response');
    }
    return PaperMonitor(
      schemaVersion: schemaVersion,
      mode: mode,
      observedAt: _utc(json, 'observed_at'),
      strategySelected: _boolean(json, 'strategy_selected'),
      sessionCount: _nonnegativeInt(json, 'session_count'),
      pendingPolicyCount: _nonnegativeInt(json, 'pending_policy_count'),
      runner: PaperMonitorRunner.fromJson(runner),
      latestPolicy: latestPolicy == null
          ? null
          : PaperMonitorPolicy.fromJson(latestPolicy as PaperMonitorJson),
    );
  }

  final String schemaVersion;
  final String mode;
  final String observedAt;
  final bool strategySelected;
  final int sessionCount;
  final int pendingPolicyCount;
  final PaperMonitorRunner runner;
  final PaperMonitorPolicy? latestPolicy;
}

class PaperMonitorRunner {
  const PaperMonitorRunner({
    required this.state,
    required this.heartbeatAt,
    required this.expiresAt,
  });

  factory PaperMonitorRunner.fromJson(PaperMonitorJson json) {
    _exactKeys(json, const {'state', 'heartbeat_at', 'expires_at'});
    final state = _text(json, 'state');
    if (!const {
      'unowned',
      'lease_recorded',
      'expired',
      'clock_regressed',
      'selection_changed',
    }.contains(state)) {
      throw const FormatException('Unsupported paper runner state');
    }
    final heartbeatAt = _nullableUtc(json, 'heartbeat_at');
    final expiresAt = _nullableUtc(json, 'expires_at');
    if ((state == 'unowned') != (heartbeatAt == null && expiresAt == null) ||
        state != 'unowned' && (heartbeatAt == null || expiresAt == null)) {
      throw const FormatException('Inconsistent paper runner timestamps');
    }
    return PaperMonitorRunner(
      state: state,
      heartbeatAt: heartbeatAt,
      expiresAt: expiresAt,
    );
  }

  final String state;
  final String? heartbeatAt;
  final String? expiresAt;
}

class PaperMonitorPolicy {
  const PaperMonitorPolicy({
    required this.asOf,
    required this.recordedAt,
    required this.decision,
    required this.reasonCode,
    required this.matchesCurrentSelection,
  });

  factory PaperMonitorPolicy.fromJson(PaperMonitorJson json) {
    _exactKeys(json, const {
      'as_of',
      'recorded_at',
      'decision',
      'reason_code',
      'matches_current_selection',
    });
    final decision = _text(json, 'decision');
    final reasonCode = _text(json, 'reason_code');
    if (!const {
          'INSUFFICIENT',
          'HOLD',
          'HALT_AND_ROLLBACK',
        }.contains(decision) ||
        !const {
          'minimum_same_selection_samples_not_met',
          'within_local_paper_safety_bounds',
          'max_drawdown_limit_reached',
          'cumulative_return_floor_reached',
        }.contains(reasonCode)) {
      throw const FormatException('Unsupported paper policy value');
    }
    final reasonMatchesDecision = switch (decision) {
      'INSUFFICIENT' => reasonCode == 'minimum_same_selection_samples_not_met',
      'HOLD' => reasonCode == 'within_local_paper_safety_bounds',
      'HALT_AND_ROLLBACK' => const {
        'max_drawdown_limit_reached',
        'cumulative_return_floor_reached',
      }.contains(reasonCode),
      _ => false,
    };
    if (!reasonMatchesDecision) {
      throw const FormatException('Inconsistent paper policy decision');
    }
    return PaperMonitorPolicy(
      asOf: _utc(json, 'as_of'),
      recordedAt: _utc(json, 'recorded_at'),
      decision: decision,
      reasonCode: reasonCode,
      matchesCurrentSelection: _boolean(json, 'matches_current_selection'),
    );
  }

  final String asOf;
  final String recordedAt;
  final String decision;
  final String reasonCode;
  final bool matchesCurrentSelection;
}

void _exactKeys(PaperMonitorJson json, Set<String> expected) {
  if (json.length != expected.length || !json.keys.every(expected.contains)) {
    throw const FormatException('Unexpected paper monitor fields');
  }
}

String _text(PaperMonitorJson json, String key) {
  final value = json[key];
  if (value is! String || value.isEmpty) {
    throw const FormatException('Missing paper monitor text');
  }
  return value;
}

bool _boolean(PaperMonitorJson json, String key) {
  final value = json[key];
  if (value is! bool) throw const FormatException('Invalid paper monitor flag');
  return value;
}

int _nonnegativeInt(PaperMonitorJson json, String key) {
  final value = json[key];
  if (value is! int || value < 0) {
    throw const FormatException('Invalid paper monitor count');
  }
  return value;
}

String? _nullableUtc(PaperMonitorJson json, String key) {
  final value = json[key];
  if (value == null) return null;
  return _validUtc(value);
}

String _utc(PaperMonitorJson json, String key) => _validUtc(json[key]);

String _validUtc(Object? value) {
  if (value is! String) throw const FormatException('Invalid UTC timestamp');
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?Z$',
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
    throw const FormatException('Invalid UTC timestamp');
  }
  return value;
}

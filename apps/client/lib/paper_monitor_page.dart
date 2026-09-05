import 'package:flutter/material.dart';

import 'api.dart';
import 'paper_monitor.dart';

class PaperMonitorPage extends StatefulWidget {
  const PaperMonitorPage({super.key, required this.api});

  final OmniApi api;

  @override
  State<PaperMonitorPage> createState() => _PaperMonitorPageState();
}

class _PaperMonitorPageState extends State<PaperMonitorPage> {
  PaperMonitor? _monitor;
  String? _error;
  var _busy = false;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final monitor = await widget.api.paperMonitor();
      if (mounted) setState(() => _monitor = monitor);
    } catch (_) {
      if (mounted) {
        setState(() {
          _error = _monitor == null
              ? '모의 자동매매 상태를 불러오지 못했습니다'
              : '새로고침하지 못했습니다. 마지막 정상 관측을 그대로 유지합니다.';
        });
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text(
        '모의 자동매매 상태',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: '모의 자동매매 상태 새로고침',
          onPressed: _busy ? null : _refresh,
          icon: const Icon(Icons.refresh_rounded),
        ),
        const SizedBox(width: 8),
      ],
    ),
    body: SafeArea(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: _body(context),
        ),
      ),
    ),
  );

  Widget _body(BuildContext context) {
    final monitor = _monitor;
    if (monitor == null && _busy) {
      return Center(
        child: Semantics(
          liveRegion: true,
          label: '모의 자동매매 상태 불러오는 중',
          child: const CircularProgressIndicator(),
        ),
      );
    }
    if (monitor == null) {
      return _MonitorMessage(
        title: _error ?? '모의 자동매매 상태를 불러오지 못했습니다',
        action: _busy ? null : _refresh,
      );
    }
    return ListView(
      key: const Key('paper-monitor-scroll'),
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        if (_busy) ...[
          const LinearProgressIndicator(),
          const SizedBox(height: 12),
        ],
        if (_error != null) ...[
          _MonitorNotice(
            icon: Icons.sync_problem_outlined,
            text: _error!,
            color: Theme.of(context).colorScheme.error,
            liveRegion: true,
          ),
          const SizedBox(height: 12),
        ],
        _MonitorNotice(
          icon: Icons.info_outline,
          text:
              '저장된 로컬 모의 데이터 · 현재 증권사 상태 아님\n'
              '프로세스 생존, 실행 권한, 실전 거래, 안전 또는 수익성을 뜻하지 않습니다.',
          color: Theme.of(context).colorScheme.primary,
        ),
        const SizedBox(height: 12),
        _ObservationCard(monitor: monitor),
        const SizedBox(height: 12),
        _RunnerCard(runner: monitor.runner),
        const SizedBox(height: 12),
        _PolicyCard(policy: monitor.latestPolicy),
      ],
    );
  }
}

class _ObservationCard extends StatelessWidget {
  const _ObservationCard({required this.monitor});

  final PaperMonitor monitor;

  @override
  Widget build(BuildContext context) {
    final selection = monitor.strategySelected ? '전략 선택됨' : '선택된 전략 없음';
    return _MonitorCard(
      title: '저장 스냅샷',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(selection, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 12),
          Text('모의 계좌 ${monitor.sessionCount}개'),
          const SizedBox(height: 4),
          Text('미완료 정책 ${monitor.pendingPolicyCount}건'),
          const SizedBox(height: 4),
          Text(
            '저장된 성과 중 안전정책 평가가 끝나지 않은 기록입니다. '
            '이전 전략의 기록도 포함하며, 실행 대기 주문 수가 아닙니다.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 12),
          Text('관측 시각 ${monitor.observedAt}', style: _tabular(context)),
        ],
      ),
    );
  }
}

class _RunnerCard extends StatelessWidget {
  const _RunnerCard({required this.runner});

  final PaperMonitorRunner runner;

  @override
  Widget build(BuildContext context) {
    final state = switch (runner.state) {
      'unowned' => '소유권 기록 없음',
      'lease_recorded' => '만료 전 소유권 기록',
      'expired' => '만료된 소유권 기록',
      'clock_regressed' => '서버 시각 확인 필요',
      'selection_changed' => '전략 선택 변경 기록',
      _ => throw StateError('validated paper runner state required'),
    };
    final timestamps = runner.heartbeatAt == null
        ? '하트비트·만료 시각 없음'
        : '하트비트 ${runner.heartbeatAt}\n만료 ${runner.expiresAt}';
    return _MonitorCard(
      title: '정책 실행 소유권 기록',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(state, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Text(timestamps, style: _tabular(context)),
          const SizedBox(height: 8),
          Text(
            runner.state == 'expired'
                ? '만료 기록만으로 인계 가능 여부를 판단하지 않습니다.'
                : '클라이언트는 임대 신선도나 소유권을 판정하지 않습니다.',
          ),
          const SizedBox(height: 4),
          Text(
            '저장된 정책 실행 소유권 기록이며 프로세스 생존이나 실제 주문 상태가 아닙니다.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
    );
  }
}

class _PolicyCard extends StatelessWidget {
  const _PolicyCard({required this.policy});

  final PaperMonitorPolicy? policy;

  @override
  Widget build(BuildContext context) {
    final policy = this.policy;
    if (policy == null) {
      return const _MonitorCard(title: '최근 정책 결정', child: Text('저장된 정책 결정 없음'));
    }
    final decision = switch (policy.decision) {
      'INSUFFICIENT' => '근거 부족',
      'HOLD' => '유지',
      'HALT_AND_ROLLBACK' => '중단 및 롤백',
      _ => throw StateError('validated paper policy decision required'),
    };
    final reason = switch (policy.reasonCode) {
      'minimum_same_selection_samples_not_met' => '같은 전략 선택의 최소 표본 미충족',
      'within_local_paper_safety_bounds' => '로컬 모의 정책 범위 안',
      'max_drawdown_limit_reached' => '최대 낙폭 한도 도달',
      'cumulative_return_floor_reached' => '누적 수익률 하한 도달',
      _ => throw StateError('validated paper policy reason required'),
    };
    final selection = policy.matchesCurrentSelection
        ? '현재 선택과 일치'
        : '현재 선택과 불일치';
    return _MonitorCard(
      title: '최근 정책 결정',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(decision, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Text(reason),
          const SizedBox(height: 8),
          Text(selection),
          if (!policy.matchesCurrentSelection) ...[
            const SizedBox(height: 4),
            Text(
              '자동 롤백 뒤에는 불일치가 예상될 수 있습니다.',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
          const SizedBox(height: 8),
          Text('정책 기준 ${policy.asOf}', style: _tabular(context)),
          const SizedBox(height: 4),
          Text('정책 기록 ${policy.recordedAt}', style: _tabular(context)),
          const SizedBox(height: 8),
          Text(
            '모든 로컬 계좌에서 가장 나중에 저장된 모의 결정입니다. '
            '현재 전략의 정책이 아닐 수 있으며, 현재 안전이나 수익성을 뜻하지 않습니다.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
    );
  }
}

class _MonitorCard extends StatelessWidget {
  const _MonitorCard({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) => MergeSemantics(
    child: Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Semantics(
              header: true,
              child: Text(
                title,
                style: Theme.of(context).textTheme.titleMedium,
              ),
            ),
            const SizedBox(height: 12),
            child,
          ],
        ),
      ),
    ),
  );
}

class _MonitorNotice extends StatelessWidget {
  const _MonitorNotice({
    required this.icon,
    required this.text,
    required this.color,
    this.liveRegion = false,
  });

  final IconData icon;
  final String text;
  final Color color;
  final bool liveRegion;

  @override
  Widget build(BuildContext context) => Semantics(
    container: true,
    excludeSemantics: true,
    liveRegion: liveRegion,
    label: text,
    child: DecoratedBox(
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(12),
      ),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(icon, color: color),
            const SizedBox(width: 8),
            Expanded(child: Text(text)),
          ],
        ),
      ),
    ),
  );
}

class _MonitorMessage extends StatelessWidget {
  const _MonitorMessage({required this.title, required this.action});

  final String title;
  final VoidCallback? action;

  @override
  Widget build(BuildContext context) => ListView(
    padding: const EdgeInsets.all(24),
    children: [
      const SizedBox(height: 48),
      Icon(
        Icons.cloud_off_outlined,
        size: 48,
        color: Theme.of(context).colorScheme.error,
      ),
      const SizedBox(height: 16),
      Semantics(
        liveRegion: true,
        child: Text(
          title,
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.titleMedium,
        ),
      ),
      const SizedBox(height: 8),
      const Text(
        '민감한 오류 정보는 표시하지 않습니다. 서버를 확인한 뒤 다시 시도하세요.',
        textAlign: TextAlign.center,
      ),
      const SizedBox(height: 20),
      ElevatedButton(onPressed: action, child: const Text('다시 시도')),
    ],
  );
}

TextStyle _tabular(BuildContext context) => Theme.of(context)
    .textTheme
    .bodyMedium!
    .copyWith(fontFeatures: const [FontFeature.tabularFigures()]);

import 'package:flutter/material.dart';

import 'api.dart';
import 'research_report.dart';

class ResearchReportPage extends StatefulWidget {
  const ResearchReportPage({super.key, required this.api});
  final OmniApi api;

  @override
  State<ResearchReportPage> createState() => _ResearchReportPageState();
}

class _ResearchReportPageState extends State<ResearchReportPage> {
  ResearchReport? _observation;
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
      final observation = await widget.api.researchReport();
      if (mounted) setState(() => _observation = observation);
    } catch (_) {
      if (mounted) {
        setState(
          () => _error = _observation == null
              ? '연구 보고서를 불러오지 못했습니다'
              : '새로고침하지 못했습니다. 마지막 정상 관측을 그대로 유지합니다.',
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text(
        '백테스트 보고서',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: '백테스트 보고서 새로고침',
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
    final observation = _observation;
    if (observation == null && _busy) {
      return Center(
        child: Semantics(
          liveRegion: true,
          label: '연구 보고서 불러오는 중',
          child: const CircularProgressIndicator(),
        ),
      );
    }
    if (observation == null) {
      return ListView(
        padding: const EdgeInsets.all(24),
        children: [
          const SizedBox(height: 48),
          Semantics(
            liveRegion: true,
            child: Text(
              _error ?? '연구 보고서를 불러오지 못했습니다',
              textAlign: TextAlign.center,
            ),
          ),
          const SizedBox(height: 16),
          ElevatedButton(
            onPressed: _busy ? null : _refresh,
            child: const Text('다시 시도'),
          ),
        ],
      );
    }
    final report = observation.report;
    return ListView(
      key: const Key('research-report-scroll'),
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      children: [
        if (_busy) ...[
          const LinearProgressIndicator(),
          const SizedBox(height: 12),
        ],
        if (_error != null) ...[
          Semantics(
            liveRegion: true,
            child: Text(
              _error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
          const SizedBox(height: 12),
        ],
        const _ReportCard(
          title: '저장된 연구 결과 · 지표 재계산 안 함',
          children: [
            Text('가장 나중에 등록된 연구 결과입니다. 현재 선택한 전략이나 현재 포트폴리오의 성과가 아닙니다.'),
            Text('투자 조언, 수익 보장 또는 주문 실행 권한을 뜻하지 않습니다.'),
          ],
        ),
        const SizedBox(height: 12),
        _ReportCard(
          title: '최근 등록 결과',
          children: [
            Text('관측 시각 ${observation.observedAt}'),
            if (report == null)
              const Text('저장된 연구 보고서 없음')
            else ...[
              Text('등록 시각 ${report.recordedAt}'),
              const Text('SMA 1.0.0'),
              Text('후보 ${report.candidateCount}개 · 검증 구간 ${report.foldCount}개'),
              Text(
                report.promotionTarget == ResearchPromotion.paperCandidate
                    ? '저장된 판정: 모의 후보'
                    : '저장된 판정: 승격하지 않음',
              ),
              if (report.failedGates.isNotEmpty)
                Text(
                  '미통과 평가: ${report.failedGates.map(_gateLabel).join(', ')}',
                ),
            ],
          ],
        ),
        if (report != null) ...[
          const SizedBox(height: 12),
          _ReportCard(
            title: '구간마다 다시 선택한 후보의 검증 요약',
            children: [
              const Text(
                '각 구간에서 후보를 다시 선택한 walk-forward 결과입니다. 최종 평가 후보 하나의 성과가 아닙니다.',
              ),
              Text(
                '비용 차감 산술평균 수익률 ${researchPercent(report.walkForward.afterCostReturn)}',
              ),
              Text(
                '구간 중 최대 낙폭 ${researchPercent(report.walkForward.maxDrawdown)}',
              ),
              Text('합산 완료 왕복 거래 ${report.walkForward.roundTrips}'),
            ],
          ),
          const SizedBox(height: 12),
          _ReportCard(
            title: '최종 평가 후보',
            children: [
              Text(
                '빠른 창 ${report.fastWindow} · 느린 창 ${report.slowWindow} · 수량 ${report.quantity}',
              ),
              const Text('최종 평가 이전 자료 전체에서 선택해 최종 홀드아웃에서 평가한 후보입니다.'),
              Text('첫 봉 ${report.finalHoldout.firstBarAt}'),
              Text('마지막 봉 ${report.finalHoldout.lastBarAt}'),
              Text('평가 봉 ${report.finalHoldout.barCount}개'),
              const Text('최종 홀드아웃 결과'),
              ..._metrics(report.holdout),
            ],
          ),
          const SizedBox(height: 12),
          _ReportCard(
            title: '매수 후 보유 비교',
            children: [
              const Text(
                '같은 최종 홀드아웃에서 동일한 현금·수량·비용·지연을 적용한 모델입니다. 종목 가격의 단순 등락률이 아닙니다.',
              ),
              ..._metrics(report.buyAndHold),
            ],
          ),
        ],
      ],
    );
  }
}

List<Widget> _metrics(ResearchMetrics metrics) => [
  Text('비용 차감 수익률 ${researchPercent(metrics.afterCostReturn)}'),
  Text('최대 낙폭 ${researchPercent(metrics.maxDrawdown)}'),
  Text('완료 왕복 거래 ${metrics.roundTrips}'),
];

String _gateLabel(ResearchGate gate) => switch (gate) {
  ResearchGate.walkForward => '구간별 재선택 검증',
  ResearchGate.finalHoldout => '최종 홀드아웃',
  ResearchGate.buyAndHoldBaseline => '매수 후 보유 비교',
};

class _ReportCard extends StatelessWidget {
  const _ReportCard({required this.title, required this.children});
  final String title;
  final List<Widget> children;

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
            for (final child in children) ...[const SizedBox(height: 8), child],
          ],
        ),
      ),
    ),
  );
}

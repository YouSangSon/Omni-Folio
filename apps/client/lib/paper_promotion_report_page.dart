import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart' show decimalPercent;
import 'paper_promotion_report.dart';

class PaperPromotionReportPage extends StatefulWidget {
  const PaperPromotionReportPage({super.key, required this.api});

  final OmniApi api;

  @override
  State<PaperPromotionReportPage> createState() => _PaperPromotionReportPageState();
}

class _PaperPromotionReportPageState extends State<PaperPromotionReportPage> {
  PaperPromotionReport? _observation;
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
      final observation = await widget.api.paperPromotionReport();
      if (mounted) setState(() => _observation = observation);
    } catch (_) {
      if (mounted) {
        setState(() {
          _error = _observation == null
              ? '모의 전략 비교를 불러오지 못했습니다.'
              : '새로고침하지 못했습니다. 마지막 정상 관측을 그대로 유지합니다.';
        });
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final observation = _observation;
    final attempt = observation?.attempt;
    return Scaffold(
      appBar: AppBar(
        title: const Text(
          '모의 전략 비교',
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          IconButton(
            tooltip: '모의 전략 비교 새로고침',
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
            child: ListView(
              key: const Key('paper-promotion-report-scroll'),
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
              children: [
                _card(context, '저장된 모의 비교', const [
                  Text('저장된 모델 비교이며 현재 증권사 상태, 실시간 시장 정보 또는 실제 수익이 아닙니다.'),
                  Text('세 구간은 각각 같은 현금으로 시작합니다. 구간 결과를 누적하거나 합산하지 않습니다.'),
                  Text('전략 선택 결과는 자동매매 실행 권한을 뜻하지 않습니다.'),
                ]),
                if (_busy) ...[
                  const SizedBox(height: 12),
                  Semantics(
                    liveRegion: true,
                    label: '모의 전략 비교 불러오는 중',
                    excludeSemantics: true,
                    child: MediaQuery.disableAnimationsOf(context)
                        ? const Text('모의 전략 비교 불러오는 중')
                        : const LinearProgressIndicator(),
                  ),
                ],
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Semantics(
                    liveRegion: true,
                    child: Text(
                      _error!,
                      style: TextStyle(color: Theme.of(context).colorScheme.error),
                    ),
                  ),
                  if (observation == null) ...[
                    const SizedBox(height: 12),
                    ElevatedButton(onPressed: _busy ? null : _refresh, child: const Text('다시 시도')),
                  ],
                ],
                if (observation != null) ...[
                  const SizedBox(height: 12),
                  _card(context, '최근 비교 기록', [
                    Text('관측 시각 ${observation.observedAt}', style: _tabular(context)),
                    if (attempt == null)
                      const Text('저장된 모의 전략 비교 기록이 없습니다.')
                    else ...[
                      Text(_decision(attempt.decision), style: Theme.of(context).textTheme.titleMedium),
                      Text(_reason(attempt.reasonCode)),
                      if (attempt.decision == 'PROMOTE')
                        Text(attempt.selectionStatus == 'current'
                            ? '이 비교의 선택 기록이 현재 선택으로 남아 있습니다.'
                            : '이 비교의 선택 기록은 현재 선택이 아닙니다. 같은 전략을 다시 선택했더라도 다른 선택 기록입니다.'),
                      if (attempt.decision == 'HOLD')
                        const Text('비교 시점의 결과이며 현재 안전을 뜻하지 않습니다.'),
                      Text('비교 요청 기록 시각 ${attempt.claimedAt}', style: _tabular(context)),
                      Text('결정 시각 ${attempt.decidedAt ?? '기록 없음'}', style: _tabular(context)),
                      Text('비교 범위 ${attempt.comparisonStart} ~ ${attempt.comparisonEnd}', style: _tabular(context)),
                    ],
                  ]),
                  if (attempt != null) ...[
                    const SizedBox(height: 12),
                    _card(context, '비교한 전략', [
                      Text('종목 ${attempt.symbol} · 이동평균 교차', style: _tabular(context)),
                      Text('후보: ${_strategy(attempt.candidate)}', style: _tabular(context)),
                      Text('당시 선택 전략: ${_strategy(attempt.incumbent)}', style: _tabular(context)),
                    ]),
                    if (attempt.comparison case final comparison?) ...[
                      const SizedBox(height: 12),
                      _card(context, '저장된 비교 지표', [
                        const Text('저장된 입력으로 다시 확인한 모델 지표입니다. 매수 후 보유도 같은 봉과 실행 모델을 사용합니다.'),
                        if (attempt.decision == 'STALE')
                          Text(
                            '비교 지표 판정: ${comparison.decision == 'PROMOTE' ? '후보 기준 통과' : '후보 기준 미통과'} · ${_reason(comparison.reasonCode)}',
                          ),
                        const Text('거래 회전율은 금액이 아닌 비율입니다.'),
                      ]),
                      for (var i = 0; i < comparison.windows.length; i++) ...[
                        const SizedBox(height: 8),
                        _window(context, comparison.windows[i], i),
                      ],
                    ],
                  ],
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

String _strategy(PaperPromotionStrategy strategy) =>
    '단기 이동평균 ${strategy.fastWindow}개 봉 · 장기 이동평균 ${strategy.slowWindow}개 봉 · 수량 ${strategy.quantity}주';

String _decision(String decision) => switch (decision) {
  'PENDING' => '결정 기록 없음',
  'PROMOTE' => '이 비교에서 후보 선택',
  'HOLD' => '이 비교에서 후보를 선택하지 않음',
  'STALE' => '적용 전 상태 검증을 통과하지 못해 선택에 반영하지 않았습니다.',
  'ERROR' => '비교 기록 처리 실패',
  'ABORTED' => '비교 결정 중단',
  _ => throw StateError('Validated decision required'),
};

String _reason(String reason) => switch (reason) {
  'comparison_claimed' => '비교 요청이 저장되었으나 결정 기록은 없습니다.',
  'all_forward_gates_passed' => '비교 기준을 모두 통과했습니다.',
  'insufficient_round_trips' => '완료된 왕복 거래가 기준에 못 미쳤습니다.',
  'candidate_negative_return' => '한 구간에서 후보의 비용 차감 수익률이 음수였습니다.',
  'candidate_return_below_incumbent' => '한 구간에서 후보 수익률이 당시 선택 전략보다 낮았습니다.',
  'candidate_drawdown_above_incumbent' => '한 구간에서 후보 낙폭이 당시 선택 전략보다 컸습니다.',
  'candidate_turnover_above_incumbent' => '한 구간에서 후보 거래 회전율이 당시 선택 전략보다 높았습니다.',
  'holdout_not_better_than_incumbent' => '마지막 검증 구간에서 당시 선택 전략보다 낫지 않았습니다.',
  'holdout_not_better_than_buy_and_hold' => '마지막 검증 구간에서 매수 후 보유 모델보다 낫지 않았습니다.',
  'account_or_selection_changed' => '저장된 비교의 적용 전 상태를 확인하지 못했습니다.',
  'comparison_failed' => '저장된 비교를 처리하지 못했습니다.',
  'explicit_abort' => '중단 결정이 기록되었습니다.',
  _ => throw StateError('Validated reason required'),
};

Widget _card(BuildContext context, String title, List<Widget> children) =>
    MergeSemantics(
      child: Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Semantics(header: true, child: Text(title, style: Theme.of(context).textTheme.titleMedium)),
              for (final child in children) ...[const SizedBox(height: 8), child],
            ],
          ),
        ),
      ),
    );

Widget _window(BuildContext context, PaperPromotionWindow window, int index) =>
    Card(
      child: ExpansionTile(
        expansionAnimationStyle: MediaQuery.disableAnimationsOf(context)
            ? AnimationStyle.noAnimation
            : null,
        title: Text(index == 2 ? '구간 3 · 마지막 검증 구간 · 30개 봉' : '구간 ${index + 1} · 30개 봉'),
        childrenPadding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        expandedCrossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('${window.start} ~ ${window.end}', style: _tabular(context)),
          for (final (label, metrics) in [
            ('후보', window.candidate),
            ('당시 선택 전략', window.incumbent),
            ('매수 후 보유 모델', window.buyAndHold),
          ]) ...[
            const SizedBox(height: 12),
            Semantics(header: true, child: Text(label, style: Theme.of(context).textTheme.titleSmall)),
            Text('비용 차감 수익률 ${decimalPercent(metrics.afterCostReturn)}', style: _tabular(context)),
            Text('최대 낙폭 ${decimalPercent(metrics.maxDrawdown)}', style: _tabular(context)),
            Text('완료 왕복 거래 ${metrics.roundTrips}회', style: _tabular(context)),
            Text('거래 회전율 ${decimalPercent(metrics.turnover)}', style: _tabular(context)),
          ],
        ],
      ),
    );

TextStyle _tabular(BuildContext context) => Theme.of(context)
    .textTheme
    .bodyMedium!
    .copyWith(fontFeatures: const [FontFeature.tabularFigures()]);

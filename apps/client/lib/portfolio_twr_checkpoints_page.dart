import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart';
import 'portfolio_twr_checkpoint_plot.dart';
import 'portfolio_twr_issue.dart';

class PortfolioTwrCheckpointsPage extends StatefulWidget {
  const PortfolioTwrCheckpointsPage({super.key, required this.api});
  final OmniApi api;
  @override
  State<PortfolioTwrCheckpointsPage> createState() =>
      _PortfolioTwrCheckpointsPageState();
}

class _PortfolioTwrCheckpointsPageState
    extends State<PortfolioTwrCheckpointsPage> {
  PortfolioTwrCheckpoints? _observation;
  bool _busy = false, _failed = false;
  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    if (_busy) {
      return;
    }
    setState(() {
      _busy = true;
      _failed = false;
    });
    try {
      final value = await widget.api.portfolioTwrCheckpoints();
      if (mounted) {
        setState(() => _observation = value);
      }
    } catch (_) {
      if (mounted) {
        setState(() => _failed = true);
      }
    } finally {
      if (mounted) {
        setState(() => _busy = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final observation = _observation;
    final report = observation?.report;
    return Scaffold(
      appBar: AppBar(
        title: const Text(
          '입출금 구간별 성과',
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          IconButton(
            tooltip: '구간별 성과 새로고침',
            onPressed: _busy ? null : _refresh,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            if (_busy)
              Semantics(
                liveRegion: true,
                child: MediaQuery.disableAnimationsOf(context)
                    ? const Text('구간별 성과를 불러오는 중')
                    : const LinearProgressIndicator(
                        semanticsLabel: '구간별 성과를 불러오는 중',
                      ),
              ),
            if (_failed) ...[
              Semantics(
                liveRegion: true,
                child: Text(
                  observation == null
                      ? '구간별 성과를 불러오지 못했습니다.'
                      : '새로고침하지 못했습니다. 이전 보고서와 원래 기준 시점을 유지합니다.',
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
              ),
              FilledButton(
                onPressed: _busy ? null : _refresh,
                child: const Text('구간별 성과 다시 불러오기'),
              ),
            ],
            if (report != null) ...[
              const SizedBox(height: 12),
              Semantics(
                header: true,
                child: Text(
                  'KRW 기준 누적 수익률',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
              ),
              if (report.cumulativeRate != null)
                Text(
                  '약 ${_rateLabel(report.cumulativeRate!)}',
                  key: const Key('checkpoint-terminal-rate'),
                  style: Theme.of(context).textTheme.headlineMedium,
                ),
              Text('보고서 기준: ${report.periodEnd}'),
              Text('기간 시작: ${report.periodStart ?? '미확인'}'),
              const SizedBox(height: 12),
              if (report.ownerDeclaredPrice)
                const Text('직접 입력 가격 사용 · 시세 제공자가 검증한 값이 아닙니다.'),
              if (report.ownerDeclaredFx)
                const Text('직접 입력 환율 사용 · 시세 제공자가 검증한 값이 아닙니다.'),
              if (report.sample)
                const Text('예시 가격·환율 포함 · 실제 투자 성과로 사용하지 마세요.'),
              if (report.status == 'empty')
                const Text('아직 투자 기록이 없습니다. 거래 내역을 가져온 뒤 다시 확인하세요.'),
              for (final issue in report.issues.where(
                (issue) => !const {
                  'sample_data',
                  'owner_declared_fx',
                  'owner_declared_price',
                }.contains(issue.code),
              ))
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(portfolioTwrIssueText(issue.code)),
                ),
              const SizedBox(height: 12),
              const Text(
                '입출금 경계와 조회 기말만 계산합니다. 입금이 한 번이면 시작과 기말 두 점뿐입니다. 일별 경로나 점 사이의 움직임은 계산하지 않습니다.',
              ),
              if (observation != null && observation.points.isNotEmpty) ...[
                const SizedBox(height: 16),
                _CheckpointChart(observation: observation),
                const SizedBox(height: 12),
                OutlinedButton.icon(
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute<void>(
                      builder: (_) =>
                          _CheckpointTable(observation: observation),
                    ),
                  ),
                  icon: const Icon(Icons.table_rows_outlined),
                  label: const Text('같은 계산점 표로 보기'),
                ),
              ],
              const SizedBox(height: 12),
              const Text(
                '시각은 평가 경계이며 시장 가격의 관측 시각이 아닙니다. 입출금 경계점은 같은 시각의 매매·배당·비용을 반영하기 전이고, 기말점은 모두 반영한 뒤입니다.',
              ),
              const SizedBox(height: 8),
              const Text(
                '수익률은 소수 12자리로 반올림된 근사값이며 연환산 값이 아닙니다. 그래프의 점선은 계산점을 잇는 안내선입니다. 부분 경로를 정상 성과로 표시하거나 실시간·증권사 성과로 인증하지 않습니다.',
              ),
            ],
          ],
        ),
      ),
    );
  }
}

String _rateLabel(String rate) {
  if (rate.length > 128) {
    return '큰 양수 · 서버 계산값은 표에서 확인';
  }
  if (rate == '0') {
    return '0% (변화 없음)';
  }
  return '${rate.startsWith('-') ? '' : '+'}${decimalPercent(rate)} (${rate.startsWith('-') ? '하락' : '상승'})';
}

class _CheckpointChart extends StatelessWidget {
  const _CheckpointChart({required this.observation});
  final PortfolioTwrCheckpoints observation;
  @override
  Widget build(BuildContext context) {
    final plot = TwrCheckpointPlot(observation.points);
    final colors = Theme.of(context).colorScheme;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('입출금 경계 ${observation.report.boundaryCount}개 + 기말 1점'),
            Text('최대: ${_rateLabel(plot.maximum)}'),
            Semantics(
              label:
                  '입출금 경계와 기말의 누적 수익률 ${observation.points.length}점. 첫 점 ${_rateLabel(observation.points.first.cumulativeRate)}, 기말 ${_rateLabel(observation.points.last.cumulativeRate)}. 중간 일별 경로는 계산하지 않았습니다. 같은 계산점 표에서 시각과 서버 값을 확인하세요.',
              child: ExcludeSemantics(
                child: RepaintBoundary(
                  child: SizedBox(
                    height: 200,
                    child: CustomPaint(
                      key: const Key('portfolio-twr-checkpoint-chart'),
                      painter: _CheckpointPainter(
                        plot,
                        colors.primary,
                        colors.outlineVariant,
                      ),
                    ),
                  ),
                ),
              ),
            ),
            Text('최소: ${_rateLabel(plot.minimum)}'),
            const Text('왼쪽: 기간 시작 · 오른쪽: 보고서 기준. 테두리 점은 조회 기말입니다.'),
          ],
        ),
      ),
    );
  }
}

class _CheckpointPainter extends CustomPainter {
  _CheckpointPainter(this.plot, this.color, this.border);
  final TwrCheckpointPlot plot;
  final Color color, border;
  @override
  void paint(Canvas canvas, Size size) {
    if (size.width <= 24 || size.height <= 24) {
      return;
    }
    const margin = 12.0;
    Offset position(({double x, double y}) point) => Offset(
      margin + point.x * (size.width - 2 * margin),
      margin + (1 - point.y) * (size.height - 2 * margin),
    );
    final points = plot.points.map(position).toList();
    final axis = Paint()
      ..color = border
      ..strokeWidth = 1;
    if (plot.zeroY != null) {
      final y = margin + (1 - plot.zeroY!) * (size.height - 2 * margin);
      canvas.drawLine(Offset(margin, y), Offset(size.width - margin, y), axis);
    }
    final guide = Paint()
      ..color = color.withValues(alpha: 0.65)
      ..strokeWidth = 1.5;
    for (var index = 1; index < points.length; index++) {
      final delta = points[index] - points[index - 1];
      final length = delta.distance;
      if (length == 0) {
        continue;
      }
      final direction = delta / length;
      for (var offset = 0.0; offset < length; offset += 8) {
        canvas.drawLine(
          points[index - 1] + direction * offset,
          points[index - 1] + direction * math.min(offset + 4, length),
          guide,
        );
      }
    }
    final dot = Paint()..color = color;
    for (final point in points) {
      canvas.drawCircle(point, 3, dot);
    }
    canvas.drawCircle(
      points.last,
      6,
      Paint()
        ..color = color
        ..style = PaintingStyle.stroke
        ..strokeWidth = 2,
    );
  }

  @override
  bool shouldRepaint(covariant _CheckpointPainter oldDelegate) =>
      oldDelegate.plot != plot ||
      oldDelegate.color != color ||
      oldDelegate.border != border;
}

class _CheckpointTable extends StatelessWidget {
  const _CheckpointTable({required this.observation});
  final PortfolioTwrCheckpoints observation;
  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text(
        '구간별 성과 계산점',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
    ),
    body: SafeArea(
      child: ListView.builder(
        padding: const EdgeInsets.all(16),
        itemCount: observation.points.length + 1,
        itemBuilder: (context, index) {
          if (index == 0) {
            return Padding(
              padding: const EdgeInsets.only(bottom: 16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '차트와 같은 보고서 기준: ${observation.report.periodEnd}. 평가 경계 시각과 반올림된 서버 비율을 표시합니다. 0.1은 약 10%입니다.',
                  ),
                  if (observation.report.ownerDeclaredPrice)
                    const Text('직접 입력 가격 사용 · 시세 제공자가 검증한 값이 아닙니다.'),
                  if (observation.report.ownerDeclaredFx)
                    const Text('직접 입력 환율 사용 · 시세 제공자가 검증한 값이 아닙니다.'),
                  if (observation.report.sample)
                    const Text('예시 가격·환율 포함 · 실제 투자 성과로 사용하지 마세요.'),
                ],
              ),
            );
          }
          final point = observation.points[index - 1];
          return Card(
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    point.kind == 'valuation' ? '조회 기말' : '입출금 경계 $index',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  Text('평가 경계: ${point.at}'),
                  Text('약 ${_rateLabel(point.cumulativeRate)}'),
                  if (point.cumulativeRate.length <= 128)
                    SelectableText(
                      '서버 비율: ${point.cumulativeRate}',
                      style: const TextStyle(
                        fontFeatures: [FontFeature.tabularFigures()],
                      ),
                    )
                  else
                    ExpansionTile(
                      title: const Text('큰 서버 계산값 원문 보기'),
                      children: [SelectableText(point.cumulativeRate)],
                    ),
                ],
              ),
            ),
          );
        },
      ),
    ),
  );
}

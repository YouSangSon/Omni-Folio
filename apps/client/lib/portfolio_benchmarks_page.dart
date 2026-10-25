import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart';

class PortfolioBenchmarksPage extends StatefulWidget {
  const PortfolioBenchmarksPage({super.key, required this.api});
  final OmniApi api;
  @override
  State<PortfolioBenchmarksPage> createState() =>
      _PortfolioBenchmarksPageState();
}

class _PortfolioBenchmarksPageState extends State<PortfolioBenchmarksPage> {
  PortfolioBenchmarks? _report;
  bool _busy = false;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _failed = false;
    });
    try {
      final report = await widget.api.portfolioBenchmarks();
      if (mounted) setState(() => _report = report);
    } catch (_) {
      if (mounted) setState(() => _failed = true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text(
        '같은 기간 지수 비교',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: '지수 비교 새로고침',
          onPressed: _busy ? null : _refresh,
          icon: const Icon(Icons.refresh_rounded),
        ),
      ],
    ),
    body: SafeArea(
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(16),
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 640),
            child: _content(context),
          ),
        ),
      ),
    ),
  );

  Widget _content(BuildContext context) {
    final report = _report;
    if (report == null) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Semantics(
            liveRegion: true,
            child: Text(
              _busy ? '같은 기간의 지수 수익률을 확인하고 있습니다' : '지수 비교를 불러오지 못했습니다',
              style: Theme.of(context).textTheme.titleLarge,
            ),
          ),
          const SizedBox(height: 12),
          const Text('저장된 원장과 지수 자료만 조회합니다. 주문을 보내거나 기록을 바꾸지 않습니다.'),
          if (!_busy) ...[
            const SizedBox(height: 16),
            ElevatedButton(onPressed: _refresh, child: const Text('다시 시도')),
          ],
        ],
      );
    }
    final portfolio = report.portfolio;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const Text('로컬 원장 · 직접 보관한 지수 기준'),
        const SizedBox(height: 12),
        if (_failed)
          Semantics(
            liveRegion: true,
            child: Text(
              '새로고침하지 못했습니다. 이전 비교 전체와 기준 시점을 유지합니다.',
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
        if (_busy) const Text('새 결과를 확인하고 있습니다. 기존 비교는 유지됩니다.'),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Semantics(
                  header: true,
                  child: Text(
                    '내 포트폴리오 · KRW',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                const SizedBox(height: 12),
                if (portfolio.cumulativeRate != null)
                  Text(
                    '약 ${decimalPercent(portfolio.cumulativeRate!)}',
                    key: const Key('benchmark-portfolio-rate'),
                    style: Theme.of(context).textTheme.headlineMedium,
                  )
                else
                  Text(
                    portfolio.status == 'empty'
                        ? '아직 투자 기록이 없습니다'
                        : '포트폴리오 TWR을 계산할 수 없습니다',
                  ),
                const SizedBox(height: 12),
                Text('시작 (UTC): ${portfolio.periodStart ?? '미확인'}'),
                Text(
                  '보고서 기준 (UTC): ${portfolio.periodEnd}',
                  key: const Key('benchmark-period-end'),
                ),
                if (portfolio.status != 'calculated')
                  const Padding(
                    padding: EdgeInsets.only(top: 8),
                    child: Text(
                      '포트폴리오 TWR이 계산된 뒤 같은 기간의 지수와 비교합니다. 앞 화면에서 계산 기준과 사유를 확인하세요.',
                    ),
                  ),
              ],
            ),
          ),
        ),
        if (report.sample)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 12),
            child: Text('예시 지수·가격·환율 포함 · 실제 투자 성과로 사용하지 마세요.'),
          ),
        const SizedBox(height: 12),
        const Text('모든 지수는 위와 같은 기간으로 비교합니다. USD 지수는 본래 통화와 KRW 환산 값을 구분합니다.'),
        for (final issue in report.issues)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 16),
            child: Text(
              _listIssue(issue.code),
              key: const Key('benchmark-list-issue'),
            ),
          ),
        for (final row in report.benchmarks) _indexCard(context, row),
        const SizedBox(height: 16),
        const Text(
          '차이는 포트폴리오 TWR에서 KRW 기준 지수 수익률을 뺀 %p입니다. 지수의 배당·세금·비용 기준은 포트폴리오와 다를 수 있습니다.',
        ),
        const SizedBox(height: 8),
        const Text(
          '각 경계 시각 이전의 24시간 이내 관측을 사용합니다. 실제 선택한 관측 시각은 서로 다를 수 있으며, 휴일 값을 보간하지 않습니다.',
        ),
        const SizedBox(height: 8),
        const Text(
          '지수는 직접 보관한 자료이며 공급자 정확성 검증은 별도입니다. 누적 비교이며 연환산·실제 증권사 성과 인증이 아닙니다.',
        ),
      ],
    );
  }

  Widget _indexCard(BuildContext context, PortfolioBenchmark row) {
    final difference = row.differencePercentagePoints;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Semantics(
              header: true,
              child: Text(
                row.series.label,
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ),
            const SizedBox(height: 8),
            Text('${row.series.currency} · ${_basis(row.series.returnBasis)}'),
            const SizedBox(height: 16),
            Text(
              row.series.currency == 'USD' ? '지수 본래 통화 · USD' : '지수 수익률 · KRW',
            ),
            Text(
              _rate(row.native),
              key: Key('benchmark-native-${row.series.id}'),
              style: Theme.of(context).textTheme.titleLarge,
            ),
            if (row.series.currency == 'USD') ...[
              const SizedBox(height: 12),
              const Text('지수 KRW 환산'),
              Text(
                _rate(row.krw),
                key: Key('benchmark-krw-${row.series.id}'),
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ],
            const SizedBox(height: 12),
            const Text('내 포트폴리오와 지수의 차이'),
            Text(
              difference == null
                  ? '계산 불가'
                  : '${difference.startsWith('-') || difference == '0' ? '' : '+'}$difference%p',
              key: Key('benchmark-difference-${row.series.id}'),
              style: Theme.of(context).textTheme.titleLarge,
            ),
            const SizedBox(height: 8),
            ExpansionTile(
              title: Text('${row.series.label} 자료 기준과 관측 시각'),
              expansionAnimationStyle: MediaQuery.disableAnimationsOf(context)
                  ? AnimationStyle.noAnimation
                  : null,
              tilePadding: EdgeInsets.zero,
              children: [
                Align(
                  alignment: Alignment.centerLeft,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('자료 버전: ${row.series.sourceVersion}'),
                      Text('수집 (UTC): ${row.series.fetchedAt}'),
                      Text(
                        '시작 지수 ${row.start?.value ?? '없음'} · 관측 (UTC): ${row.start?.observedAt ?? '없음'}',
                      ),
                      Text(
                        '기말 지수 ${row.end?.value ?? '없음'} · 관측 (UTC): ${row.end?.observedAt ?? '없음'}',
                      ),
                      if (row.series.currency == 'USD') ...[
                        Text(
                          '시작 USD→KRW ${row.startFx?.value ?? '없음'} · 관측 (UTC): ${row.startFx?.observedAt ?? '없음'}',
                        ),
                        Text(
                          '기말 USD→KRW ${row.endFx?.value ?? '없음'} · 관측 (UTC): ${row.endFx?.observedAt ?? '없음'}',
                        ),
                      ],
                      Text('자료 출처: ${row.series.sourceReference}'),
                      const Text('저장 내용의 무결성 확인은 시장 자료의 정확성 인증과 다릅니다.'),
                    ],
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  String _rate(BenchmarkRate result) => result.rate == null
      ? _issue(result.issue!)
      : '약 ${decimalPercent(result.rate!)}';
  String _basis(String basis) => switch (basis) {
    'price_index' => '가격지수 · 배당 제외',
    'gross_total_return' => '배당 재투자 · 세전',
    _ => '배당 재투자 · 세후',
  };
  String _listIssue(String code) => switch (code) {
    'no_benchmarks' => '아직 보관한 비교 지수가 없습니다. 지수 자료를 준비한 뒤 다시 확인하세요.',
    'no_known_benchmarks' => '현재 보고서 기준 시점에 사용할 수 있는 지수 자료가 없습니다.',
    _ => '한 번에 비교할 수 있는 지수 32개를 넘었습니다. 포트폴리오 결과만 표시합니다.',
  };
  String _issue(String code) => switch (code) {
    'portfolio_unavailable' => '같은 기간의 포트폴리오 TWR이 계산되지 않았습니다.',
    'missing_benchmark_point' => '투자 시작 시점에 사용할 지수 관측이 없습니다.',
    'stale_benchmark_point' => '지수 관측이 24시간 기준을 넘었습니다.',
    'zero_benchmark_start' => '시작 지수 값이 0이어서 수익률을 계산할 수 없습니다.',
    'missing_historical_fx' => '시작 또는 기말의 직접 USD→KRW 환율이 없습니다.',
    'stale_historical_fx' => '직접 USD→KRW 환율이 24시간 기준을 넘었습니다.',
    _ => '현재 정밀도와 표시 범위에서 값을 확인할 수 없습니다.',
  };
}

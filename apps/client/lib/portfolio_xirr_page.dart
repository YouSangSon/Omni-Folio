import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart';
import 'portfolio_twr_page.dart';

class PortfolioXirrPage extends StatefulWidget {
  const PortfolioXirrPage({super.key, required this.api});
  final OmniApi api;
  @override
  State<PortfolioXirrPage> createState() => _PortfolioXirrPageState();
}

class _PortfolioXirrPageState extends State<PortfolioXirrPage> {
  PortfolioXirr? _report;
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
      final report = await widget.api.portfolioXirr();
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
        '입출금 반영 수익률',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: '수익률 새로고침',
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
              _busy ? '수익률을 계산하고 있습니다' : '수익률을 불러오지 못했습니다',
              style: Theme.of(context).textTheme.titleLarge,
            ),
          ),
          const SizedBox(height: 12),
          const Text('현재 원장과 가격·환율을 함께 확인합니다. 주문을 보내거나 원장을 변경하지 않습니다.'),
          if (!_busy) ...[
            const SizedBox(height: 16),
            ElevatedButton(onPressed: _refresh, child: const Text('다시 시도')),
          ],
        ],
      );
    }
    final title = switch (report.status) {
      'calculated' => '연환산 수익률 (XIRR)',
      'candidate' => '수익률 하나로 확정할 수 없습니다',
      'empty' => '아직 투자 기록이 없습니다',
      _ => '수익률을 계산할 수 없습니다',
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const Text('로컬 원장 계산 · 현재 증권사 성과 아님'),
        const SizedBox(height: 12),
        OutlinedButton.icon(
          onPressed: () => Navigator.of(context).push<void>(
            MaterialPageRoute(
              builder: (_) => PortfolioTwrPage(api: widget.api),
            ),
          ),
          icon: const Icon(Icons.timeline),
          label: const Text('입출금 영향을 뺀 누적 수익률 보기', textAlign: TextAlign.center),
        ),
        const SizedBox(height: 12),
        if (_failed)
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Semantics(
              liveRegion: true,
              child: Text(
                '새로고침하지 못했습니다. 이전 결과와 기준 시점을 유지합니다.',
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ),
          ),
        if (_busy)
          const Padding(
            padding: EdgeInsets.only(bottom: 12),
            child: Text('새 결과를 확인하고 있습니다. 기존 결과는 유지됩니다.'),
          ),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Semantics(
                  header: true,
                  child: Text(
                    title,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                const SizedBox(height: 12),
                if (report.annualRate != null)
                  Text(
                    '약 ${decimalPercent(report.annualRate!)}',
                    key: const Key('portfolio-xirr-rate'),
                    style: Theme.of(context).textTheme.headlineMedium,
                  ),
                if (report.status == 'candidate')
                  const Text(
                    '입출금 방향이 여러 번 바뀌어 유일한 해인지 보장하지 못했습니다. 계산 후보는 아래 상세에서만 확인하세요.',
                  ),
                if (report.status == 'empty')
                  const Text('거래 내역을 가져온 뒤 다시 확인하세요.'),
                for (final issue in report.issues.where(
                  (issue) =>
                      issue.code != 'sample_data' &&
                      issue.code != 'owner_declared_fx' &&
                      issue.code != 'owner_declared_price' &&
                      issue.code != 'uniqueness_not_proven',
                ))
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: Text(_issue(issue.code)),
                  ),
              ],
            ),
          ),
        ),
        if (report.ownerDeclaredPrice)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 12),
            child: Text('직접 입력 가격 사용 · 시세 제공자가 검증한 값이 아닙니다.'),
          ),
        if (report.ownerDeclaredFx)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 12),
            child: Text('직접 입력 환율 사용 · 시세 제공자가 검증한 값이 아닙니다.'),
          ),
        if (report.sample)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 12),
            child: Text('예시 가격·환율 포함 · 실제 투자 성과로 사용하지 마세요.'),
          ),
        const SizedBox(height: 12),
        Text(
          '기간: ${report.periodStart ?? '미확인'} ~ ${report.periodEnd} (UTC 날짜)',
        ),
        const SizedBox(height: 8),
        if (report.netContributions != null)
          Text('순입금액: ${report.netContributions!.amount} KRW'),
        if (report.valuation.totalValue != null)
          Text('기말 자산: ${report.valuation.totalValue!.amount} KRW'),
        if (report.externalEventCount != null)
          Text('정정 후 외부 입출금 ${report.externalEventCount}건'),
        const SizedBox(height: 12),
        ExpansionTile(
          title: const Text('계산 방법과 기준 보기'),
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (report.candidateRate != null) ...[
                    Text(
                      '참고 후보: 약 ${decimalPercent(report.candidateRate!)}',
                      key: const Key('portfolio-xirr-candidate'),
                    ),
                    const SizedBox(height: 8),
                    const Text(
                      '10% 초기 추측으로 수렴한 후보입니다. 다른 해가 있을 수 있으며 Excel과 같은 후보를 고른다는 보장은 없습니다.',
                    ),
                    const SizedBox(height: 12),
                  ],
                  const Text(
                    '입출금 날짜와 남은 자산을 반영한 연환산 값입니다. UTC 날짜의 실제 일수를 365로 나누며 하루 안의 시각 차이는 계산하지 않습니다.',
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    '입금은 투자금, 출금은 회수금으로 봅니다. 매매·배당·수수료·세금·환전은 기말 자산에 포함합니다. 취소된 원본 입출금은 제외합니다.',
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    '외화 입출금은 발생 시각의 직접 환율로 각각 환산한 뒤 합칩니다. 보고서 시점까지 저장된 과거 관측을 사용하며, 빠진 환율을 현재 환율로 대신하지 않습니다.',
                  ),
                  const SizedBox(height: 12),
                  Text('원장: ${report.valuation.ledgerRevision}'),
                  Text('보고서 기준: ${report.valuation.valuationAsOf}'),
                  const Text('계산 결과는 근삿값입니다. 투자 추천이나 수익 보장이 아닙니다.'),
                ],
              ),
            ),
          ],
        ),
      ],
    );
  }

  String _issue(String code) => switch (code) {
    'valuation_unavailable' => '현재 자산 평가가 완전하지 않습니다. 저장 가격과 환율을 확인하세요.',
    'missing_historical_fx' => '입출금 당시의 직접 환율이 없습니다.',
    'stale_historical_fx' => '입출금 당시 환율이 24시간 기준을 넘었습니다.',
    'insufficient_period' => '서로 다른 UTC 날짜의 자금 흐름이 필요합니다.',
    'one_sided_cash_flows' => '투자금과 회수금 양쪽의 흐름이 필요합니다.',
    'cash_flow_limit' => '계산할 일별 흐름이 10,000개를 넘었습니다.',
    _ => '수익률을 안정적으로 계산하지 못했습니다. 가능한 수익률이 없다는 뜻은 아닙니다.',
  };
}

import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart';

class PortfolioTwrPage extends StatefulWidget {
  const PortfolioTwrPage({super.key, required this.api});
  final OmniApi api;
  @override
  State<PortfolioTwrPage> createState() => _PortfolioTwrPageState();
}

class _PortfolioTwrPageState extends State<PortfolioTwrPage> {
  PortfolioTwr? _report;
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
      final report = await widget.api.portfolioTwr();
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
        '입출금 영향을 뺀 수익률',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: 'TWR 새로고침',
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
    if (report == null)
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Semantics(
            liveRegion: true,
            child: Text(
              _busy ? 'TWR을 계산하고 있습니다' : 'TWR을 불러오지 못했습니다',
              style: Theme.of(context).textTheme.titleLarge,
            ),
          ),
          const SizedBox(height: 12),
          const Text('저장된 원장과 과거 가격·환율만 조회합니다. 주문을 보내거나 원장을 바꾸지 않습니다.'),
          if (!_busy) ...[
            const SizedBox(height: 16),
            ElevatedButton(onPressed: _refresh, child: const Text('다시 시도')),
          ],
        ],
      );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const Text('로컬 원장 계산 · 현재 증권사 성과 아님'),
        const SizedBox(height: 12),
        if (_failed)
          Semantics(
            liveRegion: true,
            child: Text(
              '새로고침하지 못했습니다. 이전 결과와 기준 시점을 유지합니다.',
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
        if (_busy) const Text('새 결과를 확인하고 있습니다. 기존 결과는 유지됩니다.'),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Semantics(
                  header: true,
                  child: Text(switch (report.status) {
                    'calculated' => '누적 수익률 (TWR)',
                    'empty' => '아직 투자 기록이 없습니다',
                    _ => 'TWR을 계산할 수 없습니다',
                  }, style: Theme.of(context).textTheme.titleLarge),
                ),
                const SizedBox(height: 12),
                if (report.cumulativeRate != null)
                  Text(
                    '약 ${decimalPercent(report.cumulativeRate!)}',
                    key: const Key('portfolio-twr-rate'),
                    style: Theme.of(context).textTheme.headlineMedium,
                  ),
                if (report.status == 'empty')
                  const Text('거래 내역을 가져온 뒤 다시 확인하세요.'),
                for (final issue in report.issues.where(
                  (issue) => issue.code != 'sample_data',
                ))
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: Text(_issue(issue.code)),
                  ),
              ],
            ),
          ),
        ),
        if (report.sample)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 12),
            child: Text('예시 가격·환율 포함 · 실제 투자 성과로 사용하지 마세요.'),
          ),
        const SizedBox(height: 12),
        const Text('입출금 사이의 자산 변화를 이어 계산한 누적 값입니다. 연환산 값인 XIRR과 계산 기준이 다릅니다.'),
        const SizedBox(height: 12),
        Text('시작: ${report.periodStart ?? '미확인'}'),
        Text('보고서 기준: ${report.periodEnd}'),
        if (report.netContributions != null)
          Text('순입금액: ${report.netContributions!.amount} KRW'),
        if (report.valuation.totalValue != null)
          Text('기말 자산: ${report.valuation.totalValue!.amount} KRW'),
        if (report.externalEventCount != null)
          Text(
            '정정 후 외부 입출금 ${report.externalEventCount}건 · 평가 경계 ${report.boundaryCount}개',
          ),
        const SizedBox(height: 12),
        ExpansionTile(
          title: const Text('TWR 계산 방법과 기준 보기'),
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text(
                    '100원이 110원이 된 뒤 100원을 더 넣어 210원이 됐다면 수익률은 10%입니다. 입출금을 수익으로 보지 않습니다.',
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    '동일 UTC 시각의 외부 입출금을 먼저 묶고 매매·배당·비용·환전은 그 뒤에 처리합니다. 소수초를 유지하며 날짜 단위로 합치지 않습니다.',
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    '취소된 현금 기록과 정정 행은 과거 잔액에서도 제외합니다. 각 경계의 자산을 발생 시각 이전의 가격·직접 환율로 평가하며, 보고서 시점까지 저장된 과거 관측만 사용합니다. 24시간을 넘거나 빠진 자료를 현재 가격으로 대신하지 않습니다.',
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    '중간 자본이 0 이하가 되거나 분할 당시 가격 단위를 판단할 수 없으면 계산하지 않습니다. 전액 출금은 전액 손실이 아닙니다. 수익률은 마지막에만 소수 12자리로 반올림합니다.',
                  ),
                  const SizedBox(height: 12),
                  Text('원장: ${report.valuation.ledgerRevision}'),
                  const Text(
                    '투자 추천이나 수익 보장이 아닙니다. 일별 평가를 가정하는 다른 서비스와 값이 다를 수 있습니다.',
                  ),
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
    'missing_historical_fx' => '입출금 경계의 직접 환율이 없습니다.',
    'stale_historical_fx' => '입출금 경계의 환율이 24시간 기준을 넘었습니다.',
    'missing_security_price' => '입출금 경계의 보유 자산 가격이 없습니다.',
    'stale_security_price' => '입출금 경계의 보유 자산 가격이 24시간 기준을 넘었습니다.',
    'ambiguous_security_price_venue' => '보유 자산 가격의 거래소를 하나로 정할 수 없습니다.',
    'split_price_unavailable' => '분할 뒤 수량에 맞는 가격이 없습니다.',
    'ambiguous_split_boundary' => '입출금과 분할의 시각이 같아 가격 단위를 판단할 수 없습니다.',
    'capital_path_unavailable' =>
      '투자 시작 전 잔액이 있거나 중간 자본이 0 이하가 되어 이 계산 기준을 적용할 수 없습니다. 전액 출금을 손실로 표시하지 않습니다.',
    'no_external_cash_flows' => '투자 시작을 정할 외부 입출금 기록이 없습니다.',
    'cash_flow_limit' => '입출금 평가 경계가 현재 계산 한도인 1,000개를 넘었습니다.',
    _ => '현재 정밀도와 계산 범위에서 결과를 확인할 수 없습니다.',
  };
}

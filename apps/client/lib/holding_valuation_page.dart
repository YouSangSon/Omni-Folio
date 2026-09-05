import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart';

class HoldingValuationPage extends StatefulWidget {
  const HoldingValuationPage({super.key, required this.api});

  final OmniApi api;

  @override
  State<HoldingValuationPage> createState() => _HoldingValuationPageState();
}

class _HoldingValuationPageState extends State<HoldingValuationPage> {
  HoldingValuation? _valuation;
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
      final valuation = await widget.api.holdingValuation();
      if (mounted) setState(() => _valuation = valuation);
    } catch (_) {
      if (mounted) {
        setState(() {
          _error = _valuation == null
              ? '저장 가격 평가를 불러오지 못했습니다'
              : '새로고침하지 못했습니다. 이전 평가 결과를 유지합니다.';
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
        '저장 가격 평가',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: '저장 가격 평가 새로고침',
          onPressed: _busy ? null : _refresh,
          icon: const Icon(Icons.refresh_rounded),
        ),
        const SizedBox(width: 8),
      ],
    ),
    body: SafeArea(child: _body(context)),
  );

  Widget _body(BuildContext context) {
    final valuation = _valuation;
    if (valuation == null && _busy) {
      return Center(
        child: Semantics(
          liveRegion: true,
          label: '저장 가격 평가 불러오는 중',
          child: const CircularProgressIndicator(),
        ),
      );
    }
    if (valuation == null) {
      return _ValuationMessage(
        icon: Icons.cloud_off_outlined,
        title: _error ?? '저장 가격 평가를 불러오지 못했습니다',
        body: '민감한 오류 정보는 표시하지 않습니다. 서버를 확인한 뒤 다시 시도하세요.',
        action: _busy ? null : _refresh,
      );
    }
    return ListView.builder(
      key: const Key('holding-valuation-list'),
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      itemCount: valuation.lines.length + 1,
      itemBuilder: (context, index) {
        if (index == 0) {
          return _ValuationHeader(
            valuation: valuation,
            busy: _busy,
            error: _error,
          );
        }
        return Padding(
          padding: const EdgeInsets.only(top: 12),
          child: _ValuationLineCard(line: valuation.lines[index - 1]),
        );
      },
    );
  }
}

class _ValuationHeader extends StatelessWidget {
  const _ValuationHeader({
    required this.valuation,
    required this.busy,
    required this.error,
  });

  final HoldingValuation valuation;
  final bool busy;
  final String? error;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      if (busy) ...[
        const LinearProgressIndicator(),
        const SizedBox(height: 12),
      ],
      if (error != null) ...[
        _ValuationNotice(
          icon: Icons.sync_problem_outlined,
          text: error!,
          color: Theme.of(context).colorScheme.error,
        ),
        const SizedBox(height: 12),
      ],
      _ValuationNotice(
        icon: Icons.info_outline,
        text: valuation.sample
            ? '샘플 저장 가격 · 실시간/현재 시세 아님'
            : '저장된 평가 증거 · 실시간/현재 시세 아님',
        color: Theme.of(context).colorScheme.tertiary,
      ),
      if (valuation.status == 'empty') ...[
        const SizedBox(height: 12),
        _ValuationNotice(
          icon: Icons.inbox_outlined,
          text: '평가할 보유 종목이 없습니다. 적용된 거래가 없거나 모든 종목을 매도했습니다.',
          color: Theme.of(context).colorScheme.primary,
        ),
      ],
      if (valuation.status == 'unavailable') ...[
        const SizedBox(height: 12),
        _ValuationNotice(
          icon: Icons.warning_amber_rounded,
          text: '일부 종목을 평가할 수 없어 합계를 표시하지 않습니다.',
          color: Theme.of(context).colorScheme.error,
        ),
      ],
      const SizedBox(height: 12),
      Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('평가 근거', style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              Text('평가 기준 ${_readableTime(valuation.valuationAsOf)}'),
              const SizedBox(height: 4),
              const Text('범위 · 보유 종목만 (현금 제외 · 전체 계좌 평가 아님)'),
              const SizedBox(height: 4),
              Text(
                '가격 정책 · 관측 후 최대 ${valuation.maxObservationAgeSeconds ~/ 3600}시간',
              ),
              const SizedBox(height: 4),
              Text(
                '원장 ${valuation.ledgerRevision} · 기록 ${_readableTime(valuation.ledgerRecordedAt)}',
              ),
            ],
          ),
        ),
      ),
      if (valuation.totals.isNotEmpty) ...[
        const SizedBox(height: 20),
        Semantics(
          header: true,
          child: Text('통화별 합계', style: Theme.of(context).textTheme.titleMedium),
        ),
        const SizedBox(height: 8),
        for (final total in valuation.totals) ...[
          _ValuationTotalCard(total: total),
          const SizedBox(height: 8),
        ],
      ],
      if (valuation.lines.isNotEmpty) ...[
        const SizedBox(height: 12),
        Semantics(
          header: true,
          child: Text('종목별 평가', style: Theme.of(context).textTheme.titleMedium),
        ),
      ],
    ],
  );
}

class _ValuationTotalCard extends StatelessWidget {
  const _ValuationTotalCard({required this.total});

  final HoldingValuationTotal total;

  @override
  Widget build(BuildContext context) => Semantics(
    container: true,
    label:
        '${total.currency} 합계, 원가 ${total.currency} ${total.costBasis}, '
        '평가금액 ${total.currency} ${total.marketValue}, '
        '${_pnlText(total.unrealizedPnl, total.currency)}',
    child: ExcludeSemantics(
      child: Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                total.currency,
                style: Theme.of(context).textTheme.titleMedium,
              ),
              const SizedBox(height: 8),
              Text('원가 ${total.currency} ${total.costBasis}'),
              const SizedBox(height: 4),
              Text('평가금액 ${total.currency} ${total.marketValue}'),
              const SizedBox(height: 4),
              Text(
                _pnlText(total.unrealizedPnl, total.currency),
                style: TextStyle(
                  color: _pnlColor(context, total.unrealizedPnl),
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _ValuationLineCard extends StatelessWidget {
  const _ValuationLineCard({required this.line});

  final HoldingValuationLine line;

  @override
  Widget build(BuildContext context) {
    final valueText = line.marketValue == null
        ? '평가금액 없음'
        : '평가금액 ${line.currency} ${line.marketValue}';
    final pnlText = line.unrealizedPnl == null
        ? '미실현 손익 없음'
        : _pnlText(line.unrealizedPnl!, line.currency);
    final issueText = _issueText(line.status);
    final price = line.price;
    final semantics = [
      line.symbol,
      '수량 ${line.quantity}',
      '원가 ${line.currency} ${line.costBasis}',
      valueText,
      pnlText,
      ?issueText,
      if (price != null)
        '저장 가격 ${price.currency} ${price.price}, 원천 ${price.source}, '
            '거래소 ${price.venue}, 관측 ${_readableTime(price.observedAt)}, '
            '가져옴 ${_readableTime(price.fetchedAt)}, 저장 ${_readableTime(price.recordedAt)}, '
            '샘플이며 오래된 상태',
    ].join(', ');
    return Semantics(
      container: true,
      label: semantics,
      child: ExcludeSemantics(
        child: Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  line.symbol,
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const SizedBox(height: 8),
                Text('수량 ${line.quantity}'),
                const SizedBox(height: 4),
                Text('원가 ${line.currency} ${line.costBasis}'),
                const SizedBox(height: 4),
                Text(valueText),
                const SizedBox(height: 4),
                Text(
                  pnlText,
                  style: TextStyle(
                    color: line.unrealizedPnl == null
                        ? Theme.of(context).colorScheme.onSurface
                        : _pnlColor(context, line.unrealizedPnl!),
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (issueText != null) ...[
                  const SizedBox(height: 8),
                  Text(
                    issueText,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ],
                if (price != null) ...[
                  const SizedBox(height: 12),
                  const Divider(height: 1),
                  const SizedBox(height: 12),
                  Text('저장 가격 ${price.currency} ${price.price}'),
                  const SizedBox(height: 4),
                  Text(
                    '${price.source} · ${price.venue} · ${price.priceAdjustment}',
                  ),
                  const SizedBox(height: 4),
                  Text('관측 ${_readableTime(price.observedAt)}'),
                  const SizedBox(height: 4),
                  Text('가져옴 ${_readableTime(price.fetchedAt)}'),
                  const SizedBox(height: 4),
                  Text('저장 ${_readableTime(price.recordedAt)}'),
                  const SizedBox(height: 4),
                  Text(
                    '샘플 · 오래된 가격 · 현재 시세 아님',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _ValuationNotice extends StatelessWidget {
  const _ValuationNotice({
    required this.icon,
    required this.text,
    required this.color,
  });

  final IconData icon;
  final String text;
  final Color color;

  @override
  Widget build(BuildContext context) => Semantics(
    container: true,
    liveRegion: true,
    label: text,
    child: ExcludeSemantics(
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: color.withValues(alpha: 0.10),
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: color.withValues(alpha: 0.45)),
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
    ),
  );
}

class _ValuationMessage extends StatelessWidget {
  const _ValuationMessage({
    required this.icon,
    required this.title,
    required this.body,
    required this.action,
  });

  final IconData icon;
  final String title;
  final String body;
  final VoidCallback? action;

  @override
  Widget build(BuildContext context) => ListView(
    padding: const EdgeInsets.all(24),
    children: [
      const SizedBox(height: 48),
      Icon(icon, size: 48, color: Theme.of(context).colorScheme.primary),
      const SizedBox(height: 16),
      Text(
        title,
        textAlign: TextAlign.center,
        style: Theme.of(context).textTheme.titleMedium,
      ),
      const SizedBox(height: 8),
      Text(body, textAlign: TextAlign.center),
      const SizedBox(height: 20),
      ElevatedButton(onPressed: action, child: const Text('다시 시도')),
    ],
  );
}

String _pnlText(String amount, String currency) {
  if (amount == '0') return '미실현 변동 없음 $currency 0';
  return amount.startsWith('-')
      ? '미실현 손실 $currency $amount'
      : '미실현 이익 $currency +$amount';
}

Color _pnlColor(BuildContext context, String amount) {
  if (amount == '0') return Theme.of(context).colorScheme.onSurface;
  if (amount.startsWith('-')) return Theme.of(context).colorScheme.error;
  return Theme.of(context).brightness == Brightness.dark
      ? const Color(0xFF34D399)
      : const Color(0xFF047857);
}

String? _issueText(String status) => switch (status) {
  'missing' => '저장된 적격 가격이 없습니다.',
  'ambiguous' => '가격의 거래소가 하나로 결정되지 않았습니다.',
  'stale' => '저장 가격이 최대 24시간 정책을 초과했습니다.',
  _ => null,
};

String _readableTime(String value) =>
    value.replaceFirst('T', ' ').replaceFirst('Z', ' UTC');

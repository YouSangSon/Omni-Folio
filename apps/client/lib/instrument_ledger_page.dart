import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart';

class InstrumentLedgerPage extends StatefulWidget {
  const InstrumentLedgerPage({
    super.key,
    required this.api,
    required this.holding,
  });
  final OmniApi api;
  final Holding holding;

  @override
  State<InstrumentLedgerPage> createState() => _InstrumentLedgerPageState();
}

class _InstrumentLedgerPageState extends State<InstrumentLedgerPage> {
  InstrumentLedger? _view;
  var _busy = false;
  var _failed = false;

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
      final view = await widget.api.instrumentLedger(
        widget.holding.instrumentId,
        widget.holding.currency,
      );
      if (view.instrumentId != widget.holding.instrumentId ||
          view.currency != widget.holding.currency) {
        throw const FormatException(
          'Instrument ledger does not match selection',
        );
      }
      if (mounted) setState(() => _view = view);
    } catch (_) {
      if (mounted) setState(() => _failed = true);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text(
        '${widget.holding.symbol} 원장',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: '종목 원장 새로고침',
          onPressed: _busy ? null : _refresh,
          icon: const Icon(Icons.refresh_rounded),
        ),
      ],
    ),
    body: SafeArea(child: _body(context)),
  );

  Widget _body(BuildContext context) {
    final view = _view;
    if (view == null && _busy) {
      return Center(
        child: Semantics(
          label: '종목 원장 불러오는 중',
          liveRegion: true,
          child: const CircularProgressIndicator(),
        ),
      );
    }
    if (view == null) {
      return ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const Text('종목 원장을 불러오지 못했습니다. 서버 연결과 가져온 매매 기록을 확인하세요.'),
          const SizedBox(height: 16),
          FilledButton(onPressed: _refresh, child: const Text('다시 시도')),
        ],
      );
    }
    return ListView.builder(
      key: const Key('instrument-ledger-list'),
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      itemCount: view.trades.length + 1,
      itemBuilder: (context, index) {
        if (index == 0) return _header(context, view);
        final trade = view.trades[index - 1];
        final text =
            '${trade.type == 'BUY' ? '매수' : '매도'} · ${trade.quantity}주\n'
            '거래 가격 ${view.currency} ${trade.price}\n수수료 ${view.currency} ${trade.fee}\n'
            '현금 변동 ${view.currency} ${trade.amount}\n발생 시각 (UTC) ${trade.occurredAt}';
        return Padding(
          padding: const EdgeInsets.only(top: 12),
          child: Semantics(
            container: true,
            label: text,
            excludeSemantics: true,
            child: Card(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Text(text, style: _numbers(context)),
              ),
            ),
          ),
        );
      },
    );
  }

  Widget _header(BuildContext context, InstrumentLedger view) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      if (_busy) const LinearProgressIndicator(),
      if (_failed)
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 12),
          child: Semantics(
            liveRegion: true,
            child: Text(
              '새로고침하지 못했습니다. 이전 원장 결과와 기준시점을 유지합니다.',
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
        ),
      const Text('로컬 원장 · 증권사 확인 체결 아님'),
      const SizedBox(height: 12),
      Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Semantics(
                container: true,
                header: true,
                child: Text(
                  '현재 잔여 평균원가',
                  style: Theme.of(context).textTheme.titleMedium,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                view.averageCost == null
                    ? '현재 보유 수량 없음'
                    : '약 ${view.currency} ${view.averageCost}',
                style: _numbers(
                  context,
                ).copyWith(fontSize: 24, fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 8),
              Text(
                '수량 ${view.quantity}주\n남은 원가 ${view.currency} ${view.costBasis}',
                style: _numbers(context),
              ),
              const SizedBox(height: 8),
              const Text(
                '가져온 모든 계좌 합산 · 먼저 산 수량부터 매도한 뒤 남은 원가입니다. 매수 수수료와 원장의 분할을 반영합니다. 평균은 표시를 위해 반올림한 값입니다.',
              ),
            ],
          ),
        ),
      ),
      const SizedBox(height: 12),
      Semantics(
        container: true,
        explicitChildNodes: true,
        child: ExpansionTile(
          internalAddSemanticForOnTap: true,
          title: const Text('원장 기준과 표시 방법'),
          childrenPadding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
          expandedCrossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '원장 ${view.ledgerRevision}\n원장 마지막 발생 시각 (UTC) ${view.ledgerAsOf}\n마지막 추가 기록의 저장 시각 (UTC) ${view.ledgerRecordedAt}',
              style: _numbers(context),
            ),
            const SizedBox(height: 8),
            const Text(
              '평균은 소수 8자리까지 반올림하며, 정확히 중간이면 마지막 자리를 짝수로 맞춥니다. 이 표시값으로 원가를 다시 계산하지 않습니다.\n\n시세 차트와는 별도 조회입니다. 원본 매매 가격과 현재 평균원가는 분할 기준이 다를 수 있어 차트에 겹쳐 표시하지 않습니다.',
            ),
          ],
        ),
      ),
      const SizedBox(height: 16),
      Semantics(
        container: true,
        header: true,
        child: Text(
          '최근 매수·매도 ${view.trades.length}건',
          style: Theme.of(context).textTheme.titleMedium,
        ),
      ),
      const SizedBox(height: 8),
      const Text('기록 당시 원본 값 · 분할·배당 등은 이 목록에서 제외됩니다.'),
      if (view.hasMore)
        const Padding(
          padding: EdgeInsets.only(top: 8),
          child: Text(
            '최근 50건만 표시합니다. 이전 매매는 생략되어 있으며 전체 기록은 내역 탭에서 확인할 수 있습니다.',
          ),
        ),
    ],
  );

  TextStyle _numbers(BuildContext context) => Theme.of(context)
      .textTheme
      .bodyMedium!
      .copyWith(fontFeatures: const [FontFeature.tabularFigures()]);
}

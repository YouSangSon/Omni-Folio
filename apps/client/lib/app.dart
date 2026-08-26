import 'package:flutter/material.dart';

import 'api.dart';
import 'models.dart';

class OmniFolioApp extends StatefulWidget {
  const OmniFolioApp({super.key, required this.api});
  final OmniApi api;

  @override
  State<OmniFolioApp> createState() => _OmniFolioAppState();
}

class _OmniFolioAppState extends State<OmniFolioApp> {
  late final PortfolioController _portfolio = PortfolioController(widget.api)
    ..refresh();
  var _tab = 0;

  @override
  void dispose() {
    _portfolio.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'Omni Folio',
    theme: _theme(Brightness.light),
    darkTheme: _theme(Brightness.dark),
    themeMode: ThemeMode.system,
    home: AnimatedBuilder(
      animation: _portfolio,
      builder: (context, _) {
        final pages = [
          OverviewPage(
            controller: _portfolio,
            onImport: () => setState(() => _tab = 2),
            onConnections: () => setState(() => _tab = 3),
          ),
          HoldingsPage(controller: _portfolio),
          ActivityPage(api: widget.api),
          DataPage(api: widget.api, controller: _portfolio),
        ];
        return Scaffold(
          appBar: AppBar(
            title: Text(const ['홈', '보유', '내역', '연결'][_tab]),
            actions: [
              IconButton(
                tooltip: '데이터 새로고침',
                onPressed: _portfolio.busy ? null : _portfolio.refresh,
                icon: const Icon(Icons.refresh),
              ),
            ],
          ),
          body: SafeArea(child: pages[_tab]),
          bottomNavigationBar: NavigationBar(
            animationDuration: MediaQuery.disableAnimationsOf(context)
                ? Duration.zero
                : null,
            selectedIndex: _tab,
            onDestinationSelected: (index) => setState(() => _tab = index),
            destinations: const [
              NavigationDestination(
                icon: Icon(Icons.home_outlined),
                selectedIcon: Icon(Icons.home_rounded),
                label: '홈',
              ),
              NavigationDestination(
                icon: Icon(Icons.account_balance_wallet_outlined),
                selectedIcon: Icon(Icons.account_balance_wallet),
                label: '보유',
              ),
              NavigationDestination(
                icon: Icon(Icons.receipt_long_outlined),
                selectedIcon: Icon(Icons.receipt_long),
                label: '내역',
              ),
              NavigationDestination(
                icon: Icon(Icons.link_outlined),
                selectedIcon: Icon(Icons.link),
                label: '연결',
              ),
            ],
          ),
        );
      },
    ),
  );
}

ThemeData _theme(Brightness brightness) {
  final dark = brightness == Brightness.dark;
  final canvas = Color(dark ? 0xFF17171C : 0xFFF2F4F6);
  final raised = Color(dark ? 0xFF202027 : 0xFFFFFFFF);
  final primary = Color(dark ? 0xFFF2F4F6 : 0xFF191F28);
  final secondary = Color(dark ? 0xFFB0B8C1 : 0xFF6B7684);
  final action = Color(dark ? 0xFF60A5FA : 0xFF2563EB);
  return ThemeData(
    useMaterial3: true,
    brightness: brightness,
    scaffoldBackgroundColor: canvas,
    colorScheme: ColorScheme(
      brightness: brightness,
      primary: action,
      onPrimary: dark ? const Color(0xFF0F172A) : Colors.white,
      secondary: action,
      onSecondary: dark ? const Color(0xFF0F172A) : Colors.white,
      error: dark ? const Color(0xFFFCA5A5) : const Color(0xFFB91C1C),
      onError: dark ? const Color(0xFF0F172A) : Colors.white,
      surface: raised,
      onSurface: primary,
    ),
    appBarTheme: AppBarTheme(
      backgroundColor: canvas,
      foregroundColor: primary,
      surfaceTintColor: Colors.transparent,
      centerTitle: false,
      elevation: 0,
      titleSpacing: 20,
      titleTextStyle: TextStyle(
        color: primary,
        fontSize: 22,
        fontWeight: FontWeight.w700,
      ),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: raised,
      elevation: 0,
      indicatorColor: action.withValues(alpha: 0.12),
      iconTheme: WidgetStateProperty.resolveWith(
        (states) => IconThemeData(
          color: states.contains(WidgetState.selected) ? action : secondary,
        ),
      ),
      labelTextStyle: WidgetStateProperty.resolveWith(
        (states) => TextStyle(
          color: states.contains(WidgetState.selected) ? action : secondary,
          fontSize: 12,
          fontWeight: states.contains(WidgetState.selected)
              ? FontWeight.w700
              : FontWeight.w500,
        ),
      ),
    ),
    textTheme: TextTheme(
      bodyLarge: TextStyle(fontSize: 16, color: primary),
      bodyMedium: TextStyle(fontSize: 16, color: primary),
      bodySmall: TextStyle(fontSize: 13, color: secondary),
      titleMedium: TextStyle(
        fontSize: 20,
        fontWeight: FontWeight.w700,
        color: primary,
      ),
      headlineMedium: TextStyle(
        fontSize: 32,
        fontWeight: FontWeight.w700,
        color: primary,
        fontFeatures: const [FontFeature.tabularFigures()],
      ),
    ),
    cardTheme: CardThemeData(
      color: raised,
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide.none,
      ),
    ),
    elevatedButtonTheme: ElevatedButtonThemeData(
      style: ElevatedButton.styleFrom(
        minimumSize: const Size(double.infinity, 52),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
        textStyle: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(double.infinity, 52),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
        textStyle: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
      ),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: raised,
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(14),
        borderSide: BorderSide.none,
      ),
      contentPadding: EdgeInsets.all(16),
    ),
  );
}

class PortfolioController extends ChangeNotifier {
  PortfolioController(this.api);
  final OmniApi api;
  ServiceStatus? status;
  PortfolioSnapshot? snapshot;
  BrokerReconciliation? reconciliation;
  String? error;
  var reconciliationFailed = false;
  var busy = false;
  var reconciliationBusy = false;
  var _disposed = false;

  Future<void> refresh() async {
    if (busy) return;
    busy = true;
    error = null;
    _notify();
    final reconciliationRefresh = reconciliationBusy
        ? null
        : refreshReconciliation();
    final results = await Future.wait([
      _settle(api.status()),
      _settle(api.snapshot()),
    ]);
    final statusResult = results[0] as _Result<ServiceStatus>;
    final snapshotResult = results[1] as _Result<PortfolioSnapshot>;
    if (statusResult.value != null) status = statusResult.value;
    if (snapshotResult.value != null) snapshot = snapshotResult.value;
    error = statusResult.error ?? snapshotResult.error;
    busy = false;
    _notify();
    await reconciliationRefresh;
  }

  Future<void> refreshReconciliation() async {
    if (reconciliationBusy) return;
    reconciliationBusy = true;
    reconciliationFailed = false;
    _notify();
    try {
      reconciliation = await api.latestBrokerReconciliation();
    } catch (_) {
      reconciliationFailed = true;
    } finally {
      reconciliationBusy = false;
      _notify();
    }
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }

  DataState get state {
    if (busy && status == null && snapshot == null) return DataState.loading;
    if (error != null && status == null && snapshot == null) {
      return DataState.error;
    }
    if (error != null ||
        status?.trustState == 'partial' ||
        status?.trustState == 'error') {
      return DataState.partial;
    }
    if (status?.trustState == 'never_verified') {
      return DataState.neverVerified;
    }
    if (status?.trustState == 'stale') return DataState.stale;
    if (status == null && snapshot == null) return DataState.empty;
    return DataState.success;
  }
}

enum DataState { loading, empty, error, partial, neverVerified, stale, success }

class _Result<T> {
  const _Result.value(this.value) : error = null;
  const _Result.error(this.error) : value = null;
  final T? value;
  final String? error;
}

Future<_Result<T>> _settle<T>(Future<T> future) async {
  try {
    return _Result.value(await future);
  } catch (error) {
    return _Result.error(error.toString());
  }
}

class OverviewPage extends StatelessWidget {
  const OverviewPage({
    super.key,
    required this.controller,
    required this.onImport,
    required this.onConnections,
  });
  final PortfolioController controller;
  final VoidCallback onImport;
  final VoidCallback onConnections;

  @override
  Widget build(BuildContext context) {
    if (controller.state == DataState.loading) return const _Loading();
    if (controller.state == DataState.error) {
      return _Message(
        icon: Icons.cloud_off,
        title: '스냅샷을 불러오지 못했습니다',
        body:
            '${controller.error ?? '알 수 없는 오류'}\n저장된 데이터는 없으며 원장은 변경되지 않았습니다.',
        action: controller.refresh,
        actionLabel: '다시 시도',
      );
    }
    final snapshot = controller.snapshot;
    if (snapshot == null) {
      return _Message(
        icon: Icons.inbox_outlined,
        title: '아직 스냅샷이 없습니다',
        body: '거래 내역을 미리 확인한 뒤 적용하면 현금과 보유 내역이 표시됩니다.',
        action: onImport,
        actionLabel: '거래 내역 가져오기',
      );
    }
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _TrustBanner(
          state: controller.state,
          status: controller.status,
          retainedError: controller.error,
          reconciliation: controller.reconciliation,
          reconciliationFailed: controller.reconciliationFailed,
          reconciliationBusy: controller.reconciliationBusy,
        ),
        const SizedBox(height: 12),
        _SectionCard(
          title: '현금 잔액',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                _moneySummary(snapshot.cash),
                style: Theme.of(context).textTheme.headlineMedium,
                semanticsLabel: '현금 ${_moneySummary(snapshot.cash)}',
              ),
              const SizedBox(height: 4),
              Text(
                '마지막 기록 ${_time(snapshot.recordedAt)}',
                style: Theme.of(context).textTheme.bodySmall,
              ),
              const Divider(height: 24),
              Text(
                '보유 ${snapshot.holdings.length}종목',
                style: _tabular(context),
              ),
              const SizedBox(height: 8),
              Text(
                '실현 손익 ${_moneySummary(snapshot.realizedPnl)}',
                style: _gainStyle(context, snapshot.realizedPnl),
              ),
            ],
          ),
        ),
        const SizedBox(height: 12),
        _OverviewReconciliationCard(
          reconciliation: controller.reconciliation,
          retainedError: controller.reconciliationFailed,
          busy: controller.reconciliationBusy,
          onDetails: onConnections,
        ),
        const SizedBox(height: 12),
        const _SectionCard(
          title: '복구 상태',
          child: Text(
            '아직 확인된 백업 정보가 없습니다. 복구하기 전에는 서버가 만든 백업 확인 내역을 먼저 확인하세요.',
          ),
        ),
      ],
    );
  }
}

class HoldingsPage extends StatelessWidget {
  const HoldingsPage({super.key, required this.controller});
  final PortfolioController controller;

  @override
  Widget build(BuildContext context) {
    final holdings = controller.snapshot?.holdings;
    if (holdings == null) {
      return const _Message(
        icon: Icons.account_balance_wallet_outlined,
        title: '보유 데이터 없음',
        body: '홈에서 데이터를 새로고침하세요.',
      );
    }
    if (holdings.isEmpty) {
      return const _Message(
        icon: Icons.inbox_outlined,
        title: '보유 종목 없음',
        body: '적용된 거래가 없거나 모두 매도되었습니다.',
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.all(16),
      itemCount: holdings.length,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, index) {
        final holding = holdings[index];
        return Card(
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            onTap: () => Navigator.of(context).push(
              MaterialPageRoute<void>(
                builder: (_) =>
                    AssetDetailPage(api: controller.api, holding: holding),
              ),
            ),
            child: ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 48),
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Row(
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            holding.symbol,
                            style: Theme.of(context).textTheme.titleMedium,
                          ),
                          const SizedBox(height: 8),
                          Text(
                            '수량 ${holding.quantity} · 원가 ${holding.currency} ${holding.costBasis}',
                            style: _tabular(context),
                          ),
                        ],
                      ),
                    ),
                    const Icon(Icons.chevron_right),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}

class AssetDetailPage extends StatefulWidget {
  const AssetDetailPage({super.key, required this.api, required this.holding});
  final OmniApi api;
  final Holding holding;

  @override
  State<AssetDetailPage> createState() => _AssetDetailPageState();
}

class _AssetDetailPageState extends State<AssetDetailPage> {
  MarketCandles? _candles;
  String? _error;
  var _busy = true;
  var _tableVisible = false;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    if (_busy && _candles != null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final candles = await widget.api.candles(widget.holding.symbol);
      if (mounted) setState(() => _candles = candles);
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text('${widget.holding.symbol} 시세'),
      actions: [
        IconButton(
          tooltip: '차트 새로고침',
          onPressed: _busy ? null : _refresh,
          icon: const Icon(Icons.refresh),
        ),
      ],
    ),
    body: SafeArea(child: _body(context)),
  );

  Widget _body(BuildContext context) {
    if (_busy && _candles == null) return const _Loading();
    if (_candles == null) {
      return _Message(
        icon: Icons.cloud_off,
        title: '시세를 불러오지 못했습니다',
        body: '${_error ?? '알 수 없는 오류'}\n원천/원천 기준/가져온 시각: 확인할 수 없음',
        action: _refresh,
        actionLabel: '다시 시도',
      );
    }
    final candles = _candles!;
    return ListView(
      key: const Key('asset-detail-scroll'),
      padding: const EdgeInsets.all(16),
      children: [
        if (candles.sample) ...[
          Semantics(
            container: true,
            liveRegion: true,
            child: _Notice(
              icon: Icons.science_outlined,
              text: '샘플 데이터 · 실시간 아님',
              color: _warningColor(context),
            ),
          ),
          const SizedBox(height: 12),
        ],
        _SectionCard(
          title: '${candles.symbol} · ${candles.interval}',
          child: _MarketMetadata(candles: candles),
        ),
        const SizedBox(height: 12),
        if (_error != null) ...[
          Semantics(
            key: const Key('market-refresh-error'),
            container: true,
            excludeSemantics: true,
            liveRegion: true,
            label: '새로고침 실패: $_error. 마지막 정상 시세는 유지됩니다.',
            child: _Notice(
              icon: Icons.error_outline,
              text: '새로고침 실패: $_error\n마지막 정상 시세는 유지됩니다.',
              color: Theme.of(context).colorScheme.error,
            ),
          ),
          const SizedBox(height: 12),
        ],
        if (candles.state == 'empty')
          const _Message(
            icon: Icons.show_chart,
            title: '표시할 봉이 없습니다',
            body: '선택한 종목의 일봉 데이터가 아직 없습니다.',
          )
        else ...[
          if (candles.state == 'stale' || candles.state == 'partial') ...[
            _Notice(
              icon: candles.state == 'stale'
                  ? Icons.schedule
                  : Icons.warning_amber_rounded,
              text: candles.state == 'stale'
                  ? '오래된 시세입니다. 투자 판단 전 원천과 시각을 확인하세요.'
                  : '일부 시세입니다. 누락 또는 경고를 확인하세요.',
              color: _warningColor(context),
            ),
            const SizedBox(height: 12),
          ],
          if (candles.issues.isNotEmpty) ...[
            _Notice(
              icon: Icons.info_outline,
              text: candles.issues
                  .map((issue) => '${issue.code}: ${issue.message}')
                  .join('\n'),
              color: _warningColor(context),
            ),
            const SizedBox(height: 12),
          ],
          _CandleChart(candles: candles),
          const SizedBox(height: 12),
          OutlinedButton.icon(
            onPressed: () => setState(() => _tableVisible = !_tableVisible),
            icon: Icon(_tableVisible ? Icons.expand_less : Icons.table_rows),
            label: Text(_tableVisible ? 'OHLCV 표 닫기' : '정확한 OHLCV 표 보기'),
          ),
          if (_tableVisible) ...[
            const SizedBox(height: 12),
            _OhlcvTable(candles: candles),
          ],
        ],
      ],
    );
  }
}

class _MarketMetadata extends StatelessWidget {
  const _MarketMetadata({required this.candles});
  final MarketCandles candles;

  @override
  Widget build(BuildContext context) {
    final priceAdjustment = switch (candles.priceAdjustment) {
      'provider_adjusted' => '공급자 조정 가격 · 조정 정확성 미검증',
      _ => '조정 여부 확인 안 됨',
    };
    return Text(
      '거래소 ${candles.venue}\n시간대 ${candles.timezone}\n가격 기준 $priceAdjustment\n원천 ${candles.source}\n원천 기준 ${candles.sourceAsOf ?? '없음'}\n가져온 시각 ${candles.fetchedAt}\n상태 ${candles.state}',
      style: _tabular(context),
    );
  }
}

class _CandleChart extends StatelessWidget {
  const _CandleChart({required this.candles});
  final MarketCandles candles;

  @override
  Widget build(BuildContext context) {
    final summary =
        '${candles.symbol} ${candles.interval} 캔들 차트. '
        '${candles.bars.length}개 봉, 상태 ${candles.state}. '
        '시가 ${candles.bars.first.open}, 종가 ${candles.bars.last.close}. '
        '정확한 OHLCV 표 보기 버튼으로 모든 값을 확인할 수 있습니다.';
    return _SectionCard(
      title: '시세 차트',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Semantics(
            container: true,
            label: summary,
            child: ExcludeSemantics(
              child: LayoutBuilder(
                builder: (context, constraints) {
                  final minimumWidth = candles.bars.length * 4.0;
                  final chartWidth = minimumWidth > constraints.maxWidth
                      ? minimumWidth
                      : constraints.maxWidth;
                  return SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: SizedBox(
                      height: 260,
                      width: chartWidth,
                      child: RepaintBoundary(
                        child: CustomPaint(
                          painter: _CandlePainter(
                            bars: candles.bars,
                            bull: _positiveColor(context),
                            bear: Theme.of(context).colorScheme.error,
                            axis: Theme.of(
                              context,
                            ).colorScheme.onSurface.withValues(alpha: 0.35),
                          ),
                        ),
                      ),
                    ),
                  );
                },
              ),
            ),
          ),
          const SizedBox(height: 12),
          Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _ChartLegend(
                color: _positiveColor(context),
                label: '상승 · 채운 몸통',
                filled: true,
              ),
              _ChartLegend(
                color: Theme.of(context).colorScheme.error,
                label: '하락 · 빈 몸통',
                filled: false,
              ),
              const _ChartLegend(
                color: Colors.grey,
                label: '아래 막대 · 거래량',
                filled: true,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _ChartLegend extends StatelessWidget {
  const _ChartLegend({
    required this.color,
    required this.label,
    required this.filled,
  });
  final Color color;
  final String label;
  final bool filled;
  @override
  Widget build(BuildContext context) => Row(
    children: [
      Container(
        width: 14,
        height: 14,
        decoration: BoxDecoration(
          color: filled ? color : Colors.transparent,
          border: Border.all(color: color),
        ),
      ),
      const SizedBox(width: 6),
      Expanded(child: Text(label)),
    ],
  );
}

class _CandlePainter extends CustomPainter {
  const _CandlePainter({
    required this.bars,
    required this.bull,
    required this.bear,
    required this.axis,
  });
  final List<MarketBar> bars;
  final Color bull;
  final Color bear;
  final Color axis;

  @override
  void paint(Canvas canvas, Size size) {
    const padding = 10.0;
    final priceHeight = size.height * 0.72;
    double geometry(String value) {
      final parsed = double.tryParse(value);
      return parsed != null && parsed.isFinite ? parsed : 0;
    }

    final values = bars
        .expand((bar) => [bar.low, bar.high])
        .map(geometry)
        .toList();
    final min = values.reduce((a, b) => a < b ? a : b);
    final max = values.reduce((a, b) => a > b ? a : b);
    final flat = max == min;
    final range = flat ? 1.0 : max - min;
    final maxVolume = bars
        .map((bar) => geometry(bar.volume))
        .reduce((a, b) => a > b ? a : b);
    final spacing = (size.width - padding * 2) / bars.length;
    final width = (spacing * 0.62).clamp(2.0, 16.0);
    final axisPaint = Paint()
      ..color = axis
      ..strokeWidth = 1;
    canvas.drawLine(
      Offset(padding, priceHeight),
      Offset(size.width - padding, priceHeight),
      axisPaint,
    );
    for (var index = 0; index < bars.length; index++) {
      final bar = bars[index];
      final x = padding + spacing * (index + 0.5);
      double y(String value) => flat
          ? padding + (priceHeight - padding * 2) / 2
          : padding +
                (max - geometry(value)) / range * (priceHeight - padding * 2);
      final open = y(bar.open);
      final close = y(bar.close);
      final rising = geometry(bar.close) >= geometry(bar.open);
      final color = rising ? bull : bear;
      final paint = Paint()
        ..color = color
        ..strokeWidth = 1.5
        ..style = PaintingStyle.stroke;
      canvas.drawLine(Offset(x, y(bar.high)), Offset(x, y(bar.low)), paint);
      final body = Rect.fromLTRB(
        x - width / 2,
        open < close ? open : close,
        x + width / 2,
        open < close ? close : open,
      );
      if (rising) {
        paint.style = PaintingStyle.fill;
      }
      canvas.drawRect(
        body.height < 1
            ? Rect.fromLTWH(body.left, body.top, body.width, 1)
            : body,
        paint,
      );
      final volumeHeight = maxVolume == 0
          ? 0.0
          : geometry(bar.volume) /
                maxVolume *
                (size.height - priceHeight - padding);
      canvas.drawRect(
        Rect.fromLTWH(
          x - width / 2,
          size.height - volumeHeight,
          width,
          volumeHeight,
        ),
        Paint()..color = color.withValues(alpha: 0.55),
      );
    }
  }

  @override
  bool shouldRepaint(covariant _CandlePainter oldDelegate) =>
      oldDelegate.bars != bars ||
      oldDelegate.bull != bull ||
      oldDelegate.bear != bear ||
      oldDelegate.axis != axis;
}

class _OhlcvTable extends StatelessWidget {
  const _OhlcvTable({required this.candles});
  final MarketCandles candles;

  @override
  Widget build(BuildContext context) => _SectionCard(
    title: 'OHLCV 표',
    child: SizedBox(
      height: 360,
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: SizedBox(
          width: 1320,
          child: Column(
            children: [
              const _OhlcvRow.header(),
              const Divider(height: 1),
              Expanded(
                child: ListView.builder(
                  key: const Key('ohlcv-table-rows'),
                  itemExtent: 56,
                  itemCount: candles.bars.length,
                  itemBuilder: (context, index) =>
                      _OhlcvRow(bar: candles.bars[index]),
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

class _OhlcvRow extends StatelessWidget {
  const _OhlcvRow.header() : bar = null;
  const _OhlcvRow({required this.bar});
  final MarketBar? bar;

  @override
  Widget build(BuildContext context) {
    final value = bar;
    if (value == null) {
      return const Row(
        children: [
          _TableCell(
            '시각',
            width: 420,
            header: true,
            semanticKey: Key('ohlcv-header-at'),
          ),
          _TableCell('시가', width: 180, header: true),
          _TableCell('고가', width: 180, header: true),
          _TableCell('저가', width: 180, header: true),
          _TableCell('종가', width: 180, header: true),
          _TableCell('거래량', width: 180, header: true),
        ],
      );
    }
    return Row(
      children: [
        _TableCell(
          value.at,
          width: 420,
          semanticLabel: '행 ${value.at}, 시각 ${value.at}',
        ),
        _TableCell(
          value.open,
          width: 180,
          semanticLabel: '행 ${value.at}, 시가 ${value.open}',
        ),
        _TableCell(
          value.high,
          width: 180,
          semanticLabel: '행 ${value.at}, 고가 ${value.high}',
        ),
        _TableCell(
          value.low,
          width: 180,
          semanticLabel: '행 ${value.at}, 저가 ${value.low}',
        ),
        _TableCell(
          value.close,
          width: 180,
          semanticLabel: '행 ${value.at}, 종가 ${value.close}',
        ),
        _TableCell(
          value.volume,
          width: 180,
          semanticLabel: '행 ${value.at}, 거래량 ${value.volume}',
        ),
      ],
    );
  }
}

class _TableCell extends StatelessWidget {
  const _TableCell(
    this.value, {
    required this.width,
    this.semanticLabel,
    this.header = false,
    this.semanticKey,
  });
  final String value;
  final double width;
  final String? semanticLabel;
  final bool header;
  final Key? semanticKey;
  @override
  Widget build(BuildContext context) => Semantics(
    key: semanticKey,
    container: true,
    excludeSemantics: true,
    header: header,
    label: semanticLabel ?? value,
    child: SizedBox(
      width: width,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
        child: Text(value, style: _tabular(context)),
      ),
    ),
  );
}

class _OverviewReconciliationCard extends StatelessWidget {
  const _OverviewReconciliationCard({
    required this.reconciliation,
    required this.retainedError,
    required this.busy,
    required this.onDetails,
  });

  final BrokerReconciliation? reconciliation;
  final bool retainedError;
  final bool busy;
  final VoidCallback onDetails;

  @override
  Widget build(BuildContext context) {
    final value = reconciliation;
    if (busy && value == null) {
      return const _SectionCard(
        title: '증권사 잔고 대조',
        child: _Loading(compact: true),
      );
    }
    if (value == null) {
      return _SectionCard(
        title: '증권사 잔고 대조',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(retainedError ? '대조 결과를 불러오지 못했습니다.' : '아직 저장된 대조 기록이 없습니다.'),
            const SizedBox(height: 4),
            Text(
              '로컬 원장 확인과 증권사 잔고 대조는 별개입니다.',
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 12),
            OutlinedButton(
              onPressed: onDetails,
              child: const Text('연결에서 자세히 보기'),
            ),
          ],
        ),
      );
    }

    final differences = value.positionDifferences;
    final mismatchCount = differences.where((item) => !item.match).length;
    final status = differences.isEmpty
        ? '대조할 보유 종목 없음'
        : value.allPositionsMatch
        ? '${differences.length}개 모두 일치'
        : '${differences.length}개 중 $mismatchCount개 불일치';
    final fetchedAt = _time(DateTime.parse(value.fetchedAt));
    final recordedAt = _time(DateTime.parse(value.recordedAt));
    final semantics =
        '증권사 잔고 대조, 마지막 저장 기록, 현재 상태 아님, $status, 증권사 $fetchedAt, 저장 $recordedAt';
    return _SectionCard(
      title: '증권사 잔고 대조',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Semantics(
            container: true,
            excludeSemantics: true,
            label: semantics,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '마지막 저장 기록 · 현재 상태 아님',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: 8),
                Text(status, style: Theme.of(context).textTheme.titleMedium),
                const SizedBox(height: 4),
                Text(
                  '증권사 $fetchedAt · 저장 $recordedAt',
                  style: _tabular(context),
                ),
              ],
            ),
          ),
          if (busy) ...[
            const SizedBox(height: 12),
            const _ReconciliationRefreshingNotice(),
          ],
          if (retainedError) ...[
            const SizedBox(height: 12),
            _Notice(
              icon: Icons.warning_amber_rounded,
              text: '새로고침에 실패해 마지막 정상 대조 기록을 유지합니다.',
              color: Theme.of(context).colorScheme.error,
            ),
          ],
          const SizedBox(height: 12),
          OutlinedButton(
            onPressed: onDetails,
            child: const Text('연결에서 자세히 보기'),
          ),
        ],
      ),
    );
  }
}

class _TrustBanner extends StatelessWidget {
  const _TrustBanner({
    required this.state,
    required this.status,
    required this.retainedError,
    required this.reconciliation,
    required this.reconciliationFailed,
    required this.reconciliationBusy,
  });
  final DataState state;
  final ServiceStatus? status;
  final String? retainedError;
  final BrokerReconciliation? reconciliation;
  final bool reconciliationFailed;
  final bool reconciliationBusy;

  @override
  Widget build(BuildContext context) {
    final isGood = state == DataState.success;
    final color = switch (state) {
      DataState.success => _positiveColor(context),
      DataState.neverVerified => _warningColor(context),
      DataState.stale => _warningColor(context),
      _ => Theme.of(context).colorScheme.error,
    };
    final label = switch (state) {
      DataState.success => '로컬 기록 확인 완료',
      DataState.neverVerified => '아직 확인되지 않음',
      DataState.stale => '새로고침 필요',
      DataState.partial => '일부 데이터 확인 필요',
      _ => '확인 필요',
    };
    final verified = status?.lastVerifiedAt == null
        ? '없음'
        : _time(status!.lastVerifiedAt!);
    final error = retainedError == null
        ? ''
        : '\n새로고침 실패: ${retainedError!}\n마지막 정상 스냅샷은 유지됩니다.';
    final reconciliationText = reconciliation == null
        ? reconciliationBusy
              ? '증권사 잔고 대조 확인 중'
              : !reconciliationFailed
              ? '증권사 잔고 대조 기록 없음'
              : '증권사 잔고 대조 확인 실패'
        : reconciliationBusy
        ? '증권사 잔고 대조 저장 기록 다시 확인 중 · 현재 상태 아님'
        : '증권사 잔고 대조 저장 기록 · 현재 상태 아님';
    final text =
        '데이터 상태: $label\n로컬 원장 마지막 확인 $verified\n$reconciliationText$error';
    return Semantics(
      container: true,
      excludeSemantics: true,
      liveRegion: !isGood,
      label: text,
      child: _Notice(
        icon: isGood ? Icons.verified : Icons.warning_amber_rounded,
        color: color,
        text: text,
      ),
    );
  }
}

class ActivityPage extends StatefulWidget {
  const ActivityPage({super.key, required this.api});
  final OmniApi api;

  @override
  State<ActivityPage> createState() => _ActivityPageState();
}

class _ActivityPageState extends State<ActivityPage> {
  final _csv = TextEditingController();
  ImportPreview? _preview;
  ApplyReceipt? _receipt;
  String? _error;
  var _busy = false;

  @override
  void dispose() {
    _csv.dispose();
    super.dispose();
  }

  Future<void> _previewCsv() async {
    if (_csv.text.trim().isEmpty) {
      setState(() => _error = 'CSV 내용을 붙여 넣으세요. 미리보기는 원장을 변경하지 않습니다.');
      return;
    }
    final csv = _csv.text;
    setState(() {
      _busy = true;
      _error = null;
      _receipt = null;
    });
    try {
      final preview = await widget.api.preview(csv);
      if (mounted && _csv.text == csv) {
        setState(() => _preview = preview);
      } else if (mounted) {
        setState(() => _error = 'CSV 내용이 변경되어 이전 미리보기는 무효입니다. 새 미리보기를 만드세요.');
      }
    } catch (error) {
      if (mounted) {
        setState(() => _error = '$error\n이전 미리보기는 유지됩니다.');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _apply() async {
    final preview = _preview;
    if (preview == null || !preview.canApply) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final receipt = await widget.api.apply(
        preview.previewId,
        'import-${preview.previewId}',
      );
      if (mounted) setState(() => _receipt = receipt);
    } catch (error) {
      if (mounted) {
        setState(
          () => _error = '$error\n같은 미리보기와 멱등성 키로 다시 시도해도 중복 반영되지 않습니다.',
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _invalidatePreviewOnEdit(String _) {
    if (_preview == null && _receipt == null) return;
    setState(() {
      _preview = null;
      _receipt = null;
      _error = 'CSV 내용이 변경되어 이전 미리보기는 무효입니다. 새 미리보기를 만드세요.';
    });
  }

  @override
  Widget build(BuildContext context) => ListView(
    padding: const EdgeInsets.all(16),
    children: [
      _SectionCard(
        title: '거래 내역 가져오기',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('원장을 바꾸기 전에 새 거래, 중복, 오류를 먼저 확인합니다.'),
            const SizedBox(height: 12),
            TextField(
              controller: _csv,
              onChanged: _invalidatePreviewOnEdit,
              minLines: 6,
              maxLines: 12,
              keyboardType: TextInputType.multiline,
              decoration: const InputDecoration(
                labelText: 'CSV 텍스트',
                hintText: 'header\nrow…',
              ),
            ),
            const SizedBox(height: 12),
            ElevatedButton.icon(
              onPressed: _busy ? null : _previewCsv,
              icon: const Icon(Icons.preview),
              label: const Text('미리보기 만들기'),
            ),
          ],
        ),
      ),
      if (_busy)
        const Padding(
          padding: EdgeInsets.all(16),
          child: _Loading(compact: true),
        ),
      if (_error != null)
        Padding(
          padding: const EdgeInsets.only(top: 12),
          child: _Notice(
            icon: Icons.error_outline,
            text: _error!,
            color: Theme.of(context).colorScheme.error,
          ),
        ),
      if (_preview != null)
        Padding(
          padding: const EdgeInsets.only(top: 12),
          child: _PreviewCard(
            preview: _preview!,
            onApply: _busy ? null : _apply,
          ),
        ),
      if (_receipt != null)
        Padding(
          padding: const EdgeInsets.only(top: 12),
          child: _ReceiptCard(receipt: _receipt!),
        ),
    ],
  );
}

class DataPage extends StatefulWidget {
  const DataPage({super.key, required this.api, required this.controller});
  final OmniApi api;
  final PortfolioController controller;

  @override
  State<DataPage> createState() => _DataPageState();
}

class _DataPageState extends State<DataPage> {
  LocalOrderLog? _orderLog;
  var _orderLogFailed = false;
  var _orderLogBusy = true;

  @override
  void initState() {
    super.initState();
    _loadLocalOrders();
  }

  Future<void> _loadLocalOrders() async {
    if (!_orderLogBusy) {
      setState(() {
        _orderLogBusy = true;
        _orderLogFailed = false;
      });
    }
    try {
      final result = await widget.api.localOrders();
      if (mounted) {
        setState(() {
          _orderLog = result;
          _orderLogFailed = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _orderLogFailed = true);
    } finally {
      if (mounted) setState(() => _orderLogBusy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final baseUrl = widget.api is RestOmniApi
        ? (widget.api as RestOmniApi).baseUrl
        : '테스트 API';
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _Notice(
          icon: Icons.block,
          text: '실전 주문 꺼짐 · 현재는 로컬 거래 내역 가져오기만 사용할 수 있어요.',
          color: _warningColor(context),
        ),
        const SizedBox(height: 12),
        _SectionCard(
          title: '연결 정보',
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('기본 주소: $baseUrl', style: _tabular(context)),
              const SizedBox(height: 8),
              const Text(
                'API 주소는 앱 빌드 설정에서만 바꿀 수 있습니다. 캐시는 읽기 전용이며 오프라인 주문을 저장하지 않습니다.',
              ),
            ],
          ),
        ),
        const SizedBox(height: 12),
        _buildReconciliation(context),
        const SizedBox(height: 12),
        _buildLocalOrders(context),
      ],
    );
  }

  Widget _buildLocalOrders(BuildContext context) {
    final orderLog = _orderLog;
    if (_orderLogBusy && orderLog == null) {
      return const _SectionCard(
        title: '로컬 주문 기록',
        child: _Loading(compact: true),
      );
    }
    if (_orderLogFailed && orderLog == null) {
      return _SectionCard(
        title: '로컬 주문 기록',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('주문 기록을 불러오지 못했습니다.'),
            const SizedBox(height: 12),
            ElevatedButton(
              onPressed: _loadLocalOrders,
              child: const Text('로컬 주문 기록 다시 불러오기'),
            ),
          ],
        ),
      );
    }
    if (orderLog == null || orderLog.orders.isEmpty) {
      return const _SectionCard(
        title: '로컬 주문 기록',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('아직 저장된 주문 기록이 없습니다'),
            SizedBox(height: 4),
            Text('로컬 주문 기록과 현재 증권사 주문 상태는 별개입니다.'),
          ],
        ),
      );
    }
    return _LocalOrderLogCard(
      orderLog: orderLog,
      retainedError: _orderLogFailed,
      busy: _orderLogBusy,
      onRefresh: _loadLocalOrders,
    );
  }

  Widget _buildReconciliation(BuildContext context) {
    final reconciliation = widget.controller.reconciliation;
    if (widget.controller.reconciliationBusy && reconciliation == null) {
      return const _SectionCard(
        title: '증권사 잔고 대조',
        child: _Loading(compact: true),
      );
    }
    if (widget.controller.reconciliationFailed && reconciliation == null) {
      return _SectionCard(
        title: '증권사 잔고 대조',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('대조 결과를 불러오지 못했습니다'),
            const SizedBox(height: 12),
            ElevatedButton(
              onPressed: widget.controller.refreshReconciliation,
              child: const Text('대조 결과 다시 불러오기'),
            ),
          ],
        ),
      );
    }
    if (reconciliation == null) {
      return _SectionCard(
        title: '증권사 잔고 대조',
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('아직 증권사 대조 기록이 없습니다'),
            const SizedBox(height: 4),
            Text(
              '로컬 원장 확인과 증권사 잔고 대조는 별개입니다.',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
        ),
      );
    }
    return _BrokerReconciliationCard(
      reconciliation: reconciliation,
      retainedError: widget.controller.reconciliationFailed,
      busy: widget.controller.reconciliationBusy,
      onRefresh: widget.controller.refreshReconciliation,
    );
  }
}

class _LocalOrderLogCard extends StatelessWidget {
  const _LocalOrderLogCard({
    required this.orderLog,
    required this.retainedError,
    required this.busy,
    required this.onRefresh,
  });

  final LocalOrderLog orderLog;
  final bool retainedError;
  final bool busy;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) => _SectionCard(
    title: '로컬 주문 기록',
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _Notice(
          icon: Icons.history,
          text: '로컬 주문 기록 · 현재 브로커 상태가 아닙니다.\n이 화면은 증권사를 새로 조회하지 않습니다.',
          color: _warningColor(context),
        ),
        for (final order in orderLog.orders) ...[
          const Divider(height: 24),
          _LocalOrderRow(order: order),
        ],
        if (retainedError) ...[
          const SizedBox(height: 12),
          _Notice(
            icon: Icons.warning_amber_rounded,
            text: '새로고침 실패: 주문 기록을 불러오지 못했습니다.\n마지막 정상 주문 기록은 유지됩니다.',
            color: Theme.of(context).colorScheme.error,
          ),
        ],
        const SizedBox(height: 12),
        ElevatedButton.icon(
          onPressed: busy ? null : onRefresh,
          icon: const Icon(Icons.refresh),
          label: const Text('로컬 주문 기록 다시 불러오기'),
        ),
      ],
    ),
  );
}

class _LocalOrderRow extends StatelessWidget {
  const _LocalOrderRow({required this.order});

  final LocalOrderView order;

  @override
  Widget build(BuildContext context) {
    final mode = order.mode == 'synthetic' ? '합성 테스트' : '로컬 페이퍼';
    final side = order.side == 'BUY' ? '매수' : '매도';
    final status = _localOrderStatus(order.status);
    final semanticsStatus = order.status == 'SUBMIT_UNKNOWN'
        ? '브로커 결과 미확정, 재주문 금지'
        : status.$1;
    return Semantics(
      container: true,
      excludeSemantics: true,
      label:
          '$mode 주문, ${order.symbol} $side 지정가, 주문 수량 ${order.quantity}, 체결 수량 ${order.filledQuantity}, ${order.currency} ${order.limitPrice}, $semanticsStatus',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(status.$1, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 4),
          Text(status.$2),
          const SizedBox(height: 8),
          Text('$mode · ${order.symbol} · $side · 지정가'),
          const SizedBox(height: 4),
          Text(
            '주문 ${order.quantity}주 · 체결 ${order.filledQuantity}주\n${order.currency} ${order.limitPrice} · ${_time(DateTime.parse(order.lastRecordedAt))}',
            style: _tabular(context),
          ),
        ],
      ),
    );
  }
}

(String, String) _localOrderStatus(String status) => switch (status) {
  'RECORDED' => ('로컬 기록 저장', '주문 의도가 로컬 로그에 저장되었습니다.'),
  'READY' => ('로컬 전송 준비', '위험 검사를 통과한 로컬 기록입니다.'),
  'RISK_REJECTED' => ('위험 검사 차단', '위험 규칙이 주문 전송을 차단했습니다.'),
  'SUBMIT_UNKNOWN' => (
    '브로커 결과 미확정 · 재주문 금지',
    '증권사 접수 결과를 아직 확인하지 못했습니다. 같은 주문을 다시 보내면 안 됩니다.',
  ),
  'OPEN' => ('로컬 기록: 접수', '저장된 이벤트에서 접수 상태로 재생되었습니다.'),
  'REJECTED' => ('로컬 기록: 거절', '저장된 이벤트에서 거절 상태로 재생되었습니다.'),
  'PARTIALLY_FILLED' => ('로컬 기록: 일부 체결', '저장된 이벤트에서 일부 체결로 재생되었습니다.'),
  'CANCEL_UNKNOWN' => ('취소 결과 미확정 · 추가 조작 금지', '증권사 취소 결과를 아직 확인하지 못했습니다.'),
  'CANCELED' => ('로컬 기록: 취소', '저장된 이벤트에서 취소 상태로 재생되었습니다.'),
  'FILLED' => ('로컬 기록: 체결', '저장된 이벤트에서 체결 상태로 재생되었습니다.'),
  _ => throw StateError('validated local order status required'),
};

class _BrokerReconciliationCard extends StatelessWidget {
  const _BrokerReconciliationCard({
    required this.reconciliation,
    required this.retainedError,
    required this.busy,
    required this.onRefresh,
  });

  final BrokerReconciliation reconciliation;
  final bool retainedError;
  final bool busy;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    final differences = reconciliation.positionDifferences;
    final mismatchCount = differences.where((item) => !item.match).length;
    final status = reconciliation.allPositionsMatch
        ? '일치 · ${differences.length}종목의 보유 수량이 원장과 같습니다.'
        : '불일치 · $mismatchCount/${differences.length}종목의 보유 수량이 다릅니다.';
    final environment = reconciliation.environment == 'mock' ? '모의투자' : '실전';
    return _SectionCard(
      title: '증권사 잔고 대조',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Notice(
            icon: Icons.history,
            text:
                '마지막 저장 스냅샷 · 현재 상태 아님\n현재 대조 범위는 보유 수량입니다. 현금, 평가금액, 수수료, 주문 체결은 대조하지 않았습니다.',
            color: _warningColor(context),
          ),
          const SizedBox(height: 12),
          Text('키움 · $environment · ${reconciliation.exchange}'),
          const SizedBox(height: 4),
          Text(
            '증권사 ${_time(DateTime.parse(reconciliation.fetchedAt))} · 저장 ${_time(DateTime.parse(reconciliation.recordedAt))}\n원장 ${reconciliation.ledgerRevision}',
            style: _tabular(context),
          ),
          const SizedBox(height: 12),
          Semantics(
            container: true,
            label: status,
            child: Text(status, style: Theme.of(context).textTheme.titleMedium),
          ),
          if (differences.isEmpty) ...[
            const SizedBox(height: 8),
            const Text('현재 대조할 보유 종목이 없습니다.'),
          ],
          for (final difference in differences) ...[
            const Divider(height: 24),
            _BrokerDifferenceRow(difference: difference),
          ],
          if (busy) ...[
            const SizedBox(height: 12),
            const _ReconciliationRefreshingNotice(),
          ],
          if (retainedError) ...[
            const SizedBox(height: 12),
            _Notice(
              icon: Icons.warning_amber_rounded,
              text: '대조 결과 새로고침에 실패했습니다.\n마지막 정상 대조 기록은 유지됩니다.',
              color: Theme.of(context).colorScheme.error,
            ),
          ],
          const SizedBox(height: 12),
          ElevatedButton.icon(
            onPressed: busy ? null : onRefresh,
            icon: const Icon(Icons.refresh),
            label: const Text('저장된 대조 다시 불러오기'),
          ),
        ],
      ),
    );
  }
}

class _ReconciliationRefreshingNotice extends StatelessWidget {
  const _ReconciliationRefreshingNotice();

  static const text = '저장된 대조를 다시 불러오는 중입니다. 마지막 정상 기록을 유지합니다.';

  @override
  Widget build(BuildContext context) => Semantics(
    container: true,
    excludeSemantics: true,
    liveRegion: true,
    label: text,
    child: _Notice(icon: Icons.sync, text: text, color: _warningColor(context)),
  );
}

class _BrokerDifferenceRow extends StatelessWidget {
  const _BrokerDifferenceRow({required this.difference});
  final BrokerPositionDifference difference;

  @override
  Widget build(BuildContext context) {
    final status = difference.match ? '일치' : '불일치';
    final semantics =
        '${difference.symbol} $status, 증권사 수량 ${difference.brokerQuantity}, 원장 수량 ${difference.ledgerQuantity}, 차이 ${difference.difference}';
    return Semantics(
      container: true,
      excludeSemantics: true,
      label: semantics,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('${difference.symbol} · $status'),
          const SizedBox(height: 4),
          Text(
            '증권사 ${difference.brokerQuantity} · 원장 ${difference.ledgerQuantity} · 차이 ${difference.difference}',
            style: _tabular(context),
          ),
        ],
      ),
    );
  }
}

class _PreviewCard extends StatelessWidget {
  const _PreviewCard({required this.preview, required this.onApply});
  final ImportPreview preview;
  final VoidCallback? onApply;

  @override
  Widget build(BuildContext context) => _SectionCard(
    title: '미리보기 결과',
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '신규 ${preview.newRows} · 중복 ${preview.duplicateRows} · 오류 ${preview.errorRows} · 미해결 ${preview.unresolvedRows}',
          style: _tabular(context),
        ),
        const SizedBox(height: 8),
        Text(
          preview.canApply
              ? '적용 가능: 이 미리보기만 원장에 원자적으로 반영됩니다.'
              : '적용 차단: 오류 또는 미해결 행을 고친 뒤 새 미리보기를 만드세요.',
        ),
        const SizedBox(height: 4),
        Text(
          '스키마 ${preview.schemaVersion} · 매핑 ${preview.mappingVersion}',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        const SizedBox(height: 12),
        ...preview.rows.take(5).map((row) {
          final target = row.correctionTarget;
          return Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: target == null
                ? Text(
                    '행 ${row.rowNumber}: ${row.status}${row.symbol == null ? '' : ' · ${row.symbol!}'}${row.errors.isEmpty ? '' : ' · ${row.errors.join(', ')}'}',
                    style: _tabular(context),
                  )
                : Semantics(
                    container: true,
                    excludeSemantics: true,
                    label:
                        '정정 행 ${row.rowNumber}, 원본 ${target.type}, ${target.currency} ${target.amount}, source ${target.sourceEventId}, 반전 ${row.currency} ${row.amount}',
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text('행 ${row.rowNumber}: ${row.status} · 정정'),
                        const SizedBox(height: 4),
                        const Text('원본 기록은 보존되고 반대 금액으로 상쇄됩니다.'),
                        const SizedBox(height: 4),
                        Text(
                          '원본 ${target.type} · ${target.currency} ${target.amount} · source ${target.sourceEventId}',
                          style: _tabular(context),
                        ),
                        Text(
                          '정정 ${row.transactionType} · ${row.currency} ${row.amount}',
                          style: _tabular(context),
                        ),
                      ],
                    ),
                  ),
          );
        }),
        const SizedBox(height: 8),
        ElevatedButton.icon(
          onPressed: preview.canApply ? onApply : null,
          icon: const Icon(Icons.check_circle_outline),
          label: const Text('원자적으로 적용'),
        ),
      ],
    ),
  );
}

class _ReceiptCard extends StatelessWidget {
  const _ReceiptCard({required this.receipt});
  final ApplyReceipt receipt;

  @override
  Widget build(BuildContext context) => Semantics(
    liveRegion: true,
    label: '적용 확인 receipt ${receipt.receiptId}',
    child: _SectionCard(
      title: '적용 확인',
      child: Text(
        '원자적 적용 완료\n적용 ${receipt.appliedRows}건 · 중복 제외 ${receipt.skippedDuplicateRows}건\nReceipt ${receipt.receiptId}\n원장 ${receipt.ledgerRevisionAfter} · ${_time(receipt.recordedAt)}',
        style: _tabular(context),
      ),
    ),
  );
}

class _SectionCard extends StatelessWidget {
  const _SectionCard({required this.title, required this.child});
  final String title;
  final Widget child;
  @override
  Widget build(BuildContext context) => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Semantics(
            container: true,
            header: true,
            child: Text(title, style: Theme.of(context).textTheme.titleMedium),
          ),
          const SizedBox(height: 12),
          child,
        ],
      ),
    ),
  );
}

class _Notice extends StatelessWidget {
  const _Notice({required this.icon, required this.text, required this.color});
  final IconData icon;
  final String text;
  final Color color;
  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.all(16),
    decoration: BoxDecoration(
      border: Border.all(color: color),
      borderRadius: BorderRadius.circular(16),
    ),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, color: color),
        const SizedBox(width: 12),
        Expanded(child: Text(text)),
      ],
    ),
  );
}

class _Message extends StatelessWidget {
  const _Message({
    required this.icon,
    required this.title,
    required this.body,
    this.action,
    this.actionLabel,
  });
  final IconData icon;
  final String title;
  final String body;
  final VoidCallback? action;
  final String? actionLabel;
  @override
  Widget build(BuildContext context) => Center(
    child: SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 48),
          const SizedBox(height: 16),
          Text(
            title,
            style: Theme.of(context).textTheme.titleMedium,
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 8),
          Text(body, textAlign: TextAlign.center),
          if (action != null) ...[
            const SizedBox(height: 16),
            ElevatedButton(onPressed: action, child: Text(actionLabel!)),
          ],
        ],
      ),
    ),
  );
}

class _Loading extends StatelessWidget {
  const _Loading({this.compact = false});
  final bool compact;
  @override
  Widget build(BuildContext context) => Semantics(
    label: '데이터를 불러오는 중',
    liveRegion: true,
    child: Center(
      child: SizedBox(
        height: compact ? 24 : 48,
        width: compact ? 24 : 48,
        child: MediaQuery.disableAnimationsOf(context)
            ? const Icon(Icons.hourglass_empty)
            : const CircularProgressIndicator(),
      ),
    ),
  );
}

TextStyle _tabular(BuildContext context) => Theme.of(context)
    .textTheme
    .bodyLarge!
    .copyWith(fontFeatures: const [FontFeature.tabularFigures()]);

TextStyle _gainStyle(BuildContext context, List<Money> money) {
  final negative = money.any((item) => item.amount.startsWith('-'));
  return _tabular(context).copyWith(
    color: negative
        ? Theme.of(context).colorScheme.error
        : _positiveColor(context),
  );
}

Color _positiveColor(BuildContext context) =>
    Theme.of(context).brightness == Brightness.dark
    ? const Color(0xFF34D399)
    : const Color(0xFF047857);

Color _warningColor(BuildContext context) =>
    Theme.of(context).brightness == Brightness.dark
    ? const Color(0xFFFCD34D)
    : const Color(0xFFA16207);

String _moneySummary(List<Money> money) => money.isEmpty
    ? '없음'
    : money.map((item) => '${item.currency} ${item.amount}').join(' · ');

String _time(DateTime value) => value.toLocal().toString().substring(0, 16);

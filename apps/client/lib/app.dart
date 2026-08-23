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
          OverviewPage(controller: _portfolio),
          HoldingsPage(controller: _portfolio),
          ActivityPage(api: widget.api),
          DataPage(api: widget.api),
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
  String? error;
  var busy = false;

  Future<void> refresh() async {
    if (busy) return;
    busy = true;
    error = null;
    notifyListeners();
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
    notifyListeners();
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
  const OverviewPage({super.key, required this.controller});
  final PortfolioController controller;

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
        action: controller.refresh,
        actionLabel: '새로고침',
      );
    }
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        _TrustBanner(
          state: controller.state,
          status: controller.status,
          retainedError: controller.error,
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
        return _SectionCard(
          title: holding.symbol,
          child: Text(
            '수량 ${holding.quantity} · 원가 ${holding.currency} ${holding.costBasis}',
            style: _tabular(context),
          ),
        );
      },
    );
  }
}

class _TrustBanner extends StatelessWidget {
  const _TrustBanner({
    required this.state,
    required this.status,
    required this.retainedError,
  });
  final DataState state;
  final ServiceStatus? status;
  final String? retainedError;

  @override
  Widget build(BuildContext context) {
    final isGood = state == DataState.success;
    final color = switch (state) {
      DataState.success => const Color(0xFF047857),
      DataState.neverVerified => const Color(0xFFA16207),
      DataState.stale => const Color(0xFFA16207),
      _ => const Color(0xFFB91C1C),
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
    return Semantics(
      container: true,
      excludeSemantics: true,
      liveRegion: !isGood,
      label: '신뢰 상태 $label',
      child: _Notice(
        icon: isGood ? Icons.verified : Icons.warning_amber_rounded,
        color: color,
        text: '데이터 상태: $label\n증권사 잔고 대조 전 · 마지막 확인 $verified$error',
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

class DataPage extends StatelessWidget {
  const DataPage({super.key, required this.api});
  final OmniApi api;

  @override
  Widget build(BuildContext context) {
    final baseUrl = api is RestOmniApi
        ? (api as RestOmniApi).baseUrl
        : '테스트 API';
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        const _Notice(
          icon: Icons.block,
          text: '실전 주문 꺼짐 · 현재는 로컬 거래 내역 가져오기만 사용할 수 있어요.',
          color: Color(0xFFA16207),
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
      ],
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
        ...preview.rows
            .take(5)
            .map(
              (row) => Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Text(
                  '행 ${row.rowNumber}: ${row.status}${row.symbol == null ? '' : ' · ${row.symbol!}'}${row.errors.isEmpty ? '' : ' · ${row.errors.join(', ')}'}',
                  style: _tabular(context),
                ),
              ),
            ),
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
          Text(title, style: Theme.of(context).textTheme.titleMedium),
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
        Icon(icon, color: color, semanticLabel: '상태'),
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
        child: const CircularProgressIndicator(),
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
        : const Color(0xFF047857),
  );
}

String _moneySummary(List<Money> money) => money.isEmpty
    ? '없음'
    : money.map((item) => '${item.currency} ${item.amount}').join(' · ');

String _time(DateTime value) => value.toLocal().toString().substring(0, 16);

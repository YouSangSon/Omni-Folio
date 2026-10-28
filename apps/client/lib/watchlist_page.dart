import 'package:flutter/material.dart';

import 'models.dart';
import 'watchlist.dart';

class WatchlistPage extends StatefulWidget {
  const WatchlistPage({super.key, required this.controller});
  final WatchlistController controller;

  @override
  State<WatchlistPage> createState() => _WatchlistPageState();
}

class _WatchlistPageState extends State<WatchlistPage> {
  late final TextEditingController _symbol;
  late String _venue;
  String? _symbolError;
  bool _confirming = false;

  @override
  void initState() {
    super.initState();
    _venue = widget.controller.draftVenue;
    _symbol = TextEditingController(text: widget.controller.draftSymbol);
    widget.controller.ensureLoaded();
  }

  @override
  void dispose() {
    _symbol.dispose();
    super.dispose();
  }

  Future<void> _add() async {
    final controller = widget.controller;
    if (!controller.canEdit) return;
    final symbol = _symbol.text.trim().toUpperCase();
    final valid = _venue == 'XKRX'
        ? RegExp(r'^[0-9]{6}$').hasMatch(symbol)
        : RegExp(r'^[A-Z0-9][A-Z0-9.\-]{0,31}$').hasMatch(symbol);
    if (!valid) {
      setState(() {
        _symbolError = _venue == 'XKRX'
            ? '한국 종목 코드는 숫자 6자리로 입력하세요.'
            : '미국 심볼은 영문·숫자로 시작하고 영문·숫자·점·하이픈을 32자 이내로 입력하세요.';
      });
      return;
    }
    setState(() => _symbolError = null);
    final entry = WatchlistEntry.fromJson({
      'venue': _venue,
      'symbol': symbol,
      'currency': _venue == 'XKRX' ? 'KRW' : 'USD',
    });
    final saved = await controller.add(entry);
    if (saved) {
      controller.draftSymbol = '';
      if (mounted) _symbol.clear();
    }
  }

  Future<void> _confirmRemove(WatchlistEntry entry) async {
    if (_confirming || !widget.controller.canEdit) return;
    _confirming = true;
    try {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('관심종목 삭제'),
          content: Text(
            '${entry.venue} ${entry.symbol} (${entry.currency})을 관심종목에서 삭제할까요?',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('취소'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('삭제'),
            ),
          ],
        ),
      );
      if (confirmed == true && mounted) await widget.controller.remove(entry);
    } finally {
      _confirming = false;
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.controller,
    builder: (context, _) {
      final controller = widget.controller;
      final state = controller.state;
      final report = controller.report;
      final entries = state?.entries ?? const <WatchlistEntry>[];
      return Scaffold(
        appBar: AppBar(
          title: const Text('관심종목'),
          actions: [
            IconButton(
              tooltip: '관심종목 새로고침',
              constraints: const BoxConstraints(minWidth: 48, minHeight: 48),
              onPressed: controller.busy ? null : controller.refresh,
              icon: const Icon(Icons.refresh),
            ),
          ],
        ),
        body: SafeArea(
          child: ListView.builder(
            padding: const EdgeInsets.all(16),
            itemCount: entries.length + 1,
            itemBuilder: (context, index) {
              if (index > 0) {
                final entry = entries[index - 1];
                return _WatchlistRow(
                  entry: entry,
                  quote: report?.quotes[index - 1],
                  canEdit: controller.canEdit,
                  onRemove: () => _confirmRemove(entry),
                );
              }
              return Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  const Text(
                    '이 목록은 이 서비스에 저장한 개인 설정입니다. 보유 종목이나 실제 상장 여부를 확인하지 않습니다.',
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    '표시하는 값은 저장된 가격이며 실시간 시세가 아닙니다. 새로 추가한 종목에는 가격이 없을 수 있습니다. 가격 파일은 원장에 등록된 종목만 가져올 수 있습니다.',
                  ),
                  if (controller.busy) ...[
                    const SizedBox(height: 12),
                    Semantics(
                      liveRegion: true,
                      child: MediaQuery.disableAnimationsOf(context)
                          ? Text(
                              controller.saving
                                  ? '관심종목 저장 확인 중'
                                  : '관심종목 불러오는 중',
                            )
                          : LinearProgressIndicator(
                              semanticsLabel: controller.saving
                                  ? '관심종목 저장 확인 중'
                                  : '관심종목 불러오는 중',
                            ),
                    ),
                  ],
                  if (controller.message != null) ...[
                    const SizedBox(height: 12),
                    Semantics(
                      liveRegion: true,
                      child: Text(controller.message!),
                    ),
                  ],
                  if (controller.pending != null) ...[
                    const SizedBox(height: 12),
                    const Text(
                      '대기 중인 변경을 확인하기 전에는 목록을 바꿀 수 없습니다. 이 화면을 닫아도 앱 실행 중에는 요청이 유지되며, 앱을 종료하면 유지되지 않습니다.',
                    ),
                    const SizedBox(height: 8),
                    FilledButton(
                      style: _buttonStyle,
                      onPressed: controller.busy ? null : controller.retry,
                      child: const Text('같은 변경 다시 시도'),
                    ),
                    OutlinedButton(
                      style: _buttonStyle,
                      onPressed: controller.busy ? null : controller.refresh,
                      child: const Text('저장 목록 다시 불러오기'),
                    ),
                  ],
                  const SizedBox(height: 16),
                  Semantics(
                    header: true,
                    child: Text(
                      '관심종목 추가',
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                  ),
                  const SizedBox(height: 8),
                  DropdownButtonFormField<String>(
                    initialValue: _venue,
                    isExpanded: true,
                    decoration: const InputDecoration(labelText: '거래소'),
                    items: const [
                      DropdownMenuItem(
                        value: 'XKRX',
                        child: Text('한국 XKRX · KRW'),
                      ),
                      DropdownMenuItem(
                        value: 'XNAS',
                        child: Text('미국 XNAS · USD'),
                      ),
                      DropdownMenuItem(
                        value: 'XNYS',
                        child: Text('미국 XNYS · USD'),
                      ),
                    ],
                    onChanged: !controller.canEdit
                        ? null
                        : (value) {
                            if (value != null) {
                              setState(() {
                                _venue = value;
                                controller.draftVenue = value;
                                _symbolError = null;
                              });
                            }
                          },
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    key: const Key('watchlist-symbol'),
                    controller: _symbol,
                    enabled: controller.canEdit,
                    autocorrect: false,
                    enableSuggestions: false,
                    textCapitalization: TextCapitalization.characters,
                    keyboardType: _venue == 'XKRX'
                        ? TextInputType.number
                        : TextInputType.text,
                    textInputAction: TextInputAction.done,
                    decoration: InputDecoration(
                      labelText: '종목 코드 또는 심볼',
                      hintText: _venue == 'XKRX' ? '예: 005930' : '예: AAPL',
                      errorText: _symbolError,
                      errorMaxLines: 4,
                    ),
                    onChanged: (value) {
                      controller.draftSymbol = value;
                      if (_symbolError != null) {
                        setState(() => _symbolError = null);
                      }
                    },
                    onSubmitted: (_) => _add(),
                  ),
                  const SizedBox(height: 12),
                  FilledButton.icon(
                    style: _buttonStyle,
                    onPressed: controller.canEdit ? _add : null,
                    icon: const Icon(Icons.add),
                    label: const Text('관심종목 저장'),
                  ),
                  const SizedBox(height: 20),
                  Semantics(
                    header: true,
                    child: Text(
                      '저장 목록 ${entries.length}/50',
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                  ),
                  if (state?.updatedAt != null)
                    Text('목록 저장 시각: ${state!.updatedAt}'),
                  if (report != null) Text('가격 확인 시각: ${report.observedAt}'),
                  if (state != null && report == null)
                    const Text('저장 목록은 확인했습니다. 가격을 다시 불러와 확인하세요.'),
                  if (state != null && entries.isEmpty)
                    const Text('저장된 관심종목이 없습니다.'),
                  if (state == null && !controller.busy)
                    const Text('목록을 불러온 뒤 종목을 추가할 수 있습니다.'),
                  const SizedBox(height: 8),
                ],
              );
            },
          ),
        ),
      );
    },
  );
}

const _buttonStyle = ButtonStyle(
  minimumSize: WidgetStatePropertyAll(Size(48, 48)),
);

class _WatchlistRow extends StatelessWidget {
  const _WatchlistRow({
    required this.entry,
    required this.quote,
    required this.canEdit,
    required this.onRemove,
  });

  final WatchlistEntry entry;
  final WatchlistQuote? quote;
  final bool canEdit;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    final quote = this.quote;
    final price = quote?.price;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              '${entry.venue} · ${entry.symbol} · ${entry.currency}',
              style: Theme.of(context).textTheme.titleMedium,
            ),
            if (quote == null)
              const Text('가격 확인 필요')
            else if (quote.status == 'ambiguous')
              const Text('출처 확인 필요')
            else if (price == null)
              const Text('저장 가격 없음')
            else ...[
              Text('저장 가격: ${price.price} ${entry.currency}'),
              Text('가격 관측 시각: ${price.observedAt}'),
              Text('가격 수집 시각: ${price.fetchedAt}'),
              Text('가격 기록 시각: ${price.recordedAt}'),
              if (price.source == 'owner_declared') ...[
                const Text('소유자가 입력한 가격 · 제공자가 검증한 값이 아닙니다.'),
                Text('출처 버전: ${price.sourceVersion}'),
                const Text('무조정 가격'),
              ] else
                const Text('로컬 예시 출처 · 조정 여부 미지정'),
              if (price.sample) const Text('예시 가격입니다.'),
              if (quote.status == 'stale') const Text('오래된 가격입니다.'),
              if (quote.status == 'unknown')
                const Text('현재 시세 여부는 확인되지 않았습니다.'),
            ],
            Align(
              alignment: AlignmentDirectional.centerEnd,
              child: IconButton(
                tooltip: '${entry.symbol} 삭제',
                constraints: const BoxConstraints(minWidth: 48, minHeight: 48),
                onPressed: canEdit ? onRemove : null,
                icon: const Icon(Icons.delete_outline),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

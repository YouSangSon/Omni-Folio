import 'package:flutter/material.dart';

import 'api.dart';
import 'public_research.dart';

Future<void> selectPublicResearch(
  BuildContext context,
  OmniApi api, {
  String symbol = '',
}) async {
  final identity = await showDialog<PublicResearchIdentity>(
    context: context,
    builder: (_) => _IdentityDialog(symbol: symbol),
  );
  if (identity != null && context.mounted) {
    await Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => PublicResearchPage(api: api, identity: identity),
      ),
    );
  }
}

class _IdentityDialog extends StatefulWidget {
  const _IdentityDialog({required this.symbol});
  final String symbol;

  @override
  State<_IdentityDialog> createState() => _IdentityDialogState();
}

class _IdentityDialogState extends State<_IdentityDialog> {
  late final _symbol = TextEditingController(text: widget.symbol);
  String? _market, _error;

  @override
  void dispose() {
    _symbol.dispose();
    super.dispose();
  }

  void _open() {
    if (_market == null) return;
    try {
      final identity = PublicResearchIdentity(
        market: _market!,
        symbol: _symbol.text.trim().toUpperCase(),
      );
      Navigator.of(context).pop(identity);
    } on FormatException {
      setState(
        () => _error = '영문·숫자로 시작하는 종목 코드를 32자 이내로 입력하세요. 점과 하이픈도 사용할 수 있습니다.',
      );
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('공시·뉴스·분석 조회'),
    content: SingleChildScrollView(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Text('조회할 시장을 직접 선택하세요. 보유 통화로 시장을 추정하지 않습니다.'),
          const SizedBox(height: 16),
          DropdownButtonFormField<String>(
            key: const Key('research-market'),
            isExpanded: true,
            decoration: const InputDecoration(labelText: '시장'),
            items: const [
              DropdownMenuItem(value: 'KR', child: Text('한국 KR')),
              DropdownMenuItem(value: 'US', child: Text('미국 US')),
            ],
            onChanged: (value) => setState(() => _market = value),
          ),
          const SizedBox(height: 12),
          TextField(
            key: const Key('research-symbol'),
            controller: _symbol,
            autocorrect: false,
            enableSuggestions: false,
            textCapitalization: TextCapitalization.characters,
            textInputAction: TextInputAction.done,
            onSubmitted: (_) => _open(),
            decoration: InputDecoration(
              labelText: '종목 코드 또는 심볼',
              hintText: '005930 또는 AAPL',
              errorText: _error,
              errorMaxLines: 5,
            ),
          ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('취소'),
      ),
      FilledButton(
        key: const Key('research-open'),
        onPressed: _market == null ? null : _open,
        child: const Text('조회'),
      ),
    ],
  );
}

class PublicResearchPage extends StatefulWidget {
  const PublicResearchPage({
    super.key,
    required this.api,
    required this.identity,
  });
  final OmniApi api;
  final PublicResearchIdentity identity;

  @override
  State<PublicResearchPage> createState() => _PublicResearchPageState();
}

class _PublicResearchPageState extends State<PublicResearchPage> {
  PublicResearch? _observation;
  String? _error;
  bool _busy = false;
  int _generation = 0;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void didUpdateWidget(PublicResearchPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!widget.identity.matches(oldWidget.identity) ||
        widget.api != oldWidget.api) {
      _generation++;
      _observation = null;
      _busy = false;
      _refresh();
    }
  }

  Future<void> _refresh() async {
    if (_busy) return;
    final generation = ++_generation, identity = widget.identity;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final observation = await widget.api.publicResearch(identity);
      if (!observation.identity.matches(identity)) {
        throw const FormatException('Research identity mismatch');
      }
      if (mounted && generation == _generation) {
        setState(() => _observation = observation);
      }
    } catch (error) {
      if (mounted && generation == _generation) {
        setState(
          () => _error = _observation != null
              ? '새로고침하지 못했습니다. 마지막 정상 관측을 그대로 유지합니다.'
              : switch (error) {
                  ApiException(code: 'research_not_configured') =>
                    '공개 연구 파일이 연결되지 않았습니다. 서버의 연구 파일 설정을 확인하세요.',
                  ApiException(code: 'research_feed_missing') =>
                    '연결된 공개 연구 파일이 없습니다. 내보낸 파일을 확인하세요.',
                  _ => '공시·뉴스·분석을 불러오지 못했습니다. 파일과 서버 연결을 확인한 뒤 다시 시도하세요.',
                },
        );
      }
    } finally {
      if (mounted && generation == _generation) {
        setState(() => _busy = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text(
        '${widget.identity.market} ${widget.identity.symbol} 공개 연구',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          tooltip: '공개 연구 새로고침',
          onPressed: _busy ? null : _refresh,
          icon: const Icon(Icons.refresh_rounded),
        ),
      ],
    ),
    body: SafeArea(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: _body(context),
        ),
      ),
    ),
  );

  Widget _body(BuildContext context) {
    final observation = _observation;
    if (observation == null && _busy) {
      return Center(
        child: Semantics(
          liveRegion: true,
          label: '공개 연구 불러오는 중',
          child: const CircularProgressIndicator(),
        ),
      );
    }
    if (observation == null) {
      return ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Semantics(
            liveRegion: true,
            child: Text(_error ?? '공개 연구를 불러오지 못했습니다.'),
          ),
          const SizedBox(height: 16),
          ElevatedButton(
            onPressed: _busy ? null : _refresh,
            child: const Text('다시 시도'),
          ),
        ],
      );
    }
    return ListView.builder(
      key: const Key('public-research-scroll'),
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 32),
      itemCount: observation.items.length + 1,
      itemBuilder: (context, index) {
        if (index > 0) {
          final item = observation.items[index - 1];
          return _ResearchItemCard(key: ValueKey(item.id), item: item);
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (_busy)
              const LinearProgressIndicator(semanticsLabel: '공개 연구 새로고침 중'),
            if (_error != null)
              Semantics(
                liveRegion: true,
                child: Text(
                  _error!,
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
              ),
            _ResearchCard(
              title: 'Mimir 저장 자료',
              children: [
                if (observation.sample)
                  const Text('합성 예시 자료 · 실제 투자 자료가 아닙니다.'),
                const Text('공시·뉴스·Mimir 가공 분석을 읽는 화면입니다. 투자 조언이나 주문 신호가 아닙니다.'),
                const Text('출처 기록의 존재는 사용권 승인이나 수집 완전성을 뜻하지 않습니다.'),
                Text('관측 시각 ${observation.observedAt}'),
                Text('파일 생성 시각 ${observation.generatedAt}'),
                Text('자료 범위 ${observation.since} ~ ${observation.until} (UTC)'),
                Text(
                  '종목 미연결 뉴스 ${observation.unscopedNews}건 · 출처 근거 누락 ${observation.missingSourceEvidence}건 제외',
                ),
                if (observation.items.isEmpty)
                  const Text('이 파일에서 해당 시장·종목의 자료가 없습니다.'),
              ],
            ),
          ],
        );
      },
    );
  }
}

class _ResearchItemCard extends StatelessWidget {
  const _ResearchItemCard({super.key, required this.item});
  final PublicResearchItem item;

  @override
  Widget build(BuildContext context) => _ResearchCard(
    title: switch (item.kind) {
      'filing' => '공시',
      'news' => '뉴스',
      _ => 'Mimir 가공 분석',
    },
    children: [
      if (item.kind == 'insight') ...[
        Text('분석 기준일 ${item.asOf}'),
        Text(
          '연구 의견 ${switch (item.direction) {
            'bullish' => '상승 관점',
            'bearish' => '하락 관점',
            _ => '중립',
          }} · 별점 ${item.stars}/5 · 신뢰도 ${(item.confidence! * 100).toStringAsFixed(0)}%',
        ),
        for (final reason in item.reasons)
          SelectableText(reason, semanticsLabel: reason),
        SelectableText(item.disclaimer!, semanticsLabel: item.disclaimer),
      ] else ...[
        if (item.title != null && item.title!.isNotEmpty)
          SelectableText(item.title!, semanticsLabel: item.title),
        if (item.summary != null && item.summary!.isNotEmpty)
          SelectableText(item.summary!, semanticsLabel: item.summary),
        if (item.kind == 'news')
          Text(
            item.association == 'mention'
                ? 'Mimir가 종목 언급으로 연결한 뉴스입니다.'
                : '원천의 명시적 종목 태그로 연결한 뉴스입니다.',
          ),
        const Text('원문 URL'),
        SelectableText(item.url!, semanticsLabel: item.url),
      ],
      Text('원천 ${item.source}'),
      Text('원천 시각 ${item.occurredAt}'),
      Text('수집 시각 ${item.capturedAt}'),
      if (item.sources.isEmpty) const Text('기록된 출처 목록 없음 · 사용권 승인 의미 아님'),
      for (final source in item.sources)
        ExpansionTile(
          title: Text('출처 ${source.publisher}'),
          tilePadding: EdgeInsets.zero,
          subtitle: Text(switch (source.evidence) {
            'usage_evidence_recorded' => '사용 근거 기록 있음 · 법적 승인 확인 아님',
            'builtin_discovery_only' => '발견용 출처 · 사용권 미확인',
            _ => '출처 메타데이터만 있음 · 사용권 미확인',
          }),
          children: [
            SelectableText(
              '대상 ${source.target}',
              semanticsLabel: '대상 ${source.target}',
            ),
            const Text('발견 출처 URL'),
            SelectableText(source.sourceUrl, semanticsLabel: source.sourceUrl),
            if (source.attribution != null) ...[
              SelectableText(
                source.attribution!,
                semanticsLabel: source.attribution,
              ),
              Text('저자 ${source.author}'),
              Text('권리자 ${source.rightsHolder}'),
              Text('근거 확인일 ${source.checkedOn}'),
              const Text('권리 근거 URL'),
              SelectableText(
                source.rightsSourceUrl!,
                semanticsLabel: source.rightsSourceUrl,
              ),
              const Text('약관 URL'),
              for (final url in source.termsUrls)
                SelectableText(url, semanticsLabel: url),
            ],
          ],
        ),
    ],
  );
}

class _ResearchCard extends StatelessWidget {
  const _ResearchCard({required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Card(
    semanticContainer: false,
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Semantics(
            header: true,
            child: Text(title, style: Theme.of(context).textTheme.titleMedium),
          ),
          for (final child in children) ...[const SizedBox(height: 8), child],
        ],
      ),
    ),
  );
}

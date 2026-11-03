import 'dart:convert';

typedef PublicResearchJson = Map<String, dynamic>;

class PublicResearchIdentity {
  PublicResearchIdentity({required this.market, required this.symbol}) {
    if (!const {'KR', 'US'}.contains(market) ||
        RegExp(r'^[A-Z0-9][A-Z0-9.\-]{0,31}$').stringMatch(symbol) != symbol) {
      throw const FormatException('Invalid public research identity');
    }
  }

  factory PublicResearchIdentity.fromVenue(String venue, String symbol) =>
      PublicResearchIdentity(
        market: switch (venue) {
          'XKRX' => 'KR',
          'XNAS' || 'XNYS' => 'US',
          _ => throw const FormatException('Unsupported research venue'),
        },
        symbol: symbol,
      );

  final String market, symbol;

  bool matches(PublicResearchIdentity other) =>
      market == other.market && symbol == other.symbol;
}

class PublicResearch {
  PublicResearch._({
    required this.identity,
    required this.observedAt,
    required this.generatedAt,
    required this.since,
    required this.until,
    required this.sample,
    required this.unscopedNews,
    required this.missingSourceEvidence,
    required this.items,
  });

  factory PublicResearch.fromJson(
    PublicResearchJson json,
    PublicResearchIdentity requested,
  ) {
    _keys(json, const {
      'schema_version',
      'feed_schema_version',
      'observed_at',
      'market',
      'symbol',
      'generated_at',
      'since',
      'until',
      'sample',
      'omitted',
      'items',
    });
    final identity = PublicResearchIdentity(
      market: _text(json['market'], 2),
      symbol: _text(json['symbol'], 32),
    );
    if (json['schema_version'] != 'public-research-view.v1' ||
        json['feed_schema_version'] != 'mimir-public-research.v1' ||
        !identity.matches(requested) ||
        json['sample'] is! bool) {
      throw const FormatException('Mismatched public research view');
    }
    final generated = _utc(json['generated_at']);
    final since = _date(json['since']), until = _date(json['until']);
    if (since.compareTo(until) > 0 ||
        DateTime.parse(until).difference(DateTime.parse(since)).inDays >= 31 ||
        until.compareTo(generated.substring(0, 10)) > 0) {
      throw const FormatException('Invalid public research window');
    }
    final omitted = _object(json['omitted']);
    _keys(omitted, const {'unscoped_news', 'missing_source_evidence'});
    final items = _list(
      json['items'],
      500,
    ).map((raw) => PublicResearchItem.fromJson(_object(raw))).toList();
    final ids = <String>{};
    for (final item in items) {
      if (!item.identity.matches(identity) ||
          !ids.add(item.id) ||
          DateTime.parse(item.occurredAt).isAfter(DateTime.parse(generated)) ||
          DateTime.parse(item.capturedAt).isAfter(DateTime.parse(generated)) ||
          item.occurredAt.substring(0, 10).compareTo(since) < 0 ||
          item.occurredAt.substring(0, 10).compareTo(until) > 0) {
        throw const FormatException('Inconsistent public research item');
      }
    }
    return PublicResearch._(
      identity: identity,
      observedAt: _utc(json['observed_at']),
      generatedAt: generated,
      since: since,
      until: until,
      sample: json['sample'] as bool,
      unscopedNews: _integer(omitted['unscoped_news'], 0),
      missingSourceEvidence: _integer(omitted['missing_source_evidence'], 0),
      items: List.unmodifiable(items),
    );
  }

  final PublicResearchIdentity identity;
  final String observedAt, generatedAt, since, until;
  final bool sample;
  final int unscopedNews, missingSourceEvidence;
  final List<PublicResearchItem> items;
}

class PublicResearchItem {
  PublicResearchItem._(
    this.id,
    this.kind,
    this.identity,
    this.source,
    this.occurredAt,
    this.capturedAt,
    this.sources,
    this.title,
    this.summary,
    this.url,
    this.association,
    this.asOf,
    this.direction,
    this.stars,
    this.confidence,
    this.reasons,
    this.disclaimer,
  );

  factory PublicResearchItem.fromJson(PublicResearchJson json) {
    final kind = json['kind'];
    final article = kind == 'filing' || kind == 'news';
    if (!article && kind != 'insight') {
      throw const FormatException('Unknown public research kind');
    }
    _keys(json, {
      'id',
      'kind',
      'market',
      'symbol',
      'source',
      'occurred_at',
      'captured_at',
      'sources',
      if (article) ...{'title', 'summary', 'url'},
      if (kind == 'news') 'association',
      if (!article) ...{
        'as_of',
        'direction',
        'stars',
        'confidence',
        'reasons',
        'disclaimer',
      },
    });
    final id = _text(json['id'], 64), source = _text(json['source'], 64);
    if (RegExp(r'^[0-9a-f]{64}$').stringMatch(id) != id ||
        RegExp(r'^[A-Za-z0-9][A-Za-z0-9_.\-]{0,63}$').stringMatch(source) !=
            source) {
      throw const FormatException('Invalid research identifiers');
    }
    final occurred = _utc(json['occurred_at']);
    String? association, asOf, direction;
    int? stars;
    double? confidence;
    final reasons = <String>[];
    if (kind == 'news') {
      association = _text(json['association'], 12);
      if (!const {'explicit_tag', 'mention'}.contains(association)) {
        throw const FormatException('Invalid news association');
      }
    }
    if (!article) {
      asOf = _date(json['as_of']);
      direction = _text(json['direction'], 7);
      stars = _integer(json['stars'], 1, 5);
      final score = json['confidence'];
      if (score is! num ||
          !score.isFinite ||
          score < 0 ||
          score > 1 ||
          !const {'bullish', 'bearish', 'neutral'}.contains(direction) ||
          asOf != occurred.substring(0, 10)) {
        throw const FormatException('Invalid research opinion');
      }
      confidence = score.toDouble();
      reasons.addAll(_list(json['reasons'], 10).map((v) => _text(v, 2048)));
    }
    return PublicResearchItem._(
      id,
      kind as String,
      PublicResearchIdentity(
        market: _text(json['market'], 2),
        symbol: _text(json['symbol'], 32),
      ),
      source,
      occurred,
      _utc(json['captured_at']),
      List.unmodifiable(
        _list(
          json['sources'],
          20,
        ).map((v) => ResearchSourceEvidence.fromJson(_object(v))),
      ),
      article && json['title'] != null
          ? _text(json['title'], 512, empty: true)
          : null,
      article && json['summary'] != null
          ? _text(json['summary'], 2048, empty: true)
          : null,
      article ? _url(json['url']) : null,
      association,
      asOf,
      direction,
      stars,
      confidence,
      List.unmodifiable(reasons),
      article ? null : _text(json['disclaimer'], 2048),
    );
  }

  final String id, kind, source, occurredAt, capturedAt;
  final PublicResearchIdentity identity;
  final List<ResearchSourceEvidence> sources;
  final String? title, summary, url, association, asOf, direction, disclaimer;
  final int? stars;
  final double? confidence;
  final List<String> reasons;
}

class ResearchSourceEvidence {
  ResearchSourceEvidence._(
    this.publisher,
    this.target,
    this.sourceUrl,
    this.evidence,
    this.attribution,
    this.author,
    this.rightsHolder,
    this.rightsSourceUrl,
    this.checkedOn,
    this.termsUrls,
  );

  factory ResearchSourceEvidence.fromJson(PublicResearchJson json) {
    final evidence = json['evidence'];
    final usage = evidence == 'usage_evidence_recorded';
    if (!usage &&
        evidence != 'metadata_only' &&
        evidence != 'builtin_discovery_only') {
      throw const FormatException('Invalid research evidence');
    }
    _keys(json, {
      'publisher',
      'target',
      'source_url',
      'evidence',
      if (usage) ...{
        'attribution',
        'author',
        'rights_holder',
        'rights_source_url',
        'checked_on',
        'terms_urls',
      },
    });
    final terms = usage
        ? _list(json['terms_urls'], 10).map(_url).toList()
        : <String>[];
    if (usage && terms.isEmpty) {
      throw const FormatException('Missing research terms');
    }
    final target = _text(json['target'], 2048);
    if (target.contains('://')) {
      _url(target);
    }
    return ResearchSourceEvidence._(
      _text(json['publisher'], 256),
      target,
      _url(json['source_url']),
      evidence as String,
      usage ? _text(json['attribution'], 2048) : null,
      usage ? _text(json['author'], 2048) : null,
      usage ? _text(json['rights_holder'], 2048) : null,
      usage ? _url(json['rights_source_url']) : null,
      usage ? _date(json['checked_on']) : null,
      List.unmodifiable(terms),
    );
  }

  final String publisher, target, sourceUrl, evidence;
  final String? attribution, author, rightsHolder, rightsSourceUrl, checkedOn;
  final List<String> termsUrls;
}

void _keys(PublicResearchJson json, Set<String> keys) {
  if (json.length != keys.length || !keys.every(json.containsKey)) {
    throw const FormatException('Unexpected public research fields');
  }
}

PublicResearchJson _object(dynamic value) {
  if (value is! PublicResearchJson) {
    throw const FormatException('Invalid research object');
  }
  return value;
}

List<dynamic> _list(dynamic value, int max) {
  if (value is! List || value.length > max) {
    throw const FormatException('Invalid research list');
  }
  return value;
}

int _integer(dynamic value, int min, [int? max]) {
  if (value is! int || value < min || (max != null && value > max)) {
    throw const FormatException('Invalid research integer');
  }
  return value;
}

String _text(dynamic value, int max, {bool empty = false}) {
  if (value is! String ||
      (!empty && value.trim().isEmpty) ||
      utf8.encode(value).length > max ||
      value.runes.any(
        (r) =>
            (r < 32 && r != 9 && r != 10) ||
            (r >= 127 && r <= 159) ||
            (r >= 0xd800 && r <= 0xdfff),
      )) {
    throw const FormatException('Invalid public research text');
  }
  return value;
}

String _url(dynamic value) {
  final text = _text(value, 2048);
  final uri = Uri.tryParse(text);
  if (uri == null ||
      !const {'http', 'https'}.contains(uri.scheme) ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.port > 65535 ||
      RegExp(r'[\s<>"\\]').hasMatch(text) ||
      uri.queryParameters.keys.any(
        (key) => const {
          'key',
          'api_key',
          'apikey',
          'token',
          'access_token',
          'secret',
          'password',
        }.contains(key.toLowerCase()),
      )) {
    throw const FormatException('Unsafe research URL');
  }
  return text;
}

String _date(dynamic value) {
  final text = _text(value, 10);
  final date = DateTime.tryParse(text);
  if (!RegExp(r'^[0-9]{4}-[0-9]{2}-[0-9]{2}$').hasMatch(text) ||
      date == null ||
      date.year < 1 ||
      date.toIso8601String().substring(0, 10) != text) {
    throw const FormatException('Invalid research date');
  }
  return text;
}

String _utc(dynamic value) {
  final text = _text(value, 30);
  if (!RegExp(
        r'^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$',
      ).hasMatch(text) ||
      DateTime.tryParse(text) == null) {
    throw const FormatException('Invalid research timestamp');
  }
  _date(text.substring(0, 10));
  final time = text.substring(11, 19).split(':').map(int.parse).toList();
  if (time[0] > 23 || time[1] > 59 || time[2] > 59) {
    throw const FormatException('Invalid research time');
  }
  return text;
}

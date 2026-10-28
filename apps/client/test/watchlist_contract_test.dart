import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/models.dart';

Json _fixture(String name) =>
    (jsonDecode(
              File(
                '../../contracts/fixtures/golden-watchlist.json',
              ).readAsStringSync(),
            )
            as Json)['reports'][name]
        as Json;
Json _copy(Json value) => jsonDecode(jsonEncode(value)) as Json;

class _OversizedClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async =>
      http.StreamedResponse(
        Stream.value(List<int>.filled(128 * 1024 + 1, 32)),
        200,
      );
}

void main() {
  test(
    'server-produced shared states preserve exact native prices and provenance',
    () {
      final empty = WatchlistReport.fromJson(_fixture('empty'));
      expect(empty.watchlist.revision, 0);
      expect(empty.quotes, isEmpty);
      final mixed = WatchlistReport.fromJson(_fixture('mixed'));
      expect(mixed.quotes.map((q) => q.status), [
        'stale',
        'unknown',
        'missing',
        'ambiguous',
      ]);
      expect(mixed.quotes[0].entry.currency, 'KRW');
      expect(mixed.quotes[0].price!.price, '70000');
      expect(mixed.quotes[1].entry.currency, 'USD');
      expect(mixed.quotes[1].price!.price, '150.25');
      expect(mixed.quotes[1].price!.sourceVersion, 'price-v1');
      expect(mixed.quotes[1].price!.sample, isTrue);
      expect(() => mixed.watchlist.entries.clear(), throwsUnsupportedError);
      expect(() => mixed.quotes.clear(), throwsUnsupportedError);
    },
  );

  test(
    'closed report rejects altered identity, state, provenance and nanosecond age',
    () {
      final mutations = <void Function(Json)>[
        (j) => j['extra'] = true,
        (j) => j['policy_version'] = 'live',
        (j) => j['max_observation_age_seconds'] = 86400.0,
        (j) => j['watchlist']['revision'] = -1,
        (j) => j['watchlist']['revision'] = 9007199254740992,
        (j) => j['watchlist']['updated_at'] = null,
        (j) => j['watchlist']['updated_at'] = '2026-01-02T00:00:00.000000001Z',
        (j) => j['watchlist']['entries'][0]['currency'] = 'USD',
        (j) => j['watchlist']['entries'][1]['symbol'] = 'aapl',
        (j) => j['watchlist']['entries'][1]['symbol'] = 'AAPL\n',
        (j) => j['watchlist']['entries'].add(j['watchlist']['entries'][0]),
        (j) => j['watchlist']['entries'] = (j['watchlist']['entries'] as List)
            .reversed
            .toList(),
        (j) => j['quotes'].removeLast(),
        (j) => j['quotes'][0]['symbol'] = '000000',
        (j) => j['quotes'][0]['price']['source'] = 'kiwoom_production',
        (j) => j['quotes'][0]['price']['sample'] = false,
        (j) => j['quotes'][0]['price']['source_version'] = 'imputed',
        (j) => j['quotes'][0]['price']['observation_id'] = 'private',
        (j) => j['quotes'][0]['status'] = 'unknown',
        (j) => j['quotes'][1]['price']['source_version'] = null,
        (j) => j['quotes'][1]['price']['price'] = '150.250',
        (j) => j['quotes'][1]['price']['price'] = '0',
        (j) => j['quotes'][1]['price']['price'] = '-1',
        (j) => j['quotes'][1]['price']['price_adjustment'] = 'split_adjusted',
        (j) => j['quotes'][1]['price']['recorded_at'] =
            '2026-01-02T00:00:00.000000001Z',
        (j) => j['quotes'][1]['price']['observed_at'] =
            '2025-12-31T23:59:59.999999999Z',
        (j) => j['quotes'][2]['price'] = j['quotes'][0]['price'],
        (j) => j['quotes'][3]['status'] = 'unknown',
      ];
      for (final mutate in mutations) {
        final bad = _copy(_fixture('mixed'));
        mutate(bad);
        expect(() => WatchlistReport.fromJson(bad), throwsFormatException);
      }
      final boundary = _copy(_fixture('mixed'));
      boundary['quotes'][1]['price']['observed_at'] = '2026-01-01T00:00:00Z';
      expect(WatchlistReport.fromJson(boundary).quotes[1].status, 'unknown');
      boundary['quotes'][1]['price']['observed_at'] =
          '2025-12-31T23:59:59.999999999Z';
      boundary['quotes'][1]['status'] = 'stale';
      expect(WatchlistReport.fromJson(boundary).quotes[1].status, 'stale');
    },
  );

  test(
    'HTTP GET and exact CAS POST are bounded and validate matching acknowledgements',
    () async {
      final mixed = WatchlistReport.fromJson(_fixture('mixed'));
      final change = WatchlistChange(
        expectedRevision: 0,
        entries: mixed.watchlist.entries.reversed.toList(),
      );
      var reads = 0, writes = 0;
      final client = MockClient((request) async {
        expect(request.url.path, '/v1/watchlist');
        expect(request.url.hasQuery, isFalse);
        if (request.method == 'GET') {
          reads++;
          return http.Response(jsonEncode(_fixture('mixed')), 200);
        }
        writes++;
        expect(request.method, 'POST');
        expect(request.headers['content-type'], startsWith('application/json'));
        expect(jsonDecode(request.body), change.toJson());
        return http.Response(jsonEncode(_fixture('mixed')['watchlist']), 200);
      });
      addTearDown(client.close);
      final api = RestOmniApi(client: client, baseUrl: 'http://localhost:8080');
      expect((await api.watchlist()).quotes[1].price!.price, '150.25');
      expect((await api.updateWatchlist(change)).revision, 1);
      expect(reads, 1);
      expect(writes, 1);
      final bad = MockClient(
        (_) async =>
            http.Response(jsonEncode(_fixture('empty')['watchlist']), 200),
      );
      addTearDown(bad.close);
      await expectLater(
        RestOmniApi(client: bad).updateWatchlist(change),
        throwsFormatException,
      );
      final over = _OversizedClient();
      addTearDown(over.close);
      await expectLater(
        RestOmniApi(client: over).watchlist(),
        throwsFormatException,
      );
      await expectLater(
        RestOmniApi(client: over).updateWatchlist(change),
        throwsFormatException,
      );
      for (final code in [400, 403, 409, 503]) {
        final error = MockClient(
          (_) async => http.Response(
            jsonEncode({
              'code': code == 409
                  ? 'watchlist_changed'
                  : 'watchlist_unavailable',
              'message': 'upstream-private',
            }),
            code,
          ),
        );
        try {
          await expectLater(
            RestOmniApi(client: error).updateWatchlist(change),
            throwsA(
              isA<ApiException>().having((e) => e.statusCode, 'status', code),
            ),
          );
        } finally {
          error.close();
        }
      }
    },
  );
}

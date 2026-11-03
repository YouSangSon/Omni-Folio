import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/models.dart';

Json currentPortfolioValuationJson({
  String status = 'stale_sample',
  bool sample = true,
  String ledgerRevision = 'rev_0000000003',
  Object? cashValue = const {'currency': 'KRW', 'amount': '95530'},
  Object? holdingsValue = const {'currency': 'KRW', 'amount': '61650'},
  Object? totalValue = const {'currency': 'KRW', 'amount': '157180'},
  List<Json>? issues,
}) => {
  'scope': 'whole_portfolio',
  'policy_version': 'current_base_portfolio_valuation_v1',
  'max_observation_age_seconds': 86400,
  'base_currency': 'KRW',
  'valuation_as_of': '2026-01-11T14:00:00Z',
  'ledger_revision': ledgerRevision,
  'ledger_as_of': '2026-01-10T10:00:00Z',
  'ledger_recorded_at': '2026-01-10T15:01:00Z',
  'status': status,
  'sample': sample,
  'cash_value': cashValue,
  'holdings_value': holdingsValue,
  'total_value': totalValue,
  'issues':
      issues ??
      const [
        {
          'code': 'sample_data',
          'message': 'portfolio valuation uses stored local sample data',
        },
      ],
};

void main() {
  test('current portfolio valuation parses a closed exact aggregate', () {
    final valuation = CurrentPortfolioValuation.fromJson(
      currentPortfolioValuationJson(),
    );

    expect(valuation.status, 'stale_sample');
    expect(valuation.baseCurrency, 'KRW');
    expect(valuation.totalValue?.amount, '157180');
    expect(valuation.cashValue?.amount, '95530');
    expect(valuation.holdingsValue?.amount, '61650');
    expect(valuation.issues.single.code, 'sample_data');
  });

  test('current portfolio valuation rejects drifted authority and math', () {
    final valid = currentPortfolioValuationJson();
    for (final mutation in <Json>[
      {...valid, 'private_account_id': 'secret'},
      {...valid, 'base_currency': 'USD'},
      {...valid, 'valuation_as_of': '2026-01-11T23:00:00+09:00'},
      {...valid, 'ledger_revision': '3'},
      {
        ...valid,
        'total_value': {'currency': 'KRW', 'amount': '157181'},
      },
      {
        ...valid,
        'holdings_value': {'currency': 'USD', 'amount': '61650'},
      },
      {...valid, 'status': 'complete'},
      {...valid, 'status': 'complete', 'sample': false, 'issues': const []},
      {...valid, 'sample': false},
      {...valid, 'issues': const []},
      {
        ...valid,
        'status': 'unavailable',
        'cash_value': {'currency': 'KRW', 'amount': '95530'},
        'holdings_value': null,
        'total_value': null,
        'issues': const [
          {
            'code': 'missing_security_price',
            'message': 'price missing',
            'field': 'AAPL',
          },
        ],
      },
    ]) {
      expect(
        () => CurrentPortfolioValuation.fromJson(mutation),
        throwsFormatException,
        reason: mutation.toString(),
      );
    }
  });

  test(
    'current portfolio valuation accepts exact empty and cash-only states',
    () {
      final empty = CurrentPortfolioValuation.fromJson(
        currentPortfolioValuationJson(
          status: 'empty',
          sample: false,
          cashValue: null,
          holdingsValue: null,
          totalValue: null,
          issues: const [],
        ),
      );
      final cashOnly = CurrentPortfolioValuation.fromJson(
        currentPortfolioValuationJson(
          status: 'complete',
          sample: false,
          cashValue: const {
            'currency': 'KRW',
            'amount': '999999999999999999.1',
          },
          holdingsValue: const {'currency': 'KRW', 'amount': '0'},
          totalValue: const {
            'currency': 'KRW',
            'amount': '999999999999999999.1',
          },
          issues: const [],
        ),
      );

      expect(empty.status, 'empty');
      expect(cashOnly.status, 'complete');
      expect(cashOnly.totalValue?.amount, '999999999999999999.1');
    },
  );

  test(
    'REST client requests current valuation without query parameters',
    () async {
      late http.Request request;
      final api = RestOmniApi(
        baseUrl: 'http://example.test',
        client: MockClient((value) async {
          request = value;
          return http.Response(
            jsonEncode(currentPortfolioValuationJson()),
            200,
          );
        }),
      );

      final valuation = await api.currentPortfolioValuation();

      expect(request.method, 'GET');
      expect(request.url.path, '/v1/portfolio/current-valuation');
      expect(request.url.query, isEmpty);
      expect(valuation.totalValue?.amount, '157180');
    },
  );
}

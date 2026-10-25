import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';
import 'paper_monitor.dart';
import 'research_report.dart';

abstract interface class OmniApi {
  Future<ServiceStatus> status();
  Future<PortfolioSnapshot> snapshot();
  Future<CurrentPortfolioValuation> currentPortfolioValuation();
  Future<HoldingValuation> holdingValuation();
  Future<InstrumentLedger> instrumentLedger(
    String instrumentId,
    String currency,
  );
  Future<MarketCandles> candles(String symbol);
  Future<BrokerReconciliation?> latestBrokerReconciliation();
  Future<LocalOrderLog> localOrders();
  Future<PaperMonitor> paperMonitor();
  Future<ResearchReport> researchReport();
  Future<LedgerActivityPage> ledgerActivities({String? cursor});
  Future<ImportPreview> preview(String csv);
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey);
}

const defaultApiUrl = String.fromEnvironment(
  'OMNI_API_URL',
  defaultValue: 'http://127.0.0.1:8080',
);
const apiConnectionError = 'API에 연결할 수 없습니다. 서버 주소를 확인한 뒤 다시 시도하세요.';

class ApiException implements Exception {
  const ApiException(this.message, {this.statusCode, this.code});
  final String message;
  final int? statusCode;
  final String? code;

  bool get isConfirmedImportRejection =>
      (statusCode == 409 && code == 'stale_preview') ||
      (statusCode == 400 &&
          const {
            'preview_not_found',
            'preview_has_errors',
            'invalid_ledger',
          }.contains(code));
  @override
  String toString() => message;
}

class RestOmniApi implements OmniApi {
  RestOmniApi({http.Client? client, String? baseUrl})
    : _client = client ?? http.Client(),
      baseUrl = (baseUrl ?? defaultApiUrl).replaceFirst(RegExp(r'/$'), '');

  final http.Client _client;
  final String baseUrl;

  Uri _uri(String path) => Uri.parse('$baseUrl$path');

  Uri _marketCandlesUri(String symbol) => _uri(
    '/v1/market-data/candles',
  ).replace(queryParameters: {'symbol': symbol, 'interval': '1d'});

  Future<Json> _get(String path) => _getUri(_uri(path));

  Future<Json> _getUri(Uri uri) async {
    try {
      final response = await _client.get(uri);
      return _object(response);
    } catch (error) {
      if (error is ApiException || error is FormatException) rethrow;
      throw const ApiException(apiConnectionError);
    }
  }

  Future<Json> _post(
    String path, {
    required String contentType,
    required Object body,
  }) async {
    try {
      final response = await _client.post(
        _uri(path),
        headers: {'content-type': contentType},
        body: body,
      );
      return _object(response);
    } catch (error) {
      if (error is ApiException || error is FormatException) rethrow;
      throw const ApiException(apiConnectionError);
    }
  }

  Json _object(http.Response response) {
    if (response.statusCode >= 200 && response.statusCode < 300) {
      return decodeObject(response.body);
    }
    try {
      final error = decodeObject(response.body);
      throw ApiException(
        error['message'] is String
            ? error['message'] as String
            : '요청을 완료하지 못했습니다.',
        statusCode: response.statusCode,
        code: error['code'] is String ? error['code'] as String : null,
      );
    } on FormatException {
      throw ApiException('요청을 완료하지 못했습니다 (${response.statusCode}).');
    }
  }

  @override
  Future<ServiceStatus> status() async =>
      ServiceStatus.fromJson(await _get('/v1/status'));

  @override
  Future<PortfolioSnapshot> snapshot() async =>
      PortfolioSnapshot.fromJson(await _get('/v1/portfolio/snapshot'));

  @override
  Future<CurrentPortfolioValuation> currentPortfolioValuation() async =>
      CurrentPortfolioValuation.fromJson(
        await _get('/v1/portfolio/current-valuation'),
      );

  @override
  Future<HoldingValuation> holdingValuation() async =>
      HoldingValuation.fromJson(await _get('/v1/portfolio/holding-valuation'));

  @override
  Future<InstrumentLedger> instrumentLedger(
    String instrumentId,
    String currency,
  ) async {
    final view = InstrumentLedger.fromJson(
      await _getUri(
        _uri('/v1/portfolio/instrument-ledger').replace(
          queryParameters: {
            'instrument_id': instrumentId,
            'currency': currency,
          },
        ),
      ),
    );
    if (view.instrumentId != instrumentId || view.currency != currency) {
      throw const FormatException('Instrument ledger does not match request');
    }
    return view;
  }

  @override
  Future<MarketCandles> candles(String symbol) async {
    if (symbol.isEmpty) throw const ApiException('종목 코드가 필요합니다.');
    try {
      final response = await _client.get(_marketCandlesUri(symbol));
      final candles = MarketCandles.fromJson(_object(response));
      if (candles.symbol != symbol || candles.interval != '1d') {
        throw const FormatException('Market response does not match request');
      }
      return candles;
    } catch (error) {
      if (error is ApiException || error is FormatException) rethrow;
      throw const ApiException(apiConnectionError);
    }
  }

  @override
  Future<BrokerReconciliation?> latestBrokerReconciliation() async {
    try {
      final response = await _client.get(
        _uri('/v1/broker-reconciliation/latest'),
      );
      if (response.statusCode == 404) {
        final error = decodeObject(response.body);
        if (error['code'] == 'broker_reconciliation_not_found') return null;
      }
      return BrokerReconciliation.fromJson(_object(response));
    } catch (error) {
      if (error is ApiException || error is FormatException) rethrow;
      throw const ApiException(apiConnectionError);
    }
  }

  @override
  Future<LocalOrderLog> localOrders() async =>
      LocalOrderLog.fromJson(await _get('/v1/orders'));

  @override
  Future<PaperMonitor> paperMonitor() async =>
      PaperMonitor.fromJson(await _get('/v1/paper/monitor'));

  @override
  Future<ResearchReport> researchReport() async =>
      ResearchReport.fromJson(await _get('/v1/research/report'));

  @override
  Future<LedgerActivityPage> ledgerActivities({String? cursor}) async {
    final uri = _uri(
      '/v1/ledger/activities',
    ).replace(queryParameters: cursor == null ? null : {'cursor': cursor});
    return LedgerActivityPage.fromJson(await _getUri(uri));
  }

  @override
  Future<ImportPreview> preview(String csv) async {
    return ImportPreview.fromJson(
      await _post('/v1/imports/preview', contentType: 'text/csv', body: csv),
    );
  }

  @override
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey) async {
    return ApplyReceipt.fromJson(
      await _post(
        '/v1/imports/apply',
        contentType: 'application/json',
        body: jsonEncode({
          'preview_id': previewId,
          'idempotency_key': idempotencyKey,
        }),
      ),
    );
  }
}

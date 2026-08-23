import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';

abstract interface class OmniApi {
  Future<ServiceStatus> status();
  Future<PortfolioSnapshot> snapshot();
  Future<ImportPreview> preview(String csv);
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey);
}

const defaultApiUrl = String.fromEnvironment(
  'OMNI_API_URL',
  defaultValue: 'http://127.0.0.1:8080',
);
const apiConnectionError = 'API에 연결할 수 없습니다. 서버 주소를 확인한 뒤 다시 시도하세요.';

class ApiException implements Exception {
  const ApiException(this.message);
  final String message;
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

  Future<Json> _get(String path) async {
    try {
      final response = await _client.get(_uri(path));
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

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/csv_file.dart';
import 'package:omni_folio_client/models.dart';
import 'package:omni_folio_client/owner_data_import.dart';
import 'package:omni_folio_client/owner_data_import_page.dart';

import 'vertical_slice_test.dart' show goldenApi;

List<Json> cases() =>
    (jsonDecode(
              File(
                '../../contracts/fixtures/golden-owner-data-import.json',
              ).readAsStringSync(),
            )
            as List)
        .cast<Json>();
Json copy(Json value) => jsonDecode(jsonEncode(value)) as Json;
OwnerDataPreview review(Json c) => OwnerDataPreview.fromJson(
  c['preview'] as Json,
  (c['preview'] as Json)['input'] as String,
);

class _Api implements OmniApi {
  Future<OwnerDataPreview> Function(String)? previewHandler;
  Future<OwnerDataReceipt> Function(OwnerDataPreview)? applyHandler;
  @override
  Future<OwnerDataPreview> previewOwnerData(String input) =>
      previewHandler!(input);
  @override
  Future<OwnerDataReceipt> applyOwnerData(OwnerDataPreview input) =>
      applyHandler!(input);
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _RefreshApi implements OmniApi {
  final base = goldenApi();
  final statusGate = Completer<ServiceStatus>();
  final reconciliationGate = Completer<BrokerReconciliation?>();
  var currentCalls = 0;
  @override
  Future<ServiceStatus> status() => statusGate.future;
  @override
  Future<PortfolioSnapshot> snapshot() => base.snapshot();
  @override
  Future<CurrentPortfolioValuation> currentPortfolioValuation() {
    currentCalls++;
    return base.currentPortfolioValuation();
  }

  @override
  Future<BrokerReconciliation?> latestBrokerReconciliation() =>
      reconciliationGate.future;
  @override
  Future<LocalOrderLog> localOrders() => base.localOrders();
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _WaitingClient extends http.BaseClient {
  final response = Completer<http.StreamedResponse>();
  bool aborted = false;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    (request as http.Abortable).abortTrigger!.then((_) {
      aborted = true;
      response.completeError(http.RequestAbortedException());
    });
    return response.future;
  }
}

void main() {
  test(
    'all shared producer wire variants bind exact input and matching receipt',
    () async {
      for (final c in cases()) {
        final original = review(c);
        final calls = <String>[];
        final api = RestOmniApi(
          client: MockClient((request) async {
            expect(request.method, 'POST');
            expect(request.headers['content-type'], 'application/json');
            expect(request.body, original.input);
            calls.add(request.url.path);
            return http.Response(
              jsonEncode(
                c[request.url.path.endsWith('preview') ? 'preview' : 'result'],
              ),
              200,
              headers: {'content-type': 'application/json; charset=utf-8'},
            );
          }),
        );
        final preview = await api.previewOwnerData(original.input);
        final result = await api.applyOwnerData(preview);
        expect(result.review.summary, original.summary);
        expect(calls, [
          '/v1/market-data/imports/preview',
          '/v1/market-data/imports/apply',
        ]);
      }
    },
  );

  test(
    'wrong input, metadata, authority and malformed receipts stay invalid',
    () {
      final c = cases().first;
      final original = review(c);
      final mutations = <void Function(Json)>[
        (j) => j['input'] = '${j['input']} ',
        (j) => j['kind'] = 'fx',
        (j) => j['summary']['storage_check'] = 'verified',
        (j) => j['summary']['data_sha256'] = 'wrong',
        (j) => j['summary']['point_count'] = 1,
        (j) => j['summary']['sample'] = true,
        (j) => j['summary']['authority'] = 'provider',
        (j) => j['summary']['last_observed_at'] = '2027-01-01T00:00:00Z',
      ];
      for (final change in mutations) {
        final altered = copy(c['preview'] as Json);
        change(altered);
        expect(
          () => OwnerDataPreview.fromJson(altered, original.input),
          throwsFormatException,
        );
      }
      for (final change in <void Function(Json)>[
        (j) => j['kind'] = 'fx',
        (j) => j['receipt']['source_version'] = 'other',
        (j) => j['receipt']['storage_check'] = 'unchecked',
        (j) => j['receipt']['recorded_at'] = '2025-01-01T00:00:00Z',
        (j) => j['receipt']['extra'] = true,
      ]) {
        final altered = copy(c['result'] as Json);
        change(altered);
        expect(
          () => OwnerDataReceipt.fromJson(altered, original),
          throwsFormatException,
        );
      }
      expect(() => original.summary['sample'] = true, throwsUnsupportedError);
    },
  );

  test(
    'bounded UTF8 reader supports Android memory files and rejects malformed bytes',
    () async {
      final raw = review(cases().first).input;
      expect(
        await readUtf8File(
          XFile.fromData(
            Uint8List.fromList(utf8.encode(raw)),
            path: 'input.json',
          ),
        ),
        raw,
      );
      await expectLater(
        readUtf8File(
          XFile.fromData(Uint8List.fromList([0xc3, 0x28]), path: 'bad.json'),
        ),
        throwsFormatException,
      );
      await expectLater(
        readUtf8File(
          XFile.fromData(Uint8List((1 << 20) + 1), path: 'large.json'),
        ),
        throwsRangeError,
      );
    },
  );

  test(
    'stale preview and picker callbacks cannot attach to a newer draft; cancel preserves review',
    () async {
      final a = review(cases()[0]), b = review(cases()[1]);
      final latePreview = Completer<OwnerDataPreview>();
      final api = _Api()..previewHandler = (_) => latePreview.future;
      final ctl = OwnerDataImportController(api, onStored: () {});
      addTearDown(ctl.dispose);
      await ctl.selectFile(() async => a.input);
      final task = ctl.preview();
      await ctl.selectFile(() async => b.input);
      latePreview.complete(a);
      await task;
      expect(ctl.input, b.input);
      expect(ctl.review, isNull);
      api.previewHandler = (_) async => b;
      await ctl.preview();
      await ctl.selectFile(() async => null);
      expect(ctl.review, b);
      await ctl.selectFile(() async => throw Exception('private-sentinel'));
      expect(ctl.review, b);
      final latePicker = Completer<String?>();
      final pick = ctl.selectFile(() => latePicker.future);
      await ctl.selectFile(() async => a.input);
      latePicker.complete(b.input);
      await pick;
      expect(ctl.input, a.input);
    },
  );

  test(
    'uncertainty survives later confirmed rejection and retries immutable original until matching receipt',
    () async {
      final c = cases().first, r = review(cases().first);
      var attempt = 0, stored = 0;
      final posted = <String>[];
      final api = _Api()..previewHandler = (_) async => r;
      api.applyHandler = (request) async {
        posted.add(request.input);
        attempt++;
        if (attempt == 1) throw const ApiException('network');
        if (attempt == 2) {
          throw const ApiException(
            'rejected',
            statusCode: 409,
            code: 'owner_data_conflict',
          );
        }
        return OwnerDataReceipt.fromJson(c['result'] as Json, request);
      };
      final ctl = OwnerDataImportController(api, onStored: () => stored++);
      addTearDown(ctl.dispose);
      await ctl.selectFile(() async => r.input);
      await ctl.preview();
      expect(posted, isEmpty);
      await ctl.apply();
      expect(ctl.pending, r);
      expect(stored, 0);
      await ctl.selectFile(() async => 'different');
      expect(ctl.input, r.input);
      await ctl.apply();
      expect(ctl.pending, r);
      expect(stored, 0);
      await ctl.apply();
      expect(ctl.pending, isNull);
      expect(stored, 1);
      expect(posted, [r.input, r.input, r.input]);
    },
  );

  test(
    'first confirmed rejection releases draft, malformed 2xx stays pending and double click sends once',
    () async {
      final c = cases().first, r = review(cases().first);
      final api = _Api()..previewHandler = (_) async => r;
      final ctl = OwnerDataImportController(api, onStored: () {});
      addTearDown(ctl.dispose);
      await ctl.selectFile(() async => r.input);
      await ctl.preview();
      api.applyHandler = (_) async => throw const ApiException(
        'invalid',
        statusCode: 400,
        code: 'owner_data_invalid',
      );
      await ctl.apply();
      expect(ctl.pending, isNull);
      expect(ctl.review, isNull);
      await ctl.preview();
      final wait = Completer<OwnerDataReceipt>();
      var calls = 0;
      api.applyHandler = (_) {
        calls++;
        return wait.future;
      };
      final first = ctl.apply();
      await ctl.apply();
      expect(calls, 1);
      wait.completeError(const FormatException('private-sentinel'));
      await first;
      expect(ctl.pending, r);
      api.applyHandler = (request) async =>
          OwnerDataReceipt.fromJson(c['result'] as Json, request);
      await ctl.apply();
      expect(ctl.receipt, isNotNull);
    },
  );

  test(
    'transport rejects oversized responses and malformed successful writes',
    () async {
      final r = review(cases().first);
      for (final body in ['{}', 'x' * (6 * (1 << 20) + 16 * 1024 + 1)]) {
        final api = RestOmniApi(
          client: MockClient((_) async => http.Response(body, 200)),
        );
        await expectLater(api.applyOwnerData(r), throwsFormatException);
      }
    },
  );

  testWidgets(
    'root connections exposes owner file page without selecting or storing',
    (tester) async {
      await tester.pumpWidget(OmniFolioApp(api: goldenApi()));
      await tester.pumpAndSettle();
      await tester.tap(find.text('연결').last);
      await tester.pumpAndSettle();
      final entry = find.text('가격·환율·지수 파일 가져오기');
      await tester.ensureVisible(entry);
      await tester.tap(entry);
      await tester.pumpAndSettle();
      expect(find.text('JSON 파일 선택'), findsOneWidget);
      expect(find.text('검토한 파일 저장'), findsNothing);
      await tester.pageBack();
      await tester.pumpAndSettle();
      expect(find.text('가격·환율·지수 파일 가져오기'), findsOneWidget);
    },
  );

  test(
    'post-store refresh waits for precommit valuation then coalesces one fresh read',
    () async {
      final api = goldenApi();
      final old = Completer<CurrentPortfolioValuation>();
      api.currentValuationCompleter = old;
      final ctl = PortfolioController(api);
      addTearDown(ctl.dispose);
      final precommit = ctl.refresh();
      expect(api.currentValuationCalls, 1);
      ctl.refreshAfterMarketDataImport();
      ctl.refreshAfterMarketDataImport();
      await Future<void>.delayed(Duration.zero);
      expect(api.currentValuationCalls, 1);
      api.currentValuationCompleter = null;
      old.completeError(const ApiException('precommit read failed'));
      await precommit;
      await Future<void>.delayed(Duration.zero);
      expect(api.currentValuationCalls, 2);
      expect(ctl.busy, isFalse);
    },
  );

  test(
    'post-store refresh does not await unrelated precommit reconciliation',
    () async {
      final api = _RefreshApi();
      final ctl = PortfolioController(api);
      addTearDown(ctl.dispose);
      final precommit = ctl.refresh();
      await Future<void>.delayed(Duration.zero);
      expect(ctl.currentValuationBusy, isFalse);
      expect(ctl.busy, isTrue);
      ctl.refreshAfterMarketDataImport();
      api.statusGate.complete(await api.base.status());
      await Future<void>.delayed(Duration.zero);
      expect(api.reconciliationGate.isCompleted, isFalse);
      expect(api.currentCalls, 2);
      api.reconciliationGate.complete(null);
      await precommit;
    },
  );

  testWidgets(
    'owner waiting limit aborts transport but leaves storage uncertain',
    (tester) async {
      final client = _WaitingClient();
      final api = RestOmniApi(client: client);
      final call = api.applyOwnerData(review(cases().first));
      final checked = expectLater(call, throwsA(isA<ApiException>()));
      await tester.pump(const Duration(seconds: 41));
      await checked;
      expect(client.aborted, isTrue);
    },
  );

  for (final (dark, size) in [
    (false, const Size(375, 812)),
    (true, const Size(375, 812)),
    (false, const Size(812, 375)),
    (true, const Size(812, 375)),
  ]) {
    testWidgets(
      'owner file review supports ${size.width}x${size.height} large-text ${dark ? 'dark' : 'light'} layout and explicit store',
      (tester) async {
        tester.view.physicalSize = size;
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final semantics = tester.ensureSemantics();
        try {
          final c = cases().first, r = review(cases().first);
          var calls = 0;
          final api = _Api()..previewHandler = (_) async => r;
          api.applyHandler = (request) async {
            calls++;
            return OwnerDataReceipt.fromJson(c['result'] as Json, request);
          };
          final ctl = OwnerDataImportController(api, onStored: () {});
          await ctl.selectFile(() async => r.input);
          await ctl.preview();
          await tester.pumpWidget(
            MaterialApp(
              theme: ThemeData(
                brightness: dark ? Brightness.dark : Brightness.light,
              ),
              builder: (context, child) => MediaQuery(
                data: MediaQuery.of(context).copyWith(
                  textScaler: const TextScaler.linear(2),
                  disableAnimations: true,
                ),
                child: child!,
              ),
              home: OwnerDataImportPage(controller: ctl),
            ),
          );
          await tester.pumpAndSettle();
          expect(tester.takeException(), isNull);
          expect(calls, 0);
          await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
          final button = find.text('검토한 파일 저장');
          await tester.scrollUntilVisible(button, 180);
          await tester.tap(button);
          await tester.pumpAndSettle();
          expect(calls, 1);
          expect(ctl.receipt, isNotNull);
          expect(tester.takeException(), isNull);
          await tester.pumpWidget(const SizedBox.shrink());
          ctl.dispose();
        } finally {
          semantics.dispose();
        }
      },
    );
  }
}

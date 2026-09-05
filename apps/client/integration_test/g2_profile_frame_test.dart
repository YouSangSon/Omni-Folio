import 'dart:ui' show FrameTiming;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:omni_folio_client/api.dart';
import 'package:omni_folio_client/app.dart';
import 'package:omni_folio_client/models.dart';

const _frameBudgetMs = 16.67;
const _flutterVersion = String.fromEnvironment('G2_FLUTTER_VERSION');
const _deviceName = String.fromEnvironment('G2_DEVICE');
const _osVersion = String.fromEnvironment('G2_OS_VERSION');

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('G2 representative fixture stays within the 60 Hz frame budget', (
    tester,
  ) async {
    expect(
      kProfileMode,
      isTrue,
      reason: 'Run this check with flutter drive --profile.',
    );
    expect(
      [_flutterVersion, _deviceName, _osVersion],
      everyElement(isNotEmpty),
      reason: 'Pass G2_FLUTTER_VERSION, G2_DEVICE, and G2_OS_VERSION defines.',
    );

    await tester.pumpWidget(OmniFolioApp(api: _ProfileApi()));
    await tester.pumpAndSettle();
    await tester.tap(find.text('보유'));
    await tester.pumpAndSettle();

    final list = find.byType(ListView);
    await tester.fling(list, const Offset(0, -1200), 2400);
    await tester.pumpAndSettle();
    await tester.fling(list, const Offset(0, 1200), 2400);
    await tester.pumpAndSettle();
    await Future<void>.delayed(const Duration(seconds: 2));

    final timings = <FrameTiming>[];
    var tableScrollExercised = false;
    final collectTimings = timings.addAll;
    binding.addTimingsCallback(collectTimings);
    try {
      for (var index = 0; index < 4; index++) {
        await tester.fling(list, const Offset(0, -1200), 2400);
        await tester.pumpAndSettle();
        await tester.fling(list, const Offset(0, 1200), 2400);
        await tester.pumpAndSettle();
      }

      await tester.tap(find.text('내역'));
      await tester.pumpAndSettle();
      expect(find.text('거래 내역 가져오기'), findsOneWidget);
      await tester.tap(find.text('보유'));
      await tester.pumpAndSettle();
      await tester.fling(find.byType(ListView), const Offset(0, -1200), 2400);
      await tester.pumpAndSettle();
      await tester.fling(find.byType(ListView), const Offset(0, 1200), 2400);
      await tester.pumpAndSettle();
      await tester.tap(find.text('KRX000'));
      await tester.pumpAndSettle();
      expect(find.text('시세 차트'), findsOneWidget);
      await tester.fling(
        find.byType(ListView).last,
        const Offset(0, -900),
        2000,
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('chart-range-30d')));
      await tester.pumpAndSettle();
      expect(
        find.semantics.byLabel(RegExp(r'KRX000 1d 캔들 차트.*31개 봉')),
        findsOneWidget,
      );
      await tester.fling(
        find.byType(ListView).last,
        const Offset(0, -900),
        2000,
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('정확한 OHLCV 표 보기'));
      await tester.pumpAndSettle();
      expect(find.text('OHLCV 표'), findsOneWidget);
      final tableRows = find.byKey(const Key('ohlcv-table-rows'));
      await tester.ensureVisible(tableRows);
      await tester.pumpAndSettle();
      final tableScrollable = find.descendant(
        of: tableRows,
        matching: find.byType(Scrollable),
      );
      expect(tableScrollable, findsOneWidget);
      final tableState = tester.state<ScrollableState>(tableScrollable);
      final origin = tester.getTopLeft(tableRows);
      final size = tester.getSize(tableRows);
      final viewSize = tester.view.physicalSize / tester.view.devicePixelRatio;
      final start = Offset(
        origin.dx < 0 ? 24 : origin.dx + 24,
        origin.dy + size.height / 2,
      );
      expect(start.dx, inInclusiveRange(0, viewSize.width));
      expect(start.dy, inInclusiveRange(0, viewSize.height));
      final localStart = tester
          .renderObject<RenderBox>(tableRows)
          .globalToLocal(start);
      expect(localStart.dx, inInclusiveRange(0, size.width));
      expect(localStart.dy, inInclusiveRange(0, size.height));
      final before = tableState.position.pixels;
      await tester.flingFrom(start, const Offset(0, -800), 2400);
      await tester.pumpAndSettle();
      final forwardPixels = tableState.position.pixels;
      expect(forwardPixels, greaterThan(before));
      await tester.flingFrom(start, const Offset(0, 800), 2400);
      await tester.pumpAndSettle();
      expect(tableState.position.pixels, lessThan(forwardPixels));
      expect(tableState.position.pixels, greaterThanOrEqualTo(before));
      tableScrollExercised = true;
      await Future<void>.delayed(const Duration(seconds: 2));
    } finally {
      binding.removeTimingsCallback(collectTimings);
    }

    expect(timings, isNotEmpty);
    final p95BuildMs = _p95(timings.map((timing) => timing.buildDuration));
    final p95RasterMs = _p95(timings.map((timing) => timing.rasterDuration));
    final p95TotalMs = _p95(timings.map((timing) => timing.totalSpan));
    final view = tester.view;
    final logicalSize = view.physicalSize / view.devicePixelRatio;
    binding.reportData = {
      'g2_frame_timing': {
        'mode': 'profile',
        'flutter_version': _flutterVersion,
        'platform': kIsWeb ? 'web-js' : defaultTargetPlatform.name,
        'device': _deviceName,
        'os_version': _osVersion,
        'logical_viewport':
            '${logicalSize.width.toStringAsFixed(2)}x'
            '${logicalSize.height.toStringAsFixed(2)}',
        'device_pixel_ratio': view.devicePixelRatio,
        'fixture':
            '120 holdings list scroll plus import screen transition plus 500-bar asset chart, 30-day selection and exact table',
        'network_db_excluded': true,
        'table_scroll_exercised': tableScrollExercised,
        'sample_count': timings.length,
        'p95_build_ms': p95BuildMs,
        'p95_raster_ms': p95RasterMs,
        'p95_total_span_ms': p95TotalMs,
        'budget_ms_per_build_or_raster_phase': _frameBudgetMs,
      },
    };

    expect(timings.length, greaterThanOrEqualTo(50));
    expect(p95BuildMs, lessThanOrEqualTo(_frameBudgetMs));
    expect(p95RasterMs, lessThanOrEqualTo(_frameBudgetMs));
  });
}

double _p95(Iterable<Duration> values) {
  final milliseconds =
      values.map((value) => value.inMicroseconds / 1000).toList()..sort();
  return milliseconds[(milliseconds.length * 0.95).ceil() - 1];
}

class _ProfileApi implements OmniApi {
  final _status = ServiceStatus(
    liveEnabled: false,
    mode: 'local_import_only',
    trustState: 'verified',
    ledgerRevision: 'rev_profile',
    lastVerifiedAt: DateTime.utc(2026, 8, 24),
    issues: const [],
  );
  final _snapshot = PortfolioSnapshot(
    ledgerRevision: 'rev_profile',
    costBasisPolicy: 'fifo_exact_else_half_even_residual_8_v1',
    recordedAt: DateTime.utc(2026, 8, 24),
    cash: const [Money('KRW', '1000000')],
    holdings: List.generate(
      120,
      (index) => Holding(
        symbol: 'KRX${index.toString().padLeft(3, '0')}',
        quantity: '${index + 1}',
        costBasis: '${100000 + index}',
        currency: 'KRW',
      ),
      growable: false,
    ),
    realizedPnl: const [Money('KRW', '12500')],
  );
  final _candles = MarketCandles.fromJson({
    'symbol': 'KRX000',
    'venue': 'KRX',
    'timezone': 'Asia/Seoul',
    'interval': '1d',
    'price_adjustment': 'unspecified',
    'source': 'local_fixture',
    'sample': true,
    'state': 'stale',
    'source_as_of': '2025-05-14T00:00:00.000Z',
    'fetched_at': '2026-08-24T03:00:00Z',
    'issues': const [
      {
        'code': 'sample_data',
        'message': 'market data is a local sample and not live',
      },
    ],
    'bars': List.generate(500, (index) {
      final at = DateTime.utc(
        2024,
        1,
        1,
      ).add(Duration(days: index)).toIso8601String();
      final open = 100000 + index;
      return {
        'at': at,
        'open': '$open',
        'high': '${open + 500}',
        'low': '${open - 500}',
        'close': '${open + 100}',
        'volume': '${1000 + index}',
      };
    }, growable: false),
  });
  @override
  Future<ApplyReceipt> apply(String previewId, String idempotencyKey) =>
      throw UnsupportedError('Not part of the G2 frame fixture.');

  @override
  Future<ImportPreview> preview(String csv) =>
      throw UnsupportedError('Not part of the G2 frame fixture.');

  @override
  Future<MarketCandles> candles(String symbol) async => _candles;

  @override
  Future<BrokerReconciliation?> latestBrokerReconciliation() async => null;

  @override
  Future<LocalOrderLog> localOrders() async => const LocalOrderLog(orders: []);

  @override
  Future<LedgerActivityPage> ledgerActivities() async =>
      const LedgerActivityPage(
        source: 'local_ledger',
        brokerFreshness: 'unverified',
        ledgerRevision: 'rev_0000000000',
        recordedAt: '1970-01-01T00:00:00Z',
        events: [],
        nextCursor: null,
      );

  @override
  Future<PortfolioSnapshot> snapshot() async => _snapshot;

  @override
  Future<HoldingValuation> holdingValuation() =>
      throw UnsupportedError('Not part of the G2 frame fixture.');

  @override
  Future<ServiceStatus> status() async => _status;
}

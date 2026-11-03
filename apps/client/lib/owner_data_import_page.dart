import 'package:flutter/material.dart';

import 'owner_data_import.dart';

class OwnerDataImportPage extends StatelessWidget {
  const OwnerDataImportPage({super.key, required this.controller});
  final OwnerDataImportController controller;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('가격·환율·지수 파일')),
    body: SafeArea(
      child: AnimatedBuilder(
        animation: controller,
        builder: (context, _) {
          final review = controller.pending ?? controller.review;
          final summary = review?.summary;
          final locked = controller.pending != null;
          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              const Text(
                '파일 선택 → 입력 검토 → 직접 저장',
                style: TextStyle(fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 8),
              const Text(
                'UTF-8 JSON · 최대 1MiB. 종목 가격, 환율, KRW·USD 지수를 지원합니다. 입력 검토는 저장 가능 여부나 최신 데이터 여부를 보증하지 않습니다.',
              ),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: locked || controller.selecting || controller.applying
                    ? null
                    : controller.selectFile,
                icon: const Icon(Icons.file_open_outlined),
                label: Text(controller.selecting ? '파일 읽는 중…' : 'JSON 파일 선택'),
              ),
              if (controller.input != null) ...[
                const SizedBox(height: 8),
                const Text('파일을 읽었습니다. 내용을 서버에서 검토한 뒤 저장하세요.'),
                OutlinedButton(
                  onPressed:
                      locked ||
                          controller.selecting ||
                          controller.reviewing ||
                          controller.applying
                      ? null
                      : controller.preview,
                  child: Text(controller.reviewing ? '입력 검토 중…' : '입력 형식 검토'),
                ),
              ],
              if (review != null && summary != null) ...[
                const Divider(height: 32),
                Text(switch (review.kind) {
                  'security_price' =>
                    '종목 가격 · ${summary['symbol']} / ${summary['currency']}',
                  'fx' =>
                    '환율 · ${summary['base_currency']} → ${summary['quote_currency']}',
                  _ => '비교 지수 · ${summary['label']} / ${summary['currency']}',
                }, style: Theme.of(context).textTheme.titleMedium),
                Text('버전: ${summary['source_version']}'),
                Text(
                  '${summary['point_count']}개 관측 · 소유자 선언 · 샘플: ${summary['sample'] == true ? '예' : '아니요'}',
                ),
                if (review.kind == 'security_price')
                  Text('거래소: ${summary['venue']} · 무조정, 원장 1주당 가격'),
                if (review.kind == 'benchmark')
                  Text(
                    '수익 기준: ${switch (summary['return_basis']) {
                      'price_index' => '가격 지수',
                      'gross_total_return' => '세전 총수익 지수',
                      _ => '세후 총수익 지수',
                    }}',
                  ),
                Text(
                  '관측: ${summary['first_observed_at'] ?? '없음'} ~ ${summary['last_observed_at'] ?? '없음'}',
                ),
                Text('수집: ${summary['fetched_at']}'),
                if (summary['source_reference'] != null)
                  Text('출처: ${summary['source_reference']}'),
                const SizedBox(height: 8),
                const Text(
                  '새 버전은 해당 시계열 전체를 대체합니다. 빈 가격·환율 시계열은 활성 관측을 철회합니다. 지수는 비울 수 없습니다. 출처의 사용·영구 보관 권한을 확인하세요. 기존 기록과 백업에는 과거 버전이 남습니다.',
                ),
                const SizedBox(height: 12),
                FilledButton(
                  onPressed: controller.applying ? null : controller.apply,
                  child: Text(
                    controller.applying
                        ? '저장 확인 중…'
                        : locked
                        ? '같은 원본으로 저장 재시도'
                        : '검토한 파일 저장',
                  ),
                ),
              ],
              if (controller.selecting ||
                  controller.reviewing ||
                  controller.applying)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 12),
                  child: LinearProgressIndicator(),
                ),
              if (controller.message != null)
                Semantics(
                  liveRegion: true,
                  child: Padding(
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    child: Text(controller.message!),
                  ),
                ),
              if (controller.receipt != null) ...[
                const Divider(),
                Text(
                  '마지막 저장 확인: ${controller.receipt!.review.summary['source_version']} · ${controller.receipt!.recordedAt}',
                ),
                const Text(
                  '저장 무결성 확인이며 제공사 인증이 아닙니다. 저장 후 읽기 갱신이 실패하면 기존 값과 오류 상태를 확인하세요.',
                ),
              ],
              const SizedBox(height: 12),
              const Text('이 화면을 나가도 대기 요청은 앱 실행 중 유지됩니다. 앱을 종료하면 유지되지 않습니다.'),
            ],
          );
        },
      ),
    ),
  );
}

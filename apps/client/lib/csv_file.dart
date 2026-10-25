import 'dart:convert';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';

const maxCsvFileBytes = 1 << 20;

class CsvFileException implements Exception {
  const CsvFileException(this.message);
  final String message;
}

Future<String?> selectCsvFile() async {
  final file = await openFile(
    acceptedTypeGroups: const [
      XTypeGroup(
        label: 'CSV',
        extensions: ['csv'],
        mimeTypes: ['text/csv'],
        uniformTypeIdentifiers: ['public.comma-separated-values-text'],
      ),
    ],
  );
  return file == null ? null : readCsvFile(file);
}

Future<String> readCsvFile(XFile file) async {
  const tooLarge = CsvFileException(
    'CSV 파일은 1MiB 이하로 선택하세요. 기존 입력과 결과는 유지됩니다.',
  );
  if (await file.length() > maxCsvFileBytes) throw tooLarge;
  final bytes = BytesBuilder(copy: false);
  // Do not use metadata as the endpoint: a file may grow after length().
  await for (final chunk in file.openRead()) {
    if (bytes.length + chunk.length > maxCsvFileBytes) throw tooLarge;
    bytes.add(chunk);
  }
  try {
    return utf8.decode(bytes.takeBytes());
  } on FormatException {
    throw const CsvFileException('UTF-8로 저장한 CSV 파일을 선택하세요. 기존 입력과 결과는 유지됩니다.');
  }
}

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
  try {
    return await readUtf8File(file);
  } on RangeError {
    throw tooLarge;
  } on FormatException {
    throw const CsvFileException('UTF-8로 저장한 CSV 파일을 선택하세요. 기존 입력과 결과는 유지됩니다.');
  }
}

Future<String> readUtf8File(XFile file) async {
  if (await file.length() > maxCsvFileBytes)
    throw RangeError('File exceeds 1MiB');
  final bytes = BytesBuilder(copy: false);
  // Do not use metadata as the endpoint: a file may grow after length().
  await for (final chunk in file.openRead()) {
    if (bytes.length + chunk.length > maxCsvFileBytes)
      throw RangeError('File exceeds 1MiB');
    bytes.add(chunk);
  }
  return utf8.decode(bytes.takeBytes());
}

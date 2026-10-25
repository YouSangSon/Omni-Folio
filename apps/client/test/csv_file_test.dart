import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:omni_folio_client/csv_file.dart';

void main() {
  late Directory root;
  setUp(() => root = Directory.systemTemp.createTempSync('omni-csv-reader-'));
  tearDown(() => root.deleteSync(recursive: true));

  XFile file(List<int> bytes) {
    final path = File('${root.path}/input.csv')..writeAsBytesSync(bytes);
    return XFile(path.path);
  }

  test(
    'reads UTF-8 and strips only the initial BOM without deleting the file',
    () async {
      final input = file([
        0xef,
        0xbb,
        0xbf,
        ...utf8.encode('header\n한국어\ufeff'),
      ]);
      expect(await readCsvFile(input), 'header\n한국어\ufeff');
      expect(File(input.path).existsSync(), isTrue);
    },
  );

  test(
    'accepts exactly 1 MiB and rejects one additional encoded byte',
    () async {
      final exact = Uint8List(maxCsvFileBytes)
        ..fillRange(0, maxCsvFileBytes, 0x61);
      expect((await readCsvFile(file(exact))).length, maxCsvFileBytes);
      await expectLater(
        readCsvFile(file([...exact, 0x61])),
        throwsA(isA<CsvFileException>()),
      );
      await expectLater(
        readCsvFile(file(utf8.encode('가' * (maxCsvFileBytes ~/ 3 + 1)))),
        throwsA(isA<CsvFileException>()),
      );
    },
  );

  test(
    'checks actual bytes after length and cancels an oversized stream',
    () async {
      final input = _GrowingFile();
      await expectLater(readCsvFile(input), throwsA(isA<CsvFileException>()));
      expect(input.range, (null, null));
      expect(input.cancelled, isTrue);
    },
  );

  test('reads the in-memory XFile returned by the Android selector', () async {
    final bytes = Uint8List.fromList(utf8.encode('header\n한국어'));
    final input = XFile.fromData(
      bytes,
      length: bytes.length,
      path: 'unused.csv',
    );
    expect(await readCsvFile(input), 'header\n한국어');
  });

  test('rejects malformed UTF-8 instead of replacing invalid bytes', () async {
    await expectLater(
      readCsvFile(file([0x61, 0xc3, 0x28])),
      throwsA(
        isA<CsvFileException>().having(
          (error) => error.message,
          'message',
          contains('UTF-8'),
        ),
      ),
    );
  });

  test('propagates read failures for the UI to redact', () async {
    await expectLater(
      readCsvFile(XFile('${root.path}/missing.csv')),
      throwsA(isA<FileSystemException>()),
    );
  });
}

class _GrowingFile extends XFile {
  _GrowingFile() : super('unused');
  (int?, int?)? range;
  bool cancelled = false;
  @override
  Future<int> length() async => 1;
  @override
  Stream<Uint8List> openRead([int? start, int? end]) async* {
    range = (start, end);
    try {
      yield Uint8List(maxCsvFileBytes);
      yield Uint8List(1);
      fail('the reader must cancel before requesting another chunk');
    } finally {
      cancelled = true;
    }
  }
}

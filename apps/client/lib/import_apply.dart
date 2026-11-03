import 'package:flutter/foundation.dart';

import 'api.dart';
import 'models.dart';

typedef ImportApplyRequest = ({String previewId, String csv, String key});
typedef ImportApplyCompletion = ({
  ApplyReceipt receipt,
  String csv,
  String previewId,
});

enum ImportApplyFailure { uncertain, rejected }

class ImportApplyController extends ChangeNotifier {
  ImportApplyController(this.api);

  final OmniApi api;
  ImportApplyRequest? _pending;
  ImportApplyCompletion? _completion;
  ImportApplyFailure? _failure;
  var _completionSerial = 0;
  var _rejectionSerial = 0;
  var _busy = false;
  var _disposed = false;

  ImportApplyRequest? get pending => _pending;
  ImportApplyCompletion? get completion => _completion;
  ImportApplyFailure? get failure => _failure;
  int get completionSerial => _completionSerial;
  int get rejectionSerial => _rejectionSerial;
  bool get busy => _busy;

  Future<void> apply(ImportPreview preview, String csv) async {
    if (_disposed || _busy || !preview.canApply) return;
    if (_pending != null && _pending!.csv != csv) return;
    // A lost response may already be committed; retries retain the original key.
    _pending ??= (
      previewId: preview.previewId,
      csv: csv,
      key: 'import-${preview.previewId}',
    );
    await retry();
  }

  Future<void> retry() async {
    final request = _pending;
    if (_disposed || _busy || request == null) return;
    _busy = true;
    _failure = null;
    notifyListeners();
    try {
      final receipt = await api.apply(request.previewId, request.key);
      if (_disposed) return;
      _completion = (
        receipt: receipt,
        csv: request.csv,
        previewId: request.previewId,
      );
      _completionSerial += 1;
      _pending = null;
    } catch (error) {
      if (_disposed) return;
      if (error is ApiException && error.isConfirmedImportRejection) {
        _pending = null;
        _failure = ImportApplyFailure.rejected;
        _rejectionSerial += 1;
      } else {
        _failure = ImportApplyFailure.uncertain;
      }
    } finally {
      if (!_disposed) {
        _busy = false;
        notifyListeners();
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}

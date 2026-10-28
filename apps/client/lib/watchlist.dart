import 'package:flutter/foundation.dart';

import 'api.dart';
import 'models.dart';

class WatchlistController extends ChangeNotifier {
  WatchlistController(this.api);

  final OmniApi api;
  WatchlistState? state;
  WatchlistReport? _observation;
  WatchlistChange? pending;
  String? message;
  String draftVenue = 'XKRX', draftSymbol = '';
  bool busy = false;
  bool saving = false;
  bool _attempted = false, _everUncertain = false, _disposed = false;
  bool _needsReload = false;

  WatchlistReport? get report {
    final current = state, observation = _observation;
    if (current == null ||
        observation == null ||
        current.revision != observation.watchlist.revision ||
        !current.sameEntries(observation.watchlist.entries)) {
      return null;
    }
    return observation;
  }

  bool get canEdit =>
      !_disposed && !busy && !_needsReload && pending == null && state != null;

  void ensureLoaded() {
    if (!_attempted && !busy && !_disposed) refresh();
  }

  Future<void> refresh() async {
    if (_disposed || busy) return;
    _attempted = true;
    busy = true;
    message = null;
    _notify();
    try {
      await _read();
    } finally {
      if (!_disposed) {
        busy = false;
        _notify();
      }
    }
  }

  Future<bool> add(WatchlistEntry entry) async {
    final current = state;
    if (!canEdit || current == null) return false;
    if (current.entries.length >= 50) {
      message = '관심종목은 최대 50개까지 저장할 수 있습니다.';
      _notify();
      return false;
    }
    if (current.entries.any((item) => item.key == entry.key)) {
      message = '이미 관심종목에 저장되어 있습니다.';
      _notify();
      return false;
    }
    return _startChange([...current.entries, entry]);
  }

  Future<bool> remove(WatchlistEntry entry) async {
    final current = state;
    if (!canEdit || current == null) return false;
    if (!current.entries.any((item) => item.key == entry.key)) return false;
    return _startChange(
      current.entries.where((item) => item.key != entry.key).toList(),
    );
  }

  Future<bool> _startChange(List<WatchlistEntry> entries) async {
    final current = state;
    if (!canEdit || current == null) return false;
    pending = WatchlistChange(
      expectedRevision: current.revision,
      entries: entries,
    );
    _everUncertain = false;
    return _commit();
  }

  Future<bool> retry() async {
    if (_disposed || busy || pending == null) return false;
    return _commit();
  }

  Future<bool> _commit() async {
    final request = pending;
    if (_disposed || busy || request == null) return false;
    busy = true;
    saving = true;
    message = null;
    _notify();
    try {
      final acknowledgement = await api.updateWatchlist(request);
      if (_disposed) return false;
      // add/remove always changes the captured list; a no-op ACK cannot confirm it.
      if (acknowledgement.revision != request.expectedRevision + 1 ||
          !request.matchesAck(acknowledgement)) {
        throw const FormatException('Watchlist acknowledgement mismatch');
      }
      state = acknowledgement;
      pending = null;
      _everUncertain = false;
      saving = false;
      _notify();
      await _read(successMessage: '관심종목 저장과 가격 확인을 마쳤습니다.');
      return true;
    } catch (error) {
      if (_disposed) return false;
      final conflict =
          error is ApiException &&
          error.statusCode == 409 &&
          error.code == 'watchlist_changed';
      final rejected =
          error is ApiException &&
          ((error.statusCode == 400 && error.code == 'watchlist_invalid') ||
              (error.statusCode == 403 &&
                  error.code == 'watchlist_origin_rejected'));
      if (conflict) {
        if (!_everUncertain) pending = null;
        _needsReload = true;
        saving = false;
        _notify();
        await _read(
          successMessage: _everUncertain
              ? '현재 목록을 불러왔습니다. 앞선 요청의 적용 여부는 확인할 수 없습니다.'
              : '목록이 다른 곳에서 변경되어 현재 저장 목록을 불러왔습니다.',
        );
      } else if (rejected && !_everUncertain) {
        pending = null;
        message = '저장이 거절되었습니다. 입력과 서버 허용 주소를 확인하세요.';
      } else {
        _everUncertain = true;
        message = '저장 여부를 확인하지 못했습니다. 같은 변경을 재시도하거나 저장 목록을 다시 불러오세요.';
      }
      return false;
    } finally {
      if (!_disposed) {
        saving = false;
        busy = false;
        _notify();
      }
    }
  }

  Future<void> _read({String? successMessage}) async {
    try {
      final observation = await api.watchlist();
      if (_disposed) return;
      _needsReload = false;
      state = observation.watchlist;
      _observation = observation;
      final request = pending;
      if (request != null) {
        final current = observation.watchlist;
        if (current.revision > request.expectedRevision ||
            (current.revision == request.expectedRevision &&
                current.sameEntries(request.entries))) {
          pending = null;
          _everUncertain = false;
          message = '현재 저장 목록을 불러왔습니다. 앞선 요청의 적용 여부는 확인할 수 없습니다.';
        } else if (current.revision < request.expectedRevision) {
          message =
              '서버의 저장 기록이 이전 버전으로 돌아가 앞선 저장 여부를 확인할 수 없습니다. 대기 중인 변경은 유지됩니다.';
        } else {
          message = '현재 목록만으로 앞선 저장 여부를 확인할 수 없습니다. 같은 변경을 재시도하거나 다시 불러오세요.';
        }
      } else {
        message = successMessage;
      }
    } catch (_) {
      if (_disposed) return;
      message = pending != null
          ? '현재 목록을 불러오지 못했습니다. 저장 여부를 확인할 때까지 대기 중인 변경은 유지됩니다.'
          : _needsReload
          ? '목록 충돌 후 현재 목록을 불러오지 못했습니다. 다시 불러온 뒤 변경하세요.'
          : _observation == null
          ? '관심종목을 불러오지 못했습니다. 다시 시도하세요.'
          : report == null
          ? '저장한 목록은 유지됩니다. 가격을 확인하지 못했습니다. 새로고침하세요.'
          : '새로고침하지 못했습니다. 이전에 확인한 목록과 시각을 유지합니다.';
    }
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}

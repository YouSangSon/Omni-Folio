# Omni Folio Client

Flutter client for iOS, Android, and app-centric web. The current slice shows ledger trust state, holdings, CSV preview, and idempotent apply receipts; it never submits orders.

Use the repository root commands:

```sh
make run-core
make run-client
make check
```

The API defaults to `http://127.0.0.1:8080`. Override it with `API_URL=... make run-client`; never put broker credentials in Dart defines or the client bundle.

Profile the representative G2 list/import surface on a connected device:

```sh
FLUTTER_TEST_OUTPUTS_DIR="$PWD/../../output/g2-profile" \
  flutter drive --profile -d <device-id> \
  --dart-define=G2_FLUTTER_VERSION=<flutter-version> \
  --dart-define=G2_DEVICE=<device-model> \
  --dart-define=G2_OS_VERSION=<os-version> \
  --driver=test_driver/g2_profile_frame_test.dart \
  --target=integration_test/g2_profile_frame_test.dart
```

The harness records the supplied environment metadata, measured viewport, and build/raster/total-span p95 separately. It fails above the 16.67 ms build or raster budget. Emulator and web results are local baselines, not substitutes for physical iOS/Android release evidence.

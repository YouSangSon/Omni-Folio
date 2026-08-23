# Omni Folio Client

Flutter client for iOS, Android, and app-centric web. The current slice shows ledger trust state, holdings, CSV preview, and idempotent apply receipts; it never submits orders.

Use the repository root commands:

```sh
make run-core
make run-client
make check
```

The API defaults to `http://127.0.0.1:8080`. Override it with `API_URL=... make run-client`; never put broker credentials in Dart defines or the client bundle.

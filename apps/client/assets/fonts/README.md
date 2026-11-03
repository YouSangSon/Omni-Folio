# Bundled Korean typography

`NotoSansKR.ttf` is the **unmodified** variable TrueType file `NotoSansKR[wght].ttf` from [Google Fonts at b38c5c93af322c45f633e17ac440ec1e6c94d489](https://github.com/google/fonts/tree/b38c5c93af322c45f633e17ac440ec1e6c94d489/ofl/notosanskr). Only the local filename differs. Its weight axis spans 100–900; one asset supplies normal and emphasized text rather than simulated bold or multiple static font downloads.

- Size: 10,414,588 bytes (uncompressed).
- SHA-256: `194018e6b2b293a7964f037b25c0249ce1418bc9ab3c971060a03aa57861e252`.
- Upstream Git blob: `b386890ba945e1f39448a6b59f20c5d194f58808`.
- License: [OFL.txt](OFL.txt), preserved verbatim and included as an app asset. Reserved Font Name: `Source`. The font license is separate from the application license.

The full upstream glyph set is intentional: subsetting only today's UI text would break future Korean symbols, names or error messages. The size is a known startup/transfer cost, not a measured performance pass. Consider reproducible subsetting only with a declared language coverage contract and rendered-glyph regression evidence. Do not add runtime `google_fonts` downloads for this bundled family.

Flutter's [custom-font guide](https://docs.flutter.dev/cookbook/design/fonts) supports TTF/OTF/TTC across platforms; WOFF/WOFF2 is not supported on desktop. Theme defaults and explicit app-bar/navigation/button styles use the same family. Missing glyphs outside this font's coverage can still invoke engine fallback: bundling this file does not make all Unicode, the API, or the web shell offline-capable.

For updates, inspect the upstream metadata/license and compare hashes before replacing the asset; rerun the typography test and a fresh-browser build with all non-loopback requests blocked. Use `--no-web-resources-cdn` for local CanvasKit as well. This verifies absence of an external dependency for exercised screens, not installation/cache support for an unavailable web origin.

Cupertino icon assets are supplied separately by exact-pinned `cupertino_icons: 1.0.9` from the verified `flutter.dev` publisher; its archive SHA-256 is fixed in `pubspec.lock`. It has no runtime Dart dependencies. Its MIT license is included in Flutter's generated notices. It provides platform glyphs referenced by the Flutter SDK; actual iOS rendering remains a separate gate.

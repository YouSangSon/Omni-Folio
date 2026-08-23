# Local Project Reuse Audit

Date: 2026-08-23

The following user-owned repositories were inspected read-only before importing anything. `Mimir` and `akasha` had unrelated dirty paths, so their files were treated as evidence only and were not modified. All three repositories carry the MIT license.

| Repository | Audited revision | Useful now | Deferred | Rejected |
|---|---|---|---|---|
| Parallax | `b804d722dfe41fc367c2250c2aa3c98e56e77577` | Input-size limits, source hashing/provenance, path containment and secret-redaction test ideas | OpenAPI compatibility checks when a broker contract is recorded | Graph index, vector/ML stack, impact-map UI |
| Mimir | `4fc4e0b57ab8c287e0955a7284f8a171ccc5c518` | First-bar-strictly-after-signal evaluation and insufficient-sample gating for research | Official-source metadata, per-source failure isolation, SEC/DART/FRED/ECOS adapters when market-data ingestion starts | JSONL as the operational ledger, static dashboard, GitHub Actions as a trading runner |
| akasha | `1bd4234aac7e37b4d1cea7b23a5dd4393939cd70` | Loopback-only local publication, separate liveness/readiness, deterministic injected-clock tests | Timing-safe owner authentication and account isolation before any remote/cloud profile | Qdrant/RAG, MCP tool surface, document-ingestion domain |

## Transfer decision

No source file was copied wholesale and no dependency was added. The current slice adapts only patterns that close an existing requirement:

- Compose publishes the unauthenticated local API on loopback only.
- Go exposes process liveness separately from database/schema readiness and does not return internal errors to clients.
- The automatic research loop evaluates a signal no earlier than the next bar, rejects insufficient samples, and emits only a paper candidate.

Parallax's containment/redaction helpers are not ported yet because the current CSV arrives as a bounded HTTP body and the product has no broker secret or arbitrary import path. Mimir's source framework and finance adapters wait for the first read-only market-data gate. Akasha's authentication stack waits until a non-loopback profile is designed with TLS and owner authentication. This keeps the useful provenance without importing three unrelated frameworks.

## Provenance rule

If future work copies a substantial implementation rather than adapting a concept, preserve the originating MIT copyright notice in the copied file or an adjacent `NOTICE`, pin the source revision, and add a contract test in Omni Folio before using it.

# Mosaic Manifest — Blockchain (v2, Removed)

> **This design is no longer used.** The v2 personal hash chain (blockchain) was replaced by a signed LWW-Set CRDT in v3. See [manifest-crdt.md](manifest-crdt.md) for the current design.

## Why It Was Replaced

The blockchain grew with every file operation (one block per add/remove/rename), resulting in a manifest that ballooned without bound. 1000 add/remove cycles for the same file = 2000 blocks, all transmitted on every peer connection. For a distributed filesystem where files change frequently, this was unsustainable.

The replacement LWW-Set CRDT stores one record per file (not per operation), provides the same tamper resistance via ECDSA signatures and monotonic sequence numbers, and stays O(N files) regardless of how many operations have been performed.

## What the Blockchain Provided (and What Replaced It)

| Property | v2 (blockchain) | v3 (LWW-Set CRDT) |
|---|---|---|
| Tamper detection | Hash chain — any modification invalidates subsequent PrevHash links | ECDSA signature over each record — tampered fields fail verification |
| Replay prevention | N/A (longer chain wins, older operations can't win) | Monotonic `Seq` — lower-or-equal `Seq` is rejected on merge |
| Conflict resolution | Deterministic: longer chain wins; equal-length by first-differing block hash | Deterministic: higher `Seq` wins; equal `Seq` is idempotent |
| Size | O(N operations) — grows forever | O(N active files) — constant for steady-state usage |
| History | Full operation history preserved | Current state only; no operation log |
| Fork handling | Lossy — one fork's operations are dropped | Not applicable — one actor per user, no forks |

## v2 Code

All v2 code (`ChainBlock`, `UserChain`, `AppendBlock`, `ChainToFiles`, `ValidateChain`, `pickBetterChain`) has been removed. v2 manifest files on disk are treated as empty — no migration was performed since Mosaic was not yet in production.

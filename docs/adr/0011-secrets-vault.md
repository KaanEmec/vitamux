# ADR-0011 Secrets vault: master key file, HKDF purposes, AES-256-GCM

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
Provider tokens and lab PDFs must not be readable from a database dump or backup, and a rotation must be possible without downtime. One owner, no KMS. See [security.md › Keys and secrets](../architecture/security.md#keys-and-secrets).

## Decision
- **Master key file**: `VITAMUX_MASTER_KEY_FILE` holds 32 random bytes as 64 hex characters (trailing whitespace ignored). Hex text survives copy-paste, password managers, and backups. The loader opens the file once, refuses non-regular files and any mode with group or other bits (`mode & 0o077`), and requires exactly 64 hex characters. Errors never contain key material. `vitamux admin init-secrets [--out PATH]` creates it with `O_EXCL` and mode 0600, never overwrites, and prints only the path and key id.
- **Key id**: first 8 bytes of SHA-256(`vitamux/key-id/v1\x00` + key), as 16 hex characters. Non-secret and deterministic.
- **Purposes**: HKDF-SHA256 (stdlib `crypto/hkdf`, no salt, info `vitamux/v1/<purpose>`) derives independent keys for `credentials`, `documents`, `blob-names`, `session-signing`, once at load. `Keyring.PurposeKey` exposes the current key for HMAC use.
- **Sealed format**: one `bytea`, stored next to a `key_id text` column: `version(1) | key id(8) | nonce(12) | ciphertext+tag`, AES-256-GCM with a random nonce. The GCM AAD is `version | key id | purpose | 0x00 | caller AAD`, so header, purpose, and the row binding (e.g. `credentials:<connection id>`) are all authenticated. Any change fails with `ErrOpen`; an unknown key id fails with `ErrUnknownKey`.
- **Keyring**: `Load(current, previous...)`. Seal always uses the current key; Open picks the key by the id inside the value. Previous keys come from `VITAMUX_PREVIOUS_MASTER_KEY_FILES` (comma-separated paths, same file rules).
- **Rotation** (`vitamux keys rotate`, J03.1 T03.1.5): (1) `init-secrets --out new.key`; (2) set `VITAMUX_MASTER_KEY_FILE=new.key` and `VITAMUX_PREVIOUS_MASTER_KEY_FILES=old.key`, restart; the app now seals with the new key and still opens old values; (3) `keys rotate` walks each table with a sealed column in batches `WHERE key_id <> current`, opens with the old key, reseals with the new one, and updates value and `key_id` in one statement, so it is idempotent and resumable; (4) when no row has an old `key_id`, drop the previous setting and archive the old key as long as backups may need it. Document encryption keys, if wrapped per document, are rewrapped the same way.

## Alternatives considered
- **Raw 32-byte file**: smaller, but awkward to back up and easy to corrupt through text tools.
- **Key from an env var**: leaks via process listings and crash dumps; the file route matches the `*_FILE` rule for every other secret.
- **External KMS or age/sops**: more moving parts than a single-owner self-hosted tool needs; the format leaves room to add a wrapping layer later.
- **Separate key id and nonce columns**: more columns to keep consistent; a self-describing blob cannot be half-updated.
- **XChaCha20-Poly1305**: needs `x/crypto`; random 96-bit GCM nonces are safe well below 2^32 values per key, far above our volume.

## Consequences
- Losing the key loses provider tokens (reauthorize) and encrypted documents; backups must store the key separately from the database.
- Callers must pass the same AAD on open as on seal, so the row identity is part of the contract.
- The key is read at startup; changing it needs a restart. `/readyz` must report not ready without a loadable key (J03.1 done-when, not yet wired).

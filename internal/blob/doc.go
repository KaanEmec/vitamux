// Package blob implements the content-addressed, zstd-compressed blob store for raw payloads
// and files (docs/adr/0004-blob-store.md).
//
// A blob is keyed by the SHA-256 of its uncompressed content. Files live under the blob
// directory with names derived by HMAC from that hash, so a directory listing reveals no
// content hashes. Metadata and reference counts live in the blobs table.
//
// Writers call Put and then Retain in the transaction that inserts the referencing row, and
// Release in the transaction that deletes it. Every table that references blobs must keep
// refcount in step, or Sweep will fail on the foreign key (never delete a referenced blob).
package blob

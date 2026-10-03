// Package lifecycle implements owner control over deletion and retention (J13.4;
// docs/architecture/security.md#export-and-deletion, data-model.md#retention):
//
//   - DeleteConnection removes a connection with everything it brought in;
//   - Purge removes everything of one owner (`vitamux admin purge-user`);
//   - the daily prune_raw, prune_superseded and prune_idempotency_keys jobs apply the owner's
//     Retention settings.
//
// Blob references are released in the deleting transaction; the blob sweep removes the files.
// Audit events and logs carry counts only, never health values.
package lifecycle

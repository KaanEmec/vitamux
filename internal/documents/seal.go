package documents

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Each document has its own random AES-256-GCM data key, stored in document_keys sealed
// with the master key (purpose documents, AAD document_keys:<id>). Everything derived from
// the document that must disappear with it (the PDF, its filename, raw extractor responses)
// is sealed with the data key: version(1) | nonce(12) | ciphertext+tag, with an AAD naming
// the document and the field. Deleting the key row makes all of it unreadable, even with
// the master key.

const (
	dataKeySize          = 32
	dataSealVersion byte = 1
)

var (
	// ErrShredded means the document key is gone: the document was deleted.
	ErrShredded = errors.New("documents: document key destroyed")
	errSealed   = errors.New("documents: cannot open sealed value")
)

func keyAAD(id uuid.UUID) []byte { return []byte("document_keys:" + id.String()) }

// fieldAAD binds a value sealed with a data key to its document and purpose.
func fieldAAD(id uuid.UUID, field string) []byte {
	return []byte("document:" + id.String() + ":" + field)
}

// newDataKey returns a random data key and its master-sealed form for document id.
func newDataKey(kr *crypto.Keyring, id uuid.UUID) (key, sealed []byte, err error) {
	key = make([]byte, dataKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, nil, err
	}
	sealed, err = kr.Seal(crypto.Documents, key, keyAAD(id))
	return key, sealed, err
}

// dataKey unseals the data key of document id. It returns ErrShredded once the key is deleted.
func dataKey(ctx context.Context, q *dbq.Queries, kr *crypto.Keyring, id uuid.UUID) ([]byte, error) {
	sealed, err := q.GetDocumentKey(ctx, id)
	if errors.Is(db.MapErr(err), db.ErrNotFound) {
		return nil, ErrShredded
	}
	if err != nil {
		return nil, err
	}
	return kr.Open(crypto.Documents, sealed, keyAAD(id))
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealWith(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 1+aead.NonceSize(), 1+aead.NonceSize()+len(plaintext)+aead.Overhead())
	out[0] = dataSealVersion
	nonce := out[1:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(out, nonce, plaintext, aad), nil //nolint:gosec // nonce is random (rand.Read above)
}

func openWith(key, sealed, aad []byte) ([]byte, error) {
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	n := 1 + aead.NonceSize()
	if len(sealed) < n+aead.Overhead() || sealed[0] != dataSealVersion {
		return nil, errSealed
	}
	pt, err := aead.Open(nil, sealed[1:n], sealed[n:], aad)
	if err != nil {
		return nil, errSealed
	}
	return pt, nil
}

// Seal encrypts plaintext with the data key of document id, so it is shredded with the
// document. field names what is sealed (e.g. "extraction_response:<run id>") and must be
// passed again to Open. Use it for anything holding document content, such as raw
// extractor responses, before storing it.
func (s *Store) Seal(ctx context.Context, q *dbq.Queries, id uuid.UUID, field string, plaintext []byte) ([]byte, error) {
	key, err := dataKey(ctx, q, s.keys, id)
	if err != nil {
		return nil, err
	}
	return sealWith(key, plaintext, fieldAAD(id, field))
}

// Open decrypts a value from Seal. It returns ErrShredded after the document was deleted.
func (s *Store) Open(ctx context.Context, q *dbq.Queries, id uuid.UUID, field string, sealed []byte) ([]byte, error) {
	key, err := dataKey(ctx, q, s.keys, id)
	if err != nil {
		return nil, err
	}
	return openWith(key, sealed, fieldAAD(id, field))
}

// RotateKeyBatch re-seals up to batch document keys whose master key id differs from the
// current one and returns how many it re-sealed; `vitamux keys rotate` calls it in a
// transaction until it returns 0 (ADR-0011). Locked rows are skipped for a later run.
func RotateKeyBatch(ctx context.Context, q *dbq.Queries, kr *crypto.Keyring, batch int32) (int, error) {
	rows, err := q.LockDocumentKeysToRotate(ctx, dbq.LockDocumentKeysToRotateParams{KeyID: kr.KeyID(), Batch: batch})
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		key, err := kr.Open(crypto.Documents, r.Ciphertext, keyAAD(r.DocumentID))
		if err != nil {
			return 0, fmt.Errorf("document_keys %s: %w", r.DocumentID, err)
		}
		sealed, err := kr.Seal(crypto.Documents, key, keyAAD(r.DocumentID))
		clear(key)
		if err != nil {
			return 0, err
		}
		if err := q.ResealDocumentKey(ctx, dbq.ResealDocumentKeyParams{Ciphertext: sealed, KeyID: kr.KeyID(), DocumentID: r.DocumentID}); err != nil {
			return 0, err
		}
	}
	return len(rows), nil
}

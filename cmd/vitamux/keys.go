package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/documents"
)

const keysUsage = `usage: vitamux keys <command>

Commands:
  rotate   re-seal every sealed value under the current master key (resumable)
`

// rotateBatchSize is the number of rows re-sealed per transaction.
const rotateBatchSize = 100

func keys(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "rotate" {
		fmt.Fprint(stderr, keysUsage)
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "configuration error:\n%v\n", err)
		return 1
	}
	if cfg.MasterKeyFile == "" {
		fmt.Fprintln(stderr, "keys rotate: VITAMUX_MASTER_KEY_FILE is not set")
		return 1
	}
	kr, err := crypto.Load(cfg.MasterKeyFile, cfg.PreviousMasterKeyFiles...)
	if err != nil {
		fmt.Fprintf(stderr, "keys rotate: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, cfg.DatabaseURL.Value(), db.AppRole)
	if err != nil {
		fmt.Fprintf(stderr, "database: %v\n", err)
		return 1
	}
	defer pool.Close()

	n, err := rotateKeys(ctx, db.New(pool), kr, rotateBatchSize, 0)
	fmt.Fprintf(stdout, "current key id: %s\ncredentials: %d re-sealed\nusers.totp: %d re-sealed\nprovider_app_credentials: %d re-sealed\nsidecars: %d re-sealed\n",
		kr.KeyID(), n.Credentials, n.TOTP, n.ProviderApps, n.Sidecars)
	if err != nil {
		fmt.Fprintf(stderr, "keys rotate: %v (run it again to continue)\n", err)
		return 1
	}
	return 0
}

// rotated counts the values re-sealed per table.
type rotated struct{ Credentials, TOTP, Documents, ProviderApps, Sidecars int }

// rotateKeys re-seals every value whose key_id differs from kr's current key, batchSize rows
// per transaction. A batch commits only whole, so an interrupted run leaves every row sealed
// under either key and the next run continues with the rest. maxBatches > 0 stops after that
// many batches (tests simulate an interruption with it). Rows locked by another transaction
// are skipped and picked up by the next run. One audit event with the counts is written when
// anything was re-sealed.
func rotateKeys(ctx context.Context, d *db.DB, kr *crypto.Keyring, batchSize, maxBatches int) (rotated, error) {
	var n rotated
	id := kr.KeyID()
	steps := []struct {
		count *int
		batch func(q *dbq.Queries) (int, error)
	}{
		{&n.Credentials, func(q *dbq.Queries) (int, error) {
			rows, err := q.LockCredentialsToRotate(ctx, dbq.LockCredentialsToRotateParams{KeyID: id, Batch: int32(batchSize)}) //nolint:gosec // small constant
			if err != nil {
				return 0, err
			}
			for _, r := range rows {
				sealed, err := reseal(kr, r.Ciphertext, crypto.CredentialsAAD(r.ConnectionID))
				if err != nil {
					return 0, fmt.Errorf("credentials %s: %w", r.ConnectionID, err)
				}
				if err := q.ResealCredential(ctx, dbq.ResealCredentialParams{Ciphertext: sealed, KeyID: id, ConnectionID: r.ConnectionID}); err != nil {
					return 0, err
				}
			}
			return len(rows), nil
		}},
		{&n.TOTP, func(q *dbq.Queries) (int, error) {
			rows, err := q.LockTOTPToRotate(ctx, dbq.LockTOTPToRotateParams{KeyID: &id, Batch: int32(batchSize)}) //nolint:gosec // small constant
			if err != nil {
				return 0, err
			}
			for _, r := range rows {
				sealed, err := reseal(kr, r.TotpCiphertext, crypto.TOTPAAD(r.ID))
				if err != nil {
					return 0, fmt.Errorf("users.totp %s: %w", r.ID, err)
				}
				if err := q.ResealTOTP(ctx, dbq.ResealTOTPParams{Ciphertext: sealed, KeyID: &id, ID: r.ID}); err != nil {
					return 0, err
				}
			}
			return len(rows), nil
		}},
		{&n.Documents, func(q *dbq.Queries) (int, error) {
			return documents.RotateKeyBatch(ctx, q, kr, int32(batchSize)) //nolint:gosec // small constant
		}},
		{&n.ProviderApps, func(q *dbq.Queries) (int, error) {
			rows, err := q.LockProviderAppsToRotate(ctx, dbq.LockProviderAppsToRotateParams{KeyID: id, Batch: int32(batchSize)}) //nolint:gosec // small constant
			if err != nil {
				return 0, err
			}
			for _, r := range rows {
				sealed, err := reseal(kr, r.Ciphertext, crypto.ProviderAppAAD(r.Provider))
				if err != nil {
					return 0, fmt.Errorf("provider_app_credentials %s: %w", r.Provider, err)
				}
				if err := q.ResealProviderApp(ctx, dbq.ResealProviderAppParams{Ciphertext: sealed, KeyID: id, Provider: r.Provider}); err != nil {
					return 0, err
				}
			}
			return len(rows), nil
		}},
		{&n.Sidecars, func(q *dbq.Queries) (int, error) {
			rows, err := q.LockSidecarsToRotate(ctx, dbq.LockSidecarsToRotateParams{KeyID: id, Batch: int32(batchSize)}) //nolint:gosec // small constant
			if err != nil {
				return 0, err
			}
			for _, r := range rows {
				sealed, err := reseal(kr, r.Ciphertext, crypto.SidecarAAD(r.Name))
				if err != nil {
					return 0, fmt.Errorf("sidecars %s: %w", r.Name, err)
				}
				if err := q.ResealSidecar(ctx, dbq.ResealSidecarParams{Ciphertext: sealed, KeyID: id, Name: r.Name}); err != nil {
					return 0, err
				}
			}
			return len(rows), nil
		}},
	}

	var err error
	batches := 0
steps:
	for _, s := range steps {
		for maxBatches == 0 || batches < maxBatches {
			var got int
			if err = d.Tx(ctx, func(q *dbq.Queries) (err error) { got, err = s.batch(q); return }); err != nil {
				break steps
			}
			if got == 0 {
				break
			}
			*s.count += got
			batches++
		}
	}
	if n == (rotated{}) {
		return n, err
	}
	auditErr := d.Tx(ctx, func(q *dbq.Queries) error {
		return audit.Record(ctx, q, audit.Event{
			Actor:  audit.System,
			Action: "keys.rotate",
			Detail: map[string]any{"credentials": n.Credentials, "users_totp": n.TOTP, "document_keys": n.Documents,
				"provider_app_credentials": n.ProviderApps, "sidecars": n.Sidecars},
		})
	})
	return n, errors.Join(err, auditErr)
}

// reseal opens a value with whichever keyring key sealed it and seals it under the current key.
func reseal(kr *crypto.Keyring, sealed, aad []byte) ([]byte, error) {
	pt, err := kr.Open(crypto.Credentials, sealed, aad)
	if err != nil {
		return nil, err
	}
	defer clear(pt)
	return kr.Seal(crypto.Credentials, pt, aad)
}

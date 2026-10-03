//go:build integration

package analytes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
	"github.com/KaanEmec/vitamux/internal/documents/analytes"
)

// The seed must contain exactly the catalogue with stable ids, and every seeded alias.
func TestSeedMatchesCatalogue(t *testing.T) {
	_, app := dbtest.Migrated(t)
	ctx := context.Background()
	rows, err := app.Query(ctx, `SELECT id, code, name, coalesce(canonical_unit, ''), coalesce(loinc, '') FROM analytes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := analytes.All()
	i := 0
	for ; rows.Next(); i++ {
		var id int16
		var code, name, unit, loinc string
		if err := rows.Scan(&id, &code, &name, &unit, &loinc); err != nil {
			t.Fatal(err)
		}
		if i >= len(want) || int(id) != i+1 || code != want[i].Code || name != want[i].Name || unit != want[i].Unit || loinc != want[i].LOINC {
			t.Fatalf("row %d: (%d, %s, %s, %s)", i, id, code, unit, loinc)
		}
	}
	if err := rows.Err(); err != nil || i != len(want) {
		t.Fatalf("analytes: %d of %d (err %v)", i, len(want), err)
	}
	aliases := 0
	for _, an := range want {
		aliases += len(an.SeedAliases())
	}
	var n int
	if err := app.QueryRow(ctx, `SELECT count(*) FROM analyte_aliases WHERE user_id IS NULL`).Scan(&n); err != nil || n != aliases {
		t.Fatalf("seed aliases: %d of %d (err %v)", n, aliases, err)
	}
	if _, err := app.Exec(ctx, `UPDATE analytes SET name = name`); err == nil {
		t.Fatal("the app role may change the catalogue")
	}
}

func TestAliases(t *testing.T) {
	u, app := dbtest.Migrated(t)
	ctx := context.Background()
	d := db.New(app)
	user, other := uuid.New(), uuid.New()
	owner := dbtest.Pool(t, u, db.OwnerRole)
	for i, id := range []uuid.UUID{user, other} {
		if _, err := owner.Exec(ctx, `INSERT INTO users (id, username, password_hash) VALUES ($1, $2, 'synthetic')`, id, []string{"a", "b"}[i]); err != nil {
			t.Fatal(err)
		}
	}
	suggest := func(u uuid.UUID, label string) string {
		t.Helper()
		code, _, err := analytes.Suggest(ctx, d.Q(), u, label)
		if err != nil {
			t.Fatal(err)
		}
		return code
	}
	if got := suggest(user, "  LDL-C "); got != "ldl_c" {
		t.Fatalf("seeded LDL-C -> %q", got)
	}
	if got := suggest(user, "Blutzucker"); got != "" {
		t.Fatalf("unknown label -> %q", got)
	}

	a, err := analytes.AddAlias(ctx, d, user, audit.Owner, "Blutzucker", "glucose")
	if err != nil || !a.Owner || a.Analyte != "glucose" {
		t.Fatalf("add: %+v %v", a, err)
	}
	if got := suggest(user, "blutzucker"); got != "glucose" {
		t.Fatalf("owner alias -> %q", got)
	}
	if got := suggest(other, "Blutzucker"); got != "" {
		t.Fatalf("another user's alias leaked: %q", got)
	}
	// An owner alias overrides a seeded one for the same label.
	if _, err := analytes.AddAlias(ctx, d, user, audit.Owner, "LDL", "ldl_c_direct"); err != nil {
		t.Fatal(err)
	}
	if got := suggest(user, "LDL"); got != "ldl_c_direct" {
		t.Fatalf("override -> %q", got)
	}
	if _, err := analytes.AddAlias(ctx, d, user, audit.Owner, "blutzucker!", "glucose"); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("duplicate label: %v", err)
	}
	if _, err := analytes.AddAlias(ctx, d, user, audit.Owner, "x", "not_a_code"); !errors.Is(err, analytes.ErrUnknownAnalyte) {
		t.Fatalf("unknown analyte: %v", err)
	}
	if _, err := analytes.AddAlias(ctx, d, user, audit.Owner, " -- ", "glucose"); !errors.Is(err, analytes.ErrEmptyLabel) {
		t.Fatalf("empty label: %v", err)
	}

	list, err := analytes.ListAliases(ctx, d, user)
	if err != nil {
		t.Fatal(err)
	}
	var seed *analytes.Alias
	owned := 0
	for i := range list {
		if list[i].Owner {
			owned++
		} else if seed == nil {
			seed = &list[i]
		}
	}
	if owned != 2 || seed == nil {
		t.Fatalf("list: %d owner aliases", owned)
	}
	if err := analytes.RemoveAlias(ctx, d, user, audit.Owner, seed.ID); !errors.Is(err, analytes.ErrSeedAlias) {
		t.Fatalf("remove seed alias: %v", err)
	}
	if err := analytes.RemoveAlias(ctx, d, other, audit.Owner, a.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("remove another user's alias: %v", err)
	}
	if err := analytes.RemoveAlias(ctx, d, user, audit.Owner, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := suggest(user, "Blutzucker"); got != "" {
		t.Fatalf("removed alias still suggests %q", got)
	}
}

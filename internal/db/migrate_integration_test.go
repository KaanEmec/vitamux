//go:build integration

package db_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

func TestConcurrentMigrateUp(t *testing.T) {
	url := dbtest.Empty(t)
	ctx := context.Background()
	app := dbtest.Pool(t, url, db.AppRole)
	if err := db.CheckSchema(ctx, app); err == nil || !strings.Contains(err.Error(), "migrate up") {
		t.Fatalf("unmigrated schema: got %v", err)
	}

	var wg sync.WaitGroup
	applied := make([]int, 2)
	errs := make([]error, 2)
	for i := range 2 {
		m, err := db.NewMigrator(dbtest.Pool(t, url, db.OwnerRole))
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			res, err := m.Up(ctx)
			applied[i], errs[i] = len(res), err
		})
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("up errors: %v", errs)
	}
	if got, want := applied[0]+applied[1], int(db.ExpectedVersion()); got != want || (applied[0] != 0 && applied[1] != 0) {
		t.Fatalf("applied %v, want exactly one run applying %d", applied, want)
	}
	if err := db.CheckSchema(ctx, app); err != nil {
		t.Fatal(err)
	}

	owner := dbtest.Pool(t, url, db.OwnerRole)
	if _, err := owner.Exec(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)", db.ExpectedVersion()+1); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx, app); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("newer schema: got %v", err)
	}
}

func TestAppRoleHasNoDDL(t *testing.T) {
	_, app := dbtest.Migrated(t)
	ctx := context.Background()
	for _, stmt := range []string{
		"CREATE TABLE nope (id int)",
		"CREATE TABLE public.nope (id int)",
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (999999, true)",
		"DELETE FROM goose_db_version",
	} {
		if _, err := app.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s: want permission denied, got %v", stmt, err)
		}
	}
}

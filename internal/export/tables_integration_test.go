//go:build integration

package export

import (
	"slices"
	"testing"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

// TestEveryTableClassified: a new table must be exported (tables, refTables) or listed in
// withoutFile with a reason, so owner data never silently misses the export.
func TestEveryTableClassified(t *testing.T) {
	_, pool := dbtest.Migrated(t)
	known := map[string]bool{}
	for _, tb := range tables {
		known[tb.name] = true
	}
	for _, tb := range refTables {
		known[tb.name] = true
	}
	for name := range withoutFile {
		if known[name] {
			t.Errorf("%s is listed in tables and withoutFile", name)
		}
		known[name] = true
	}
	rows, err := pool.Query(t.Context(), `SELECT tablename FROM pg_tables WHERE schemaname = $1`, db.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !known[name] {
			t.Errorf("table %s is neither exported (internal/export/tables.go) nor in withoutFile", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestProviderRefs: every exported column that references providers.id is translated by the
// importer (providerRefs), since a sidecar's provider has another id in another instance.
func TestProviderRefs(t *testing.T) {
	_, pool := dbtest.Migrated(t)
	rows, err := pool.Query(t.Context(), `SELECT cl.relname, a.attname FROM pg_constraint c
		JOIN pg_class cl ON cl.oid = c.conrelid
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND c.confrelid = to_regclass($1 || '.providers') ORDER BY 1, 2`, db.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string][]string{}
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		if _, skipped := withoutFile[table]; !skipped {
			got[table] = append(got[table], col)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for table, cols := range got {
		if !slices.Equal(cols, slices.Sorted(slices.Values(providerRefs[table]))) {
			t.Errorf("%s references providers through %v; providerRefs has %v", table, cols, providerRefs[table])
		}
	}
	for table := range providerRefs {
		if _, ok := got[table]; !ok {
			t.Errorf("providerRefs lists %s, which has no provider reference", table)
		}
	}
}

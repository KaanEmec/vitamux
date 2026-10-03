//go:build integration

package export

import (
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

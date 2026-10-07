//go:build integration

package catalog_test

import (
	"testing"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db/dbtest"
)

// The 00010 seed must contain exactly the catalogue, with canonical units and stable ids.
func TestSeedMatchesCatalogue(t *testing.T) {
	_, app := dbtest.Migrated(t)
	ctx := t.Context()

	rows, err := app.Query(ctx, `SELECT m.id, m.code, u.code FROM metric_catalog m JOIN units u ON u.id = m.unit_id ORDER BY m.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for want := catalog.Metrics(); rows.Next(); i++ {
		var id int16
		var code, unit string
		if err := rows.Scan(&id, &code, &unit); err != nil {
			t.Fatal(err)
		}
		if i >= len(want) || int(id) != i+1 || code != want[i].Code || unit != want[i].Unit {
			t.Fatalf("row %d: got (%d, %s, %s)", i, id, code, unit)
		}
	}
	if err := rows.Err(); err != nil || i != len(catalog.Metrics()) {
		t.Fatalf("metric rows: %d of %d (err %v)", i, len(catalog.Metrics()), err)
	}

	var n int
	if err := app.QueryRow(ctx, `SELECT count(*) FROM units`).Scan(&n); err != nil || n != catalog.UnitCount {
		t.Fatalf("units: %d of %d (err %v)", n, catalog.UnitCount, err)
	}
}

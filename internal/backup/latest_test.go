package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Latest(filepath.Join(dir, "missing")); ok || err != nil {
		t.Fatalf("missing dir: %v, %v", ok, err)
	}
	if _, ok, err := Latest(dir); ok || err != nil {
		t.Fatalf("empty dir: %v, %v", ok, err)
	}
	write := func(name string, at time.Time) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(Manifest{Format: Format, CreatedAt: at})
		if err := os.WriteFile(filepath.Join(dir, name, ManifestFile), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old, newest := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC), time.Date(2026, 10, 3, 3, 0, 0, 0, time.UTC)
	write("vitamux-20261001T030000Z", old)
	write("vitamux-20261003T030000Z", newest)
	// An unfinished backup (no manifest) and a stray directory never count, however new.
	if err := os.MkdirAll(filepath.Join(dir, "vitamux-20261004T030000Z"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "other"), 0o700); err != nil {
		t.Fatal(err)
	}
	m, ok, err := Latest(dir)
	if err != nil || !ok || !m.CreatedAt.Equal(newest) {
		t.Fatalf("latest: %v %v %v", m.CreatedAt, ok, err)
	}
}

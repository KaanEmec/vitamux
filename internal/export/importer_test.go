package export

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/KaanEmec/vitamux/internal/db"
)

func manifestFS(t *testing.T, m Manifest, files map[string]string) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	fsys[ManifestName] = &fstest.MapFile{Data: b}
	return fsys
}

func TestReadManifestRejectsOtherVersions(t *testing.T) {
	ok := Manifest{Format: Format, FormatVersion: FormatVersion, SchemaVersion: db.ExpectedVersion()}
	if _, err := readManifest(manifestFS(t, ok, nil)); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	for name, m := range map[string]Manifest{
		"format":  {Format: "other", FormatVersion: FormatVersion, SchemaVersion: db.ExpectedVersion()},
		"version": {Format: Format, FormatVersion: FormatVersion + 1, SchemaVersion: db.ExpectedVersion()},
		"schema":  {Format: Format, FormatVersion: FormatVersion, SchemaVersion: db.ExpectedVersion() - 1},
	} {
		if _, err := readManifest(manifestFS(t, m, nil)); !errors.Is(err, ErrIncompatible) {
			t.Errorf("%s: %v, want ErrIncompatible", name, err)
		}
	}
	if _, err := readManifest(fstest.MapFS{}); !errors.Is(err, ErrIncompatible) {
		t.Errorf("no manifest: %v", err)
	}
}

func TestLinesVerifiesChecksums(t *testing.T) {
	body := "{\"id\": 1}\n{\"id\": 2}\n"
	sum := sha256.Sum256([]byte(body))
	f := File{Name: "measurements.ndjson", Rows: 2, Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	m := Manifest{Files: []File{f}}
	im := &importer{fsys: manifestFS(t, m, map[string]string{f.Name: body}), man: &m}
	var n int
	if err := im.lines(f, func([]byte) error { n++; return nil }); err != nil || n != 2 {
		t.Fatalf("lines: %v after %d lines", err, n)
	}
	im.fsys = manifestFS(t, m, map[string]string{f.Name: "{\"id\": 1}\n{\"id\": 3}\n"})
	if err := im.lines(f, func([]byte) error { return nil }); !errors.Is(err, ErrChecksum) {
		t.Errorf("tampered file: %v, want ErrChecksum", err)
	}
}

func TestRowPatching(t *testing.T) {
	im := &importer{off: map[string]int64{"raw_payloads": 100, "normalizer_versions": 5},
		raws: map[int64]int64{7: 3}, nvs: map[int64]int64{}, owner: json.RawMessage(`"owner"`)}
	r := row{}
	if err := json.Unmarshal([]byte(`{"user_id": "src", "raw_payload_id": 7, "deleted_by_raw_id": 8,
		"normalizer_version_id": 2, "superseded_by": 9, "connection_id": "c"}`), &r); err != nil {
		t.Fatal(err)
	}
	if err := im.canonical(r); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	want := `{"connection_id":"c","deleted_by_raw_id":108,"normalizer_version_id":7,"raw_payload_id":3,"superseded_by":null,"user_id":"owner"}`
	if string(b) != want {
		t.Errorf("patched row\n got %s\nwant %s", b, want)
	}
}

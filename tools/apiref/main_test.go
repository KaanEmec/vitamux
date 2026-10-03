package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDocIsCurrent is the drift check for docs/api-reference.md.
func TestDocIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	want, err := renderFiles(filepath.Join(root, specPath), filepath.Join(root, authzPath))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, docPath))
	if err != nil {
		t.Fatalf("%v (run: go run ./tools/apiref)", err)
	}
	if string(got) != want {
		t.Errorf("%s is stale; run: go run ./tools/apiref", docPath)
	}
}

package backup

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Latest returns the manifest of the newest complete backup in dir, and false when there is
// none (or dir does not exist). It only reads the manifest: unlike Verify it neither checks
// the MAC nor hashes the files, so it is cheap enough for a status page.
func Latest(dir string) (Manifest, bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), namePrefix) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names))) // names sort by creation time
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(dir, n, ManifestFile)) //nolint:gosec // operator-configured backup directory
		if errors.Is(err, fs.ErrNotExist) {
			continue // not complete
		}
		if err != nil {
			return Manifest{}, false, err
		}
		var m Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return Manifest{}, false, err
		}
		return m, true, nil
	}
	return Manifest{}, false, nil
}

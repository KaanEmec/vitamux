package documents

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// apiNames are API field names that contain a listed word but are not copy; they are removed
// before a line is checked.
var apiNames = strings.NewReplacer("suggested_analyte", "")

// TestLabUICopyHasNoInterpretiveLanguage applies the prompt's word list to the lab UI
// (J12.6): the review and results pages show what was printed and what differs from the PDF,
// never a judgement. Every line counts, comments included.
func TestLabUICopyHasNoInterpretiveLanguage(t *testing.T) {
	files := 0
	for _, root := range []string{"../../web/src/routes/(app)/lab", "../../web/src/lib/lab"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files++
			for n, line := range strings.Split(string(b), "\n") {
				if m := forbiddenRe.FindString(apiNames.Replace(line)); m != "" {
					t.Errorf("%s:%d: forbidden word %q", path, n+1, m)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 5 {
		t.Fatalf("scanned %d files; the lab UI moved?", files)
	}
}

package documents

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// forbiddenRe lists words of judgement, diagnosis or advice. The extraction prompt and the
// schema it is sent with are transcription only (prompts/lab-extraction/REVIEW.md); none of
// these may appear in either.
var forbiddenRe = regexp.MustCompile(`(?i)\b(ab)?normal|\b(un)?healthy|\brisk|\brecommend|\bshould|diagnos|\bconcern|\bworr|\bdanger|\boptimal|deficien|\belevated|\bsuggest`)

func TestPromptHasNoInterpretiveLanguage(t *testing.T) {
	for _, path := range []string{"../../prompts/lab-extraction/v1.md", "../../schemas/lab-extraction.v1.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(b), "\n") {
			if m := forbiddenRe.FindString(line); m != "" {
				t.Errorf("%s:%d: forbidden word %q", path, n+1, m)
			}
		}
	}
}

func TestPromptNamesItsVersions(t *testing.T) {
	b, err := os.ReadFile("../../prompts/lab-extraction/v1.md")
	if err != nil {
		t.Fatal(err)
	}
	p := string(b)
	for _, want := range []string{PromptVersion, ExtractionSchema, "schemas/lab-extraction.v1.json", "Do not interpret results, add flags of your own, or advise", "Copy what is printed", "Never guess"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt does not contain %q", want)
		}
	}
	// Every warning code the schema allows is explained to the model.
	for _, w := range append(append([]string{}, RowWarnings...), DocumentWarnings...) {
		if !strings.Contains(p, "`"+w+"`") {
			t.Errorf("prompt does not explain warning %q", w)
		}
	}
}

func TestForbiddenWordsAreCaught(t *testing.T) {
	for _, s := range []string{"within normal limits", "Abnormal", "a healthy value", "cardiovascular risk", "we recommend", "you should", "diagnosis", "Diagnose"} {
		if !forbiddenRe.MatchString(s) {
			t.Errorf("%q not caught", s)
		}
	}
}

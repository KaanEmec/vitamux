//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/documents/extract"
)

func TestListExtractors(t *testing.T) {
	e, _, d := newExtractEnv(t)
	list := func() []oapi.Extractor {
		t.Helper()
		var body struct{ Extractors []oapi.Extractor }
		if err := json.Unmarshal(e.do(http.MethodGet, "GET /api/v1/extractors", "/api/v1/extractors", "", nil, http.StatusOK), &body); err != nil {
			t.Fatal(err)
		}
		return body.Extractors
	}
	// fake always, Gemini because it is configured; OpenAI is not configured, so not listed.
	got := list()
	if len(got) != 2 || got[0].ID != "fake" || !got[0].Enabled || got[0].External || got[0].Model != nil {
		t.Fatalf("extractors: %+v", got)
	}
	if g := got[1]; g.ID != "gemini" || !g.External || g.Enabled || g.Model == nil || *g.Model != "gemini-test" {
		t.Fatalf("gemini: %+v", g)
	}
	if err := extract.SetEnabled(context.Background(), d, e.user, audit.Owner, extract.Gemini, true); err != nil {
		t.Fatal(err)
	}
	if g := list()[1]; !g.Enabled {
		t.Errorf("gemini after enabling: %+v", g)
	}
}

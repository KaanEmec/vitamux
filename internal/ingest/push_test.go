package ingest

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// FuzzDecodeBatch feeds arbitrary bodies to the batch parser. CI runs it briefly
// (.github/workflows/ci.yml). Invariants: no panic; errors are *ValidationError; an
// accepted batch converts for StoreRaw with the body bytes unchanged.
func FuzzDecodeBatch(f *testing.F) {
	for _, name := range []string{"ingest-batch.v1.healthkit.json", "ingest-batch.v1.import-file.json"} {
		b, err := os.ReadFile(filepath.Join(schemaDir, "examples", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"schema":"vitamux.ingest.batch/1","items":[{"body":[]}]}`))
	f.Add([]byte(`{} {}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := DecodeBatch(data)
		if err != nil {
			if ve := (*ValidationError)(nil); !errors.As(err, &ve) || len(ve.Errors) == 0 {
				t.Fatalf("error is not a non-empty *ValidationError: %v", err)
			}
			return
		}
		if _, err := ParseConnectionID(b.ConnectionID); err != nil {
			t.Fatalf("accepted connection id does not parse: %v", err)
		}
		for i, it := range b.Items {
			raw, err := it.Raw()
			if err != nil {
				t.Fatalf("item %d: accepted but Raw failed: %v", i, err)
			}
			if err := raw.check(); err != nil {
				t.Fatalf("item %d: accepted but StoreRaw would reject it: %v", i, err)
			}
			if it.Body != nil && !bytes.Equal(raw.Body, it.Body) {
				t.Fatalf("item %d: body bytes changed", i)
			}
		}
	})
}

func TestBatchID(t *testing.T) {
	id := uuid.New()
	got, err := ParseBatchID(FormatBatchID(id))
	if err != nil || got != id {
		t.Fatalf("round trip: %v %v", got, err)
	}
	for _, bad := range []string{"", "bat_", "bat_" + id.String(), "conn_0123456789abcdef0123456789abcdef", "bat_0123456789ABCDEF0123456789ABCDEF"} {
		if _, err := ParseBatchID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNormalization(t *testing.T) {
	items := func(st ...Status) []BatchItemStatus {
		out := make([]BatchItemStatus, len(st))
		for i, s := range st {
			out[i].Status = s
		}
		return out
	}
	for _, c := range []struct {
		name  string
		items []BatchItemStatus
		job   string
		want  string
	}{
		{"all duplicates", nil, "", "done"},
		{"queued", items(StatusStored), "queued", "queued"},
		{"running", items(StatusNormalized, StatusStored), "running", "running"},
		{"normalized", items(StatusNormalized, StatusNormalized), "succeeded", "done"},
		{"one failed", items(StatusNormalized, StatusNormalizeFailed), "succeeded", "failed"},
		{"quarantined", items(StatusQuarantined), "succeeded", "failed"},
		{"job dead", items(StatusStored), "dead", "failed"},
		{"reprocess pending", items(StatusStored), "", "queued"},
		{"left stored by the job", items(StatusStored), "succeeded", "done"},
	} {
		if got := normalization(c.items, c.job); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

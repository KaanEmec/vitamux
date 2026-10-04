package imports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// SourceBatches is the import_runs.source of ImportBatches.
const SourceBatches = "batches"

const (
	maxBatchFile = 64 << 20  // one ingest batch file
	maxBlobFile  = 256 << 20 // one binary item
	maxFailures  = 20        // dry-run failures listed in the report
)

// BatchesOptions configures ImportBatches.
type BatchesOptions struct {
	Processor *normalize.Processor // normalizes stored items; its DB and Blobs store them
	DryRun    bool                 // validate and normalize in memory; write nothing
}

// StreamCount is one stream's share of an import.
type StreamCount struct {
	Items    int `json:"items"`
	Records  int `json:"records,omitempty"`  // dry run: canonical records the normalizer returned
	Warnings int `json:"warnings,omitempty"` // dry run: normalizer warnings
	Failed   int `json:"failed,omitempty"`   // dry run: items the normalizer rejected
}

// BatchesReport is what ImportBatches did; it is also the import run's stats.
type BatchesReport struct {
	DryRun      bool                    `json:"dry_run,omitempty"`
	Files       int                     `json:"files"`
	Items       int                     `json:"items"`
	New         int                     `json:"new"`        // stored, or a new version of a stored item
	Duplicates  int                     `json:"duplicates"` // same content as the stored version
	Streams     map[string]*StreamCount `json:"streams"`
	Connections []uuid.UUID             `json:"connections"`
	Failures    []string                `json:"failures,omitempty"` // dry run: "stream key: reason", the first few
	Normalized  normalize.Summary       `json:"normalized"`
}

// ImportBatches replays a directory of ingest batch files (schemas/ingest-batch.v1.json, one
// batch per *.json file, applied in name order) into the owner's connections they name, as if
// the connection had fetched them: each item becomes a raw payload of its connection and is
// normalized. Items that reference blob_sha256 read the file blobs/<sha256> next to the
// batches. This is how a collector's archive joins a live connection: the items' dedupe keys
// are the connection's, so a later sync of the same records adds no copies.
//
// It is idempotent per item (ingest.StoreRaw), so a second run stores nothing new and an
// interrupted run resumes by normalizing what was stored but not normalized.
func ImportBatches(ctx context.Context, dir string, opt BatchesOptions) (BatchesReport, error) {
	im := &batchImport{opt: opt, d: opt.Processor.DB, conns: map[uuid.UUID]string{},
		rep: BatchesReport{DryRun: opt.DryRun, Streams: map[string]*StreamCount{}}}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return im.rep, err
	}
	if len(files) == 0 {
		return im.rep, fmt.Errorf("batches: no *.json batch files in %s", dir)
	}
	slices.Sort(files)
	if im.owner, err = soleOwner(ctx, im.d, "batches"); err != nil {
		return im.rep, err
	}
	if !opt.DryRun {
		if im.vers, err = normalize.RegisterVersions(ctx, im.d.Q(), opt.Processor.Registry); err != nil {
			return im.rep, err
		}
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return im.rep, err
		}
		if err := im.file(ctx, dir, f); err != nil {
			return im.rep, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		im.rep.Files++
	}
	if opt.DryRun {
		return im.rep, nil
	}
	return im.rep, im.finish(ctx)
}

type batchImport struct {
	opt   BatchesOptions
	d     *db.DB
	owner uuid.UUID
	vers  map[string]int32
	conns map[uuid.UUID]string // checked connections and their provider
	rep   BatchesReport
}

// soleOwner returns the instance's only owner; importers need exactly one.
func soleOwner(ctx context.Context, d *db.DB, what string) (uuid.UUID, error) {
	owners, err := d.Q().ImportOwners(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if len(owners) != 1 {
		return uuid.Nil, fmt.Errorf("%s: the instance needs exactly one owner (vitamux admin create-owner)", what)
	}
	return owners[0], nil
}

// errNothingNew rolls back a batch row that would hold no raw payloads.
var errNothingNew = errors.New("batches: nothing new")

func (im *batchImport) file(ctx context.Context, dir, path string) error {
	data, err := readLimited(path, maxBatchFile)
	if err != nil {
		return err
	}
	b, err := ingest.DecodeBatch(data)
	if err != nil {
		return err
	}
	conn, err := ingest.ParseConnectionID(b.ConnectionID)
	if err != nil {
		return err
	}
	provider, err := im.connection(ctx, conn)
	if err != nil {
		return err
	}
	items := make([]ingest.RawItem, len(b.Items))
	blobs := map[string][]byte{} // binary item content by hex sha256
	for i, it := range b.Items {
		if items[i], err = it.Raw(); err != nil {
			return err // Validate accepted it, so the file is inconsistent
		}
		if it.BlobSHA256 != "" {
			if blobs[it.BlobSHA256], err = readBlob(dir, it.BlobSHA256); err != nil {
				return fmt.Errorf("item %d: %w", i, err)
			}
		}
		c := im.rep.Streams[it.Stream]
		if c == nil {
			c = &StreamCount{}
			im.rep.Streams[it.Stream] = c
		}
		c.Items++
		im.rep.Items++
	}
	if im.opt.DryRun {
		for i, it := range items {
			body := it.Body
			if it.BlobSHA256 != nil {
				body = blobs[hex.EncodeToString(it.BlobSHA256)]
			}
			im.normalizeInMemory(ctx, provider, it, body, i)
		}
		return nil
	}
	return im.store(ctx, b, conn, items, blobs)
}

// connection checks that conn belongs to the owner and is not disabled, and returns its provider.
func (im *batchImport) connection(ctx context.Context, conn uuid.UUID) (string, error) {
	if p, ok := im.conns[conn]; ok {
		return p, nil
	}
	c, err := im.d.Q().GetSyncConnection(ctx, conn)
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) || err == nil && c.UserID != im.owner {
		return "", fmt.Errorf("connection %s: not a connection of the owner", ingest.FormatConnectionID(conn))
	} else if err != nil {
		return "", err
	}
	if c.Status == "disabled" {
		return "", fmt.Errorf("connection %s is disabled", ingest.FormatConnectionID(conn))
	}
	im.conns[conn] = c.Provider
	im.rep.Connections = append(im.rep.Connections, conn)
	return c.Provider, nil
}

// normalizeInMemory runs the item's normalizer without the database and counts the outcome.
func (im *batchImport) normalizeInMemory(ctx context.Context, provider string, it ingest.RawItem, body []byte, i int) {
	c := im.rep.Streams[it.Stream]
	fp := ""
	if it.Body != nil && strings.Contains(it.ContentType, "json") {
		fp, _ = ingest.ShapeFingerprint(body) // invalid JSON: the normalizer reports it
	}
	fail := func(reason string) {
		c.Failed++
		if len(im.rep.Failures) < maxFailures {
			im.rep.Failures = append(im.rep.Failures, fmt.Sprintf("%s %s: %s", it.Stream, it.ExternalKey, reason))
		}
	}
	n, err := im.opt.Processor.Registry.For(it.Stream, fp)
	if err != nil {
		fail(err.Error())
		return
	}
	raw := normalize.RawPayload{ID: int64(-1 - i), Stream: it.Stream, ExternalKey: it.ExternalKey, ContentType: it.ContentType,
		FetchedAt: it.FetchedAt, RequestMeta: ingest.SanitizeRequest(it.Request), Body: body}
	out, err := n.Normalize(ctx, raw, normalize.Env{Provider: provider})
	if err == nil {
		err = out.Validate()
	}
	if err != nil {
		fail(err.Error())
		return
	}
	c.Records += len(out.Measurements) + len(out.Groups) + len(out.Sleep) + len(out.Workouts) + len(out.Events)
	c.Warnings += len(out.Warnings)
}

// store writes the batch's items to one ingest batch, then normalizes every item still stored
// (new ones, and ones an interrupted run left behind).
func (im *batchImport) store(ctx context.Context, b *ingest.Batch, conn uuid.UUID, items []ingest.RawItem, blobs map[string][]byte) error {
	info := ingest.BatchInfo{UserID: im.owner, ConnectionID: conn, SourceKind: ingest.SourceImport}
	if b.Provenance != nil && b.Provenance.MigrationSource != nil {
		info.MigrationSource = *b.Provenance.MigrationSource
	}
	var results []ingest.Result
	err := im.d.Tx(ctx, func(q *dbq.Queries) error {
		for sum, data := range blobs {
			raw, _ := hex.DecodeString(sum)
			_, err := q.GetBlob(ctx, raw)
			if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
				_, err = im.opt.Processor.Blobs.Put(ctx, q, bytes.NewReader(data), blob.Plain)
			}
			if err != nil {
				return err
			}
		}
		ref, err := ingest.CreateBatch(ctx, q, info)
		if err != nil {
			return err
		}
		if results, err = ingest.StoreRaw(ctx, q, im.opt.Processor.Blobs, ref, items); err != nil {
			return err
		}
		if !slices.ContainsFunc(results, func(r ingest.Result) bool { return r.Outcome != ingest.Duplicate }) {
			return errNothingNew
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNothingNew) {
		return err
	}
	for _, r := range results {
		if r.Outcome == ingest.Duplicate {
			im.rep.Duplicates++
		} else {
			im.rep.New++
		}
		status, err := im.d.Q().GetRawStatus(ctx, r.RawPayloadID)
		if err != nil {
			return db.MapErr(err)
		}
		if ingest.Status(status) != ingest.StatusStored {
			continue
		}
		res, err := im.opt.Processor.Process(ctx, r.RawPayloadID, im.vers)
		if err != nil {
			return err
		}
		im.rep.Normalized.Add(res)
	}
	return nil
}

func (im *batchImport) finish(ctx context.Context) error {
	stats, err := json.Marshal(im.rep)
	if err != nil {
		return err
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	var conn *uuid.UUID
	if len(im.rep.Connections) == 1 {
		conn = &im.rep.Connections[0]
	}
	return im.d.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.InsertImportRun(ctx, dbq.InsertImportRunParams{ID: runID, UserID: im.owner,
			ConnectionID: conn, Source: SourceBatches, Stats: stats}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &im.owner, Actor: audit.System, Action: "import." + SourceBatches,
			TargetType: "import_run", TargetID: runID.String(), Detail: map[string]any{"files": im.rep.Files,
				"items": im.rep.Items, "new": im.rep.New, "connections": len(im.rep.Connections)}})
	})
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the operator names the directory
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("larger than %d bytes", limit)
	}
	return data, err
}

// readBlob reads blobs/<sum> and checks that its content has that hash.
func readBlob(dir, sum string) ([]byte, error) {
	data, err := readLimited(filepath.Join(dir, "blobs", sum), maxBlobFile)
	if err != nil {
		return nil, fmt.Errorf("blob %s: %w", sum, err)
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != sum {
		return nil, fmt.Errorf("blob %s: content does not match its name", sum)
	}
	return data, nil
}

// String renders the report for the CLI.
func (r BatchesReport) String() string {
	var b bytes.Buffer
	verb := "imported"
	if r.DryRun {
		verb = "checked (dry run, nothing written)"
	}
	fmt.Fprintf(&b, "%d items in %d files %s", r.Items, r.Files, verb)
	if !r.DryRun {
		fmt.Fprintf(&b, ": %d new, %d already stored; normalized: %s", r.New, r.Duplicates, r.Normalized)
	}
	b.WriteString("\n")
	streams := make([]string, 0, len(r.Streams))
	for s := range r.Streams {
		streams = append(streams, s)
	}
	slices.Sort(streams)
	for _, s := range streams {
		c := r.Streams[s]
		fmt.Fprintf(&b, "%-40s %8d items", s, c.Items)
		if r.DryRun {
			fmt.Fprintf(&b, " %8d records %6d warnings %6d failed", c.Records, c.Warnings, c.Failed)
		}
		b.WriteString("\n")
	}
	for _, f := range r.Failures {
		fmt.Fprintf(&b, "failed: %s\n", f)
	}
	return b.String()
}

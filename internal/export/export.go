// Package export writes portable export zips and imports them again (J10.6;
// docs/architecture/api.md#exports).
//
// An export is a zip with one NDJSON file per table, in import order. Each line is the
// table row as PostgreSQL's to_jsonb renders it (column names as keys, bytea as "\\x…" hex,
// timestamps in UTC), so the importer can insert it again with jsonb_populate_record and keep
// ids, timestamps and provenance exactly. Credentials, sessions, API keys, client and webhook
// token hashes, jobs and cursors are never exported. Optional extras: measurements.csv (format csv: active
// measurements with catalogue codes, for spreadsheets) and blob_content.ndjson (base64 content of
// the ECG waveform and route documents, plus with include_raw the raw payloads and workout files). manifest.json, written last, lists every
// file with its row count and SHA-256, plus the schema version the rows belong to.
//
// All reads run in one REPEATABLE READ snapshot and are keyset-paged, so memory stays
// bounded by the page size whatever the data volume.
package export

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/version"
)

// Format names the file layout in manifest.json; FormatVersion changes when it does.
const (
	Format        = "vitamux-export"
	FormatVersion = 1
	ManifestName  = "manifest.json"
	contentName   = "blob_content.ndjson"
	csvName       = "measurements.csv"
)

// pageSize rows are read per query; it bounds the export's memory.
const pageSize = 2000

// Export formats: ndjson writes the NDJSON files only; csv adds measurements.csv.
const (
	FormatNDJSON = "ndjson"
	FormatCSV    = "csv"
)

// Options selects what Write exports.
type Options struct {
	UserID     uuid.UUID
	Format     string // FormatNDJSON or FormatCSV
	IncludeRaw bool   // add blob_content.ndjson; needs Write's blob store
}

// Manifest is manifest.json.
type Manifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	SchemaVersion int64     `json:"schema_version"`
	Vitamux       string    `json:"vitamux_version"`
	CreatedAt     time.Time `json:"created_at"`
	IncludeRaw    bool      `json:"include_raw"`
	Files         []File    `json:"files"`
	// RawMissing counts referenced blobs whose content was not in the blob store.
	RawMissing int `json:"raw_missing,omitempty"`
}

// File is one file of the zip.
type File struct {
	Name   string `json:"name"`
	Rows   int64  `json:"rows"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	// MaxID is the largest exported id of a table with an identity column; the importer
	// reserves that many ids.
	MaxID int64 `json:"max_id,omitempty"`
}

func (m *Manifest) file(name string) (File, bool) {
	for _, f := range m.Files {
		if f.Name == name {
			return f, true
		}
	}
	return File{}, false
}

// Write streams the export zip to w and returns its manifest. blobs may be nil unless
// o.IncludeRaw or the owner has waveform or route documents.
func Write(ctx context.Context, d *db.DB, blobs *blob.Store, w io.Writer, o Options) (*Manifest, error) {
	if o.Format != FormatNDJSON && o.Format != FormatCSV {
		return nil, fmt.Errorf("export: unknown format %q", o.Format)
	}
	if o.IncludeRaw && blobs == nil {
		return nil, errors.New("export: raw content needs the blob store")
	}
	m := &Manifest{Format: Format, FormatVersion: FormatVersion, SchemaVersion: db.ExpectedVersion(),
		Vitamux: version.Version, CreatedAt: time.Now().UTC().Truncate(time.Second), IncludeRaw: o.IncludeRaw}
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestSpeed)
	})
	e := &exporter{ctx: ctx, zw: zw, m: m, user: o.UserID, blobs: blobs}
	ran := false
	err := d.Tx(ctx, func(q *dbq.Queries) error {
		if ran { // the zip is half written; a retry cannot start over
			return errors.New("export: snapshot transaction retried")
		}
		ran = true
		e.q = q
		if err := q.ExportSnapshot(ctx); err != nil {
			return err
		}
		if err := q.ExportUTC(ctx); err != nil {
			return err
		}
		for _, t := range refTables {
			if err := e.file(t.name+".ndjson", func() (int64, error) {
				rows, err := t.rows(q, ctx)
				return all(e, rows, err)
			}); err != nil {
				return err
			}
		}
		for _, t := range tables {
			if err := e.file(t.name+".ndjson", func() (int64, error) { return t.export(e) }); err != nil {
				return fmt.Errorf("%s: %w", t.name, err)
			}
		}
		if o.Format == FormatCSV {
			if err := e.file(csvName, e.measurementsCSV); err != nil {
				return fmt.Errorf("csv: %w", err)
			}
		}
		// Waveforms and routes are canonical data, so their documents are exported whether or
		// not raw content is included.
		withContent := o.IncludeRaw
		if !withContent {
			files, err := q.ExportEventFiles(ctx, dbq.ExportEventFilesParams{UserID: o.UserID, Lim: 1})
			if err != nil {
				return err
			}
			withContent = len(files) > 0
		}
		if withContent {
			if blobs == nil {
				return errors.New("export: waveform and route documents need the blob store")
			}
			if err := e.file(contentName, func() (int64, error) { return e.content(o.IncludeRaw) }); err != nil {
				return fmt.Errorf("blob content: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: m.CreatedAt})
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(mw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return m, zw.Close()
}

type exporter struct {
	ctx   context.Context
	q     *dbq.Queries
	zw    *zip.Writer
	m     *Manifest
	user  uuid.UUID
	blobs *blob.Store

	cur  *bufio.Writer // the file being written
	rows int64
}

// file writes one zip entry through fill, which calls e.emit (or writes e.cur), and records
// it in the manifest.
func (e *exporter) file(name string, fill func() (int64, error)) error {
	fw, err := e.zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: e.m.CreatedAt})
	if err != nil {
		return err
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(fw, h)}
	e.cur, e.rows = bufio.NewWriterSize(cw, 64<<10), 0
	maxID, err := fill()
	if err == nil {
		err = e.cur.Flush()
	}
	if err != nil {
		return err
	}
	e.m.Files = append(e.m.Files, File{Name: name, Rows: e.rows, Bytes: cw.n, SHA256: hex.EncodeToString(h.Sum(nil)), MaxID: maxID})
	return nil
}

func (e *exporter) emit(row json.RawMessage) error {
	e.rows++
	if _, err := e.cur.Write(row); err != nil {
		return err
	}
	return e.cur.WriteByte('\n')
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// paged reads a table in key order, one page at a time, and emits every row. item returns a
// row's key and what to emit. paged returns the last key.
func paged[K, R, V any](fetch func(after K) ([]R, error), item func(R) (K, V), emit func(V) error) (K, error) {
	var after K
	for {
		rs, err := fetch(after)
		if err != nil {
			return after, err
		}
		for _, r := range rs {
			var v V
			after, v = item(r)
			if err := emit(v); err != nil {
				return after, err
			}
		}
		if len(rs) < pageSize {
			return after, nil
		}
	}
}

// measurementsCSV writes active measurements with catalogue codes.
func (e *exporter) measurementsCSV() (int64, error) {
	cw := csv.NewWriter(e.cur)
	if err := cw.Write([]string{"id", "metric", "unit", "kind", "start_at", "end_at", "tz_offset_min", "local_date", "value",
		"source_value", "source_unit", "provider", "connection_id", "device_id", "origin_id", "group_id", "external_id",
		"quality_flags", "raw_payload_id"}); err != nil {
		return 0, err
	}
	_, err := paged(func(after int64) ([]dbq.ExportMeasurementsCSVRow, error) {
		return e.q.ExportMeasurementsCSV(e.ctx, dbq.ExportMeasurementsCSVParams{UserID: e.user, After: after, Lim: pageSize})
	}, func(r dbq.ExportMeasurementsCSVRow) (int64, dbq.ExportMeasurementsCSVRow) { return r.ID, r }, func(r dbq.ExportMeasurementsCSVRow) error {
		e.rows++
		return cw.Write([]string{strconv.FormatInt(r.ID, 10), r.Metric, r.Unit, r.Kind, csvTime(&r.StartAt), csvTime(r.EndAt),
			csvInt(r.TzOffsetMin), r.LocalDate.Format(time.DateOnly), strconv.FormatFloat(r.Value, 'g', -1, 64),
			csvFloat(r.SourceValue), csvStr(r.SourceUnit), r.Provider, r.ConnectionID.String(), csvUUID(r.DeviceID),
			csvUUID(r.OriginID), csvInt(r.GroupID), csvStr(r.ExternalID), strconv.FormatInt(int64(r.QualityFlags), 10),
			csvInt(r.RawPayloadID)})
	})
	cw.Flush()
	if err == nil {
		err = cw.Error()
	}
	return 0, err
}

func csvTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func csvInt[T int16 | int64](v *T) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(int64(*v), 10)
}

func csvFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'g', -1, 64)
}

func csvStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func csvUUID(v *uuid.UUID) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// content writes blob_content.ndjson: one line per event document (ECG waveform, workout
// route) and, with raw, per raw payload and workout file, {"sha256": "<hex>", "content_base64":
// "…"}, streamed from the blob store. A blob shared by several rows appears once per row;
// content the store lacks is counted, not fatal.
func (e *exporter) content(raw bool) (int64, error) {
	_, err := paged(func(after uuid.UUID) ([]dbq.ExportEventFilesRow, error) {
		return e.q.ExportEventFiles(e.ctx, dbq.ExportEventFilesParams{UserID: e.user, After: after, Lim: pageSize})
	}, func(r dbq.ExportEventFilesRow) (uuid.UUID, []byte) { return r.ID, r.FileBlobSha256 }, e.blobLine)
	if err != nil || !raw {
		return 0, err
	}
	_, err = paged(func(after int64) ([]dbq.ExportRawContentRow, error) {
		return e.q.ExportRawContent(e.ctx, dbq.ExportRawContentParams{UserID: e.user, After: after, Lim: pageSize})
	}, func(r dbq.ExportRawContentRow) (int64, []byte) { return r.ID, r.ContentSha256 }, e.blobLine)
	if err != nil {
		return 0, err
	}
	_, err = paged(func(after uuid.UUID) ([]dbq.ExportWorkoutFilesRow, error) {
		return e.q.ExportWorkoutFiles(e.ctx, dbq.ExportWorkoutFilesParams{UserID: e.user, After: after, Lim: pageSize})
	}, func(r dbq.ExportWorkoutFilesRow) (uuid.UUID, []byte) { return r.ID, r.FileBlobSha256 }, e.blobLine)
	return 0, err
}

// blobLine writes the content of the blob whose hash is sum.
func (e *exporter) blobLine(sum []byte) error {
	rc, err := e.blobs.Open(sum)
	if errors.Is(err, blob.ErrNotFound) {
		e.m.RawMissing++
		return nil
	}
	if err != nil {
		return err
	}
	defer rc.Close()
	if _, err := fmt.Fprintf(e.cur, `{"sha256": "%x", "content_base64": "`, sum); err != nil {
		return err
	}
	enc := base64.NewEncoder(base64.StdEncoding, e.cur)
	if _, err := io.Copy(enc, rc); err != nil { // ErrCorrupt on a hash mismatch fails the export
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	e.rows++
	_, err = e.cur.WriteString("\"}\n")
	return err
}

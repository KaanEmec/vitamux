package export

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/blob"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// The importer writes rows through its own path, not normalize.Write: it must keep exported
// ids, timestamps, normalizer versions and superseded chains, which the canonical writer
// would assign anew. Everything runs in one transaction, so a failed import leaves nothing.
//
// Ids: uuid ids are kept. Identity ids are shifted by an offset: the importer first moves
// each sequence past the exported maximum (ReserveIDs), so in a fresh instance the offset
// is 0 and ids stay identical, and in a merge they land in a range nobody else uses.
// Rows the target already has are skipped and their references remapped: registry rows by
// natural key (connection account, device fingerprint, origin key, normalizer version),
// raw payloads by (connection, stream, external key, version), and canonical chains by
// dedupe key (a chain whose dedupe key has an active row in the target is skipped whole).
// The remap tables hold only the skipped rows of small tables plus raw payloads, so memory
// stays bounded by what already existed. All rows move to the target's single owner.

var (
	// ErrNotEmpty means the target already holds health data and Merge was not set.
	ErrNotEmpty = errors.New("import: the target instance already has data (use --merge to add to it)")
	// ErrIncompatible means the export's format or schema version does not match this build.
	ErrIncompatible = errors.New("import: incompatible export")
	// ErrChecksum means a file does not match manifest.json.
	ErrChecksum = errors.New("import: file does not match the manifest")
)

const (
	batchRows  = 1000
	batchBytes = 4 << 20
	maxLine    = 64 << 20 // a base64 raw file of up to ~48 MiB
)

// ImportOptions configures Import.
type ImportOptions struct {
	// Merge adds to a target that already has data; rows it already has are skipped.
	Merge bool
	// Blobs stores raw content; required when the export includes it.
	Blobs *blob.Store
}

// TableStats counts one table's rows.
type TableStats struct {
	Name     string `json:"name"`
	Read     int64  `json:"read"`
	Inserted int64  `json:"inserted"`
}

// ImportStats is what Import did, table by table in import order.
type ImportStats struct {
	Tables     []TableStats `json:"tables"`
	RawContent int64        `json:"raw_content"` // blob_content.ndjson lines stored
}

// Import loads the export in fsys (an unpacked directory or an opened zip) into d.
func Import(ctx context.Context, d *db.DB, fsys fs.FS, o ImportOptions) (ImportStats, error) {
	man, err := readManifest(fsys)
	if err != nil {
		return ImportStats{}, err
	}
	var st ImportStats
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		im := &importer{ctx: ctx, q: q, fsys: fsys, man: man, opts: o, off: map[string]int64{},
			conns: map[uuid.UUID]uuid.UUID{}, devices: map[uuid.UUID]uuid.UUID{}, origins: map[uuid.UUID]uuid.UUID{},
			batches: map[uuid.UUID]uuid.UUID{}, nvs: map[int64]int64{}, raws: map[int64]int64{}, groups: map[int64]int64{},
			sessions: map[uuid.UUID]bool{}, workouts: map[uuid.UUID]bool{}, provs: map[int64]int64{}}
		if err := im.run(); err != nil {
			return err
		}
		st = im.stats
		return nil
	})
	return st, err
}

func readManifest(fsys fs.FS) (*Manifest, error) {
	b, err := fs.ReadFile(fsys, ManifestName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIncompatible, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%w: manifest: %w", ErrIncompatible, err)
	}
	switch {
	case m.Format != Format || m.FormatVersion != FormatVersion:
		return nil, fmt.Errorf("%w: format %q version %d, want %q version %d", ErrIncompatible, m.Format, m.FormatVersion, Format, FormatVersion)
	case m.SchemaVersion != db.ExpectedVersion():
		return nil, fmt.Errorf("%w: exported at schema version %d, this build is at %d; import with the matching vitamux version",
			ErrIncompatible, m.SchemaVersion, db.ExpectedVersion())
	}
	return &m, nil
}

type importer struct {
	ctx   context.Context
	q     *dbq.Queries
	fsys  fs.FS
	man   *Manifest
	opts  ImportOptions
	owner json.RawMessage
	now   json.RawMessage
	off   map[string]int64 // table → id offset

	// Exported id → target id, for rows the target already had.
	conns, devices, origins, batches map[uuid.UUID]uuid.UUID
	nvs, raws, groups                map[int64]int64
	// Exported provider id → target id, only where they differ (see providers).
	provs map[int64]int64
	// Sleep sessions and workouts this import inserted; only their stages and segments follow.
	sessions, workouts map[uuid.UUID]bool

	blobRows [][]byte // blob rows of the raw payload or workout batch being built
	stats    ImportStats
}

func (im *importer) run() error {
	if err := im.q.ImportCustomPlans(im.ctx); err != nil {
		return err
	}
	owners, err := im.q.ImportOwners(im.ctx)
	if err != nil {
		return err
	}
	if len(owners) != 1 {
		return errors.New("import: the target needs exactly one owner (vitamux admin create-owner)")
	}
	im.owner, _ = json.Marshal(owners[0])
	im.now, _ = json.Marshal(time.Now().UTC())
	if !im.opts.Merge {
		has, err := im.q.HasHealthData(im.ctx)
		if err != nil {
			return err
		}
		if has != nil && *has {
			return ErrNotEmpty
		}
	}
	if err := im.checkRefs(); err != nil {
		return err
	}
	for _, t := range tables {
		if f, ok := im.man.file(t.name + ".ndjson"); ok && t.seq != "" && f.MaxID > 0 {
			if im.off[t.name], err = im.q.ReserveIDs(im.ctx, dbq.ReserveIDsParams{Seq: t.seq, N: f.MaxID}); err != nil {
				return fmt.Errorf("reserve %s ids: %w", t.name, err)
			}
		}
	}
	if err := im.content(); err != nil {
		return fmt.Errorf("raw content: %w", err)
	}
	for _, t := range tables {
		if t.name == "audit_events" && im.opts.Merge {
			continue // no natural key: merging would duplicate the trail
		}
		if err := im.table(t); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
		if t.link != nil {
			if err := t.link(im); err != nil {
				return fmt.Errorf("%s links: %w", t.name, err)
			}
		}
	}
	stats, err := json.Marshal(im.stats)
	if err != nil {
		return err
	}
	runID := uuid.Must(uuid.NewV7())
	if err := im.q.RecordImportRun(im.ctx, dbq.RecordImportRunParams{ID: runID, UserID: owners[0], Stats: stats}); err != nil {
		return err
	}
	return audit.Record(im.ctx, im.q, audit.Event{UserID: &owners[0], Actor: audit.System, Action: "import.ndjson",
		TargetType: "import_run", TargetID: runID.String(), Detail: map[string]any{"merge": im.opts.Merge, "exported_at": im.man.CreatedAt}})
}

// checkRefs makes sure the seeded reference rows the export's ids point at mean the same here.
// Providers are the exception: sidecars register theirs at runtime, so ids depend on the
// instance and providers are matched by code (see providers).
func (im *importer) checkRefs() error {
	for _, t := range refTables {
		f, ok := im.man.file(t.name + ".ndjson")
		if !ok {
			continue
		}
		if t.name == "providers" {
			if err := im.providers(f); err != nil {
				return err
			}
			continue
		}
		local, err := t.rows(im.q, im.ctx)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, r := range local {
			have[refKey(r)] = true
		}
		err = im.lines(f, func(line []byte) error {
			if !have[refKey(line)] {
				return fmt.Errorf("%w: %s row %s differs from this instance", ErrIncompatible, t.name, line)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// providers registers the export's providers this instance lacks (a sidecar's, or the same
// ones registered in another order) and records the id of each one that differs here.
func (im *importer) providers(f File) error {
	type provider struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
		Name string `json:"name"`
	}
	var exported []provider
	err := im.lines(f, func(line []byte) error {
		var p provider
		if err := json.Unmarshal(line, &p); err != nil {
			return err
		}
		exported = append(exported, p)
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range exported {
		if err := im.q.RegisterProvider(im.ctx, dbq.RegisterProviderParams{Code: p.Code, Name: p.Name}); err != nil {
			return fmt.Errorf("%w: provider %q: %w", ErrIncompatible, p.Code, err)
		}
	}
	local, err := im.q.ExportProviders(im.ctx)
	if err != nil {
		return err
	}
	ids := map[string]int64{}
	for _, l := range local {
		var p provider
		if err := json.Unmarshal(l, &p); err != nil {
			return err
		}
		ids[p.Code] = p.ID
	}
	for _, p := range exported {
		if ids[p.Code] != p.ID {
			im.provs[p.ID] = ids[p.Code]
		}
	}
	return nil
}

// providerRefs lists the exported columns that reference providers.id; they are translated to
// the target's ids. TestProviderRefs checks the list against the schema.
var providerRefs = map[string][]string{
	"connections": {"provider_id"}, "devices": {"provider_id"}, "data_origins": {"provider_id", "relayed_provider_id"},
	"measurement_groups": {"provider_id"}, "measurements": {"provider_id"}, "sleep_sessions": {"provider_id"}, "workouts": {"provider_id"},
}

func (im *importer) provRef(r row, k string) error {
	if r.null(k) || len(im.provs) == 0 {
		return nil
	}
	v, err := r.int(k)
	if n, ok := im.provs[v]; ok {
		r.setInt(k, n)
	}
	return err
}

// refKey is a reference row's id and code.
func refKey(b []byte) string {
	var r struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	_ = json.Unmarshal(b, &r)
	return strconv.FormatInt(r.ID, 10) + "/" + r.Code
}

// lines calls fn for every line of f and then checks the file against the manifest.
func (im *importer) lines(f File, fn func([]byte) error) error {
	rc, err := im.fsys.Open(f.Name)
	if err != nil {
		return err
	}
	defer rc.Close()
	h := sha256.New()
	sc := bufio.NewScanner(io.TeeReader(rc, h))
	sc.Buffer(make([]byte, 64<<10), maxLine)
	var n int64
	for sc.Scan() {
		n++
		if err := fn(sc.Bytes()); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if n != f.Rows || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("%w: %s", ErrChecksum, f.Name)
	}
	return nil
}

// table inserts one table's rows in batches: decode, patch, re-encode, insert.
func (im *importer) table(t table) error {
	f, ok := im.man.file(t.name + ".ndjson")
	if !ok {
		return nil
	}
	st := TableStats{Name: t.name}
	var batch bytes.Buffer
	rows := 0
	flush := func() error {
		if rows == 0 {
			return nil
		}
		batch.WriteByte(']')
		n, err := t.insert(im, batch.Bytes())
		st.Inserted += n
		batch.Reset()
		rows = 0
		return err
	}
	err := im.lines(f, func(line []byte) error {
		st.Read++
		r := row{}
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		for _, k := range providerRefs[t.name] {
			if err := im.provRef(r, k); err != nil {
				return err
			}
		}
		keep, err := t.patch(im, r)
		if err != nil || !keep {
			return err
		}
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if rows == 0 {
			batch.WriteByte('[')
		} else {
			batch.WriteByte(',')
		}
		batch.Write(b)
		if rows++; rows == batchRows || batch.Len() >= batchBytes {
			return flush()
		}
		return nil
	})
	if err == nil {
		err = flush()
	}
	im.stats.Tables = append(im.stats.Tables, st)
	return err
}

// content stores blob_content.ndjson in the blob store before any row references it.
func (im *importer) content() error {
	f, ok := im.man.file(contentName)
	if !ok {
		return nil
	}
	if im.opts.Blobs == nil {
		return errors.New("the export includes raw content: configure the master key and data directory")
	}
	return im.lines(f, func(line []byte) error {
		var c struct {
			SHA256  string `json:"sha256"`
			Content []byte `json:"content_base64"`
		}
		if err := json.Unmarshal(line, &c); err != nil {
			return err
		}
		info, err := im.opts.Blobs.Put(im.ctx, im.q, bytes.NewReader(c.Content), blob.Plain)
		if err != nil {
			return err
		}
		if hex.EncodeToString(info.SHA256) != c.SHA256 {
			return fmt.Errorf("%w: blob %s content has another hash", ErrChecksum, c.SHA256)
		}
		im.stats.RawContent++
		return nil
	})
}

// link sets superseded_by once a table's chains are all in, reading the file again.
func (im *importer) link(name string, set func(ids, by []string) error) error {
	f, ok := im.man.file(name + ".ndjson")
	if !ok {
		return nil
	}
	var ids, by []string
	flush := func() error {
		if len(ids) == 0 {
			return nil
		}
		err := set(ids, by)
		ids, by = ids[:0], by[:0]
		return err
	}
	null := []byte(`"superseded_by": null`)
	err := im.lines(f, func(line []byte) error {
		if bytes.Contains(line, null) {
			return nil
		}
		var r struct {
			ID           json.RawMessage `json:"id"`
			SupersededBy json.RawMessage `json:"superseded_by"`
		}
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		if len(r.SupersededBy) == 0 || string(r.SupersededBy) == "null" {
			return nil
		}
		ids, by = append(ids, string(r.ID)), append(by, string(r.SupersededBy))
		if len(ids) == batchRows {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// linkInt links a table with identity ids, shifted by its offset.
func (im *importer) linkInt(name string, set func(context.Context, []int64, []int64) error) error {
	off := im.off[name]
	return im.link(name, func(ids, by []string) error {
		a, b := make([]int64, len(ids)), make([]int64, len(by))
		for i := range ids {
			x, err1 := strconv.ParseInt(ids[i], 10, 64)
			y, err2 := strconv.ParseInt(by[i], 10, 64)
			if err := errors.Join(err1, err2); err != nil {
				return err
			}
			a[i], b[i] = x+off, y+off
		}
		return set(im.ctx, a, b)
	})
}

// linkUUID links a table with uuid ids.
func (im *importer) linkUUID(name string, set func(context.Context, []uuid.UUID, []uuid.UUID) error) error {
	return im.link(name, func(ids, by []string) error {
		a, b := make([]uuid.UUID, len(ids)), make([]uuid.UUID, len(by))
		for i := range ids {
			x, err1 := parseQuotedUUID(ids[i])
			y, err2 := parseQuotedUUID(by[i])
			if err := errors.Join(err1, err2); err != nil {
				return err
			}
			a[i], b[i] = x, y
		}
		return set(im.ctx, a, b)
	})
}

func parseQuotedUUID(s string) (uuid.UUID, error) {
	var v string
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(v)
}

// row is one exported row, keyed by column, values kept as JSON.
type row map[string]json.RawMessage

var jsonNull = json.RawMessage("null")

func (r row) null(k string) bool { v, ok := r[k]; return !ok || bytes.Equal(v, jsonNull) }

func (r row) int(k string) (int64, error) {
	var v int64
	err := json.Unmarshal(r[k], &v)
	return v, err
}

func (r row) setInt(k string, v int64) { r[k] = strconv.AppendInt(nil, v, 10) }

func (r row) uuid(k string) (uuid.UUID, error) {
	var s string
	if err := json.Unmarshal(r[k], &s); err != nil {
		return uuid.Nil, err
	}
	return uuid.Parse(s)
}

func (r row) setUUID(k string, v uuid.UUID) { r[k] = json.RawMessage(`"` + v.String() + `"`) }

// own moves the row to the target's owner.
func (im *importer) own(r row) {
	if !r.null("user_id") {
		r["user_id"] = im.owner
	}
}

// shift moves the row's identity id by its table's offset.
func (im *importer) shift(r row, table string) error {
	id, err := r.int("id")
	r.setInt("id", id+im.off[table])
	return err
}

// ref points an identity reference at the target row: a remapped one, or the shifted id.
func (im *importer) ref(r row, k, table string, remap map[int64]int64) error {
	if r.null(k) {
		return nil
	}
	v, err := r.int(k)
	if n, ok := remap[v]; ok {
		r.setInt(k, n)
	} else {
		r.setInt(k, v+im.off[table])
	}
	return err
}

// refUUID points a uuid reference at the target row when the target already had it.
func (im *importer) refUUID(r row, k string, remap map[uuid.UUID]uuid.UUID) error {
	if r.null(k) || len(remap) == 0 {
		return nil
	}
	v, err := r.uuid(k)
	if n, ok := remap[v]; ok {
		r.setUUID(k, n)
	}
	return err
}

// canonical patches the provenance shared by canonical rows. superseded_by is set by the
// link pass, once the newer row exists.
func (im *importer) canonical(r row) error {
	im.own(r)
	r["superseded_by"] = jsonNull
	return errors.Join(
		im.refUUID(r, "connection_id", im.conns),
		im.refUUID(r, "device_id", im.devices),
		im.refUUID(r, "origin_id", im.origins),
		im.ref(r, "raw_payload_id", "raw_payloads", im.raws),
		im.ref(r, "deleted_by_raw_id", "raw_payloads", im.raws),
		im.ref(r, "normalizer_version_id", "normalizer_versions", im.nvs),
	)
}

// takeBlob moves the embedded _blob row (for content the export did not include) into the
// pending blob rows, with no references yet.
func (im *importer) takeBlob(r row) error {
	b, ok := r["_blob"]
	delete(r, "_blob")
	if !ok || bytes.Equal(b, jsonNull) {
		return nil
	}
	br := row{}
	if err := json.Unmarshal(b, &br); err != nil {
		return err
	}
	br["refcount"] = json.RawMessage("0")
	enc, err := json.Marshal(br)
	im.blobRows = append(im.blobRows, enc)
	return err
}

// flushBlobs inserts the pending blob rows (existing ones, e.g. stored from
// blob_content.ndjson, are kept).
func (im *importer) flushBlobs() error {
	if len(im.blobRows) == 0 {
		return nil
	}
	_, err := im.q.ImportBlobs(im.ctx, append(append([]byte{'['}, bytes.Join(im.blobRows, []byte{','})...), ']'))
	im.blobRows = im.blobRows[:0]
	return err
}

// remapped records rows that resolved to another target id and counts the inserted ones.
func remapped[K comparable](m map[K]K, src, id K, inserted bool, n *int64) {
	if src != id {
		m[src] = id
	}
	if inserted {
		*n++
	}
}

// resolved records where a shifted identity row src ended up: inserted (1), or skipped for
// the target's row id, which references to the exported id then use.
func (im *importer) resolved(m map[int64]int64, table string, src, id int64, inserted bool) int64 {
	if inserted {
		return 1
	}
	m[src-im.off[table]] = id
	return 0
}

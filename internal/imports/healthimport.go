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
	"math"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/connectors/applehealth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// SourceAppleHealthExport is the import_runs.source of ImportAppleHealth.
const SourceAppleHealthExport = "apple_health_export"

// AppleHealthOptions configures ImportAppleHealth.
type AppleHealthOptions struct {
	Processor *normalize.Processor // normalizes each stored page; its DB and Blobs store them
	Limits    Limits
	PageSize  int // records per raw page; 0 means 1,000
}

// Overlap counts one type's records that matched app-synced data and their time range.
type Overlap struct {
	Records int64     `json:"records"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
}

// AppleHealthReport is what ImportAppleHealth did; it is also the import run's stats.
type AppleHealthReport struct {
	ConnectionID uuid.UUID           `json:"connection_id"`
	Records      int64               `json:"records"`
	Pages        int                 `json:"pages"`
	NewPages     int                 `json:"new_pages"`
	Overlaps     map[string]*Overlap `json:"overlaps,omitempty"` // by HealthKit type
	Normalized   normalize.Summary   `json:"normalized"`
}

// ImportAppleHealth imports an Apple Health export.xml read from r (see OpenExport) into the
// owner's apple_health connection, the one paired devices share (created if there is none).
//
// Records are stored raw as found (stream apple_health.export.v1), in pages of PageSize
// consecutive records of one type (sleep pages end between nights), then normalized through the HealthKit mapping. A page's key
// is its type and the hash of its record ids, so importing the same export again stores and
// changes nothing. A record that matches data the app already synced (same type, origin, start,
// end and value; AppleExportOverlaps) is kept in the page's overlapping list, counted in the
// report and not normalized, so the export never adds a second copy of synced data.
func ImportAppleHealth(ctx context.Context, r io.Reader, opt AppleHealthOptions) (AppleHealthReport, error) {
	if opt.PageSize <= 0 {
		opt.PageSize = 1000
	}
	im := &appleImport{opt: opt, d: opt.Processor.DB, rep: AppleHealthReport{Overlaps: map[string]*Overlap{}}}
	if err := im.setup(ctx); err != nil {
		return im.rep, err
	}
	pending := map[string][]applehealth.ExportRecord{}
	err := ParseExport(r, opt.Limits, func(typ string, rec applehealth.ExportRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		im.rep.Records++
		full := len(pending[typ]) >= opt.PageSize
		if typ == applehealth.SleepType {
			// Cut sleep pages between nights only: a session split across pages would become two.
			start, end := applehealth.ExportSpan(rec)
			full = full && start.After(im.sleepEnd.Add(applehealth.SleepGap)) || len(pending[typ]) >= 10*opt.PageSize
			if end.After(im.sleepEnd) {
				im.sleepEnd = end
			}
		}
		if full {
			recs := pending[typ]
			pending[typ] = nil
			if err := im.page(ctx, typ, recs); err != nil {
				return err
			}
		}
		pending[typ] = append(pending[typ], rec)
		return nil
	})
	if err != nil {
		return im.rep, err
	}
	types := make([]string, 0, len(pending))
	for typ := range pending {
		types = append(types, typ)
	}
	slices.Sort(types)
	for _, typ := range types {
		if err := im.page(ctx, typ, pending[typ]); err != nil {
			return im.rep, err
		}
	}
	return im.rep, im.finish(ctx)
}

type appleImport struct {
	opt      AppleHealthOptions
	d        *db.DB
	owner    uuid.UUID
	vers     map[string]int32
	batch    *ingest.BatchRef // created with the first new page
	sleepEnd time.Time        // latest end of the sleep records read so far
	rep      AppleHealthReport
}

func (im *appleImport) setup(ctx context.Context) error {
	owner, err := soleOwner(ctx, im.d, "apple health export")
	if err != nil {
		return err
	}
	im.owner = owner
	err = im.d.Tx(ctx, func(q *dbq.Queries) error {
		conn, err := q.OwnerApplePushConnection(ctx, im.owner)
		if err = db.MapErr(err); !errors.Is(err, db.ErrNotFound) {
			im.rep.ConnectionID = conn
			return err
		}
		if conn, err = uuid.NewV7(); err != nil {
			return err
		}
		if _, err := q.InsertPushConnection(ctx, dbq.InsertPushConnectionParams{ID: conn, UserID: im.owner, Provider: applehealth.Provider}); err != nil {
			return err
		}
		im.rep.ConnectionID = conn
		return audit.Record(ctx, q, audit.Event{UserID: &im.owner, Actor: audit.System, Action: "connection.create",
			TargetType: "connection", TargetID: conn.String(), Detail: map[string]any{"provider": applehealth.Provider, "mode": "push"}})
	})
	if err != nil {
		return err
	}
	im.vers, err = normalize.RegisterVersions(ctx, im.d.Q(), im.opt.Processor.Registry)
	return err
}

// page splits recs by overlap, stores the page unless the same page is stored already, and
// normalizes it unless that was done (an interrupted run resumes here).
func (im *appleImport) page(ctx context.Context, typ string, recs []applehealth.ExportRecord) error {
	overlaps, err := im.overlaps(ctx, typ, recs)
	if err != nil {
		return err
	}
	pg := applehealth.ExportPage{Type: typ, Records: []applehealth.ExportRecord{}}
	ids := sha256.New()
	for i, rec := range recs {
		ids.Write([]byte(applehealth.ExportID(typ, rec) + "\n"))
		if overlaps[i] == nil {
			pg.Records = append(pg.Records, rec)
			continue
		}
		pg.Overlapping = append(pg.Overlapping, rec)
		o := im.rep.Overlaps[typ]
		if o == nil {
			o = &Overlap{From: overlaps[i].start, To: overlaps[i].end}
			im.rep.Overlaps[typ] = o
		}
		o.Records++
		if overlaps[i].start.Before(o.From) {
			o.From = overlaps[i].start
		}
		if overlaps[i].end.After(o.To) {
			o.To = overlaps[i].end
		}
	}
	body, err := json.Marshal(pg)
	if err != nil {
		return err
	}
	im.rep.Pages++
	key := typ + ":export:" + hex.EncodeToString(ids.Sum(nil)[:16])
	sum := sha256.Sum256(body)

	var rawID int64
	latest, err := im.d.Q().LatestRawVersion(ctx, dbq.LatestRawVersionParams{ConnectionID: im.rep.ConnectionID,
		Stream: applehealth.StreamExport, ExternalKey: key})
	switch err = db.MapErr(err); {
	case err == nil && bytes.Equal(latest.ContentSha256, sum[:]):
		status, err := im.d.Q().GetRawStatus(ctx, latest.ID)
		if err != nil || ingest.Status(status) != ingest.StatusStored {
			return err
		}
		rawID = latest.ID
	case err == nil || errors.Is(err, db.ErrNotFound):
		batch := im.batch
		err := im.d.Tx(ctx, func(q *dbq.Queries) error {
			batch = im.batch
			if batch == nil {
				ref, err := ingest.CreateBatch(ctx, q, ingest.BatchInfo{UserID: im.owner, ConnectionID: im.rep.ConnectionID,
					SourceKind: ingest.SourceImport})
				if err != nil {
					return err
				}
				batch = &ref
			}
			res, err := ingest.StoreRaw(ctx, q, im.opt.Processor.Blobs, *batch, []ingest.RawItem{{Stream: applehealth.StreamExport,
				ExternalKey: key, ContentType: "application/json", FetchedAt: time.Now().UTC(), Body: body}})
			if err != nil {
				return err
			}
			rawID = res[0].RawPayloadID
			return nil
		})
		if err != nil {
			return err
		}
		im.batch = batch
		im.rep.NewPages++
	default:
		return err
	}

	res, err := im.opt.Processor.Process(ctx, rawID, im.vers)
	if err != nil {
		return err
	}
	im.rep.Normalized.Add(res)
	return nil
}

type span struct{ start, end time.Time }

// overlaps returns, per record, its time span when it matches app-synced data, else nil. Each
// record is mapped alone, and every row it maps to is a probe; one matching probe suffices.
func (im *appleImport) overlaps(ctx context.Context, typ string, recs []applehealth.ExportRecord) ([]*span, error) {
	arg := dbq.AppleExportOverlapsParams{UserID: im.owner}
	spans := make([]*span, len(recs))
	for i, rec := range recs {
		origin := rec.Attrs["sourceName"]
		add := func(kind, code string, start time.Time, end *time.Time, val float64) {
			if end == nil {
				end = &start
			}
			arg.Ords = append(arg.Ords, int32(i)) //nolint:gosec // i < PageSize
			arg.Kinds, arg.Codes, arg.Vals, arg.Origins = append(arg.Kinds, kind), append(arg.Codes, code), append(arg.Vals, val), append(arg.Origins, origin)
			arg.Starts, arg.Ends = append(arg.Starts, start), append(arg.Ends, *end)
		}
		out := applehealth.ExportOutput(typ, []applehealth.ExportRecord{rec})
		for _, m := range out.Measurements {
			add("measurement", m.Metric, m.Start, m.End, m.Value)
		}
		for _, g := range out.Groups {
			add("measurement", g.Components[0].Metric, g.MeasuredAt, nil, g.Components[0].Value)
		}
		for _, s := range out.Sleep {
			for _, st := range s.Stages {
				add("sleep", st.Stage, st.Start, &st.End, math.NaN())
			}
		}
		for _, w := range out.Workouts {
			add("workout", w.Sport, w.Start, &w.End, math.NaN())
		}
		for _, e := range out.Events {
			add("event", e.Code, e.Start, e.End, math.NaN())
		}
	}
	if len(arg.Ords) == 0 {
		return spans, nil
	}
	ords, err := im.d.Q().AppleExportOverlaps(ctx, arg)
	if err != nil {
		return nil, err
	}
	for _, o := range ords {
		j := slices.Index(arg.Ords, o)
		if spans[o] == nil {
			spans[o] = &span{arg.Starts[j], arg.Ends[j]}
		}
	}
	return spans, nil
}

func (im *appleImport) finish(ctx context.Context) error {
	stats, err := json.Marshal(im.rep)
	if err != nil {
		return err
	}
	runID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return im.d.Tx(ctx, func(q *dbq.Queries) error {
		if err := q.InsertImportRun(ctx, dbq.InsertImportRunParams{ID: runID, UserID: im.owner,
			ConnectionID: &im.rep.ConnectionID, Source: SourceAppleHealthExport, Stats: stats}); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &im.owner, Actor: audit.System, Action: "import." + SourceAppleHealthExport,
			TargetType: "import_run", TargetID: runID.String(), Detail: map[string]any{"records": im.rep.Records,
				"pages": im.rep.Pages, "new_pages": im.rep.NewPages, "overlapping_types": len(im.rep.Overlaps)}})
	})
}

// String renders the report for the CLI.
func (r AppleHealthReport) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%d records in %d pages (%d new); normalized: %s\n", r.Records, r.Pages, r.NewPages, r.Normalized)
	types := make([]string, 0, len(r.Overlaps))
	for typ := range r.Overlaps {
		types = append(types, typ)
	}
	slices.Sort(types)
	for _, typ := range types {
		o := r.Overlaps[typ]
		fmt.Fprintf(&b, "overlap %-50s %8d records  %s – %s (already synced by the app; not added)\n", typ, o.Records,
			o.From.UTC().Format(time.DateOnly), o.To.UTC().Format(time.DateOnly))
	}
	return b.String()
}

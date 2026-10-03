package normalize

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Source is the provenance of one Write.
type Source struct {
	ConnectionID        uuid.UUID
	RawPayloadID        int64 // the payload normalized; also deleted_by_raw_id for its tombstones
	NormalizerVersionID int32 // from RegisterVersions
}

// WriteStats counts what one Write did, over all record types.
type WriteStats struct {
	Inserted    int // new rows, replacements included
	Superseded  int // rows replaced by a changed version
	Unchanged   int // equal rows, left as they were apart from Reversioned
	Reversioned int // equal rows that only took the new normalizer version
	Deleted     int // rows tombstoned
	Warnings    []Warning
}

// Write applies one payload's output inside the caller's transaction (docs/adr/0016-canonical-writer.md):
//
//  1. no active row with the dedupe key: insert;
//  2. equal row: unchanged, apart from normalizer_version_id/normalized_at when the version changed;
//  3. different row (or a deleted one that reappears): insert the new row and supersede the old;
//  4. tombstone: set deleted_at and deleted_by_raw_id on the active row, nothing else.
//
// Every change marks resolution_dirty in the same transaction. Replaying the same output is a
// no-op, so the caller can retry or reprocess freely.
func Write(ctx context.Context, q *dbq.Queries, src Source, out Output) (WriteStats, error) {
	if err := out.Validate(); err != nil {
		return WriteStats{}, err
	}
	w, err := newWriter(ctx, q, src)
	if err != nil {
		return WriteStats{}, err
	}
	if err := w.sources(out); err != nil {
		return w.stats, err
	}
	var rows []mrow
	for _, m := range out.Measurements {
		r, err := w.measurement(m, Key{}, nil)
		if err != nil {
			return w.stats, err
		}
		rows = append(rows, r)
	}
	for _, g := range out.Groups {
		comps, err := w.group(g)
		if err != nil {
			return w.stats, err
		}
		rows = append(rows, comps...)
	}
	if err := w.measurements(rows); err != nil {
		return w.stats, err
	}
	for _, s := range out.Sleep {
		if err := w.sleep(s); err != nil {
			return w.stats, err
		}
	}
	for _, x := range out.Workouts {
		if err := w.workout(x); err != nil {
			return w.stats, err
		}
	}
	if err := w.tombstones(out.Tombstones); err != nil {
		return w.stats, err
	}
	return w.stats, w.flushDirty()
}

type originRef struct {
	id      uuid.UUID
	relayed bool
}

type dirtyKey struct {
	metric int16
	date   time.Time
}

type writer struct {
	ctx      context.Context
	q        *dbq.Queries
	src      Source
	user     uuid.UUID
	provider int16
	keys     keySource
	tl       Timeline
	metrics  map[string]int16
	units    map[string]int16
	sleepIDs []int16 // sleep-derived metrics, marked dirty for a session's sleep date
	devices  map[string]uuid.UUID
	origins  map[string]originRef
	dirty    map[dirtyKey]struct{}
	stats    WriteStats
}

func newWriter(ctx context.Context, q *dbq.Queries, src Source) (*writer, error) {
	c, err := q.GetWriteConnection(ctx, src.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("normalize: connection: %w", db.MapErr(err))
	}
	w := &writer{ctx: ctx, q: q, src: src, user: c.UserID, provider: c.ProviderID,
		keys:    newKeySource(c.Provider, c.AccountKey, src.ConnectionID),
		metrics: map[string]int16{}, units: map[string]int16{},
		devices: map[string]uuid.UUID{}, origins: map[string]originRef{}, dirty: map[dirtyKey]struct{}{}}
	if w.tl, err = loadTimeline(ctx, q, c.UserID); err != nil {
		return nil, err
	}
	ms, err := q.ListMetricCodes(ctx)
	if err != nil {
		return nil, db.MapErr(err)
	}
	for _, m := range ms {
		w.metrics[m.Code] = m.ID
	}
	us, err := q.ListUnitCodes(ctx)
	if err != nil {
		return nil, db.MapErr(err)
	}
	for _, u := range us {
		w.units[u.Code] = u.ID
	}
	for _, m := range catalog.Metrics() {
		if m.Agg == catalog.SleepDerived {
			id, ok := w.metrics[m.Code]
			if !ok {
				return nil, fmt.Errorf("normalize: metric %q is not seeded", m.Code)
			}
			w.sleepIDs = append(w.sleepIDs, id)
		}
	}
	return w, nil
}

// sources upserts the payload's devices and origins. A new origin matching known_relay_origins
// is flagged as relaying another vendor; measurements from it get FlagRelayed.
func (w *writer) sources(out Output) error {
	for _, d := range out.Devices {
		id, err := newID()
		if err != nil {
			return err
		}
		if w.devices[d.Fingerprint], err = w.q.UpsertDevice(w.ctx, dbq.UpsertDeviceParams{
			ID: id, UserID: w.user, ProviderID: w.provider, Fingerprint: d.Fingerprint,
			DeviceType: strp(d.Type), Manufacturer: strp(d.Manufacturer), Model: strp(d.Model),
			HardwareVersion: strp(d.HardwareVersion), SoftwareVersion: strp(d.SoftwareVersion)}); err != nil {
			return db.MapErr(err)
		}
	}
	for _, o := range out.Origins {
		id, err := newID()
		if err != nil {
			return err
		}
		r, err := w.q.UpsertOrigin(w.ctx, dbq.UpsertOriginParams{
			ID: id, UserID: w.user, ProviderID: w.provider, OriginKey: o.Key, Name: strp(o.Name), IsNative: o.Native})
		if err != nil {
			return db.MapErr(err)
		}
		w.origins[o.Key] = originRef{r.ID, r.RelayedProviderID != nil && *r.RelayedProviderID != w.provider}
	}
	return nil
}

func (w *writer) device(fp string) *uuid.UUID {
	if id, ok := w.devices[fp]; ok {
		return &id
	}
	return nil
}

func (w *writer) origin(key string) (*uuid.UUID, bool) {
	if o, ok := w.origins[key]; ok {
		return &o.id, o.relayed
	}
	return nil, false
}

func (w *writer) mark(metric int16, date time.Time) {
	w.dirty[dirtyKey{metric, date.UTC()}] = struct{}{}
}

func (w *writer) markSleep(date time.Time) {
	for _, id := range w.sleepIDs {
		w.mark(id, date)
	}
}

// flushDirty writes the marks in one statement, sorted so concurrent writers lock in one order.
func (w *writer) flushDirty() error {
	if len(w.dirty) == 0 {
		return nil
	}
	keys := make([]dirtyKey, 0, len(w.dirty))
	for k := range w.dirty {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].metric != keys[j].metric {
			return keys[i].metric < keys[j].metric
		}
		return keys[i].date.Before(keys[j].date)
	})
	p := dbq.MarkDirtyParams{UserID: w.user}
	for _, k := range keys {
		p.MetricIds = append(p.MetricIds, k.metric)
		p.Dates = append(p.Dates, k.date)
	}
	return db.MapErr(w.q.MarkDirty(w.ctx, p))
}

// mrow is a measurement ready to insert; the JSON fields are the columns InsertMeasurements reads.
type mrow struct {
	MetricID     int16      `json:"metric_id"`
	Kind         string     `json:"kind"`
	StartAt      time.Time  `json:"start_at"`
	EndAt        *time.Time `json:"end_at"`
	TZOffsetMin  *int16     `json:"tz_offset_min"`
	LocalDate    string     `json:"local_date"`
	Value        float64    `json:"value"`
	SourceValue  *float64   `json:"source_value"`
	SourceUnitID *int16     `json:"source_unit_id"`
	DeviceID     *uuid.UUID `json:"device_id"`
	OriginID     *uuid.UUID `json:"origin_id"`
	GroupID      *int64     `json:"group_id"`
	ExternalID   *string    `json:"external_id"`
	DedupeKey    string     `json:"dedupe_key"` // hex
	QualityFlags int32      `json:"quality_flags"`
	date         time.Time
}

// measurement prepares one row. A group component without its own Key takes the group's, with
// the metric as component.
func (w *writer) measurement(m Measurement, group Key, groupID *int64) (mrow, error) {
	met, _ := catalog.Lookup(m.Metric) // Validate checked it
	metricID, ok := w.metrics[m.Metric]
	if !ok {
		return mrow{}, fmt.Errorf("normalize: metric %q is not seeded", m.Metric)
	}
	v, converted, err := catalog.ToCanonical(m.Metric, m.Value, m.Unit)
	if err != nil {
		return mrow{}, err
	}
	start, end := micro(m.Start), microp(m.End)
	loc, err := LocalDate(start, m.Zone, w.tl)
	if err != nil {
		return mrow{}, err
	}
	flags := m.Flags &^ (FlagImplausible | FlagRelayed) // the writer owns these two
	if v < met.Min || v > met.Max {
		flags |= FlagImplausible
	}
	origin, relayed := w.origin(m.Origin)
	if relayed {
		flags |= FlagRelayed
	}
	key := m.Key
	if key.ExternalID == "" && group.ExternalID != "" {
		key = Key{RecordType: group.RecordType, ExternalID: group.ExternalID, Component: m.Metric}
	}
	r := mrow{MetricID: metricID, Kind: string(m.Kind), StartAt: start, EndAt: end, TZOffsetMin: loc.OffsetMin,
		LocalDate: loc.Date.Format(time.DateOnly), date: loc.Date, Value: v, DeviceID: w.device(m.Device),
		OriginID: origin, GroupID: groupID, ExternalID: strp(key.ExternalID), QualityFlags: int32(flags),
		DedupeKey: hex.EncodeToString(w.keys.key(key, m.Metric, string(m.Kind), start, end, m.Device, m.Origin))}
	if converted {
		unitID, ok := w.units[m.Unit]
		if !ok {
			return mrow{}, fmt.Errorf("normalize: unit %q is not seeded", m.Unit)
		}
		r.SourceValue, r.SourceUnitID = &m.Value, &unitID
	}
	return r, nil
}

func sameMeasurement(o dbq.ListActiveMeasurementsRow, n mrow) bool {
	return o.DeletedAt == nil && o.MetricID == n.MetricID && o.Kind == n.Kind && o.StartAt.Equal(n.StartAt) &&
		eqTime(o.EndAt, n.EndAt) && eq(o.TzOffsetMin, n.TZOffsetMin) && o.LocalDate.Equal(n.date) &&
		o.Value == n.Value && eq(o.SourceValue, n.SourceValue) && eq(o.SourceUnitID, n.SourceUnitID) &&
		eq(o.DeviceID, n.DeviceID) && eq(o.OriginID, n.OriginID) && eq(o.GroupID, n.GroupID) &&
		eq(o.ExternalID, n.ExternalID) && o.QualityFlags == n.QualityFlags
}

// measurements upserts all rows of the payload in a few set-based statements, since intraday
// series bring thousands of rows per payload.
func (w *writer) measurements(rows []mrow) error {
	// A key repeated inside one payload keeps its first row; a conflicting repeat is reported.
	seen := map[string]int{}
	uniq := rows[:0:0]
	for _, r := range rows {
		if i, ok := seen[r.DedupeKey]; ok {
			if !reflect.DeepEqual(uniq[i], r) {
				w.stats.Warnings = append(w.stats.Warnings, Warning{Code: "duplicate_key",
					Detail: fmt.Sprintf("conflicting records share a dedupe key (metric id %d); kept the first", r.MetricID)})
			}
			continue
		}
		seen[r.DedupeKey] = len(uniq)
		uniq = append(uniq, r)
	}
	if len(uniq) == 0 {
		return nil
	}
	keys := make([][]byte, len(uniq))
	for i, r := range uniq {
		keys[i], _ = hex.DecodeString(r.DedupeKey)
	}
	old, err := w.q.ListActiveMeasurements(w.ctx, keys)
	if err != nil {
		return db.MapErr(err)
	}
	byKey := make(map[string]dbq.ListActiveMeasurementsRow, len(old))
	for _, o := range old {
		byKey[hex.EncodeToString(o.DedupeKey)] = o
	}

	var touch, superseded []int64
	var ins []mrow
	replaces := map[string]int64{} // dedupe key -> old id
	for _, r := range uniq {
		o, ok := byKey[r.DedupeKey]
		switch {
		case !ok:
			ins = append(ins, r)
		case sameMeasurement(o, r):
			w.stats.Unchanged++
			if o.NormalizerVersionID != w.src.NormalizerVersionID {
				touch = append(touch, o.ID)
			}
		default:
			superseded = append(superseded, o.ID)
			replaces[r.DedupeKey] = o.ID
			ins = append(ins, r)
			w.mark(o.MetricID, o.LocalDate)
		}
	}
	if len(touch) > 0 {
		if err := w.q.TouchMeasurements(w.ctx, dbq.TouchMeasurementsParams{
			NormalizerVersionID: w.src.NormalizerVersionID, Ids: touch}); err != nil {
			return db.MapErr(err)
		}
		w.stats.Reversioned += len(touch)
	}
	if len(superseded) > 0 {
		if err := w.q.SupersedeMeasurements(w.ctx, superseded); err != nil {
			return db.MapErr(err)
		}
		w.stats.Superseded += len(superseded)
	}
	if len(ins) == 0 {
		return nil
	}
	body, err := json.Marshal(ins)
	if err != nil {
		return err
	}
	created, err := w.q.InsertMeasurements(w.ctx, dbq.InsertMeasurementsParams{UserID: w.user, ProviderID: w.provider,
		ConnectionID: w.src.ConnectionID, RawPayloadID: w.src.RawPayloadID,
		NormalizerVersionID: w.src.NormalizerVersionID, Rows: body})
	if err != nil {
		return db.MapErr(err)
	}
	w.stats.Inserted += len(created)
	for _, r := range ins {
		w.mark(r.MetricID, r.date)
	}
	if len(replaces) == 0 {
		return nil
	}
	var oldIDs, newIDs []int64
	for _, c := range created {
		if id, ok := replaces[hex.EncodeToString(c.DedupeKey)]; ok {
			oldIDs, newIDs = append(oldIDs, id), append(newIDs, c.ID)
		}
	}
	return db.MapErr(w.q.LinkMeasurementSuccessors(w.ctx, dbq.LinkMeasurementSuccessorsParams{OldIds: oldIDs, NewIds: newIDs}))
}

// event applies the upsert rule to one event row (group, sleep session, workout). insert must
// create the new row; it runs after the old row is superseded and before link points at it.
func (w *writer) event(found, same bool, oldVersion int32, touch, supersede, insert, link func() error) (inserted bool, err error) {
	if found && same {
		w.stats.Unchanged++
		if oldVersion == w.src.NormalizerVersionID {
			return false, nil
		}
		w.stats.Reversioned++
		return false, db.MapErr(touch())
	}
	if found {
		if err := supersede(); err != nil {
			return false, db.MapErr(err)
		}
	}
	if err := insert(); err != nil {
		return false, db.MapErr(err)
	}
	w.stats.Inserted++
	if found {
		w.stats.Superseded++
		return true, db.MapErr(link())
	}
	return true, nil
}

// active turns a lookup error into found/not found.
func active(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// group upserts the group row and returns its components, pointing at the active group row.
func (w *writer) group(g Group) ([]mrow, error) {
	at := micro(g.MeasuredAt)
	loc, err := LocalDate(at, g.Zone, w.tl)
	if err != nil {
		return nil, err
	}
	ctxJSON := json.RawMessage(g.Context)
	if len(ctxJSON) == 0 {
		ctxJSON = json.RawMessage(`{}`)
	}
	dev := w.device(g.Device)
	org, _ := w.origin(g.Origin)
	ext := strp(g.Key.ExternalID)
	dk := w.keys.key(g.Key, g.Kind, "group", at, nil, g.Device, g.Origin)

	old, err := w.q.GetActiveGroup(w.ctx, dk)
	found, err := active(err)
	if err != nil {
		return nil, err
	}
	same := found && old.DeletedAt == nil && old.Kind == g.Kind && old.MeasuredAt.Equal(at) &&
		eq(old.TzOffsetMin, loc.OffsetMin) && old.LocalDate.Equal(loc.Date) && jsonEqual(old.Context, ctxJSON) &&
		eq(old.DeviceID, dev) && eq(old.OriginID, org) && eq(old.ExternalID, ext)
	id := old.ID
	raw := w.src.RawPayloadID
	if _, err := w.event(found, same, old.NormalizerVersionID,
		func() error {
			return w.q.TouchGroup(w.ctx, dbq.TouchGroupParams{NormalizerVersionID: w.src.NormalizerVersionID, ID: old.ID})
		},
		func() error { return w.q.SupersedeGroup(w.ctx, old.ID) },
		func() (err error) {
			id, err = w.q.InsertGroup(w.ctx, dbq.InsertGroupParams{UserID: w.user, Kind: g.Kind, MeasuredAt: at,
				TzOffsetMin: loc.OffsetMin, LocalDate: loc.Date, Context: ctxJSON, ProviderID: w.provider,
				ConnectionID: w.src.ConnectionID, DeviceID: dev, OriginID: org, ExternalID: ext, DedupeKey: dk,
				RawPayloadID: &raw, NormalizerVersionID: w.src.NormalizerVersionID})
			return err
		},
		func() error {
			return w.q.LinkGroupSuccessor(w.ctx, dbq.LinkGroupSuccessorParams{ID: old.ID, NewID: &id})
		},
	); err != nil {
		return nil, err
	}

	out := make([]mrow, 0, len(g.Components))
	for _, c := range g.Components {
		if c.Device == "" {
			c.Device = g.Device
		}
		if c.Origin == "" {
			c.Origin = g.Origin
		}
		if c.Zone.OffsetMin == nil && c.Zone.TZ == "" {
			c.Zone = g.Zone
		}
		r, err := w.measurement(c, g.Key, &id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// sleepTotals returns the stored totals: provider-reported, else summed from stages. Stage kinds
// a session never reports stay nil, so missing stages never read as zero.
func sleepTotals(s SleepSession) (basis string, t SleepTotals) {
	if s.Totals != nil {
		return "provider", *s.Totals
	}
	if len(s.Stages) == 0 {
		return "stages", t
	}
	sum := map[string]int32{}
	for _, st := range s.Stages {
		sum[st.Stage] += int32(st.End.Sub(st.Start) / time.Second) //nolint:gosec // a stage is shorter than 68 years
	}
	has := func(k string) bool { _, ok := sum[k]; return ok }
	val := func(k string) *int32 { v := sum[k]; return &v }
	t.Awake = val("awake")
	if has("deep") || has("light") || has("rem") {
		t.Deep, t.Light, t.REM = val("deep"), val("light"), val("rem")
	}
	asleep := sum["deep"] + sum["light"] + sum["rem"] + sum["asleep_unspecified"]
	t.Asleep = &asleep
	return "stages", t
}

func (w *writer) sleep(s SleepSession) error {
	start, end := micro(s.Start), micro(s.End)
	loc, err := LocalDate(end, s.Zone, w.tl) // sleep_date is the local date of waking up
	if err != nil {
		return err
	}
	stages := slices.Clone(s.Stages)
	for i := range stages {
		stages[i].Start, stages[i].End = micro(stages[i].Start), micro(stages[i].End)
	}
	sort.SliceStable(stages, func(i, j int) bool { return stages[i].Start.Before(stages[j].Start) })
	basis, t := sleepTotals(s)
	dev := w.device(s.Device)
	org, _ := w.origin(s.Origin)
	ext := strp(s.Key.ExternalID)
	dk := w.keys.key(s.Key, "sleep", "session", start, &end, s.Device, s.Origin)

	old, err := w.q.GetActiveSleep(w.ctx, dk)
	found, err := active(err)
	if err != nil {
		return err
	}
	same := found && old.DeletedAt == nil && old.StartAt.Equal(start) && old.EndAt.Equal(end) &&
		eq(old.TzOffsetMin, loc.OffsetMin) && old.SleepDate.Equal(loc.Date) && old.IsNap == s.Nap &&
		old.HasStages == (len(stages) > 0) && old.TotalsBasis == basis && eq(old.AsleepS, t.Asleep) &&
		eq(old.DeepS, t.Deep) && eq(old.LightS, t.Light) && eq(old.RemS, t.REM) && eq(old.AwakeS, t.Awake) &&
		eq(old.LatencyS, t.Latency) && eq(old.DeviceID, dev) && eq(old.OriginID, org) && eq(old.ExternalID, ext)
	if same {
		have, err := w.q.ListSleepStages(w.ctx, old.ID)
		if err != nil {
			return db.MapErr(err)
		}
		same = len(have) == len(stages)
		for i := 0; same && i < len(have); i++ {
			same = have[i].Stage == stages[i].Stage && have[i].StartAt.Equal(stages[i].Start) && have[i].EndAt.Equal(stages[i].End)
		}
	}
	id, err := newID()
	if err != nil {
		return err
	}
	raw := w.src.RawPayloadID
	inserted, err := w.event(found, same, old.NormalizerVersionID,
		func() error {
			return w.q.TouchSleep(w.ctx, dbq.TouchSleepParams{NormalizerVersionID: w.src.NormalizerVersionID, ID: old.ID})
		},
		func() error { return w.q.SupersedeSleep(w.ctx, old.ID) },
		func() error {
			if err := w.q.InsertSleep(w.ctx, dbq.InsertSleepParams{ID: id, UserID: w.user, StartAt: start, EndAt: end,
				TzOffsetMin: loc.OffsetMin, SleepDate: loc.Date, IsNap: s.Nap, HasStages: len(stages) > 0,
				TotalsBasis: basis, AsleepS: t.Asleep, DeepS: t.Deep, LightS: t.Light, RemS: t.REM, AwakeS: t.Awake,
				LatencyS: t.Latency, ProviderID: w.provider, ConnectionID: w.src.ConnectionID, DeviceID: dev,
				OriginID: org, ExternalID: ext, DedupeKey: dk, RawPayloadID: &raw,
				NormalizerVersionID: w.src.NormalizerVersionID}); err != nil || len(stages) == 0 {
				return err
			}
			p := dbq.InsertSleepStagesParams{SessionID: id}
			for _, st := range stages {
				p.Stages, p.Starts, p.Ends = append(p.Stages, st.Stage), append(p.Starts, st.Start), append(p.Ends, st.End)
			}
			return w.q.InsertSleepStages(w.ctx, p)
		},
		func() error {
			return w.q.LinkSleepSuccessor(w.ctx, dbq.LinkSleepSuccessorParams{ID: old.ID, NewID: &id})
		},
	)
	if inserted {
		w.markSleep(loc.Date)
		if found {
			w.markSleep(old.SleepDate)
		}
	}
	return err
}

// segRow is one workout segment as InsertWorkoutSegments reads it.
type segRow struct {
	Seq   int32           `json:"seq"`
	Kind  string          `json:"kind"`
	Start time.Time       `json:"start_at"`
	End   *time.Time      `json:"end_at"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// workout upserts one workout with its segments. Workouts have no catalogue metric, so they mark
// nothing dirty.
func (w *writer) workout(x Workout) error {
	start, end := micro(x.Start), micro(x.End)
	loc, err := LocalDate(start, x.Zone, w.tl)
	if err != nil {
		return err
	}
	segs := make([]segRow, len(x.Segments))
	for i, s := range x.Segments {
		segs[i] = segRow{Seq: int32(i), Kind: s.Kind, Start: micro(s.Start), End: microp(s.End), Data: s.Data} //nolint:gosec // segment counts are small
	}
	dev := w.device(x.Device)
	org, _ := w.origin(x.Origin)
	ext, psport := strp(x.Key.ExternalID), strp(x.ProviderSport)
	dk := w.keys.key(x.Key, "workout", "workout", start, &end, x.Device, x.Origin)

	old, err := w.q.GetActiveWorkout(w.ctx, dk)
	found, err := active(err)
	if err != nil {
		return err
	}
	same := found && old.DeletedAt == nil && old.StartAt.Equal(start) && old.EndAt.Equal(end) &&
		eq(old.TzOffsetMin, loc.OffsetMin) && old.LocalDate.Equal(loc.Date) && old.Sport == x.Sport &&
		eq(old.ProviderSport, psport) && eq(old.DistanceM, x.DistanceM) && eq(old.EnergyKcal, x.EnergyKcal) &&
		eq(old.AvgHrBpm, x.AvgHRBpm) && eq(old.MaxHrBpm, x.MaxHRBpm) && slices.Equal(old.FileBlobSha256, x.FileSHA256) &&
		eq(old.DeviceID, dev) && eq(old.OriginID, org) && eq(old.ExternalID, ext)
	if same {
		have, err := w.q.ListWorkoutSegments(w.ctx, old.ID)
		if err != nil {
			return db.MapErr(err)
		}
		same = len(have) == len(segs)
		for i := 0; same && i < len(have); i++ {
			h, s := have[i], segs[i]
			same = h.Seq == s.Seq && h.Kind == s.Kind && h.StartAt.Equal(s.Start) && eqTime(h.EndAt, s.End) && jsonEqual(h.Data, s.Data)
		}
	}
	id, err := newID()
	if err != nil {
		return err
	}
	raw := w.src.RawPayloadID
	var file []byte
	if len(x.FileSHA256) > 0 {
		file = x.FileSHA256
	}
	_, err = w.event(found, same, old.NormalizerVersionID,
		func() error {
			return w.q.TouchWorkout(w.ctx, dbq.TouchWorkoutParams{NormalizerVersionID: w.src.NormalizerVersionID, ID: old.ID})
		},
		func() error { return w.q.SupersedeWorkout(w.ctx, old.ID) },
		func() error {
			if err := w.q.InsertWorkout(w.ctx, dbq.InsertWorkoutParams{ID: id, UserID: w.user, StartAt: start, EndAt: end,
				TzOffsetMin: loc.OffsetMin, LocalDate: loc.Date, Sport: x.Sport, ProviderSport: psport,
				DistanceM: x.DistanceM, EnergyKcal: x.EnergyKcal, AvgHrBpm: x.AvgHRBpm, MaxHrBpm: x.MaxHRBpm,
				FileBlobSha256: file, ProviderID: w.provider, ConnectionID: w.src.ConnectionID, DeviceID: dev,
				OriginID: org, ExternalID: ext, DedupeKey: dk, RawPayloadID: &raw,
				NormalizerVersionID: w.src.NormalizerVersionID}); err != nil || len(segs) == 0 {
				return err
			}
			body, err := json.Marshal(segs)
			if err != nil {
				return err
			}
			return w.q.InsertWorkoutSegments(w.ctx, dbq.InsertWorkoutSegmentsParams{WorkoutID: id, Rows: body})
		},
		func() error {
			return w.q.LinkWorkoutSuccessor(w.ctx, dbq.LinkWorkoutSuccessorParams{ID: old.ID, NewID: &id})
		},
	)
	return err
}

// tombstones marks the active rows with these upstream ids deleted, in every canonical table;
// deleting a group deletes its components. Unknown ids are ignored.
func (w *writer) tombstones(ks []Key) error {
	if len(ks) == 0 {
		return nil
	}
	keys := make([][]byte, len(ks))
	for i, k := range ks {
		keys[i] = w.keys.idKey(k)
	}
	raw := &w.src.RawPayloadID
	ms, err := w.q.DeleteMeasurementsByKey(w.ctx, dbq.DeleteMeasurementsByKeyParams{RawPayloadID: raw, Keys: keys})
	if err != nil {
		return db.MapErr(err)
	}
	for _, m := range ms {
		w.mark(m.MetricID, m.LocalDate)
	}
	gids, err := w.q.DeleteGroupsByKey(w.ctx, dbq.DeleteGroupsByKeyParams{RawPayloadID: raw, Keys: keys})
	if err != nil {
		return db.MapErr(err)
	}
	comps, err := w.q.DeleteGroupComponents(w.ctx, dbq.DeleteGroupComponentsParams{RawPayloadID: raw, GroupIds: gids})
	if err != nil {
		return db.MapErr(err)
	}
	for _, m := range comps {
		w.mark(m.MetricID, m.LocalDate)
	}
	dates, err := w.q.DeleteSleepByKey(w.ctx, dbq.DeleteSleepByKeyParams{RawPayloadID: raw, Keys: keys})
	if err != nil {
		return db.MapErr(err)
	}
	for _, d := range dates {
		w.markSleep(d)
	}
	nw, err := w.q.DeleteWorkoutsByKey(w.ctx, dbq.DeleteWorkoutsByKeyParams{RawPayloadID: raw, Keys: keys})
	if err != nil {
		return db.MapErr(err)
	}
	w.stats.Deleted += len(ms) + len(gids) + len(comps) + len(dates) + int(nw)
	return nil
}

func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("normalize: new id: %w", err)
	}
	return id, nil
}

// micro matches PostgreSQL's timestamp precision, so a re-read row compares equal.
func micro(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func microp(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	m := micro(*t)
	return &m
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func eq[T comparable](a, b *T) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func eqTime(a, b *time.Time) bool { return (a == nil) == (b == nil) && (a == nil || a.Equal(*b)) }

// jsonEqual compares JSON by value, as jsonb does; empty counts as {}.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if len(a) == 0 {
		a = []byte(`{}`)
	}
	if len(b) == 0 {
		b = []byte(`{}`)
	}
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

package api

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/resolve"
)

// Inventory, health events and per-source series (J21.5, J21.6): what Explore lists and the
// all-sources overlay of long ranges. Metric counts come from the hourly aggregates plus daily
// values (queries/inventory.sql), so no request scans a whole history of measurements.
func (rt *router) exploreRoutes() {
	read := scope(auth.ReadHealth)
	rt.handle("GET /api/v1/inventory", read, rt.ops.GetInventory)
	rt.handle("GET /api/v1/event-types", read, rt.ops.ListEventTypes)
	rt.handle("GET /api/v1/events", read, rt.ops.ListEvents)
	rt.handle("GET /api/v1/sources/series", read, rt.ops.GetSourceSeries)
}

// ---- inventory

// inventory collects items in the order they are first seen.
type inventory struct {
	items []*oapi.InventoryItem
	byKey map[string]*oapi.InventoryItem
}

func (inv *inventory) item(kind oapi.InventoryItemKind, code string) *oapi.InventoryItem {
	k := string(kind) + "/" + code
	if it, ok := inv.byKey[k]; ok {
		return it
	}
	it := &oapi.InventoryItem{Kind: kind, Code: code, Providers: []string{}, Devices: []oapi.DeviceRef{}, Origins: []oapi.OriginRef{}}
	inv.byKey[k] = it
	inv.items = append(inv.items, it)
	return it
}

// addSource adds a source's provider, device and origin to the item once each.
func addSource(it *oapi.InventoryItem, provider string, device *uuid.UUID, deviceType, deviceModel, originKey, originName string) {
	if !slices.Contains(it.Providers, provider) {
		it.Providers = append(it.Providers, provider)
	}
	src := resolve.Source{DeviceType: deviceType, DeviceModel: deviceModel, OriginKey: originKey, OriginName: originName}
	if device != nil {
		src.DeviceID = *device
	}
	if d := deviceRef(src); d != nil && !slices.ContainsFunc(it.Devices, func(x oapi.DeviceRef) bool {
		return ptrVal(x.ID) == ptrVal(d.ID) && ptrVal(x.Type) == ptrVal(d.Type) && ptrVal(x.Model) == ptrVal(d.Model)
	}) {
		it.Devices = append(it.Devices, *d)
	}
	if originKey != "" && !slices.ContainsFunc(it.Origins, func(x oapi.OriginRef) bool { return ptrVal(x.Key) == originKey }) {
		it.Origins = append(it.Origins, *originRef(src, ""))
	}
}

// GetInventory lists every metric, group kind, event code, sleep, workouts and lab analyte with
// active data. Metric bounds and latest values come from the rows (index probes); their counts,
// days and sources from the hourly aggregates and daily values.
func (o *owner) GetInventory(ctx context.Context, _ oapi.GetInventoryRequestObject) (oapi.GetInventoryResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	user := auth.PrincipalFrom(ctx).UserID
	q := d.Q()
	inv := &inventory{byKey: map[string]*oapi.InventoryItem{}}

	bounds, err := q.InventoryMetricBounds(ctx, user)
	if err != nil {
		return nil, db.MapErr(err)
	}
	for _, b := range bounds {
		m, ok := catalog.Lookup(b.Metric)
		if !ok {
			continue
		}
		it := inv.item(oapi.InventoryItemKindMetric, b.Metric)
		body := metricBody(m)
		it.Metric = &body
		it.FirstAt, it.LastAt, it.FirstDate, it.LastDate = &b.FirstAt, &b.LastAt, apiDate(b.FirstDate), apiDate(b.LastDate)
		v := num(b.LatestValue)
		it.Latest = &oapi.InventoryLatest{At: &b.LastAt, LocalDate: apiDate(b.LastDate), Value: &v, Unit: &m.Unit}
	}
	sources, err := q.InventoryMetricSources(ctx, user)
	if err != nil {
		return nil, db.MapErr(err)
	}
	for _, s := range sources {
		it, ok := inv.byKey[string(oapi.InventoryItemKindMetric)+"/"+s.Metric]
		if !ok { // aggregates of rows deleted since; the rebuild job drops them
			continue
		}
		it.Count += s.Count
		it.Days = int(s.Days)
		addSource(it, s.Provider, s.DeviceID, s.DeviceType, s.DeviceModel, s.OriginKey, s.OriginName)
	}

	records, err := q.InventoryRecords(ctx, user)
	if err != nil {
		return nil, db.MapErr(err)
	}
	var groupIDs []int64
	groupOf := map[int64]*oapi.InventoryItem{}
	for _, r := range records {
		it := inv.item(oapi.InventoryItemKind(r.Kind), r.Code)
		if it.Count == 0 {
			it.FirstAt, it.LastAt, it.FirstDate, it.LastDate = &r.FirstAt, &r.LastAt, apiDate(r.FirstDate), apiDate(r.LastDate)
			it.Days = int(r.Days)
			it.Latest = recordLatest(it, r)
			if id := r.LatestGroupID; id != nil {
				groupIDs = append(groupIDs, *id)
				groupOf[*id] = it
			}
		}
		it.Count += r.Count
		if r.FirstAt.Before(*it.FirstAt) {
			it.FirstAt, it.FirstDate = &r.FirstAt, apiDate(r.FirstDate)
		}
		if r.LastAt.After(*it.LastAt) {
			it.LastAt, it.LastDate = &r.LastAt, apiDate(r.LastDate)
		}
		addSource(it, r.Provider, r.DeviceID, r.DeviceType, r.DeviceModel, r.OriginKey, r.OriginName)
	}
	if len(groupIDs) > 0 {
		comps, err := q.ReadGroupComponents(ctx, groupIDs)
		if err != nil {
			return nil, db.MapErr(err)
		}
		for _, c := range comps {
			l := groupOf[c.GroupID].Latest
			if l.Components == nil {
				l.Components = &map[string]float64{}
			}
			(*l.Components)[c.Metric] = num(c.Value)
		}
	}

	labs, err := q.InventoryAnalytes(ctx, user)
	if err != nil {
		return nil, db.MapErr(err)
	}
	for _, a := range labs {
		it := inv.item(oapi.InventoryItemKindAnalyte, a.Code)
		it.Analyte = &oapi.AnalyteRef{Code: a.Code, Name: a.Name, CanonicalUnit: a.CanonicalUnit}
		it.Count, it.Days = a.Count, int(a.Days)
		it.FirstDate, it.LastDate, it.FirstAt, it.LastAt = apiDate(a.FirstDate), apiDate(a.LastDate), a.FirstAt, a.LastAt
		if a.LatestDate != nil {
			it.Latest = &oapi.InventoryLatest{At: a.LatestAt, LocalDate: apiDate(*a.LatestDate), Value: a.LatestValue, Unit: a.LatestUnit, Text: a.LatestText}
		}
	}

	pending, err := q.AggregatesPending(ctx, dbq.AggregatesPendingParams{UserID: user})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := oapi.GetInventory200JSONResponse{AggregatesPending: pending, Items: make([]oapi.InventoryItem, len(inv.items))}
	for i, it := range inv.items {
		out.Items[i] = *it
	}
	return out, nil
}

// recordLatest is the newest record of a group, event, sleep or workouts item, with its catalogue
// metadata set on the item.
func recordLatest(it *oapi.InventoryItem, r dbq.InventoryRecordsRow) *oapi.InventoryLatest {
	l := &oapi.InventoryLatest{At: &r.LastAt, LocalDate: apiDate(r.LastDate), Level: r.LatestLevel, Text: r.LatestText}
	if r.LatestValue != nil {
		v := num(*r.LatestValue)
		l.Value = &v
	}
	switch it.Kind {
	case oapi.InventoryItemKindGroup:
		var codes []string
		for _, m := range catalog.Metrics() {
			if m.Group == it.Code {
				codes = append(codes, m.Code)
			}
		}
		it.Components = &codes
	case oapi.InventoryItemKindEvent:
		if e, ok := catalog.LookupEvent(it.Code); ok {
			et := eventType(e)
			it.Event = &et
		}
	case oapi.InventoryItemKindSleep:
		l.Unit = optString("s")
	default:
	}
	return l
}

// ---- events

func eventType(e catalog.Event) oapi.EventType {
	return oapi.EventType{Code: e.Code, Levels: append([]string{}, e.Levels...)}
}

func (o *owner) ListEventTypes(context.Context, oapi.ListEventTypesRequestObject) (oapi.ListEventTypesResponseObject, error) {
	es := catalog.Events()
	out := oapi.ListEventTypes200JSONResponse{EventTypes: make([]oapi.EventType, len(es))}
	for i, e := range es {
		out.EventTypes[i] = eventType(e)
	}
	return out, nil
}

func (o *owner) ListEvents(ctx context.Context, req oapi.ListEventsRequestObject) (oapi.ListEventsResponseObject, error) {
	prm := req.Params
	f, err := o.filter(ctx, filterParams{start: prm.Start, end: prm.End, startDate: prm.StartDate, endDate: prm.EndDate,
		providers: prm.Provider, origins: prm.Origin, connections: prm.Connection, devices: prm.Device, include: prm.Include})
	if err != nil {
		return nil, err
	}
	bound := prm
	bound.Limit, bound.Cursor = nil, nil
	p, err := o.newPage("events", bound, prm.Limit, prm.Cursor)
	if err != nil {
		return nil, err
	}
	afterKey, afterID, err := p.afterKeyUUID()
	if err != nil {
		return nil, err
	}
	rows, err := o.opts.DB.Q().ReadEvents(ctx, dbq.ReadEventsParams{UserID: f.user, Codes: ptrVal(prm.Code),
		Providers: f.providers, Connections: f.connections, Devices: f.devices, Origins: f.origins,
		StartAt: f.start, EndAt: f.end, StartDate: f.startDate, EndDate: f.endDate,
		WithSuperseded: f.include["superseded"], WithDeleted: f.include["deleted"], AfterKey: afterKey, AfterID: afterID, Lim: p.lim()})
	if err != nil {
		return nil, db.MapErr(err)
	}
	rows, more, next := trim(o, p, rows, func(r dbq.ReadEventsRow) (time.Time, string) { return r.StartAt, r.ID.String() })
	raws, err := loadRawRefs(ctx, o, f, rows, func(r dbq.ReadEventsRow) *int64 { return r.RawPayloadID })
	if err != nil {
		return nil, err
	}
	out := oapi.ListEvents200JSONResponse{HasMore: more, NextCursor: next, Events: make([]oapi.HealthEvent, len(rows))}
	for i, r := range rows {
		s := srcCols{r.Provider, r.ConnectionID, r.DeviceID, r.DeviceType, r.OriginKey, r.ExternalID, r.DedupeKey,
			r.RawPayloadID, r.NormalizerName, r.NormalizerVersion, r.IngestedAt, r.NormalizedAt,
			r.SupersededAt, r.SupersededBy, r.DeletedAt, r.DeletedByRawID}
		out.Events[i] = oapi.HealthEvent{ID: r.ID, Code: r.Code, StartAt: r.StartAt, EndAt: r.EndAt, TzOffsetMin: intp(r.TzOffsetMin),
			LocalDate: apiDate(r.LocalDate), Value: r.Value, Level: r.Level, Context: r.Context, QualityFlags: int(r.QualityFlags),
			Source: s.source(), Provenance: s.provenance(raws)}
	}
	return out, nil
}

// ---- per-source series

const (
	maxHourSeriesDays = 93
	maxDaySeriesDays  = 3660
)

// GetSourceSeries answers each source's own values of a metric per local hour or day from the
// hourly aggregates, plus per day the source's reported daily values. Nothing is resolved: each
// source only carries where the rule in effect places it.
func (o *owner) GetSourceSeries(ctx context.Context, req oapi.GetSourceSeriesRequestObject) (oapi.GetSourceSeriesResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	prm := req.Params
	m, ok := catalog.Lookup(prm.Metric)
	switch {
	case !ok:
		return nil, problemErr(CodeValidationFailed, "unknown metric", FieldError{Pointer: "/metric", Detail: "not a catalogue metric code"})
	case len(m.Kinds) == 0:
		return nil, problemErr(CodeValidationFailed, "no hourly aggregates", FieldError{Pointer: "/metric", Detail: "sleep and derived codes have none; use /resolved/series"})
	}
	grain := cmp.Or(ptrVal(prm.Grain), oapi.GetSourceSeriesParamsGrainDay)
	maxDays := maxDaySeriesDays
	switch grain {
	case oapi.GetSourceSeriesParamsGrainHour:
		maxDays = maxHourSeriesDays
	case oapi.GetSourceSeriesParamsGrainDay:
	default:
		return nil, problemErr(CodeValidationFailed, "invalid grain", FieldError{Pointer: "/grain", Detail: "must be hour or day"})
	}
	switch {
	case !prm.End.After(prm.Start):
		return nil, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end", Detail: "must be after start"})
	case prm.End.Sub(prm.Start) > time.Duration(maxDays)*24*time.Hour:
		return nil, problemErr(CodeValidationFailed, "invalid range", FieldError{Pointer: "/end", Detail: fmt.Sprintf("at most %d days for %s", maxDays, grain)})
	}
	z, err := o.zones(ctx)
	if err != nil {
		return nil, err
	}
	fromDate, err := z.localDate(prm.Start)
	if err != nil {
		return nil, resolveErr(err)
	}
	toDate, err := z.localDate(prm.End.Add(-time.Nanosecond))
	if err != nil {
		return nil, resolveErr(err)
	}
	user, q, byDay := auth.PrincipalFrom(ctx).UserID, d.Q(), grain == oapi.GetSourceSeriesParamsGrainDay
	rows, err := q.SourceSeriesAggregates(ctx, dbq.SourceSeriesAggregatesParams{UserID: user, Metric: m.Code, FromAt: prm.Start, ToAt: prm.End, ByDay: byDay})
	if err != nil {
		return nil, db.MapErr(err)
	}
	var daily []dbq.SourceDailyValuesRow
	if byDay && slices.Contains(m.Kinds, catalog.DailyValue) {
		if daily, err = q.SourceDailyValues(ctx, dbq.SourceDailyValuesParams{UserID: user, Metric: m.Code, FromDate: fromDate, ToDate: toDate}); err != nil {
			return nil, db.MapErr(err)
		}
	}
	behind, err := q.AggregatesPending(ctx, dbq.AggregatesPendingParams{UserID: user, Metric: &m.Code, FromDate: &fromDate, ToDate: &toDate})
	if err != nil {
		return nil, db.MapErr(err)
	}
	out := oapi.GetSourceSeries200JSONResponse{Metric: m.Code, Unit: m.Unit, Aggregation: oapi.SourceSeriesAggregation(m.Agg),
		Grain: oapi.SourceSeriesGrain(grain), Timezone: z.name(prm.Start), Behind: behind, Sources: []oapi.SourceSeriesSource{}}
	v, hasRule, err := o.ruleFor(ctx, m.Code)
	if err != nil {
		return nil, err
	}
	var rule *resolve.Rule
	if hasRule {
		ref := ruleRef(v, v.Rule.Strategy.Op)
		out.Rule, rule = &ref, v.Rule
	}

	// source returns the index in out.Sources of the source with these ids, adding it first.
	idx := map[string]int{}
	source := func(conn uuid.UUID, dev, origin *uuid.UUID, src resolve.Source) int {
		key := fmt.Sprint(conn, dev, origin)
		if i, ok := idx[key]; ok {
			return i
		}
		src.ConnectionID, src.Manual = conn, src.Provider == "manual"
		if dev != nil {
			src.DeviceID = *dev
		}
		s := oapi.SourceSeriesSource{Provider: src.Provider, ConnectionID: ingest.FormatConnectionID(conn), Device: deviceRef(src),
			Origin: originRef(src, ""), RuleStatus: oapi.SourceSeriesSourceRuleStatusNotInRule, Points: []oapi.SourcePoint{}}
		if rule != nil {
			switch a := rule.Assign(src); a.Membership() {
			case resolve.Excluded:
				s.RuleStatus = oapi.SourceSeriesSourceRuleStatusExcluded
			case resolve.Grouped:
				s.RuleStatus, s.Group = oapi.SourceSeriesSourceRuleStatusUsed, &rule.Groups[a.Group].ID
			case resolve.NotInRule:
			}
		}
		idx[key] = len(out.Sources)
		out.Sources = append(out.Sources, s)
		return idx[key]
	}
	for _, r := range rows {
		i := source(r.ConnectionID, r.DeviceID, r.OriginID, resolve.Source{Provider: r.Provider, DeviceType: r.DeviceType,
			DeviceModel: r.DeviceModel, OriginKey: r.OriginKey, OriginName: r.OriginName, Relayed: r.Relayed})
		s := &out.Sources[i]
		pt := oapi.SourcePoint{LocalDate: apiDate(r.LocalDate), N: int(r.Samples)}
		if !byDay {
			pt.Start = z.ptr(r.HourStart)
		}
		switch {
		case m.Agg == catalog.Additive:
			pt.Sum = optNum(r.IntervalSum)
		case r.Buckets > 0:
			pt.Mean, pt.Min, pt.Max = optNum(r.BucketMeanSum/float64(r.Buckets)), optNum(r.MinValue), optNum(r.MaxValue)
		}
		s.Points = append(s.Points, pt)
	}
	if len(daily) == 0 {
		return out, nil
	}
	// Day grain: each daily value joins its source's point of that date, or adds one.
	type sourceDay struct {
		source int
		date   time.Time
	}
	at := map[sourceDay]int{}
	for i, s := range out.Sources {
		for j, p := range s.Points {
			at[sourceDay{i, p.LocalDate.Time}] = j
		}
	}
	for _, r := range daily {
		i := source(r.ConnectionID, r.DeviceID, r.OriginID, resolve.Source{Provider: r.Provider, DeviceType: r.DeviceType,
			DeviceModel: r.DeviceModel, OriginKey: r.OriginKey, OriginName: r.OriginName, Relayed: r.Relayed})
		s := &out.Sources[i]
		if j, ok := at[sourceDay{i, r.LocalDate}]; ok {
			s.Points[j].DailyValue = optNum(r.Value)
			continue
		}
		s.Points = append(s.Points, oapi.SourcePoint{LocalDate: apiDate(r.LocalDate), DailyValue: optNum(r.Value)})
	}
	for i := range out.Sources {
		slices.SortStableFunc(out.Sources[i].Points, func(a, b oapi.SourcePoint) int { return a.LocalDate.Compare(b.LocalDate.Time) })
	}
	return out, nil
}

func optNum(f float64) *float64 {
	n := num(f)
	return &n
}

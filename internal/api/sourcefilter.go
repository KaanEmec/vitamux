package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/ingest"
	"github.com/KaanEmec/vitamux/internal/jobs"
	"github.com/KaanEmec/vitamux/internal/sourcefilter"
)

// Apple Health source filter (J22.25; docs/architecture/apple-health.md#source-filter): per
// paired device, which apps' data the phone takes from Apple Health. Both clients edit it with
// PUT /devices/{id}/source-filter; the device reads it from GET /devices/self and reports the
// apps it finds with PUT /devices/self/sources.
func (rt *router) sourceFilterRoutes() {
	rt.handle("GET /api/v1/devices/{id}/source-filter", scope(auth.ReadConfig), rt.ops.GetDeviceSourceFilter)
	rt.handle("PUT /api/v1/devices/{id}/source-filter", scope(auth.WriteConfig), rt.ops.SetDeviceSourceFilter)
	rt.handle("PUT /api/ingest/v1/devices/self/sources", access{ingest: true}, rt.reportDeviceSources)
}

const (
	maxReportedSources = 500
	maxReportedTypes   = 200
)

func (o *owner) GetDeviceSourceFilter(ctx context.Context, req oapi.GetDeviceSourceFilterRequestObject) (oapi.GetDeviceSourceFilterResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, errNoDevice
	}
	user := auth.PrincipalFrom(ctx).UserID
	row, err := d.Q().GetDeviceSourceFilter(ctx, dbq.GetDeviceSourceFilterParams{ID: id, UserID: user})
	if err != nil {
		if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
			return nil, errNoDevice
		}
		return nil, err
	}
	view, err := sourceFilterView(ctx, d.Q(), dbq.LockDeviceSourceFilterRow(row))
	if err != nil {
		return nil, err
	}
	return oapi.GetDeviceSourceFilter200JSONResponse(view), nil
}

// SetDeviceSourceFilter replaces the explicit choices. Origins whose data changes from ignored to
// taken get an anchor reset for the types now taken, and the device's raw payloads holding their
// ignored records are normalized again, in the same transaction.
func (o *owner) SetDeviceSourceFilter(ctx context.Context, req oapi.SetDeviceSourceFilterRequestObject) (oapi.SetDeviceSourceFilterResponseObject, error) {
	d, err := o.ownerDB()
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return nil, errNoDevice
	}
	in := make([]sourcefilter.Choice, len(req.Body.Origins))
	for i, c := range req.Body.Origins {
		in[i] = sourcefilter.Choice{BundleID: c.BundleID, Mode: sourcefilter.Mode(c.Mode), Name: ptrVal(c.Name)}
		if c.Types != nil {
			in[i].Types = *c.Types
		}
	}
	choices, err := sourcefilter.Normalize(in)
	if fe, ok := errors.AsType[*sourcefilter.FieldError](err); ok {
		return nil, problemErr(CodeValidationFailed, "invalid source filter", FieldError{Pointer: fe.Pointer, Detail: fe.Detail})
	} else if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	var view oapi.SourceFilterView
	err = d.Tx(ctx, func(q *dbq.Queries) error {
		row, err := q.LockDeviceSourceFilter(ctx, dbq.LockDeviceSourceFilterParams{ID: id, UserID: p.UserID})
		if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) || err == nil && row.RevokedAt != nil {
			return errNoDevice
		} else if err != nil {
			return err
		}
		if v := req.Body.Version; v != nil && int32(*v) != row.SourceFilterVersion { //nolint:gosec // compared, not stored
			return problemErr(CodeConflict, "the source filter changed since version "+strconv.Itoa(*v)+"; reload it")
		}
		before, err := sourcefilter.Build(ctx, q, p.UserID, row.SourceFilterVersion, row.SourceFilter)
		if err != nil {
			return err
		}
		after := before
		after.Choices = choices
		reported, err := sourcefilter.ParseSources(row.HealthSources)
		if err != nil {
			return err
		}
		origins, err := q.ListAppleOrigins(ctx, p.UserID)
		if err != nil {
			return err
		}
		changes := sourcefilter.Diff(before, after, filterBundles(reported, slices.Concat(before.Choices, choices), origins), sourcefilter.Writes(reported))

		body, err := sourcefilter.Stored{Origins: choices}.Marshal()
		if err != nil {
			return err
		}
		if row.SourceFilterVersion, err = q.SetDeviceSourceFilter(ctx, dbq.SetDeviceSourceFilterParams{SourceFilter: body, ID: id}); err != nil {
			return err
		}
		row.SourceFilter = body
		pulled, renormalized, err := pullTaken(ctx, q, p.UserID, id, after, changes)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, q, audit.Event{UserID: &p.UserID, Actor: p.Actor(), Action: "device.source_filter",
			TargetType: "client", TargetID: id.String(), Detail: map[string]any{"version": row.SourceFilterVersion,
				"changes": changes, "anchor_reset": pulled, "renormalized_payloads": renormalized}}); err != nil {
			return err
		}
		view, err = sourceFilterView(ctx, q, row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return oapi.SetDeviceSourceFilter200JSONResponse(view), nil
}

// pullTaken queues an anchor reset for the types newly taken and moves the device's raw payloads
// with ignored records of those origins back to stored, queueing their batches for normalization.
// It returns the reset types and how many payloads were queued.
func pullTaken(ctx context.Context, q *dbq.Queries, user, device uuid.UUID, after sourcefilter.Filter, changes []sourcefilter.Transition) ([]string, int, error) {
	var types, keys []string
	for _, c := range changes {
		if len(c.Pulled) > 0 {
			types = append(types, c.Pulled...)
			keys = append(keys, c.BundleID)
		}
	}
	if len(types) == 0 {
		return nil, 0, nil
	}
	slices.Sort(types)
	types = slices.Compact(types)
	if slices.Contains(types, allTypes) {
		types = []string{allTypes}
	}
	now := time.Now().UTC()
	resets := make(map[string]time.Time, len(types))
	for _, t := range types {
		resets[t] = now
	}
	body, err := json.Marshal(resets)
	if err != nil {
		return nil, 0, err
	}
	if _, err := q.AddAnchorResets(ctx, dbq.AddAnchorResetsParams{Resets: body, ID: device, UserID: user}); err != nil {
		return nil, 0, err
	}
	raws, err := q.ListIgnoredRawForTake(ctx, dbq.ListIgnoredRawForTakeParams{UserID: user, ClientID: &device, OriginKeys: keys})
	if err != nil {
		return nil, 0, err
	}
	batches := map[uuid.UUID]bool{}
	queued := map[int64]bool{}
	for _, r := range raws {
		if queued[r.ID] || !after.Decide(r.OriginKey).Takes(r.Type) {
			continue
		}
		if err := ingest.SetStatus(ctx, q, r.ID, ingest.StatusStored); err != nil {
			return nil, 0, err
		}
		batches[r.BatchID], queued[r.ID] = true, true
	}
	n := len(queued)
	for b := range batches {
		if _, _, err := jobs.Enqueue(ctx, q, jobs.NewJob{Kind: ingest.KindNormalizeBatch,
			DedupeKey: ingest.KindNormalizeBatch + ":" + b.String(), Payload: ingest.NormalizePayload{BatchID: b}}); err != nil {
			return nil, 0, err
		}
	}
	return types, n, nil
}

// filterBundles lists every app a view or diff covers: reported, chosen or seen in Apple Health.
// Export origins (export:<name>) never come from a device, so no filter applies to them.
func filterBundles(reported []sourcefilter.HealthSource, choices []sourcefilter.Choice, origins []dbq.ListAppleOriginsRow) []string {
	var out []string
	for _, s := range reported {
		out = append(out, s.BundleID)
	}
	for _, c := range choices {
		out = append(out, c.BundleID)
	}
	for _, o := range origins {
		if !strings.HasPrefix(o.OriginKey, "export:") {
			out = append(out, o.OriginKey)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sourceFilterView renders a device's filter as both clients list it.
func sourceFilterView(ctx context.Context, q *dbq.Queries, row dbq.LockDeviceSourceFilterRow) (oapi.SourceFilterView, error) {
	f, err := sourcefilter.Build(ctx, q, row.UserID, row.SourceFilterVersion, row.SourceFilter)
	if err != nil {
		return oapi.SourceFilterView{}, err
	}
	reported, err := sourcefilter.ParseSources(row.HealthSources)
	if err != nil {
		return oapi.SourceFilterView{}, err
	}
	origins, err := q.ListAppleOrigins(ctx, row.UserID)
	if err != nil {
		return oapi.SourceFilterView{}, db.MapErr(err)
	}
	patterns, err := q.ListAppleRelayPatterns(ctx)
	if err != nil {
		return oapi.SourceFilterView{}, db.MapErr(err)
	}
	byKey := map[string]dbq.ListAppleOriginsRow{}
	for _, o := range origins {
		byKey[o.OriginKey] = o
	}
	byBundle := map[string]sourcefilter.HealthSource{}
	for _, s := range reported {
		byBundle[s.BundleID] = s
	}
	view := oapi.SourceFilterView{DeviceID: row.ID, Version: int(row.SourceFilterVersion), SourcesReportedAt: row.HealthSourcesAt,
		DefaultIgnore: defaultIgnore(f.Defaults), Origins: []oapi.SourceFilterOrigin{}}
	for _, b := range filterBundles(reported, f.Choices, origins) {
		dec := f.Decide(b)
		o := oapi.SourceFilterOrigin{BundleID: b, Mode: oapi.SourceFilterMode(dec.Mode), Types: append([]string{}, dec.Types...),
			Explicit: dec.Explicit, DefaultMode: oapi.SourceFilterOriginDefaultMode(dec.DefaultMode),
			Writes: []oapi.SourceType{}, Classification: oapi.SourceFilterOriginClassificationDirect}
		if dec.Reason != sourcefilter.ReasonNone {
			r := oapi.SourceFilterOriginDefaultReason(dec.Reason)
			o.DefaultReason = &r
		}
		if dec.Provider != "" {
			o.ReasonProvider, o.ReasonProviderName = &dec.Provider, &dec.ProviderName
		}
		if s, ok := byBundle[b]; ok {
			o.Name = optString(s.Name)
			for _, t := range s.Types {
				o.Writes = append(o.Writes, oapi.SourceType{Type: t.Type, LastSampleAt: t.LastSampleAt})
			}
		}
		if i := slices.IndexFunc(f.Choices, func(c sourcefilter.Choice) bool { return c.BundleID == b }); i >= 0 && o.Name == nil {
			o.Name = optString(f.Choices[i].Name)
		}
		if r, ok := byKey[b]; ok {
			id := r.ID
			o.OriginID, o.RelayedProvider, o.IgnoredRecords = &id, r.RelayedProvider, r.IgnoredRecords
			if o.Name == nil {
				o.Name = r.Name
			}
			switch {
			case r.IsNative:
				o.Classification = oapi.SourceFilterOriginClassificationNative
			case r.RelayedProvider != nil:
				o.Classification = oapi.SourceFilterOriginClassificationRelayed
			}
		} else {
			switch {
			case sourcefilter.Native(b):
				o.Classification = oapi.SourceFilterOriginClassificationNative
			default:
				for _, k := range patterns {
					if sourcefilter.Like(k.OriginPattern, b) || sourcefilter.Like(k.OriginPattern, sourcefilter.Parent(b)) {
						o.Classification, o.RelayedProvider = oapi.SourceFilterOriginClassificationRelayed, &k.Provider
						break
					}
				}
			}
		}
		view.Origins = append(view.Origins, o)
	}
	slices.SortStableFunc(view.Origins, func(a, b oapi.SourceFilterOrigin) int {
		return strings.Compare(strings.ToLower(cmp.Or(ptrVal(a.Name), a.BundleID)), strings.ToLower(cmp.Or(ptrVal(b.Name), b.BundleID)))
	})
	return view, nil
}

func defaultIgnore(defs []sourcefilter.Default) []oapi.SourceFilterDefault {
	out := make([]oapi.SourceFilterDefault, len(defs))
	for i, d := range defs {
		out[i] = oapi.SourceFilterDefault{OriginPattern: d.Pattern, Provider: d.Provider, ProviderName: d.ProviderName}
	}
	return out
}

// deviceSourceFilter is the filter as GET /devices/self returns it: the explicit choices and
// the default-ignore patterns, which the device applies (HealthBridgeKit's SourceFilter).
func deviceSourceFilter(ctx context.Context, q *dbq.Queries, c dbq.Client) (map[string]any, error) {
	f, err := sourcefilter.Build(ctx, q, c.UserID, c.SourceFilterVersion, c.SourceFilter)
	if err != nil {
		return nil, err
	}
	origins := make([]map[string]any, len(f.Choices))
	for i, ch := range f.Choices {
		m := map[string]any{"bundle_id": ch.BundleID, "mode": ch.Mode}
		if ch.Name != "" {
			m["name"] = ch.Name
		}
		if ch.Mode == sourcefilter.PerType {
			m["types"] = ch.Types
		}
		origins[i] = m
	}
	return map[string]any{"version": f.Version, "origins": origins, "default_ignore": defaultIgnore(f.Defaults)}, nil
}

// reportDeviceSources stores the apps the calling device found in Apple Health.
func (rt *router) reportDeviceSources(w http.ResponseWriter, r *http.Request) {
	if rt.opts.DB == nil {
		writeProblem(w, r, CodeUnavailable, "ingest is unavailable")
		return
	}
	var in struct {
		Sources []sourcefilter.HealthSource `json:"sources"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if fe := checkReportedSources(in.Sources); fe != nil {
		writeProblem(w, r, CodeValidationFailed, "invalid sources", *fe)
		return
	}
	if in.Sources == nil {
		in.Sources = []sourcefilter.HealthSource{}
	}
	body, err := json.Marshal(in.Sources)
	if err != nil {
		rt.internal(w, r, "report sources", err)
		return
	}
	n, err := rt.opts.DB.Q().SetDeviceHealthSources(r.Context(), dbq.SetDeviceHealthSourcesParams{HealthSources: body,
		ReportedAt: time.Now(), ID: auth.PrincipalFrom(r.Context()).ID})
	switch {
	case err != nil:
		rt.internal(w, r, "report sources", db.MapErr(err))
	case n == 0:
		writeProblem(w, r, CodeForbidden, "only a paired device reports its sources")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// checkReportedSources applies DeviceSourcesReport's limits.
func checkReportedSources(sources []sourcefilter.HealthSource) *FieldError {
	if len(sources) > maxReportedSources {
		return &FieldError{Pointer: "/sources", Detail: "at most " + strconv.Itoa(maxReportedSources)}
	}
	for i, s := range sources {
		at := "/sources/" + strconv.Itoa(i)
		if s.BundleID == "" || len(s.BundleID) > 255 || strings.ContainsFunc(s.BundleID, unicode.IsControl) {
			return &FieldError{Pointer: at + "/bundle_id", Detail: "1 to 255 characters without control characters"}
		}
		if utf8.RuneCountInString(s.Name) > 200 || strings.ContainsFunc(s.Name, unicode.IsControl) {
			return &FieldError{Pointer: at + "/name", Detail: "at most 200 characters without control characters"}
		}
		if len(s.Types) > maxReportedTypes {
			return &FieldError{Pointer: at + "/types", Detail: "at most " + strconv.Itoa(maxReportedTypes)}
		}
		for j, t := range s.Types {
			if !hkType.MatchString(t.Type) {
				return &FieldError{Pointer: at + "/types/" + strconv.Itoa(j) + "/type", Detail: "not a HealthKit type identifier"}
			}
		}
	}
	return nil
}

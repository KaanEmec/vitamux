package withings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

const measurePath = "/measure"

// cursor is the withings.measures cursor. The stored stream cursor is {"lastupdate": N}; the
// pages of one call add the next offset and, for lastupdate calls, the first page's
// updatetime, which becomes the next lastupdate once the call is complete.
type cursor struct {
	LastUpdate int64 `json:"lastupdate"`
	Offset     int64 `json:"offset,omitempty"`
	UpdateTime int64 `json:"updatetime,omitempty"`
}

// Plan returns one unit. Incremental and manual runs call getmeas with the stored lastupdate
// (0 before the first sync: the whole history); an interrupted call restarts from it, since
// re-fetched groups are no-ops. Correction and backfill runs fetch [From, To) by measurement
// date; the runtime splits backfills into 30-day units.
func (*Connector) Plan(_ context.Context, _ connectors.Conn, req connectors.PlanRequest) ([]connectors.WorkUnit, error) {
	switch req.Mode {
	case connectors.ModeIncremental, connectors.ModeManual:
		var cur cursor
		if len(req.Cursor) > 0 {
			if err := json.Unmarshal(req.Cursor, &cur); err != nil {
				return nil, fmt.Errorf("withings: unreadable cursor: %w", connectors.ErrPermanent)
			}
		}
		b, _ := json.Marshal(cursor{LastUpdate: cur.LastUpdate})
		return []connectors.WorkUnit{{Cursor: b}}, nil
	case connectors.ModeCorrection, connectors.ModeBackfill:
		if req.From.IsZero() || !req.To.After(req.From) {
			return nil, fmt.Errorf("withings: %s needs a window: %w", req.Mode, connectors.ErrPermanent)
		}
		return []connectors.WorkUnit{{From: req.From, To: req.To}}, nil
	}
	return nil, fmt.Errorf("withings: unsupported mode %q: %w", req.Mode, connectors.ErrPermanent)
}

// Fetch gets one getmeas page and puts one raw record per measure group
// (docs/providers/withings.md#how-vitamux-syncs).
func (w *Connector) Fetch(ctx context.Context, c connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, out *connectors.RawSink) (connectors.FetchResult, error) {
	var cur cursor
	if len(u.Cursor) > 0 {
		if err := json.Unmarshal(u.Cursor, &cur); err != nil {
			return connectors.FetchResult{}, fmt.Errorf("withings: unreadable cursor: %w", connectors.ErrPermanent)
		}
	}
	incremental := u.From.IsZero() && u.To.IsZero()
	form := url.Values{"action": {"getmeas"}, "category": {"1"}}
	if incremental {
		form.Set("lastupdate", strconv.FormatInt(cur.LastUpdate, 10))
	} else {
		form.Set("startdate", strconv.FormatInt(u.From.Unix(), 10))
		form.Set("enddate", strconv.FormatInt(u.To.Unix(), 10))
	}
	if cur.Offset > 0 {
		form.Set("offset", strconv.FormatInt(cur.Offset, 10))
	}
	env, err := w.post(ctx, c.HTTP, measurePath, cred.AccessToken, form)
	switch {
	case err != nil:
		return connectors.FetchResult{}, err
	case env.http != 0:
		return connectors.FetchResult{}, fmt.Errorf("withings getmeas: HTTP %d: %w", env.http, connectors.ErrPermanent)
	case env.Status != 0:
		return connectors.FetchResult{}, statusError("getmeas", env.Status)
	}
	params := map[string]any{}
	for k := range form {
		params[k] = form.Get(k)
	}
	req := ingest.Request{Endpoint: "POST " + measurePath, Params: params}
	p, err := decodePage(env.Body)
	if err == nil && p.More && p.Offset <= cur.Offset {
		err = errDrift // paging that does not advance would loop forever
	}
	if err != nil {
		// Keep the page (the runtime stores it quarantined) and never substitute other data.
		fp, _ := ingest.ShapeFingerprint(env.Body)
		out.Put(ingest.RawItem{ExternalKey: "getmeas-page:" + fp, ContentType: "application/json", Body: env.Body, Request: req})
		return connectors.FetchResult{}, &connectors.SchemaDriftError{Endpoint: "measure getmeas", Fingerprint: fp}
	}
	var hw time.Time
	for _, g := range p.groups {
		out.Put(ingest.RawItem{
			ExternalKey: "measuregrp:" + strconv.FormatInt(g.id, 10), ContentType: "application/json",
			Body: groupRecord(p.Timezone, g.raw), Request: req,
		})
		if t := time.Unix(g.date, 0).UTC(); t.After(hw) {
			hw = t
		}
	}
	res := connectors.FetchResult{HighWatermark: hw}
	next := cursor{LastUpdate: cur.LastUpdate, UpdateTime: cur.UpdateTime}
	if next.UpdateTime == 0 {
		next.UpdateTime = p.UpdateTime
	}
	switch {
	case p.More:
		next.Offset = p.Offset
		if !incremental {
			next = cursor{Offset: p.Offset}
		}
	case incremental:
		res.Done, next = true, cursor{LastUpdate: next.UpdateTime}
	default:
		return connectors.FetchResult{HighWatermark: hw, Done: true}, nil
	}
	res.NextCursor, _ = json.Marshal(next)
	return res, nil
}

// groupRecord is the raw body of one group: the response's timezone and the group as received.
func groupRecord(tz, group json.RawMessage) []byte {
	if len(tz) == 0 {
		tz = json.RawMessage("null")
	}
	b := append([]byte(`{"timezone":`), tz...)
	b = append(b, `,"measuregrp":`...)
	b = append(b, group...)
	return append(b, '}')
}

var errDrift = fmt.Errorf("withings: unexpected getmeas shape")

type page struct {
	UpdateTime int64
	Timezone   json.RawMessage
	More       bool
	Offset     int64
	groups     []group
}

type group struct {
	id, date int64
	raw      json.RawMessage
}

// decodePage checks the fields the connector relies on; a missing or retyped one is drift.
// Extra fields are fine.
func decodePage(body []byte) (page, error) {
	var b struct {
		UpdateTime  *flexInt           `json:"updatetime"`
		Timezone    json.RawMessage    `json:"timezone"`
		MeasureGrps *[]json.RawMessage `json:"measuregrps"`
		More        flexInt            `json:"more"`
		Offset      flexInt            `json:"offset"`
	}
	if err := json.Unmarshal(body, &b); err != nil || b.UpdateTime == nil || b.MeasureGrps == nil {
		return page{}, errDrift
	}
	p := page{UpdateTime: int64(*b.UpdateTime), Timezone: b.Timezone, More: b.More != 0, Offset: int64(b.Offset)}
	for _, raw := range *b.MeasureGrps {
		var g struct {
			GrpID    *int64 `json:"grpid"`
			Date     *int64 `json:"date"`
			Measures *[]struct {
				Value *int64 `json:"value"`
				Type  *int   `json:"type"`
				Unit  *int   `json:"unit"`
			} `json:"measures"`
		}
		if err := json.Unmarshal(raw, &g); err != nil || g.GrpID == nil || g.Date == nil || g.Measures == nil {
			return page{}, errDrift
		}
		for _, m := range *g.Measures {
			if m.Value == nil || m.Type == nil || m.Unit == nil {
				return page{}, errDrift
			}
		}
		p.groups = append(p.groups, group{id: *g.GrpID, date: *g.Date, raw: bytes.Clone(raw)})
	}
	return p, nil
}

// flexInt accepts an integer sent as a number, a numeric string or a boolean (more).
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	switch s := string(bytes.Trim(b, `"`)); s {
	case "true":
		*f = 1
	case "false", "null":
		*f = 0
	default:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		*f = flexInt(n)
	}
	return nil
}

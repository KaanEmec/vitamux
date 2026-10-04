package withings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

const (
	measurePath   = "/measure"
	measureV2Path = "/v2/measure"
	sleepPath     = "/v2/sleep"

	// lastupdateOverlap: getactivity and getsummary answer without an updatetime, so the next
	// lastupdate is the run's slot minus this margin; re-fetched records are no-ops.
	lastupdateOverlap = time.Hour
)

// cursor is the cursor of the lastupdate streams (measures, activity, sleep) and of intraday.
// The stored cursor is {"lastupdate": N} (intraday: {"start": N}); the pages of one call add
// the next offset and, for lastupdate calls, the updatetime that becomes the next lastupdate
// once the call is complete.
type cursor struct {
	LastUpdate int64 `json:"lastupdate"`
	Offset     int64 `json:"offset,omitempty"`
	UpdateTime int64 `json:"updatetime,omitempty"`
	Start      int64 `json:"start,omitempty"`
}

func readCursor(b json.RawMessage) (cursor, error) {
	var cur cursor
	if len(b) > 0 {
		if err := json.Unmarshal(b, &cur); err != nil {
			return cursor{}, fmt.Errorf("withings: unreadable cursor: %w", connectors.ErrPermanent)
		}
	}
	return cur, nil
}

// Plan returns one unit. Incremental and manual runs of the lastupdate streams call the API with
// the stored lastupdate (0 before the first sync: the whole history); an interrupted call
// restarts from it, since re-fetched records are no-ops. Intraday runs fetch from the stored
// start (before the first sync, the last intradayFirst) to the slot. Correction and backfill
// runs fetch [From, To) by date; the runtime splits backfills into units.
func (*Connector) Plan(_ context.Context, _ connectors.Conn, req connectors.PlanRequest) ([]connectors.WorkUnit, error) {
	cur, err := readCursor(req.Cursor)
	if err != nil {
		return nil, err
	}
	switch req.Mode {
	case connectors.ModeIncremental, connectors.ModeManual:
		switch req.Stream {
		case StreamIntraday:
			from := req.To.Add(-intradayFirst)
			if cur.Start > 0 {
				from = time.Unix(cur.Start, 0).UTC()
			}
			return []connectors.WorkUnit{intradayUnit(from, req.To)}, nil
		case StreamActivity, StreamSleep:
			b, _ := json.Marshal(cursor{LastUpdate: cur.LastUpdate, UpdateTime: req.To.Add(-lastupdateOverlap).Unix()})
			return []connectors.WorkUnit{{Cursor: b}}, nil
		}
		b, _ := json.Marshal(cursor{LastUpdate: cur.LastUpdate})
		return []connectors.WorkUnit{{Cursor: b}}, nil
	case connectors.ModeCorrection, connectors.ModeBackfill:
		if req.From.IsZero() || !req.To.After(req.From) {
			return nil, fmt.Errorf("withings: %s needs a window: %w", req.Mode, connectors.ErrPermanent)
		}
		if req.Stream == StreamIntraday {
			return []connectors.WorkUnit{intradayUnit(req.From, req.To)}, nil
		}
		return []connectors.WorkUnit{{From: req.From, To: req.To}}, nil
	}
	return nil, fmt.Errorf("withings: unsupported mode %q: %w", req.Mode, connectors.ErrPermanent)
}

// Fetch gets one page of the unit's stream.
func (w *Connector) Fetch(ctx context.Context, c connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, out *connectors.RawSink) (connectors.FetchResult, error) {
	cur, err := readCursor(u.Cursor)
	if err != nil {
		return connectors.FetchResult{}, err
	}
	switch u.Stream {
	case StreamActivity:
		return w.fetchActivity(ctx, c, cred, u, cur, out)
	case StreamIntraday:
		return w.fetchIntraday(ctx, c, cred, u, cur, out)
	case StreamSleep:
		return w.fetchSleep(ctx, c, cred, u, cur, out)
	}
	return w.fetchMeasures(ctx, c, cred, u, cur, out)
}

// fetchMeasures gets one getmeas page and puts one raw record per measure group
// (docs/providers/withings.md#how-vitamux-syncs).
func (w *Connector) fetchMeasures(ctx context.Context, c connectors.Conn, cred connectors.Credentials, u connectors.WorkUnit, cur cursor, out *connectors.RawSink) (connectors.FetchResult, error) {
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
	body, req, err := w.call(ctx, c, cred, measurePath, form)
	if err != nil {
		return connectors.FetchResult{}, err
	}
	p, err := decodePage(body)
	if err == nil && p.More && p.Offset <= cur.Offset {
		err = errDrift // paging that does not advance would loop forever
	}
	if err != nil {
		return connectors.FetchResult{}, drift(out, req, body)
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
	if cur.UpdateTime == 0 {
		cur.UpdateTime = p.UpdateTime
	}
	return cur.next(incremental, p.More, p.Offset, hw), nil
}

// next is the result of a page of a lastupdate call (incremental) or of a date window: the next
// offset while more pages follow, then, for a lastupdate call, cur.UpdateTime as the next
// lastupdate.
func (cur cursor) next(incremental, more bool, offset int64, hw time.Time) connectors.FetchResult {
	res := connectors.FetchResult{HighWatermark: hw}
	next := cursor{LastUpdate: cur.LastUpdate, UpdateTime: cur.UpdateTime}
	switch {
	case more:
		next.Offset = offset
		if !incremental {
			next = cursor{Offset: offset}
		}
	case incremental:
		res.Done, next = true, cursor{LastUpdate: next.UpdateTime}
	default:
		res.Done = true
		return res
	}
	res.NextCursor, _ = json.Marshal(next)
	return res
}

// call posts one API action and returns its body and the request to record with raw items.
// Transport and body-status failures are typed.
func (w *Connector) call(ctx context.Context, c connectors.Conn, cred connectors.Credentials, path string, form url.Values) (json.RawMessage, ingest.Request, error) {
	action := form.Get("action")
	env, err := w.post(ctx, c.HTTP, path, cred.AccessToken, form)
	switch {
	case err != nil:
		return nil, ingest.Request{}, err
	case env.http != 0:
		return nil, ingest.Request{}, fmt.Errorf("withings %s: HTTP %d: %w", action, env.http, connectors.ErrPermanent)
	case env.Status != 0:
		return nil, ingest.Request{}, statusError(action, env.Status)
	}
	params := map[string]any{}
	for k := range form {
		params[k] = form.Get(k)
	}
	return env.Body, ingest.Request{Endpoint: "POST " + path, Params: params}, nil
}

// drift keeps an unexpected response (the runtime stores it quarantined) and never substitutes
// other data.
func drift(out *connectors.RawSink, req ingest.Request, body []byte) error {
	action, _ := req.Params["action"].(string)
	fp, _ := ingest.ShapeFingerprint(body)
	out.Put(ingest.RawItem{ExternalKey: action + "-page:" + fp, ContentType: "application/json", Body: body, Request: req})
	return &connectors.SchemaDriftError{Endpoint: strings.TrimPrefix(req.Endpoint, "POST /") + " " + action, Fingerprint: fp}
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

var errDrift = fmt.Errorf("withings: unexpected response shape")

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

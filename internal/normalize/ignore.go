package normalize

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/sourcefilter"
)

// Ignored summarizes the records of one origin and inventory item that DropOrigins left out:
// Kind and Code as in the inventory (metric, group, event, sleep, workouts).
type Ignored struct {
	Origin, Kind, Code string
	Records            int
	First, Last        time.Time
}

// DropOrigins removes the records whose origin ignore reports, so they are not normalized
// (J22.25, the server-side guard of the Apple Health source filter). Origins stay, so the origin
// is listed and the ignored records can name it; devices stay only when a kept record uses them,
// files only when a kept event references them. Tombstones stay: withdrawing a row is never
// collecting data. It returns the output to write and what it left out, sorted.
func DropOrigins(out Output, ignore func(origin string) bool) (Output, []Ignored) {
	acc := map[[3]string]*Ignored{}
	drop := func(origin, kind, code string, start time.Time, end *time.Time) bool {
		if origin == "" || !ignore(origin) {
			return false
		}
		last := start
		if end != nil {
			last = *end
		}
		k := [3]string{origin, kind, code}
		if x, ok := acc[k]; ok {
			x.Records++
			x.First, x.Last = minTime(x.First, start), maxTime(x.Last, last)
		} else {
			acc[k] = &Ignored{Origin: origin, Kind: kind, Code: code, Records: 1, First: start, Last: last}
		}
		return true
	}
	out.Measurements = slices.DeleteFunc(slices.Clone(out.Measurements), func(m Measurement) bool {
		return drop(m.Origin, "metric", m.Metric, m.Start, m.End)
	})
	out.Groups = slices.DeleteFunc(slices.Clone(out.Groups), func(g Group) bool {
		return drop(g.Origin, "group", g.Kind, g.MeasuredAt, nil)
	})
	out.Sleep = slices.DeleteFunc(slices.Clone(out.Sleep), func(s SleepSession) bool {
		return drop(s.Origin, "sleep", "sleep", s.Start, &s.End)
	})
	out.Workouts = slices.DeleteFunc(slices.Clone(out.Workouts), func(w Workout) bool {
		return drop(w.Origin, "workouts", "workouts", w.Start, &w.End)
	})
	out.Events = slices.DeleteFunc(slices.Clone(out.Events), func(e Event) bool {
		return drop(e.Origin, "event", e.Code, e.Start, e.End)
	})
	if len(acc) == 0 {
		return out, nil
	}

	used := map[string]bool{}
	files := map[string]bool{}
	for _, m := range out.Measurements {
		used[m.Device] = true
	}
	for _, g := range out.Groups {
		used[g.Device] = true
		for _, c := range g.Components {
			used[c.Device] = true
		}
	}
	for _, s := range out.Sleep {
		used[s.Device] = true
	}
	for _, w := range out.Workouts {
		used[w.Device] = true
	}
	for _, e := range out.Events {
		used[e.Device] = true
		files[string(e.FileSHA256)] = true
	}
	out.Devices = slices.DeleteFunc(slices.Clone(out.Devices), func(d Device) bool { return !used[d.Fingerprint] })
	out.Files = slices.DeleteFunc(slices.Clone(out.Files), func(f File) bool { return !files[string(f.SHA256)] })

	ignored := make([]Ignored, 0, len(acc))
	for _, x := range acc {
		ignored = append(ignored, *x)
	}
	slices.SortFunc(ignored, func(a, b Ignored) int {
		return cmp.Or(strings.Compare(a.Origin, b.Origin), strings.Compare(a.Kind, b.Kind), strings.Compare(a.Code, b.Code))
	})
	return out, ignored
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// pageType is the HealthKit type of a healthkit.samples page: its external key is '<type>:<key>'.
func pageType(externalKey string) string {
	t, _, _ := strings.Cut(externalKey, ":")
	return t
}

// sourceFilterGuard applies the pushing device's source filter to an Apple Health payload's
// output. Payloads of other providers or senders pass unchanged.
func sourceFilterGuard(ctx context.Context, q *dbq.Queries, provider string, raw RawPayload, out Output) (Output, []Ignored, error) {
	if provider != "apple_health" {
		return out, nil, nil
	}
	f, ok, err := sourcefilter.ForRaw(ctx, q, raw.ID)
	if err != nil || !ok {
		return out, nil, err
	}
	typ := pageType(raw.ExternalKey)
	kept, ignored := DropOrigins(out, func(origin string) bool { return !f.Decide(origin).Takes(typ) })
	for i, x := range ignored {
		if i == 0 || ignored[i-1].Origin != x.Origin {
			kept.Warnings = append(kept.Warnings, Warning{Code: "ignored_by_filter", Detail: x.Origin})
		}
	}
	return kept, ignored, nil
}

// storeIgnored replaces the payload's ignored records; an empty list clears them (the payload
// was normalized again after its origins were taken).
func storeIgnored(ctx context.Context, q *dbq.Queries, rawID int64, ignored []Ignored) error {
	if err := q.DeleteIgnoredRecords(ctx, rawID); err != nil {
		return db.MapErr(err)
	}
	if len(ignored) == 0 {
		return nil
	}
	p := dbq.InsertIgnoredRecordsParams{RawPayloadID: rawID}
	for _, x := range ignored {
		p.OriginKeys = append(p.OriginKeys, x.Origin)
		p.ItemKinds = append(p.ItemKinds, x.Kind)
		p.ItemCodes = append(p.ItemCodes, x.Code)
		p.Records = append(p.Records, int32(min(x.Records, 1<<31-1))) //nolint:gosec // clamped
		p.FirstAts = append(p.FirstAts, x.First)
		p.LastAts = append(p.LastAts, x.Last)
	}
	return db.MapErr(q.InsertIgnoredRecords(ctx, p))
}

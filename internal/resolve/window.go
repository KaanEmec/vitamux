package resolve

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Window is one resolution window. Start is inclusive and End exclusive, except for latest
// windows, which have no Start and include End (the as_of instant).
type Window struct {
	Kind  catalog.Window
	Start time.Time
	End   time.Time
	Date  time.Time // local date (midnight UTC) for bucket, hour, local_day, local_night and sleep_episode
	// Key identifies the window within its kind, for resolved_cache and manual_overrides:
	// the UTC start (RFC 3339) for bucket, hour and sleep_episode; the date (2006-01-02) for
	// local_day and local_night; the as_of instant for latest; "g:<group id>" or "m:<row id>"
	// for reading.
	Key string
}

// ErrWindowKind is returned for a window kind that DayWindows cannot build.
var ErrWindowKind = errors.New("resolve: window kind needs its own constructor")

// Partial reports whether the window ends after now; the result then carries partial: true.
func (w Window) Partial(now time.Time) bool { return w.End.After(now) }

// Includes reports whether in belongs to w. local_day uses the stored local_date, so a travel
// day is never split. Daily values belong to local_day windows only. Intervals belong to
// every instant window they overlap (J09.4 pro-rates them); samples by their instant.
func (w Window) Includes(in Input) bool {
	switch w.Kind {
	case catalog.WindowLocalDay:
		return in.LocalDate.Equal(w.Date)
	case catalog.WindowLatest:
		return !in.At().After(w.End)
	case catalog.WindowReading:
		return readingKey(in) == w.Key
	default: // bucket, hour, local_night, sleep_episode
		if in.Kind == catalog.DailyValue {
			return false
		}
		if in.End.IsZero() || in.End.Equal(in.Start) {
			return !in.Start.Before(w.Start) && in.Start.Before(w.End)
		}
		return in.End.After(w.Start) && in.Start.Before(w.End)
	}
}

const dateLayout = "2006-01-02"

// DayWindows returns the windows of one local date for the kinds bound to a date: the bucket
// or hour windows covering the day, the day itself, or the night (anchor from Rule.NightAnchor).
// sleep_episode, latest and reading windows come from EpisodeWindow, LatestWindow and
// ReadingWindows. A timeline without periods is normalize.ErrNoTimezone.
func DayWindows(rw RuleWindow, date time.Time, tl normalize.Timeline, anchor time.Duration) ([]Window, error) {
	switch rw.Kind {
	case catalog.WindowBucket:
		size := rw.Size.Std()
		if size <= 0 || time.Hour%size != 0 {
			return nil, fmt.Errorf("resolve: invalid bucket size %q", rw.Size)
		}
		return Buckets(date, size, tl)
	case catalog.WindowHour:
		return Buckets(date, time.Hour, tl)
	case catalog.WindowLocalDay:
		w, err := LocalDay(date, tl)
		return []Window{w}, err
	case catalog.WindowLocalNight:
		w, err := LocalNight(date, anchor, tl)
		return []Window{w}, err
	default:
		return nil, fmt.Errorf("%w: %s", ErrWindowKind, rw.Kind)
	}
}

// LocalDay is the span of a local date: from its local midnight to the next one. Hours follow
// the wall clock, so DST days are 23 or 25 hours long.
func LocalDay(date time.Time, tl normalize.Timeline) (Window, error) {
	start, err := dayStart(date, tl)
	if err != nil {
		return Window{}, err
	}
	end, err := dayStart(date.AddDate(0, 0, 1), tl)
	if err != nil {
		return Window{}, err
	}
	d := midnightUTC(date)
	return Window{Kind: catalog.WindowLocalDay, Start: start, End: end, Date: d, Key: d.Format(dateLayout)}, nil
}

// Buckets splits a local date into windows of size (30 s, or 1, 5, 15, 30 or 60 minutes),
// aligned to local midnight in the zone in effect at that midnight. Offsets change by whole hours in
// almost every zone, so walking absolute time keeps the buckets on the wall clock: 23 or 25
// hours on DST days. On a day that changes zone (travel) the walk continues in the starting
// zone and the last bucket is cut at the next local midnight.
func Buckets(date time.Time, size time.Duration, tl normalize.Timeline) ([]Window, error) {
	day, err := LocalDay(date, tl)
	if err != nil {
		return nil, err
	}
	kind := catalog.WindowBucket
	if size == time.Hour {
		kind = catalog.WindowHour
	}
	var out []Window
	for t := day.Start; t.Before(day.End); t = t.Add(size) {
		end := t.Add(size)
		if end.After(day.End) {
			end = day.End
		}
		out = append(out, Window{Kind: kind, Start: t, End: end, Date: day.Date, Key: t.UTC().Format(time.RFC3339)})
	}
	return out, nil
}

// LocalNight is the candidate span of night D (ADR-0009): sessions ending in
// [D-1 at anchor, D at anchor), local time. J09.6 builds episodes from those candidates;
// WithEpisode then narrows the window to the main episode.
func LocalNight(date time.Time, anchor time.Duration, tl normalize.Timeline) (Window, error) {
	d := midnightUTC(date)
	start, err := localAt(d.AddDate(0, 0, -1), anchor, tl)
	if err != nil {
		return Window{}, err
	}
	end, err := localAt(d, anchor, tl)
	if err != nil {
		return Window{}, err
	}
	return Window{Kind: catalog.WindowLocalNight, Start: start, End: end, Date: d, Key: d.Format(dateLayout)}, nil
}

// NightOf returns the night date of a sleep session ending at end (ADR-0009): its local wake
// date, or the next date when it ends at or after the anchor. The zone comes from the record
// first, then the timeline, as for normalize.LocalDate, so the wake date equals sleep_date.
func NightOf(end time.Time, z normalize.Zone, tl normalize.Timeline, anchor time.Duration) (time.Time, error) {
	loc, err := recordLocation(end, z, tl)
	if err != nil {
		return time.Time{}, err
	}
	l := end.In(loc)
	d := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
	tod := time.Duration(l.Hour())*time.Hour + time.Duration(l.Minute())*time.Minute + time.Duration(l.Second())*time.Second
	if tod >= anchor {
		d = d.AddDate(0, 0, 1)
	}
	return d, nil
}

// WithEpisode narrows a local_night window to the night's main episode; Kind, Date and Key stay.
func (w Window) WithEpisode(e Episode) Window {
	// Keep the window's own location (the owner's zone from DayWindows): episode times come from
	// database scans in the process zone, and explanations format clock times in the window's zone.
	loc := w.Start.Location()
	if w.Start.IsZero() {
		loc = w.End.Location()
	}
	w.Start, w.End = e.Start.In(loc), e.End.In(loc)
	return w
}

// Episode is one aligned sleep episode. The J09.6 episode builder produces it from sleep
// sessions; resolution only reads it.
type Episode struct {
	Night    time.Time // night date (NightOf the episode's end), midnight UTC
	Start    time.Time // union span of the sessions
	End      time.Time
	Main     bool // the night's main episode (largest union span); others are secondary
	Sessions []EpisodeSession
}

// EpisodeSession is one source's session inside an episode, after same-source fragment merge.
type EpisodeSession struct {
	IDs        []uuid.UUID // sleep_sessions.id of the merged fragments
	Source     Source
	Start, End time.Time
	IsNap      bool
	HasStages  bool
}

// EpisodeWindow is the sleep_episode window of e.
func EpisodeWindow(e Episode) Window {
	return Window{Kind: catalog.WindowSleepEpisode, Start: e.Start, End: e.End, Date: e.Night, Key: e.Start.UTC().Format(time.RFC3339)}
}

// LatestWindow is the latest window at asOf: inputs at or before it.
func LatestWindow(asOf time.Time) Window {
	return Window{Kind: catalog.WindowLatest, End: asOf, Key: asOf.UTC().Format(time.RFC3339)}
}

// LatestInput returns the most recent input at or before asOf (by Input.At, ties by larger
// ID). Callers pass inputs that already passed the quality gates.
func LatestInput(in []Input, asOf time.Time) (Input, bool) {
	best, ok := Input{}, false
	for _, x := range in {
		if x.At().After(asOf) {
			continue
		}
		if !ok || x.At().After(best.At()) || (x.At().Equal(best.At()) && x.ID > best.ID) {
			best, ok = x, true
		}
	}
	return best, ok
}

// ReadingWindows returns one window per reading (measurement group) in time order. A row
// without a group is a reading of its own.
func ReadingWindows(in []Input) []Window {
	var out []Window
	idx := map[string]int{}
	for _, x := range in {
		k := readingKey(x)
		if i, ok := idx[k]; ok {
			if x.Start.Before(out[i].Start) {
				out[i].Start = x.Start
			}
			continue
		}
		idx[k] = len(out)
		out = append(out, Window{Kind: catalog.WindowReading, Start: x.Start, End: x.Start, Date: x.LocalDate, Key: k})
	}
	slices.SortStableFunc(out, func(a, b Window) int { return a.Start.Compare(b.Start) })
	return out
}

func readingKey(in Input) string {
	if in.GroupID != 0 {
		return "g:" + strconv.FormatInt(in.GroupID, 10)
	}
	return "m:" + strconv.FormatInt(in.ID, 10)
}

func midnightUTC(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// dayStart is local midnight of date in the zone in effect at that midnight. A period that
// starts during the day does not move the day's start.
func dayStart(date time.Time, tl normalize.Timeline) (time.Time, error) {
	return localAt(midnightUTC(date), 0, tl)
}

// localAt is the instant of local time-of-day tod on date, in the zone in effect then. The
// zone is found by a fixpoint: guess with the UTC wall time, then retry once with the zone in
// effect at the guess; the retry wins only if it is self-consistent.
func localAt(date time.Time, tod time.Duration, tl normalize.Timeline) (time.Time, error) {
	wall := midnightUTC(date).Add(tod)
	at := func(loc *time.Location) time.Time {
		return time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, loc)
	}
	loc, err := zoneAt(wall, tl)
	if err != nil {
		return time.Time{}, err
	}
	t := at(loc)
	if loc2, _ := zoneAt(t, tl); loc2 != nil && loc2 != loc {
		t2 := at(loc2)
		if loc3, _ := zoneAt(t2, tl); loc3 == loc2 {
			return t2, nil
		}
	}
	return t, nil
}

func zoneAt(t time.Time, tl normalize.Timeline) (*time.Location, error) {
	name, ok := tl.At(t)
	if !ok {
		return nil, normalize.ErrNoTimezone
	}
	return loadLocation(name)
}

// recordLocation mirrors normalize.LocalDate's precedence: record offset, record zone, periods.
func recordLocation(t time.Time, z normalize.Zone, tl normalize.Timeline) (*time.Location, error) {
	switch {
	case z.OffsetMin != nil:
		off := int(*z.OffsetMin)
		if off < -18*60 || off > 18*60 {
			return nil, fmt.Errorf("%w: offset %d min", normalize.ErrBadTimezone, off)
		}
		return time.FixedZone("", off*60), nil
	case z.TZ != "":
		return loadLocation(z.TZ)
	}
	return zoneAt(t, tl)
}

var locations sync.Map // IANA name -> *time.Location; one pointer per name, so pointers compare

func loadLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("%w: %q", normalize.ErrBadTimezone, name)
	}
	if l, ok := locations.Load(name); ok {
		return l.(*time.Location), nil
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", normalize.ErrBadTimezone, name)
	}
	actual, _ := locations.LoadOrStore(name, l)
	return actual.(*time.Location), nil
}

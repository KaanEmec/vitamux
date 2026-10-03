package resolve

import (
	"errors"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

func date(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func instant(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

var (
	berlin   = normalize.Timeline{{ValidFrom: instant("2020-01-01T00:00:00Z"), TZ: "Europe/Berlin"}}
	traveler = normalize.Timeline{
		{ValidFrom: instant("2020-01-01T00:00:00Z"), TZ: "Europe/Berlin"},
		{ValidFrom: instant("2026-06-10T08:00:00Z"), TZ: "America/New_York"},
	}
)

func TestBucketsAndHoursAcrossDST(t *testing.T) {
	cases := []struct {
		day                  string
		size                 time.Duration
		want                 int
		firstStart, lastStop string
	}{
		{"2026-03-29", 5 * time.Minute, 276, "2026-03-28T23:00:00Z", "2026-03-29T22:00:00Z"},
		{"2026-03-29", time.Hour, 23, "2026-03-28T23:00:00Z", "2026-03-29T22:00:00Z"},
		{"2026-10-25", 5 * time.Minute, 300, "2026-10-24T22:00:00Z", "2026-10-25T23:00:00Z"},
		{"2026-10-25", time.Hour, 25, "2026-10-24T22:00:00Z", "2026-10-25T23:00:00Z"},
		{"2026-10-25", 30 * time.Minute, 50, "2026-10-24T22:00:00Z", "2026-10-25T23:00:00Z"},
		{"2026-07-01", 15 * time.Minute, 96, "2026-06-30T22:00:00Z", "2026-07-01T22:00:00Z"},
		{"2026-07-01", time.Minute, 1440, "2026-06-30T22:00:00Z", "2026-07-01T22:00:00Z"},
	}
	for _, tc := range cases {
		ws, err := Buckets(date(tc.day), tc.size, berlin)
		if err != nil {
			t.Fatal(err)
		}
		if len(ws) != tc.want || !ws[0].Start.Equal(instant(tc.firstStart)) || !ws[len(ws)-1].End.Equal(instant(tc.lastStop)) {
			t.Errorf("%s/%v: %d windows %v..%v, want %d %s..%s", tc.day, tc.size, len(ws), ws[0].Start, ws[len(ws)-1].End, tc.want, tc.firstStart, tc.lastStop)
		}
		loc, _ := time.LoadLocation("Europe/Berlin")
		for _, w := range ws {
			l := w.Start.In(loc)
			if (l.Hour()*60+l.Minute())%int(tc.size.Minutes()) != 0 || !w.Date.Equal(date(tc.day)) {
				t.Fatalf("%s/%v: bucket %v not aligned to the wall clock", tc.day, tc.size, l)
			}
		}
	}
	// After the spring-forward gap the wall clock jumps from 02:00 to 03:00.
	hours, _ := Buckets(date("2026-03-29"), time.Hour, berlin)
	loc, _ := time.LoadLocation("Europe/Berlin")
	if h := hours[2].Start.In(loc).Hour(); h != 3 {
		t.Errorf("third local hour starts at %02d:00, want 03:00", h)
	}
	if hours[0].Kind != catalog.WindowHour || hours[0].Key != "2026-03-28T23:00:00Z" {
		t.Errorf("hour window = %+v", hours[0])
	}
}

func TestDayWindowsDispatch(t *testing.T) {
	ws, err := DayWindows(RuleWindow{Kind: catalog.WindowBucket, Size: "5m"}, date("2026-07-01"), berlin, DefaultNightAnchor)
	if err != nil || len(ws) != 288 || ws[0].Kind != catalog.WindowBucket {
		t.Errorf("bucket: %d windows, %v", len(ws), err)
	}
	ws, err = DayWindows(RuleWindow{Kind: catalog.WindowLocalDay}, date("2026-07-01"), berlin, DefaultNightAnchor)
	if err != nil || len(ws) != 1 || ws[0].Key != "2026-07-01" || ws[0].End.Sub(ws[0].Start) != 24*time.Hour {
		t.Errorf("local_day: %+v, %v", ws, err)
	}
	if _, err := DayWindows(RuleWindow{Kind: catalog.WindowLatest}, date("2026-07-01"), berlin, DefaultNightAnchor); !errors.Is(err, ErrWindowKind) {
		t.Errorf("latest: %v, want ErrWindowKind", err)
	}
	if _, err := DayWindows(RuleWindow{Kind: catalog.WindowHour}, date("2026-07-01"), nil, DefaultNightAnchor); !errors.Is(err, normalize.ErrNoTimezone) {
		t.Errorf("no timeline: %v, want ErrNoTimezone", err)
	}
}

// local_day uses the stored local_date, so a travel day stays whole; its span runs from
// Berlin midnight to New York midnight.
func TestTravelDate(t *testing.T) {
	d, err := LocalDay(date("2026-06-10"), traveler)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Start.Equal(instant("2026-06-09T22:00:00Z")) || !d.End.Equal(instant("2026-06-11T04:00:00Z")) {
		t.Errorf("travel day = %v..%v", d.Start, d.End)
	}
	late := Input{Kind: catalog.Sample, Start: instant("2026-06-11T02:00:00Z"), LocalDate: date("2026-06-10")} // 22:00 in New York
	next := Input{Kind: catalog.Sample, Start: instant("2026-06-11T05:00:00Z"), LocalDate: date("2026-06-11")}
	total := Input{Kind: catalog.DailyValue, Start: instant("2026-06-10T04:00:00Z"), End: instant("2026-06-11T04:00:00Z"), LocalDate: date("2026-06-10")}
	if !d.Includes(late) || d.Includes(next) || !d.Includes(total) {
		t.Error("local_day membership must follow the stored local_date")
	}
	hours, err := Buckets(date("2026-06-10"), time.Hour, traveler)
	if err != nil || len(hours) != 30 {
		t.Errorf("travel day hours = %d, %v", len(hours), err)
	}
	// The day after starts at New York midnight.
	if d2, _ := LocalDay(date("2026-06-11"), traveler); !d2.Start.Equal(instant("2026-06-11T04:00:00Z")) {
		t.Errorf("next day starts %v", d2.Start)
	}
}

func TestNight(t *testing.T) {
	n, err := LocalNight(date("2026-09-15"), DefaultNightAnchor, berlin)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Start.Equal(instant("2026-09-14T16:00:00Z")) || !n.End.Equal(instant("2026-09-15T16:00:00Z")) || n.Key != "2026-09-15" {
		t.Errorf("night = %+v", n)
	}
	// A night over the spring-forward change is 23 hours long.
	if dst, _ := LocalNight(date("2026-03-29"), DefaultNightAnchor, berlin); dst.End.Sub(dst.Start) != 23*time.Hour {
		t.Errorf("DST night = %v", dst.End.Sub(dst.Start))
	}

	cases := []struct {
		name string
		end  string
		zone normalize.Zone
		want string
	}{
		{"night across midnight", "2026-09-15T04:55:00Z", normalize.Zone{}, "2026-09-15"},                         // 06:55 Berlin
		{"afternoon nap", "2026-09-15T15:59:59Z", normalize.Zone{}, "2026-09-15"},                                 // 17:59:59
		{"at the anchor", "2026-09-15T16:00:00Z", normalize.Zone{}, "2026-09-16"},                                 // 18:00
		{"evening nap", "2026-09-15T17:00:00Z", normalize.Zone{}, "2026-09-16"},                                   // 19:00
		{"ends before midnight", "2026-09-15T21:50:00Z", normalize.Zone{}, "2026-09-16"},                          // 23:50
		{"record offset wins", "2026-09-15T20:00:00Z", normalize.Zone{OffsetMin: ptr(int16(-240))}, "2026-09-15"}, // 16:00 at -04:00, 22:00 in Berlin
		{"record zone wins", "2026-09-15T23:00:00Z", normalize.Zone{TZ: "America/New_York"}, "2026-09-16"},        // 19:00 New York
	}
	for _, tc := range cases {
		got, err := NightOf(instant(tc.end), tc.zone, berlin, DefaultNightAnchor)
		if err != nil || !got.Equal(date(tc.want)) {
			t.Errorf("%s: NightOf = %v, %v, want %s", tc.name, got, err, tc.want)
		}
		// The night is the wake date the writer stores as sleep_date, or the day after it.
		if wake, err := normalize.LocalDate(instant(tc.end), tc.zone, berlin); err != nil || (!got.Equal(wake.Date) && !got.Equal(wake.Date.AddDate(0, 0, 1))) {
			t.Errorf("%s: night %v vs wake date %v (%v)", tc.name, got, wake.Date, err)
		}
	}

	ep := Episode{Night: date("2026-09-15"), Start: instant("2026-09-14T21:10:00Z"), End: instant("2026-09-15T04:55:00Z"), Main: true}
	w := n.WithEpisode(ep)
	if !w.Start.Equal(ep.Start) || w.Key != "2026-09-15" || w.Kind != catalog.WindowLocalNight {
		t.Errorf("WithEpisode = %+v", w)
	}
	sample := Input{Kind: catalog.Sample, Start: instant("2026-09-15T00:00:00Z")}
	if !w.Includes(sample) || w.Includes(Input{Kind: catalog.Sample, Start: instant("2026-09-15T06:00:00Z")}) {
		t.Error("night window membership")
	}
	ew := EpisodeWindow(ep)
	if ew.Kind != catalog.WindowSleepEpisode || ew.Key != "2026-09-14T21:10:00Z" || !ew.Date.Equal(ep.Night) {
		t.Errorf("EpisodeWindow = %+v", ew)
	}
}

func TestIncludesInstantWindows(t *testing.T) {
	w := Window{Kind: catalog.WindowHour, Start: instant("2026-07-01T10:00:00Z"), End: instant("2026-07-01T11:00:00Z")}
	cases := []struct {
		name string
		in   Input
		want bool
	}{
		{"sample at start", Input{Kind: catalog.Sample, Start: w.Start}, true},
		{"sample at end", Input{Kind: catalog.Sample, Start: w.End}, false},
		{"interval overlapping the start", Input{Kind: catalog.Interval, Start: instant("2026-07-01T09:55:00Z"), End: instant("2026-07-01T10:05:00Z")}, true},
		{"interval ending at start", Input{Kind: catalog.Interval, Start: instant("2026-07-01T09:55:00Z"), End: w.Start}, false},
		{"zero-length interval inside", Input{Kind: catalog.Interval, Start: instant("2026-07-01T10:30:00Z"), End: instant("2026-07-01T10:30:00Z")}, true},
		{"daily value never in an hour", Input{Kind: catalog.DailyValue, Start: instant("2026-06-30T22:00:00Z"), End: instant("2026-07-01T22:00:00Z")}, false},
	}
	for _, tc := range cases {
		if got := w.Includes(tc.in); got != tc.want {
			t.Errorf("%s: Includes = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLatestAsOf(t *testing.T) {
	in := []Input{
		{ID: 1, Kind: catalog.Sample, Start: instant("2026-09-01T07:00:00Z")},
		{ID: 2, Kind: catalog.Sample, Start: instant("2026-09-03T07:00:00Z")},
		{ID: 3, Kind: catalog.Sample, Start: instant("2026-09-03T07:00:00Z")},
		{ID: 4, Kind: catalog.Interval, Start: instant("2026-09-04T06:00:00Z"), End: instant("2026-09-04T08:00:00Z")},
	}
	cases := []struct {
		asOf string
		want int64
	}{
		{"2026-08-31T00:00:00Z", 0},
		{"2026-09-02T00:00:00Z", 1},
		{"2026-09-03T07:00:00Z", 3}, // inclusive; a tie goes to the larger id
		{"2026-09-04T07:00:00Z", 3}, // the interval ends after as_of
		{"2026-09-04T08:00:00Z", 4},
	}
	for _, tc := range cases {
		got, ok := LatestInput(in, instant(tc.asOf))
		if (tc.want == 0 && ok) || (tc.want != 0 && got.ID != tc.want) {
			t.Errorf("as_of %s: got %d (%v), want %d", tc.asOf, got.ID, ok, tc.want)
		}
	}
	now := instant("2026-09-04T12:00:00Z")
	if w := LatestWindow(now.Add(time.Hour)); !w.Partial(now) || w.Includes(Input{Start: now.Add(2 * time.Hour)}) || !w.Includes(in[3]) {
		t.Error("latest window in the future must be partial and include inputs up to as_of")
	}
	if LatestWindow(now.Add(-time.Hour)).Partial(now) {
		t.Error("latest window in the past is not partial")
	}
}

func TestOpenWindow(t *testing.T) {
	now := instant("2026-07-01T12:00:00Z")
	today, _ := LocalDay(date("2026-07-01"), berlin)
	yesterday, _ := LocalDay(date("2026-06-30"), berlin)
	if !today.Partial(now) || yesterday.Partial(now) {
		t.Error("today is partial, yesterday is not")
	}
	tonight, _ := LocalNight(date("2026-07-01"), DefaultNightAnchor, berlin)
	if !tonight.Partial(now) {
		t.Error("the night of today stays open until the anchor")
	}
}

func TestReadingWindows(t *testing.T) {
	in := []Input{
		{ID: 10, GroupID: 8, Kind: catalog.Sample, Start: instant("2026-09-14T19:00:00Z"), LocalDate: date("2026-09-14")},
		{ID: 11, GroupID: 7, Kind: catalog.Sample, Start: instant("2026-09-14T07:00:00Z"), LocalDate: date("2026-09-14")},
		{ID: 12, GroupID: 7, Kind: catalog.Sample, Start: instant("2026-09-14T07:00:00Z"), LocalDate: date("2026-09-14")},
		{ID: 13, Kind: catalog.Sample, Start: instant("2026-09-14T12:00:00Z"), LocalDate: date("2026-09-14")},
	}
	ws := ReadingWindows(in)
	if len(ws) != 3 || ws[0].Key != "g:7" || ws[1].Key != "m:13" || ws[2].Key != "g:8" {
		t.Fatalf("readings = %+v", ws)
	}
	if !ws[0].Includes(in[1]) || !ws[0].Includes(in[2]) || ws[0].Includes(in[0]) || !ws[1].Includes(in[3]) {
		t.Error("reading membership follows the measurement group")
	}
}

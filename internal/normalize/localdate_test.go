package normalize

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func day(s string) time.Time { return utc(s + "T00:00:00Z") }

func tl(ps ...Period) Timeline {
	for i := range ps {
		ps[i].ID = uuid.New()
	}
	return Timeline(ps)
}

var (
	amsterdam = tl(Period{ValidFrom: utc("2026-01-01T00:00:00Z"), TZ: "Europe/Amsterdam"})
	off       = func(m int16) *int16 { return &m }
)

// hoursPerLocalDate walks UTC hours over [from, to) and counts how many land on each local date.
func hoursPerLocalDate(t *testing.T, z Zone, timeline Timeline, from, to time.Time) map[string]int {
	t.Helper()
	out := map[string]int{}
	for at := from; at.Before(to); at = at.Add(time.Hour) {
		l, err := LocalDate(at, z, timeline)
		if err != nil {
			t.Fatal(err)
		}
		out[l.Date.Format(time.DateOnly)]++
	}
	return out
}

func TestDSTDaysAreNotTwentyFourHours(t *testing.T) {
	// Europe/Amsterdam: clocks go forward on 2026-03-29 (23 h day) and back on 2026-10-25 (25 h day).
	// Walk whole UTC days so the middle local dates are complete.
	for _, tc := range []struct {
		date  string
		hours int
	}{{"2026-03-28", 24}, {"2026-03-29", 23}, {"2026-03-30", 24}, {"2026-10-24", 24}, {"2026-10-25", 25}, {"2026-10-26", 24}} {
		from := day(tc.date).Add(-2 * time.Hour) // covers the whole local date in either offset
		got := hoursPerLocalDate(t, Zone{}, amsterdam, from.Add(-24*time.Hour), from.Add(72*time.Hour))
		if got[tc.date] != tc.hours {
			t.Errorf("%s has %d hours, want %d", tc.date, got[tc.date], tc.hours)
		}
	}
	// The same through a record zone instead of the owner's periods.
	got := hoursPerLocalDate(t, Zone{TZ: "Europe/Amsterdam"}, nil, utc("2026-03-27T00:00:00Z"), utc("2026-03-31T00:00:00Z"))
	if got["2026-03-29"] != 23 {
		t.Errorf("record zone: 2026-03-29 has %d hours", got["2026-03-29"])
	}
}

func TestLocalDateDiffersFromUTCDate(t *testing.T) {
	late := utc("2026-06-15T23:30:00Z")
	for _, tc := range []struct {
		tz   string
		want string
	}{{"Europe/Amsterdam", "2026-06-16"}, {"America/New_York", "2026-06-15"}, {"Asia/Tokyo", "2026-06-16"}, {"UTC", "2026-06-15"}} {
		l, err := LocalDate(late, Zone{}, tl(Period{ValidFrom: utc("2020-01-01T00:00:00Z"), TZ: tc.tz}))
		if err != nil || l.Date.Format(time.DateOnly) != tc.want || l.Basis != BasisPeriod || l.OffsetMin != nil {
			t.Errorf("%s: %+v, %v; want %s from a period with no stored offset", tc.tz, l, err, tc.want)
		}
	}
	if l, _ := LocalDate(late, Zone{}, amsterdam); l.Date.Location() != time.UTC || l.Date.Hour() != 0 {
		t.Errorf("date must be midnight UTC, got %v", l.Date)
	}
}

func TestOffsetPrecedence(t *testing.T) {
	at := utc("2026-06-15T23:30:00Z")
	tokyo := Zone{OffsetMin: off(540), TZ: "America/New_York"}

	// Record offset beats record zone and the owner's period.
	l, err := LocalDate(at, tokyo, amsterdam)
	if err != nil || l.Basis != BasisOffset || l.Date != day("2026-06-16") || *l.OffsetMin != 540 {
		t.Errorf("offset: %+v %v", l, err)
	}
	// A negative offset moves the date back, and works without any period configured.
	l, err = LocalDate(utc("2026-06-16T02:00:00Z"), Zone{OffsetMin: off(-300)}, nil)
	if err != nil || l.Date != day("2026-06-15") {
		t.Errorf("negative offset: %+v %v", l, err)
	}
	// Record zone beats the period, and yields the offset in effect at that instant to store.
	l, err = LocalDate(at, Zone{TZ: "America/New_York"}, amsterdam)
	if err != nil || l.Basis != BasisTZ || l.Date != day("2026-06-15") || *l.OffsetMin != -240 {
		t.Errorf("summer zone: %+v %v", l, err)
	}
	l, _ = LocalDate(utc("2026-01-15T23:30:00Z"), Zone{TZ: "America/New_York"}, amsterdam)
	if *l.OffsetMin != -300 {
		t.Errorf("winter offset = %d, want -300", *l.OffsetMin)
	}
	// Neither: the period decides.
	l, err = LocalDate(at, Zone{}, amsterdam)
	if err != nil || l.Basis != BasisPeriod || l.Date != day("2026-06-16") {
		t.Errorf("period: %+v %v", l, err)
	}
	// Offset 0 is an offset, not "unset".
	l, _ = LocalDate(at, Zone{OffsetMin: off(0)}, amsterdam)
	if l.Basis != BasisOffset || l.Date != day("2026-06-15") {
		t.Errorf("zero offset: %+v", l)
	}
}

func TestLocalDateErrors(t *testing.T) {
	at := utc("2026-06-15T12:00:00Z")
	for name, c := range map[string]struct {
		z   Zone
		tl  Timeline
		err error
	}{
		"no period":        {Zone{}, nil, ErrNoTimezone},
		"offset too big":   {Zone{OffsetMin: off(1081)}, amsterdam, ErrBadTimezone},
		"offset too small": {Zone{OffsetMin: off(-1081)}, amsterdam, ErrBadTimezone},
		"unknown record":   {Zone{TZ: "Mars/Olympus"}, amsterdam, ErrBadTimezone},
		"Local":            {Zone{TZ: "Local"}, amsterdam, ErrBadTimezone},
		"bad period zone":  {Zone{}, tl(Period{ValidFrom: utc("2020-01-01T00:00:00Z"), TZ: "Nope/Nope"}), ErrBadTimezone},
	} {
		if _, err := LocalDate(at, c.z, c.tl); !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", name, err, c.err)
		}
	}
	// The bound itself is valid.
	if _, err := LocalDate(at, Zone{OffsetMin: off(1080)}, nil); err != nil {
		t.Error(err)
	}
}

func TestMidYearTravel(t *testing.T) {
	// Home in Amsterdam, then a trip to Tokyo from 2026-06-10 20:00 UTC to 2026-06-20 00:00 UTC, then home.
	trip := tl(
		Period{ValidFrom: utc("2026-01-01T00:00:00Z"), TZ: "Europe/Amsterdam"},
		Period{ValidFrom: utc("2026-06-10T20:00:00Z"), TZ: "Asia/Tokyo"},
		Period{ValidFrom: utc("2026-06-20T00:00:00Z"), TZ: "Europe/Amsterdam"},
	)
	for _, tc := range []struct{ at, want string }{
		{"2025-12-31T23:30:00Z", "2026-01-01"}, // before the first period: the first zone still applies
		{"2026-06-10T19:59:59Z", "2026-06-10"}, // 21:59 in Amsterdam
		{"2026-06-10T20:00:00Z", "2026-06-11"}, // the period starts here (inclusive): 05:00 in Tokyo
		{"2026-06-19T14:59:59Z", "2026-06-19"}, // 23:59 in Tokyo
		{"2026-06-19T15:00:00Z", "2026-06-20"}, // 00:00 in Tokyo
		{"2026-06-19T23:59:59Z", "2026-06-20"}, // still Tokyo
		{"2026-06-20T00:00:00Z", "2026-06-20"}, // back in Amsterdam: 02:00
		{"2026-12-31T23:30:00Z", "2027-01-01"},
	} {
		l, err := LocalDate(utc(tc.at), Zone{}, trip)
		if err != nil || l.Date != day(tc.want) {
			t.Errorf("%s -> %v (%v), want %s", tc.at, l.Date.Format(time.DateOnly), err, tc.want)
		}
	}
	// Flying east skips 7 hours: 2026-06-10 ends at 22:00 and 2026-06-11 starts at 05:00. Both days
	// are whole and nothing is split, because a date follows the instant, not a boundary stored with the period.
	if got := hoursPerLocalDate(t, Zone{}, trip, utc("2026-06-09T00:00:00Z"), utc("2026-06-12T00:00:00Z")); got["2026-06-10"] != 22 || got["2026-06-11"] != 19 {
		t.Errorf("travel-day hours = %v", got)
	}
}

func TestTimelineAt(t *testing.T) {
	line := tl(
		Period{ValidFrom: utc("2026-03-01T00:00:00Z"), TZ: "A"},
		Period{ValidFrom: utc("2026-05-01T00:00:00Z"), TZ: "B"},
	)
	for _, tc := range []struct{ at, want string }{
		{"2020-01-01T00:00:00Z", "A"}, {"2026-03-01T00:00:00Z", "A"}, {"2026-04-30T23:59:59Z", "A"},
		{"2026-05-01T00:00:00Z", "B"}, {"2030-01-01T00:00:00Z", "B"},
	} {
		if got, ok := line.At(utc(tc.at)); !ok || got != tc.want {
			t.Errorf("At(%s) = %s, want %s", tc.at, got, tc.want)
		}
	}
	if _, ok := (Timeline{}).At(utc("2026-01-01T00:00:00Z")); ok {
		t.Error("an empty timeline has no zone")
	}
}

func TestAffectedRange(t *testing.T) {
	a := Period{ID: uuid.New(), ValidFrom: utc("2026-01-01T00:00:00Z"), TZ: "Europe/Amsterdam"}
	b := Period{ID: uuid.New(), ValidFrom: utc("2026-06-01T00:00:00Z"), TZ: "Asia/Tokyo"}
	c := Period{ID: uuid.New(), ValidFrom: utc("2026-09-01T00:00:00Z"), TZ: "Europe/Amsterdam"}
	base := Timeline{a, c}

	type want struct{ from, to string } // "" = unbounded
	rng := func(r *Range) *want {
		if r == nil {
			return nil
		}
		w := &want{}
		if !r.From.IsZero() {
			w.from = r.From.Format(time.RFC3339)
		}
		if !r.To.IsZero() {
			w.to = r.To.Format(time.RFC3339)
		}
		return w
	}
	for name, tc := range map[string]struct {
		before, after Timeline
		want          *want
	}{
		"insert in the middle covers up to the next period": {base, base.with(b), &want{"2026-06-01T00:00:00Z", "2026-09-01T00:00:00Z"}},
		"insert after the last is open-ended":               {Timeline{a}, Timeline{a}.with(b), &want{"2026-06-01T00:00:00Z", ""}},
		"insert before the first covers all earlier time":   {Timeline{b}, Timeline{b}.with(a), &want{"", "2026-06-01T00:00:00Z"}},
		"remove the middle":                                 {base.with(b), base, &want{"2026-06-01T00:00:00Z", "2026-09-01T00:00:00Z"}},
		"remove the first covers earlier time":              {base.with(b), base.with(b).without(a.ID), &want{"", "2026-06-01T00:00:00Z"}},
		"remove the only period affects everything":         {Timeline{a}, nil, &want{"", ""}},
		"same zone as before changes nothing":               {base, base.with(Period{ID: uuid.New(), ValidFrom: utc("2026-03-01T00:00:00Z"), TZ: "Europe/Amsterdam"}), nil},
		"zone change on one period": {base.with(b), base.with(b).without(b.ID).with(Period{ID: b.ID, ValidFrom: b.ValidFrom, TZ: "America/New_York"}),
			&want{"2026-06-01T00:00:00Z", "2026-09-01T00:00:00Z"}},
		"moving a start later covers the span it gave up": {base.with(b), base.with(b).without(b.ID).with(Period{ID: b.ID, ValidFrom: utc("2026-07-01T00:00:00Z"), TZ: b.TZ}),
			&want{"2026-06-01T00:00:00Z", "2026-07-01T00:00:00Z"}},
		"identical":  {base, base, nil},
		"both empty": {nil, nil, nil},
	} {
		got := rng(affected(tc.before, tc.after))
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
	}
}

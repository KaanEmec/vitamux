package normalize

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	_ "time/tzdata" // zone data in the binary, so images without /usr/share/zoneinfo still work

	"github.com/google/uuid"
)

// Basis says where a local date came from.
type Basis string

const (
	BasisOffset Basis = "record_offset" // the record carried a UTC offset
	BasisTZ     Basis = "record_tz"     // the record carried an IANA zone
	BasisPeriod Basis = "period"        // the owner's timezone_periods
)

// Errors a caller can branch on. Neither is retryable: the record or the owner's setup must change.
var (
	ErrNoTimezone  = errors.New("normalize: no timezone period configured")
	ErrBadTimezone = errors.New("normalize: invalid timezone or offset")
)

// Zone is what a source says about where a record was made. Both fields may be unset.
type Zone struct {
	OffsetMin *int16 `json:"offset_min,omitempty"` // minutes east of UTC; same bounds as the tz_offset_min column
	TZ        string `json:"tz,omitempty"`         // IANA name
}

// Local is a record's calendar date and how it was derived.
type Local struct {
	// Date is the local calendar date as midnight UTC (the form pgx reads and writes for date columns).
	Date time.Time
	// OffsetMin is the value to store in tz_offset_min: the record's own offset, or the offset of
	// its record zone at that instant. It is nil when the date came from a timezone period, so a
	// later period edit can find and recompute exactly those rows.
	OffsetMin *int16
	Basis     Basis
}

const maxOffsetMin = 18 * 60 // the tz_offset_min CHECK

// LocalDate returns the owner's calendar date for instant t. Precedence: the record's offset,
// then the record's IANA zone, then the owner's timezone periods. A bad offset or zone is an
// error rather than a silent fall through to a lower source, so wrong data stays visible.
func LocalDate(t time.Time, z Zone, tl Timeline) (Local, error) {
	switch {
	case z.OffsetMin != nil:
		off := int(*z.OffsetMin)
		if off < -maxOffsetMin || off > maxOffsetMin {
			return Local{}, fmt.Errorf("%w: offset %d min", ErrBadTimezone, off)
		}
		return Local{dateIn(t, time.FixedZone("", off*60)), z.OffsetMin, BasisOffset}, nil
	case z.TZ != "":
		loc, err := location(z.TZ)
		if err != nil {
			return Local{}, err
		}
		_, secs := t.In(loc).Zone()
		off := int16(secs / 60) //nolint:gosec // real zone offsets are within +-18 h
		return Local{dateIn(t, loc), &off, BasisTZ}, nil
	}
	name, ok := tl.At(t)
	if !ok {
		return Local{}, ErrNoTimezone
	}
	loc, err := location(name)
	if err != nil {
		return Local{}, err
	}
	return Local{dateIn(t, loc), nil, BasisPeriod}, nil
}

func dateIn(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

var locations sync.Map // IANA name -> *time.Location

func location(name string) (*time.Location, error) {
	if v, ok := locations.Load(name); ok {
		return v.(*time.Location), nil
	}
	// "Local" and "" would read the server's zone; only named zones are accepted.
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("%w: %q", ErrBadTimezone, name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrBadTimezone, name)
	}
	locations.Store(name, loc)
	return loc, nil
}

// Period is an IANA zone in effect from ValidFrom until the next period starts. Periods never
// overlap by construction: each one ends where the next begins, and (user, valid_from) is unique.
type Period struct {
	ID        uuid.UUID
	ValidFrom time.Time
	TZ        string
}

// Timeline is one owner's periods, sorted by ValidFrom ascending.
type Timeline []Period

// At returns the zone in effect at t. The first period also covers everything before it, so a
// single period describes the owner's whole history. ok is false only for an empty timeline.
func (tl Timeline) At(t time.Time) (name string, ok bool) {
	if len(tl) == 0 {
		return "", false
	}
	i := sort.Search(len(tl), func(i int) bool { return tl[i].ValidFrom.After(t) })
	if i == 0 {
		return tl[0].TZ, true
	}
	return tl[i-1].TZ, true
}

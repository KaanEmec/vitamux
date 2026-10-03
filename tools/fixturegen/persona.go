package main

import "time"

// The persona is a fictional adult in Europe/Berlin with one trip to New York. Every scenario
// below is pinned to a calendar date, so a subset of days contains exactly the scenarios that fall
// inside it. The injector for each one is named after the resolution edge case it exercises
// (docs/architecture/resolution.md#edge-cases); fixtures/README.md has the full map.
const (
	homeTZ = "Europe/Berlin"
	tripTZ = "America/New_York"

	originDate = "2025-01-01" // day index 0; indices, not run positions, key every random stream
)

var (
	tripFrom, tripTo = civil("2025-05-12"), civil("2025-05-21") // inclusive local dates; flights eat the first and last night

	// Garmin watch dead for whole days (gap).
	batteryDead = dates("2025-02-17", "2025-02-18")
	// Garmin watch off 13:00-21:00 local: partial wear, below the coverage threshold.
	partialWear = dates("2025-06-03", "2025-06-04", "2025-06-10")
	// Apple Watch not worn; only the iPhone sees steps.
	phoneOnly = dates("2025-08-04", "2025-08-05", "2025-08-06", "2025-08-07", "2025-08-08", "2025-08-09", "2025-08-10")
	// Garmin splits the night around a wake of 75 min (separate episodes) or 35 min (merge).
	splitLong  = dates("2025-03-12", "2025-05-27", "2025-09-09", "2025-11-18")
	splitShort = dates("2025-04-08", "2025-07-22", "2025-10-14")
	// Garmin daily step totals corrected two days later.
	stepCorrections = dates("2025-04-14", "2025-04-15", "2025-04-16")
	// Upstream deletes the evening BP reading / the weigh-in; one BP systolic value is corrected.
	deleteBP, deleteWeighIn, correctBP = civil("2025-07-09"), civil("2025-09-03"), civil("2025-02-11")
)

func civil(s string) int {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return dayIndex(t)
}

func dates(ss ...string) map[int]bool {
	m := make(map[int]bool, len(ss))
	for _, s := range ss {
		m[civil(s)] = true
	}
	return m
}

// dayIndex numbers civil dates from originDate.
func dayIndex(t time.Time) int {
	o, _ := time.Parse("2006-01-02", originDate)
	return int(t.Sub(o).Hours() / 24)
}

func dayDate(idx int) time.Time {
	o, _ := time.Parse("2006-01-02", originDate)
	return o.AddDate(0, 0, idx)
}

func onTrip(idx int) bool { return idx >= tripFrom && idx <= tripTo }

var homeLoc, tripLoc = mustLoad(homeTZ), mustLoad(tripTZ)

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func zoneOf(idx int) *time.Location {
	if onTrip(idx) {
		return tripLoc
	}
	return homeLoc
}

// flightDay: the first and last trip day change timezone, so there is no night to record.
func flightDay(idx int) bool { return idx == tripFrom || idx == tripTo+1 }

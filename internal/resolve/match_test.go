package resolve

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

var (
	connA   = uuid.MustParse("0190a6a0-0000-7000-8000-00000000000a")
	connB   = uuid.MustParse("0190a6a0-0000-7000-8000-00000000000b")
	devWear = uuid.MustParse("0190a6a0-0000-7000-8000-0000000000d1")

	appleWatch   = Source{Provider: "apple_health", ConnectionID: connA, OriginKey: "com.apple.health.watch", OriginName: "Watch", DeviceID: devWear, DeviceType: "watch", DeviceModel: "Watch7,1"}
	iPhone       = Source{Provider: "apple_health", ConnectionID: connA, OriginKey: "com.apple.health.phone", OriginName: "iPhone", DeviceType: "phone"}
	relayGarmin  = Source{Provider: "apple_health", ConnectionID: connA, OriginKey: "com.garmin.connect.mobile", OriginName: "Connect", Relayed: true, DeviceType: "watch"}
	manualEntry  = Source{Provider: "apple_health", ConnectionID: connA, OriginKey: "com.apple.Health", Manual: true}
	withingsCuff = Source{Provider: "withings", ConnectionID: connB, DeviceType: "bp_monitor"}
	oura         = Source{Provider: "oura", ConnectionID: connB, DeviceType: "ring"}
)

func TestSelectorMatches(t *testing.T) {
	cases := []struct {
		name string
		sel  Selector
		src  Source
		want bool
	}{
		{"provider", Selector{Provider: "apple_health"}, iPhone, true},
		{"provider differs", Selector{Provider: "withings"}, iPhone, false},
		{"fields are ANDed", Selector{Provider: "apple_health", DeviceType: "watch"}, iPhone, false},
		{"connection", Selector{ConnectionID: connB.String()}, withingsCuff, true},
		{"connection differs", Selector{ConnectionID: connB.String()}, iPhone, false},
		{"origin key", Selector{OriginKey: "com.garmin.connect.mobile"}, relayGarmin, true},
		{"origin key is exact", Selector{OriginKey: "com.garmin"}, relayGarmin, false},
		{"origin prefix", Selector{OriginKeyPrefix: "com.apple.health."}, appleWatch, true},
		{"origin prefix needs an origin", Selector{OriginKeyPrefix: "c"}, withingsCuff, false},
		{"origin name", Selector{OriginName: "iPhone"}, iPhone, true},
		{"relayed true", Selector{Relayed: new(true)}, relayGarmin, true},
		{"relayed false", Selector{Relayed: new(false)}, relayGarmin, false},
		{"relayed false matches direct", Selector{Relayed: new(false)}, appleWatch, true},
		{"device model", Selector{DeviceModel: "Watch7,1"}, appleWatch, true},
		{"device id", Selector{DeviceID: devWear.String()}, appleWatch, true},
		{"device id needs a device", Selector{DeviceID: devWear.String()}, iPhone, false},
		{"entry manual", Selector{Entry: EntryManual}, manualEntry, true},
		{"entry device", Selector{Entry: EntryDevice}, manualEntry, false},
		{"entry device matches devices", Selector{Entry: EntryDevice}, appleWatch, true},
	}
	for _, tc := range cases {
		if got := tc.sel.Matches(tc.src); got != tc.want {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAssign(t *testing.T) {
	// Overlapping selectors: apple_any also matches the Watch, but apple_watch comes first.
	r := &Rule{
		Groups: []Group{
			{ID: "apple_watch", Match: []Selector{{Provider: "apple_health", DeviceType: "watch"}}},
			{ID: "cuff", Match: []Selector{{DeviceType: "bp_monitor"}}},
			{ID: "apple_any", Match: []Selector{{Provider: "apple_health"}}},
		},
		Exclude: []Selector{{Entry: EntryManual}, {Provider: "apple_health", Relayed: new(true)}},
	}
	cases := []struct {
		name    string
		src     Source
		group   int
		exclude int
		m       Membership
	}{
		{"watch takes the first matching group", appleWatch, 0, -1, Grouped},
		{"iPhone falls to the broader group", iPhone, 2, -1, Grouped},
		// The relayed Garmin origin matches apple_watch, but exclusions win over every group.
		{"relayed origin is excluded", relayGarmin, -1, 1, Excluded},
		{"manual entry is excluded", manualEntry, -1, 0, Excluded},
		{"direct provider", withingsCuff, 1, -1, Grouped},
		{"unmatched source", oura, -1, -1, NotInRule},
	}
	for _, tc := range cases {
		a := r.Assign(tc.src)
		if a.Group != tc.group || a.Exclude != tc.exclude || a.Membership() != tc.m {
			t.Errorf("%s: Assign = %+v (%s), want group %d exclude %d (%s)", tc.name, a, a.Membership(), tc.group, tc.exclude, tc.m)
		}
	}

	in := []Input{{ID: 1, Source: iPhone}, {ID: 2, Source: appleWatch}, {ID: 3, Source: relayGarmin}, {ID: 4, Source: oura}, {ID: 5, Source: appleWatch}, {ID: 6, Source: manualEntry}}
	p := r.Partition(in)
	ids := func(xs []Input) []int64 {
		var out []int64
		for _, x := range xs {
			out = append(out, x.ID)
		}
		return out
	}
	if got := ids(p.Groups[0]); !slices.Equal(got, []int64{2, 5}) {
		t.Errorf("apple_watch = %v", got)
	}
	if got := ids(p.Groups[2]); !slices.Equal(got, []int64{1}) {
		t.Errorf("apple_any = %v", got)
	}
	if len(p.Groups[1]) != 0 {
		t.Errorf("cuff = %v", ids(p.Groups[1]))
	}
	if got := ids(p.Excluded); !slices.Equal(got, []int64{3, 6}) {
		t.Errorf("excluded = %v", got)
	}
	if got := ids(p.NotInRule); !slices.Equal(got, []int64{4}) {
		t.Errorf("not_in_rule = %v", got)
	}
}

func TestLadder(t *testing.T) {
	r := &Rule{
		Groups: []Group{{ID: "watch"}, {ID: "garmin"}, {ID: "strap"}, {ID: "ring"}},
		Contexts: map[Context][]string{
			ContextWorkout: {"strap", ContextWorkoutSource},
			ContextSleep:   {"ring"},
		},
	}
	cases := []struct {
		name    string
		ctx     Context
		workout int
		want    []int
	}{
		{"default order", "", -1, []int{0, 1, 2, 3}},
		{"sleep context", ContextSleep, -1, []int{3, 0, 1, 2}},
		{"workout recorded by garmin", ContextWorkout, 1, []int{2, 1, 0, 3}},
		{"workout recorded by the strap itself", ContextWorkout, 2, []int{2, 0, 1, 3}},
		{"workout source unknown", ContextWorkout, -1, []int{2, 0, 1, 3}},
	}
	for _, tc := range cases {
		if got := r.Ladder(tc.ctx, tc.workout); !slices.Equal(got, tc.want) {
			t.Errorf("%s: Ladder = %v, want %v", tc.name, got, tc.want)
		}
	}
}

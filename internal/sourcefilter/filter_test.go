package sourcefilter

import (
	"errors"
	"slices"
	"testing"
)

const (
	hr    = "HKQuantityTypeIdentifierHeartRate"
	rhr   = "HKQuantityTypeIdentifierRestingHeartRate"
	steps = "HKQuantityTypeIdentifierStepCount"
)

// Synthetic bundle ids; com.whoop.iphone is the seeded relay origin of the direct WHOOP connector.
var whoop = Default{Pattern: "com.whoop.iphone", Provider: "whoop", ProviderName: "WHOOP"}

func TestNormalize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      []Choice
		want    []Choice
		pointer string
	}{
		{name: "take drops types", in: []Choice{{BundleID: "com.example.a", Name: " A ", Mode: Take, Types: []string{hr}}},
			want: []Choice{{BundleID: "com.example.a", Name: "A", Mode: Take}}},
		{name: "per type sorted and deduplicated", in: []Choice{{BundleID: "com.example.a", Mode: PerType, Types: []string{steps, hr, steps}}},
			want: []Choice{{BundleID: "com.example.a", Mode: PerType, Types: []string{hr, steps}}}},
		{name: "per type without types", in: []Choice{{BundleID: "com.example.a", Mode: PerType}}, pointer: "/origins/0/types"},
		{name: "unknown mode", in: []Choice{{BundleID: "com.example.a", Mode: "skip"}}, pointer: "/origins/0/mode"},
		{name: "bad type", in: []Choice{{BundleID: "com.example.a", Mode: PerType, Types: []string{"heart"}}}, pointer: "/origins/0/types/0"},
		{name: "bad bundle id", in: []Choice{{BundleID: "com example", Mode: Take}}, pointer: "/origins/0/bundle_id"},
		{name: "listed twice", in: []Choice{{BundleID: "com.example.a", Mode: Take}, {BundleID: "com.example.a", Mode: Ignore}}, pointer: "/origins/1/bundle_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Normalize(tt.in)
			var fe *FieldError
			switch {
			case tt.pointer != "":
				if !errors.As(err, &fe) || fe.Pointer != tt.pointer {
					t.Fatalf("err %v, want a field error at %s", err, tt.pointer)
				}
			case err != nil:
				t.Fatal(err)
			case !slices.EqualFunc(got, tt.want, func(a, b Choice) bool {
				return a.BundleID == b.BundleID && a.Name == b.Name && a.Mode == b.Mode && slices.Equal(a.Types, b.Types)
			}):
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFilter_Decide(t *testing.T) {
	t.Parallel()
	f := Filter{
		Choices: []Choice{
			{BundleID: "com.example.scale", Mode: Ignore},
			{BundleID: "com.example.ring", Mode: PerType, Types: []string{hr}},
		},
		Defaults: []Default{whoop, {Pattern: "com.apple.%", Provider: "other", ProviderName: "Other"}},
	}
	tests := []struct {
		name     string
		bundle   string
		mode     Mode
		explicit bool
		reason   Reason
		takesHR  bool
	}{
		{name: "unknown app is taken", bundle: "com.example.unknown", mode: Take, takesHR: true},
		{name: "apple source is native and taken even when a pattern matches", bundle: "com.apple.health.00000000-0000-4000-8000-000000000001",
			mode: Take, reason: ReasonNative, takesHR: true},
		{name: "relay of a direct connection is ignored by default", bundle: "com.whoop.iphone", mode: Ignore, reason: ReasonDirect},
		{name: "watch extension follows its parent's default", bundle: "com.whoop.iphone.watchkitapp", mode: Ignore, reason: ReasonDirect},
		{name: "explicit ignore", bundle: "com.example.scale", mode: Ignore, explicit: true},
		{name: "watch extension follows its parent's choice", bundle: "com.example.scale.watchkitapp.watchkitextension", mode: Ignore, explicit: true},
		{name: "per type takes the listed type", bundle: "com.example.ring", mode: PerType, explicit: true, takesHR: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := f.Decide(tt.bundle)
			if d.Mode != tt.mode || d.Explicit != tt.explicit || d.Reason != tt.reason || d.Takes(hr) != tt.takesHR {
				t.Fatalf("Decide(%s) = %+v", tt.bundle, d)
			}
		})
	}
	if d := f.Decide("com.whoop.iphone"); d.Provider != "whoop" || d.ProviderName != "WHOOP" || d.DefaultMode != Ignore {
		t.Fatalf("the default names the direct provider: %+v", d)
	}
	if f.Decide("com.example.ring").Takes(steps) {
		t.Fatal("per type ignores the types it does not list")
	}
	explicitTake := Filter{Choices: []Choice{{BundleID: "com.whoop.iphone", Mode: Take}}, Defaults: []Default{whoop}}
	if d := explicitTake.Decide("com.whoop.iphone"); d.Mode != Take || !d.Explicit || d.DefaultMode != Ignore || d.Reason != ReasonDirect {
		t.Fatalf("an explicit choice wins over the default, which is still reported: %+v", d)
	}
}

func TestParent(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"com.example.app.watchkitapp":                   "com.example.app",
		"com.example.app.watchkitapp.watchkitextension": "com.example.app",
		"com.example.app.watchkitextension":             "com.example.app",
		"com.example.app":                               "",
		".watchkitapp":                                  "",
	} {
		if got := Parent(in); got != want {
			t.Errorf("Parent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLike(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"com.whoop.iphone", "com.whoop.iphone", true},
		{"com.whoop.iphone", "com.whoop.iphone2", false},
		{"com.garmin.%", "com.garmin.connect.mobile", true},
		{"com.garmin.%", "com.garmin", false},
		{"%", "", true},
		{"a_c", "abc", true},
		{"a_c", "ac", false},
		{`a\_c`, "abc", false},
		{`a\_c`, "a_c", true},
		{`a\%`, "a%", true},
		{"%.iphone", "com.whoop.iphone", true},
	}
	for _, tt := range tests {
		if got := Like(tt.pattern, tt.s); got != tt.want {
			t.Errorf("Like(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()
	before := Filter{Defaults: []Default{whoop}}
	writes := map[string][]string{"com.whoop.iphone": {hr, rhr}, "com.example.scale": {steps}}
	bundles := []string{"com.example.new", "com.example.scale", "com.whoop.iphone"}

	t.Run("ignored by default to taken pulls the types it writes", func(t *testing.T) {
		t.Parallel()
		after := Filter{Defaults: before.Defaults, Choices: []Choice{{BundleID: "com.whoop.iphone", Mode: Take}}}
		got := Diff(before, after, bundles, writes)
		if len(got) != 1 || got[0].BundleID != "com.whoop.iphone" || got[0].From != Ignore || got[0].To != Take ||
			!slices.Equal(got[0].Pulled, []string{hr, rhr}) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("taking per type pulls only those types", func(t *testing.T) {
		t.Parallel()
		after := Filter{Defaults: before.Defaults, Choices: []Choice{{BundleID: "com.whoop.iphone", Mode: PerType, Types: []string{rhr}}}}
		got := Diff(before, after, bundles, writes)
		if len(got) != 1 || !slices.Equal(got[0].Pulled, []string{rhr}) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("ignoring pulls nothing", func(t *testing.T) {
		t.Parallel()
		after := Filter{Defaults: before.Defaults, Choices: []Choice{{BundleID: "com.example.scale", Mode: Ignore}}}
		got := Diff(before, after, bundles, writes)
		if len(got) != 1 || got[0].To != Ignore || len(got[0].Pulled) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("an explicit choice equal to the default changes nothing", func(t *testing.T) {
		t.Parallel()
		after := Filter{Defaults: before.Defaults, Choices: []Choice{{BundleID: "com.whoop.iphone", Mode: Ignore}}}
		if got := Diff(before, after, bundles, writes); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("an app whose types are unknown pulls every type", func(t *testing.T) {
		t.Parallel()
		from := Filter{Choices: []Choice{{BundleID: "com.example.new", Mode: Ignore}}}
		got := Diff(from, Filter{}, bundles, writes)
		if len(got) != 1 || !slices.Equal(got[0].Pulled, []string{"*"}) {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestStored_Marshal(t *testing.T) {
	t.Parallel()
	b, err := Stored{}.Marshal()
	if err != nil || string(b) != `{"origins":[]}` {
		t.Fatalf("%s %v", b, err)
	}
	s, err := Parse(b)
	if err != nil || len(s.Origins) != 0 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := Parse([]byte(`{"origins": 1}`)); err == nil {
		t.Fatal("a malformed filter must not parse")
	}
}

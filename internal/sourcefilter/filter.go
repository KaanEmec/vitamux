// Package sourcefilter decides which origin apps' data a paired Apple Health device takes
// (J22.25, docs/architecture/apple-health.md#source-filter). The owner's explicit choices are
// stored on the device (clients.source_filter); the defaults are evaluated on every read:
// Apple's own sources and unknown apps are taken, and a known relay origin of a provider the
// owner connects directly is ignored, so its copy in Apple Health is not counted twice. The
// phone applies the same rules (HealthBridgeKit's SourceFilter); the server applies them again
// to anything that still arrives (normalize.DropOrigins).
package sourcefilter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mode is what a device does with an origin's data.
type Mode string

const (
	Take    Mode = "take"
	Ignore  Mode = "ignore"
	PerType Mode = "per_type" // only Choice.Types are taken
)

// Reason explains a default.
type Reason string

const (
	ReasonNone   Reason = ""
	ReasonNative Reason = "native"            // Apple's own sources are always taken by default
	ReasonDirect Reason = "direct_connection" // the provider it relays is connected directly
)

// Limits of a stored filter.
const (
	MaxChoices  = 200
	MaxTypes    = 64
	maxBundleID = 255
	maxName     = 200
)

var (
	hkType   = regexp.MustCompile(`^HK[A-Za-z0-9]{1,126}$`)
	bundleID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Choice is the owner's explicit setting for one origin app (a HealthKit bundle id).
type Choice struct {
	BundleID string   `json:"bundle_id"`
	Name     string   `json:"name,omitempty"`
	Mode     Mode     `json:"mode"`
	Types    []string `json:"types,omitempty"` // PerType: the HealthKit types taken
}

// Stored is the JSON of clients.source_filter.
type Stored struct {
	Origins []Choice `json:"origins"`
}

// Parse reads clients.source_filter; an empty value is an empty filter.
func Parse(raw []byte) (Stored, error) {
	var s Stored
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Stored{}, fmt.Errorf("sourcefilter: %w", err)
	}
	return s, nil
}

// Marshal renders s for clients.source_filter, origins never null.
func (s Stored) Marshal() ([]byte, error) {
	if s.Origins == nil {
		s.Origins = []Choice{}
	}
	return json.Marshal(s)
}

// FieldError names the invalid part of a choice list by JSON pointer below /origins.
type FieldError struct {
	Pointer, Detail string
}

func (e *FieldError) Error() string { return e.Pointer + ": " + e.Detail }

// Normalize validates choices and returns them cleaned: names trimmed, per-type lists sorted and
// deduplicated, types dropped unless the mode is per_type. A bundle id may appear once.
func Normalize(choices []Choice) ([]Choice, error) {
	if len(choices) > MaxChoices {
		return nil, &FieldError{"/origins", fmt.Sprintf("at most %d", MaxChoices)}
	}
	out := make([]Choice, 0, len(choices))
	seen := map[string]bool{}
	for i, c := range choices {
		at := fmt.Sprintf("/origins/%d", i)
		if len(c.BundleID) > maxBundleID || !bundleID.MatchString(c.BundleID) {
			return nil, &FieldError{at + "/bundle_id", "not a bundle id"}
		}
		if seen[c.BundleID] {
			return nil, &FieldError{at + "/bundle_id", "listed twice"}
		}
		seen[c.BundleID] = true
		c.Name = strings.TrimSpace(c.Name)
		if utf8.RuneCountInString(c.Name) > maxName || strings.ContainsFunc(c.Name, unicode.IsControl) {
			return nil, &FieldError{at + "/name", "at most 200 characters without control characters"}
		}
		switch c.Mode {
		case Take, Ignore:
			c.Types = nil
		case PerType:
			if len(c.Types) == 0 || len(c.Types) > MaxTypes {
				return nil, &FieldError{at + "/types", fmt.Sprintf("1 to %d HealthKit types", MaxTypes)}
			}
			for j, t := range c.Types {
				if !hkType.MatchString(t) {
					return nil, &FieldError{fmt.Sprintf("%s/types/%d", at, j), "not a HealthKit type identifier"}
				}
			}
			c.Types = slices.Compact(slices.Sorted(slices.Values(c.Types)))
		default:
			return nil, &FieldError{at + "/mode", "take, ignore or per_type"}
		}
		out = append(out, c)
	}
	return out, nil
}

// Default is a pattern of origins ignored by default because the provider they relay is
// connected directly. Pattern is a SQL LIKE pattern (backslash escapes), as in known_relay_origins.
type Default struct {
	Pattern      string `json:"origin_pattern"`
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
}

// Filter is a device's filter as it applies now.
type Filter struct {
	Version  int32
	Choices  []Choice
	Defaults []Default
}

// Decision is what a filter does with one origin.
type Decision struct {
	Mode     Mode
	Types    []string // PerType: the types taken
	Explicit bool     // the owner chose it; defaults never change it
	// The default, and why it is what it is.
	DefaultMode  Mode
	Reason       Reason
	Provider     string // ReasonDirect: the provider connected directly
	ProviderName string
}

// Takes reports whether the decision takes data of HealthKit type typ.
func (d Decision) Takes(typ string) bool {
	switch d.Mode {
	case Ignore:
		return false
	case PerType:
		return slices.Contains(d.Types, typ)
	case Take:
	}
	return true
}

// Decide returns the decision for an origin. A Watch-side extension (bundle id of its parent
// app plus a watchkitapp or watchkitextension suffix) follows its parent's choice and default
// when it has none of its own. Unknown origins are taken.
func (f Filter) Decide(bundle string) Decision {
	d := f.defaultFor(bundle)
	for _, id := range candidates(bundle) {
		if i := slices.IndexFunc(f.Choices, func(c Choice) bool { return c.BundleID == id }); i >= 0 {
			c := f.Choices[i]
			d.Mode, d.Types, d.Explicit = c.Mode, c.Types, true
			return d
		}
	}
	d.Mode = d.DefaultMode
	return d
}

func (f Filter) defaultFor(bundle string) Decision {
	if Native(bundle) {
		return Decision{DefaultMode: Take, Reason: ReasonNative}
	}
	for _, id := range candidates(bundle) {
		for _, p := range f.Defaults {
			if Like(p.Pattern, id) {
				return Decision{DefaultMode: Ignore, Reason: ReasonDirect, Provider: p.Provider, ProviderName: p.ProviderName}
			}
		}
	}
	return Decision{DefaultMode: Take}
}

// candidates is the bundle id, then its parent app's when it is a Watch-side extension.
func candidates(bundle string) []string {
	if p := Parent(bundle); p != "" {
		return []string{bundle, p}
	}
	return []string{bundle}
}

// watchSuffixes are the bundle id suffixes of Watch apps and extensions, longest first.
var watchSuffixes = []string{".watchkitapp.watchkitextension", ".watchkitextension", ".watchkitapp", ".watchextension", ".watchapp"}

// Parent returns the parent app's bundle id of a Watch-side extension, or "" for any other id.
// HealthKit reports no parent, so this follows Apple's naming convention for embedded Watch apps.
func Parent(bundle string) string {
	for _, s := range watchSuffixes {
		if p, ok := strings.CutSuffix(bundle, s); ok && p != "" {
			return p
		}
	}
	return ""
}

// Native reports Apple's own sources: the per-device com.apple.health.<UUID> sources of the
// iPhone and Watch, and Apple's apps.
func Native(bundle string) bool {
	return strings.HasPrefix(bundle, "com.apple.")
}

// Like matches s against a SQL LIKE pattern: % any run, _ any one character, backslash escapes.
func Like(pattern, s string) bool {
	p, r := []rune(pattern), []rune(s)
	var match func(i, j int) bool
	match = func(i, j int) bool {
		for i < len(p) {
			switch p[i] {
			case '%':
				for i < len(p) && p[i] == '%' {
					i++
				}
				if i == len(p) {
					return true
				}
				for k := j; k <= len(r); k++ {
					if match(i, k) {
						return true
					}
				}
				return false
			case '_':
				if j >= len(r) {
					return false
				}
			case '\\':
				if i+1 < len(p) {
					i++
				}
				fallthrough
			default:
				if j >= len(r) || r[j] != p[i] {
					return false
				}
			}
			i++
			j++
		}
		return j == len(r)
	}
	return match(0, 0)
}

// Transition is a change of what a device takes from one origin.
type Transition struct {
	BundleID string `json:"bundle_id"`
	From     Mode   `json:"from"`
	To       Mode   `json:"to"`
	// Pulled are the types newly taken, so their history must be read again ("*": every type the
	// origin writes, when the device has not reported them).
	Pulled []string `json:"pulled,omitempty"`
}

// Diff lists the origins whose decision changes from before to after, over bundles. writes
// gives the types each origin was seen writing; Pulled is computed from them.
func Diff(before, after Filter, bundles []string, writes map[string][]string) []Transition {
	var out []Transition
	for _, b := range bundles {
		x, y := before.Decide(b), after.Decide(b)
		if x.Mode == y.Mode && slices.Equal(x.Types, y.Types) {
			continue
		}
		t := Transition{BundleID: b, From: x.Mode, To: y.Mode}
		known := slices.Concat(writes[b], x.Types, y.Types)
		slices.Sort(known)
		for _, typ := range slices.Compact(known) {
			if !x.Takes(typ) && y.Takes(typ) {
				t.Pulled = append(t.Pulled, typ)
			}
		}
		if y.Mode == Take && x.Mode != Take && len(writes[b]) == 0 {
			t.Pulled = []string{"*"}
		}
		out = append(out, t)
	}
	return out
}

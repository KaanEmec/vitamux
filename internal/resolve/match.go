package resolve

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/normalize"
)

// Source is the provenance identity selectors match on: one (provider, connection, origin,
// device, entry) combination. It is comparable, so callers can assign once per source.
type Source struct {
	Provider     string
	ConnectionID uuid.UUID
	OriginKey    string // data_origins.origin_key, "" when the row has no origin
	OriginName   string
	Relayed      bool      // the origin relays another vendor (data_origins.relayed_provider_id)
	DeviceID     uuid.UUID // uuid.Nil when the row has no device
	DeviceType   string
	DeviceModel  string
	Manual       bool // provider "manual" or the manual_entry quality flag
}

// Input is one canonical row as resolution sees it. J09.4 fills it from active measurements.
type Input struct {
	ID        int64 // measurements.id, for record_refs
	Source    Source
	Kind      catalog.Kind
	Start     time.Time
	End       time.Time // zero for samples
	LocalDate time.Time // stored local_date, midnight UTC
	Value     float64   // canonical unit
	Flags     normalize.Flags
	GroupID   int64 // measurement_groups.id; 0 when the row is not part of a reading
}

// At is the instant used by latest windows: the end of an interval, else the start.
func (in Input) At() time.Time {
	if in.End.IsZero() {
		return in.Start
	}
	return in.End
}

// Matches reports whether every set field of s equals src. origin_key_prefix is a plain
// prefix; all comparisons are exact. A field set on s never matches an empty field of src.
func (s Selector) Matches(src Source) bool {
	switch {
	case s.Provider != "" && s.Provider != src.Provider,
		s.ConnectionID != "" && s.ConnectionID != src.ConnectionID.String(),
		s.OriginKey != "" && s.OriginKey != src.OriginKey,
		s.OriginKeyPrefix != "" && (src.OriginKey == "" || !strings.HasPrefix(src.OriginKey, s.OriginKeyPrefix)),
		s.OriginName != "" && s.OriginName != src.OriginName,
		s.Relayed != nil && *s.Relayed != src.Relayed,
		s.DeviceType != "" && s.DeviceType != src.DeviceType,
		s.DeviceModel != "" && s.DeviceModel != src.DeviceModel,
		s.DeviceID != "" && (src.DeviceID == uuid.Nil || s.DeviceID != src.DeviceID.String()),
		s.Entry == EntryManual && !src.Manual,
		s.Entry == EntryDevice && src.Manual:
		return false
	}
	return true
}

func anyMatch(sels []Selector, src Source) int {
	for i, s := range sels {
		if s.Matches(src) {
			return i
		}
	}
	return -1
}

// Membership is how an input relates to a rule.
type Membership string

const (
	Grouped   Membership = "grouped"
	Excluded  Membership = "excluded"    // matched an exclude selector; wins over every group
	NotInRule Membership = "not_in_rule" // matched no group
)

// Assignment is where one source lands under a rule.
type Assignment struct {
	Group   int // index into Rule.Groups; -1 unless Grouped
	Exclude int // index into Rule.Exclude of the first matching exclusion; -1 when none
}

// Membership derives the input's status from the assignment.
func (a Assignment) Membership() Membership {
	switch {
	case a.Exclude >= 0:
		return Excluded
	case a.Group >= 0:
		return Grouped
	}
	return NotInRule
}

// Assign places a source: exclusions first, then the first group (in rule order) with a
// matching selector. Membership never depends on contexts; see Ladder.
func (r *Rule) Assign(src Source) Assignment {
	if i := anyMatch(r.Exclude, src); i >= 0 {
		return Assignment{Group: -1, Exclude: i}
	}
	for i, g := range r.Groups {
		if anyMatch(g.Match, src) >= 0 {
			return Assignment{Group: i, Exclude: -1}
		}
	}
	return Assignment{Group: -1, Exclude: -1}
}

// Partition is a rule's inputs split by membership; nothing is dropped, so the all-sources
// view can list excluded and unmatched rows.
type Partition struct {
	Groups    [][]Input // indexed like Rule.Groups
	Excluded  []Input
	NotInRule []Input
}

// Partition assigns every input, keeping input order within each part.
func (r *Rule) Partition(in []Input) Partition {
	p := Partition{Groups: make([][]Input, len(r.Groups))}
	seen := map[Source]Assignment{}
	for _, x := range in {
		a, ok := seen[x.Source]
		if !ok {
			a = r.Assign(x.Source)
			seen[x.Source] = a
		}
		switch a.Membership() {
		case Excluded:
			p.Excluded = append(p.Excluded, x)
		case NotInRule:
			p.NotInRule = append(p.NotInRule, x)
		case Grouped:
			p.Groups[a.Group] = append(p.Groups[a.Group], x)
		}
	}
	return p
}

// Ladder returns group indices in priority order. With a context (E1) that the rule lists,
// the listed groups come first, then the rest in rule order; ctx "" is the rule order.
// workoutGroup is the group of the source that recorded the workout (-1 when unknown) and
// replaces ContextWorkoutSource.
func (r *Rule) Ladder(ctx Context, workoutGroup int) []int {
	out := make([]int, 0, len(r.Groups))
	used := make([]bool, len(r.Groups))
	add := func(i int) {
		if i >= 0 && i < len(r.Groups) && !used[i] {
			used[i] = true
			out = append(out, i)
		}
	}
	for _, id := range r.Contexts[ctx] {
		if id == ContextWorkoutSource {
			add(workoutGroup)
			continue
		}
		add(r.groupIndex(id))
	}
	for i := range r.Groups {
		add(i)
	}
	return out
}

func (r *Rule) groupIndex(id string) int {
	for i, g := range r.Groups {
		if g.ID == id {
			return i
		}
	}
	return -1
}

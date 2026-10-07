package resolve

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
)

// WorkoutMatchOverlap is the share of the shorter workout two workouts must overlap to cluster
// (docs/architecture/resolution.md#sleep-episode-alignment).
const WorkoutMatchOverlap = 0.6

// SportOther is the generic canonical sport; it is compatible with every sport.
const SportOther = "other"

// WorkoutInput is one active workout with its segments. J09.8 fills it from workouts and
// workout_segments; this package does no I/O.
type WorkoutInput struct {
	ID         uuid.UUID
	Source     Source
	Start, End time.Time
	Sport      string // canonical sport
	DistanceM  *float64
	EnergyKcal *float64
	AvgHRBpm   *float64
	MaxHRBpm   *float64
	Segments   []WorkoutSegment // in seq order
}

// WorkoutSegment is one workout_segments row.
type WorkoutSegment struct {
	Seq   int32
	Kind  string // lap, set or interval
	Start time.Time
	End   time.Time // zero when open
	Data  json.RawMessage
}

// WorkoutCluster is one activity as recorded by one or more sources.
type WorkoutCluster struct {
	Start, End time.Time      // union span
	Workouts   []WorkoutInput // start order
}

// ClusterWorkouts links two workouts when they overlap by at least WorkoutMatchOverlap of the
// shorter one and their sports are compatible (equal, or one is SportOther). Connected
// workouts form one cluster. Links are taken best overlap first, and a cluster holds at most
// one sport besides SportOther, so a generic workout joins the activity it overlaps most and
// never bridges a run and a walk. Clusters come in start order.
func ClusterWorkouts(in []WorkoutInput) []WorkoutCluster {
	ws := slices.Clone(in)
	slices.SortStableFunc(ws, func(a, b WorkoutInput) int { return a.Start.Compare(b.Start) })
	root := make([]int, len(ws))
	sport := make([]string, len(ws)) // per root: the cluster's specific sport, else SportOther
	for i := range root {
		root[i], sport[i] = i, ws[i].Sport
	}
	find := func(i int) int {
		for root[i] != i {
			root[i] = root[root[i]]
			i = root[i]
		}
		return i
	}
	type link struct {
		i, j  int
		ratio float64
	}
	var links []link
	for i := range ws {
		for j := i + 1; j < len(ws); j++ {
			if r := overlapRatio(ws[i].Start, ws[i].End, ws[j].Start, ws[j].End); r >= WorkoutMatchOverlap {
				links = append(links, link{i, j, r})
			}
		}
	}
	// Best matches first, so a generic workout joins the activity it overlaps most.
	slices.SortStableFunc(links, func(a, b link) int { return cmp.Compare(b.ratio, a.ratio) })
	for _, l := range links {
		ri, rj := find(l.i), find(l.j)
		if ri == rj || !sportsCompatible(sport[ri], sport[rj]) {
			continue
		}
		root[rj] = ri
		if sport[ri] == SportOther {
			sport[ri] = sport[rj]
		}
	}
	idx := map[int]int{}
	var out []WorkoutCluster
	for i, w := range ws {
		k := find(i)
		n, ok := idx[k]
		if !ok {
			n = len(out)
			idx[k] = n
			out = append(out, WorkoutCluster{Start: w.Start, End: w.End})
		}
		c := &out[n]
		c.End = timeMax(c.End, w.End)
		c.Workouts = append(c.Workouts, w)
	}
	return out
}

func sportsCompatible(a, b string) bool { return a == b || a == SportOther || b == SportOther }

// WorkoutPick is the workout a rule selects from a cluster. The workout keeps its own
// segments; nothing is spliced from the alternates.
type WorkoutPick struct {
	Workout    WorkoutInput
	Group      int            // index into Rule.Groups
	Alternates []WorkoutInput // the cluster's other workouts, in start order
}

// PickWorkout selects the workout of the first group in rule order (event_priority) that
// recorded one in the cluster; within a group the longest wins, then the earliest. Excluded
// and unmatched workouts are never picked but stay listed as alternates. ok is false when no
// workout of the cluster is in a group.
func (r *Rule) PickWorkout(c WorkoutCluster) (WorkoutPick, bool) {
	best, bestGroup := -1, 0
	for i, w := range c.Workouts {
		g := r.Assign(w.Source).Group
		if g < 0 {
			continue
		}
		if best < 0 || g < bestGroup ||
			g == bestGroup && w.End.Sub(w.Start) > c.Workouts[best].End.Sub(c.Workouts[best].Start) {
			best, bestGroup = i, g
		}
	}
	if best < 0 {
		return WorkoutPick{}, false
	}
	p := WorkoutPick{Workout: c.Workouts[best], Group: bestGroup}
	for i, w := range c.Workouts {
		if i != best {
			p.Alternates = append(p.Alternates, w)
		}
	}
	return p, true
}

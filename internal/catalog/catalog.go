package catalog

import (
	"slices"
	"time"
)

// Kind is a measurement kind; the values are the CHECK values of measurements.kind.
type Kind string

const (
	Sample     Kind = "sample"
	Interval   Kind = "interval"
	Cumulative Kind = "cumulative"
	DailyValue Kind = "daily_value"
)

// Aggregation is the within-source aggregation of docs/architecture/resolution.md#within-source-aggregation.
type Aggregation string

const (
	Intensive    Aggregation = "intensive"
	Additive     Aggregation = "additive"
	Latest       Aggregation = "latest"
	DailySummary Aggregation = "daily_summary"
	SleepDerived Aggregation = "sleep_derived"
)

// Window is a window kind of docs/architecture/resolution.md#windows.
type Window string

const (
	WindowBucket       Window = "bucket"
	WindowHour         Window = "hour"
	WindowLocalDay     Window = "local_day"
	WindowLocalNight   Window = "local_night"
	WindowSleepEpisode Window = "sleep_episode"
	WindowLatest       Window = "latest"
	WindowReading      Window = "reading"
)

// Strategy is a cross-source strategy of docs/architecture/resolution.md#strategies.
type Strategy string

const (
	SingleSource   Strategy = "single_source"
	FirstAvailable Strategy = "first_available"
	Mean           Strategy = "mean"
	Min            Strategy = "min"
	Max            Strategy = "max"
	Sum            Strategy = "sum"
	LatestOf       Strategy = "latest"
	Earliest       Strategy = "earliest"
	EventPriority  Strategy = "event_priority"
)

// Metric is one catalogue entry. Only Code and Unit reach the database; the rest is resolution
// metadata owned by code. HK and Withings are documentation only: the normalizers own the real mappings.
type Metric struct {
	Code    string
	Section string      // grouping in docs/metrics.md
	Unit    string      // canonical unit code
	Kinds   []Kind      // kinds a source may store; none for values derived from sleep sessions
	Agg     Aggregation // within-source aggregation
	// Min and Max bound a plausible value in the canonical unit; outside it a row gets the
	// implausible quality flag and the resolution quality gate rejects it.
	Min, Max float64
	// Group is the measurement_groups.kind whose reading this metric belongs to, if any.
	Group string
	// ProviderScoped marks a provider-namespaced score (<provider>_<name>): never pooled across providers.
	ProviderScoped bool
	// SelectionOnly marks a metric that providers define differently (metric-catalog.md#rules):
	// rules select one source and never pool it.
	SelectionOnly bool
	// DerivedFrom names the source metric of a derived code (rule extension E2). A derived code
	// has no rows of its own: resolution computes it from the source metric's series with a
	// window statistic, so it has no Kinds and only the night windows.
	DerivedFrom string
	// Unresolved marks a raw series (rr_interval): stored and drawn like other metrics, but no
	// rule resolves it, so it has no windows or strategies.
	Unresolved bool
	HK         string // HealthKit identifier without prefix
	Withings   string // meastype
	Since      int    // seed migration marker (SeedV1 when 0); a code added later takes a new one
}

// BaseBucket is the bucket size for within-source aggregation: 5 minutes for sample and
// interval series, zero where no bucketing applies (latest, daily summaries, sleep).
func (m Metric) BaseBucket() time.Duration {
	if m.Agg == Intensive || m.Agg == Additive {
		return 5 * time.Minute
	}
	return 0
}

// Intraday is the bucket ladder of a metric's day view (J22.26): Default for a 24-hour span,
// Finest the smallest step a client may zoom to. Values are bucket sizes (30s, 1m, 5m, 15m,
// 30m) or raw for the stored samples.
type Intraday struct{ Default, Finest string }

// dense lists the intensive codes sent seconds apart. Other intensive codes arrive a minute or
// more apart (SpO2, respiration, temperature, HRV, stress, glucose, gait), so their view starts coarser.
var dense = []string{"heart_rate", "physical_effort", "power_cycling", "power_running", "cadence_cycling",
	"speed_cycling", "speed_running", "speed_rowing", "speed_paddle", "speed_xc_ski",
	"running_ground_contact_time", "running_stride_length", "running_vertical_oscillation"}

// Intraday returns the day-view ladder by aggregation class; ok is false for metrics measured
// once a day or night (daily summaries, latest readings, sleep and derived night codes).
func (m Metric) Intraday() (in Intraday, ok bool) {
	switch {
	case m.DerivedFrom != "":
		return Intraday{}, false
	case m.Agg == Intensive && slices.Contains(dense, m.Code):
		return Intraday{Default: "1m", Finest: "raw"}, true
	case m.Agg == Intensive:
		return Intraday{Default: "5m", Finest: "raw"}, true
	case m.Agg == Additive:
		return Intraday{Default: "30m", Finest: "1m"}, true
	}
	return Intraday{}, false
}

// Windows lists the window kinds a rule may use for this metric.
func (m Metric) Windows() []Window {
	if m.Unresolved {
		return nil
	}
	if m.DerivedFrom != "" {
		return []Window{WindowLocalNight, WindowSleepEpisode}
	}
	var w []Window
	switch m.Agg {
	case Intensive:
		w = []Window{WindowBucket, WindowHour, WindowLocalDay, WindowLocalNight, WindowSleepEpisode, WindowLatest}
	case Additive:
		w = []Window{WindowBucket, WindowHour, WindowLocalDay}
	case Latest:
		w = []Window{WindowLocalDay, WindowLatest}
	case DailySummary:
		w = []Window{WindowLocalDay, WindowLatest}
	case SleepDerived:
		w = []Window{WindowLocalNight, WindowSleepEpisode}
	}
	if m.Group != "" {
		w = append(w, WindowReading)
	}
	return w
}

// Strategies lists the cross-source strategies a rule may use for this metric. Sum needs
// additive data (and the rule must still acknowledge the duplicate risk); mean, min and max
// are rejected for provider-scoped scores and selection-only metrics; event_priority applies to sleep episodes only.
func (m Metric) Strategies() []Strategy {
	if m.Unresolved {
		return nil
	}
	s := []Strategy{SingleSource, FirstAvailable}
	if m.Poolable() {
		s = append(s, Mean, Min, Max)
	}
	if m.Agg == Additive {
		s = append(s, Sum)
	}
	s = append(s, LatestOf, Earliest)
	if m.Agg == SleepDerived {
		s = append(s, EventPriority)
	}
	return s
}

// Poolable reports whether values from different providers may be combined (mean, min, max).
func (m Metric) Poolable() bool { return !m.ProviderScoped && !m.SelectionOnly }

// AllowsWindow and AllowsStrategy are the checks J09 rule validation calls.
func (m Metric) AllowsWindow(w Window) bool     { return slices.Contains(m.Windows(), w) }
func (m Metric) AllowsStrategy(s Strategy) bool { return slices.Contains(m.Strategies(), s) }

// Lookup finds a metric by code.
func Lookup(code string) (Metric, bool) {
	m, ok := byCode[code]
	return m, ok
}

// Metrics returns all metrics in seed order.
func Metrics() []Metric { return slices.Clone(metrics) }

// Combinable reports whether two metric codes may feed one rule. Sources combine only when they
// share a code, so SDNN and RMSSD (or any two methods of one quantity) never do.
func Combinable(a, b string) bool {
	_, ok := byCode[a]
	return ok && a == b
}

var byCode = func() map[string]Metric {
	m := make(map[string]Metric, len(metrics))
	for _, x := range metrics {
		m[x.Code] = x
	}
	return m
}()

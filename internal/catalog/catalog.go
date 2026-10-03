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
	HK             string // HealthKit identifier without prefix
	Withings       string // meastype
}

// BaseBucket is the bucket size for within-source aggregation: 5 minutes for sample and
// interval series, zero where no bucketing applies (latest, daily summaries, sleep).
func (m Metric) BaseBucket() time.Duration {
	if m.Agg == Intensive || m.Agg == Additive {
		return 5 * time.Minute
	}
	return 0
}

// Windows lists the window kinds a rule may use for this metric.
func (m Metric) Windows() []Window {
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
// are rejected for provider-scoped scores; event_priority applies to sleep episodes only.
func (m Metric) Strategies() []Strategy {
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
func (m Metric) Poolable() bool { return !m.ProviderScoped }

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

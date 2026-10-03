package catalog

import "fmt"

// Unit converts to its base unit with base = value*Factor + Offset. Units that share a Base are
// convertible; a base unit has Base == Code and Factor 1. Canonical units are chosen per metric
// (Metric.Unit), so a base unit is not necessarily the canonical one (hrv_sdnn is in ms, base s).
type Unit struct {
	Code   string
	Base   string
	Factor float64
	Offset float64
}

// units lists every unit the catalogue knows: the canonical ones and the source units that
// normalizers convert from. Order is the seed order. Only units with a conversion carry Base data.
var units = []Unit{
	base("count"),
	base("s"), {"ms", "s", 0.001, 0}, {"min", "s", 60, 0}, {"h", "s", 3600, 0},
	base("m"), {"km", "m", 1000, 0}, {"cm", "m", 0.01, 0}, {"mi", "m", 1609.344, 0}, {"ft", "m", 0.3048, 0}, {"in", "m", 0.0254, 0},
	base("kcal"), {"kJ", "kcal", 1 / 4.184, 0},
	base("kg"), {"g", "kg", 0.001, 0}, {"lb", "kg", 0.45359237, 0}, {"oz", "kg", 0.028349523125, 0}, {"st", "kg", 6.35029318, 0},
	base("bpm"),
	base("mL/kg/min"),
	base("m/s"), {"km/h", "m/s", 1 / 3.6, 0}, {"mph", "m/s", 0.44704, 0},
	base("years"),
	base("mmHg"), {"kPa", "mmHg", 1000 / 133.322387415, 0},
	base("%"), {"fraction", "%", 100, 0},
	base("breaths/min"),
	base("°C"), {"°F", "°C", 5.0 / 9, -160.0 / 9},
	base("index"),
	base("kcal/day"),
}

func base(code string) Unit { return Unit{code, code, 1, 0} }

var unitByCode = func() map[string]Unit {
	m := make(map[string]Unit, len(units))
	for _, u := range units {
		m[u.Code] = u
	}
	return m
}()

// Units returns all units in seed order.
func Units() []Unit { return append([]Unit(nil), units...) }

// LookupUnit finds a unit by code.
func LookupUnit(code string) (Unit, bool) {
	u, ok := unitByCode[code]
	return u, ok
}

// Convert converts v from one unit to another of the same base. Identity (from == to) returns v
// unchanged, so callers can tell from the unit codes whether source_value must be kept.
func Convert(v float64, from, to string) (float64, error) {
	if from == to {
		if _, ok := unitByCode[from]; !ok {
			return 0, fmt.Errorf("catalog: unknown unit %q", from)
		}
		return v, nil
	}
	f, ok := unitByCode[from]
	if !ok {
		return 0, fmt.Errorf("catalog: unknown unit %q", from)
	}
	t, ok := unitByCode[to]
	if !ok {
		return 0, fmt.Errorf("catalog: unknown unit %q", to)
	}
	if f.Base != t.Base {
		return 0, fmt.Errorf("catalog: cannot convert %s to %s", from, to)
	}
	return ((v*f.Factor + f.Offset) - t.Offset) / t.Factor, nil
}

// ToCanonical converts v, expressed in unit, to the canonical unit of the metric. The bool is
// true when the value changed unit, in which case the writer keeps source_value and source_unit_id.
func ToCanonical(code string, v float64, unit string) (float64, bool, error) {
	m, ok := Lookup(code)
	if !ok {
		return 0, false, fmt.Errorf("catalog: unknown metric %q", code)
	}
	out, err := Convert(v, unit, m.Unit)
	return out, unit != m.Unit, err
}

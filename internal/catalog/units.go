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
	Since  int // seed migration marker (SeedV1 when 0); a unit added later takes a new one
}

// units lists every unit the catalogue knows: the canonical ones and the source units that
// normalizers convert from. Order is the seed order. Only units with a conversion carry Base data.
var units = []Unit{
	base("count"),
	base("s"), conv("ms", "s", 0.001, 0), conv("min", "s", 60, 0), conv("h", "s", 3600, 0),
	base("m"), conv("km", "m", 1000, 0), conv("cm", "m", 0.01, 0), conv("mi", "m", 1609.344, 0), conv("ft", "m", 0.3048, 0), conv("in", "m", 0.0254, 0),
	base("kcal"), conv("kJ", "kcal", 1/4.184, 0),
	base("kg"), conv("g", "kg", 0.001, 0), conv("lb", "kg", 0.45359237, 0), conv("oz", "kg", 0.028349523125, 0), conv("st", "kg", 6.35029318, 0),
	base("bpm"),
	base("mL/kg/min"),
	base("m/s"), conv("km/h", "m/s", 1/3.6, 0), conv("mph", "m/s", 0.44704, 0),
	base("years"),
	base("mmHg"), conv("kPa", "mmHg", 1000/133.322387415, 0),
	base("%"), conv("fraction", "%", 100, 0),
	base("breaths/min"),
	base("°C"), conv("°F", "°C", 5.0/9, -160.0/9),
	base("index"),
	base("kcal/day"),

	// E15 (J15.2). A mass concentration converts to mmol/L only per substance, so glucose has its own unit.
	hk(base("events/h")), hk(base("kg/m²")), hk(base("mmol/L")), hk(conv("mg/dL glucose", "mmol/L", 10/180.156, 0)),
	hk(conv("mg", "kg", 1e-6, 0)), hk(base("L")), hk(conv("mL", "L", 0.001, 0)),

	// E25 (J25.1): units of the Apple Health activity, audio and insulin types and of the Withings skin conductance.
	mapped(base("W")), mapped(base("rpm")), mapped(base("kcal/kg/h")), mapped(base("dBA")), mapped(base("dB")),
	mapped(base("L/min")), mapped(base("IU")), mapped(base("µS")),

	// J25.5: running cadence.
	{Code: "steps/min", Base: "steps/min", Factor: 1, Since: SeedGarminAct},
	// J25.2: units of the Withings U-Scan.
	withings(base("pH")), withings(base("ratio")), withings(base("mmol/mmol")), withings(conv("µmol/L", "mmol/L", 0.001, 0)),
}

func hk(u Unit) Unit { u.Since = SeedHealthKit; return u }

func mapped(u Unit) Unit { u.Since = SeedMappings; return u }

func withings(u Unit) Unit { u.Since = SeedWithings; return u }

func base(code string) Unit { return Unit{Code: code, Base: code, Factor: 1} }

func conv(code, base string, factor, offset float64) Unit {
	return Unit{Code: code, Base: base, Factor: factor, Offset: offset}
}

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

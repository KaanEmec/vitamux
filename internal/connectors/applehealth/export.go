package applehealth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/normalize"
)

// The Health app's export (export.xml) is the fallback path (docs/architecture/apple-health.md#export-importer-fallback).
// internal/imports stores its records as found, one page per run of records of one type, and
// this normalizer turns each record into the sample shape the app sends, so the mapping above
// is shared. Exports carry no UUIDs: a record's id is a hash of its natural key (ExportID).
const (
	StreamExport       = "apple_health.export.v1"
	ExportNormalizerID = "apple_health.export"

	// ExportOriginPrefix marks origins named by an export's sourceName: exports carry no bundle id.
	ExportOriginPrefix = "export:"
	exportTimeLayout   = "2006-01-02 15:04:05 -0700"
)

// SleepType's records group into sessions within one page, so the importer cuts its pages only
// where a record starts more than SleepGap after the end of those before it.
const (
	SleepType = typeSleep
	SleepGap  = sleepGap
)

// ExportSpan returns the record's start and end; zero when they do not parse.
func ExportSpan(r ExportRecord) (start, end time.Time) {
	start, _ = time.Parse(exportTimeLayout, r.Attrs["startDate"])
	end, _ = time.Parse(exportTimeLayout, r.Attrs["endDate"])
	return start, end
}

// ExportPage is the raw body of StreamExport: records of one type with their attributes, metadata
// entries and children as found. Overlapping holds the records that matched app-synced data
// when the page was imported; they are kept here but not normalized (their ids are tombstoned,
// so a record that only later turned out to overlap is withdrawn).
type ExportPage struct {
	Type        string         `json:"type"`
	Records     []ExportRecord `json:"records"`
	Overlapping []ExportRecord `json:"overlapping,omitempty"`
}

// ExportRecord is one Record, Correlation or Workout element.
type ExportRecord struct {
	Attrs      map[string]string   `json:"attrs"`
	Metadata   map[string]string   `json:"metadata,omitempty"`   // MetadataEntry key → value
	Records    []ExportRecord      `json:"records,omitempty"`    // a correlation's member records
	Statistics []map[string]string `json:"statistics,omitempty"` // a workout's WorkoutStatistics attributes
}

// ExportNormalizer normalizes StreamExport pages through the healthkit.samples mapping.
type ExportNormalizer struct{}

func (ExportNormalizer) ID() string                    { return ExportNormalizerID }
func (ExportNormalizer) Version() int                  { return 1 }
func (ExportNormalizer) Accepts(stream, _ string) bool { return stream == StreamExport }

func (ExportNormalizer) Normalize(_ context.Context, raw normalize.RawPayload, _ normalize.Env) (normalize.Output, error) {
	var p ExportPage
	if err := json.Unmarshal(raw.Body, &p); err != nil || p.Type == "" {
		return normalize.Output{}, errUnreadable
	}
	out := ExportOutput(p.Type, p.Records)
	for _, r := range p.Overlapping {
		out.Tombstones = append(out.Tombstones, normalize.Key{RecordType: p.Type, ExternalID: strings.ToUpper(ExportID(p.Type, r))})
	}
	return out, nil
}

// ExportOutput maps export records of type typ as the app's samples would be. A record repeated
// in the export (same id) counts once. The importer calls it per record for its overlap check.
func ExportOutput(typ string, recs []ExportRecord) normalize.Output {
	p := page{Type: typ}
	seen := map[string]bool{}
	for _, r := range recs {
		s := exportSample(typ, r)
		if !seen[s.UUID] {
			seen[s.UUID] = true
			p.Samples = append(p.Samples, s)
		}
	}
	return normalizePage(p)
}

// ExportID is the record's id: 32 hex digits of SHA-256 over its natural key (type, start, end,
// value, sourceName, device). Instants are compared, not their spelling, and the device's
// object address is dropped, so a later export of the same record has the same id; a value
// exported in another unit (the owner changed preferred units) does not.
func ExportID(typ string, r ExportRecord) string {
	a := r.Attrs
	value := a["value"] + " " + a["unit"]
	switch {
	case typ == typeWorkout:
		value = a["workoutActivityType"] + " " + a["duration"] + " " + a["durationUnit"]
	case len(r.Records) > 0:
		members := make([]string, len(r.Records))
		for i, m := range r.Records {
			members[i] = m.Attrs["type"] + "=" + m.Attrs["value"] + " " + m.Attrs["unit"]
		}
		slices.Sort(members)
		value = strings.Join(members, ",")
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{typ, instant(a["startDate"]), instant(a["endDate"]),
		value, a["sourceName"], deviceAddr.ReplaceAllString(a["device"], "")}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

var deviceAddr = regexp.MustCompile(`0x[0-9A-Fa-f]+`)

func instant(s string) string {
	t, err := time.Parse(exportTimeLayout, s)
	if err != nil {
		return s
	}
	return strconv.FormatInt(t.UnixMicro(), 10)
}

// exportSample converts one record. What does not convert stays unset, so the shared mapping
// warns about it as it would for an app sample.
func exportSample(typ string, r ExportRecord) sample {
	a := r.Attrs
	s := sample{UUID: ExportID(typ, r), Device: exportDevice(a["device"])}
	s.Start, s.End = ExportSpan(r)
	if name := a["sourceName"]; name != "" {
		s.Source = source{BundleID: ExportOriginPrefix + name, Name: name}
	}
	if len(r.Metadata) > 0 {
		s.Metadata = make(map[string]json.RawMessage, len(r.Metadata))
		for k, v := range r.Metadata {
			s.Metadata[k] = metadataValue(v)
		}
		s.WasUserEntered = r.Metadata["HKWasUserEntered"] == "1"
	}
	q, isQuantity := quantities[typ]
	switch {
	case isQuantity:
		s.Value, s.Unit = convert(number(a["value"]), a["unit"], q.hkUnit)
	case typ == typeInsulin:
		s.Value, s.Unit = convert(number(a["value"]), a["unit"], "IU")
	case typ == typeWorkout:
		s.Workout = exportWorkout(r)
	case len(r.Records) > 0:
		for _, m := range r.Records {
			v, unit := number(m.Attrs["value"]), m.Attrs["unit"]
			if m.Attrs["type"] == typeHeartRate {
				v, unit = convert(v, unit, "count/min")
			}
			s.Objects = append(s.Objects, member{Type: m.Attrs["type"], Value: v, Unit: unit})
		}
	case strings.HasPrefix(typ, hkCategory):
		if c, ok := categoryValues[a["value"]]; ok {
			f := float64(c)
			s.Value = &f
		} else {
			s.Value = number(a["value"])
		}
	}
	return s
}

func exportWorkout(r ExportRecord) *workoutInfo {
	w := &workoutInfo{ActivityType: activityTypes[r.Attrs["workoutActivityType"]], Totals: map[string]float64{}}
	if v, unit := convert(number(r.Attrs["duration"]), r.Attrs["durationUnit"], "s"); v != nil && unit == "s" {
		w.DurationS = *v
	}
	total := func(id, sum, unit, want string) {
		if v, u := convert(number(sum), unit, want); v != nil && u == want {
			w.Totals[id] = *v
		}
	}
	for _, st := range r.Statistics {
		switch id := st["type"]; {
		case slices.Contains(workoutDistances, id):
			total(id, st["sum"], st["unit"], "m")
		case id == hkQuantity+"ActiveEnergyBurned":
			total(id, st["sum"], st["unit"], "kcal")
		}
	}
	// Exports before iOS 16 carry totals as attributes only; the distance's type is not given.
	if len(w.Totals) == 0 {
		total(hkQuantity+"DistanceWalkingRunning", r.Attrs["totalDistance"], r.Attrs["totalDistanceUnit"], "m")
		total(hkQuantity+"ActiveEnergyBurned", r.Attrs["totalEnergyBurned"], r.Attrs["totalEnergyBurnedUnit"], "kcal")
	}
	return w
}

// metadataValue keeps a JSON number literal as a number (the app sends numbers) and anything
// else as a string, without reformatting either.
func metadataValue(v string) json.RawMessage {
	if jsonNumber.MatchString(v) {
		return json.RawMessage(v)
	}
	b, _ := json.Marshal(v)
	return b
}

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,15})(\.[0-9]{1,15})?$`)

func number(s string) *float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !finite(f) {
		return nil
	}
	return &f
}

// unitFactors converts the units an export writes (the owner's preferred units) into the units
// of the type registry: value × factor.
var unitFactors = map[[2]string]float64{
	{"km", "m"}: 1000, {"cm", "m"}: 0.01, {"mm", "m"}: 0.001, {"mi", "m"}: 1609.344, {"yd", "m"}: 0.9144,
	{"ft", "m"}: 0.3048, {"in", "m"}: 0.0254,
	{"lb", "kg"}: 0.45359237, {"g", "kg"}: 0.001, {"st", "kg"}: 6.35029318,
	{"min", "s"}: 60, {"hr", "s"}: 3600, {"ms", "s"}: 0.001,
	{"Cal", "kcal"}: 1, {"kJ", "kcal"}: 1 / 4.184, {"cal", "kcal"}: 0.001,
	{"mL/min·kg", "ml/kg*min"}: 1, {"mL/kg·min", "ml/kg*min"}: 1, {"count/s", "count/min"}: 60,
	{"L", "mL"}: 1000, {"fl_oz_us", "mL"}: 29.5735295625, {"fl_oz_imp", "mL"}: 28.4130625,
	{"mcg", "mg"}: 0.001, {"g", "mg"}: 1000, {"mg", "g"}: 0.001,
}

var mmolPerL = regexp.MustCompile(`^mmol<([0-9]+(?:\.[0-9]+)?)>/L$`)

// convert returns v in unit to, or v unchanged in its own unit when no conversion is known.
func convert(v *float64, from, to string) (*float64, string) {
	if v == nil || from == to {
		return v, from
	}
	x := *v
	switch f, ok := unitFactors[[2]string{from, to}]; {
	case ok:
		x *= f
	case from == "degF" && to == "degC":
		x = (x - 32) * 5 / 9
	case to == "mg/dL" && mmolPerL.MatchString(from):
		mass, _ := strconv.ParseFloat(mmolPerL.FindStringSubmatch(from)[1], 64)
		x *= mass / 10
	default:
		return v, from
	}
	if !finite(x) || math.Abs(x) > math.MaxFloat32 {
		return nil, from
	}
	return &x, to
}

// deviceField finds the fields of an HKDevice description such as
// "<<HKDevice: 0x1>, name:Apple Watch, manufacturer:Apple Inc., model:Watch, hardware:Watch6,1, software:9.1>".
var deviceField = regexp.MustCompile(`(?:^|, )(name|manufacturer|model|hardware|software|firmware|localIdentifier|UDIDeviceIdentifier|FDAUDI):`)

func exportDevice(desc string) *hkDevice {
	desc = strings.TrimSuffix(strings.TrimSpace(desc), ">")
	if i := strings.Index(desc, ">,"); strings.HasPrefix(desc, "<<HKDevice") && i >= 0 {
		desc = desc[i+1:]
	}
	locs := deviceField.FindAllStringSubmatchIndex(desc, -1)
	fields := map[string]string{}
	for i, l := range locs {
		end := len(desc)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		fields[desc[l[2]:l[3]]] = strings.TrimSpace(desc[l[1]:end])
	}
	d := hkDevice{Name: fields["name"], Manufacturer: fields["manufacturer"], Model: fields["model"],
		HardwareVersion: fields["hardware"], SoftwareVersion: fields["software"]}
	if d == (hkDevice{}) {
		return nil
	}
	return &d
}

// categoryValues maps the case names an export writes to HKCategoryValue raw values.
var categoryValues = map[string]int{
	"HKCategoryValueNotApplicable":                                 0,
	"HKCategoryValueSleepAnalysisInBed":                            0,
	"HKCategoryValueSleepAnalysisAsleep":                           1,
	"HKCategoryValueSleepAnalysisAsleepUnspecified":                1,
	"HKCategoryValueSleepAnalysisAwake":                            2,
	"HKCategoryValueSleepAnalysisAsleepCore":                       3,
	"HKCategoryValueSleepAnalysisAsleepDeep":                       4,
	"HKCategoryValueSleepAnalysisAsleepREM":                        5,
	"HKCategoryValueAppleStandHourStood":                           0,
	"HKCategoryValueAppleStandHourIdle":                            1,
	"HKCategoryValueAppleWalkingSteadinessEventInitialLow":         1,
	"HKCategoryValueAppleWalkingSteadinessEventInitialVeryLow":     2,
	"HKCategoryValueAppleWalkingSteadinessEventRepeatLow":          3,
	"HKCategoryValueAppleWalkingSteadinessEventRepeatVeryLow":      4,
	"HKCategoryValueEnvironmentalAudioExposureEventMomentaryLimit": 1,
	"HKCategoryValueHeadphoneAudioExposureEventSevenDayLimit":      1,
}

// activityTypes maps HKWorkoutActivityType case names as exported (HKWorkoutActivityTypeRunning)
// to raw values; an unknown name becomes 0, which sport reports as unknown.
var activityTypes = func() map[string]int {
	m := make(map[string]int, len(sports))
	for v, name := range sports {
		m["HKWorkoutActivityType"+strings.ToUpper(name[:1])+name[1:]] = v
	}
	return m
}()

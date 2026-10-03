package main

import (
	"time"
)

// Num is a decimal as an integer and a count of decimals, so output never depends on float formatting.
type Num struct {
	V   int64
	Dec uint8
}

func n0(v int) Num { return Num{int64(v), 0} }
func n1(v int) Num { return Num{int64(v), 1} } // v is in tenths

// Quality flags, named as in measurements.quality_flags (data-model.md#measurements).
const (
	flagManual  uint8 = 1
	flagRelayed uint8 = 2
)

// Source is one device or app stream: a (provider, device, origin) triple. Key is stable and is
// how records refer to it.
type Source struct {
	Key, Provider, DeviceType, Fingerprint, Model string
	OriginKey, RelayedProvider                    string // origin only for apple_health streams
}

type Measurement struct {
	Src        *Source
	Metric     string
	Kind       string // sample | interval | daily_value
	Start, End time.Time
	Offset     int // minutes east of UTC at Start
	Date       string
	Val        Num
	Ext        string // upstream id, when the source has one
	Flags      uint8
}

// Group is a reading taken together (bp_reading, body_composition); parts are its measurements.
type Group struct {
	Src     *Source
	Kind    string
	At      time.Time
	Offset  int
	Date    string
	Ext     string
	Context string // JSON object literal
	Parts   []Part
}

type Part struct {
	Metric string
	Val    Num
}

type Stage struct {
	Name       string // awake | light | deep | rem | asleep_unspecified
	Start, End time.Time
}

// Sleep is one session; the *S totals are seconds, -1 when the source does not report them.
type Sleep struct {
	Src                                        *Source
	Ext                                        string
	Start, End                                 time.Time
	Offset                                     int
	Date                                       string // local date of waking up
	Nap, HasStages                             bool
	Basis                                      string // provider | stages
	AsleepS, DeepS, LightS, REMS, AwakeS, LatS int
	Stages                                     []Stage
}

// Revision is a later upstream correction or deletion of an earlier record. The original stays
// in the output; At is when the source issued the change.
type Revision struct {
	Op     string // correct | delete
	At     time.Time
	Record string // measurement | group
	Src    *Source
	Metric string // measurement, or the component of a group correction
	Ext    string
	Val    Num // correct only
}

var (
	garminWatch = &Source{Key: "garmin_watch", Provider: "garmin", DeviceType: "watch", Fingerprint: "synthetic-garmin-watch-01", Model: "Synthetic Band H"}
	appleWatch  = &Source{Key: "apple_watch", Provider: "apple_health", DeviceType: "watch", Fingerprint: "synthetic-apple-watch-01", Model: "Synthetic Watch", OriginKey: "com.apple.health.synthetic-watch"}
	iphone      = &Source{Key: "iphone", Provider: "apple_health", DeviceType: "phone", Fingerprint: "synthetic-iphone-01", Model: "Synthetic Phone", OriginKey: "com.apple.health.synthetic-phone"}
	garminHK    = &Source{Key: "garmin_via_healthkit", Provider: "apple_health", DeviceType: "watch", Fingerprint: "synthetic-garmin-watch-01", Model: "Synthetic Band H", OriginKey: "com.garmin.connect.mobile", RelayedProvider: "garmin"}
	bpMonitor   = &Source{Key: "withings_bp", Provider: "withings", DeviceType: "bp_monitor", Fingerprint: "synthetic-bp-monitor-01", Model: "Synthetic BP Cuff"}
	scale       = &Source{Key: "withings_scale", Provider: "withings", DeviceType: "scale", Fingerprint: "synthetic-scale-01", Model: "Synthetic Scale"}
	scaleHK     = &Source{Key: "withings_via_healthkit", Provider: "apple_health", DeviceType: "scale", Fingerprint: "synthetic-scale-01", Model: "Synthetic Scale", OriginKey: "com.withings.wiScaleNG", RelayedProvider: "withings"}
	manualEntry = &Source{Key: "manual_entry", Provider: "manual"}

	allSources = []*Source{garminWatch, appleWatch, iphone, garminHK, bpMonitor, scale, scaleHK, manualEntry}
)

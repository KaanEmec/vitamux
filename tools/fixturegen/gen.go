package main

import (
	"math/rand/v2"
	"time"
)

type gen struct {
	seed   uint64
	hrStep int
	out    []ShapeWriter
	err    error
}

// day is one local day of ground truth shared by every source: per-minute steps and heart rate
// that each device then observes with its own coverage, noise and sampling.
type day struct {
	idx        int
	loc        *time.Location
	date       string
	start, end time.Time
	n          int   // minutes
	off        []int // offset minutes east of UTC, per minute
	wall       []int // wall clock minute of day, per minute
	steps, hr  []int
	wsStart    int // workout minute range [wsStart, wsEnd)
	wsEnd      int
}

func (d *day) at(m int) time.Time { return d.start.Add(time.Duration(m) * time.Minute) }

// minuteAt returns the first minute whose wall clock is w (or the nearest valid minute).
func (d *day) minuteAt(w int) int {
	for m, x := range d.wall {
		if x >= w {
			return m
		}
	}
	return d.n - 1
}

func (d *day) offAt(t time.Time) int {
	m := int(t.Sub(d.start) / time.Minute)
	return d.off[min(max(m, 0), d.n-1)]
}

func offsetOf(t time.Time, loc *time.Location) int {
	_, o := t.In(loc).Zone()
	return o / 60
}

func (g *gen) run(first, days int) error {
	for _, s := range allSources {
		g.each(func(w ShapeWriter) error { return w.Source(*s) })
	}
	var cursor time.Time // end of the previous day: a timezone change must not overlap in time
	for i := range days {
		d := g.newDay(first+i, cursor)
		cursor = d.end
		wornG := g.garmin(d)
		g.garminRelay(d, wornG)
		g.apple(d)
		g.phone(d)
		g.sleep(d)
		g.bp(d)
		g.weighIn(d)
		g.manual(d)
		if g.err != nil {
			return g.err
		}
	}
	return nil
}

func (g *gen) each(f func(ShapeWriter) error) {
	for _, w := range g.out {
		if g.err == nil {
			g.err = f(w)
		}
	}
}

func (g *gen) meas(d *day, src *Source, metric, kind string, start, end time.Time, v Num, ext string, flags uint8) {
	m := Measurement{Src: src, Metric: metric, Kind: kind, Start: start, End: end, Offset: d.offAt(start), Date: d.date, Val: v, Ext: ext, Flags: flags}
	g.each(func(w ShapeWriter) error { return w.Measurement(m) })
}

func (g *gen) revise(r Revision) { g.each(func(w ShapeWriter) error { return w.Revision(r) }) }

// hrCirc is the typical heart rate offset above the resting base for each wall clock hour.
var hrCirc = [24]int{-4, -6, -7, -7, -6, -5, -2, 4, 10, 12, 13, 13, 13, 13, 13, 13, 13, 13, 13, 13, 12, 10, 6, 0}

func (g *gen) newDay(idx int, cursor time.Time) *day {
	loc := zoneOf(idx)
	t := dayDate(idx)
	y, mo, dd := t.Date()
	d := &day{idx: idx, loc: loc, date: t.Format("2006-01-02")}
	d.start = time.Date(y, mo, dd, 0, 0, 0, 0, loc)
	d.end = time.Date(y, mo, dd+1, 0, 0, 0, 0, loc)
	if d.start.Before(cursor) {
		d.start = cursor
	}
	d.n = int(d.end.Sub(d.start) / time.Minute)
	d.off, d.wall = make([]int, d.n), make([]int, d.n)
	d.steps, d.hr = make([]int, d.n), make([]int, d.n)

	r := stream(g.seed, "truth", idx)
	workout, wsWall, wsLen := r.IntN(7) < 3, between(r, 1050, 1139), between(r, 35, 54)
	base, walk, bout := 56+(idx/45)%4+r.IntN(5), 0, 0
	for m := range d.n {
		lt := d.at(m).In(loc)
		h, mi, _ := lt.Clock()
		w := h*60 + mi
		_, o := lt.Zone()
		d.off[m], d.wall[m] = o/60, w

		inWorkout := workout && w >= wsWall && w < wsWall+wsLen
		spm := r.IntN(2) // ground truth steps per minute
		switch {
		case inWorkout:
			spm = between(r, 150, 174)
			if d.wsEnd == 0 {
				d.wsStart = m
			}
			d.wsEnd = m + 1
		case w < 420 || w >= 1380:
			spm = 0
		case bout > 0:
			spm = between(r, 70, 114)
			bout--
		case r.IntN(90) == 0:
			bout = between(r, 3, 17)
		}
		d.steps[m] = spm

		walk = min(max(walk+r.IntN(3)-1, -5), 5)
		hr := base + hrCirc[h] + spm/4 + walk
		if inWorkout {
			hr += 45
		}
		d.hr[m] = min(max(hr, 38), 195)
	}
	return d
}

// garmin emits the high-frequency wrist source and returns which minutes it covered.
// Scenarios: gap (dead battery), partial wear, daily totals next to intervals, corrections.
func (g *gen) garmin(d *day) []bool {
	worn := make([]bool, d.n)
	if batteryDead[d.idx] {
		return worn
	}
	r := stream(g.seed, "garmin", d.idx)
	chargeFrom := between(r, 1140, 1259)
	chargeTo := chargeFrom + between(r, 30, 60)
	for m := range worn {
		w := d.wall[m]
		charging := w >= chargeFrom && w < chargeTo
		off := partialWear[d.idx] && w >= 780 && w < 1260
		worn[m] = !charging && !off
	}

	for s := 0; s < d.n*60; s += g.hrStep {
		if m := s / 60; worn[m] {
			g.meas(d, garminWatch, "heart_rate", "sample", d.start.Add(time.Duration(s)*time.Second), time.Time{}, n0(d.hr[m]+r.IntN(5)-2), "", 0)
		} else {
			r.IntN(5) // keep the stream aligned whatever the coverage
		}
	}
	total := 0
	for m := 0; m < d.n; m += 15 {
		e, sum, seen := min(m+15, d.n), 0, false
		for k := m; k < e; k++ {
			if worn[k] {
				sum, seen = sum+d.steps[k], true
			}
		}
		if seen && sum > 0 {
			g.meas(d, garminWatch, "steps", "interval", d.at(m), d.at(e), n0(sum), "", 0)
			total += sum
		}
	}
	if total > 0 {
		ext := "garmin-steps-" + d.date
		g.meas(d, garminWatch, "steps", "daily_value", d.start, d.end, n0(total), ext, 0)
		if stepCorrections[d.idx] {
			g.revise(Revision{Op: "correct", At: d.end.Add(48 * time.Hour), Record: "measurement", Src: garminWatch, Metric: "steps", Ext: ext, Val: n0(total + between(r, 300, 1200))})
		}
	}
	lowest := 255
	for m, ok := range worn {
		if ok {
			lowest = min(lowest, d.hr[m])
		}
	}
	if lowest < 255 {
		g.meas(d, garminWatch, "resting_heart_rate", "daily_value", d.start, d.end, n0(lowest+r.IntN(3)), "garmin-rhr-"+d.date, 0)
	}
	return worn
}

// garminRelay is the same watch's data arriving a second time through HealthKit at minute and
// hourly resolution, flagged relayed.
func (g *gen) garminRelay(d *day, worn []bool) {
	for m, ok := range worn {
		if ok {
			g.meas(d, garminHK, "heart_rate", "sample", d.at(m), time.Time{}, n0(d.hr[m]), "", flagRelayed)
		}
	}
	for m := 0; m < d.n; m += 60 {
		e, sum := min(m+60, d.n), 0
		for k := m; k < e; k++ {
			if worn[k] {
				sum += d.steps[k]
			}
		}
		if sum > 0 {
			g.meas(d, garminHK, "steps", "interval", d.at(m), d.at(e), n0(sum), "", flagRelayed)
		}
	}
}

// apple emits the Apple Watch: irregular heart rate samples (dense during workouts) and
// variable-length step intervals that overlap the iPhone's.
func (g *gen) apple(d *day) {
	r := stream(g.seed, "apple", d.idx)
	if phoneOnly[d.idx] || r.IntN(10) == 0 {
		return
	}
	worn := func(m int) bool { return d.wall[m] < 390 || d.wall[m] >= 450 } // charging 06:30-07:30
	for s := between(r, 0, 100); s < d.n*60; {
		m := s / 60
		inWorkout := m >= d.wsStart && m < d.wsEnd
		if v := d.hr[m] + r.IntN(7) - 3; worn(m) {
			g.meas(d, appleWatch, "heart_rate", "sample", d.start.Add(time.Duration(s)*time.Second), time.Time{}, n0(v), "", 0)
		}
		if inWorkout {
			s += 5
		} else {
			s += between(r, 10, 90)
		}
	}
	for m := 0; m < d.n; {
		e, sum := min(m+between(r, 5, 15), d.n), 0
		for k := m; k < e; k++ {
			if worn(k) {
				sum += d.steps[k]
			}
		}
		if v := sum * 95 / 100; v > 0 {
			g.meas(d, appleWatch, "steps", "interval", d.at(m), d.at(e), n0(v), "", 0)
		}
		m = e
	}
}

// phone emits the iPhone's steps, carried about 55 % of the time in blocks of 1-3 hours.
func (g *gen) phone(d *day) {
	r := stream(g.seed, "phone", d.idx)
	carried := make([]bool, d.n)
	for m := 0; m < d.n; {
		c, e := r.IntN(100) < 55, min(m+between(r, 60, 180), d.n)
		for ; m < e; m++ {
			carried[m] = c
		}
	}
	for m := 0; m < d.n; {
		e, sum := min(m+between(r, 10, 40), d.n), 0
		for k := m; k < e; k++ {
			if carried[k] {
				sum += d.steps[k]
			}
		}
		if v := sum * 85 / 100; v > 0 {
			g.meas(d, iphone, "steps", "interval", d.at(m), d.at(e), n0(v), "", 0)
		}
		m = e
	}
}

// bp emits morning and evening Withings cuff readings as groups, with one deletion and one correction.
func (g *gen) bp(d *day) {
	r := stream(g.seed, "bp", d.idx)
	for _, slot := range []struct {
		name         string
		wall, spread int
		pct          int
	}{{"am", 420, 45, 85}, {"pm", 1230, 60, 70}} {
		roll, m := r.IntN(100), d.minuteAt(slot.wall+r.IntN(slot.spread))
		sys := 124 + (d.idx/30)%5 + between(r, -7, 7)
		if slot.name == "am" {
			sys += 4
		}
		dia := 78 + between(r, -4, 4) + (sys-124)/3
		pulse := d.hr[m] + between(r, -2, 2)
		pinned := (slot.name == "pm" && d.idx == deleteBP) || (slot.name == "am" && d.idx == correctBP)
		if roll >= slot.pct && !pinned {
			continue
		}
		ext := "withings-bp-" + d.date + "-" + slot.name
		at := d.at(m)
		grp := Group{Src: bpMonitor, Kind: "bp_reading", At: at, Offset: d.off[m], Date: d.date, Ext: ext, Context: `{"arm":"left","posture":"sitting"}`,
			Parts: []Part{{"bp_systolic", n0(sys)}, {"bp_diastolic", n0(dia)}, {"bp_pulse", n0(pulse)}}}
		g.each(func(w ShapeWriter) error { return w.Group(grp) })
		switch {
		case slot.name == "pm" && d.idx == deleteBP:
			g.revise(Revision{Op: "delete", At: at.Add(26 * time.Hour), Record: "group", Src: bpMonitor, Ext: ext})
		case slot.name == "am" && d.idx == correctBP:
			g.revise(Revision{Op: "correct", At: at.Add(48 * time.Hour), Record: "group", Src: bpMonitor, Metric: "bp_systolic", Ext: ext, Val: n0(sys + 10)})
		}
	}
}

// weighIn emits scale body-composition groups about every other day; 60 % are also relayed
// through HealthKit as plain samples (a duplicate the rules must be able to exclude).
func (g *gen) weighIn(d *day) {
	r := stream(g.seed, "scale", d.idx)
	roll, m, relay := r.IntN(100), d.minuteAt(400+r.IntN(40)), r.IntN(100) < 60
	w := 800 - d.idx/10 + between(r, -3, 3) // tenths of kg
	fat := 230 - d.idx/20 + between(r, -2, 2)
	muscle, hydration := 360+between(r, -2, 2), 440+between(r, -4, 4)
	if roll >= 50 && d.idx != deleteWeighIn {
		return
	}
	ext, at := "withings-bc-"+d.date, d.at(m)
	grp := Group{Src: scale, Kind: "body_composition", At: at, Offset: d.off[m], Date: d.date, Ext: ext, Context: `{}`,
		Parts: []Part{{"weight", n1(w)}, {"body_fat_ratio", n1(fat)}, {"fat_mass", n1(w * fat / 1000)}, {"muscle_mass", n1(muscle)}, {"bone_mass", n1(31)}, {"hydration", n1(hydration)}}}
	g.each(func(x ShapeWriter) error { return x.Group(grp) })
	if d.idx == deleteWeighIn {
		g.revise(Revision{Op: "delete", At: at.Add(24 * time.Hour), Record: "group", Src: scale, Ext: ext})
	}
	if relay {
		g.meas(d, scaleHK, "weight", "sample", at, time.Time{}, n1(w), "hk-weight-"+d.date, flagRelayed)
		g.meas(d, scaleHK, "body_fat_ratio", "sample", at, time.Time{}, n1(fat), "hk-fat-"+d.date, flagRelayed)
	}
}

// manual emits an occasional hand-typed heart rate; defaults exclude these.
func (g *gen) manual(d *day) {
	r := stream(g.seed, "manual", d.idx)
	if d.idx%11 == 3 {
		g.meas(d, manualEntry, "heart_rate", "sample", d.at(d.minuteAt(720+r.IntN(60))), time.Time{}, n0(between(r, 68, 87)), "", flagManual)
	}
}

type span struct {
	name     string
	from, to int // minutes since bed
}

// nightSpans builds a night's ground truth stages: awake latency, then light/deep/light/rem
// cycles (deep early, REM late) with brief awakenings.
func nightSpans(r *rand.Rand, dur int) []span {
	lat := 5 + r.IntN(16)
	spans, t := []span{{"awake", 0, lat}}, lat
	add := func(name string, l int) {
		if t < dur {
			spans = append(spans, span{name, t, min(t+l, dur)})
			t += l
		}
	}
	for c := 0; t < dur; c++ {
		add("light", 20+r.IntN(16))
		add("deep", max(5, 30-c*7)+r.IntN(6))
		add("light", 10+r.IntN(11))
		add("rem", 8+c*6+r.IntN(8))
		if r.IntN(10) < 4 {
			add("awake", 1+r.IntN(5))
		}
	}
	return spans
}

// carve turns [a, b) into awake time.
func carve(spans []span, a, b int) []span {
	var out []span
	for _, s := range spans {
		if s.to <= a || s.from >= b {
			out = append(out, s)
			continue
		}
		if s.from < a {
			out = append(out, span{s.name, s.from, a})
		}
		if s.to > b {
			out = append(out, span{s.name, b, s.to})
		}
	}
	out = append(out, span{"awake", a, b})
	// keep chronological order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].from < out[j-1].from; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// session builds a Sleep from the truth spans inside [a, b) minutes after bed. Totals are summed
// from the stages, so a stage-less source reports -1 where it has none.
func session(src *Source, ext string, bed time.Time, spans []span, a, b int, loc *time.Location, date string, lat int) Sleep {
	s := Sleep{Src: src, Ext: ext, Start: bed.Add(time.Duration(a) * time.Minute), End: bed.Add(time.Duration(b) * time.Minute),
		Date: date, HasStages: true, Basis: "provider", DeepS: 0, LightS: 0, REMS: 0, AwakeS: 0, LatS: lat}
	s.Offset = offsetOf(s.End, loc)
	for _, p := range spans {
		from, to := max(p.from, a), min(p.to, b)
		if from >= to {
			continue
		}
		s.Stages = append(s.Stages, Stage{p.name, bed.Add(time.Duration(from) * time.Minute), bed.Add(time.Duration(to) * time.Minute)})
		sec := (to - from) * 60
		switch p.name {
		case "deep":
			s.DeepS += sec
		case "light":
			s.LightS += sec
		case "rem":
			s.REMS += sec
		default:
			s.AwakeS += sec
		}
	}
	s.AsleepS = s.DeepS + s.LightS + s.REMS
	return s
}

// sleep emits the night ending on this local day from two sources: Garmin (staged, split around
// long wakes, plus naps) and Apple Watch (partial nights, sometimes without stages).
func (g *gen) sleep(d *day) {
	r := stream(g.seed, "sleep", d.idx)
	emit := func(s Sleep) { g.each(func(w ShapeWriter) error { return w.Sleep(s) }) }

	if d.idx%9 == 4 && !batteryDead[d.idx] {
		m, l := d.minuteAt(810+r.IntN(60)), between(r, 30, 45)
		emit(Sleep{Src: garminWatch, Ext: "garmin-nap-" + d.date, Start: d.at(m), End: d.at(m + l), Offset: d.off[m], Date: d.date, Nap: true,
			Basis: "provider", AsleepS: l * 60, DeepS: -1, LightS: -1, REMS: -1, AwakeS: -1, LatS: -1})
	}
	if flightDay(d.idx) || zoneOf(d.idx-1) != d.loc {
		return
	}
	y, mo, dd := dayDate(d.idx - 1).Date()
	bed := time.Date(y, mo, dd, 0, 0, 0, 0, d.loc).Add(time.Duration(1365+r.IntN(71)) * time.Minute)
	dur := between(r, 390, 510)
	spans := nightSpans(r, dur)
	gap := 0
	switch {
	case splitLong[d.idx]:
		gap = 75
	case splitShort[d.idx]:
		gap = 35
	}
	if gap > 0 {
		spans = carve(spans, dur/2, dur/2+gap)
	}
	appleRoll, appleOn, cut1, cut2 := r.IntN(100), r.IntN(2) == 0, between(r, 20, 90), r.IntN(41)
	lat := (spans[0].to - spans[0].from) * 60

	if !batteryDead[d.idx] {
		if gap > 0 {
			emit(session(garminWatch, "garmin-sleep-"+d.date+"-1", bed, spans, 0, dur/2, d.loc, d.date, lat))
			emit(session(garminWatch, "garmin-sleep-"+d.date+"-2", bed, spans, dur/2+gap, dur, d.loc, d.date, 0))
		} else {
			emit(session(garminWatch, "garmin-sleep-"+d.date, bed, spans, 0, dur, d.loc, d.date, lat))
		}
	}
	if appleRoll < 70 && !phoneOnly[d.idx] && dur-cut1-cut2 > 120 {
		a, b := cut1, dur-cut2
		ext := "apple-sleep-" + d.date
		if appleOn {
			s := session(appleWatch, ext, bed, spans, a, b, d.loc, d.date, -1)
			s.Basis = "stages"
			emit(s)
		} else {
			start := bed.Add(time.Duration(a) * time.Minute)
			end := bed.Add(time.Duration(b) * time.Minute)
			emit(Sleep{Src: appleWatch, Ext: ext, Start: start, End: end, Offset: offsetOf(end, d.loc), Date: d.date, Basis: "stages",
				AsleepS: (b - a) * 60, DeepS: -1, LightS: -1, REMS: -1, AwakeS: -1, LatS: -1,
				Stages: []Stage{{"asleep_unspecified", start, end}}})
		}
	}
}

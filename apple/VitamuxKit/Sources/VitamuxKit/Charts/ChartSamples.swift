import SwiftUI

/// Synthetic data for previews and tests: deterministic curves, no real or recorded values.
enum ChartSamples {
    static let timeZone = TimeZone(identifier: "Europe/Berlin")!
    static let day = LocalDate("2026-10-04")!
    static var dayStart: Date { day.start(in: timeZone) }

    /// One value every `step` seconds over a local day, with a gap from 13:00 to 14:00.
    static func intraday(step: TimeInterval = 6, base: Double = 64, swing: Double = 18) -> [ChartPoint] {
        let count = Int(86_400 / step)
        return (0 ..< count).map { i in
            let t = Double(i) * step
            let gap = t >= 13 * 3_600 && t < 14 * 3_600
            let y = base + swing * sin(t / 86_400 * 2 * .pi - .pi / 2) + 6 * sin(t / 600)
            return ChartPoint(x: dayStart.addingTimeInterval(t), y: gap ? nil : y.rounded())
        }
    }

    /// Daily points ending on `day`, with statuses and one missing day.
    static func daily(_ count: Int = 30, base: Double = 58, swing: Double = 4) -> [ChartPoint] {
        let statuses: [DataStatus] = [.direct, .direct, .fallback, .direct, .calculated, .direct, .overridden, .direct, .partial]
        return (0 ..< count).map { i in
            let date = day.adding(days: i - count + 1).start(in: timeZone)
            if i == count / 2 { return ChartPoint(x: date, y: nil, status: .noData) }
            return ChartPoint(x: date, y: (base + swing * sin(Double(i) / 3)).rounded(), status: statuses[i % statuses.count], providers: [i % 2 == 0 ? "apple_health" : "garmin"])
        }
    }

    static var dailySeries: [ChartSeries] {
        let resolved = daily()
        return [
            ChartSeries(label: "Resolved", points: resolved),
            ChartSeries(label: "Garmin", points: resolved.map { ChartPoint(x: $0.x, y: $0.y.map { $0 + 1 }) }, source: "garmin"),
            ChartSeries(label: "Draft", points: resolved.map { ChartPoint(x: $0.x, y: $0.y.map { $0 - 1 }) }, style: .ghost),
        ]
    }

    static var band: ChartBand {
        ChartBand(label: "Daily range", points: daily().map { .init(x: $0.x, low: $0.y.map { $0 - 6 }, high: $0.y.map { $0 + 9 }) })
    }

    static var nights: (xs: [Date], stacks: [BarStack]) {
        let xs = (0 ..< 14).map { day.adding(days: $0 - 13).start(in: timeZone) }
        let hours = { (base: Double, k: Double) in xs.indices.map { i -> Double? in i == 6 ? nil : ((base + k * sin(Double(i))) * 10).rounded() / 10 } }
        return (xs, [
            BarStack(label: "Deep", values: hours(1.4, 0.3), stage: .deep),
            BarStack(label: "Light", values: hours(3.8, 0.5), stage: .light),
            BarStack(label: "REM", values: hours(1.6, 0.4), stage: .rem),
            BarStack(label: "Awake", values: hours(0.4, 0.2), stage: .awake),
        ])
    }

    static var stages: [StageInterval] {
        let start = dayStart.addingTimeInterval(-2 * 3_600)
        let plan: [(SleepStageKind, Double)] = [(.awake, 10), (.light, 40), (.deep, 50), (.light, 30), (.rem, 25), (.awake, 5), (.light, 60), (.deep, 35), (.rem, 40), (.light, 70), (.rem, 30), (.awake, 8)]
        var t = start
        return plan.map { stage, minutes in
            defer { t = t.addingTimeInterval(minutes * 60) }
            return StageInterval(stage: stage, start: t, end: t.addingTimeInterval(minutes * 60))
        }
    }

    static var readings: [RangeDumbbell.Reading] {
        (0 ..< 12).map { i in
            RangeDumbbell.Reading(
                x: day.adding(days: i - 11).start(in: timeZone).addingTimeInterval(7.5 * 3_600),
                low: 76 + 4 * sin(Double(i)), high: 121 + 6 * cos(Double(i)),
                details: [.init(label: "Pulse", value: "\(62 + i % 5) bpm")]
            )
        }
    }

    static var lanes: [EventLanes.Lane] {
        let at = { (h: Double) in dayStart.addingTimeInterval(h * 3_600) }
        return [
            .init(label: "Workouts", source: "apple_health", events: [.init(start: at(7), end: at(7.75), label: "Outdoor run"), .init(start: at(18), end: at(19), label: "Cycling")]),
            .init(label: "Sessions", source: "whoop", events: [.init(start: at(12.5), end: at(12.8), label: "Breathing")]),
            .init(label: "Notifications", events: [.init(start: at(15), end: at(15), label: "Reminder")]),
        ]
    }

    static var coverage: [CoverageStrip.Row] {
        let days = 0 ..< 30
        let apple: [Double] = days.map { i in i % 7 == 3 ? 0 : Double(i % 4 + 1) / 4 }
        let garmin: [Double] = days.map { i in i < 5 ? 0 : 1 }
        let picks: [String?] = days.map { i in i % 7 == 3 ? nil : i < 5 ? "apple_health" : "garmin" }
        return [
            CoverageStrip.Row(label: "apple_health", source: "apple_health", cells: apple),
            CoverageStrip.Row(label: "garmin", source: "garmin", cells: garmin),
            CoverageStrip.Row(label: "Resolved from", cells: Array(repeating: 1, count: 30), picks: picks),
        ]
    }

    /// A Day view's 30-minute buckets, nothing at night.
    static var halfHours: (xs: [Date], values: [Double?]) {
        let xs = (0 ..< 48).map { dayStart.addingTimeInterval(Double($0) * 1_800) }
        return (xs, xs.indices.map { i in i < 14 ? nil : (300 + 250 * sin(Double(i) / 4)).rounded() })
    }

    /// The Day view's overlays: a night, a workout and now.
    static var dayOverlays: [ChartOverlay] {
        let at = { (h: Double) in dayStart.addingTimeInterval(h * 3_600) }
        return [
            ChartOverlay(label: "Night", start: at(-1), end: at(6.8), style: .shade),
            ChartOverlay(label: "Running", start: at(7.2), end: at(7.9), style: .tint),
            ChartOverlay(label: "Now", start: at(15.5), style: .line),
        ]
    }

    /// Every view of the kit, for previews and the rendering smoke tests.
    @MainActor static var gallery: [(String, AnyView)] {
        let nights = nights
        return [
            ("TimeSeries intraday", AnyView(TimeSeries(title: "Heart rate", series: [ChartSeries(label: "Heart rate", points: intraday())], unit: "bpm", hue: .heartRate, area: true, timeZone: timeZone))),
            ("TimeSeries band", AnyView(TimeSeries(title: "Resting heart rate", series: dailySeries, unit: "bpm", hue: .heartRate, band: band, baseline: ChartBaseline(value: 58, label: "30-day mean"), timeZone: timeZone, withTime: false))),
            ("TimeSeries step", AnyView(TimeSeries(title: "Weight", series: [ChartSeries(label: "Weight", points: daily(base: 72, swing: 0.6))], unit: "kg", hue: .weight, kind: .step, timeZone: timeZone, withTime: false))),
            ("TimeSeries dots", AnyView(TimeSeries(title: "Lab analyte", series: [ChartSeries(label: "Lab analyte", points: daily(6, base: 80, swing: 20), style: .dots)], unit: "U/L", hue: .lab, baseline: ChartBaseline(value: nil, label: "As printed", range: 30 ... 400), timeZone: timeZone, withTime: false))),
            ("Bars", AnyView(Bars(title: "Steps", xs: daily().map(\.x), values: daily(base: 8_000, swing: 3_000).map(\.y), unit: "steps", hue: .steps, baseline: ChartBaseline(value: 8_000, label: "30-day mean"), status: daily().map(\.status), timeZone: timeZone))),
            ("Bars stages", AnyView(Bars(title: "Sleep stages", xs: nights.xs, stacks: nights.stacks, unit: "h", hue: .sleep, timeZone: timeZone))),
            ("Hypnogram", AnyView(Hypnogram(title: "Night of 3 October", stages: stages, from: stages[0].start, to: stages.last!.end, timeZone: timeZone))),
            ("RangeDumbbell", AnyView(RangeDumbbell(title: "Blood pressure", readings: readings, lowLabel: "Diastolic", highLabel: "Systolic", unit: "mmHg", timeZone: timeZone))),
            ("EventLanes", AnyView(EventLanes(title: "Events", lanes: lanes, from: dayStart, to: dayStart.addingTimeInterval(86_400), timeZone: timeZone))),
            ("Sparkline", AnyView(Sparkline(values: daily().map(\.y), band: 54 ... 62, mean: 58, ghost: daily().map { $0.y.map { $0 - 1 } }, label: "Resting heart rate, 30 days", hue: .heartRate))),
            ("CoverageStrip", AnyView(CoverageStrip(caption: "Coverage", rows: coverage, start: day.adding(days: -29)))),
            ("Bars day", AnyView(Bars(
                title: "Steps per 30 minutes", xs: halfHours.xs, values: halfHours.values, unit: "steps", hue: .steps, timeZone: timeZone,
                binWidth: 1_800, domain: dayStart ... dayStart.addingTimeInterval(86_400), overlays: dayOverlays
            ))),
        ]
    }
}

#Preview("Chart kit") {
    ScrollView {
        VStack(alignment: .leading, spacing: 24) {
            ForEach(ChartSamples.gallery, id: \.0) { name, view in
                VStack(alignment: .leading) {
                    Text(name).font(.headline)
                    view
                }
            }
        }
        .padding()
    }
}

import Foundation

extension WidgetSnapshot {
    /// Synthetic values for the widget gallery, previews and tests; never the owner's data.
    static let sample = WidgetSnapshot(
        asOf: Date(timeIntervalSince1970: 1_791_100_800), // 2026-10-04 08:00 UTC
        day: "2026-10-04",
        timeZone: "Europe/Berlin",
        cards: [
            Card(
                metric: "sleep", label: "Sleep", hue: "sleep", value: "7h 19m", unit: "asleep", sub: "in bed 7h 52m",
                delta: "30-day mean 7h 05m", status: "direct",
                values: [25_200, 26_100, 24_000, nil, 27_300, 25_800, 26_340], bars: true, mean: 25_500,
                stages: [Stage(kind: "deep", seconds: 5_400), Stage(kind: "light", seconds: 13_500),
                         Stage(kind: "rem", seconds: 7_440), Stage(kind: "awake", seconds: 1_980)]
            ),
            Card(
                metric: "resting_heart_rate", label: "Resting heart rate", hue: "heartRate", value: "52", unit: "bpm",
                delta: "−1.4 vs 30-day mean", status: "direct", values: [54, 53, 55, 52, 53, 54, 52], band: 50...57, mean: 53.4
            ),
            Card(
                metric: "hrv_rmssd_nightly", label: "HRV · nightly RMSSD", hue: "hrv", value: "61", unit: "ms",
                delta: "+3 vs 30-day mean", status: "fallback", values: [55, 58, nil, 60, 57, 59, 61], band: 49...66, mean: 58
            ),
            Card(
                metric: "steps", label: "Steps", hue: "steps", value: "8,412", unit: "so far",
                delta: "30-day mean 9,120", status: "partial", values: [9_800, 7_400, 10_200, 8_900, 11_000, 9_300, 8_412], bars: true,
                mean: 9_120
            ),
            Card(
                metric: "blood_pressure", label: "Blood pressure", hue: "bloodPressure", value: "118/76", unit: "mmHg",
                sub: "pulse 58 bpm", delta: "30-day mean 120/78", status: "direct", values: [121, 119, nil, 120, 118], mean: 120
            ),
        ]
    )
}

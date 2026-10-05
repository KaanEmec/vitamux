import SwiftUI
import VitamuxKit

typealias Run = Components.Schemas.Run

/// A connection's sync runs over the last 14 local days (web/src/lib/connections/runs.ts).
enum Runs {
    static let days = 14
    private static let pageSize = 500
    private static let maxPages = 4

    struct Day: Identifiable {
        var date: Date
        var succeeded = 0
        var failed = 0
        var id: Date { date }
    }

    /// The runs of the last 14 local days, newest first.
    static func load(_ client: Client, id: String, now: Date = .now) async throws -> [Run] {
        let since = firstDay(now)
        var out: [Run] = []
        var cursor: String?
        for _ in 0..<maxPages {
            let page = try await client.listConnectionRuns(path: .init(id: id), query: .init(limit: pageSize, cursor: cursor)).ok.body.json
            out += page.value2.runs
            guard page.value1.hasMore, let next = page.value1.nextCursor, let oldest = page.value2.runs.last, oldest.startedAt >= since else { break }
            cursor = next
        }
        return out.filter { $0.startedAt >= since }
    }

    /// One entry per local day, oldest first, counting finished runs by outcome; retries are not counted.
    static func days(_ runs: [Run], now: Date = .now) -> [Day] {
        let calendar = Calendar.current
        let start = firstDay(now)
        var days = (0..<days).map { Day(date: calendar.date(byAdding: .day, value: $0, to: start)!) }
        for run in runs {
            let index = calendar.dateComponents([.day], from: start, to: calendar.startOfDay(for: run.startedAt)).day ?? -1
            guard days.indices.contains(index) else { continue }
            if run.outcome == "succeeded" { days[index].succeeded += 1 } else if run.outcome == "failed" { days[index].failed += 1 }
        }
        return days
    }

    private static func firstDay(_ now: Date) -> Date {
        let calendar = Calendar.current
        return calendar.date(byAdding: .day, value: -(days - 1), to: calendar.startOfDay(for: now))!
    }
}

/// The 14-day run strip: a filled cell for a day with only successful runs, a hatched one with a
/// failed run (a shape, not only a colour), an empty one without runs. Its summary is its label.
struct RunStrip: View {
    /// nil while loading; an empty list when the API did not answer is shown as no runs.
    let runs: Loadable<[Run]>
    var large = false

    var body: some View {
        let days = Runs.days(runs.value ?? [])
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("Sync runs · \(Runs.days) days")
                Spacer()
                if let runs = runs.value { Text(SourcesCopy.plural(runs.count, "run")) }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            HStack(spacing: 3) {
                ForEach(days) { day in Cell(day: day, large: large) }
            }
            if large, let first = days.first {
                HStack {
                    Text(SourcesCopy.day(first.date))
                    Spacer()
                    Text("Today")
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
                .accessibilityHidden(true)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(label(days))
        .accessibilityIdentifier("runStrip")
    }

    private func label(_ days: [Runs.Day]) -> String {
        switch runs {
        case .loading: return "Sync runs: loading"
        case .failed: return "Sync runs are not available"
        case .loaded:
            let ok = days.filter { $0.succeeded > 0 && $0.failed == 0 }.count
            let failed = days.filter { $0.failed > 0 }.count
            let p = SourcesCopy.plural
            return "Sync runs, last \(Runs.days) days: \(p(ok, "day", nil)) with successful runs, \(p(failed, "day", nil)) with a failed run, \(p(Runs.days - ok - failed, "day", nil)) without runs"
        }
    }

    private struct Cell: View {
        let day: Runs.Day
        let large: Bool

        var body: some View {
            RoundedRectangle(cornerRadius: 2)
                .fill(fill)
                .overlay {
                    if day.failed > 0 { Hatch().stroke(.orange, lineWidth: 1.5).clipShape(.rect(cornerRadius: 2)) }
                }
                .frame(height: large ? 22 : 12)
                .frame(maxWidth: .infinity)
        }

        private var fill: Color {
            if day.failed > 0 { return .orange.opacity(0.3) }
            return day.succeeded > 0 ? .green.opacity(0.75) : Color(.tertiarySystemFill)
        }
    }

    /// Diagonal lines for a day with a failed run.
    nonisolated private struct Hatch: Shape {
        func path(in rect: CGRect) -> Path {
            var path = Path()
            var x = -rect.height
            while x < rect.width {
                path.move(to: CGPoint(x: x, y: rect.height))
                path.addLine(to: CGPoint(x: x + rect.height, y: 0))
                x += 4
            }
            return path
        }
    }
}

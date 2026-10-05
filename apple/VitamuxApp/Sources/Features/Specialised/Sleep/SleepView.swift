import SwiftUI
import VitamuxKit

/// Sleep (`vitamux://explore/sleep`, artboard Sleep): the range, the mean night with median bed and
/// wake, stage bars per night, the last night with its status and selected source, and every
/// night newest first. A night opens its sources' hypnograms side by side (`NightView`).
struct SleepView: View {
    @Environment(AppState.self) private var state
    @State private var model = SleepModel()
    @State private var opened: OpenedNight?

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                ViewIntro(code: "sleep", text: "Episodes are aligned across sources before one is chosen. A night is dated by the day you woke up.")
                RangePicker(range: $model.span.range, end: $model.span.end, latest: model.span.latest)
                    .buttonStyle(.borderless) // one row, two buttons: each takes only its own taps
                    .accessibilityIdentifier("rangePicker")
            }
            switch model.sleep {
            case .loading:
                ProgressView("Loading sleep").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded where model.withData.isEmpty:
                EmptyRangeView(title: "No sleep in this range", text: "Nothing is stored for these nights, or no source covers them.")
            case .loaded:
                SleepStats(model: model)
                StageBarsSection(model: model)
                if let last = model.withData.last {
                    LastNightSection(night: last, timeZone: model.timeZone, opened: $opened)
                }
                NightsSection(model: model, opened: $opened)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Sleep")
        .task(id: model.span.key) { await model.load(state.client) }
        .navigationDestination(item: $opened) { opened in
            NightView(night: opened.night, timeZone: model.timeZone)
        }
    }
}

/// The night a row opened.
struct OpenedNight: Identifiable, Hashable {
    let night: Night
    var id: String { night.localDate }

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.id == rhs.id }
    func hash(into hasher: inout Hasher) { hasher.combine(id) }
}

private struct SleepStats: View {
    let model: SleepModel

    var body: some View {
        Section {
            HStack(alignment: .top) {
                StatTile(label: model.span.meanLabel, value: asleepText(model.meanAsleep))
                StatTile(label: "Median bedtime", value: model.medianBed)
                StatTile(label: "Median wake", value: model.medianWake)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("sleepStats")
            Text("\(model.withData.count) of \(model.nights.count) nights with data")
                .font(.footnote)
                .foregroundStyle(.secondary)
        }
    }
}

private struct StageBarsSection: View {
    let model: SleepModel

    var body: some View {
        Section {
            Bars(
                title: "Sleep stages per night, stacked", xs: model.xs, stacks: model.stacks, unit: "h", hue: .sleep,
                baseline: model.meanAsleep.map { ChartBaseline(value: $0 / 3600, label: "Mean \(Format.duration($0))") },
                timeZone: .gmt
            )
            .accessibilityIdentifier("stageBars")
        } header: {
            Text("Nightly stages")
        } footer: {
            Text("Hours per night by stage, from the source the rule selected. Nights with no data have no bar.")
        }
    }
}

/// The newest night with data, its status and selected source; it opens the night's sources.
private struct LastNightSection: View {
    let night: Night
    let timeZone: TimeZone
    @Binding var opened: OpenedNight?

    var body: some View {
        Section("Last night") {
            VStack(alignment: .leading, spacing: 6) {
                Text(night.label).font(.headline)
                HStack(spacing: 8) {
                    Text("\(asleepText(night.seconds["sleep_total"])) asleep").monospacedDigit()
                    if let times = night.bedWake(in: timeZone) { Text("· \(times)").foregroundStyle(.secondary).monospacedDigit() }
                }
                .font(.subheadline)
                HStack(spacing: 6) {
                    StatusLabel(status: DataStatus(night.result))
                    if let member = night.selectedMember {
                        Text("· \(memberName(provider: member.provider, origin: member.origin, device: member.device))")
                    }
                }
                .font(.subheadline)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("lastNight")
            Button {
                opened = OpenedNight(night: night)
            } label: {
                Label(night.members.count == 1 ? "Stages and provenance" : "Compare \(night.members.count) sources", systemImage: "rectangle.split.1x2")
            }
            .accessibilityIdentifier("openLastNight")
        }
    }
}

/// Every night with data, newest first, a page at a time.
private struct NightsSection: View {
    let model: SleepModel
    @Binding var opened: OpenedNight?
    @State private var shown = ListPage.size

    var body: some View {
        let nights = Array(model.withData.reversed())
        Section("Nights") {
            ForEach(nights.prefix(shown), id: \.localDate) { night in
                Button {
                    opened = OpenedNight(night: night)
                } label: {
                    NightRow(night: night, timeZone: model.timeZone)
                }
                .tint(.primary)
                .accessibilityIdentifier("night-\(night.localDate)")
            }
            ShowMoreButton(remaining: nights.count - shown) { shown += ListPage.size }
        }
    }
}

private struct NightRow: View {
    let night: Night
    let timeZone: TimeZone

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(night.label)
                if let times = night.bedWake(in: timeZone) {
                    Text(times).font(.footnote).foregroundStyle(.secondary).monospacedDigit()
                }
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 2) {
                Text(asleepText(night.seconds["sleep_total"])).monospacedDigit()
                Text(night.members.count == 1 ? "1 source" : "\(night.members.count) sources").font(.footnote).foregroundStyle(.secondary)
            }
            Image(systemName: "chevron.forward").font(.footnote).foregroundStyle(.tertiary)
        }
        .contentShape(.rect)
    }
}

import SwiftUI
import VitamuxKit

/// Workouts (`vitamux://explore/workouts`): a month calendar with the number of workouts per day,
/// then the month's workouts newest first. Workouts that overlap in time are one card, one row per
/// source with the rule's pick marked, its duration, distance, energy and heart rate, and its
/// provenance.
struct WorkoutsView: View {
    @Environment(AppState.self) private var state
    @State private var model = WorkoutsModel()
    @State private var provenance: ProvenanceRequest?

    var body: some View {
        List {
            Section {
                ViewIntro(code: "workouts", text: "Workouts that overlap in time are listed together, one row per source, so duplicates across sources are visible.")
                MonthCalendar(model: model)
            }
            switch model.workouts {
            case .loading:
                ProgressView("Loading workouts").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                let listed = model.listed
                if listed.isEmpty {
                    EmptyRangeView(title: model.picked == nil ? "No workouts in this month" : "No workouts on this day",
                                   text: "Workouts from every connected source appear here.")
                } else {
                    if let picked = model.picked {
                        Text("Showing \(Format.day(picked)). Pick the day again to show the whole month.")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                    ForEach(listed, id: \.key) { cluster in
                        ClusterSection(cluster: cluster, timeZone: model.timeZone, provenance: $provenance)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Workouts")
        .task(id: model.month) { await model.load(state.client) }
        .sheet(item: $provenance) { ProvenanceSheet(request: $0) }
    }
}

extension WorkoutCluster {
    /// Stable across loads: start, sport and first member.
    var key: String { "\(start.timeIntervalSince1970)-\(sport)-\(members.first?.id ?? "")" }
}

/// One cluster: its sport and time, one row per source, and the rule's explanation.
private struct ClusterSection: View {
    let cluster: WorkoutCluster
    let timeZone: TimeZone
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        Section {
            ForEach(cluster.members, id: \.id) { member in
                MemberRow(member: member, provenance: $provenance)
            }
        } header: {
            HStack {
                Text("\(metricLabel(cluster.sport)) · \(Format.day(cluster.localDate, weekday: false)) \(clockText(cluster.start, in: timeZone))–\(clockText(cluster.end, in: timeZone))")
                if cluster.members.count > 1 {
                    Text("\(cluster.members.count) sources")
                        .padding(.horizontal, 6).padding(.vertical, 1)
                        .background(.tint.opacity(0.15), in: .capsule)
                }
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("cluster-\(cluster.key)")
        } footer: {
            Text(cluster.explanation)
        }
    }
}

private struct MemberRow: View {
    let member: WorkoutMember
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                SourceChip(provider: member.provider, name: member.name).font(.subheadline.weight(.medium))
                Spacer()
                RuleTagLabel(selected: member.selected, text: member.tag)
                    .accessibilityIdentifier("ruleTag-\(member.id)")
            }
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 2) {
                GridRow {
                    Text("Duration")
                    Text("Distance")
                    Text("Energy")
                    Text("Avg / max HR")
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
                GridRow {
                    Text(Format.duration(member.duration))
                    Text(member.distanceM.map { Format.value($0 / 1000, unit: "km") } ?? "–")
                    Text(member.energyKcal.map { Format.value($0, unit: "kcal") } ?? "–")
                    Text("\(member.avgHrBpm.map(Format.number) ?? "–") / \(member.maxHrBpm.map(Format.number) ?? "–") bpm")
                }
                .font(.footnote)
                .monospacedDigit()
            }
            .accessibilityElement(children: .combine)
            ProvenanceButton(entity: .workout, id: member.id, request: $provenance)
        }
        .padding(.vertical, 2)
        .listRowBackground(member.selected ? Color.accentColor.opacity(0.08) : nil)
    }
}

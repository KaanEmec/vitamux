import SwiftUI
import VitamuxKit

/// One night across its sources: every source that recorded it, selected first as the rule
/// ordered them, each with its hypnogram on one shared time axis, its place in the rule and the
/// provenance of its sessions; then the day's naps and the rule's explanation.
struct NightView: View {
    @Environment(AppState.self) private var state
    @State private var model: NightModel
    @State private var provenance: ProvenanceRequest?

    init(night: Night, timeZone: TimeZone) {
        _model = State(initialValue: NightModel(night: night, timeZone: timeZone))
    }

    var body: some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: 4) {
                    Text("\(asleepText(model.night.seconds["sleep_total"])) asleep").font(.title2.weight(.semibold)).monospacedDigit()
                    if let times = model.night.bedWake(in: model.timeZone) { Text(times).foregroundStyle(.secondary).monospacedDigit() }
                    StatusLabel(status: DataStatus(model.night.result)).font(.subheadline)
                }
                .accessibilityElement(children: .combine)
            } footer: {
                Text("Every source that recorded the night, on one time axis, and where each stands under the rule.")
            }
            switch model.sessions {
            case .loading:
                ProgressView("Loading sessions").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded:
                ForEach(Array(model.night.members.enumerated()), id: \.offset) { index, member in
                    MemberSection(model: model, member: member, index: index, provenance: $provenance)
                }
                NapsSection(model: model, provenance: $provenance)
            }
            Section {
                Text(model.night.result.explanation).font(.footnote).foregroundStyle(.secondary)
                NavigationLink(value: Route.rule(metric: "sleep")) {
                    Label("Change rule", systemImage: "slider.horizontal.3")
                }
            } header: {
                Text("How it’s chosen")
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(model.night.label)
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.load(state.client) }
        .sheet(item: $provenance) { ProvenanceSheet(request: $0) }
    }
}

/// One source's episode: its name and rule tag, its hypnogram on the shared axis, times, time
/// asleep and the provenance of each session.
private struct MemberSection: View {
    let model: NightModel
    let member: Components.Schemas.SleepMember
    let index: Int
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        let sessions = model.sessions(of: member)
        let name = memberName(provider: member.provider, origin: member.origin, device: member.device)
        Section {
            HStack {
                SourceChip(provider: member.provider, name: name).font(.headline)
                Spacer()
                RuleTagLabel(selected: member.selected, text: ruleTag(selected: member.selected, status: member.ruleStatus.rawValue, reason: member.reason))
                    .accessibilityIdentifier("ruleTag-\(index)")
            }
            let stages = model.stages(of: sessions)
            if let axis = model.axis, !stages.isEmpty {
                Hypnogram(title: "Sleep stages of \(name), \(model.span(sessions))", stages: stages, rows: axis.rows, from: axis.from, to: axis.to, timeZone: model.timeZone)
                    .accessibilityIdentifier("hypnogram-\(index)")
            } else if !sessions.isEmpty {
                Text("This source reported no sleep stages.").foregroundStyle(.secondary)
            }
            Text(meta(sessions)).font(.footnote).foregroundStyle(.secondary).monospacedDigit()
            ForEach(sessions, id: \.id) { session in
                ProvenanceButton(entity: .sleep, id: session.id, request: $provenance)
            }
        }
    }

    private func meta(_ sessions: [SleepSession]) -> String {
        let asleep = "\(asleepText(member.values?.additionalProperties["sleep_total"])) asleep"
        return sessions.isEmpty ? asleep : "\(model.span(sessions)) · \(asleep)"
    }
}

/// Naps of the night's date that no source's episode counts.
private struct NapsSection: View {
    let model: NightModel
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        let naps = model.naps
        if !naps.isEmpty {
            Section("Naps") {
                ForEach(naps, id: \.id) { nap in
                    VStack(alignment: .leading, spacing: 4) {
                        SourceChip(provider: nap.source.provider, name: recordName(nap.source))
                        Text("\(model.span([nap])) · \(Format.duration(nap.endAt.timeIntervalSince(nap.startAt)))")
                            .font(.footnote).foregroundStyle(.secondary).monospacedDigit()
                        ProvenanceButton(entity: .sleep, id: nap.id, request: $provenance)
                    }
                    .accessibilityIdentifier("nap-\(nap.id)")
                }
            }
        }
    }
}

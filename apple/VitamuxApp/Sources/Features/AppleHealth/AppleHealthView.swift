import HealthBridgeHealthKit
import SwiftUI
import VitamuxKit

/// Apple Health (`vitamux://apple-health`, artboard AppleHealth): this iPhone as an Apple Health
/// source. Joins the phone's state (pairing, groups, anchors, last upload, waiting types) with the
/// server's view of the device (last seen, possibly-denied hints, requested resets). Moved from
/// Bridge's Pair, Metrics, Status and Settings tabs.
struct AppleHealthView: View {
    @Environment(AppState.self) private var state
    @State private var model = AppleHealthModel()

    var body: some View {
        let device = state.device
        List {
            if device.isPaired {
                PairedSections(device: device, model: model)
            } else {
                PairingSections(device: device)
            }
            Section {
                NavigationLink("What Vitamux reads and sends") { AppleHealthPrivacyView() }
                    .accessibilityIdentifier("privacyLink")
            }
        }
        .navigationTitle("Apple Health")
        .task(id: "\(device.credentials?.deviceID ?? "")-\(device.finishedRuns)") {
            await model.load(state.client, deviceID: device.credentials?.deviceID)
        }
    }
}

/// Status, Sync now, groups, per-type status, sources, the server's view and unpair.
private struct PairedSections: View {
    @Environment(AppState.self) private var state
    let device: ThisDevice
    let model: AppleHealthModel
    @State private var confirmPullAgain = false
    @State private var confirmUnpair = false
    @State private var unpairProblem: Problem?

    var body: some View {
        Section {
            Label(device.isRevoked ? "This iPhone is no longer paired" : "This iPhone is paired",
                  systemImage: device.isRevoked ? "xmark.seal" : "checkmark.seal.fill")
                .foregroundStyle(device.isRevoked ? .red : .green)
                .accessibilityIdentifier(device.isRevoked ? "revokedState" : "pairedState")
            LabeledContent("Server") {
                Text(device.credentials?.baseURL.host() ?? "").accessibilityIdentifier("pairedServer")
            }
            SyncSummary(device: device)
            if !device.isRevoked {
                Button {
                    Task { await device.syncNow() }
                } label: {
                    HStack {
                        Label("Sync now", systemImage: "arrow.triangle.2.circlepath")
                        if device.isSyncing {
                            Spacer()
                            ProgressView()
                        }
                    }
                }
                .disabled(device.isSyncing)
                .accessibilityIdentifier("syncNowButton")
                Button("Pull again…") { confirmPullAgain = true }
                    .disabled(device.enabledTypes.isEmpty)
                    .accessibilityIdentifier("pullAgainButton")
            }
        } footer: {
            Text("iOS decides when background syncs run, and some types update at most hourly. Data arrives eventually, not instantly.")
        }
        if device.isRevoked {
            RevokedSection(device: device)
        }
        if let problem = device.problem {
            Section { ProblemView(problem: problem) }
        }
        if !device.healthAvailable {
            Section { Text("Health data is not available on this device.").foregroundStyle(.secondary) }
        }
        GroupsSection(device: device) // GroupsSection.swift
        WatchCard(device: device) // WatchCard.swift
        if !device.enabledTypes.isEmpty {
            Section("Per type") {
                ForEach(MetricGroup.allCases.filter(device.enabled.contains), id: \.self) { group in
                    NavigationLink {
                        TypeStatusView(device: device, group: group, possiblyDenied: model.possiblyDenied)
                    } label: {
                        LabeledContent(group.title, value: "\(device.types(in: group).count) types")
                    }
                    .accessibilityIdentifier("types-\(group.rawValue)")
                }
            }
        }
        Section {
            // J22.25: Sources/AppleHealthSourcesView.swift.
            AppleHealthSourcesLink(device: device)
                .accessibilityIdentifier("appleHealthSources")
        } footer: {
            Text("Choose which apps' data Vitamux takes from Apple Health, so a provider you connect directly is not counted twice.")
        }
        ServerSection(model: model, signedInHere: signedInHere)
        Section {
            Button("Unpair this iPhone", role: .destructive) { confirmUnpair = true }
                .accessibilityIdentifier("unpairButton")
            if let unpairProblem { ProblemView(problem: unpairProblem) }
        } footer: {
            Text(signedInHere
                 ? "Unpairing revokes this iPhone's device token on the server and removes it from this phone. Data already on the server stays."
                 : "Unpairing removes the device token from this phone. Revoke the device in the Vitamux web panel of \(device.credentials?.baseURL.host() ?? "its server") too.")
        }
        .confirmationDialog("Pull every enabled type again?", isPresented: $confirmPullAgain, titleVisibility: .visible) {
            Button("Pull again") { Task { await device.resetAllAnchors() } }
                .accessibilityIdentifier("confirmPullAgain")
        } message: {
            Text("Every enabled type is read and sent in full. The server skips what it already has.")
        }
        .confirmationDialog("Unpair this iPhone?", isPresented: $confirmUnpair, titleVisibility: .visible) {
            Button("Unpair", role: .destructive) { Task { await unpair() } }
                .accessibilityIdentifier("confirmUnpair")
        }
    }

    private var signedInHere: Bool {
        device.credentials?.baseURL.host() == state.profile?.baseURL.host()
    }

    private func unpair() async {
        do {
            try await device.unpair(using: state.client, on: state.profile)
            unpairProblem = nil
        } catch {
            unpairProblem = Problem(error)
        }
    }
}

/// "Last sync 12 min ago · nothing waiting", the history pull's progress and an applied server reset.
private struct SyncSummary: View {
    let device: ThisDevice

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(summary)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("syncSummary")
            if let progress = device.progress {
                ProgressView(value: Double(progress.done), total: Double(max(progress.total, 1))) {
                    Text(device.historyPending.isEmpty ? "Syncing" : "Pulling history")
                        .font(.caption)
                } currentValueLabel: {
                    Text("\(progress.done) of \(progress.total) types")
                }
                .accessibilityIdentifier("syncProgress")
            }
            if let reset = device.lastAppliedReset {
                Text("Pulled again at the server's request \(reset.formatted(.relative(presentation: .named)))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("serverResetApplied")
            }
        }
    }

    private var summary: String {
        let last = device.lastUpload.map { "Last sync \($0.formatted(.relative(presentation: .named)))" } ?? "Not synced yet"
        let waiting = device.waiting.count
        let queue = waiting == 0 ? "nothing waiting" : "\(waiting) \(waiting == 1 ? "type" : "types") waiting to re-send"
        let groups = device.enabled.isEmpty ? "no groups on" : "background delivery on"
        return [last, queue, groups].joined(separator: " · ")
    }
}

/// The server answered 401 to the device token: sync has stopped until the owner pairs again.
private struct RevokedSection: View {
    @Environment(AppState.self) private var state
    let device: ThisDevice

    var body: some View {
        Section {
            Text("The server no longer accepts this iPhone's device token, so syncing has stopped. Nothing is lost: pairing again picks up where it stopped.")
                .accessibilityIdentifier("revokedMessage")
            if let client = state.client, let profile = state.profile {
                Button {
                    Task { await device.pairHere(using: client, on: profile) }
                } label: {
                    HStack {
                        Label("Pair again", systemImage: "arrow.clockwise")
                        if device.isPairing {
                            Spacer()
                            ProgressView()
                        }
                    }
                }
                .disabled(device.isPairing)
                .accessibilityIdentifier("repairButton")
            }
        }
    }
}

/// What the server knows about this iPhone.
private struct ServerSection: View {
    let model: AppleHealthModel
    let signedInHere: Bool

    var body: some View {
        if signedInHere {
            Section {
                switch model.server {
                case .loading:
                    ProgressView()
                case .failed(let problem):
                    ProblemView(problem: problem)
                case .loaded(nil):
                    Text("This server does not list this iPhone.").foregroundStyle(.secondary)
                case .loaded(let device?):
                    LabeledContent("Last seen", value: device.lastSeenAt.map(relative) ?? "Never")
                    LabeledContent("Last batch", value: device.lastSyncAt.map(relative) ?? "None yet")
                    LabeledContent("Types reported") {
                        Text("\(device.types.count)").accessibilityIdentifier("serverTypes")
                    }
                    if !device.possiblyDenied.isEmpty {
                        LabeledContent("Possibly denied") {
                            Text("\(device.possiblyDenied.count)").accessibilityIdentifier("possiblyDenied")
                        }
                    }
                }
            } header: {
                Text("On the server")
            } footer: {
                Text("A type that stays silent for 7 days is flagged \"possibly denied\": iOS may have denied it, or there was nothing new. Check Settings › Health › Data Access & Devices.")
            }
        }
    }

    private func relative(_ date: Date) -> String {
        date.formatted(.relative(presentation: .named))
    }
}

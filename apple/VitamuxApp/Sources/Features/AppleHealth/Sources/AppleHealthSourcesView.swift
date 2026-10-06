import SwiftUI
import VitamuxKit

/// Apple Health › Sources (`vitamux://apple-health/sources`, J22.25): one section per app seen in
/// Apple Health with what it writes, its classification, Take or Ignore, "Choose types…", why a
/// default ignores it, and a link to Explore filtered to that origin. Ignored data never leaves
/// this iPhone; after a change the phone syncs so the filter applies at once.
struct AppleHealthSourcesView: View {
    @Environment(AppState.self) private var state
    @State private var model = AppleHealthSourcesModel()

    var body: some View {
        let device = state.device
        List {
            if !device.isPaired {
                Text("Pair this iPhone in Apple Health first; the apps it finds are listed here.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("sourcesNotPaired")
            } else if !signedInHere {
                Text("Sign in to \(device.credentials?.baseURL.host() ?? "the server") to choose its sources: this iPhone is paired with it.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("sourcesOtherServer")
            } else {
                switch model.filter {
                case .loading:
                    ProgressView("Loading the apps").frame(maxWidth: .infinity)
                case .failed(let problem):
                    ProblemView(problem: problem)
                case .loaded(let filter):
                    SourcesIntro(filter: filter, problem: model.problem)
                    ForEach(filter.origins, id: \.bundleId) { origin in
                        OriginSection(origin: origin, model: model)
                    }
                }
            }
        }
        .navigationTitle("Sources")
        .refreshable { await load() }
        .task { await load() }
    }

    private var signedInHere: Bool {
        guard let paired = state.device.credentials?.baseURL.host() else { return false }
        return paired == state.profile?.baseURL.host()
    }

    private func load() async {
        guard signedInHere else { return }
        await model.load(state.client, deviceID: state.device.credentials?.deviceID)
    }
}

/// What the screen does, when the phone last reported its apps, and a failed change.
private struct SourcesIntro: View {
    let filter: AppleHealthSourcesModel.Filter
    let problem: Problem?

    var body: some View {
        Section {
            if let problem {
                ProblemView(problem: problem).accessibilityIdentifier("sourcesProblem")
            }
            if filter.origins.isEmpty {
                Text("No apps reported yet. Turn on a data group and sync; the apps that wrote its types appear here.")
                    .foregroundStyle(.secondary)
            }
        } footer: {
            VStack(alignment: .leading, spacing: 4) {
                Text("Take or ignore each app's data in Apple Health. Ignored data stays on this iPhone and is never read, so a provider you connect directly is not counted twice. Taking an app again pulls its history.")
                if let reported = filter.sourcesReportedAt {
                    Text("Apps found \(reported.formatted(.relative(presentation: .named)))")
                }
            }
        }
    }
}

/// One app: icon tile, name, what it writes, classification, Take or Ignore, the default's
/// reason, your choice or the default, "Choose types…" and "Show in Explore".
private struct OriginSection: View {
    @Environment(AppState.self) private var state
    let origin: AppleHealthSourcesModel.Origin
    let model: AppleHealthSourcesModel
    @State private var choosingTypes = false

    var body: some View {
        Section {
            OriginHeader(origin: origin)
            Picker("Data from \(origin.title)", selection: Binding<AppleHealthSourcesModel.Pick?> {
                switch origin.mode {
                case .take: .take
                case .ignore: .ignore
                case .perType: nil
                }
            } set: { pick in
                if let pick { save(pick) }
            }) {
                Text("Take").tag(AppleHealthSourcesModel.Pick?.some(.take))
                Text("Ignore").tag(AppleHealthSourcesModel.Pick?.some(.ignore))
            }
            .pickerStyle(.segmented)
            .disabled(model.saving != nil)
            .accessibilityIdentifier("mode-\(origin.bundleId)")
            if origin.mode == .perType {
                Text("Takes \(origin.types.map(healthTypeLabel).joined(separator: ", ")) only")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("perType-\(origin.bundleId)")
            }
            if !origin.explicit, origin.defaultMode == .ignore, origin.defaultReason == .directConnection {
                Text("You get \(origin.reasonProviderName ?? origin.reasonProvider.map(providerLabel) ?? "this provider") directly; its copy in Apple Health would count twice.")
                    .font(.subheadline)
                    .accessibilityIdentifier("reason-\(origin.bundleId)")
            }
            if origin.ignoredRecords > 0 {
                Text("\(origin.ignoredRecords) records held raw on the server, not used")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            ChoiceRow(origin: origin, saving: model.saving == origin.bundleId) { save(.useDefault) }
            if !origin.choosableTypes.isEmpty {
                Button("Choose types…") { choosingTypes = true }
                    .disabled(model.saving != nil)
                    .accessibilityIdentifier("chooseTypes-\(origin.bundleId)")
                    // On the row, not the section: a section spreads its modifiers over its rows.
                    .sheet(isPresented: $choosingTypes) {
                        TypesSheet(origin: origin) { save(.types($0)) }
                    }
            }
            Button {
                state.open(.explore(origin: origin.bundleId))
            } label: {
                Label("Show in Explore", systemImage: "safari")
            }
            .accessibilityIdentifier("explore-\(origin.bundleId)")
        }
    }

    private func save(_ pick: AppleHealthSourcesModel.Pick) {
        Task {
            let device = state.device
            guard await model.set(pick, for: origin, client: state.client, deviceID: device.credentials?.deviceID) else { return }
            await device.syncNow()
        }
    }
}

/// The icon tile, name, what it writes and the classification.
private struct OriginHeader: View {
    let origin: AppleHealthSourcesModel.Origin

    var body: some View {
        HStack(spacing: 12) {
            IconTile(origin: origin)
            VStack(alignment: .leading, spacing: 2) {
                Text(origin.title).font(.headline)
                Text(origin.writesText)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("writes-\(origin.bundleId)")
                Text(origin.classificationText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("classification-\(origin.bundleId)")
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("origin-\(origin.bundleId)")
    }
}

/// The first letter of the name on a tinted rounded square; Apple's own sources get the heart.
private struct IconTile: View {
    let origin: AppleHealthSourcesModel.Origin

    var body: some View {
        let tint = origin.isNative ? Color.pink : Self.tint(origin.bundleId)
        ZStack {
            RoundedRectangle(cornerRadius: 9, style: .continuous).fill(tint.opacity(0.18))
            if origin.isNative {
                Image(systemName: "heart.fill").foregroundStyle(tint)
            } else {
                Text(origin.title.prefix(1).uppercased()).font(.headline).foregroundStyle(tint)
            }
        }
        .frame(width: 40, height: 40)
        .accessibilityHidden(true)
    }

    /// A stable tint per bundle id.
    private static func tint(_ bundleID: String) -> Color {
        let palette: [Color] = [.blue, .green, .orange, .purple, .teal, .indigo, .brown]
        let sum = bundleID.unicodeScalars.reduce(0) { $0 &+ Int($1.value) }
        return palette[sum % palette.count]
    }
}

/// "Your choice" with "Use default", or "Default".
private struct ChoiceRow: View {
    let origin: AppleHealthSourcesModel.Origin
    let saving: Bool
    let useDefault: () -> Void

    var body: some View {
        HStack {
            Text(origin.explicit ? "Your choice" : "Default")
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("choice-\(origin.bundleId)")
            Spacer()
            if saving {
                ProgressView()
            } else if origin.explicit {
                Button("Use default", action: useDefault)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("useDefault-\(origin.bundleId)")
            }
        }
    }
}

/// "Choose types…": one toggle per type the app writes. All on takes the app, none ignores it.
private struct TypesSheet: View {
    @Environment(\.dismiss) private var dismiss
    let origin: AppleHealthSourcesModel.Origin
    let save: (Set<String>) -> Void
    @State private var chosen: Set<String>

    init(origin: AppleHealthSourcesModel.Origin, save: @escaping (Set<String>) -> Void) {
        self.origin = origin
        self.save = save
        // Seeded once from the origin; the sheet is rebuilt each time it opens.
        _chosen = State(initialValue: origin.takenTypes)
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(origin.choosableTypes, id: \.self) { type in
                        Toggle(healthTypeLabel(type), isOn: Binding {
                            chosen.contains(type)
                        } set: { on in
                            if on { chosen.insert(type) } else { chosen.remove(type) }
                        })
                        .accessibilityIdentifier("type-\(type)")
                    }
                } footer: {
                    Text("Vitamux takes the types switched on from \(origin.title) and ignores the rest.")
                }
            }
            .navigationTitle(origin.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        save(chosen)
                        dismiss()
                    }
                    .accessibilityIdentifier("saveTypes")
                }
            }
        }
    }
}

/// The Apple Health screen's row: opens this screen, with a short summary of the filter.
struct AppleHealthSourcesLink: View {
    @Environment(AppState.self) private var state
    let device: ThisDevice
    @State private var model = AppleHealthSourcesModel()

    var body: some View {
        NavigationLink {
            AppleHealthSourcesView()
        } label: {
            LabeledContent {
                if let summary = model.summary {
                    Text(summary).accessibilityIdentifier("sourcesSummary")
                }
            } label: {
                Label("Sources", systemImage: "app.badge.checkmark")
            }
        }
        .task(id: "\(device.credentials?.deviceID ?? "")-\(device.finishedRuns)") {
            guard let host = device.credentials?.baseURL.host(), host == state.profile?.baseURL.host() else { return }
            await model.load(state.client, deviceID: device.credentials?.deviceID)
        }
    }
}

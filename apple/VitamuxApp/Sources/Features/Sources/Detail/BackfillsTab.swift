import SwiftUI
import VitamuxKit

/// A connection's backfills, newest first, with unit progress (the panel's BackfillsTab.svelte):
/// start one, retry failed units (all, or one from the unit list) and cancel. Refreshes every 5 s
/// while one runs and the tab is visible.
@Observable
final class BackfillsModel {
    private(set) var backfills: Loadable<[Backfill]> = .loading
    private(set) var streams: [String] = []
    private(set) var units: [String: [Components.Schemas.BackfillUnit]] = [:]
    private(set) var problem: Problem?
    private(set) var message = ""
    private(set) var busy = false

    var isRunning: Bool { backfills.value?.contains { $0.status == .running } ?? false }

    func load(_ client: Client?, id: String) async {
        guard let client else { return }
        let answer = await Loadable { try await client.listBackfills(path: .init(id: id)).ok.body.json.backfills }
        if answer.value != nil || backfills.value == nil { backfills = answer }
        for open in units.keys { await showUnits(open, client: client, id: id) }
    }

    func loadStreams(_ client: Client?, id: String) async {
        guard let client else { return }
        streams = (try? await client.listConnectionStreams(path: .init(id: id)).ok.body.json.streams.map(\.name)) ?? []
    }

    func toggleUnits(_ backfill: Backfill, client: Client?, id: String) async {
        if units[backfill.id] != nil {
            units[backfill.id] = nil
        } else if let client {
            await showUnits(backfill.id, client: client, id: id)
        }
    }

    private func showUnits(_ backfill: String, client: Client, id: String) async {
        do {
            units[backfill] = try await client.getBackfill(path: .init(id: id, backfillId: backfill)).ok.body.json.units ?? []
        } catch {
            problem = Problem(error)
        }
    }

    func cancel(_ b: Backfill, client: Client?, id: String) async {
        await act(client, id: id) {
            let done = try await $0.cancelBackfill(path: .init(id: id, backfillId: b.id)).ok.body.json
            return "Backfill of \(done.stream) cancelled; data fetched so far stays."
        }
    }

    func retry(_ b: Backfill, unit: Date? = nil, client: Client?, id: String) async {
        await act(client, id: id) {
            let body = Components.Schemas.BackfillRetryInput(unitStart: unit)
            let done = try await $0.retryBackfill(path: .init(id: id, backfillId: b.id), body: .json(body)).ok.body.json
            return "Failed units of \(done.stream) queued again."
        }
    }

    func started(_ b: Backfill, client: Client?, id: String) async {
        message = "Backfill of \(b.stream) started."
        await load(client, id: id)
    }

    private func act(_ client: Client?, id: String, _ work: (Client) async throws -> String) async {
        guard let client else { return }
        busy = true
        problem = nil
        message = ""
        do {
            message = try await work(client)
        } catch {
            problem = Problem(error)
        }
        busy = false
        await load(client, id: id)
    }
}

struct BackfillsTab: View {
    @Environment(AppState.self) private var state
    @State private var model = BackfillsModel()
    @State private var starting = false
    let connection: Connection

    var body: some View {
        Section {
            Button("New backfill", systemImage: "plus") { starting = true }
                .disabled(connection.mode == .push || model.streams.isEmpty)
                .accessibilityIdentifier("newBackfill")
                // On the row, not on the Section, so the list presents it once.
                .sheet(isPresented: $starting) {
                    BackfillSheet(connection: connection, streams: model.streams) { created in
                        Task { await model.started(created, client: state.client, id: connection.id) }
                    }
                }
                .task {
                    await model.loadStreams(state.client, id: connection.id)
                    await model.load(state.client, id: connection.id)
                }
                // Polls only while a backfill runs and this tab is on screen.
                .task(id: model.isRunning) {
                    guard model.isRunning else { return }
                    await Poller.backfills.run {
                        await model.load(state.client, id: connection.id)
                        return model.isRunning
                    }
                }
            if connection.mode == .push {
                Text("Push sources upload their own history.").font(.footnote).foregroundStyle(.secondary)
            }
            if let problem = model.problem { ProblemRow(problem: problem) }
            if !model.message.isEmpty { NoticeRow(text: model.message, identifier: "backfillMessage") }
        }
        switch model.backfills {
        case .loading:
            Section { ProgressView("Loading backfills") }
        case .failed(let problem):
            Section { ProblemRow(problem: problem) }
        case .loaded(let list):
            if list.isEmpty {
                Section {
                    Text("No backfills yet. A backfill fetches history older than the regular sync window.").foregroundStyle(.secondary)
                }
            }
            ForEach(list, id: \.id) { backfill in
                BackfillSection(backfill: backfill, model: model, connection: connection)
            }
        }
    }
}

private struct BackfillSection: View {
    @Environment(AppState.self) private var state
    let backfill: Backfill
    let model: BackfillsModel
    let connection: Connection

    var body: some View {
        Section {
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text(backfill.stream).font(.subheadline.monospaced().weight(.semibold))
                    Spacer()
                    Label {
                        Text(backfill.status.rawValue)
                    } icon: {
                        StatusIcon(kind: icon(backfill.status.rawValue))
                    }
                    .font(.subheadline)
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("backfillStatus-\(backfill.stream)")
                }
                Text("\(Format.date(backfill.start)) – \(Format.date(backfill.end)) · started \(SourcesCopy.when(backfill.createdAt))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                ProgressView(value: Double(backfill.unitCounts.done), total: Double(max(backfill.total, 1)))
                    .accessibilityLabel("Units done for \(backfill.stream)")
                Text(progress).font(.footnote).accessibilityIdentifier("backfillProgress-\(backfill.stream)")
            }
            HStack(spacing: 12) {
                if backfill.unitCounts.failed > 0 && backfill.status != .cancelled {
                    Button("Retry failed") { Task { await model.retry(backfill, client: state.client, id: connection.id) } }
                        .accessibilityIdentifier("retryBackfill-\(backfill.stream)")
                }
                if backfill.status == .running || backfill.status == .failed {
                    Button("Cancel") { Task { await model.cancel(backfill, client: state.client, id: connection.id) } }
                        .accessibilityIdentifier("cancelBackfill-\(backfill.stream)")
                }
                Spacer()
                Button(model.units[backfill.id] == nil ? "Units" : "Hide units") {
                    Task { await model.toggleUnits(backfill, client: state.client, id: connection.id) }
                }
                .accessibilityIdentifier("units-\(backfill.stream)")
            }
            .buttonStyle(.borderless)
            .disabled(model.busy)
            ForEach(model.units[backfill.id] ?? [], id: \.start) { unit in
                HStack {
                    StatusIcon(kind: icon(unit.status.rawValue))
                    VStack(alignment: .leading) {
                        Text("\(Format.date(unit.start)) – \(Format.date(unit.end))").font(.footnote)
                        Text("\(unit.status.rawValue) · \(Format.plural(unit.attempts, "attempt"))" + (unit.errorClass.map { " · \($0)" } ?? ""))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    if unit.status == .failed && backfill.status != .cancelled {
                        Button("Retry") { Task { await model.retry(backfill, unit: unit.start, client: state.client, id: connection.id) } }
                            .buttonStyle(.borderless)
                            .accessibilityLabel("Retry unit from \(Format.date(unit.start))")
                            .accessibilityIdentifier("retryUnit")
                    }
                }
                .accessibilityElement(children: .contain)
            }
        }
    }

    private var progress: String {
        var out = backfill.progressText
        if let limit = backfill.dailyLimit { out += ", at most \(limit) a day, the rest wait for the next day" }
        return out
    }

    private func icon(_ status: String) -> StatusKind {
        switch status {
        case "running", "pending": .pending
        case "done": .ok
        case "failed": .error
        case "cancelled": .off
        default: .info
        }
    }
}

/// Start a backfill: one stream over a date range. The server splits the range into units of the
/// connector's size, queues one job per unit and resumes failed units on retry.
private struct BackfillSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let connection: Connection
    let streams: [String]
    let onCreated: (Backfill) -> Void

    @State private var stream = ""
    @State private var start = Calendar.current.date(byAdding: .year, value: -1, to: .now)!
    @State private var end = Date.now
    @State private var problem: Problem?
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Picker("Stream", selection: $stream) {
                    ForEach(streams, id: \.self) { Text($0).tag($0) }
                }
                .accessibilityIdentifier("backfillStream")
                if let error = problem?.detail(for: "/stream") { Text(error).font(.footnote).foregroundStyle(Color.feedbackError) }
                Section {
                    DatePicker("From", selection: $start, in: ...Date.now, displayedComponents: .date)
                        .accessibilityIdentifier("backfillFrom")
                    DatePicker("To (inclusive)", selection: $end, in: ...Date.now, displayedComponents: .date)
                        .accessibilityIdentifier("backfillTo")
                } footer: {
                    Text("Today means until now.")
                }
                Section {
                    if let slow = SourcesCopy.paced(stream) { Text(slow.note).font(.footnote).foregroundStyle(.secondary) }
                    Text(plan + " Each unit runs as its own job and can be retried; data fetched before a cancel stays.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                    if let localError { NoticeRow(text: localError, kind: .error, identifier: "backfillLocalError") }
                    if let problem, problem.fieldErrors.isEmpty { ProblemRow(problem: problem) }
                }
                Section {
                    Button("Start backfill") { Task { await submit() } }
                        .disabled(busy || stream.isEmpty || localError != nil)
                        .accessibilityIdentifier("startBackfill")
                }
            }
            .navigationTitle("Backfill \(providerLabel(connection.provider))")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
            .onAppear { if stream.isEmpty { stream = streams.first ?? "" } }
        }
        .sheetBackground()
    }

    private var calendar: Calendar { .current }
    private var startDay: Date { calendar.startOfDay(for: start) }
    private var endDay: Date { calendar.startOfDay(for: end) }

    private var localError: String? {
        endDay < startDay ? "The end must be on or after the start." : nil
    }

    private var days: Int {
        (calendar.dateComponents([.day], from: startDay, to: endDay).day ?? 0) + 1
    }

    private var plan: String {
        if let slow = SourcesCopy.paced(stream) {
            return "\(days) days, one per unit, at most \(slow.perDay) a day (about \((days + slow.perDay - 1) / slow.perDay) days to finish)."
        }
        if let unit = SourcesCopy.unitDays(connection.provider) {
            return "\(days) days, fetched in units of \(unit) days (about \((days + unit - 1) / unit) units)."
        }
        return "\(days) days, fetched in units chosen by the connector."
    }

    private func submit() async {
        guard let client = state.client, localError == nil else { return }
        busy = true
        problem = nil
        defer { busy = false }
        // The end is exclusive on the server; an end of today means "until now".
        let today = calendar.startOfDay(for: .now)
        let endAt = endDay < today ? calendar.date(byAdding: .day, value: 1, to: endDay) : nil
        let body = Components.Schemas.BackfillInput(stream: stream, start: startDay, end: endAt, dailyLimit: SourcesCopy.paced(stream)?.perDay)
        do {
            let created = try await client.createBackfill(path: .init(id: connection.id), body: .json(body)).accepted.body.json
            dismiss()
            onCreated(created)
        } catch {
            problem = Problem(error)
        }
    }
}

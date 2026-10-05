import Foundation
import Observation
import VitamuxKit

/// The dashboard (the panel's `/`, web/src/routes/(app)/+page.svelte): the layout
/// (`GET /settings/dashboard`), each card's summary for the chosen day (`GET /resolved/summary`,
/// 20 metrics per request), the alert sources (`GET /connections`, `/jobs?status=dead`,
/// `/system/status`) and edit mode, saved with `PUT /settings/dashboard`. A 404 or 503 leaves its
/// section empty; nothing else on the screen fails with it.
@Observable
final class DashboardModel {
    typealias Summary = Components.Schemas.MetricSummary

    /// The day asked for; nil is the owner's today.
    private(set) var requested: LocalDate?
    private(set) var layout: Loadable<Components.Schemas.DashboardLayout> = .loading
    private(set) var catalogue: [Components.Schemas.Metric] = []
    private(set) var summaries: [String: Summary] = [:]
    /// What each card shows, from its summary; kept with the summaries.
    private(set) var contents: [String: CardContent] = [:]
    private(set) var summaryProblem: Problem?
    /// The day the summaries answered for, and the owner's timezone on it.
    private(set) var shownDate: LocalDate?
    private(set) var timeZoneName = ""
    private(set) var connections: Loadable<[Components.Schemas.Connection]> = .loading
    private(set) var alerts: [DashboardAlert] = []

    /// Edit mode's working copy of the cards; nil outside edit mode.
    var draft: [DashboardCard]?
    private(set) var isSaving = false
    private(set) var saveProblem: Problem?

    private var jobs: [Components.Schemas.Job] = []
    private var lastBackup: Date?
    private var asked: Set<String> = []

    init(date: String?) {
        requested = date.flatMap(LocalDate.init)
    }

    // MARK: Derived

    var timeZone: TimeZone {
        TimeZone(identifier: timeZoneName) ?? .current
    }

    /// The owner's today: the server's answer for today, else the clock in the owner's timezone.
    var today: LocalDate {
        if requested == nil, let shownDate { return shownDate }
        return LocalDate.today(in: timeZone)
    }

    var day: LocalDate { requested ?? today }
    var isToday: Bool { requested == nil }

    var cards: [DashboardCard] { draft ?? layout.value?.cards ?? [] }
    var visible: [DashboardCard] { cards.filter { !$0.hidden } }

    /// Every visible card has its summary (or the summaries failed).
    var isReady: Bool { summaryProblem != nil || visible.allSatisfy { contents[$0.metric] != nil } }

    /// Outside edit mode a card with no data waits, hidden, until a source provides it.
    var shown: [DashboardCard] {
        visible.filter { contents[$0.metric]?.hasData == true }
    }

    /// The layout or the summaries are not served yet (404 or 503).
    var isUnavailable: Bool {
        layout.problem?.isUnavailable == true || summaryProblem?.isUnavailable == true
    }

    var canCustomize: Bool {
        layout.value != nil && summaryProblem == nil
    }

    func section(of code: String) -> String? {
        catalogue.first { $0.code == code }?.section
    }

    private func isAdditive(_ code: String) -> Bool {
        catalogue.first { $0.code == code }?.aggregation == .additive
    }

    // MARK: Loading

    /// Loads everything; pull to refresh calls it again.
    func load(_ client: Client) async {
        async let layout = Loadable { try await client.getDashboardLayout().ok.body.json }
        async let catalogue = try? client.listMetrics().ok.body.json.metrics
        async let connections = Loadable { try await client.listConnections().ok.body.json.connections }
        async let jobs = try? client.listJobs(query: .init(status: "dead", limit: 20)).ok.body.json.value2.jobs
        async let status = try? client.getSystemStatus().ok.body.json

        self.layout = await layout
        resetSummaries()
        async let summaries: Void = loadSummaries(client)
        if let catalogue = await catalogue {
            self.catalogue = catalogue
            updateContents()
        }
        self.connections = await connections
        let weekAgo = Date.now.addingTimeInterval(-7 * 86_400)
        self.jobs = (await jobs ?? []).filter { ($0.finishedAt ?? $0.createdAt) >= weekAgo }
        lastBackup = await status?.lastBackupAt
        updateAlerts()
        await summaries
    }

    /// Shows another day (nil: today); the future is not offered.
    func choose(_ day: LocalDate?, _ client: Client) async {
        let next = day.flatMap { $0 >= today ? nil : $0 }
        guard next != requested else { return }
        requested = next
        resetSummaries()
        await loadSummaries(client)
    }

    private func resetSummaries() {
        summaries = [:]
        contents = [:]
        asked = []
        summaryProblem = nil
    }

    /// Fetches the summaries of visible cards not asked for yet, 20 metrics per request.
    func loadSummaries(_ client: Client) async {
        guard layout.value != nil else { return }
        let need = visible.map(\.metric).filter { !asked.contains($0) }
        guard !need.isEmpty else { return }
        asked.formUnion(need)
        let day = requested
        let chunks = stride(from: 0, to: need.count, by: 20).map { Array(need[$0..<min($0 + 20, need.count)]) }
        let answers = await withTaskGroup(of: Result<Components.Schemas.ResolvedSummary, Problem>.self) { group in
            for chunk in chunks {
                group.addTask {
                    do {
                        return .success(try await client.getResolvedSummary(query: .init(metrics: chunk, date: day?.description)).ok.body.json)
                    } catch {
                        return .failure(Problem(error))
                    }
                }
            }
            var out: [Result<Components.Schemas.ResolvedSummary, Problem>] = []
            for await answer in group { out.append(answer) }
            return out
        }
        guard day == requested else { return } // another day was chosen meanwhile
        for answer in answers {
            switch answer {
            case .success(let summary):
                summaries.merge(summary.metrics.additionalProperties) { $1 }
                if let date = LocalDate(summary.date) { shownDate = date }
                timeZoneName = summary.timezone
            case .failure(let problem):
                summaryProblem = problem
            }
        }
        updateContents()
    }

    /// Additive metrics draw bars, which needs the catalogue.
    private func updateContents() {
        contents = summaries.reduce(into: [:]) { out, entry in
            out[entry.key] = CardContent(code: entry.key, summary: entry.value, additive: isAdditive(entry.key))
        }
    }

    private func updateAlerts() {
        let all = DashboardAlert.alerts(connections: connections.value ?? [], jobs: jobs, lastBackup: lastBackup)
        let dismissed = Set(layout.value?.dismissed ?? [])
        alerts = all.filter { !dismissed.contains($0.key) }
    }

    // MARK: Edit mode

    func customize() {
        draft = layout.value?.cards
        saveProblem = nil
    }

    func cancelEditing() {
        draft = nil
        saveProblem = nil
    }

    func setSize(_ size: CardSize, of metric: String) {
        update(metric) { $0.size = size }
    }

    func setHidden(_ hidden: Bool, _ metric: String) {
        update(metric) { $0.hidden = hidden }
    }

    func move(fromOffsets source: IndexSet, toOffset destination: Int) {
        draft = draft.map { DashboardLayout.move($0, fromOffsets: source, toOffset: destination) }
    }

    func move(_ metric: String, by step: Int) {
        draft = draft.map { DashboardLayout.move($0, metric, by: step) }
    }

    func pin(_ metric: String) {
        guard draft?.contains(where: { $0.metric == metric }) == false else { return }
        draft?.append(DashboardCard(metric: metric, size: .s, hidden: false))
    }

    func unpin(_ metric: String) {
        draft?.removeAll { $0.metric == metric }
    }

    /// The curated cards; the panel's hero tiles stay as they are (the phone has none).
    func resetToDefault() {
        draft = DashboardLayout.defaultCards
    }

    /// Saves the cards with the layout's hero tiles and dismissed alerts unchanged.
    func save(_ client: Client) async {
        guard let draft, let current = layout.value else { return }
        isSaving = true
        defer { isSaving = false }
        saveProblem = nil
        let input = Components.Schemas.DashboardLayoutInput(version: ._1, cards: draft, hero: current.hero, dismissed: current.dismissed)
        do {
            layout = .loaded(try await client.putDashboardLayout(body: .json(input)).ok.body.json)
            self.draft = nil
            updateAlerts()
            await loadSummaries(client)
        } catch {
            saveProblem = Problem(error)
        }
    }

    private func update(_ metric: String, _ change: (inout DashboardCard) -> Void) {
        guard let index = draft?.firstIndex(where: { $0.metric == metric }) else { return }
        change(&draft![index])
    }
}

extension Loadable {
    var problem: Problem? {
        if case .failed(let problem) = self { problem } else { nil }
    }
}

extension Problem {
    /// The endpoint is not available yet: the section stays empty instead of failing the screen.
    var isUnavailable: Bool { status == 404 || status == 503 }
}

import SwiftUI
import VitamuxKit

/// Tab root (`vitamux://dashboard?date=`): the day at a glance, arranged like the owner's panel
/// dashboard. Day context, alerts, the cards of the shared layout and the sources' health; the
/// toolbar's Customize opens edit mode.
struct DashboardView: View {
    @Environment(AppState.self) private var state
    /// The link's day; a later link with another day replaces the shown one.
    let date: String?
    /// Seeded once with the link's day; `onChange(of: date)` follows later links.
    @State private var model: DashboardModel

    init(date: String?) {
        self.date = date
        _model = State(initialValue: DashboardModel(date: date))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Space.stack) {
                DayHeader(model: model) { choose($0) }
                AlertList(model: model)
                CardsSection(model: model)
                SourcesSection(connections: model.connections)
            }
            .padding(.horizontal, Space.gutter)
            .padding(.bottom, 24)
        }
        .background(Color.ground)
        .navigationTitle("Dashboard")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button("Customize", systemImage: "slider.horizontal.3") { model.customize() }
                    .disabled(!model.canCustomize)
                    .accessibilityIdentifier("customizeButton")
            }
        }
        .refreshable { await ResponseCache.refreshing { await load() } }
        .task {
            if case .loading = model.layout { await load() }
        }
        .onChange(of: date) { _, new in choose(new.flatMap(LocalDate.init)) }
        // Today's cards for the widgets, after a load, another day back to today, or an edit.
        .onChange(of: model.contents) { publishToWidgets() }
        .sheet(isPresented: Binding { model.draft != nil } set: { if !$0 { model.cancelEditing() } }) {
            DashboardEditView(model: model)
        }
    }

    private func load() async {
        guard let client = state.client else { return }
        await model.load(client)
        publishToWidgets()
    }

    private func publishToWidgets() {
        WidgetSnapshotWriter.write(model, status: state.cache.status)
    }

    private func choose(_ day: LocalDate?) {
        guard let client = state.client else { return }
        Task { await model.choose(day, client) }
    }
}

// MARK: - Day context

/// "Today", the full date and the owner's timezone, and previous, picker, next and Today.
private struct DayHeader: View {
    let model: DashboardModel
    let choose: (LocalDate?) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(.largeTitle.bold())
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityIdentifier("dayTitle")
                Text(subtitle)
                    .font(.subheadline)
                    .foregroundStyle(Color.inkMuted)
                    .accessibilityIdentifier("dayLabel")
                    .accessibilityValue(model.day.description)
            }
            HStack(spacing: 8) {
                Button("Previous day", systemImage: "chevron.backward") { choose(model.day.adding(days: -1)) }
                    .labelStyle(.iconOnly)
                    .accessibilityIdentifier("previousDay")
                DatePicker("Day shown", selection: picked, in: ...model.today.start(in: model.timeZone), displayedComponents: .date)
                    .labelsHidden()
                    .environment(\.timeZone, model.timeZone)
                    .accessibilityIdentifier("dayPicker")
                Button("Next day", systemImage: "chevron.forward") { choose(model.day.adding(days: 1)) }
                    .labelStyle(.iconOnly)
                    .disabled(model.isToday)
                    .accessibilityIdentifier("nextDay")
                if !model.isToday {
                    Button("Today") { choose(nil) }
                        .accessibilityIdentifier("todayButton")
                }
            }
            .buttonStyle(.bordered)
        }
        .padding(.top, 8)
    }

    private var picked: Binding<Date> {
        Binding {
            model.day.start(in: model.timeZone)
        } set: {
            choose(LocalDate($0, in: model.timeZone))
        }
    }

    private var title: String {
        if model.isToday { return "Today" }
        if model.day == model.today.adding(days: -1) { return "Yesterday" }
        return noon.formatted(Date.FormatStyle(timeZone: .gmt).weekday(.wide))
    }

    private var subtitle: String {
        let date = noon.formatted(Date.FormatStyle(timeZone: .gmt).weekday(.wide).day().month(.wide).year())
        return model.timeZoneName.isEmpty ? date : "\(date) · Resolved values for \(model.timeZoneName)"
    }

    /// The shown day as an instant that names it in UTC.
    private var noon: Date {
        model.day.start(in: .gmt).addingTimeInterval(12 * 3600)
    }
}

// MARK: - Alerts

private struct AlertList: View {
    @Environment(AppState.self) private var state
    let model: DashboardModel

    var body: some View {
        if !model.alerts.isEmpty {
            VStack(spacing: 8) {
                ForEach(model.alerts) { alert in
                    AlertRow(alert: alert) { state.open($0) }
                }
            }
        } else if case .loaded = model.connections {
            Label {
                Text("Nothing needs your attention.").foregroundStyle(Color.inkMuted)
            } icon: {
                StatusIcon(kind: .ok)
            }
            .font(.footnote)
        }
    }
}

private struct AlertRow: View {
    let alert: DashboardAlert
    let open: (Route) -> Void

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: alert.level == .error ? "xmark.octagon.fill" : "exclamationmark.triangle.fill")
                .font(.title3)
                .foregroundStyle(color)
                .accessibilityHidden(true)
            Text(alert.text)
                .font(.subheadline)
                .foregroundStyle(color)
                .frame(maxWidth: .infinity, alignment: .leading)
            if let action = alert.action, let route = alert.route {
                Button { open(route) } label: { Text(action).tapTarget() }
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(color)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .frame(minHeight: 52)
        .background(fill, in: .rect(cornerRadius: Radius.tile, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Radius.tile, style: .continuous).strokeBorder(color.opacity(0.3)))
        .accessibilityElement(children: .contain)
    }

    private var color: Color { alert.level == .error ? .feedbackError : .feedbackWarn }
    private var fill: Color { alert.level == .error ? .feedbackErrorSoft : .feedbackWarnSoft }
}

// MARK: - Cards

private struct CardsSection: View {
    @Environment(AppState.self) private var state
    let model: DashboardModel

    var body: some View {
        if case .loading = model.layout {
            ProgressView().accessibilityLabel("Loading").frame(maxWidth: .infinity, minHeight: 120)
        } else if model.isUnavailable {
            Text("Resolved values are not available yet.")
                .foregroundStyle(.secondary)
                .accessibilityIdentifier("cardsUnavailable")
        } else if let problem = model.layout.problem ?? model.summaryProblem {
            ProblemView(problem: problem)
        } else if model.visible.isEmpty {
            EmptyCards(title: "No cards on the dashboard", text: "Every card is hidden, or the layout is empty. Customize it to pin metrics.",
                       action: "Customize") { model.customize() }
        } else if !model.isReady {
            ProgressView().accessibilityLabel("Loading").frame(maxWidth: .infinity, minHeight: 120)
        } else if model.shown.isEmpty {
            EmptyCards(title: "No data yet", text: "Metrics appear here once a source provides them.", action: "Connect a source") {
                state.open(.connections())
            }
        } else {
            CardGrid(model: model)
        }
    }
}

private struct EmptyCards: View {
    let title: String
    let text: String
    let action: String
    let perform: () -> Void

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: "square.grid.2x2")
        } description: {
            Text(text)
        } actions: {
            Button(action, action: perform)
                .buttonStyle(.borderedProminent)
                .foregroundStyle(Color.onAccent)
                .accessibilityIdentifier("emptyCardsAction")
        }
    }
}

/// The shown cards in the layout's order: S cards in pairs, M and L across the width.
private struct CardGrid: View {
    @Environment(AppState.self) private var state
    let model: DashboardModel

    var body: some View {
        VStack(spacing: Space.grid) {
            ForEach(DashboardLayout.rows(model.shown)) { row in
                HStack(alignment: .top, spacing: Space.grid) {
                    ForEach(row.cards, id: \.metric) { card in
                        if let content = model.contents[card.metric] {
                            MetricCardView(card: card, content: content, section: model.section(of: card.metric)) {
                                state.open(DashboardLayout.route(of: card.metric))
                            }
                        }
                    }
                    if row.isHalf, row.cards.count == 1 {
                        Color.clear.frame(maxWidth: .infinity)
                    }
                }
                .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}

// MARK: - Sources

/// A health row per connection, or the way to connect a first source. Hidden while the
/// connections endpoint is not available.
private struct SourcesSection: View {
    @Environment(AppState.self) private var state
    let connections: Loadable<[Components.Schemas.Connection]>

    var body: some View {
        switch connections {
        case .loading:
            EmptyView()
        case .failed(let problem) where problem.isUnavailable:
            EmptyView()
        case .failed(let problem):
            VStack(alignment: .leading, spacing: 8) {
                header
                Label(problem.title, systemImage: "exclamationmark.triangle").foregroundStyle(Color.inkMuted)
            }
        case .loaded(let list):
            VStack(alignment: .leading, spacing: 8) {
                header
                if list.isEmpty {
                    HStack {
                        Text("No sources yet.").foregroundStyle(Color.inkMuted)
                        Spacer()
                        Button("Connect a source") { state.open(.connections()) }
                            .buttonStyle(.bordered)
                            .accessibilityIdentifier("connectSource")
                    }
                } else {
                    ForEach(list, id: \.id) { connection in
                        SourceRow(connection: connection) { state.open(.connection(id: connection.id)) }
                    }
                }
            }
            .padding(.top, 8)
        }
    }

    private var header: some View {
        SectionHeader("Sources")
    }
}

private struct SourceRow: View {
    let connection: Components.Schemas.Connection
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack(spacing: 12) {
                Circle().fill(SourceStyle.color(connection.provider)).frame(width: 10, height: 10)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(providerLabel(connection.provider)).font(.headline)
                        if connection.official == false { UnofficialBadge() }
                    }
                    Text("Last success " + (connection.lastSuccessAt.map(Format.ago) ?? "never"))
                        .font(.footnote)
                        .foregroundStyle(Color.inkMuted)
                    if let reason = connection.healthReason {
                        Text(reason).font(.footnote).foregroundStyle(Color.inkMuted)
                    }
                }
                Spacer(minLength: 8)
                HealthBadge(health: connection.health)
                    .font(.footnote.weight(.medium))
                    .foregroundStyle(Color.inkMuted)
            }
            .card()
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("source-\(connection.provider)")
    }
}

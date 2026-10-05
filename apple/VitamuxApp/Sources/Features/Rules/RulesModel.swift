import Foundation
import Observation
import VitamuxKit

/// The rules catalogue (the panel's `/rules`): one entry per metric with the rule in effect
/// (`GET /rules`), the catalogue codes without one (`GET /metrics`), and the 90-day coverage per
/// source (`GET /coverage`, when the server has it); search and a custom, built-in or default filter.
@Observable
final class RulesModel {
    struct Entry: Identifiable {
        var metric: String
        var rule: RuleVersion?
        var id: String { metric }
    }

    enum Filter: String, CaseIterable, Identifiable {
        case all, custom, builtin, defaults
        var id: Self { self }

        var title: String {
            switch self {
            case .all: "All"
            case .custom: "Custom"
            case .builtin: "Built-in"
            case .defaults: "Default"
            }
        }

        func matches(_ e: Entry) -> Bool {
            switch self {
            case .all: true
            case .custom: e.rule.map { !$0.builtin } ?? false
            case .builtin: e.rule.map { $0.builtin && !$0._default } ?? false
            case .defaults: e.rule?._default ?? false
            }
        }
    }

    private(set) var entries: Loadable<[Entry]> = .loading
    private(set) var coverage: Components.Schemas.Coverage?
    var query = ""
    var filter = Filter.all
    let range = lastDays(90)

    func load(_ client: Client?) async {
        guard let client else { return }
        async let catalogue = try? client.listMetrics().ok.body.json.metrics
        let rules = await Loadable { try await client.listRules().ok.body.json.rules }
        let metrics = await catalogue ?? []
        switch rules {
        case .loading: entries = .loading
        case .failed(let problem): entries = .failed(problem)
        case .loaded(let rules):
            var list = rules.map { Entry(metric: $0.metric, rule: $0) }
            // Catalogue codes without any rule (no built-in, never configured) are listed too.
            for m in metrics where !list.contains(where: { $0.metric == m.code }) {
                list.append(Entry(metric: m.code, rule: nil))
            }
            entries = .loaded(list)
        }
        guard let codes = entries.value?.map(\.metric), !codes.isEmpty else { return }
        coverage = try? await client.getCoverage(query: .init(startDate: range.start.description, endDate: range.end.description, metric: codes)).ok.body.json
    }

    func count(_ f: Filter) -> Int {
        (entries.value ?? []).filter(f.matches).count
    }

    var visible: [Entry] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased().replacingOccurrences(of: " ", with: "_")
        return (entries.value ?? []).filter { (q.isEmpty || $0.metric.contains(q)) && filter.matches($0) }
    }

    func rows(for metric: String) -> [CoverageStrip.Row] {
        (coverage?.rows ?? []).filter { $0.metric == metric }.map { CoverageStrip.Row(label: $0.source, source: $0.source, cells: $0.days) }
    }
}

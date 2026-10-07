import SwiftUI
import VitamuxKit

/// One bucket of the Day view (the panel's bucket panel): its span, value and status, the min–max
/// of the readings behind it, the sources and the rule's explanation, each source's own value in
/// it, and at raw zoom every reading with its exact time, device and origin. "All sources and
/// overrides" opens the all-sources day.
struct DayPointSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let model: DayModel
    let pick: DayModel.Pick

    private var point: DayModel.Point { pick.point }
    private var layer: DayModel.Layer { pick.layer }
    private var start: Date { point.start ?? point.end }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    HStack(alignment: .firstTextBaseline) {
                        Text(Format.value(layer.values[pick.index], unit: model.unit))
                            .font(.system(.title, design: .rounded, weight: .semibold))
                            .monospacedDigit()
                            .accessibilityIdentifier("bucketValue")
                        Spacer()
                        StatusLabel(status: DataStatus(status: point.status.rawValue, partial: point.partial ?? false))
                    }
                    if let low = point.min, let high = point.max {
                        Text("Range \(Format.number(low))–\(Format.value(high, unit: model.unit))").foregroundStyle(.secondary)
                    }
                    Text(sourceText).accessibilityIdentifier("bucketSource")
                    Text(DayPointSheet.explanation(point, rule: layer.rule)).accessibilityIdentifier("bucketExplanation")
                    if let warnings = point.warnings, !warnings.isEmpty {
                        Label("Warnings: \(warnings.joined(separator: ", "))", systemImage: "exclamationmark.triangle")
                            .font(.footnote)
                            .foregroundStyle(Color.feedbackWarn)
                    }
                } header: {
                    Text("\(span) · \(layer.bucket.singular)").accessibilityIdentifier("bucketSpan")
                }
                if !perSource.isEmpty {
                    Section("Each source in this bucket") {
                        ForEach(perSource, id: \.label) { row in
                            LabeledContent(row.label, value: row.value)
                        }
                    }
                }
                if !readings.isEmpty {
                    Section("Readings") {
                        ForEach(readings) { reading in
                            VStack(alignment: .leading, spacing: 2) {
                                HStack {
                                    Text(model.seconds(reading.at)).monospacedDigit()
                                    Spacer()
                                    Text(Format.value(reading.value, unit: model.unit)).monospacedDigit()
                                }
                                Text(reading.detail).font(.footnote).foregroundStyle(.secondary)
                            }
                            .accessibilityElement(children: .combine)
                            .accessibilityIdentifier("reading")
                        }
                    }
                }
                Section {
                    Button {
                        dismiss()
                        if let date = model.date {
                            state.paths[state.tab, default: []].append(.metricDay(code: model.code, date: date.description))
                        }
                    } label: {
                        Label("All sources and overrides", systemImage: "square.stack.3d.up")
                    }
                    .accessibilityIdentifier("bucketAllSources")
                }
            }
            .navigationTitle(model.date.map { Format.day($0) } ?? "Bucket")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.accessibilityIdentifier("closeBucket") }
            }
        }
        .sheetBackground()
        .presentationDetents([.medium, .large])
    }

    /// "07:10–07:11", to the second for 30-second buckets.
    private var span: String {
        let format = layer.bucket == .s30 ? model.seconds : model.clock
        return "\(format(start))–\(format(point.end))"
    }

    private var sourceText: String {
        guard let providers = point.providers, !providers.isEmpty else { return "No source." }
        var text = "From \(providers.map(providerLabel).joined(separator: ", "))"
        if !point.sources.isEmpty { text += " (\(point.sources.map(groupLabel).joined(separator: ", ")))" }
        return text + "."
    }

    /// What resolved the bucket, in neutral words: the rule's operation over the groups it used.
    static func explanation(_ point: DayModel.Point, rule: Components.Schemas.RuleRef?) -> String {
        guard point.status != .noData else { return "No source had a value in this bucket." }
        let groups = point.sources.map(groupLabel)
        var text: String
        switch groups.count {
        case 0:
            text = "Set without a source."
        case 1 where point.status == .fallback:
            text = "The rule’s first source had no value here, so it fell back to \(groups[0])."
        case 1:
            text = "From \(groups[0])."
        default:
            let operation = switch rule?.strategy {
            case "minimum_across_sources": "Lowest"
            case "maximum_across_sources": "Highest"
            case "sum_across_sources": "Sum"
            default: "Mean"
            }
            text = "\(operation) of \(groups.count) sources: \(groups.joined(separator: ", "))."
        }
        if let n = point.n { text += " \(n) \(n == 1 ? "reading" : "readings")." }
        if let coverage = point.coverage { text += " Coverage \(Int((coverage * 100).rounded())) %." }
        if let rule { text += " \(ruleSummary(rule))." }
        return text
    }

    /// Each source's own value over the bucket (its point that covers the bucket's start).
    private var perSource: [(label: String, value: String)] {
        layer.sources.compactMap { source in
            guard source.step != .raw else { return nil }
            let length = source.step.seconds
            guard let p = source.points.first(where: { p in p.start.map { $0 <= start && start < $0.addingTimeInterval(length) } ?? false }) else {
                return (source.label, "no value")
            }
            let value = model.additive ? p.sum : p.mean
            let suffix = source.step == layer.bucket ? "" : " (\(source.step.singular))"
            return (source.label, "\(Format.value(value, unit: model.unit)) · \(p.n) \(p.n == 1 ? "reading" : "readings")\(suffix)")
        }
    }

    struct Reading: Identifiable {
        var id: String
        var at: Date
        var value: Double?
        var detail: String
    }

    /// The raw readings in the bucket, at raw zoom.
    private var readings: [Reading] {
        guard layer.step == .raw else { return [] }
        let end = point.end
        return layer.sources.filter { $0.step == .raw }.flatMap { source in
            source.points.compactMap { p -> Reading? in
                guard let t = p.start, t >= start, t < end else { return nil }
                let device = [providerLabel(source.provider), source.device].compactMap(\.self).joined(separator: " · ")
                return Reading(id: "\(source.id)@\(t.timeIntervalSince1970)", at: t, value: p.value, detail: "\(device) · origin \(source.origin ?? "–")")
            }
        }
        .sorted { $0.at < $1.at }
    }
}

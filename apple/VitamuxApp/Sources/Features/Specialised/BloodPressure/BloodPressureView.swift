import SwiftUI
import VitamuxKit

/// Blood pressure (`vitamux://explore/blood-pressure`): range and part of the day, the mean and
/// count of the range, one dumbbell per session, the pulse per session and every reading with its
/// source and provenance. Nothing is graded, flagged or coloured by value.
struct BloodPressureView: View {
    @Environment(AppState.self) private var state
    @State private var model = BloodPressureModel()
    @State private var provenance: ProvenanceRequest?

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                ViewIntro(code: "blood_pressure", text: "Readings shown as measured. Nothing is graded or flagged.")
                RangePicker(range: $model.span.range, end: $model.span.end, latest: model.span.latest)
                    .buttonStyle(.borderless) // one row, two buttons: each takes only its own taps
                    .accessibilityIdentifier("rangePicker")
            }
            switch model.readings {
            case .loading:
                ProgressView("Loading readings").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let readings) where readings.isEmpty:
                EmptyRangeView(title: "No readings in this range", text: "Readings from a connected monitor or a push source appear here.")
            case .loaded:
                ReadingsChart(model: model)
                ReadingsList(readings: model.chosen.reversed(), provenance: $provenance)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Blood pressure")
        .task(id: model.span.key) { await model.load(state.client) }
        .sheet(item: $provenance) { ProvenanceSheet(request: $0) }
    }
}

/// The range's mean and count, the part-of-day picker, the dumbbells and the pulse line.
private struct ReadingsChart: View {
    @Bindable var model: BloodPressureModel

    var body: some View {
        let sessions = model.sessions
        Section {
            HStack(alignment: .top) {
                StatTile(label: model.span.meanLabel, value: model.meanText ?? "–", unit: "mmHg")
                    .accessibilityIdentifier("bpMean")
                StatTile(label: "Readings", value: model.chosen.count.formatted())
                    .accessibilityIdentifier("bpCount")
            }
            Picker("Time of day", selection: $model.part) {
                ForEach(BloodPressureModel.Part.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .accessibilityIdentifier("timeOfDay")
            if sessions.isEmpty {
                Text("No readings in this part of the day.").foregroundStyle(.secondary)
            } else {
                RangeDumbbell(
                    title: "Systolic and diastolic readings", readings: model.dumbbells(sessions),
                    lowLabel: "Diastolic", highLabel: "Systolic", unit: "mmHg", hue: .bloodPressure
                )
                .accessibilityIdentifier("bpChart")
            }
        } header: {
            Text("Readings")
        } footer: {
            Text("One dumbbell per session: systolic filled, diastolic hollow. Readings within 30 minutes are one session and its mean is plotted. Morning is before 12:00 and evening from 17:00, in the local time of each reading.")
        }
        let pulse = model.pulse(sessions)
        if !pulse.isEmpty {
            Section("Pulse") {
                TimeSeries(title: "Pulse per session", series: pulse, unit: "bpm", hue: .heartRate, zoomable: false, height: 160)
                    .accessibilityIdentifier("pulseChart")
            }
        }
    }
}

/// Every reading of the part of the day, newest first, a page at a time.
private struct ReadingsList: View {
    let readings: [BloodPressureReading]
    @Binding var provenance: ProvenanceRequest?
    @State private var shown = ListPage.size

    var body: some View {
        Section("All readings") {
            ForEach(readings.prefix(shown), id: \.id) { reading in
                ReadingRow(reading: reading, provenance: $provenance)
            }
            ShowMoreButton(remaining: readings.count - shown) { shown += ListPage.size }
        }
    }
}

private struct ReadingRow: View {
    let reading: BloodPressureReading
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(instantText(reading.measuredAt, in: TimeZone(offsetMinutes: reading.tzOffsetMin)))
                Spacer()
                Text(pair).font(.body.weight(.semibold)).monospacedDigit()
                Text("mmHg").font(.caption).foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("reading-\(reading.id)")
            HStack(spacing: 8) {
                if let pulse = reading.pulse { Text("Pulse \(Format.number(pulse)) bpm").monospacedDigit() }
                let context = BloodPressureModel.context(reading)
                if !context.isEmpty { Text(context) }
            }
            .font(.footnote)
            .foregroundStyle(.secondary)
            HStack {
                SourceChip(provider: reading.source.provider, name: recordName(reading.source)).font(.footnote)
                Spacer()
                ProvenanceButton(entity: .group, id: reading.id, request: $provenance)
            }
        }
    }

    private var pair: String {
        "\(reading.systolic.map(Format.number) ?? "–")/\(reading.diastolic.map(Format.number) ?? "–")"
    }
}

import SwiftUI
import VitamuxKit

/// ECG (`vitamux://explore/ecg`): every ECG recording in the range with the classification Apple
/// recorded and the average heart rate; a row opens the recording.
struct ECGListView: View {
    @Environment(AppState.self) private var state
    @State private var model = ECGListModel()

    var body: some View {
        @Bindable var model = model
        List {
            Section {
                WatchIntro(hue: .hrv, symbol: "waveform.path.ecg",
                           text: "Recordings from the ECG app on Apple Watch, each with the classification Apple recorded. Vitamux shows them as recorded and does not interpret them.")
                RangePicker(range: $model.span.range, end: $model.span.end, latest: model.span.latest)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("rangePicker")
            }
            switch model.recordings {
            case .loading:
                ProgressView("Loading recordings").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let recordings) where recordings.isEmpty:
                EmptyRangeView(title: "No ECG recordings in this range", text: "Turn on the ECG group in Apple Health on this iPhone to send recordings from Apple Watch.")
            case .loaded(let recordings):
                Section {
                    ForEach(recordings, id: \.id) { event in
                        NavigationLink(value: Route.ecgRecording(id: event.id)) { ECGRow(event: event) }
                            .accessibilityIdentifier("ecg-\(event.id)")
                    }
                } header: {
                    Text(recordings.count == 1 ? "1 recording" : "\(recordings.count) recordings").accessibilityIdentifier("ecgCount")
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("ECG")
        .task(id: model.span.key) { await model.load(state.client) }
    }
}

private struct ECGRow: View {
    let event: HealthEvent

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(WatchText.classification(event.level)).font(.body.weight(.medium))
            Text(detail).font(.footnote).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }

    private var detail: String {
        let zone = TimeZone(offsetMinutes: event.tzOffsetMin)
        let rate = event.value.map { "average \(Format.number($0)) bpm" } ?? "no average heart rate"
        return "\(instantText(event.startAt, in: zone)) · \(rate)"
    }
}

/// One recording (`vitamux://explore/ecg/{id}`): Apple's classification as recorded, with its
/// source; average heart rate, symptoms, sampling details; the strip at 25 mm/s and 10 mm/mV,
/// scrolled sideways, and its table.
struct ECGRecordingView: View {
    @Environment(AppState.self) private var state
    @State private var model: ECGRecordingModel

    init(id: String) {
        _model = State(initialValue: ECGRecordingModel(id: id))
    }

    var body: some View {
        List {
            switch model.event {
            case .loading:
                ProgressView("Loading the recording").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(nil):
                ContentUnavailableView("No ECG recording with this id", systemImage: "waveform.path.ecg")
            case .loaded(let event?):
                ECGFacts(event: event)
                WaveformSection(model: model)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("ECG recording")
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.load(state.client) }
    }
}

private struct ECGFacts: View {
    let event: HealthEvent

    var body: some View {
        let context = event.context
        let zone = TimeZone(offsetMinutes: event.tzOffsetMin)
        Section {
            VStack(alignment: .leading, spacing: 4) {
                Text(WatchText.classification(event.level))
                    .font(.title2.weight(.semibold))
                    .accessibilityIdentifier("ecgClassification")
                Text("Classification recorded by Apple’s ECG app, shown as recorded")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                Text(WatchText.source(event.source)).font(.footnote).foregroundStyle(.secondary)
            }
            .accessibilityElement(children: .combine)
            LabeledContent("Recorded", value: instantText(event.startAt, in: zone))
            LabeledContent("Average heart rate") {
                Text(event.value.map { "\(Format.number($0)) bpm" } ?? "Not recorded").accessibilityIdentifier("ecgHeartRate")
            }
            LabeledContent("Symptoms", value: WatchText.symptoms(context.text("symptoms_status")))
            if let hz = context.number("sampling_frequency_hz") {
                LabeledContent("Sampling frequency", value: "\(Format.number(hz)) Hz")
            }
            if let lead = context.text("lead") {
                LabeledContent("Lead", value: WatchText.lead(lead))
            }
            if let version = context.number("algorithm_version") ?? context.text("algorithm_version").flatMap(Double.init) {
                LabeledContent("Algorithm version", value: Format.number(version))
            }
        }
    }
}

private struct WaveformSection: View {
    @Bindable var model: ECGRecordingModel

    var body: some View {
        Section {
            switch model.strip {
            case .loading:
                ProgressView("Loading the waveform").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(nil):
                Text("No waveform is stored for this recording.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("noWaveform")
            case .loaded(let strip?):
                ECGStripView(strip: strip)
                    .listRowInsets(EdgeInsets())
                Toggle("Show as table", isOn: $model.showsTable).accessibilityIdentifier("ecgTableToggle")
                if model.showsTable {
                    ForEach(strip.seconds) { second in
                        LabeledContent("\(second.id)–\(second.id + 1) s") {
                            Text("\(Format.number(second.low)) to \(Format.number(second.high)) mV").monospacedDigit()
                        }
                        .accessibilityIdentifier("ecgSecond-\(second.id)")
                    }
                }
            }
        } header: {
            Text("Waveform")
        } footer: {
            if case .loaded(let strip?) = model.strip {
                Text("25 mm/s and 10 mm/mV; each small square is 0.04 s by 0.1 mV. \(strip.summary). Scroll sideways for the whole strip.")
            }
        }
    }
}

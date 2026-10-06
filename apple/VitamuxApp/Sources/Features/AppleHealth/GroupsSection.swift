import HealthBridgeHealthKit
import SwiftUI

/// One toggle per metric group, each with a plain line on what it sends: "Requested" when on (iOS
/// never says whether a read was granted). The registry v2 groups (ADR-0024 › Privacy opt-in) are
/// listed apart as sensitive and stay off until the owner turns each on, after a sheet that says
/// what is read and where it is kept. One group never turns another on; routes need workouts.
struct GroupsSection: View {
    let device: ThisDevice

    var body: some View {
        Section {
            ForEach(MetricGroup.allCases.filter { !$0.isOptIn }, id: \.self) { group in
                GroupRow(device: device, group: group)
            }
        } header: {
            Text("Data groups")
        } footer: {
            Text("Turning a group on asks iOS for read access to its types and pulls their full history. iOS never says whether you allowed or denied a type, so Vitamux shows \"Requested\", not \"Granted\". Change access in Settings › Health › Data Access & Devices.")
        }
        Section {
            ForEach(MetricGroup.allCases.filter(\.isOptIn), id: \.self) { group in
                GroupRow(device: device, group: group)
            }
        } header: {
            Label("Sensitive groups", systemImage: "lock.shield")
        } footer: {
            Text("These are off until you turn each one on, also after the upgrade from Vitamux Bridge. Nothing in a group is asked for or read before that. Turning one off stops reading it; what your server already has stays until you delete it there.")
        }
    }
}

private struct GroupRow: View {
    let device: ThisDevice
    let group: MetricGroup
    /// A sensitive group asks first; its toggle stays off until the sheet's "Turn on".
    @State private var asksOptIn = false

    var body: some View {
        let on = device.enabled.contains(group)
        let missing = group.requires.flatMap { device.enabled.contains($0) ? nil : $0 }
        VStack(alignment: .leading, spacing: 4) {
            Toggle(group.title, isOn: Binding {
                on
            } set: { wanted in
                if wanted, group.isOptIn {
                    asksOptIn = true
                } else {
                    Task { await device.setGroup(group, enabled: wanted) }
                }
            })
            .disabled(device.isRevoked || (!on && missing != nil))
            .accessibilityIdentifier("group-\(group.rawValue)")
            Text(group.sends)
                .font(.caption)
                .foregroundStyle(.secondary)
            if let missing {
                Text("Needs \(missing.title) to be on.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("requires-\(group.rawValue)")
            }
            if on {
                Text("Requested")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("requested-\(group.rawValue)")
            }
        }
        .sheet(isPresented: $asksOptIn) {
            OptInSheet(group: group) {
                Task { await device.setGroup(group, enabled: true) }
            }
        }
    }
}

/// What a sensitive group reads and where it goes, before iOS asks for its types.
private struct OptInSheet: View {
    @Environment(\.dismiss) private var dismiss
    let group: MetricGroup
    let turnOn: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section("What is read") {
                    Text(group.sends)
                }
                Section("Where it goes") {
                    Text("It is sent to the Vitamux server this iPhone is paired with and stored there like your other health data: protected by the server's disk encryption, without extra encryption by the app.")
                    if group == .routes {
                        Text("Routes show where each workout went. The app's map loads Apple map tiles for that area to draw a route; the web panel draws routes without map tiles.")
                    }
                    Text("Vitamux never sends it to anyone else, and never puts it in widgets, notifications or logs.")
                }
                Section {
                    Text("Next, iOS asks for read access to this group's types only. You can turn the group off here at any time.")
                        .foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Turn on \(group.title)?")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }.accessibilityIdentifier("cancelOptIn")
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Turn on") {
                        dismiss()
                        turnOn()
                    }
                    .accessibilityIdentifier("confirmOptIn")
                }
            }
        }
        .sheetBackground()
        .presentationDetents([.medium, .large])
    }
}

extension MetricGroup {
    var title: String {
        switch self {
        case .ecg: "ECG"
        case .beats: "Beat-to-beat"
        case .cycle: "Cycle tracking"
        default: rawValue.capitalized
        }
    }

    /// What the group sends, in plain words.
    var sends: String {
        switch self {
        case .heart: "Heart rate, resting and walking heart rate, HRV, VO2 max, blood pressure, AFib burden, and heart-rate and irregular-rhythm alerts."
        case .activity: "Steps, distances, energy, exercise, stand and move time, speeds and power, mobility, and Apple's daily activity summaries with their goals."
        case .body: "Weight, height, body fat, lean body mass, BMI and waist."
        case .sleep: "Sleep stages, breathing disturbances, wrist temperature and sleep apnea alerts."
        case .workouts: "Workouts with their totals, laps, pauses, activities and effort scores."
        case .vitals: "Blood oxygen, respiratory rate, temperatures, glucose, audio exposure, lung function and handwashing."
        case .nutrition: "Energy, macronutrients, water, caffeine and alcohol you log."
        case .mind: "State of Mind entries (how you felt, with the labels and associations you chose) and mindful sessions."
        case .cycle: "Cycle tracking: menstrual flow, bleeding, test results, sexual activity, pregnancy, lactation, contraception, menopause and cycle alerts."
        case .symptoms: "The symptoms you log in Apple Health, such as headache or fatigue, with how they were logged."
        case .ecg: "ECG recordings from Apple Watch: each 30-second waveform, the classification Apple recorded, average heart rate and symptoms."
        case .beats: "The time between individual heartbeats that Apple Watch records around its HRV readings."
        case .routes: "The GPS route of each workout: locations, altitude and speed, second by second."
        }
    }
}

import HealthBridgeHealthKit
import SwiftUI

/// One metric group's types: anchor, last sync, the last error, the server's possibly-denied
/// hint and a per-type anchor reset. Bridge's Status tab.
struct TypeStatusView: View {
    let device: ThisDevice
    let group: MetricGroup
    let possiblyDenied: Set<String>

    var body: some View {
        List {
            ForEach(device.types(in: group), id: \.id) { type in
                TypeRow(device: device, type: type, possiblyDenied: possiblyDenied.contains(type.id))
            }
        }
        .navigationTitle(group.title)
    }
}

private struct TypeRow: View {
    let device: ThisDevice
    let type: HealthType
    let possiblyDenied: Bool

    var body: some View {
        let status = device.status[type.id]
        let anchor = device.anchorHashes[type.id] ?? "none"
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text(type.displayName)
                Group {
                    if let last = status?.lastSync {
                        Text("Last sync \(last.formatted(.relative(presentation: .named)))")
                    } else {
                        Text("Not synced yet")
                    }
                    Text(anchor == "none" ? "Full pull pending" : "Anchor \(anchor)")
                        .monospacedDigit()
                        .accessibilityIdentifier("anchor-\(type.id)")
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                if possiblyDenied {
                    Text("Possibly denied: nothing arrived for 7 days")
                        .font(.caption)
                        .foregroundStyle(.orange)
                        .accessibilityIdentifier("possiblyDenied-\(type.id)")
                }
                if let error = status?.lastError {
                    Text(error).font(.caption).foregroundStyle(.red)
                }
            }
            Spacer()
            Menu {
                Button("Reset anchor and pull again", systemImage: "arrow.counterclockwise") {
                    Task { await device.resetAnchor(type) }
                }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .accessibilityLabel("Actions for \(type.displayName)")
        }
    }
}

extension HealthType {
    /// "HKQuantityTypeIdentifierHeartRateVariabilitySDNN" → "Heart Rate Variability SDNN".
    var displayName: String {
        var name = id
        for prefix in ["HKQuantityTypeIdentifier", "HKCategoryTypeIdentifier", "HKCorrelationTypeIdentifier", "HKWorkoutType"]
        where name.hasPrefix(prefix) {
            name.removeFirst(prefix.count)
            break
        }
        if name.isEmpty { return "Workouts" }
        var out = ""
        var previous: Character?
        for character in name {
            if let previous, character.isUppercase, previous.isLowercase { out.append(" ") }
            out.append(character)
            previous = character
        }
        return out
    }
}

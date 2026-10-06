import CoreLocation
import Foundation
import MapKit
import Observation
import SwiftUI
import VitamuxKit

typealias Workout = Components.Schemas.Workout
typealias WorkoutSegment = Components.Schemas.WorkoutSegment

/// One workout (`vitamux://explore/workouts/{id}`) as its source recorded it (`GET
/// /workouts/{id}`): totals, the route (`GET /workouts/{id}/route`) on an Apple map, and its laps,
/// intervals, activities, pauses and markers on one time axis and as a list.
@Observable
final class WorkoutDetailModel {
    let id: String
    private(set) var workout: Loadable<Workout> = .loading
    private(set) var route: Loadable<RoutePath?> = .loading

    init(id: String) {
        self.id = id
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        let id = id
        workout = await Loadable { try await client.getWorkout(path: .init(id: id)).ok.body.json }
        route = await Loadable {
            do {
                return RoutePath(try await client.getWorkoutRoute(path: .init(id: id)).ok.body.json)
            } catch where Problem(error).status == 404 {
                return nil
            }
        }
    }
}

/// A route reduced once for drawing: valid locations only (CoreLocation marks an invalid fix with
/// a negative horizontal accuracy), at most `maxPoints`, evenly strided, first and last kept.
struct RoutePath {
    static let maxPoints = 1_000

    let coordinates: [CLLocationCoordinate2D]
    /// Valid locations in the document.
    let count: Int

    init?(_ document: Components.Schemas.RouteDocument) {
        let n = min(document.latitude.count, document.longitude.count)
        let accuracy = document.horizontalAccuracyM
        var valid: [Int] = []
        valid.reserveCapacity(n)
        for i in 0..<n {
            let lat = document.latitude[i], lon = document.longitude[i]
            guard (-90...90).contains(lat), (-180...180).contains(lon) else { continue }
            if let accuracy, i < accuracy.count, accuracy[i] < 0 { continue }
            valid.append(i)
        }
        guard valid.count > 1 else { return nil }
        let stride = max(1, Int((Double(valid.count) / Double(Self.maxPoints)).rounded(.up)))
        var picked = Swift.stride(from: 0, to: valid.count, by: stride).map { valid[$0] }
        if picked.last != valid.last { picked.append(valid.last!) }
        coordinates = picked.map { CLLocationCoordinate2D(latitude: document.latitude[$0], longitude: document.longitude[$0]) }
        count = valid.count
    }
}

struct WorkoutDetailView: View {
    @Environment(AppState.self) private var state
    @State private var model: WorkoutDetailModel
    @State private var provenance: ProvenanceRequest?

    init(id: String) {
        _model = State(initialValue: WorkoutDetailModel(id: id))
    }

    var body: some View {
        List {
            switch model.workout {
            case .loading:
                ProgressView("Loading the workout").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(let workout):
                WorkoutSummary(workout: workout, provenance: $provenance)
                RouteSection(route: model.route)
                SegmentsSection(workout: workout)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(model.workout.value.map { metricLabel($0.sport) } ?? "Workout")
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.load(state.client) }
        .sheet(item: $provenance) { ProvenanceSheet(request: $0) }
    }
}

private struct WorkoutSummary: View {
    let workout: Workout
    @Binding var provenance: ProvenanceRequest?

    var body: some View {
        let zone = TimeZone(offsetMinutes: workout.tzOffsetMin)
        Section {
            LabeledContent("Time", value: "\(instantText(workout.startAt, in: zone))–\(clockText(workout.endAt, in: zone))")
            LabeledContent("Duration", value: Format.duration(workout.endAt.timeIntervalSince(workout.startAt)))
            LabeledContent("Distance", value: workout.distanceM.map { Format.value($0 / 1000, unit: "km") } ?? "–")
            LabeledContent("Energy", value: workout.energyKcal.map { Format.value($0, unit: "kcal") } ?? "–")
            LabeledContent("Average / max heart rate",
                           value: "\(workout.avgHrBpm.map(Format.number) ?? "–") / \(workout.maxHrBpm.map(Format.number) ?? "–") bpm")
            ProvenanceButton(entity: .workout, id: workout.id, request: $provenance)
        } header: {
            Text(WatchText.source(workout.source))
        }
    }
}

private struct RouteSection: View {
    let route: Loadable<RoutePath?>

    var body: some View {
        Section {
            switch route {
            case .loading:
                ProgressView("Loading the route").frame(maxWidth: .infinity)
            case .failed(let problem):
                ProblemView(problem: problem)
            case .loaded(nil):
                Text("No route was recorded for this workout.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("noRoute")
            case .loaded(let path?):
                RouteMap(path: path)
                    .frame(height: 260)
                    .listRowInsets(EdgeInsets())
            }
        } header: {
            Text("Route")
        } footer: {
            if case .loaded(let path?) = route {
                Text("\(path.count.formatted()) locations as recorded. The map loads Apple map tiles for this area; the route itself stays on your server.")
            }
        }
    }
}

/// The route on MapKit, fitted to the path, with its start and end.
private struct RouteMap: View {
    let path: RoutePath

    var body: some View {
        Map(initialPosition: .automatic, interactionModes: [.pan, .zoom]) {
            MapPolyline(coordinates: path.coordinates)
                .stroke(MetricHue.steps.color, style: StrokeStyle(lineWidth: 4, lineCap: .round, lineJoin: .round))
            if let first = path.coordinates.first {
                Marker("Start", systemImage: "flag", coordinate: first).tint(MetricHue.other.color)
            }
            if let last = path.coordinates.last {
                Marker("End", systemImage: "flag.checkered", coordinate: last).tint(MetricHue.other.color)
            }
        }
        .mapStyle(.standard(pointsOfInterest: .excludingAll))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Route map")
        .accessibilityValue("\(path.count) locations, from start to end")
        .accessibilityIdentifier("routeMap")
    }
}

/// Laps, intervals, activities, pauses and markers: lanes over the workout, then a list.
private struct SegmentsSection: View {
    let workout: Workout

    var body: some View {
        let segments = (workout.segments ?? []).sorted { $0.seq < $1.seq }
        let zone = TimeZone(offsetMinutes: workout.tzOffsetMin)
        Section {
            if segments.isEmpty {
                Text("No laps, activities, pauses or markers were recorded.")
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("noSegments")
            } else {
                EventLanes(title: "Segments over the workout", lanes: lanes(segments), from: workout.startAt, to: workout.endAt, hue: .steps, timeZone: zone)
                    .accessibilityIdentifier("segmentLanes")
                ForEach(segments, id: \.seq) { segment in
                    SegmentRow(segment: segment, number: number(of: segment, in: segments), zone: zone)
                        .accessibilityIdentifier("segment-\(segment.seq)")
                }
            }
        } header: {
            Text("Laps and activities")
        }
    }

    private func lanes(_ segments: [WorkoutSegment]) -> [EventLanes.Lane] {
        let kinds: [WorkoutSegment.KindPayload] = [.activity, .lap, .interval, .set, .pause, .marker]
        return kinds.compactMap { kind in
            let list = segments.filter { $0.kind == kind }
            guard !list.isEmpty else { return nil }
            return EventLanes.Lane(label: kind.plural, events: list.map { segment in
                EventLanes.Event(start: segment.startAt, end: segment.endAt ?? segment.startAt, label: SegmentRow.title(segment, number: number(of: segment, in: segments)))
            })
        }
    }

    /// The segment's place among those of its kind, from 1.
    private func number(of segment: WorkoutSegment, in segments: [WorkoutSegment]) -> Int {
        segments.filter { $0.kind == segment.kind && $0.seq <= segment.seq }.count
    }
}

private struct SegmentRow: View {
    let segment: WorkoutSegment
    let number: Int
    let zone: TimeZone

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(Self.title(segment, number: number)).font(.body.weight(.medium))
            Text(detail).font(.footnote).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }

    static func title(_ segment: WorkoutSegment, number: Int) -> String {
        let data = segment.data
        switch segment.kind {
        case .activity: return data.text("sport").map(metricLabel) ?? "Activity \(number)"
        case .pause: return data.text("type") == "motion_pause" ? "Motion pause \(number)" : "Pause \(number)"
        case .marker: return data.text("type") == "pause_or_resume_request" ? "Pause or resume request" : "Marker \(number)"
        default: return "\(segment.kind.singular) \(number)"
        }
    }

    private var detail: String {
        var parts = [segment.endAt.map { "\(clockText(segment.startAt, in: zone))–\(clockText($0, in: zone))" } ?? clockText(segment.startAt, in: zone)]
        if let end = segment.endAt, end > segment.startAt {
            let minutes = end.timeIntervalSince(segment.startAt) / 60
            parts.append(minutes < 1 ? "\(Int((minutes * 60).rounded())) s" : "\(Format.number((minutes * 10).rounded() / 10)) min")
        }
        if let distance = segment.data.number("distance_m", in: "totals") { parts.append(Format.value(distance / 1000, unit: "km")) }
        if let energy = segment.data.number("energy_kcal", in: "totals") { parts.append(Format.value(energy, unit: "kcal")) }
        if let location = segment.data.text("location") { parts.append(metricLabel(location)) }
        return parts.joined(separator: " · ")
    }
}

extension WorkoutSegment.KindPayload {
    var singular: String {
        switch self {
        case .lap: "Lap"
        case .set: "Set"
        case .interval: "Interval"
        case .activity: "Activity"
        case .pause: "Pause"
        case .marker: "Marker"
        }
    }

    var plural: String {
        switch self {
        case .lap: "Laps"
        case .set: "Sets"
        case .interval: "Intervals"
        case .activity: "Activities"
        case .pause: "Pauses"
        case .marker: "Markers"
        }
    }
}

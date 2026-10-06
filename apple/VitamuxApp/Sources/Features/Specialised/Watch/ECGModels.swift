import Foundation
import Observation
import VitamuxKit

/// ECG (`vitamux://explore/ecg`): the `ecg_recording` events in the range (`GET /events`, every
/// page), newest first.
@Observable
final class ECGListModel {
    var span = ViewRange(.year)
    private(set) var recordings: Loadable<[HealthEvent]> = .loading

    func load(_ client: Client?) async {
        guard let client else { return }
        let start = span.start()?.description, end = span.end.description
        recordings = await Loadable {
            try await readAll { cursor in
                let page = try await client.listEvents(query: .init(startDate: start, endDate: end, code: ["ecg_recording"], limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.events, nextCursor(page.value1))
            }
            .sorted { $0.startAt > $1.startAt }
        }
    }
}

/// One ECG recording (`vitamux://explore/ecg/{id}`): the event as recorded and its waveform
/// (`GET /events/{id}/waveform`), laid out once as a strip. A recording without a waveform answers
/// 404 and shows the empty state.
@Observable
final class ECGRecordingModel {
    let id: String
    private(set) var event: Loadable<HealthEvent?> = .loading
    private(set) var strip: Loadable<ECGStrip?> = .loading
    var showsTable = false

    init(id: String) {
        self.id = id
    }

    func load(_ client: Client?) async {
        guard let client else { return }
        let id = id
        event = await Loadable {
            try await readAll { cursor in
                let page = try await client.listEvents(query: .init(code: ["ecg_recording"], limit: 500, cursor: cursor)).ok.body.json
                return (page.value2.events, nextCursor(page.value1))
            }
            .first { $0.id == id }
        }
        strip = await Loadable {
            do {
                return ECGStrip(try await client.getEventWaveform(path: .init(id: id)).ok.body.json)
            } catch where Problem(error).status == 404 {
                return nil
            }
        }
    }
}

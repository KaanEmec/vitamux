import Foundation
import Observation
import VitamuxKit

/// The server's view of this iPhone (`GET /devices`): last seen, last batch, the types its last
/// heartbeat listed, possibly-denied hints and requested resets. The phone's own state lives in
/// `ThisDevice`; the screen joins the two.
@Observable
final class AppleHealthModel {
    typealias Device = Components.Schemas.PairedDevice

    /// Nil when the signed-in server does not list this iPhone (paired with another server).
    private(set) var server: Loadable<Device?> = .loading

    func load(_ client: Client?, deviceID: String?) async {
        guard let client, let deviceID else {
            server = .loaded(nil)
            return
        }
        server = await Loadable { try await client.listDevices().ok.body.json.devices.first { $0.id == deviceID } }
    }

    var possiblyDenied: Set<String> {
        Set(server.value??.possiblyDenied ?? [])
    }
}

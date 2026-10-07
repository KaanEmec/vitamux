import Foundation
import Observation
import VitamuxKit

/// The worked example's model: it owns what the screen shows, as `Loadable`s, and loads it. The
/// server's side comes from `GET /system/version` and `GET /system/status` through the generated
/// client (the panel's `/settings/system`).
@Observable
final class SystemStatusModel {
    struct Versions: Equatable {
        var app: String
        var build: String
    }

    private(set) var versions: Loadable<Versions> = .loading
    private(set) var server: Loadable<Components.Schemas.SystemVersion> = .loading
    private(set) var status: Loadable<Components.Schemas.SystemStatus> = .loading

    func load(_ client: Client? = nil) async {
        versions = await Loadable { try Self.appVersions() }
        guard let client else { return }
        server = await Loadable { try await client.getSystemVersion().ok.body.json }
        status = await Loadable { try await client.getSystemStatus().ok.body.json }
    }

    private static func appVersions() throws -> Versions {
        let info = Bundle.main.infoDictionary ?? [:]
        guard let app = info["CFBundleShortVersionString"] as? String,
              let build = info["CFBundleVersion"] as? String
        else { throw Problem(title: "Version unavailable") }
        return Versions(app: app, build: build)
    }
}

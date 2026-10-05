import Foundation
import Observation
import VitamuxKit

/// The worked example's model: it owns what the screen shows, as one `Loadable`, and loads it.
/// J22.13 adds the server's side from `GET /system/status` through the generated client.
@Observable
final class SystemStatusModel {
    struct Versions: Equatable {
        var app: String
        var build: String
    }

    private(set) var versions: Loadable<Versions> = .loading

    func load() async {
        versions = await Loadable { try Self.appVersions() }
    }

    private static func appVersions() throws -> Versions {
        let info = Bundle.main.infoDictionary ?? [:]
        guard let app = info["CFBundleShortVersionString"] as? String,
              let build = info["CFBundleVersion"] as? String
        else { throw Problem(title: "Version unavailable") }
        return Versions(app: app, build: build)
    }
}

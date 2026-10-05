// swift-tools-version: 6.0
import PackageDescription

// One library target (folders API/, Core/, Charts/) and one test target: docs/adr/0023-ios-app.md.
// Allowed dependencies: Apple's swift-openapi packages only (added by J22.4).
let package = Package(
    name: "VitamuxKit",
    platforms: [.iOS(.v18), .macOS(.v15)],
    products: [
        .library(name: "VitamuxKit", targets: ["VitamuxKit"]),
    ],
    targets: [
        .target(
            name: "VitamuxKit",
            exclude: ["API/README.md", "Charts/README.md"],
            swiftSettings: [.enableUpcomingFeature("NonisolatedNonsendingByDefault")]
        ),
        .testTarget(name: "VitamuxKitTests", dependencies: ["VitamuxKit"]),
    ]
)

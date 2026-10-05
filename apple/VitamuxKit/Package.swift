// swift-tools-version: 6.0
import PackageDescription

// One library target (folders API/, Core/, Charts/) and one test target: docs/adr/0023-ios-app.md.
// Allowed dependencies: Apple's swift-openapi generator, runtime and URLSession transport only,
// plus what they resolve (pinned in Package.resolved; CI rejects anything else).
let package = Package(
    name: "VitamuxKit",
    platforms: [.iOS(.v18), .macOS(.v15)],
    products: [
        .library(name: "VitamuxKit", targets: ["VitamuxKit"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-openapi-generator", from: "1.10.0"),
        .package(url: "https://github.com/apple/swift-openapi-runtime", from: "1.8.0"),
        .package(url: "https://github.com/apple/swift-openapi-urlsession", from: "1.1.0"),
        .package(url: "https://github.com/apple/swift-http-types", from: "1.0.0"),
    ],
    targets: [
        .target(
            name: "VitamuxKit",
            dependencies: [
                .product(name: "OpenAPIRuntime", package: "swift-openapi-runtime"),
                .product(name: "OpenAPIURLSession", package: "swift-openapi-urlsession"),
                .product(name: "HTTPTypes", package: "swift-http-types"),
            ],
            exclude: ["API/README.md", "Charts/README.md"],
            swiftSettings: [.enableUpcomingFeature("NonisolatedNonsendingByDefault")],
            plugins: [.plugin(name: "OpenAPIGenerator", package: "swift-openapi-generator")]
        ),
        .testTarget(name: "VitamuxKitTests", dependencies: ["VitamuxKit"]),
    ]
)

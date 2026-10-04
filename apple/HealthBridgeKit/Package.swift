// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "HealthBridgeKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "HealthBridgeCore", targets: ["HealthBridgeCore"]),
        .library(name: "HealthBridgeHealthKit", targets: ["HealthBridgeHealthKit"]),
    ],
    targets: [
        .target(name: "HealthBridgeCore"),
        .target(name: "HealthBridgeHealthKit", dependencies: ["HealthBridgeCore"]),
        .testTarget(name: "HealthBridgeCoreTests", dependencies: ["HealthBridgeCore"]),
        .testTarget(name: "HealthBridgeHealthKitTests", dependencies: ["HealthBridgeHealthKit"]),
    ]
)

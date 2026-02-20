// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "UnitConverter",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "UnitConverter",
            path: "Sources"
        ),
    ]
)

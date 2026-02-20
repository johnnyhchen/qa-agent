// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "ContactsApp",
    platforms: [.iOS(.v17)],
    targets: [
        .executableTarget(
            name: "ContactsApp",
            path: "Sources"
        ),
    ]
)

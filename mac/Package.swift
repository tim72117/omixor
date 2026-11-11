// swift-tools-version:5.10
// Desk:原生 macOS app,瀏覽本機 Claude Code 對話,並透過 claude CLI 繼續對話。
import PackageDescription

let package = Package(
    name: "Desk",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "Desk",
            path: "Sources/Desk"
        ),
        .testTarget(
            name: "DeskTests",
            dependencies: ["Desk"],
            path: "Tests/DeskTests"
        ),
    ]
)

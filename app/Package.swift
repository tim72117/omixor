// swift-tools-version:5.10
// Ops:維運線上服務的原生 macOS app(開發進度、CI/CD、伺服器、監控告警、事件、AI 自動化)。
import PackageDescription

let package = Package(
    name: "Ops",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Ops", targets: ["Ops"]),
        // stdio ↔ HTTP 轉接,給只支援 stdio 的 MCP 用戶端(例如 Claude Desktop)
        .executable(name: "omixor-mcp", targets: ["OpsMCP"]),
    ],
    targets: [
        .executableTarget(name: "Ops", path: "Sources/Omixor"),
        .executableTarget(name: "OpsMCP", path: "Sources/OmixorMCP"),
        .testTarget(name: "OpsTests", dependencies: ["Ops"], path: "Tests/OmixorTests"),
    ]
)

// swift-tools-version:5.10
// omixor:維運線上服務的原生 macOS app(開發進度、CI/CD、伺服器、監控告警、事件、AI 自動化)。
import PackageDescription

let package = Package(
    name: "Omixor",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "Omixor", targets: ["Omixor"]),
        // stdio ↔ HTTP 轉接,給只支援 stdio 的 MCP 用戶端(例如 Claude Desktop)
        .executable(name: "omixor-mcp", targets: ["OmixorMCP"]),
    ],
    targets: [
        .executableTarget(name: "Omixor", path: "Sources/Omixor"),
        .executableTarget(name: "OmixorMCP", path: "Sources/OmixorMCP"),
        // Fixtures:測試用的假 MCP 伺服器腳本(不編譯)
        .testTarget(name: "OmixorTests", dependencies: ["Omixor"], path: "Tests/OmixorTests", exclude: ["Fixtures"]),
    ]
)

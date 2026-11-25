@testable import Omixor

extension Connection {
    /// 測試用的連線(新安裝沒有預設連線)
    static let testFixtures: [Connection] = [
        Connection(id: "evoleap", name: "evoleap 正式站", baseURL: "https://evoleap.io", isProduction: true,
                   testPath: "/api/me", auth: .approval),
        Connection(id: "evoleap-local", name: "evoleap 本機", baseURL: "http://localhost:8190", isProduction: false,
                   testPath: "/api/me", auth: .approval, approval: ApprovalSettings(consoleURL: "http://localhost:5190")),
    ]
}

/// 在 startConnections 之後放進測試用的連線
@MainActor
func seedTestConnections(_ app: AppState) {
    for c in Connection.testFixtures { app.connStore?.upsert(c) }
}

import Foundation
import Security

// 外部 MCP 閘道的登記一律透過 core server(IssueServerConfig.current)的 /api/orgs/<組織>/mcp-servers… 讀寫
// (規則由伺服器強制:成員讀、組織管理員改;功能開關 mcpGateway 沒開時清單是空的、寫入回 403,見 core/api/mcp.go)。
// core 只有登記與規則,沒有任何密鑰:密鑰的值存在這台的鑰匙圈(MCPSecretVault)。

struct CoreMCPToolRule: Codable, Hashable, Identifiable {
    var name: String
    var title: String?
    var description: String?
    var enabled: Bool
    var confirm: Bool
    var id: String { name }
}

struct CoreMCPServer: Codable, Hashable, Identifiable {
    var id: String
    var name: String
    var description: String
    var transport: String          // stdio、http
    var command: String?
    var args: [String]
    var cwd: String?
    var envNames: [String]
    var url: String?
    var headerNames: [String]
    var enabled: Bool
    var toolDefault: Bool
    var tools: [CoreMCPToolRule]
    var updatedBy: String?

    init(id: String, name: String = "", description: String = "", transport: String = "stdio", command: String? = nil, args: [String] = [],
         cwd: String? = nil, envNames: [String] = [], url: String? = nil, headerNames: [String] = [], enabled: Bool = true,
         toolDefault: Bool = false, tools: [CoreMCPToolRule] = [], updatedBy: String? = nil) {
        self.id = id; self.name = name; self.description = description; self.transport = transport; self.command = command
        self.args = args; self.cwd = cwd; self.envNames = envNames; self.url = url; self.headerNames = headerNames
        self.enabled = enabled; self.toolDefault = toolDefault; self.tools = tools; self.updatedBy = updatedBy
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = (try? c.decode(String.self, forKey: .name)) ?? id
        description = (try? c.decode(String.self, forKey: .description)) ?? ""
        transport = (try? c.decode(String.self, forKey: .transport)) ?? "stdio"
        command = try? c.decode(String.self, forKey: .command)
        args = (try? c.decode([String].self, forKey: .args)) ?? []
        cwd = try? c.decode(String.self, forKey: .cwd)
        envNames = (try? c.decode([String].self, forKey: .envNames)) ?? []
        url = try? c.decode(String.self, forKey: .url)
        headerNames = (try? c.decode([String].self, forKey: .headerNames)) ?? []
        enabled = (try? c.decode(Bool.self, forKey: .enabled)) ?? false
        toolDefault = (try? c.decode(Bool.self, forKey: .toolDefault)) ?? false
        tools = (try? c.decode([CoreMCPToolRule].self, forKey: .tools)) ?? []
        updatedBy = try? c.decode(String.self, forKey: .updatedBy)
    }

    /// 這台需要的密鑰(種類、名稱)
    var secretNames: [(kind: MCPSecretVault.Kind, name: String)] {
        transport == "http" ? headerNames.map { (.header, $0) } : envNames.map { (.env, $0) }
    }
}

struct CoreMCPGrant: Codable, Hashable {
    var server: String
    var tools: [String]

    init(server: String, tools: [String] = []) { self.server = server; self.tools = tools }
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        server = try c.decode(String.self, forKey: .server)
        tools = (try? c.decode([String].self, forKey: .tools)) ?? []
    }
}

struct CoreMCPView: Codable, Equatable {
    var enabled: Bool
    var note: String?
    var servers: [CoreMCPServer]
    var grants: [String: [CoreMCPGrant]]
    var canEdit: Bool

    init(enabled: Bool = false, note: String? = nil, servers: [CoreMCPServer] = [], grants: [String: [CoreMCPGrant]] = [:], canEdit: Bool = false) {
        self.enabled = enabled; self.note = note; self.servers = servers; self.grants = grants; self.canEdit = canEdit
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = (try? c.decode(Bool.self, forKey: .enabled)) ?? false
        note = try? c.decode(String.self, forKey: .note)
        servers = (try? c.decode([CoreMCPServer].self, forKey: .servers)) ?? []
        grants = (try? c.decode([String: [CoreMCPGrant]].self, forKey: .grants)) ?? [:]
        canEdit = (try? c.decode(Bool.self, forKey: .canEdit)) ?? false
    }

    func server(_ id: String) -> CoreMCPServer? { servers.first { $0.id.caseInsensitiveCompare(id) == .orderedSame } }

    /// 角色的權限(key 不分大小寫)
    func grants(of agent: String) -> [CoreMCPGrant] {
        grants.first { $0.key.caseInsensitiveCompare(agent) == .orderedSame }?.value ?? []
    }
}

/// core 判斷一個工具能不能用的結果
struct CoreMCPResolved: Codable, Hashable {
    var server: String
    var name: String
    var qualified: String
    var allowed: Bool
    var confirm: Bool
    var reason: String?
}

struct CoreMCPResolveResult: Codable {
    var enabled: Bool
    var note: String?
    var tools: [CoreMCPResolved]
}

extension IssueRemote {
    private func decodeMCP<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的外部 MCP 登記格式看不懂:\(error.localizedDescription)")
        }
    }

    /// GET /mcp-servers
    func mcpView(org: String) throws -> CoreMCPView {
        try decodeMCP(CoreMCPView.self, try send("GET", orgURL(org, ["mcp-servers"]), nil))
    }

    /// POST /mcp-servers:新增或修改(要組織管理員)。fields 是要改的欄位(沒給的不動);不會送任何密鑰
    func saveMCPServer(org: String, id: String, fields: [String: Any]) throws -> CoreMCPServer {
        var b = fields
        for k in ["env", "headers", "secrets", "token"] { b.removeValue(forKey: k) }
        b["id"] = id
        return try decodeMCP(CoreMCPServer.self, try send("POST", orgURL(org, ["mcp-servers"]), try json(b)))
    }

    func deleteMCPServer(org: String, id: String) throws {
        _ = try send("POST", orgURL(org, ["mcp-servers", id, "delete"]), try json([:]))
    }

    /// POST /mcp-servers/{id}/tools:同步讀到的工具清單、改規則
    func saveMCPTools(org: String, id: String, sync: [MCPRemoteTool]? = nil, prune: Bool = false,
                      rules: [[String: Any]] = []) throws -> CoreMCPServer {
        var b: [String: Any] = [:]
        if let sync {
            b["sync"] = sync.map { ["name": $0.name, "title": $0.title, "description": String($0.description.prefix(1000))] }
            b["prune"] = prune
        }
        if !rules.isEmpty { b["rules"] = rules }
        return try decodeMCP(CoreMCPServer.self, try send("POST", orgURL(org, ["mcp-servers", id, "tools"]), try json(b)))
    }

    /// POST /mcp-grants:整份取代角色的權限
    func saveMCPGrants(org: String, agent: String, grants: [CoreMCPGrant]) throws {
        let g = grants.map { ["server": $0.server, "tools": $0.tools] as [String: Any] }
        _ = try send("POST", orgURL(org, ["mcp-grants"]), try json(["agentId": agent, "grants": g]))
    }

    /// POST /mcp-tools/resolve:agent 空 = 一般成員
    func resolveMCPTools(org: String, agent: String?, tools: [(server: String, name: String)]) throws -> CoreMCPResolveResult {
        var b: [String: Any] = ["tools": tools.map { ["server": $0.server, "name": $0.name] }]
        if let agent, !agent.isEmpty { b["agentId"] = agent }
        return try decodeMCP(CoreMCPResolveResult.self, try send("POST", orgURL(org, ["mcp-tools", "resolve"]), try json(b)))
    }
}

// MARK: - 這台裝置的密鑰(鑰匙圈)

/// 外部 MCP 伺服器需要的密鑰的值:只存在這台的鑰匙圈(core 只有名稱)。
/// service 可以換(測試一律用暫存的名稱,不碰正式鑰匙圈);帳號是 "<組織>/<伺服器>/<env|header>:<名稱>"
enum MCPSecretVault {
    enum Kind: String { case env, header }

    nonisolated(unsafe) static var service = ProcessInfo.processInfo.environment["OPS_MCP_KEYCHAIN_SERVICE"] ?? "com.tim72117.ops.mcp-gateway"

    private static func account(_ org: String, _ server: String, _ kind: Kind, _ name: String) -> String {
        "\(org)/\(server)/\(kind.rawValue):\(name)"
    }

    private static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: account]
    }

    static func get(org: String, server: String, kind: Kind, name: String) -> String? {
        var q = query(account(org, server, kind, name))
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data else { return nil }
        return String(decoding: d, as: UTF8.self)
    }

    /// nil 或空字串 = 刪掉
    static func set(_ value: String?, org: String, server: String, kind: Kind, name: String) {
        let a = account(org, server, kind, name)
        SecItemDelete(query(a) as CFDictionary)
        guard let value, !value.isEmpty else { return }
        var q = query(a)
        q[kSecValueData as String] = Data(value.utf8)
        q[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        SecItemAdd(q as CFDictionary, nil)
    }

    static func has(org: String, server: String, kind: Kind, name: String) -> Bool {
        get(org: org, server: server, kind: kind, name: name) != nil
    }

    /// 刪掉這台伺服器的全部密鑰
    static func removeAll(org: String, server: CoreMCPServer) {
        for s in server.secretNames { set(nil, org: org, server: server.id, kind: s.kind, name: s.name) }
    }
}

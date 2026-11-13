import CryptoKit
import Foundation

// 事件改由 core server(遠端 omixor 或本機守護服務)保存:/api/orgs/<組織>/incidents(規則見 core/service/incident.go)。
// 這裡是 JSON 解碼(Models.swift 的 Incident 只有 Encodable)與 REST 呼叫;網址、token 與重新登入都和 Issue 共用 IssueRemote。

extension IncidentStatus: Decodable {}
extension IncidentEvent.Kind: Decodable {}

extension Actor: Decodable {
    private enum DKeys: String, CodingKey { case kind, id }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: DKeys.self)
        let kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? "system"
        let id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        switch kind {
        case "person" where !id.isEmpty: self = .person(id)
        case "ai" where !id.isEmpty: self = .ai(id)
        default: self = .system
        }
    }
}

enum IncidentJSON {
    /// 不是 UUID 的 id(例如別的用戶端寫的)換成固定的 UUID
    static func uuid(_ s: String) -> UUID {
        if let u = UUID(uuidString: s) { return u }
        var b = Array(SHA256.hash(data: Data(s.utf8)).prefix(16))
        b[6] = b[6] & 0x0f | 0x40
        b[8] = b[8] & 0x3f | 0x80
        return UUID(uuid: (b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15]))
    }

    /// 執行者 → 核心的 {kind, id}
    static func actor(_ a: Actor) -> [String: Any] {
        switch a {
        case .person(let id): return ["kind": "person", "id": id]
        case .ai(let id): return ["kind": "ai", "id": id]
        case .system: return ["kind": "system"]
        }
    }

    static func iso(_ d: Date) -> String { ISO8601DateFormatter().string(from: d) }

    /// 時間軸項目 → 核心的 IncidentEventInput
    static func event(_ e: IncidentEvent) -> [String: Any] {
        ["kind": e.kind.rawValue, "text": e.text, "date": iso(e.date), "actor": actor(e.actor)]
    }
}

extension IncidentEvent: Decodable {
    private enum DKeys: String, CodingKey { case id, date, actor, kind, text }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: DKeys.self)
        self.init(id: IncidentJSON.uuid(try c.decodeIfPresent(String.self, forKey: .id) ?? UUID().uuidString),
                  date: try c.decodeIfPresent(Date.self, forKey: .date) ?? .distantPast,
                  actor: (try? c.decodeIfPresent(Actor.self, forKey: .actor)) ?? .system,
                  kind: (try? c.decodeIfPresent(Kind.self, forKey: .kind)) ?? .note,
                  text: try c.decodeIfPresent(String.self, forKey: .text) ?? "")
    }
}

/// 伺服器回的一筆事件:app 的 Incident 加上核心多的欄位(關聯告警 id)
struct ServerIncident: Decodable {
    var incident: Incident
    var alertIds: [String]

    private enum DKeys: String, CodingKey {
        case id, title, sev, status, services, commander, startedAt, resolvedAt, impact, timeline, aiSummary, followUps, alertIds
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: DKeys.self)
        incident = Incident(
            id: try c.decode(String.self, forKey: .id),
            title: try c.decodeIfPresent(String.self, forKey: .title) ?? "",
            sev: try c.decodeIfPresent(Int.self, forKey: .sev) ?? 3,
            status: (try? c.decodeIfPresent(IncidentStatus.self, forKey: .status)) ?? .investigating,
            services: try c.decodeIfPresent([String].self, forKey: .services) ?? [],
            commander: try c.decodeIfPresent(String.self, forKey: .commander) ?? "",
            startedAt: try c.decodeIfPresent(Date.self, forKey: .startedAt) ?? .distantPast,
            resolvedAt: try? c.decodeIfPresent(Date.self, forKey: .resolvedAt),
            impact: try c.decodeIfPresent(String.self, forKey: .impact) ?? "",
            timeline: try c.decodeIfPresent([IncidentEvent].self, forKey: .timeline) ?? [],
            aiSummary: try c.decodeIfPresent(String.self, forKey: .aiSummary),
            followUps: try c.decodeIfPresent([String].self, forKey: .followUps) ?? [])
        alertIds = try c.decodeIfPresent([String].self, forKey: .alertIds) ?? []
    }
}

extension IssueRemote {
    private func decodeIncident<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的事件格式看不懂:\(error.localizedDescription)")
        }
    }

    /// GET /incidents:全部事件(新的在前)
    func listIncidents(org: String) throws -> [ServerIncident] {
        try decodeIncident([ServerIncident].self, try send("GET", orgURL(org, ["incidents"]), nil))
    }

    /// POST /incidents:宣告(欄位見 core 的 service.IncidentInput)
    func declareIncident(org: String, _ body: [String: Any]) throws -> ServerIncident {
        try decodeIncident(ServerIncident.self, try send("POST", orgURL(org, ["incidents"]), try json(body)))
    }

    /// POST /incidents/{id}:更新(欄位見 core 的 service.IncidentPatch)
    func updateIncident(org: String, id: String, _ body: [String: Any]) throws -> ServerIncident {
        try decodeIncident(ServerIncident.self, try send("POST", orgURL(org, ["incidents", id]), try json(body)))
    }

    /// POST /incidents/import:第一次搬資料用(組織管理員;伺服器已經有事件就拒絕)
    func importIncidents(org: String, _ list: [Incident]) throws {
        let data = try MCPJSON.encoder.encode(list)
        let arr = try JSONSerialization.jsonObject(with: data)
        _ = try send("POST", orgURL(org, ["incidents", "import"]), try json(["incidents": arr]))
    }
}

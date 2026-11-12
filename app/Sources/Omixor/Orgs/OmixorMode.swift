import Foundation

// 兩種資料來源,完全切開(同 Go 版 web/internal/core/mode.go):
//
//   本機:組織與 Issue 等都存在這台 Mac 的資料夾(orgs.json、orgs/<id>/issues.json…),不用登入
//   遠端:用 evoleap 授權登入 omixor server;組織清單(名稱、圖示、顏色、備註)、成員權限、Issue 都來自伺服器
//
// 目前的模式存在資料夾的 mode.json。遠端組織的 id 一律加 "~" 前綴(本機組織 id 不會有 "~"),
// 所以兩邊的資料夾(orgs/~xxx)、鑰匙圈、範例資料都不會撞名;遠端組織清單的快取另存 orgs-remote.json。
//
// 可以連哪些遠端 omixor 由啟動設定決定(畫面上不能新增,也不顯示網址)。來源(合併,id 或網址重複的只留第一個):
//
//   環境變數 OMIXOR_SERVERS:JSON 陣列,或指向 JSON 檔的路徑
//   資料夾裡的 servers.json
//   環境變數 OMIXOR_SERVER(或舊的 OPS_ISSUE_SERVER):單一網址
//
// 格式:[{"id":"prod","name":"正式站","url":"https://…","auth":["evoleap"]}];auth 沒寫就是 ["evoleap"]。
// 沒有任何設定就沒有遠端,只能用本機,啟動時也不會出現「選擇連接對象」。

enum OmixorMode {
    static let local = "local"
    static let remote = "remote"
    /// 遠端組織的 id 前綴
    static let remoteOrgPrefix = "~"
    /// 遠端 omixor token 在鑰匙圈的前綴:不分組織,全部遠端組織共用同一個登入
    static let tokenScope = "remote/"

    struct File: Codable, Equatable {
        var mode: String
        var server: String?
    }

    struct AuthOption: Hashable, Identifiable {
        var id: String
        var label: String
    }

    struct Server: Hashable, Identifiable {
        var id: String
        var name: String
        var url: URL
        var auth: [AuthOption]
    }

    /// 支援的授權方式(之後有新的就加在這裡,並在 AppState.connect 實作)
    static let authLabels = ["evoleap": "evoleap 帳號"]

    /// 測試用:非 nil 時取代啟動設定
    nonisolated(unsafe) static var testServers: [Server]?

    private struct Entry: Decodable {
        var id: String?
        var name: String?
        var url: String?
        var auth: [String]?
    }

    private static func parse(_ data: Data) -> [Entry] { (try? JSONDecoder().decode([Entry].self, from: data)) ?? [] }

    /// 啟動設定裡的遠端 omixor(沒設定回傳空陣列)
    static var servers: [Server] {
        if let t = testServers { return t }
        let env = ProcessInfo.processInfo.environment
        var raw: [Entry] = []
        if let v = env["OMIXOR_SERVERS"]?.trimmingCharacters(in: .whitespacesAndNewlines), !v.isEmpty {
            if v.hasPrefix("[") { raw += parse(Data(v.utf8)) } else if let d = try? Data(contentsOf: URL(fileURLWithPath: v)) { raw += parse(d) }
        }
        if let d = try? Data(contentsOf: OrgFiles.root.appendingPathComponent("servers.json")) { raw += parse(d) }
        for k in ["OMIXOR_SERVER", "OPS_ISSUE_SERVER"] {
            if let v = env[k]?.trimmingCharacters(in: .whitespacesAndNewlines), !v.isEmpty {
                raw.append(Entry(id: "default", url: v))
                break
            }
        }
        var out: [Server] = []
        var seenId = Set<String>(), seenURL = Set<String>()
        for e in raw {
            var s = (e.url ?? "").trimmingCharacters(in: .whitespaces)
            while s.hasSuffix("/") { s.removeLast() }
            guard s.hasPrefix("https://") || s.hasPrefix("http://"), let u = URL(string: s), u.host != nil else { continue }
            let id = (e.id ?? "").trimmingCharacters(in: .whitespaces).isEmpty ? s : e.id!.trimmingCharacters(in: .whitespaces)
            guard !seenId.contains(id), !seenURL.contains(s) else { continue }
            seenId.insert(id); seenURL.insert(s)
            let name = (e.name ?? "").trimmingCharacters(in: .whitespaces)
            let methods = (e.auth ?? []).isEmpty ? ["evoleap"] : e.auth!
            out.append(Server(id: id, name: name.isEmpty ? (u.host ?? s) : name, url: u,
                              auth: methods.map { AuthOption(id: $0, label: authLabels[$0] ?? $0) }))
        }
        return out
    }

    /// 用 id 或網址找設定裡的遠端
    static func find(_ idOrURL: String?) -> Server? {
        guard var v = idOrURL?.trimmingCharacters(in: .whitespaces), !v.isEmpty else { return nil }
        while v.hasSuffix("/") { v.removeLast() }
        return servers.first { $0.id == v || $0.url.absoluteString == v }
    }

    static var fileURL: URL { OrgFiles.root.appendingPathComponent("mode.json") }

    /// 目前的模式;上次連的遠端已經不在啟動設定裡就回本機
    static var current: File {
        guard let d = try? Data(contentsOf: fileURL), let f = try? JSONDecoder().decode(File.self, from: d) else { return File(mode: local) }
        if f.mode == remote, let s = find(f.server) { return File(mode: remote, server: s.url.absoluteString) }
        return File(mode: local)
    }

    static func save(_ f: File) {
        let e = JSONEncoder()
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        try? e.encode(f).write(to: fileURL, options: .atomic)
    }

    static var isRemote: Bool { current.mode == remote }

    /// 遠端模式下連的 omixor server(本機模式 nil)
    static var remoteServer: Server? {
        let c = current
        return c.mode == remote ? find(c.server) : nil
    }

    static func isRemoteOrg(_ id: String) -> Bool { id.hasPrefix(remoteOrgPrefix) }

    /// 內部的組織 id 換成伺服器上的組織 id
    static func serverOrgId(_ id: String) -> String { isRemoteOrg(id) ? String(id.dropFirst(remoteOrgPrefix.count)) : id }

    /// 遠端組織清單的快取
    static var remoteOrgsURL: URL { OrgFiles.root.appendingPathComponent("orgs-remote.json") }
}

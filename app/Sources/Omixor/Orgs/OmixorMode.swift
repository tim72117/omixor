import Foundation

// 兩種資料來源,完全切開(同 Go 版 web/internal/core/mode.go):
//
//   本機:組織與 Issue 等都存在這台 Mac 的資料夾(orgs.json、orgs/<id>/issues.json…),不用登入
//   遠端:用 evoleap 授權登入 omixor server;組織清單(名稱、圖示、顏色、備註)、成員權限、Issue 都來自伺服器
//
// 目前的模式存在資料夾的 mode.json。遠端組織的 id 一律是 "~<伺服器>~<組織>"(本機組織 id 不會有 "~";
// <伺服器> 是 serverKey,啟動設定裡的 id),所以本機與各台伺服器的資料夾(orgs/~cloud~xxx)、鑰匙圈、範例資料都不會撞名。
// 遠端組織清單的快取與 omixor 登入 token 也是每台伺服器各一份(orgs-remote-<伺服器>.json、鑰匙圈的 remote/<伺服器>/)。
// 舊版只有一個遠端:舊格式的快取(orgs/~<組織>、鑰匙圈的 remote/)當作設定裡第一個遠端的,第一次用到時複製過去(舊的不刪)。
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
    /// 舊版的遠端 omixor token 範圍(只有一個遠端時;現在每台伺服器各一把,見 tokenScope(for:))
    static let legacyTokenScope = "remote/"

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

    /// 內部的組織 id 換成伺服器上的組織 id:"~<伺服器>~<組織>" → "<組織>"(舊格式 "~<組織>" 也接受)
    static func serverOrgId(_ id: String) -> String {
        guard isRemoteOrg(id) else { return id }
        let rest = id.dropFirst(remoteOrgPrefix.count)
        if let r = rest.range(of: remoteOrgPrefix) { return String(rest[r.upperBound...]) }
        return String(rest)
    }

    /// 遠端 omixor 在本機的名稱(組織 id 前綴、快取檔名、鑰匙圈範圍都用它):啟動設定裡的 id;不在設定裡就用 host(:port)。
    /// 只留英數字與 . _ -
    static func serverKey(_ url: URL) -> String {
        var s = url.absoluteString
        while s.hasSuffix("/") { s.removeLast() }
        let raw = find(s)?.id ?? (url.host.map { h in url.port.map { "\(h):\($0)" } ?? h } ?? s)
        let ok = Set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-")
        let k = String(raw.map { ok.contains($0) ? $0 : "_" })
        return k.isEmpty ? "server" : k
    }

    /// 這台伺服器的組織在本機的 id 前綴:"~<伺服器>~"
    static func orgPrefix(for url: URL) -> String { remoteOrgPrefix + serverKey(url) + remoteOrgPrefix }

    /// 這台伺服器的 omixor token 在鑰匙圈的範圍
    static func tokenScope(for url: URL) -> String { legacyTokenScope + serverKey(url) + "/" }

    /// 舊格式的快取屬於設定裡的第一個遠端
    static func isLegacyServer(_ url: URL) -> Bool { servers.first.map { serverKey($0.url) == serverKey(url) } ?? false }

    /// 遠端組織清單的快取(每台伺服器各一份)
    static func remoteOrgsURL(for url: URL) -> URL { OrgFiles.root.appendingPathComponent("orgs-remote-\(serverKey(url)).json") }
}

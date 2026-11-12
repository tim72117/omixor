import Foundation
import AppKit

// 組織:每個組織有自己的工作空間(Issue、虛擬角色與任務、文件櫃與其結構、連線、追蹤的 GitHub repo、GCP 專案)。
// 資料放在 <資料夾>/orgs/<組織 id>/;<資料夾> 是 ~/Library/Application Support/Ops(OPS_DATA_DIR 可改)。
// 第一次啟動時,舊版直接放在 <資料夾> 的 issues.json 等檔案會複製到預設組織(原檔保留不動)。
// 旁白批次(voice-jobs.json)是全部組織共用的。
// 畫面上的範例資料與動態牆分組織(OrgMock.swift):預設組織用範例資料,其他組織從空的開始。
// 本機模式:組織清單在 orgs.json。遠端模式(OmixorMode.swift):組織的全部資訊(名稱、圖示、顏色、備註)以 omixor server 為準,
// 這裡只存快取 orgs-remote.json;id 加 "~" 前綴,資料夾是 orgs/~<伺服器的組織 id>/(角色、文件櫃、連線等還是存在這台 Mac)。

struct Organization: Codable, Hashable, Identifiable {
    var id: String
    var name: String
    var symbol = "building.2"
    var color = "blue"
    var note = ""
    var createdAt = Date.now
}

struct OrgIndex: Codable {
    var current: String
    var orgs: [Organization]
}

enum OrgTemplate: String, CaseIterable, Identifiable {
    case blank, copyCurrent
    var id: String { rawValue }
    var label: String { self == .blank ? "全新空白" : "複製目前組織的設定" }
    var detail: String {
        self == .blank
            ? "沒有角色、沒有連線;文件櫃只有「總覽」「文件」兩個區塊,之後再自己設定。"
            : "複製角色(含角色設定、例行任務、文件存取範圍)、文件櫃結構、連線設定、追蹤的 GitHub repo 與 GCP 專案設定;不複製 Issue、任務、文件,連線要重新登入。"
    }
}

/// 組織檔案的位置與搬遷
enum OrgFiles {
    static let defaultId = "default"
    static let perOrgFiles = ["issues.json", "agents.json", "cabinet.json", "cabinet-layout.json", "connections.json", "github.json", "gcp.json"]

    /// 跑測試卻沒指定 OPS_DATA_DIR 時用的暫存資料夾:測試絕對不能碰到真正的資料
    private static let testFallback = FileManager.default.temporaryDirectory
        .appendingPathComponent("ops-test-data-\(ProcessInfo.processInfo.processIdentifier)", isDirectory: true)

    static var root: URL {
        let isTesting = NSClassFromString("XCTestCase") != nil
        let dir = ProcessInfo.processInfo.environment["OPS_DATA_DIR"].map { URL(fileURLWithPath: $0) }
            ?? (isTesting ? testFallback : FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
                .appendingPathComponent("Ops", isDirectory: true))
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    static func dir(_ orgId: String) -> URL {
        let d = root.appendingPathComponent("orgs", isDirectory: true).appendingPathComponent(orgId, isDirectory: true)
        try? FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
        return d
    }

    static var indexURL: URL { root.appendingPathComponent("orgs.json") }

    static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        return e
    }

    static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    /// 讀組織清單;沒有就建立預設組織,並把舊版放在根目錄的檔案複製進去
    static func loadIndex() -> OrgIndex {
        if let data = try? Data(contentsOf: indexURL), let idx = try? decoder().decode(OrgIndex.self, from: data), !idx.orgs.isEmpty {
            return idx
        }
        let fm = FileManager.default
        let target = dir(defaultId)
        for f in perOrgFiles {
            let src = root.appendingPathComponent(f), dst = target.appendingPathComponent(f)
            if fm.fileExists(atPath: src.path), !fm.fileExists(atPath: dst.path) { try? fm.copyItem(at: src, to: dst) }
        }
        let idx = OrgIndex(current: defaultId, orgs: [Organization(id: defaultId, name: "evoleap", symbol: "graduationcap", color: "teal")])
        saveIndex(idx)
        return idx
    }

    static func saveIndex(_ idx: OrgIndex) {
        if let data = try? encoder().encode(idx) { try? data.write(to: indexURL, options: .atomic) }
    }

    /// 遠端組織清單的快取(上次從伺服器讀到的);沒有回傳 nil
    static func loadRemoteIndex() -> OrgIndex? {
        guard let data = try? Data(contentsOf: OmixorMode.remoteOrgsURL), let idx = try? decoder().decode(OrgIndex.self, from: data),
              !idx.orgs.isEmpty else { return nil }
        return idx
    }

    static func saveRemoteIndex(_ idx: OrgIndex) {
        if let data = try? encoder().encode(idx) { try? data.write(to: OmixorMode.remoteOrgsURL, options: .atomic) }
    }
}

extension AppState {
    /// 目前組織的資料夾
    var dataDir: URL {
        ensureOrgs()
        return OrgFiles.dir(currentOrgId)
    }

    var currentOrg: Organization? { orgs.first { $0.id == currentOrgId } }

    func ensureOrgs() {
        guard !orgsLoaded else { return }
        orgsLoaded = true
        // 遠端模式但沒有組織清單的快取(不該發生):回本機
        if OmixorMode.isRemote, OrgFiles.loadRemoteIndex() == nil { OmixorMode.save(OmixorMode.File(mode: OmixorMode.local)) }
        let idx = OmixorMode.isRemote ? OrgFiles.loadRemoteIndex()! : OrgFiles.loadIndex()
        orgs = idx.orgs
        currentOrgId = idx.orgs.contains { $0.id == idx.current } ? idx.current : idx.orgs[0].id
        TokenVault.scope = currentOrgId == OrgFiles.defaultId ? "" : "\(currentOrgId)/"
        switchMock()
    }

    private func saveOrgs() {
        let idx = OrgIndex(current: currentOrgId, orgs: orgs)
        if OmixorMode.isRemoteOrg(currentOrgId) { OrgFiles.saveRemoteIndex(idx) } else { OrgFiles.saveIndex(idx) }
    }

    /// 遠端模式:組織清單換成伺服器的(目前的組織還在就不動,不在就選第一個)。回傳目前的組織有沒有換
    @discardableResult
    func applyRemoteOrgs(_ list: [ServerOrg]) -> Bool {
        let remote = list.map(\.organization)
        let saved = OrgFiles.loadRemoteIndex()?.current ?? currentOrgId
        let cur = remote.contains { $0.id == saved } ? saved : (remote.first?.id ?? saved)
        OrgFiles.saveRemoteIndex(OrgIndex(current: cur, orgs: remote))
        guard OmixorMode.isRemote, orgsLoaded else { return false }
        if orgs != remote { orgs = remote }
        return cur != currentOrgId
    }

    /// 遠端組織的初始檔案(角色、連線、文件櫃結構存在這台 Mac,伺服器只有組織本身與 Issue)
    private func writeOrgTemplate(_ id: String, _ template: OrgTemplate) {
        let d = OrgFiles.dir(id)
        let enc = OrgFiles.encoder()
        switch template {
        case .blank:
            try? enc.encode(AgentData()).write(to: d.appendingPathComponent("agents.json"))
            try? enc.encode([Connection]()).write(to: d.appendingPathComponent("connections.json"))
            try? enc.encode(CabinetLayout.blank).write(to: d.appendingPathComponent("cabinet-layout.json"))
        case .copyCurrent:
            let agents = self.agents.map { a -> Agent in
                var a = a
                a.routines = a.routines.map { var r = $0; r.lastRunAt = nil; r.createdAt = .now; return r }
                a.createdAt = .now
                return a
            }
            try? enc.encode(AgentData(agents: agents, tasks: [])).write(to: d.appendingPathComponent("agents.json"))
            try? enc.encode(connections).write(to: d.appendingPathComponent("connections.json"))
            try? enc.encode(CabinetLayout.sections).write(to: d.appendingPathComponent("cabinet-layout.json"))
            try? enc.encode(githubSettings).write(to: d.appendingPathComponent("github.json"))
            try? enc.encode(gcpSettings).write(to: d.appendingPathComponent("gcp.json"))
        }
    }

    /// 遠端模式:在伺服器建立組織(要伺服器管理員),寫好這台 Mac 上的初始檔案;不會切換過去
    func createRemoteOrg(name: String, symbol: String, color: String, template: OrgTemplate) async throws -> Organization {
        guard let srv = OmixorMode.remoteServer else { throw CabinetError("目前不是遠端模式") }
        let root = OmixorMode.serverOrgId(newOrgId(for: name))
        var id = root, n = 2
        while orgs.contains(where: { $0.id == OmixorMode.remoteOrgPrefix + id }) { id = "\(root)-\(n)"; n += 1 }
        let created = try await OmixorAuth.createOrg(server: srv.url, id: id, name: name, symbol: symbol, color: color)
        let org = created.organization
        writeOrgTemplate(org.id, template)
        if let list = await OmixorAuth.fetchOrgs(server: srv.url) { applyRemoteOrgs(list) } else if !orgs.contains(org) { orgs.append(org); saveOrgs() }
        log(.person(me), "building.2", "在 omixor server 建立組織「\(name)」", .settings)
        return org
    }

    /// 遠端模式:改伺服器上的組織(要伺服器管理員)
    func updateRemoteOrg(_ org: Organization) async throws {
        guard let srv = OmixorMode.remoteServer else { throw CabinetError("目前不是遠端模式") }
        let saved = try await OmixorAuth.updateOrg(server: srv.url, org).organization
        if let i = orgs.firstIndex(where: { $0.id == saved.id }) { orgs[i] = saved }
        saveOrgs()
    }

    func newOrgId(for name: String) -> String {
        let ascii = name.lowercased().unicodeScalars.map { CharacterSet.alphanumerics.contains($0) && $0.isASCII ? String($0) : "-" }.joined()
        let base = ascii.split(separator: "-").joined(separator: "-")
        let root = base.isEmpty ? "org-\(orgs.count + 1)" : base
        var id = root
        var n = 2
        while orgs.contains(where: { $0.id == id }) || FileManager.default.fileExists(atPath: OrgFiles.root.appendingPathComponent("orgs/\(id)").path) {
            id = "\(root)-\(n)"; n += 1
        }
        return id
    }

    /// 建立組織並寫好它的初始檔案;不會切換過去
    @discardableResult
    func createOrg(name: String, symbol: String = "building.2", color: String = "blue", template: OrgTemplate) -> Organization {
        ensureOrgs()
        let org = Organization(id: newOrgId(for: name), name: name, symbol: symbol, color: color)
        writeOrgTemplate(org.id, template)
        orgs.append(org)
        saveOrgs()
        log(.person(me), "building.2", "建立組織「\(name)」", .settings)
        return org
    }

    func updateOrg(_ org: Organization) {
        guard let i = orgs.firstIndex(where: { $0.id == org.id }) else { return }
        orgs[i] = org
        saveOrgs()
    }

    /// 刪除組織:資料夾移到垃圾桶(可以從垃圾桶救回);不能刪目前的組織
    func deleteOrg(_ id: String) throws {
        guard !OmixorMode.isRemoteOrg(id) else { throw CabinetError("遠端的組織由 omixor server 管理,不能在 app 刪除") }
        guard id != currentOrgId else { throw CabinetError("不能刪除目前使用中的組織,先切換到別的組織") }
        guard orgs.count > 1 else { throw CabinetError("至少要留一個組織") }
        let d = OrgFiles.root.appendingPathComponent("orgs/\(id)", isDirectory: true)
        // 資料夾移走前先記下連線與專頁,才能清掉鑰匙圈裡的登入與專頁權杖
        let fbPages = (try? OrgFiles.decoder().decode(FBSettings.self, from: Data(contentsOf: d.appendingPathComponent("facebook.json"))))?.pages ?? []
        let conns = (try? OrgFiles.decoder().decode([Connection].self, from: Data(contentsOf: d.appendingPathComponent("connections.json")))) ?? []
        if FileManager.default.fileExists(atPath: d.path) {
            do { try FileManager.default.trashItem(at: d, resultingItemURL: nil) } catch {
                // 這個磁碟沒有垃圾桶時,搬到資料夾裡的 deleted-orgs/,不直接刪掉
                let keep = OrgFiles.root.appendingPathComponent("deleted-orgs", isDirectory: true)
                try FileManager.default.createDirectory(at: keep, withIntermediateDirectories: true)
                try FileManager.default.moveItem(at: d, to: keep.appendingPathComponent("\(id)-\(Int(Date().timeIntervalSince1970))"))
            }
        }
        // 這個組織在鑰匙圈裡的登入一併清掉
        let prefix = id == OrgFiles.defaultId ? "" : "\(id)/"
        let saved = TokenVault.scope
        dropMock(id)
        TokenVault.scope = prefix
        for c in conns { TokenVault.removeAll(c.id) }
        LineStore.removeSecrets()
        for p in fbPages { FacebookStore.setPageToken(nil, p.id) }
        TokenVault.scope = saved
        let name = orgs.first { $0.id == id }?.name ?? id
        orgs.removeAll { $0.id == id }
        saveOrgs()
        log(.person(me), "trash", "刪除組織「\(name)」(資料夾已移到垃圾桶)", .settings)
    }

    /// 切換組織:各功能重新讀這個組織的資料
    func switchOrg(_ id: String) {
        ensureOrgs()
        guard id != currentOrgId, orgs.contains(where: { $0.id == id }) else { return }
        currentOrgId = id
        saveOrgs()
        reloadOrgData()
        log(.person(me), "arrow.left.arrow.right", "切換到組織「\(currentOrg?.name ?? id)」", .settings)
    }

    /// 組織或模式換了之後,各功能重新讀目前組織的資料
    func reloadOrgData() {
        TokenVault.scope = currentOrgId == OrgFiles.defaultId ? "" : "\(currentOrgId)/"
        switchMock()
        issueStore?.load()
        agentStore?.load()
        cabinetLoaded = false
        Cabinet.load(self)
        connectionStates = [:]
        connStore?.load()
        ghStore?.load()
        if ghStore?.isLoggedIn == true { Task { await ghStore?.refresh() } }
        gcpStore?.load()
        lineStore?.orgChanged()
        fbStore?.load()
        if gcpStore?.isLoggedIn == true, gcpStore?.isConfigured == true { Task { await gcpStore?.refresh() } }
        focusId = nil
    }

    /// 換模式(本機 ⇄ 遠端)後重讀組織清單與各功能的資料
    func reloadOrgsForMode() {
        orgsLoaded = false
        ensureOrgs()
        reloadOrgData()
    }
}

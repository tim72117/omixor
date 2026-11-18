import Foundation

// 文件櫃:純文字(Markdown)文件,依組織的文件櫃結構(CabinetLayout:區塊與頁籤,每個組織可以自訂)分類。
// 預設組織的結構是 evoleap 研發基地(/studio,CabinetLayout.evoleapStudio),照 shuttle 的
// web/src/studio/proposal/ProposalLayout.tsx(SECTIONS、LEVELS、DEV、IDEAS、DOCS)與 web/src/studio/articles/registry.ts;
// evoleap 研發基地改版時要跟著改 evoleapStudio。
// 資料一律讀寫 core server(遠端 omixor 或本機守護服務,CabinetRemote.swift),每 10 秒重讀一次,別人或角色的修改也看得到。

struct CabinetTab: Hashable, Identifiable, Codable {
    var path: String              // 頁籤的 id,一律 / 開頭;evoleap 研發基地的頁籤就是網址路徑,例如 /studio/tools
    var label: String
    var code: String?             // L0…L4
    var group: String?            // 頁籤群組,例如「做出自己的軟體」
    var id: String { path }
    var title: String { code.map { "\($0) \(label)" } ?? label }
}

extension CabinetTab {
    // 寬鬆解碼(LossyDecoding.swift):path 必填(缺了略過這一頁籤),label 缺時用 path
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decode(String.self, forKey: .path)
        label = c.lossy(.label, default: path)
        code = c.lossyOptional(String.self, .code)
        group = c.lossyOptional(String.self, .group)
    }
}

extension CabinetSection {
    // 寬鬆解碼:tabs 是 null 或缺 → [],壞掉的頁籤略過;id 必填
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        label = c.lossy(.label, default: id)
        path = c.lossy(.path, default: "")
        tabs = c.lossyArray(CabinetTab.self, .tabs)
    }
}

struct CabinetSection: Hashable, Identifiable, Codable {
    var id: String                // overview、todo、members、levels、dev、ideas、docs、tools(自訂區塊是自動產生的英文 id)
    var label: String
    var path = ""                 // 對應的網址(選填)
    var tabs: [CabinetTab] = []
}

enum CabinetLayout {
    /// 目前組織的結構(切換組織或修改結構時更新)
    nonisolated(unsafe) static var sections: [CabinetSection] = evoleapStudio

    /// 新的空白組織用的結構
    static let blank: [CabinetSection] = [
        CabinetSection(id: "overview", label: "總覽"),
        CabinetSection(id: "docs", label: "文件"),
    ]

    /// evoleap 研發基地的結構(預設組織)
    static let evoleapStudio: [CabinetSection] = [
        CabinetSection(id: "overview", label: "總覽", path: "/studio"),
        CabinetSection(id: "todo", label: "待辦", path: "/studio/todo"),
        CabinetSection(id: "members", label: "會員", path: "/studio/members"),
        CabinetSection(id: "levels", label: "課程", path: "/studio/tools", tabs: [
            CabinetTab(path: "/studio/tools", label: "AI 工具介紹", code: "L0", group: "做出自己的軟體"),
            CabinetTab(path: "/studio/ai-tools", label: "AI 工具入門", code: "L1", group: "做出自己的軟體"),
            CabinetTab(path: "/studio/vibe-coding", label: "Vibe Coding 入門", code: "L2", group: "做出自己的軟體"),
            CabinetTab(path: "/studio/ai-agent", label: "AI Agent 整合應用", code: "L3", group: "做出自己的軟體"),
            CabinetTab(path: "/studio/advanced", label: "進階", code: "L4", group: "做出自己的軟體"),
            CabinetTab(path: "/studio/ai-tips", label: "AI 工具技巧"),
            CabinetTab(path: "/studio/kids-web", label: "兒童網頁課"),
            CabinetTab(path: "/studio/web-basics", label: "網頁運作知識"),
        ]),
        CabinetSection(id: "dev", label: "開發", path: "/studio/visuals", tabs: [
            CabinetTab(path: "/studio/visuals", label: "即時教學影像"),
            CabinetTab(path: "/studio/actor-interaction", label: "演員互動"),
            CabinetTab(path: "/studio/portrait", label: "直式舞台"),
            CabinetTab(path: "/studio/scenes", label: "場景"),
        ]),
        CabinetSection(id: "ideas", label: "構想", path: "/studio/mcp", tabs: [
            CabinetTab(path: "/studio/mcp", label: "課程 MCP"),
            CabinetTab(path: "/studio/live-script-mcp", label: "即時腳本 MCP"),
            CabinetTab(path: "/studio/member", label: "會員空間"),
            CabinetTab(path: "/studio/kids-family", label: "兒童課家長模式"),
            CabinetTab(path: "/studio/resources", label: "資源系統"),
            CabinetTab(path: "/studio/news-feed", label: "科技新知動態牆"),
            CabinetTab(path: "/studio/extensions", label: "延伸課程"),
            CabinetTab(path: "/studio/ops-course", label: "從上線到維運"),
        ]),
        CabinetSection(id: "docs", label: "文件", path: "/studio/docs", tabs: [
            CabinetTab(path: "/studio/docs", label: "全部文件"),
            CabinetTab(path: "/docs/course-goals", label: "課程目標"),
            CabinetTab(path: "/docs/research-report", label: "研究報告"),
            CabinetTab(path: "/docs/research/methodology", label: "課程設計方法論"),
            CabinetTab(path: "/docs/research/recording", label: "影音錄製實務"),
            CabinetTab(path: "/docs/research/video-shotcraft", label: "video-shotcraft"),
            CabinetTab(path: "/docs/research/hyperframes", label: "HyperFrames"),
            CabinetTab(path: "/docs/research/marketing", label: "軟體與課程行銷手法"),
            CabinetTab(path: "/docs/research/voice-cost", label: "旁白語音的費用評估"),
            CabinetTab(path: "/docs/research/tts-landscape", label: "文字轉語音工具現況"),
            CabinetTab(path: "/docs/research/script-template", label: "腳本範本結構"),
            CabinetTab(path: "/docs/live", label: "即時教學畫面規格"),
            CabinetTab(path: "/docs/recorder", label: "宣傳影片錄製工具"),
        ]),
        CabinetSection(id: "tools", label: "工具", path: "/materials"),
    ]

    static func section(_ id: String) -> CabinetSection? {
        sections.first { $0.id == id || $0.label == id }
    }

    /// 依 id、標籤或網址找區塊;頁籤可給網址路徑或名稱(也接受「L0」這種代號)
    static func resolve(section: String?, tab: String?) throws -> (CabinetSection, CabinetTab?) {
        if let tab, !tab.isEmpty {
            let candidates = section.flatMap { Self.section($0) }.map { [$0] } ?? sections
            for s in candidates {
                if let t = s.tabs.first(where: { $0.path == tab || $0.label == tab || $0.code == tab || $0.title == tab }) { return (s, t) }
            }
            throw CabinetError("找不到頁籤「\(tab)」" + (section.map { "(區塊 \($0))" } ?? ""))
        }
        guard let section, let s = Self.section(section) ?? sections.first(where: { $0.path == section }) else {
            throw CabinetError("要指定區塊(\(sections.map(\.id).joined(separator: "、")))")
        }
        return (s, nil)
    }

    static func tab(_ path: String?) -> CabinetTab? {
        guard let path else { return nil }
        for s in sections { if let t = s.tabs.first(where: { $0.path == path }) { return t } }
        return nil
    }
}

struct CabinetError: Error, LocalizedError {
    let message: String
    init(_ m: String) { message = m }
    var errorDescription: String? { message }
}

struct CabinetDoc: Hashable, Identifiable, Codable {
    var id: String                // DOC-1
    var title: String
    var body = ""                 // Markdown
    var section: String           // CabinetSection.id
    var tab: String?              // CabinetTab.path
    var tags: [String] = []
    var author: String            // person id、"mcp" 或 "agent:<角色 id>"
    var createdAt = Date.now
    var updatedAt = Date.now
    var project: String?          // 所屬專案 id(PRJ-<n>);nil = 未分專案(原本的組織文件櫃,伺服器不帶這個欄位)

    var number: Int { Int(id.filter(\.isNumber)) ?? 0 }
    var location: String {
        let s = CabinetLayout.section(section)?.label ?? section
        return CabinetLayout.tab(tab).map { "\(s) › \($0.title)" } ?? s
    }
}

extension CabinetDoc {
    // 手寫解碼:之後加欄位時,舊檔缺欄位也讀得出來
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // 寬鬆解碼(LossyDecoding.swift):tags 是 null → [];id、section 必填(缺了列表會略過這一筆)
        id = try c.decode(String.self, forKey: .id)
        title = c.lossy(.title, default: "")
        body = c.lossy(.body, default: "")
        section = try c.decode(String.self, forKey: .section)
        tab = c.lossyOptional(String.self, .tab)
        tags = c.lossyArray(String.self, .tags)
        author = c.lossy(.author, default: "mcp")
        createdAt = c.lossy(.createdAt, default: .now)
        updatedAt = c.lossy(.updatedAt, default: createdAt)
        project = c.lossyOptional(String.self, .project).flatMap { $0.isEmpty ? nil : $0 }
    }
}

/// 組織底下的專案(core/model/project.go):目前只有文件櫃(結構與文件)分專案;權限沿用組織成員。id 是 PRJ-<n>
struct Project: Hashable, Identifiable, Codable {
    var id: String
    var name: String
    var description = ""
    var createdAt = Date.now
    var updatedAt = Date.now
}

extension Project {
    // 寬鬆解碼(LossyDecoding.swift):id 必填(缺了列表會略過這一筆),name 缺時用 id
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = c.lossy(.name, default: id)
        description = c.lossy(.description, default: "")
        createdAt = c.lossy(.createdAt, default: .now)
        updatedAt = c.lossy(.updatedAt, default: createdAt)
    }
}

/// 文件櫃的動作(畫面與 MCP 共用):一律透過 core server(IssueServerConfig.current:遠端 omixor 或本機守護服務),失敗丟錯誤。
@MainActor
enum Cabinet {
    private static var remoteClient: IssueRemote?
    private static var poller: Timer?
    /// 多久重新讀一次(秒);0 = 不定時讀
    static var pollInterval: TimeInterval = NSClassFromString("XCTestCase") != nil ? 0 : 10

    /// 目前連的 core server 的用戶端(和 Issue 同一個;連線換了會換新的)
    static var remote: IssueRemote {
        let cfg = IssueServerConfig.current
        if let c = remoteClient, c.matches(cfg) { return c }   // 連線設定與組織的 token 範圍都相同才沿用
        let c = IssueRemote(config: cfg)
        remoteClient = c
        return c
    }

    /// 從伺服器重讀專案、結構與文件換進畫面;失敗記在 cabinetRemoteError(畫面照用上次的)
    static func refresh(_ app: AppState) {
        let org = app.currentOrgId
        // 專案清單讀不到(舊版伺服器沒有這個端點)就當作沒有專案,不影響文件櫃
        if let ps = try? remote.listProjects(org: org) { applyProjects(app, ps) }
        do {
            apply(app, try remote.getCabinet(org: org, project: app.cabinetProject))
            if !app.cabinetProject.isEmpty, let base = try? remote.getCabinet(org: org, project: "") { applyBaseTemplates(app, base.docs) }
        } catch {
            if app.cabinetRemoteError != error.localizedDescription { app.cabinetRemoteError = error.localizedDescription }
        }
    }

    private static var polling = false

    /// 同 refresh,但請求在背景跑,回主執行緒才更新畫面(定時重讀、切換組織用;不擋主執行緒)
    static func reload(_ app: AppState) async {
        let org = app.currentOrgId, project = app.cabinetProject, r = remote, before = app.cabinetDocs
        do {
            let (ps, c, base) = try await r.run { r -> ([Project]?, RemoteCabinet, [CabinetDoc]?) in
                let ps = try? r.listProjects(org: org)
                // 看的專案被刪掉了(別人或角色刪的):改看未分專案
                let p = ps.map { ps in ps.contains { $0.id == project } ? project : "" } ?? project
                // 看專案時也讀未分專案的文件(只取範本;讀不到就沿用上次的)
                let base = p.isEmpty ? nil : (try? r.getCabinet(org: org, project: ""))?.docs
                return (ps, try r.getCabinet(org: org, project: p), base)
            }
            // 讀的期間換了組織或專案:這次讀到的不是畫面要的
            guard app.currentOrgId == org, app.cabinetProject == project else { return }
            if let ps { applyProjects(app, ps) }
            if app.cabinetProject != project {   // 專案被刪掉,已經切回未分專案
                apply(app, c)
                return
            }
            if let base { applyBaseTemplates(app, base) }
            // 讀的期間畫面上的文件被寫入改過:這次讀到的可能比較舊,等下一輪
            guard app.cabinetDocs == before else { return }
            apply(app, c)
        } catch {
            guard app.currentOrgId == org, app.cabinetProject == project else { return }
            if app.cabinetRemoteError != error.localizedDescription { app.cabinetRemoteError = error.localizedDescription }
        }
    }

    /// 換上新的專案清單;看的專案已經不在清單裡就切回未分專案(清掉畫面上的結構與文件)
    private static func applyProjects(_ app: AppState, _ ps: [Project]) {
        if ps != app.cabinetProjects { app.cabinetProjects = ps }
        if !app.cabinetProject.isEmpty, !ps.contains(where: { $0.id == app.cabinetProject }) { switchProject(app, "") }
    }

    /// 換看的專案:清掉畫面上的結構與文件(之後再讀)
    private static func switchProject(_ app: AppState, _ id: String) {
        app.cabinetProject = id
        CabinetLayout.sections = CabinetLayout.blank
        app.cabinetDocs = []
        app.cabinetBaseTemplates = []
        app.cabinetLayoutVersion += 1
        if app.cabinetRemoteError != nil { app.cabinetRemoteError = nil }
    }

    /// 換上未分專案的範本文件(看專案時用;看未分專案時範本就在 cabinetDocs 裡,這裡保持空的)
    static func applyBaseTemplates(_ app: AppState, _ docs: [CabinetDoc]) {
        let ts = app.cabinetProject.isEmpty ? [] : docs.filter { DocTemplates.kind($0.body) == DocTemplates.templateKind }
        if ts != app.cabinetBaseTemplates { app.cabinetBaseTemplates = ts }
    }

    /// 找範本用的文件:目前看的專案的文件,加上未分專案的範本(和 console 的 cabinet_doc_html 一樣);看未分專案時就是 cabinetDocs
    static func templateDocs(_ app: AppState) -> [CabinetDoc] {
        app.cabinetProject.isEmpty ? app.cabinetDocs : app.cabinetDocs + app.cabinetBaseTemplates
    }

    private static func apply(_ app: AppState, _ c: RemoteCabinet) {
        // layout 是 null 或整份解不開(寬鬆解碼成空陣列)時保留目前的結構,不把畫面清空
        if !c.layout.isEmpty, c.layout != CabinetLayout.sections {
            CabinetLayout.sections = c.layout
            app.cabinetLayoutVersion += 1
        }
        if c.docs != app.cabinetDocs { app.cabinetDocs = c.docs }
        if app.cabinetRemoteError != nil { app.cabinetRemoteError = nil }
    }

    private static func startPolling(_ app: AppState) {
        guard poller == nil, pollInterval > 0 else { return }
        poller = Timer.scheduledTimer(withTimeInterval: pollInterval, repeats: true) { [weak app] _ in
            Task { @MainActor in
                guard let app, app.cabinetLoaded, !polling else { return }
                polling = true
                defer { polling = false }
                await reload(app)
            }
        }
    }

    /// 寫入:失敗時丟出(畫面與 MCP 顯示原因)
    private static func remoteWrite<T>(_ app: AppState, _ f: (IssueRemote) throws -> T) throws -> T {
        do {
            let v = try f(remote)
            app.cabinetRemoteError = nil
            return v
        } catch {
            throw CabinetError(error.localizedDescription)
        }
    }

    /// 讀一次(之後定時重讀)
    static func load(_ app: AppState) {
        guard begin(app) else { return }
        refresh(app)
    }

    /// 換組織:清掉畫面上的文件櫃、開始定時重讀;回傳要不要讀(已經讀過就 false)。之後用 reload 在背景讀
    @discardableResult
    static func begin(_ app: AppState) -> Bool {
        guard !app.cabinetLoaded else { return false }
        app.cabinetLoaded = true
        CabinetLayout.sections = CabinetLayout.blank
        app.cabinetDocs = []
        app.cabinetProject = ""     // 換組織:回到未分專案
        app.cabinetBaseTemplates = []
        app.cabinetProjects = []
        app.cabinetLayoutVersion += 1
        startPolling(app)
        return true
    }

    // MARK: 結構

    static func saveLayout(_ app: AppState, _ sections: [CabinetSection]) throws {
        let saved = try remoteWrite(app) { try $0.saveCabinetLayout(org: app.currentOrgId, project: app.cabinetProject, sections) }
        CabinetLayout.sections = saved
        app.cabinetLayoutVersion += 1
    }

    /// 檢查新結構:id 不重複,而且被拿掉的區塊/頁籤裡不能還有文件(畫面先擋;伺服器也會再檢查)
    static func validate(_ app: AppState, _ sections: [CabinetSection]) -> String? {
        var ids = Set<String>()
        for s in sections {
            if s.label.trimmingCharacters(in: .whitespaces).isEmpty { return "區塊名稱不能是空的" }
            if !ids.insert(s.id).inserted { return "區塊 id 重複:\(s.id)" }
            for t in s.tabs {
                if t.label.trimmingCharacters(in: .whitespaces).isEmpty { return "「\(s.label)」有頁籤沒有名稱" }
                if !t.path.hasPrefix("/") { return "頁籤 id 要用 / 開頭:\(t.path)" }
                if !ids.insert(t.path).inserted { return "頁籤 id 重複:\(t.path)" }
            }
        }
        for d in app.cabinetDocs {
            guard let s = sections.first(where: { $0.id == d.section }) else { return "「\(d.title)」還在被拿掉的區塊裡,先搬走或刪除" }
            if let t = d.tab, !s.tabs.contains(where: { $0.path == t }) { return "「\(d.title)」還在被拿掉的頁籤裡,先搬走或刪除" }
        }
        return nil
    }

    @discardableResult
    static func create(_ app: AppState, title: String, body: String, section: String, tab: String?, tags: [String], by author: String) throws -> CabinetDoc {
        load(app)
        let d = try remoteWrite(app) {
            try $0.createDoc(org: app.currentOrgId, project: app.cabinetProject, title: title, body: body, section: section, tab: tab, tags: IssueStore.clean(tags), author: author)
        }
        app.cabinetDocs.removeAll { $0.id == d.id }
        app.cabinetDocs.insert(d, at: 0)
        app.log(actor(app, d.author), "archivebox", "文件櫃新增 \(d.id)「\(d.title)」(\(d.location))", .cabinet)
        return d
    }

    @discardableResult
    static func update(_ app: AppState, _ id: String, _ change: (inout CabinetDoc) -> Void) throws -> CabinetDoc? {
        guard let i = app.cabinetDocs.firstIndex(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else { return nil }
        var d = app.cabinetDocs[i]
        let before = d
        change(&d)
        d.tags = IssueStore.clean(d.tags)
        guard d != before else { return d }
        let saved = try remoteWrite(app) { try $0.updateDoc(org: app.currentOrgId, id: before.id, from: before, to: d) }
        if (saved.project ?? "") != app.cabinetProject {
            app.cabinetDocs.removeAll { $0.id == saved.id }   // 搬到別的專案:不在這個專案的畫面上了
        } else if let j = app.cabinetDocs.firstIndex(where: { $0.id == saved.id }) {
            app.cabinetDocs[j] = saved
        }
        return saved
    }

    /// 把文件搬到另一個專案(空字串 = 未分專案)的某個位置;位置要在目標專案的結構裡(伺服器會檢查)
    @discardableResult
    static func move(_ app: AppState, _ id: String, toProject project: String, section: String, tab: String?) throws -> CabinetDoc? {
        let saved = try update(app, id) {
            $0.project = project.isEmpty ? nil : project
            $0.section = section
            $0.tab = tab
        }
        if let saved {
            app.log(actor(app, app.me), "archivebox", "文件櫃 \(saved.id)「\(saved.title)」搬到\(projectName(app, project))", .cabinet)
        }
        return saved
    }

    // MARK: 專案

    /// 專案的顯示名稱(空 = 未分專案;找不到就是 id)
    static func projectName(_ app: AppState, _ id: String) -> String {
        if id.isEmpty { return "未分專案" }
        return app.cabinetProjects.first { $0.id == id }?.name ?? id
    }

    /// 切換看的專案(空字串 = 未分專案):清掉畫面再從伺服器讀那個專案的結構與文件
    static func selectProject(_ app: AppState, _ id: String) async {
        guard id != app.cabinetProject else { return }
        switchProject(app, id)
        await reload(app)
    }

    /// 另一個專案的結構(搬移文件時選位置用);目前看的專案直接用畫面上的
    static func layout(_ app: AppState, project: String) async throws -> [CabinetSection] {
        if project == app.cabinetProject { return CabinetLayout.sections }
        let org = app.currentOrgId
        let c = try await remote.run { try $0.getCabinet(org: org, project: project) }
        return c.layout.isEmpty ? CabinetLayout.blank : c.layout
    }

    /// 新增專案(組織管理員)
    @discardableResult
    static func createProject(_ app: AppState, name: String, description: String = "") throws -> Project {
        let p = try remoteWrite(app) {
            try $0.createProject(org: app.currentOrgId, name: name.trimmingCharacters(in: .whitespaces), description: description)
        }
        app.cabinetProjects.removeAll { $0.id == p.id }
        app.cabinetProjects.append(p)
        return p
    }

    /// 改專案名稱或說明(組織管理員)
    @discardableResult
    static func updateProject(_ app: AppState, _ id: String, name: String? = nil, description: String? = nil) throws -> Project {
        let p = try remoteWrite(app) {
            try $0.updateProject(org: app.currentOrgId, id: id, name: name?.trimmingCharacters(in: .whitespaces), description: description)
        }
        if let i = app.cabinetProjects.firstIndex(where: { $0.id == p.id }) { app.cabinetProjects[i] = p }
        return p
    }

    /// 刪除專案(組織管理員;專案裡還有文件時伺服器拒絕,錯誤訊息原樣丟出)。刪的是正在看的專案就切回未分專案
    static func deleteProject(_ app: AppState, _ id: String) throws {
        try remoteWrite(app) { try $0.deleteProject(org: app.currentOrgId, id: id) }
        app.cabinetProjects.removeAll { $0.id == id }
        if app.cabinetProject == id {
            switchProject(app, "")
            refresh(app)
        }
    }

    static func delete(_ app: AppState, _ id: String) throws {
        try remoteWrite(app) { try $0.deleteDoc(org: app.currentOrgId, id: id) }
        app.cabinetDocs.removeAll { $0.id == id }
    }

    static func doc(_ app: AppState, _ id: String) -> CabinetDoc? {
        app.cabinetDocs.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
    }

    static func actor(_ app: AppState, _ author: String) -> Actor {
        app.issueStore?.actor(author) ?? IssueStore(app: app).actor(author)
    }

    static func authorName(_ app: AppState, _ author: String) -> String { app.name(of: actor(app, author)) }
}

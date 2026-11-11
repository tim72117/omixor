import Foundation

// 文件櫃:純文字(Markdown)文件,依 evoleap 研發基地(/studio)的區塊與頁籤分類。
// 結構照 shuttle 的 web/src/studio/proposal/ProposalLayout.tsx(SECTIONS、LEVELS、DEV、IDEAS、DOCS)與
// web/src/studio/articles/registry.ts;研發基地改版時要跟著改這裡。
// 存在 ~/Library/Application Support/Ops/cabinet.json(OPS_DATA_DIR 可改)。

struct CabinetTab: Hashable, Identifiable, Codable {
    var path: String              // 頁籤的 id,一律 / 開頭;研發基地的頁籤就是網址路徑,例如 /studio/tools
    var label: String
    var code: String?             // L0…L4
    var group: String?            // 頁籤群組,例如「做出自己的軟體」
    var id: String { path }
    var title: String { code.map { "\($0) \(label)" } ?? label }
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
        id = try c.decode(String.self, forKey: .id)
        title = try c.decode(String.self, forKey: .title)
        body = try c.decodeIfPresent(String.self, forKey: .body) ?? ""
        section = try c.decode(String.self, forKey: .section)
        tab = try c.decodeIfPresent(String.self, forKey: .tab)
        tags = try c.decodeIfPresent([String].self, forKey: .tags) ?? []
        author = try c.decodeIfPresent(String.self, forKey: .author) ?? "mcp"
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? .now
        updatedAt = try c.decodeIfPresent(Date.self, forKey: .updatedAt) ?? createdAt
    }
}

/// 文件櫃的動作(畫面與 MCP 共用);每次變更都寫回 cabinet.json
@MainActor
enum Cabinet {
    static func fileURL(_ app: AppState) -> URL { app.dataDir.appendingPathComponent("cabinet.json") }
    static func layoutURL(_ app: AppState) -> URL { app.dataDir.appendingPathComponent("cabinet-layout.json") }

    /// 讀檔(只讀一次)。檔案存在但讀不出來時不覆蓋,先改名保留,避免把文件洗掉
    static func load(_ app: AppState) {
        guard !app.cabinetLoaded else { return }
        app.cabinetLoaded = true
        // 結構:沒有檔案就用研發基地的結構(舊版資料)
        if let data = try? Data(contentsOf: layoutURL(app)), let l = try? OrgFiles.decoder().decode([CabinetSection].self, from: data) {
            CabinetLayout.sections = l
        } else {
            CabinetLayout.sections = CabinetLayout.evoleapStudio
        }
        app.cabinetLayoutVersion += 1
        let fileURL = fileURL(app)
        guard let data = try? Data(contentsOf: fileURL) else { app.cabinetDocs = []; return }
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        if let docs = try? d.decode([CabinetDoc].self, from: data) {
            app.cabinetDocs = docs
        } else {
            let keep = fileURL.deletingPathExtension().appendingPathExtension("unreadable-\(Int(Date().timeIntervalSince1970)).json")
            try? FileManager.default.moveItem(at: fileURL, to: keep)
            app.cabinetDocs = []
        }
    }

    static func save(_ app: AppState) {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? e.encode(app.cabinetDocs) { try? data.write(to: fileURL(app), options: .atomic) }
    }

    // MARK: 結構

    static func saveLayout(_ app: AppState, _ sections: [CabinetSection]) {
        CabinetLayout.sections = sections
        app.cabinetLayoutVersion += 1
        if let data = try? OrgFiles.encoder().encode(sections) { try? data.write(to: layoutURL(app), options: .atomic) }
    }

    /// 檢查新結構:id 不重複,而且被拿掉的區塊/頁籤裡不能還有文件
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
    static func create(_ app: AppState, title: String, body: String, section: String, tab: String?, tags: [String], by author: String) -> CabinetDoc {
        load(app)
        let num = (app.cabinetDocs.map(\.number).max() ?? 0) + 1
        let d = CabinetDoc(id: "DOC-\(num)", title: title, body: body, section: section, tab: tab,
                           tags: IssueStore.clean(tags), author: author)
        app.cabinetDocs.insert(d, at: 0)
        save(app)
        app.log(actor(app, author), "archivebox", "文件櫃新增 \(d.id)「\(title)」(\(d.location))", .cabinet)
        return d
    }

    @discardableResult
    static func update(_ app: AppState, _ id: String, _ change: (inout CabinetDoc) -> Void) -> CabinetDoc? {
        guard let i = app.cabinetDocs.firstIndex(where: { $0.id.caseInsensitiveCompare(id) == .orderedSame }) else { return nil }
        var d = app.cabinetDocs[i]
        let before = d
        change(&d)
        d.tags = IssueStore.clean(d.tags)
        guard d != before else { return d }
        d.updatedAt = .now
        app.cabinetDocs[i] = d
        save(app)
        return d
    }

    static func delete(_ app: AppState, _ id: String) {
        app.cabinetDocs.removeAll { $0.id == id }
        save(app)
    }

    static func doc(_ app: AppState, _ id: String) -> CabinetDoc? {
        app.cabinetDocs.first { $0.id.caseInsensitiveCompare(id) == .orderedSame }
    }

    static func actor(_ app: AppState, _ author: String) -> Actor {
        app.issueStore?.actor(author) ?? IssueStore(app: app).actor(author)
    }

    static func authorName(_ app: AppState, _ author: String) -> String { app.name(of: actor(app, author)) }
}

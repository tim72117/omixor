import Foundation

// 遠端模式(OmixorMode.swift)時文件櫃不讀寫本機的 cabinet.json / cabinet-layout.json,改透過 omixor server(core)的
// /api/orgs/<組織>/cabinet 與 /api/orgs/<組織>/docs 讀寫(路由說明見 core/README.md)。
// 網址、token 與重新登入都和 Issue 共用 IssueRemote(Issues/IssueRemote.swift)。
//
// 權限由伺服器決定:成員可以新增、修改、搬移、刪除文件(作者一律是登入的人;伺服器管理員可以指定署名),
// 改結構要組織管理員;存檔前伺服器也會依範本檢查必填欄位。
//
// 專案(/api/orgs/<組織>/projects):只有文件櫃分專案。文件櫃的端點帶 ?project=<專案 id>(或 body 的 project);
// 沒給 / 空字串 = 未分專案(原本的組織文件櫃)。POST docs/<id> 給 project 就是把文件搬到那個專案(空字串 = 搬回未分專案)。

/// GET /api/orgs/<組織>/cabinet 的回應
struct RemoteCabinet: Decodable {
    /// 專案 id(nil = 未分專案,伺服器不帶)
    var project: String?
    var layout: [CabinetSection]
    var docs: [CabinetDoc]
    var layoutSaved: Bool?

    private enum CodingKeys: String, CodingKey { case project, layout, docs, layoutSaved }

    // 寬鬆解碼(LossyDecoding.swift):layout、docs 是 null → [],壞掉的那筆略過
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        project = c.lossyOptional(String.self, .project).flatMap { $0.isEmpty ? nil : $0 }
        layout = c.lossyArray(CabinetSection.self, .layout)
        docs = c.lossyArray(CabinetDoc.self, .docs)
        layoutSaved = c.lossyOptional(Bool.self, .layoutSaved)
    }
}

extension IssueRemote {
    private func decodeCabinet<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的文件櫃格式看不懂:\(error.localizedDescription)")
        }
    }

    /// 文件櫃端點的網址:專案不是空的就加 ?project=<專案 id>
    func cabinetURL(_ org: String, _ rest: [String], project: String) -> URL {
        let u = orgURL(org, rest)
        guard !project.isEmpty, var c = URLComponents(url: u, resolvingAgainstBaseURL: false) else { return u }
        c.queryItems = [URLQueryItem(name: "project", value: project)]
        return c.url ?? u
    }

    /// 結構與文件(新的在前);project 空 = 未分專案
    func getCabinet(org: String, project: String = "") throws -> RemoteCabinet {
        try decodeCabinet(RemoteCabinet.self, try send("GET", cabinetURL(org, ["cabinet"], project: project), nil))
    }

    /// 改結構(組織管理員;被拿掉的區塊、頁籤裡不能還有這個專案的文件)
    func saveCabinetLayout(org: String, project: String = "", _ sections: [CabinetSection]) throws -> [CabinetSection] {
        var d: [String: Any] = ["layout": try JSONSerialization.jsonObject(with: try Self.encoder().encode(sections))]
        if !project.isEmpty { d["project"] = project }
        return try decodeCabinet(LossyList<CabinetSection>.self, try send("POST", orgURL(org, ["cabinet", "layout"]), try json(d))).elements
    }

    /// 新增文件(id 由伺服器編 DOC-<n>);project 空 = 未分專案
    func createDoc(org: String, project: String = "", title: String, body: String, section: String, tab: String?, tags: [String], author: String) throws -> CabinetDoc {
        var d: [String: Any] = ["title": title, "body": body, "section": section, "tags": tags, "author": author]
        if let tab { d["tab"] = tab }
        if !project.isEmpty { d["project"] = project }
        return try decodeCabinet(CabinetDoc.self, try send("POST", orgURL(org, ["docs"]), try json(d)))
    }

    /// 修改文件:只送改了的欄位(給 section 就是搬移,tab 空字串 = 區塊本身;project 改了就是搬到別的專案,空字串 = 未分專案)
    func updateDoc(org: String, id: String, from before: CabinetDoc, to after: CabinetDoc) throws -> CabinetDoc {
        var d: [String: Any] = [:]
        if after.title != before.title { d["title"] = after.title }
        if after.body != before.body { d["body"] = after.body }
        if after.section != before.section || after.tab != before.tab {
            d["section"] = after.section
            d["tab"] = after.tab ?? ""
        }
        if after.tags != before.tags { d["tags"] = after.tags }
        if (after.project ?? "") != (before.project ?? "") { d["project"] = after.project ?? "" }
        return try decodeCabinet(CabinetDoc.self, try send("POST", orgURL(org, ["docs", id]), try json(d)))
    }

    func deleteDoc(org: String, id: String) throws {
        _ = try send("POST", orgURL(org, ["docs", id, "delete"]), try json([:]))
    }

    // MARK: 專案

    /// 專案(依建立順序)
    func listProjects(org: String) throws -> [Project] {
        try decodeCabinet(LossyList<Project>.self, try send("GET", orgURL(org, ["projects"]), nil)).elements
    }

    func getProject(org: String, id: String) throws -> Project {
        try decodeCabinet(Project.self, try send("GET", orgURL(org, ["projects", id]), nil))
    }

    /// 新增專案(組織管理員;名稱必填、組織內不重複)→ PRJ-<n>
    func createProject(org: String, name: String, description: String = "") throws -> Project {
        try decodeCabinet(Project.self, try send("POST", orgURL(org, ["projects"]), try json(["name": name, "description": description])))
    }

    /// 修改專案(組織管理員):只送有給的欄位
    func updateProject(org: String, id: String, name: String? = nil, description: String? = nil) throws -> Project {
        var d: [String: Any] = [:]
        if let name { d["name"] = name }
        if let description { d["description"] = description }
        return try decodeCabinet(Project.self, try send("PUT", orgURL(org, ["projects", id]), try json(d)))
    }

    /// 刪除專案(組織管理員;專案裡還有文件時伺服器回 409,錯誤訊息原樣丟出)
    func deleteProject(org: String, id: String) throws {
        _ = try send("POST", orgURL(org, ["projects", id, "delete"]), try json([:]))
    }
}

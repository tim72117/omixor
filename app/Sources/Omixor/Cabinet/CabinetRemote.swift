import Foundation

// 遠端模式(OmixorMode.swift)時文件櫃不讀寫本機的 cabinet.json / cabinet-layout.json,改透過 omixor server(core)的
// /api/orgs/<組織>/cabinet 與 /api/orgs/<組織>/docs 讀寫(路由說明見 core/README.md)。
// 網址、token 與重新登入都和 Issue 共用 IssueRemote(Issues/IssueRemote.swift)。
//
// 權限由伺服器決定:成員可以新增、修改、搬移、刪除文件(作者一律是登入的人;伺服器管理員可以指定署名),
// 改結構要組織管理員;存檔前伺服器也會依範本檢查必填欄位。

/// GET /api/orgs/<組織>/cabinet 的回應
struct RemoteCabinet: Decodable {
    var layout: [CabinetSection]
    var docs: [CabinetDoc]
    var layoutSaved: Bool?
}

extension IssueRemote {
    private func decodeCabinet<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
        do { return try Self.decoder().decode(type, from: data) } catch {
            throw IssueRemoteError(message: "omixor 回應的文件櫃格式看不懂:\(error.localizedDescription)")
        }
    }

    /// 結構與文件(新的在前)
    func getCabinet(org: String) throws -> RemoteCabinet {
        try decodeCabinet(RemoteCabinet.self, try send("GET", orgURL(org, ["cabinet"]), nil))
    }

    /// 改結構(組織管理員;被拿掉的區塊、頁籤裡不能還有文件)
    func saveCabinetLayout(org: String, _ sections: [CabinetSection]) throws -> [CabinetSection] {
        let body = try JSONSerialization.data(withJSONObject: ["layout": try JSONSerialization.jsonObject(with: try Self.encoder().encode(sections))])
        return try decodeCabinet([CabinetSection].self, try send("POST", orgURL(org, ["cabinet", "layout"]), body))
    }

    /// 新增文件(id 由伺服器編 DOC-<n>)
    func createDoc(org: String, title: String, body: String, section: String, tab: String?, tags: [String], author: String) throws -> CabinetDoc {
        var d: [String: Any] = ["title": title, "body": body, "section": section, "tags": tags, "author": author]
        if let tab { d["tab"] = tab }
        return try decodeCabinet(CabinetDoc.self, try send("POST", orgURL(org, ["docs"]), try json(d)))
    }

    /// 修改文件:只送改了的欄位(給 section 就是搬移,tab 空字串 = 區塊本身)
    func updateDoc(org: String, id: String, from before: CabinetDoc, to after: CabinetDoc) throws -> CabinetDoc {
        var d: [String: Any] = [:]
        if after.title != before.title { d["title"] = after.title }
        if after.body != before.body { d["body"] = after.body }
        if after.section != before.section || after.tab != before.tab {
            d["section"] = after.section
            d["tab"] = after.tab ?? ""
        }
        if after.tags != before.tags { d["tags"] = after.tags }
        return try decodeCabinet(CabinetDoc.self, try send("POST", orgURL(org, ["docs", id]), try json(d)))
    }

    func deleteDoc(org: String, id: String) throws {
        _ = try send("POST", orgURL(org, ["docs", id, "delete"]), try json([:]))
    }
}

import Foundation

// 文件櫃的範本(和 Go 版 internal/core/doc_templates.go 一樣):
//   - 文件內文開頭可以有欄位(第一行 ---,欄位一行一個「key: value」,再一行 --- 結束),其中 kind 是文件類型
//   - 範本本身也是文件櫃裡的文件:kind: template、for: <套用的 kind>、required: <必填欄位,逗號分隔>,
//     內文放一個 ```html 區塊(版面,{{…}} 填欄位)與一個 ```css 區塊(樣式)
//   - 有對應範本的文件,畫面用網頁顯示(轉換器是 web/spec/docrender.js,兩版共用);沒有就照舊顯示 Markdown
//   - ops_doc_save 存檔前依範本檢查必填欄位
// 範本是資料:AI 用 ops_doc_save 就能新增或修改,不用改程式。

struct DocTemplate: Equatable {
    var docId: String
    var kind: String
    var html: String
    var css: String
    var required: [String]
}

enum DocTemplates {
    static let templateKind = "template"

    /// 拆開頭的欄位;沒有欄位時 meta 是空的、rest 是原文
    static func frontMatter(_ body: String) -> (meta: [String: String], rest: String) {
        let lines = body.replacingOccurrences(of: "\r", with: "").components(separatedBy: "\n")
        guard lines.first == "---", let end = lines.dropFirst().firstIndex(of: "---") else { return ([:], body) }
        var meta: [String: String] = [:]
        for l in lines[1..<end] {
            guard let i = l.firstIndex(of: ":") else { continue }
            let k = l[..<i].trimmingCharacters(in: .whitespaces)
            if !k.isEmpty { meta[k] = l[l.index(after: i)...].trimmingCharacters(in: .whitespaces) }
        }
        return (meta, lines[(end + 1)...].joined(separator: "\n"))
    }

    static func kind(_ body: String) -> String? {
        let k = frontMatter(body).meta["kind"] ?? ""
        return k.isEmpty ? nil : k
    }

    /// 取出 ```<lang> 區塊的內容(第一個)
    static func fence(_ body: String, _ lang: String) -> String? {
        var inside = false, out: [String] = []
        for l in body.replacingOccurrences(of: "\r", with: "").components(separatedBy: "\n") {
            if inside {
                if l.hasPrefix("```") { return out.joined(separator: "\n") }
                out.append(l)
            } else if l.trimmingCharacters(in: .whitespaces) == "```" + lang {
                inside = true
            }
        }
        return nil
    }

    /// 文件是範本時解析出來
    static func parse(_ doc: CabinetDoc) -> DocTemplate? {
        let meta = frontMatter(doc.body).meta
        guard meta["kind"] == templateKind, let kind = meta["for"], !kind.isEmpty, let html = fence(doc.body, "html") else { return nil }
        let req = (meta["required"] ?? "").split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        return DocTemplate(docId: doc.id, kind: kind, html: html, css: fence(doc.body, "css") ?? "", required: req)
    }

    /// 某種 kind 的範本:同一種有好幾份時用最近更新的
    static func template(for kind: String, in docs: [CabinetDoc]) -> DocTemplate? {
        docs.filter { frontMatter($0.body).meta["for"] == kind }
            .sorted { ($0.updatedAt, $0.id) > ($1.updatedAt, $1.id) }
            .lazy.compactMap(parse).first
    }

    /// 存檔前檢查;回傳錯誤訊息,沒問題回傳 nil
    static func validate(_ body: String, in docs: [CabinetDoc]) -> String? {
        let meta = frontMatter(body).meta
        guard let kind = meta["kind"], !kind.isEmpty else { return nil }
        if kind == templateKind {
            if (meta["for"] ?? "").isEmpty { return "範本要有 for 欄位(這份範本套用在哪種 kind)" }
            if fence(body, "html") == nil { return "範本要有一個 ```html 區塊(版面)" }
            return nil
        }
        guard let t = template(for: kind, in: docs) else { return nil }
        let missing = t.required.filter { (meta[$0] ?? "").isEmpty }
        return missing.isEmpty ? nil : "kind: \(kind) 的文件缺少欄位:\(missing.joined(separator: "、"))(見範本 \(t.docId))"
    }

    /// 有對應範本時,產生顯示用的整頁 HTML(網頁元件或 iframe 直接載入;禁止連外)
    static func html(for doc: CabinetDoc, in docs: [CabinetDoc]) -> String? {
        guard let kind = kind(doc.body), kind != templateKind, let t = template(for: kind, in: docs) else { return nil }
        return page(md: doc.body, template: t)
    }

    static func page(md: String, template t: DocTemplate) -> String {
        let data = (try? JSONSerialization.data(withJSONObject: ["md": md, "template": t.html],
                                                options: [.sortedKeys, .withoutEscapingSlashes])) ?? Data("{}".utf8)
        let json = String(decoding: data, as: UTF8.self)
            .replacingOccurrences(of: "<", with: "\\u003c").replacingOccurrences(of: ">", with: "\\u003e")
            .replacingOccurrences(of: "&", with: "\\u0026")
        let css = t.css.replacingOccurrences(of: "</", with: "<\\/")
        return """
        <!doctype html><html lang="zh-Hant"><head><meta charset="utf-8">\
        <meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:">\
        <meta name="viewport" content="width=device-width, initial-scale=1"><style>\(css)</style></head>\
        <body class="studio"><main id="root"></main><script>\(DocRenderJS.source)
        MdRender.mount(\(json))</script></body></html>
        """
    }
}

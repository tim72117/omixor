import CryptoKit
import Foundation

// LINE Messaging API:push、reply 與 webhook 簽章。HTTP 一律走 ConnHTTP(測試時可以用 URLProtocol 攔截)。

enum LineAPI {
    static let pushURL = URL(string: "https://api.line.me/v2/bot/message/push")!
    static let replyURL = URL(string: "https://api.line.me/v2/bot/message/reply")!
    /// 網路錯誤或 5xx 時重試一次前等多久(測試會調短)
    nonisolated(unsafe) static var retryDelay: UInt64 = 2_000_000_000
    static let maxText = 5000

    /// 推播給一個接收者。`retryKey` 是 X-Line-Retry-Key:重試時帶同一個,LINE 不會重複送出(重複時回 409)。
    static func push(token: String, to: String, messages: [[String: Any]], retryKey: String) async throws {
        let body: [String: Any] = ["to": to, "messages": messages]
        var attempt = 0
        while true {
            attempt += 1
            do {
                let r = try await post(pushURL, token: token, body: body, headers: ["X-Line-Retry-Key": retryKey])
                if r.ok || r.status == 409 { return }  // 409:同一個 retry key 已經送過了
                if r.status >= 500, attempt < 2 { try await Task.sleep(nanoseconds: retryDelay); continue }
                throw LineError(describe(r))
            } catch let e as LineError {
                throw e
            } catch {
                if attempt < 2 { try await Task.sleep(nanoseconds: retryDelay); continue }
                throw LineError("連不上 LINE:\(error.localizedDescription)")
            }
        }
    }

    /// 用 webhook 事件的 replyToken 回覆(不重試:replyToken 只能用一次,而且很快過期)
    static func reply(token: String, replyToken: String, messages: [[String: Any]]) async throws {
        let r = try await post(replyURL, token: token, body: ["replyToken": replyToken, "messages": messages], headers: [:])
        guard r.ok else { throw LineError(describe(r)) }
    }

    private static func post(_ url: URL, token: String, body: [String: Any], headers: [String: String]) async throws -> ConnHTTP.Response {
        // 保險:測試沒裝攔截器時絕不送出真的請求
        if NSClassFromString("XCTestCase") != nil, ConnHTTP.protocolClasses.isEmpty {
            throw LineError("測試中沒有攔截器,不送出 LINE 請求")
        }
        var h = headers
        h["Authorization"] = "Bearer \(token)"
        return try await ConnHTTP.json("POST", url, body, headers: h)
    }

    static func describe(_ r: ConnHTTP.Response) -> String {
        let msg = ConnHTTP.errorMessage(r)
        switch r.status {
        case 401: return "HTTP 401:channel access token 無效或過期(\(msg))"
        case 403: return "HTTP 403:沒有權限(\(msg))"
        case 429: return "HTTP 429:超過傳送上限或額度(\(msg))"
        default: return "HTTP \(r.status):\(msg)"
        }
    }

    // MARK: - 簽章

    /// X-Line-Signature = base64(HMAC-SHA256(channel secret, 原始 body))。用常數時間比較。
    static func verify(body: Data, signature: String?, secret: String) -> Bool {
        guard !secret.isEmpty, let signature, let given = Data(base64Encoded: signature.trimmingCharacters(in: .whitespaces)) else {
            return false
        }
        let mac = HMAC<SHA256>.authenticationCode(for: body, using: SymmetricKey(data: Data(secret.utf8)))
        return constantTimeEqual(Data(mac), given)
    }

    static func sign(body: Data, secret: String) -> String {
        Data(HMAC<SHA256>.authenticationCode(for: body, using: SymmetricKey(data: Data(secret.utf8)))).base64EncodedString()
    }

    static func constantTimeEqual(_ a: Data, _ b: Data) -> Bool {
        guard a.count == b.count else { return false }
        var diff: UInt8 = 0
        for (x, y) in zip(a, b) { diff |= x ^ y }
        return diff == 0
    }

    // MARK: - 訊息

    static func text(_ s: String) -> [String: Any] {
        ["type": "text", "text": s.count > maxText ? String(s.prefix(maxText - 1)) + "…" : s]
    }

    /// 助理的回答是 Markdown(**粗體** 等),轉成 LINE 的純文字
    static func plainText(_ markdown: String) -> String {
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        let s = (try? AttributedString(markdown: markdown, options: opts)).map { String($0.characters) } ?? markdown
        return s.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// postback data:ops=approval&id=…&d=approve|reject&n=<nonce>
    static func approvalData(id: String, approve: Bool, nonce: String) -> String {
        ConnHTTP.formEncode(["ops": "approval", "id": id, "d": approve ? "approve" : "reject", "n": nonce])
    }

    static func parseData(_ s: String) -> [String: String] {
        var out: [String: String] = [:]
        for pair in s.split(separator: "&") {
            let kv = pair.split(separator: "=", maxSplits: 1).map { String($0).removingPercentEncoding ?? String($0) }
            out[kv[0]] = kv.count > 1 ? kv[1] : ""
        }
        return out
    }

    static func nonce() -> String {
        var bytes = [UInt8](repeating: 0, count: 18)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return Data(bytes).base64EncodedString().replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }
}

/// 一則要推播的通知
struct LineNotice {
    var key: String              // 去重用,例如 alert:A-1
    var kind: String             // 告警、事件、待核准…
    var title: String
    var summary: String
    var fields: [(String, String)] = []
    var openIn: String           // 在 Ops 哪裡看,例如「監控與告警 › A-1」
    var color = "#1F6FEB"
    var approval: (id: String, nonce: String)?

    var altText: String {
        let s = "[\(kind)] \(title)"
        return s.count > 400 ? String(s.prefix(399)) + "…" : s
    }

    /// Flex Message:標題、摘要、欄位、在 Ops 開啟的位置;待核准請求加上核准 / 拒絕按鈕(postback)
    var message: [String: Any] {
        func t(_ s: String, size: String = "sm", color: String? = nil, bold: Bool = false, wrap: Bool = true) -> [String: Any] {
            var o: [String: Any] = ["type": "text", "text": s.isEmpty ? " " : String(s.prefix(1500)), "size": size, "wrap": wrap]
            if let color { o["color"] = color }
            if bold { o["weight"] = "bold" }
            return o
        }
        var body: [[String: Any]] = [t(summary)]
        for (k, v) in fields.prefix(8) {
            body.append(["type": "box", "layout": "baseline", "spacing": "sm", "contents": [
                ["type": "text", "text": k, "size": "xs", "color": "#8B949E", "flex": 2],
                ["type": "text", "text": v.isEmpty ? "—" : String(v.prefix(300)), "size": "xs", "wrap": true, "flex": 5],
            ]])
        }
        var footer: [[String: Any]] = []
        if let a = approval {
            footer.append(["type": "box", "layout": "horizontal", "spacing": "sm", "contents": [
                ["type": "button", "style": "primary", "color": "#2DA44E", "height": "sm",
                 "action": ["type": "postback", "label": "核准", "displayText": "核准「\(title.prefix(40))」",
                            "data": LineAPI.approvalData(id: a.id, approve: true, nonce: a.nonce)]],
                ["type": "button", "style": "secondary", "height": "sm",
                 "action": ["type": "postback", "label": "拒絕", "displayText": "拒絕「\(title.prefix(40))」",
                            "data": LineAPI.approvalData(id: a.id, approve: false, nonce: a.nonce)]],
            ]])
        }
        footer.append(t("在 Ops 開啟:\(openIn)", size: "xxs", color: "#8B949E"))
        let bubble: [String: Any] = [
            "type": "bubble",
            "header": ["type": "box", "layout": "vertical", "spacing": "xs", "contents": [
                t(kind, size: "xs", color: color, bold: true),
                t(title, size: "md", bold: true),
            ]],
            "body": ["type": "box", "layout": "vertical", "spacing": "sm", "contents": body],
            "footer": ["type": "box", "layout": "vertical", "spacing": "sm", "contents": footer],
        ]
        return ["type": "flex", "altText": altText, "contents": bubble]
    }
}

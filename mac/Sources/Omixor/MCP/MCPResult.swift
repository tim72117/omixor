import Foundation

/// tools/call 結果的唯一組裝處(App 自己實作的工具與閘道自己的失敗都走這裡;規則同 core/mcp/result.go):
///   - structuredContent 一律是 JSON 物件;content[0].text 是同一個物件的 JSON 字串
///   - 成功:工具回物件就原樣放;陣列、字串、數字等非物件包成 {"result": 值};nil(只回執)是 {}
///   - 失敗:isError = true,本體 {"error": {"code", "message", "status"[, "data"]}};
///     structuredContent 留完整 data,text 的 data 拿掉 current 與 JSON 超過 errDataValueMax 位元組的欄位並加 "truncated": true
///     (沒有大欄位時兩者相同);data 無法序列化就整個丟掉,只回 code、message、status
/// 轉給 core server 的工具結果已是這個形狀,原樣放行,不經過這裡;外部 MCP 伺服器的結果也原樣放行(格式不是我們的)。
enum MCPResult {
    /// 狀態碼對應的預設錯誤代碼(同 core 的 statusCodes 表):表裡沒有的 4xx 是 request_failed、其他是 internal
    static func code(forStatus status: Int) -> String {
        switch status {
        case 400, 422: "invalid_argument"
        case 401: "unauthorized"
        case 403: "forbidden"
        case 404: "not_found"
        case 409: "conflict"
        case 413: "payload_too_large"
        case 429: "rate_limited"
        case 500: "internal"
        case 400..<500: "request_failed"
        default: "internal"
        }
    }

    /// 成功結果
    static func ok(_ value: Any? = nil) -> [String: Any] {
        var body: [String: Any]
        switch value {
        case nil, is NSNull: body = [:]
        case let obj as [String: Any]: body = obj
        case let other?: body = ["result": other]
        }
        return build(body, isError: false)
    }

    /// error.data 單一欄位的 JSON 超過這個位元組數,就不放進 text(只在 structuredContent;同 core 的 errDataValueMax)
    static let errDataValueMax = 300

    /// 把 error.data 分成給 text 的小欄位(有大欄位時附 "truncated": true)與是否有大欄位:
    /// key 是 current(409 版本衝突時對方的整份最新內容)一律算大欄位;沒有大欄位時 small 與 data 相同、不加標記。
    static func splitErrData(_ data: [String: Any]) -> (small: [String: Any], large: Bool) {
        var small: [String: Any] = [:]
        var large = false
        for (k, v) in data {
            let size = try? JSONSerialization.data(withJSONObject: v, options: [.fragmentsAllowed]).count
            if k == "current" || size == nil || size! > errDataValueMax {
                large = true
            } else {
                small[k] = v
            }
        }
        if large { small["truncated"] = true }
        return (small, large)
    }

    /// 失敗結果。code 空就依 status 取預設;data 是額外欄位,空就不放;data 無法序列化就整個丟掉
    static func error(code: String? = nil, message: String, status: Int, data: [String: Any] = [:]) -> [String: Any] {
        let bare: [String: Any] = ["code": code ?? Self.code(forStatus: status), "message": message, "status": status]
        guard !data.isEmpty, JSONSerialization.isValidJSONObject(data) else { return build(["error": bare], isError: true) }
        let (small, large) = splitErrData(data)
        return build(["error": bare.merging(["data": large ? small : data]) { $1 }], structured: ["error": bare.merging(["data": data]) { $1 }], isError: true)
    }

    /// 任何 Swift 錯誤 → 失敗結果:MCPToolError 用它自己的狀態、代碼與 data;其他(沒預期到的)是 500
    static func error(_ err: Error) -> [String: Any] {
        if let e = err as? MCPToolError { return error(code: e.code, message: e.message, status: e.status, data: e.data) }
        return error(message: err.localizedDescription, status: 500)
    }

    /// text 與 structuredContent 預設是同一份;失敗結果的大欄位例外時,structured 另給完整版
    private static func build(_ body: [String: Any], structured: [String: Any]? = nil, isError: Bool) -> [String: Any] {
        ["content": [["type": "text", "text": MCPJSON.string(body)]], "structuredContent": structured ?? body, "isError": isError]
    }
}

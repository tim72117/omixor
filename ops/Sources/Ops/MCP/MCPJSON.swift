import Foundation

// MCP 用的 JSON 工具:把模型轉成 JSON 物件、取參數、輸出字串。
// 模型的 Encodable 宣告在 Core/Models.swift,這裡只補上需要自訂格式的部分。

extension Actor: Encodable {
    /// 透過 MCP 做的變更,執行者記為這個 id(畫面上顯示「MCP 用戶端」)
    static let mcpId = "mcp"
    static let mcp = Actor.ai(mcpId)

    private enum Keys: String, CodingKey { case kind, id }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: Keys.self)
        switch self {
        case .person(let id): try c.encode("person", forKey: .kind); try c.encode(id, forKey: .id)
        case .ai(let id): try c.encode("ai", forKey: .kind); try c.encode(id, forKey: .id)
        case .system: try c.encode("system", forKey: .kind)
        }
    }
}

/// 讓 Service.versions 這種以列舉為 key 的字典編成 JSON 物件,而不是 key/value 交錯的陣列
extension DeployEnv: CodingKeyRepresentable {}

enum MCPJSON {
    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return e
    }()

    /// 模型 → JSONSerialization 物件([String: Any]、[Any]…),方便篩選與裁切
    static func object<T: Encodable>(_ value: T) -> Any {
        guard let data = try? encoder.encode(value),
              let obj = try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed])
        else { return NSNull() }
        return obj
    }

    static func string(_ obj: Any, pretty: Bool = false) -> String {
        var opts: JSONSerialization.WritingOptions = [.sortedKeys, .withoutEscapingSlashes, .fragmentsAllowed]
        if pretty { opts.insert(.prettyPrinted) }
        guard let data = try? JSONSerialization.data(withJSONObject: obj, options: opts) else { return "null" }
        return String(decoding: data, as: UTF8.self)
    }

    static func data(_ obj: Any) -> Data {
        (try? JSONSerialization.data(withJSONObject: obj, options: [.withoutEscapingSlashes])) ?? Data("{}".utf8)
    }

    static func iso(_ date: Date) -> String { ISO8601DateFormatter().string(from: date) }
}

/// 只留下指定的欄位
func pick(_ obj: Any, _ keys: [String]) -> [String: Any] {
    guard let d = obj as? [String: Any] else { return [:] }
    var out: [String: Any] = [:]
    for k in keys { if let v = d[k] { out[k] = v } }
    return out
}

/// 依 "a.b.c" 路徑取值
func value(at path: String, in obj: Any) -> Any? {
    var cur: Any? = obj
    for part in path.split(separator: ".") {
        guard let d = cur as? [String: Any] else { return nil }
        cur = d[String(part)]
    }
    return cur
}

/// 比較時把數字、布林、字串都轉成字串
func scalarString(_ v: Any) -> String? {
    switch v {
    case let s as String: return s
    case let n as NSNumber:
        if CFGetTypeID(n) == CFBooleanGetTypeID() { return n.boolValue ? "true" : "false" }
        return n.stringValue
    default: return nil
    }
}

/// 欄位等於 want;欄位是陣列時,任一元素等於 want 即可
func matches(_ field: Any?, _ want: Any) -> Bool {
    guard let field, let w = scalarString(want) else { return false }
    if let arr = field as? [Any] { return arr.contains { scalarString($0) == w } }
    if let d = field as? [String: Any], let id = d["id"] { return scalarString(id) == w }
    return scalarString(field) == w
}

/// 列表模式下把大量資料裁短:時間序列只留最後 3 點,工作日誌只留最後 3 行
func compact(_ obj: Any) -> Any {
    if let d = obj as? [String: Any] {
        var out: [String: Any] = [:]
        for (k, v) in d {
            if k == "log", let lines = v as? [Any], lines.count > 3 {
                out[k] = Array(lines.suffix(3))
                out["logLines"] = lines.count
            } else if let arr = v as? [Any], arr.count > 3, isSeries(arr) {
                out[k] = Array(arr.suffix(3))
                out[k + "Count"] = arr.count
            } else {
                out[k] = compact(v)
            }
        }
        return out
    }
    if let arr = obj as? [Any] { return arr.map(compact) }
    return obj
}

private func isSeries(_ arr: [Any]) -> Bool {
    guard let first = arr.first as? [String: Any] else { return false }
    return Set(first.keys) == ["date", "value"]
}

// MARK: - 參數

struct MCPArgs {
    let raw: [String: Any]

    func string(_ k: String) -> String? {
        guard let v = raw[k] else { return nil }
        if let s = v as? String { return s.trimmingCharacters(in: .whitespacesAndNewlines) }
        return scalarString(v)
    }

    func int(_ k: String) -> Int? {
        if let n = raw[k] as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID() { return n.intValue }
        if let s = raw[k] as? String { return Int(s) }
        return nil
    }

    func bool(_ k: String) -> Bool? {
        if let b = raw[k] as? Bool { return b }
        if let s = raw[k] as? String { return ["true", "1", "yes"].contains(s.lowercased()) }
        return nil
    }

    func strings(_ k: String) -> [String]? {
        if let a = raw[k] as? [Any] { return a.compactMap(scalarString) }
        if let s = raw[k] as? String { return [s] }
        return nil
    }

    func require(_ k: String) throws -> String {
        guard let s = string(k), !s.isEmpty else { throw MCPToolError("缺少參數 \(k)") }
        return s
    }

    func requireInt(_ k: String) throws -> Int {
        guard let n = int(k) else { throw MCPToolError("缺少或無效的整數參數 \(k)") }
        return n
    }

    func requireEnum<E: RawRepresentable & CaseIterable>(_ k: String, _: E.Type) throws -> E where E.RawValue == String {
        let s = try require(k)
        guard let e = E(rawValue: s) else {
            throw MCPToolError("\(k) 必須是 \(E.allCases.map(\.rawValue).joined(separator: "、")) 之一,收到「\(s)」")
        }
        return e
    }

    func optionalEnum<E: RawRepresentable & CaseIterable>(_ k: String, _ t: E.Type) throws -> E? where E.RawValue == String {
        guard string(k) != nil else { return nil }
        return try requireEnum(k, t)
    }
}

/// 工具執行失敗:回給用戶端 isError = true 的結果,而不是 JSON-RPC 錯誤
struct MCPToolError: Error {
    let message: String
    init(_ message: String) { self.message = message }
}

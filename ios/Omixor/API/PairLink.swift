import Foundation

/// 配對資訊:Mac App 的 QR code 是 omixor://pair?server=<網址>&code=<碼>(見 app 的 PairPhoneSheet.swift)
struct PairLink: Equatable, Identifiable {
    var server: URL
    var code: String

    var id: String { server.absoluteString + "|" + code }

    /// 配對時要給使用者看的完整伺服器:scheme://host:port(沒寫 port 時補上預設的 80/443),有路徑也一起顯示
    var serverDisplay: String { Self.display(server) }

    /// 明文 http:token 會不加密地傳送
    var isInsecure: Bool { server.scheme?.lowercased() == "http" }

    static func display(_ url: URL) -> String {
        let scheme = url.scheme?.lowercased() ?? "?"
        let host = url.host ?? url.absoluteString
        let hostPart = host.contains(":") && !host.hasPrefix("[") ? "[\(host)]" : host
        let port = url.port ?? (scheme == "https" ? 443 : scheme == "http" ? 80 : nil)
        var s = "\(scheme)://\(hostPart)"
        if let port { s += ":\(port)" }
        let path = url.path
        if !path.isEmpty, path != "/" { s += path }
        return s
    }

    /// 配對碼字母表(同 core/service/pair.go:去掉易混的 0 O 1 I L)
    static let alphabet = Set("ABCDEFGHJKMNPQRSTUVWXYZ23456789")
    static let codeLength = 8

    /// 解析 omixor://pair 連結;不是配對連結回 nil
    init?(url: URL) {
        guard url.scheme?.lowercased() == "omixor", url.host?.lowercased() == "pair",
              let c = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return nil }
        let q = { (n: String) in c.queryItems?.first { $0.name == n }?.value ?? "" }
        guard let server = Self.normalizeServer(q("server")), let code = Self.normalizeCode(q("code")) else { return nil }
        self.server = server
        self.code = code
    }

    /// 掃到的文字:omixor://pair 連結
    init?(scanned text: String) {
        guard let u = URL(string: text.trimmingCharacters(in: .whitespacesAndNewlines)) else { return nil }
        self.init(url: u)
    }

    init(server: URL, code: String) { self.server = server; self.code = code }

    /// 手動輸入的伺服器網址:沒寫 http(s) 的補 https(127.0.0.1、localhost 與區網 IP 補 http);去掉結尾的 /
    static func normalizeServer(_ raw: String) -> URL? {
        var s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return nil }
        if !s.contains("://") {
            let host = s.split(separator: "/").first.map(String.init)?.split(separator: ":").first.map(String.init) ?? s
            s = (isLocalHost(host) ? "http://" : "https://") + s
        }
        while s.hasSuffix("/") { s.removeLast() }
        guard let u = URL(string: s), let scheme = u.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let h = u.host, !h.isEmpty else { return nil }
        return u
    }

    static func isLocalHost(_ host: String) -> Bool {
        let h = host.lowercased()
        if h == "localhost" || h == "127.0.0.1" || h == "::1" || h.hasSuffix(".local") { return true }
        let parts = h.split(separator: ".").compactMap { Int($0) }
        guard parts.count == 4 else { return false }
        return parts[0] == 10 || (parts[0] == 192 && parts[1] == 168) || (parts[0] == 172 && (16...31).contains(parts[1]))
    }

    /// 配對碼:不分大小寫、忽略空白與 -;長度或字元不對回 nil
    static func normalizeCode(_ raw: String) -> String? {
        let s = raw.uppercased().filter { !" -\t\n".contains($0) }
        guard s.count == codeLength, s.allSatisfy({ alphabet.contains($0) }) else { return nil }
        return s
    }
}

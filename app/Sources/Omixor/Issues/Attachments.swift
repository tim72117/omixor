import Foundation
import AppKit
import CryptoKit

// 附件(Issue 內文與留言裡的圖片),和 Go 版 internal/core/attachments.go 一樣:
//   - 分組織存放:<組織資料夾>/attachments/<id>
//   - 只收 PNG、JPEG、GIF、WebP(看檔案內容判斷,不收 SVG);上限 10 MB
//   - id = 內容的 SHA-256 前 32 碼 + 副檔名,同一張圖只存一份
//   - Markdown 裡寫成 ![檔名](/api/attachments/<id>),兩個版本共用資料夾時都看得到
// 剪貼簿的圖常是 TIFF,先轉成 PNG 再存。

enum Attachments {
    static let maxSize = 10 << 20
    static let urlPrefix = "/api/attachments/"
    private static let idPattern = try! NSRegularExpression(pattern: "^[0-9a-f]{32}\\.(png|jpg|gif|webp)$")

    struct Failure: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    /// 看內容判斷圖片類型,回傳副檔名;不是允許的圖片回傳 nil
    static func ext(of data: Data) -> String? {
        let b = [UInt8](data.prefix(12))
        if b.starts(with: [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]) { return ".png" }
        if b.starts(with: [0xFF, 0xD8, 0xFF]) { return ".jpg" }
        if b.starts(with: Array("GIF87a".utf8)) || b.starts(with: Array("GIF89a".utf8)) { return ".gif" }
        if b.count >= 12, b.starts(with: Array("RIFF".utf8)), Array(b[8..<12]) == Array("WEBP".utf8) { return ".webp" }
        return nil
    }

    static func isValidId(_ id: String) -> Bool {
        idPattern.firstMatch(in: id, range: NSRange(id.startIndex..., in: id)) != nil
    }

    /// Markdown 裡的網址 → 附件 id(不是本機附件回傳 nil)
    static func id(fromURL url: String) -> String? {
        guard url.hasPrefix(urlPrefix) else { return nil }
        let id = String(url.dropFirst(urlPrefix.count))
        return isValidId(id) ? id : nil
    }

    static func fileURL(_ id: String, in orgDir: URL) -> URL {
        orgDir.appendingPathComponent("attachments", isDirectory: true).appendingPathComponent(id)
    }

    /// 存到組織資料夾,回傳 id
    static func save(_ data: Data, in orgDir: URL) throws -> String {
        guard !data.isEmpty else { throw Failure(message: "檔案是空的") }
        guard data.count <= maxSize else { throw Failure(message: "圖片太大,上限 10 MB") }
        guard let ext = ext(of: data) else { throw Failure(message: "只能放 PNG、JPEG、GIF、WebP 圖片") }
        let hex = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        let id = String(hex.prefix(32)) + ext
        let url = fileURL(id, in: orgDir)
        if !FileManager.default.fileExists(atPath: url.path) {
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try data.write(to: url, options: .atomic)
        }
        return id
    }

    /// 讀附件;id 格式不對、檔案不在或內容不是允許的圖片都回傳 nil
    static func read(_ id: String, in orgDir: URL) -> Data? {
        guard isValidId(id), let data = try? Data(contentsOf: fileURL(id, in: orgDir)), ext(of: data) != nil else { return nil }
        return data
    }

    /// 其他格式(剪貼簿常見的 TIFF、HEIC…)轉成 PNG;本來就允許的格式原樣回傳
    static func normalized(_ data: Data) -> Data? {
        if ext(of: data) != nil { return data }
        guard let rep = NSBitmapImageRep(data: data) else { return nil }
        return rep.representation(using: .png, properties: [:])
    }

    /// 插入用的 Markdown
    static func markdown(name: String, id: String) -> String {
        let clean = name.replacingOccurrences(of: "[", with: " ").replacingOccurrences(of: "]", with: " ")
            .replacingOccurrences(of: "\n", with: " ")
        return "![\(clean.isEmpty ? "圖片" : clean)](\(urlPrefix)\(id))"
    }
}

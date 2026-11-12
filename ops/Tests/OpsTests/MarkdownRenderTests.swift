import XCTest
import SwiftUI
@testable import Ops

/// 把含圖片的 Markdown 算繪成圖,確認附件真的畫得出來(OPS_RENDER_OUT 有設時存檔給人看)
@MainActor
final class MarkdownRenderTests: XCTestCase {
    func testImageAttachmentRenders() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-md-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dir.path, 1)
        defer { unsetenv("OPS_DATA_DIR"); try? FileManager.default.removeItem(at: dir) }
        let app = AppState()
        let img = NSImage(size: NSSize(width: 240, height: 120))
        img.lockFocus(); NSColor.systemTeal.setFill(); NSRect(x: 0, y: 0, width: 240, height: 120).fill(); img.unlockFocus()
        let png = try XCTUnwrap(Attachments.normalized(XCTUnwrap(img.tiffRepresentation)))
        let id = try Attachments.save(png, in: app.dataDir)
        let md = "截圖如下:\n\n\(Attachments.markdown(name: "測試圖", id: id))\n\n上面是本機附件。\n\n- 清單裡的 ![小圖](/api/attachments/\(id)) 只顯示文字\n\n![不在的](/api/attachments/\(String(repeating: "0", count: 32)).png)"
        let view = MarkdownText(md).environmentObject(app).padding().frame(width: 420, alignment: .leading).background(Color.white)
        let r = ImageRenderer(content: view)
        r.scale = 2
        let out = try XCTUnwrap(r.nsImage)
        // 高度要包含 120pt 的圖片
        XCTAssertGreaterThan(out.size.height, 180)
        if let path = ProcessInfo.processInfo.environment["OPS_RENDER_OUT"],
           let tiff = out.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff) {
            try rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
        }
    }
}

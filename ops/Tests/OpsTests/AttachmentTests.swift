import XCTest
import AppKit
@testable import Ops

/// 附件:同 Go 版 TestAttachments
@MainActor
final class AttachmentTests: XCTestCase {
    var dataDir: URL!
    override func setUp() async throws {
        dataDir = FileManager.default.temporaryDirectory.appendingPathComponent("ops-att-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"
        TokenVault.scope = ""
    }
    override func tearDown() async throws {
        unsetenv("OPS_DATA_DIR")
        TokenVault.scope = ""
        try? FileManager.default.removeItem(at: dataDir)
    }

    func testSaveReadAndReject() throws {
        let app = AppState()
        let png = Data([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A] + Array(repeating: 1, count: 32))
        let id = try Attachments.save(png, in: app.dataDir)
        XCTAssertTrue(Attachments.isValidId(id))
        XCTAssertTrue(id.hasSuffix(".png"))
        XCTAssertEqual(try Attachments.save(png, in: app.dataDir), id, "同一張圖要同一個 id")
        XCTAssertEqual(Attachments.read(id, in: app.dataDir), png)
        XCTAssertTrue(FileManager.default.fileExists(atPath: dataDir.appendingPathComponent("orgs/default/attachments/\(id)").path))

        let bad: [String: Data] = [
            "svg": Data(#"<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>"#.utf8),
            "html": Data("<html><script>x</script></html>".utf8),
            "空的": Data(),
            "太大": Data([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]) + Data(count: Attachments.maxSize),
        ]
        for (name, d) in bad { XCTAssertThrowsError(try Attachments.save(d, in: app.dataDir), name) }
        for b in ["../orgs.json", "x.png", String(repeating: "a", count: 32) + ".svg"] {
            XCTAssertNil(Attachments.read(b, in: app.dataDir), b)
        }
        XCTAssertNil(Attachments.id(fromURL: "/api/attachments/../../orgs.json"))
        XCTAssertEqual(Attachments.id(fromURL: "/api/attachments/\(id)"), id)

        // 分組織:別的組織讀不到
        let org = app.createOrg(name: "另一個", template: .blank)
        app.switchOrg(org.id)
        XCTAssertNil(Attachments.read(id, in: app.dataDir))
    }

    /// 剪貼簿的 TIFF 轉成 PNG 存
    func testTIFFIsConvertedToPNG() throws {
        let img = NSImage(size: NSSize(width: 4, height: 4))
        img.lockFocus(); NSColor.red.setFill(); NSRect(x: 0, y: 0, width: 4, height: 4).fill(); img.unlockFocus()
        let tiff = try XCTUnwrap(img.tiffRepresentation)
        XCTAssertNil(Attachments.ext(of: tiff))
        let png = try XCTUnwrap(Attachments.normalized(tiff))
        XCTAssertEqual(Attachments.ext(of: png), ".png")
        XCTAssertNil(Attachments.normalized(Data("not an image".utf8)))
    }

    func testMarkdownText() {
        XCTAssertEqual(Attachments.markdown(name: "a[1]\nb.png", id: "x"), "![a 1  b.png](/api/attachments/x)")
        XCTAssertEqual(Attachments.markdown(name: "", id: "x"), "![圖片](/api/attachments/x)")
    }
}

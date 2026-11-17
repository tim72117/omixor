import XCTest
import SwiftUI
@testable import Ops

/// 文件櫃的閱讀版 Markdown:解析各種區塊;OPS_RENDER_OUT 有設時把範例輸出成圖片給人看
@MainActor
final class ReadingMarkdownTests: XCTestCase {
    static let sample = """
    # 科技新知動態牆

    > 來源:shuttle 研發基地 › 構想。2026-11-04 提出,尚未評估。

    ## 定位

    - 研發階段:構想,尚未評估工時。
    - 一句話:類似社群網站的動態牆,一則一則放科技新知;每則可以用**舞台系統**演一段短短的即時畫面。
      - 第二層:縮排的項目
    1. 第一步
    2. 第二步

    ## 待辦

    - [ ] 決定更新頻率
    - [x] 寫構想

    | 方案 | 人數 |
    |---|---|
    | 免費 | 820 |
    | 訂閱 | 64 |

    ---

    ```json
    {"stage": "browser"}
    ```
    """

    func testParse() {
        let b = ReadingMarkdown.parse(Self.sample)
        XCTAssertEqual(b.first, .heading(1, "科技新知動態牆"))
        XCTAssertTrue(b.contains(.quote(["來源:shuttle 研發基地 › 構想。2026-11-04 提出,尚未評估。"])))
        XCTAssertTrue(b.contains(.item(level: 1, marker: .bullet, text: "第二層:縮排的項目")), "縮排兩格是第二層")
        XCTAssertTrue(b.contains(.item(level: 0, marker: .number("2"), text: "第二步")))
        XCTAssertTrue(b.contains(.item(level: 0, marker: .task(false), text: "決定更新頻率")))
        XCTAssertTrue(b.contains(.item(level: 0, marker: .task(true), text: "寫構想")))
        XCTAssertTrue(b.contains(.table(header: ["方案", "人數"], rows: [["免費", "820"], ["訂閱", "64"]])))
        XCTAssertTrue(b.contains(.rule))
        XCTAssertTrue(b.contains(.code(lang: "json", text: "{\"stage\": \"browser\"}")))
        XCTAssertEqual(ReadingMarkdown.parse("| 只有一列 |"), [.paragraph("只有一列")], "不是表格就當段落")
    }

    func testRender() throws {
        guard let out = ProcessInfo.processInfo.environment["OPS_RENDER_OUT"] else { return }
        let src = (try? String(contentsOfFile: out + "/doc.md", encoding: .utf8)) ?? Self.sample
        for (name, scheme) in [("light", ColorScheme.light), ("dark", ColorScheme.dark)] {
            let view = ReadingMarkdown(src).padding(28).frame(width: 760, alignment: .leading)
                .background(scheme == .dark ? Color(white: 0.12) : Color.white).environment(\.colorScheme, scheme)
            let r = ImageRenderer(content: view)
            r.scale = 2
            let img = try XCTUnwrap(r.nsImage)
            let png = try XCTUnwrap(NSBitmapImageRep(data: try XCTUnwrap(img.tiffRepresentation))?.representation(using: .png, properties: [:]))
            try png.write(to: URL(fileURLWithPath: out).appendingPathComponent("reading-\(name).png"))
        }
    }
}

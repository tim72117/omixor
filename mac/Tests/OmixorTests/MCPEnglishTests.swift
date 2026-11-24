import XCTest
@testable import Omixor

/// 規則:LLM 透過 MCP 收到的文字一律英文(給人看的畫面文字不受限)。
/// 這裡擋 App 自己實作的工具規格(名稱、標題、說明、參數說明、enum)、server instructions 與固定的錯誤文字不含 CJK。
/// 新增工具時文字寫成中文會在這裡失敗。
@MainActor
final class MCPEnglishTests: XCTestCase {
    /// 漢字、日韓假名諧音區、CJK 標點、全形與半形形式、注音
    private static func hasCJK(_ s: String) -> Bool {
        s.unicodeScalars.contains {
            switch $0.value {
            case 0x2E80...0x2FDF, 0x3000...0x30FF, 0x3100...0x312F, 0x3400...0x4DBF, 0x4E00...0x9FFF, 0xAC00...0xD7AF, 0xF900...0xFAFF, 0xFF00...0xFFEF: true
            default: false
            }
        }
    }

    /// 遞迴收集所有字串值(含 key),回傳含 CJK 的路徑
    private static func offenders(_ v: Any, path: String) -> [String] {
        switch v {
        case let s as String: hasCJK(s) ? ["\(path) = \(s)"] : []
        case let d as [String: Any]: d.flatMap { (k, v) in (hasCJK(k) ? ["\(path) key \(k)"] : []) + offenders(v, path: "\(path).\(k)") }
        case let a as [Any]: a.enumerated().flatMap { offenders($1, path: "\(path)[\($0)]") }
        default: []
        }
    }

    func testToolSpecsHaveNoCJK() {
        let bad = MCPHandler.tools.flatMap { Self.offenders($0.definition, path: $0.name) }
        XCTAssertEqual(bad, [], "工具規格含中日韓文字(LLM 看到的文字要英文)")
    }

    func testInstructionsAndFixedErrorsHaveNoCJK() {
        XCTAssertFalse(Self.hasCJK(MCPHandler.instructions), "instructions 含中日韓文字")
        XCTAssertFalse(Self.hasCJK(MCPHandler.agentTaskCreateAsAgentError))
    }

    func testDetectorCatchesCJK() {
        for s in ["整體", "標題:x", "x、y", "ａｂ", "「a」"] { XCTAssertTrue(Self.hasCJK(s), s) }
        XCTAssertFalse(Self.hasCJK("Overview - e.g. {\"a\":1} › … "))
    }
}

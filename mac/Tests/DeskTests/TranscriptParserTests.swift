import XCTest
@testable import Desk

final class TranscriptParserTests: XCTestCase {
    private func data(_ lines: [String]) -> Data { Data(lines.joined(separator: "\n").utf8) }

    func testHeaderPrefersCustomTitle() {
        let d = data([
            #"{"type":"user","sessionId":"s1","cwd":"/tmp/p","entrypoint":"cli","message":{"role":"user","content":"第一句話"}}"#,
            #"{"type":"ai-title","aiTitle":"AI 標題","sessionId":"s1"}"#,
            #"{"type":"custom-title","customTitle":"自訂標題","sessionId":"s1"}"#,
        ])
        let h = TranscriptParser.header(of: d)
        XCTAssertEqual(h.sessionId, "s1")
        XCTAssertEqual(h.cwd, "/tmp/p")
        XCTAssertEqual(h.firstPrompt, "第一句話")
        XCTAssertEqual(h.customTitle, "自訂標題")
        XCTAssertEqual(h.aiTitle, "AI 標題")
    }

    func testItemsSkipToolResultsAndInjectedMessages() {
        let d = data([
            #"{"type":"user","uuid":"u1","message":{"role":"user","content":"<command-name>/clear</command-name>"}}"#,
            #"{"type":"user","uuid":"u2","message":{"role":"user","content":[{"type":"text","text":"幫我看 git"}]}}"#,
            #"{"type":"assistant","uuid":"a1","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git status","description":"Show status"}}]}}"#,
            #"{"type":"user","uuid":"u3","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}"#,
            #"{"type":"assistant","uuid":"a2","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"git log"}}]}}"#,
            #"{"type":"assistant","uuid":"a3","message":{"content":[{"type":"text","text":"工作目錄是乾淨的。"}]}}"#,
            #"{"type":"assistant","uuid":"a4","isSidechain":true,"message":{"content":[{"type":"text","text":"子代理"}]}}"#,
        ])
        let items = TranscriptParser.items(from: d).items
        XCTAssertEqual(items.count, 3)
        guard case .user(_, let q) = items[0] else { return XCTFail("first should be user") }
        XCTAssertEqual(q, "幫我看 git")
        guard case .tools(_, let calls) = items[1] else { return XCTFail("second should be tools") }
        XCTAssertEqual(calls.map(\.summary), ["Show status", "git log"])
        guard case .assistant(_, let a) = items[2] else { return XCTFail("third should be assistant") }
        XCTAssertEqual(a, "工作目錄是乾淨的。")
    }

    func testContextTokensUsesLastMainAssistantUsage() {
        let d = data([
            #"{"type":"assistant","message":{"content":[],"usage":{"input_tokens":5,"cache_read_input_tokens":100}}}"#,
            #"{"type":"assistant","message":{"content":[],"usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":300}}}"#,
            #"{"type":"assistant","isSidechain":true,"message":{"content":[],"usage":{"input_tokens":99999}}}"#,
        ])
        XCTAssertEqual(TranscriptParser.contextTokens(from: d), 330)
    }

    func testItemsFromTailStartAtLineBoundary() {
        let d = data([
            #"{"type":"user","uuid":"u1","message":{"role":"user","content":"很早的訊息,會被略過"}}"#,
            #"{"type":"user","uuid":"u2","message":{"role":"user","content":"最近"}}"#,
        ])
        let loaded = TranscriptParser.items(from: d, limit: 90)
        XCTAssertTrue(loaded.truncated)
        XCTAssertEqual(loaded.items.count, 1)
        guard loaded.items.count == 1, case .user(_, let text) = loaded.items[0] else { return XCTFail("should be user") }
        XCTAssertEqual(text, "最近")
    }

    func testMarkdownBlocksSplitCodeFences() {
        let blocks = MarkdownText.blocks("說明\n```bash\nls -la\n```\n結尾")
        XCTAssertEqual(blocks.count, 3)
        guard case .code(let code) = blocks[1] else { return XCTFail("middle should be code") }
        XCTAssertEqual(code, "ls -la")
    }
}

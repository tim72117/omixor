import XCTest
@testable import Desk

/// 讀本機真實的 ~/.claude/projects,量載入時間。只在設定 DESK_REAL_DATA=1 時執行。
final class RealDataTests: XCTestCase {
    func testLoadProjectsFromThisMachine() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["DESK_REAL_DATA"] == "1")
        let start = Date()
        let projects = SessionStore.loadProjects()
        let elapsed = Date().timeIntervalSince(start)
        let sessions = projects.reduce(0) { $0 + $1.sessions.count }
        print("projects=\(projects.count) sessions=\(sessions) elapsed=\(String(format: "%.2f", elapsed))s")
        for p in projects.prefix(5) {
            print("  \(p.name): \(p.sessions.prefix(3).map(\.title))")
        }
        XCTAssertFalse(projects.isEmpty)
    }
}

extension RealDataTests {
    /// 開啟本機最大的對話紀錄,量顯示需要的時間。
    func testOpenLargestTranscript() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["DESK_REAL_DATA"] == "1")
        let files = SessionStore.loadProjects().flatMap(\.sessions).map(\.file)
        let size: (URL) -> Int = { (try? $0.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0 }
        let file = try XCTUnwrap(files.max { size($0) < size($1) })
        let start = Date()
        let data = try Data(contentsOf: file, options: .mappedIfSafe)
        let loaded = TranscriptParser.items(from: data)
        let tokens = TranscriptParser.contextTokens(from: data)
        print("largest=\(size(file) / 1_048_576)MB items=\(loaded.items.count) truncated=\(loaded.truncated) tokens=\(tokens ?? -1) elapsed=\(String(format: "%.2f", Date().timeIntervalSince(start)))s")
    }
}

extension RealDataTests {
    /// 實際呼叫 claude CLI:開新對話,再用 --resume 接續,確認串流事件能組成對話。
    /// 會在 DESK_CLI_DIR 指定的資料夾產生兩輪對話紀錄,只在設定 DESK_REAL_CLI=1 時執行。
    func testRunnerNewThenResume() throws {
        let env = ProcessInfo.processInfo.environment
        try XCTSkipUnless(env["DESK_REAL_CLI"] == "1")
        let cwd = try XCTUnwrap(env["DESK_CLI_DIR"])

        func turn(_ prompt: String, resume: String?) -> (String?, [TranscriptItem], Bool) {
            let done = expectation(description: prompt)
            var sessionId: String?
            var items: [TranscriptItem] = []
            var ok = false
            let runner = ClaudeRunner()
            runner.send(prompt, options: .init(cwd: cwd, resume: resume, model: .haiku, permission: .default)) { event in
                switch event {
                case .started(let id): sessionId = id
                case .record(let obj):
                    if obj["type"] as? String == "assistant" { TranscriptParser.append(record: obj, to: &items) }
                case .finished(let isError, _, _): ok = !isError; done.fulfill()
                case .failed(let msg): print("failed: \(msg)"); done.fulfill()
                }
            }
            wait(for: [done], timeout: 120)
            return (sessionId, items, ok)
        }

        let (id, first, ok1) = turn("請記住暗號 42,只回覆「好」", resume: nil)
        XCTAssertTrue(ok1)
        let sessionId = try XCTUnwrap(id)
        print("first:", first)
        let (id2, second, ok2) = turn("剛剛的暗號是多少?只回覆數字", resume: sessionId)
        XCTAssertTrue(ok2)
        XCTAssertEqual(id2, sessionId)
        print("second:", second)
        guard case .assistant(_, let text)? = second.last else { return XCTFail("no reply") }
        XCTAssertTrue(text.contains("42"))
    }
}

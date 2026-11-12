import XCTest
@testable import Ops

/// 用假的 shuttle 專案(scripts/voice.mjs 模擬 --batch / --collect 的輸出與檔案)跑完整流程,
/// 不呼叫 Gemini、不上傳 GCS、不碰真正的 shuttle 與 Ops 資料。
@MainActor
final class VoicePipelineTests: XCTestCase {
    private var root: URL!
    private var dataDir: URL!
    /// VoicePipeline 對 AppState 是 unowned(app 本來就擁有它),測試要自己留住 app
    private var apps: [AppState] = []

    override func setUp() async throws {
        TokenVault.service = "com.tim72117.ops.tests.\(UUID().uuidString)"  // 測試一律不碰正式鑰匙圈
        let base = FileManager.default.temporaryDirectory.appendingPathComponent("ops-voice-\(UUID().uuidString)")
        root = base.appendingPathComponent("shuttle")
        dataDir = base.appendingPathComponent("data")
        try FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)
        try makeFakeShuttle()
        setenv("OPS_SHUTTLE_PATH", root.path, 1)
        setenv("OPS_DATA_DIR", dataDir.path, 1)
    }

    override func tearDown() async throws {
        unsetenv("OPS_SHUTTLE_PATH")
        unsetenv("OPS_DATA_DIR")
        try? FileManager.default.removeItem(at: root.deletingLastPathComponent())
    }

    private func write(_ rel: String, _ text: String) throws {
        let url = root.appendingPathComponent(rel)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try text.write(to: url, atomically: true, encoding: .utf8)
    }

    private func makeFakeShuttle() throws {
        try write("web/package.json", #"{"name":"fake","private":true,"scripts":{"voice":"node scripts/voice.mjs"}}"#)
        try write("web/src/content/live/launch/1.1.json", #"{"steps":[]}"#)
        try write("web/src/content/live/launch/1.2.json", #"{"steps":[]}"#)
        try write("web/src/content/live/launch/empty.json", #"{"steps":[]}"#)
        try write("web/src/content/live/launch/1.1.voice.json",
                  #"{"clips":{"舊的一句":{"file":"launch/old.mp3","ms":1200}}}"#)
        try write("server/internal/store/courses_seed.json",
                  #"[{"id":"launch","units":[{"id":"u1","lessons":[{"id":"1.1","script":"launch/1.1.json"}]}]}]"#)
        try write("web/scripts/voice.mjs", #"""
        import fs from 'node:fs'
        const args = process.argv.slice(2)
        const has = (f) => args.includes(`--${f}`)
        const BATCH = 'recordings/voice-batch.json', COUNT = 'recordings/count'
        const LIVE = 'src/content/live/'
        const die = (m) => { console.error(`錯誤:${m}`); process.exit(1) }
        fs.mkdirSync('recordings', { recursive: true })
        const targets = args.filter((a, i) => !a.startsWith('--') && !(i > 0 && args[i - 1] === '--limit'))
        if (has('dry-run')) { console.log('  launch/aaa.mp3  launch/1.1.json  新的一句'); console.log('共 2 段、10 字'); process.exit(0) }
        if (has('batch')) {
          if (fs.existsSync(BATCH)) die('已經有一批還沒收')
          if (targets.includes('launch/empty.json')) { console.log('沒有缺的旁白,不用送出'); process.exit(0) }
          fs.writeFileSync(BATCH, JSON.stringify({ name: 'batches/fake1', targets, items: [{ key: 'launch/aaa.mp3' }, { key: 'launch/bbb.mp3' }] }))
          console.log(`送出 2 段旁白(${targets.length} 份腳本),批次 batches/fake1。之後跑 npm run voice -- --collect 收結果`)
          process.exit(0)
        }
        if (has('collect')) {
          if (!fs.existsSync(BATCH)) die('沒有送出中的批次(recordings/voice-batch.json)')
          const n = fs.existsSync(COUNT) ? Number(fs.readFileSync(COUNT, 'utf8')) : 0
          fs.writeFileSync(COUNT, String(n + 1))
          if (n === 0) { console.log('批次 batches/fake1 還在處理(JOB_STATE_RUNNING),送出於 now。晚點再跑一次 --collect'); process.exit(0) }
          fs.writeFileSync(LIVE + 'launch/1.1.voice.json', JSON.stringify({ clips: {
            '舊的一句': { file: 'launch/old.mp3', ms: 1200 },
            '新的一句': { file: 'launch/aaa.mp3', ms: 2500 } } }, null, 2))
          fs.writeFileSync(LIVE + 'launch/1.2.voice.json', JSON.stringify({ clips: { '另一句': { file: 'launch/bbb.mp3', ms: 3000 } } }))
          fs.rmSync(BATCH)
          console.log('收到 2 段,1 段失敗(之後重送一次就會補上):\n  launch/ccc.mp3:{"code":500}')
          process.exit(0)
        }
        die('不認得的參數')
        """#)
    }

    private func makeApp() -> (AppState, VoicePipeline) {
        let app = AppState()
        apps.append(app)
        app.startVoice(timer: false)
        return (app, app.voice!)
    }

    private func dueNow(_ app: AppState) {
        for i in app.voiceJobs.indices { app.voiceJobs[i].nextCheckAt = .distantPast }
    }

    func testFullFlowSubmitPollCollect() async throws {
        let (app, voice) = makeApp()
        let job = try voice.submit(scripts: ["launch/1.1.json"], all: false, limit: nil, by: .mcp)
        XCTAssertEqual(job.state, .queued)
        XCTAssertEqual(job.by, Actor.mcpId)

        await voice.tick()  // 送出
        var j = try XCTUnwrap(voice.job(job.id))
        XCTAssertEqual(j.state, .processing, j.log.map(\.text).joined(separator: "\n"))
        XCTAssertEqual(j.batchName, "batches/fake1")
        XCTAssertEqual(j.clipCount, 2)
        XCTAssertEqual(j.baseline["launch/1.1.voice.json"]?.files, ["launch/old.mp3"])

        await voice.tick()  // 還沒到下次檢查時間:不動
        XCTAssertEqual(voice.job(job.id)?.checks, 0)

        dueNow(app)
        await voice.tick()  // 第一次查:還在處理
        j = try XCTUnwrap(voice.job(job.id))
        XCTAssertEqual(j.state, .processing)
        XCTAssertEqual(j.batchState, "JOB_STATE_RUNNING")
        XCTAssertGreaterThan(j.nextCheckAt ?? .distantPast, .now)

        dueNow(app)
        await voice.tick()  // 第二次查:收完
        j = try XCTUnwrap(voice.job(job.id))
        XCTAssertEqual(j.state, .ready, j.log.map(\.text).joined(separator: "\n"))
        XCTAssertEqual(j.changes.count, 2)
        let c11 = try XCTUnwrap(j.changes.first { $0.path == "launch/1.1.voice.json" })
        XCTAssertFalse(c11.isNew)
        XCTAssertEqual(c11.added.map(\.file), ["launch/aaa.mp3"])
        XCTAssertEqual(c11.clips, 2)
        XCTAssertEqual(c11.course, "launch")
        XCTAssertEqual(c11.lesson, "1.1")
        XCTAssertEqual(c11.added.first?.url?.absoluteString, "https://storage.googleapis.com/shuttle-voice/launch/aaa.mp3")
        let c12 = try XCTUnwrap(j.changes.first { $0.path == "launch/1.2.voice.json" })
        XCTAssertTrue(c12.isNew)
        XCTAssertNil(c12.course)
        XCTAssertEqual(j.failedClips.count, 1)
        XCTAssertEqual(app.badge(for: .pipelines).map { $0 >= 1 }, true)

        // 存檔:重開 app 讀得回來
        let (app2, voice2) = makeApp()
        XCTAssertEqual(app2.voiceJobs.first?.id, job.id)
        XCTAssertEqual(voice2.job(job.id)?.state, .ready)
    }

    func testQueueOneBatchAtATime() async throws {
        let (app, voice) = makeApp()
        let a = try voice.submit(scripts: ["launch/1.1.json"], all: false, limit: nil, by: .person(app.me))
        let b = try voice.submit(scripts: ["launch/1.2.json"], all: false, limit: 3, by: .person(app.me))
        await voice.tick()
        XCTAssertEqual(voice.job(a.id)?.state, .processing)
        XCTAssertEqual(voice.job(b.id)?.state, .queued)
        dueNow(app); await voice.tick()   // a 還在處理
        dueNow(app); await voice.tick()   // a 收完,接著送 b
        XCTAssertEqual(voice.job(a.id)?.state, .ready)
        XCTAssertEqual(voice.job(b.id)?.state, .processing)
    }

    func testNothingToDoAndValidation() async throws {
        let (_, voice) = makeApp()
        let j = try voice.submit(scripts: ["launch/empty"], all: false, limit: nil, by: .mcp)  // 自動補 .json
        await voice.tick()
        XCTAssertEqual(voice.job(j.id)?.state, .nothingToDo)
        XCTAssertThrowsError(try voice.submit(scripts: ["launch/nope.json"], all: false, limit: nil, by: .mcp))
        XCTAssertThrowsError(try voice.submit(scripts: ["launch/1.1.voice.json"], all: false, limit: nil, by: .mcp))
        XCTAssertThrowsError(try voice.submit(scripts: [], all: false, limit: nil, by: .mcp))
        let out = try await voice.dryRun(scripts: ["web/src/content/live/launch/1.1.json"], all: false, limit: nil)
        XCTAssertTrue(out.contains("共 2 段"))
    }

    func testFailedClipParsing() {
        let ok = "launch/2.1.json [█░░] 1/40\nlaunch/2.1.json:41 段旁白(新產生 1),共 15 秒\n收到 1 段"
        XCTAssertEqual(VoicePipeline.failedClips(in: ok), [])
        let bad = "進度…\n收到 2 段,1 段失敗(之後重送一次就會補上):\n  launch/ccc.mp3:{\"code\":500}"
        XCTAssertEqual(VoicePipeline.failedClips(in: bad), ["launch/ccc.mp3:{\"code\":500}"])
    }

    func testAdoptsBatchSubmittedOutsideOps() async throws {
        try write("web/recordings/voice-batch.json", #"{"name":"batches/term","targets":["launch/1.1.json"],"items":[{"key":"x"}]}"#)
        let (_, voice) = makeApp()
        await voice.tick()
        let j = try XCTUnwrap(voice.app.voiceJobs.first)
        XCTAssertEqual(j.by, "external")
        XCTAssertEqual(j.batchName, "batches/term")
        XCTAssertEqual(j.scripts, ["launch/1.1.json"])
    }

    func testMCPVoiceTools() async throws {
        let (app, _) = makeApp()
        let mcp = MCPHandler(app: app)
        func call(_ name: String, _ args: [String: Any]) async throws -> [String: Any] {
            let msg: [String: Any] = ["jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": ["name": name, "arguments": args]]
            let raw = await mcp.handle(try JSONSerialization.data(withJSONObject: msg))
        let out = try XCTUnwrap(raw)
            let r = try XCTUnwrap((try JSONSerialization.jsonObject(with: out) as? [String: Any])?["result"] as? [String: Any])
            XCTAssertEqual(r["isError"] as? Bool, false, "\(r)")
            let text = try XCTUnwrap((r["content"] as? [[String: Any]])?.first?["text"] as? String)
            return try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any])
        }
        let dry = try await call("ops_voice_submit", ["scripts": ["launch/1.1.json"], "dry_run": true])
        XCTAssertTrue((dry["dryRun"] as? String ?? "").contains("共 2 段"))
        XCTAssertTrue(app.voiceJobs.isEmpty)

        let sub = try await call("ops_voice_submit", ["scripts": ["launch/1.1.json"]])
        let id = try XCTUnwrap((sub["job"] as? [String: Any])?["id"] as? String)
        let one = try await call("ops_voice_jobs", ["id": id])
        XCTAssertEqual(one["state"] as? String, "queued")
        XCTAssertNil(one["baseline"])
        let list = try await call("ops_voice_jobs", [:])
        XCTAssertEqual((list["jobs"] as? [Any])?.count, 1)
        // 沒有發佈用的 MCP 工具
        XCTAssertFalse(MCPHandler.tools.contains { $0.name.contains("publish") })
    }
}

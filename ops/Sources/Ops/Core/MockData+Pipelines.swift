import Foundation

// CI/CD 流程與部署
extension MockDataBuilder {
    private enum Stack { case go, web, terraform }

    private func jobNames(stage: String, stack: Stack) -> [String] {
        switch (stage, stack) {
        case ("Checkout", _): return ["checkout"]
        case ("Build", .go): return ["go build", "docker build & push"]
        case ("Build", .web): return ["pnpm build", "docker build & push"]
        case ("Build", .terraform): return ["terraform init", "terraform validate"]
        case ("Test", .go): return ["go test ./...", "integration (testcontainers)"]
        case ("Test", .web): return ["vitest", "playwright e2e"]
        case ("Test", .terraform): return ["tflint", "terraform plan"]
        case ("Security scan", .go): return ["trivy image", "govulncheck"]
        case ("Security scan", .web): return ["trivy image", "pnpm audit"]
        case ("Security scan", .terraform): return ["checkov"]
        case ("Deploy staging", .terraform): return ["terraform apply (staging)"]
        case ("Deploy staging", _): return ["kubectl rollout (staging)", "smoke test"]
        case ("Deploy prod", .terraform): return ["terraform apply (prod)"]
        case ("Deploy prod", _): return ["人工核准", "canary rollout (prod)", "health gate"]
        default: return [stage.lowercased()]
        }
    }

    private func jobLog(job: String, repo: String, sha: String) -> [String] {
        switch job {
        case "checkout":
            return ["$ git fetch --depth=50 origin", "HEAD is now at \(sha.prefix(7))", "Checked out coursely/\(repo)"]
        case "go build":
            return ["$ go build -trimpath -ldflags \"-s -w\" ./cmd/...", "go: downloading modules (cache hit 97%)", "build OK in 41s"]
        case "pnpm build":
            return ["$ pnpm install --frozen-lockfile", "Packages: +1,284 (cached)", "$ pnpm build", "vite v5.4.10 building for production...",
                    "✓ 1,912 modules transformed", "dist/assets/index-\(sha.prefix(6)).js  412.8 kB │ gzip: 131.2 kB"]
        case "docker build & push":
            return ["$ docker buildx build --push -t asia-docker.pkg.dev/coursely/\(repo):\(sha.prefix(7)) .",
                    "#12 exporting layers 3.1s done", "pushed sha256:\(sha)…"]
        case "go test ./...":
            return ["$ go test -race -count=1 ./...", "ok   coursely/\(repo)/internal/api      4.112s",
                    "ok   coursely/\(repo)/internal/store    6.870s", "coverage: 79.1% of statements"]
        case "integration (testcontainers)":
            return ["starting postgres:16.4 container", "starting redis:7.2.5 container", "PASS: 148 tests, 0 failed"]
        case "vitest":
            return ["$ pnpm vitest run --coverage", "Test Files  212 passed (212)", "Tests  1,406 passed (1,406)"]
        case "playwright e2e":
            return ["$ pnpm playwright test --project=chromium --project=webkit", "Running 86 tests using 4 workers", "86 passed (2.4m)"]
        case "trivy image":
            return ["$ trivy image --severity HIGH,CRITICAL \(repo):\(sha.prefix(7))", "Total: 0 (HIGH: 0, CRITICAL: 0)"]
        case "govulncheck":
            return ["$ govulncheck ./...", "No vulnerabilities found."]
        case "pnpm audit":
            return ["$ pnpm audit --audit-level=high", "1 moderate vulnerability (ignored: dev dependency)"]
        case "kubectl rollout (staging)":
            return ["$ kubectl -n staging set image deploy/\(repo) \(repo)=\(repo):\(sha.prefix(7))",
                    "deployment.apps/\(repo) image updated", "deployment \"\(repo)\" successfully rolled out"]
        case "smoke test":
            return ["GET /healthz 200 (12ms)", "GET /api/v1/courses 200 (88ms)", "smoke test passed (6/6)"]
        case "人工核准":
            return ["等待 production 環境核准(需要 1 位 reviewer)"]
        case "canary rollout (prod)":
            return ["argo rollouts: setWeight 10", "analysis run: success-rate ≥ 99.5% ✓", "argo rollouts: setWeight 25"]
        case "health gate":
            return ["檢查 5xx、p95 latency、錯誤預算消耗"]
        default:
            return ["$ \(job)", "done"]
        }
    }

    /// 依照「做到第幾個 stage、該 stage 狀態」產生 stages。
    private mutating func makeStages(runId: Int, repo: String, sha: String, stack: Stack, names: [String],
                                     at current: Int, status: RunStatus, failingJob: Int = 0,
                                     failLog: [String] = [], runningJob: Int = 1) -> [PipelineStage] {
        var stages: [PipelineStage] = []
        for (i, name) in names.enumerated() {
            var jobs: [PipelineJob] = []
            let jn = jobNames(stage: name, stack: stack)
            for (k, job) in jn.enumerated() {
                let jid = "\(runId)-s\(i)-j\(k)"
                var log = jobLog(job: job, repo: repo, sha: sha)
                var st: RunStatus
                var dur: TimeInterval? = Double(int(8...140))
                if i < current {
                    st = .succeeded
                    if job == "人工核准" { dur = Double(int(60...900)) }
                } else if i == current {
                    switch status {
                    case .running:
                        if k < runningJob { st = .succeeded }
                        else if k == runningJob { st = .running; dur = nil; log.removeLast() }
                        else { st = .queued; dur = nil; log = [] }
                    case .failed:
                        if k == failingJob { st = .failed; log += failLog }
                        else if k < failingJob { st = .succeeded }
                        else { st = .canceled; dur = nil; log = [] }
                    case .waitingApproval:
                        if k == 0 { st = .waitingApproval; dur = nil } else { st = .queued; dur = nil; log = [] }
                    case .canceled:
                        st = k == 0 ? .canceled : .queued
                        dur = k == 0 ? dur : nil
                        log = k == 0 ? log + ["Run canceled by user"] : []
                    default:
                        st = status; dur = nil; log = []
                    }
                } else {
                    st = (status == .failed || status == .canceled) ? .canceled : .queued
                    dur = nil; log = []
                }
                if st == .succeeded && job == "人工核准" { log = ["已由 蔡庭宇 核准", "approved"] }
                jobs.append(PipelineJob(id: jid, name: job, status: st, duration: dur, log: log))
            }
            let stageStatus: RunStatus
            if i < current { stageStatus = .succeeded }
            else if i == current { stageStatus = status }
            else { stageStatus = (status == .failed || status == .canceled) ? .canceled : .queued }
            stages.append(PipelineStage(id: "\(runId)-s\(i)", name: name, status: stageStatus, jobs: jobs))
        }
        return stages
    }

    mutating func makePipelineRuns() -> [PipelineRun] {
        let full = ["Checkout", "Build", "Test", "Security scan", "Deploy staging", "Deploy prod"]
        let ci = ["Checkout", "Build", "Test", "Security scan"]
        let tf = ["Checkout", "Build", "Test", "Deploy staging", "Deploy prod"]

        struct R {
            let id: Int, pipeline: String, repo: String, branch: String, msg: String, trigger: String, by: Actor
            let status: RunStatus, names: [String], at: Int, startedMin: Double, durMin: Double?
            let env: DeployEnv?, diag: String?, failJob: Int, failLog: [String], stack: Stack
        }
        let rs: [R] = [
            R(id: 1290, pipeline: "api-server / main", repo: "api-server", branch: "main",
              msg: "release: v2.14.0(課程推薦、批次匯入學員)", trigger: "push", by: .person("mei"),
              status: .running, names: full, at: 5, startedMin: 38, durMin: nil, env: .production, diag: nil,
              failJob: 0, failLog: [], stack: .go),
            R(id: 1289, pipeline: "course-web / PR #1423", repo: "course-web", branch: "yuting/paddle-inline",
              msg: "fix: 處理 checkout.closed 事件", trigger: "PR", by: .person("yuting"),
              status: .queued, names: ci, at: 0, startedMin: 1, durMin: nil, env: nil, diag: nil,
              failJob: 0, failLog: [], stack: .web),
            R(id: 1288, pipeline: "course-web / main", repo: "course-web", branch: "main",
              msg: "feat: 會員中心顯示訂閱到期日", trigger: "push", by: .person("yuting"),
              status: .running, names: full, at: 2, startedMin: 7, durMin: nil, env: .staging, diag: nil,
              failJob: 0, failLog: [], stack: .web),
            R(id: 1287, pipeline: "payments / main", repo: "payments", branch: "main",
              msg: "release: v3.4.0(年繳方案、優惠碼)", trigger: "push", by: .person("mei"),
              status: .waitingApproval, names: full, at: 5, startedMin: 132, durMin: nil, env: .production, diag: nil,
              failJob: 0, failLog: [], stack: .go),
            R(id: 1286, pipeline: "course-web / PR #1418", repo: "course-web", branch: "yuting/course-map-filter",
              msg: "feat: 課程地圖零基礎篩選", trigger: "PR", by: .person("yuting"),
              status: .failed, names: ci, at: 2, startedMin: 52, durMin: 9.5, env: nil,
              diag: "playwright e2e `course-map.spec.ts:48` 在 webkit 失敗:篩選後等待 `.course-card` 出現逾時。同一測試在 main 過去 14 天通過 100%,判斷不是 flaky,而是這個 PR 把篩選改成 debounce 300ms 造成斷言太早。建議在測試中等待 `/api/v1/courses?level=` 回應後再斷言,或把 debounce 移到元件外。信心:高。",
              failJob: 1,
              failLog: ["  ✘  [webkit] › course-map.spec.ts:48:5 › 零基礎篩選只顯示對應課程 (10.2s)",
                        "    Error: Timed out 10000ms waiting for expect(locator).toHaveCount(6)",
                        "    Locator: locator('.course-card')  Expected: 6  Received: 14",
                        "  1 failed, 85 passed (2.6m)", "Error: Process completed with exit code 1."],
              stack: .web),
            R(id: 1285, pipeline: "live-player / main", repo: "live-player", branch: "main",
              msg: "fix: 手機直式播放窗字幕被切掉", trigger: "push", by: .person("kai"),
              status: .succeeded, names: full, at: 6, startedMin: 80, durMin: 21, env: .production, diag: nil,
              failJob: 0, failLog: [], stack: .web),
            R(id: 1284, pipeline: "auth / main", repo: "auth", branch: "main",
              msg: "feat: Google One Tap 登入", trigger: "push", by: .person("jason"),
              status: .failed, names: full, at: 3, startedMin: 230, durMin: 6.2, env: .staging,
              diag: "govulncheck 找到 GO-2024-3333(CVE-2024-45338,golang.org/x/net/html 的 DoS 弱點),auth 透過 OAuth callback 的 HTML 解析會呼叫到受影響函式。不是程式邏輯錯誤。「依賴套件更新」已開 PR #1424 把 x/net 升到 v0.31.0,合併後重跑即可。",
              failJob: 1,
              failLog: ["Vulnerability #1: GO-2024-3333", "    Non-linear parsing of case-insensitive content in golang.org/x/net/html",
                        "  Found in: golang.org/x/net@v0.29.0  Fixed in: golang.org/x/net@v0.31.0",
                        "  Example traces found: #1: internal/oauth/callback.go:88:24: oauth.parseForm calls html.Parse",
                        "Your code is affected by 1 vulnerability.", "Error: Process completed with exit code 3."],
              stack: .go),
            R(id: 1283, pipeline: "api-server / PR #1425", repo: "api-server", branch: "jason/progress-cache",
              msg: "perf: 課程進度 API 加 Redis 快取", trigger: "PR", by: .person("jason"),
              status: .succeeded, names: ci, at: 4, startedMin: 125, durMin: 8.4, env: nil, diag: nil,
              failJob: 0, failLog: [], stack: .go),
            R(id: 1282, pipeline: "course-web / PR #1421", repo: "course-web", branch: "ai/fix-flaky-enrollment-e2e",
              msg: "fix(ci): enrollment e2e 等待 network idle", trigger: "AI 重試", by: .ai("ai-ci-triage"),
              status: .succeeded, names: ci, at: 4, startedMin: 520, durMin: 7.9, env: nil, diag: nil,
              failJob: 0, failLog: [], stack: .web),
            R(id: 1281, pipeline: "live-player / main", repo: "live-player", branch: "main",
              msg: "feat: HLS 預載下一段影片", trigger: "push", by: .person("kai"),
              status: .failed, names: full, at: 5, startedMin: 345, durMin: 18.3, env: .production,
              diag: "部署後 health gate 失敗:5xx 比例在 canary 10% 時升到 3.1%(門檻 1%)。錯誤集中在 `GET /segments/*.ts` 回 503,原因是預載讓每個播放器同時發出 3 倍的 segment 請求,打爆 media-cdn 原點的連線上限。「異常部署自動回滾」已回滾到 v1.8.0。建議預載加上並行上限,並先在 staging 用 k6 壓測。",
              failJob: 2,
              failLog: ["health gate: 5xx ratio 3.12% > 1.00% (window 5m)", "health gate: p95 latency 2,840ms > 1,500ms",
                        "abort rollout: live-player", "Error: health gate failed"],
              stack: .web),
            R(id: 1280, pipeline: "infra / main", repo: "infra", branch: "main",
              msg: "cdn: 調整 HLS segment 快取 TTL", trigger: "push", by: .person("hao"),
              status: .succeeded, names: tf, at: 5, startedMin: 1220, durMin: 12.6, env: .production, diag: nil,
              failJob: 0, failLog: [], stack: .terraform),
            R(id: 1279, pipeline: "course-web / main", repo: "course-web", branch: "main",
              msg: "chore: 更新文案", trigger: "push", by: .person("sharon"),
              status: .canceled, names: full, at: 2, startedMin: 1380, durMin: 2.1, env: .staging, diag: nil,
              failJob: 0, failLog: [], stack: .web),
            R(id: 1278, pipeline: "api-server / nightly", repo: "api-server", branch: "main",
              msg: "nightly: 完整整合測試與 benchmark", trigger: "排程", by: .system,
              status: .succeeded, names: ci, at: 4, startedMin: 1500, durMin: 24.0, env: nil, diag: nil,
              failJob: 0, failLog: [], stack: .go),
            R(id: 1277, pipeline: "auth / main", repo: "auth", branch: "main",
              msg: "fix: OAuth state cookie 改為 SameSite=Lax", trigger: "push", by: .person("jason"),
              status: .succeeded, names: full, at: 6, startedMin: 1720, durMin: 19.4, env: .production, diag: nil,
              failJob: 0, failLog: [], stack: .go),
            R(id: 1276, pipeline: "payments / PR #1413", repo: "payments", branch: "release/v3.4.0",
              msg: "feat: 年繳方案與優惠碼", trigger: "PR", by: .person("mei"),
              status: .failed, names: ci, at: 2, startedMin: 1910, durMin: 5.7, env: nil,
              diag: "`TestWebhookSubscriptionUpdated_OutOfOrder` 失敗:測試送出兩個亂序事件,預期以 occurred_at 較新者為準,但實作用到達順序覆寫。這是真正的 bug,不是 flaky;mei 已在後續 commit 改為比較 occurred_at,PR #1413 重跑通過後合併。",
              failJob: 0,
              failLog: ["--- FAIL: TestWebhookSubscriptionUpdated_OutOfOrder (0.04s)",
                        "    webhook_test.go:211: status = \"past_due\", want \"active\"", "FAIL\tcoursely/payments/internal/webhook\t1.882s"],
              stack: .go),
        ]

        var runs: [PipelineRun] = []
        for r in rs {
            let sha = hex(40)
            let stages = makeStages(runId: r.id, repo: r.repo, sha: sha, stack: r.stack, names: r.names,
                                    at: r.at, status: r.status, failingJob: r.failJob, failLog: r.failLog)
            runs.append(PipelineRun(id: r.id, pipeline: r.pipeline, repo: r.repo, branch: r.branch, commitSHA: sha,
                                    commitMessage: r.msg, trigger: r.trigger, triggeredBy: r.by, status: r.status,
                                    stages: stages, startedAt: ago(minutes: r.startedMin),
                                    duration: r.durMin.map { $0 * 60 }, aiDiagnosis: r.diag, targetEnv: r.env))
        }
        return runs
    }

    func makeDeployments() -> [Deployment] {
        [
            Deployment(id: "dep-3071", service: "api-server", version: "v2.14.0", previousVersion: "v2.13.2", env: .production,
                       status: .running, strategy: .canary, progress: 0.25, canaryPercent: 25, startedAt: ago(minutes: 14),
                       finishedAt: nil, by: .person("mei"),
                       changes: ["課程推薦 API(依學習進度)", "批次匯入學員 CSV", "migration 0085、0086(可回滾)", "升級 pgx 到 v5.7.1"],
                       healthGate: .running),
            Deployment(id: "dep-3070", service: "payments", version: "v3.4.0", previousVersion: "v3.3.7", env: .production,
                       status: .waitingApproval, strategy: .blueGreen, progress: 0, canaryPercent: nil, startedAt: ago(minutes: 112),
                       finishedAt: nil, by: .person("mei"),
                       changes: ["年繳方案 price", "優惠碼(discount)處理", "subscription.updated 亂序保護"],
                       healthGate: .pending),
            Deployment(id: "dep-3069", service: "course-web", version: "v5.22.0-rc.1", previousVersion: "v5.21.0", env: .staging,
                       status: .succeeded, strategy: .rolling, progress: 1, canaryPercent: nil, startedAt: ago(hours: 2.5),
                       finishedAt: ago(hours: 2.4), by: .person("yuting"),
                       changes: ["會員中心顯示訂閱到期日", "結帳頁 loading 狀態"], healthGate: .passed),
            Deployment(id: "dep-3068", service: "api-server", version: "v2.14.0", previousVersion: "v2.13.2", env: .staging,
                       status: .succeeded, strategy: .rolling, progress: 1, canaryPercent: nil, startedAt: ago(minutes: 24),
                       finishedAt: ago(minutes: 20), by: .person("mei"),
                       changes: ["課程推薦 API", "批次匯入學員"], healthGate: .passed),
            Deployment(id: "dep-3067", service: "live-player", version: "v1.8.2", previousVersion: "v1.8.0", env: .staging,
                       status: .succeeded, strategy: .rolling, progress: 1, canaryPercent: nil, startedAt: ago(minutes: 70),
                       finishedAt: ago(minutes: 66), by: .person("kai"),
                       changes: ["手機直式播放窗字幕修正", "HLS 預載加並行上限(2)"], healthGate: .passed),
            Deployment(id: "dep-3066", service: "live-player", version: "v1.8.0", previousVersion: "v1.8.1", env: .production,
                       status: .succeeded, strategy: .rolling, progress: 1, canaryPercent: nil, startedAt: ago(hours: 5.4),
                       finishedAt: ago(hours: 5.3), by: .ai("ai-rollback"),
                       changes: ["自動回滾:v1.8.1 canary 5xx 3.1% 超過門檻 1%", "詳見 INC-2036"], healthGate: .passed),
            Deployment(id: "dep-3065", service: "live-player", version: "v1.8.1", previousVersion: "v1.8.0", env: .production,
                       status: .failed, strategy: .canary, progress: 0.1, canaryPercent: 10, startedAt: ago(hours: 5.6),
                       finishedAt: ago(hours: 5.4), by: .person("kai"),
                       changes: ["HLS 預載下一段影片", "播放器 bundle 拆分"], healthGate: .failed),
            Deployment(id: "dep-3064", service: "media-cdn", version: "cfg-142", previousVersion: "cfg-141", env: .production,
                       status: .succeeded, strategy: .rolling, progress: 1, canaryPercent: nil, startedAt: ago(hours: 20.3),
                       finishedAt: ago(hours: 20.1), by: .person("hao"),
                       changes: ["HLS segment 快取 TTL 從 1h 改為 24h"], healthGate: .passed),
            Deployment(id: "dep-3063", service: "auth", version: "v1.9.4", previousVersion: "v1.9.3", env: .production,
                       status: .succeeded, strategy: .canary, progress: 1, canaryPercent: 100, startedAt: ago(hours: 28.6),
                       finishedAt: ago(hours: 28), by: .person("jason"),
                       changes: ["OAuth state cookie 改為 SameSite=Lax"], healthGate: .passed),
            Deployment(id: "dep-3062", service: "worker", version: "v2.15.0-dev.3", previousVersion: "v2.15.0-dev.2", env: .development,
                       status: .succeeded, strategy: .rolling, progress: 1, canaryPercent: nil, startedAt: ago(hours: 3),
                       finishedAt: ago(hours: 2.95), by: .person("jason"),
                       changes: ["billing queue 消費者可水平擴展(實驗)"], healthGate: .passed),
            Deployment(id: "dep-3061", service: "course-web", version: "v5.21.0", previousVersion: "v5.20.3", env: .production,
                       status: .succeeded, strategy: .rolling, progress: 1, canaryPercent: nil, startedAt: ago(days: 2.1),
                       finishedAt: ago(days: 2.09), by: .person("yuting"),
                       changes: ["課程地圖讀資料庫", "手機直式 4:5 播放窗"], healthGate: .passed),
        ]
    }
}

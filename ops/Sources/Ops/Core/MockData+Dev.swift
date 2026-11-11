import Foundation

// 原始碼庫、PR、工作項目、Sprint
extension MockDataBuilder {
    func makeRepos() -> [Repository] {
        [
            Repository(id: "course-web", name: "coursely/course-web", language: "TypeScript", defaultBranch: "main", openPRs: 3,
                       coverage: 71.4, lastCommit: ago(minutes: 18),
                       weeklyCommits: [34, 41, 28, 39, 52, 47, 31, 44, 58, 49, 62, 37], vulnerabilities: 1),
            Repository(id: "api-server", name: "coursely/api-server", language: "Go", defaultBranch: "main", openPRs: 2,
                       coverage: 78.9, lastCommit: ago(minutes: 26),
                       weeklyCommits: [46, 52, 39, 61, 55, 48, 66, 59, 71, 63, 58, 44], vulnerabilities: 0),
            Repository(id: "auth", name: "coursely/auth", language: "Go", defaultBranch: "main", openPRs: 1,
                       coverage: 84.2, lastCommit: ago(hours: 3.5),
                       weeklyCommits: [8, 12, 6, 9, 14, 7, 11, 5, 10, 13, 9, 4], vulnerabilities: 2),
            Repository(id: "payments", name: "coursely/payments", language: "Go", defaultBranch: "main", openPRs: 1,
                       coverage: 81.0, lastCommit: ago(minutes: 41),
                       weeklyCommits: [11, 9, 15, 13, 18, 22, 17, 14, 20, 26, 19, 15], vulnerabilities: 0),
            Repository(id: "live-player", name: "coursely/live-player", language: "TypeScript", defaultBranch: "main", openPRs: 1,
                       coverage: 63.5, lastCommit: ago(hours: 1.2),
                       weeklyCommits: [22, 27, 19, 31, 25, 36, 29, 40, 33, 45, 38, 21], vulnerabilities: 0),
            Repository(id: "infra", name: "coursely/infra", language: "HCL", defaultBranch: "main", openPRs: 1,
                       coverage: 0, lastCommit: ago(hours: 20),
                       weeklyCommits: [5, 3, 7, 4, 6, 9, 2, 5, 8, 6, 4, 3], vulnerabilities: 0),
        ]
    }

    func makePullRequests() -> [PullRequest] {
        [
            PullRequest(id: 1426, title: "payments:Paddle webhook 改為先寫入 outbox 再非同步處理", repo: "payments",
                        author: .person("mei"), branch: "mei/webhook-outbox", status: .open, checks: .running,
                        reviewers: ["jason", "tim"],
                        aiReview: AIReview(summary: "把 webhook handler 的同步 DB 交易拆成 outbox + worker,能直接解掉 INC-2041 的逾時問題。",
                                           risk: .medium,
                                           findings: ["outbox 表缺少 (event_id) 唯一索引,重送時可能重複開通權限",
                                                      "worker 重試沒有上限,建議加 dead-letter queue",
                                                      "migration 0087 會鎖 subscriptions 表,建議改用 CREATE INDEX CONCURRENTLY"]),
                        additions: 412, deletions: 138, createdAt: ago(minutes: 35), updatedAt: ago(minutes: 6), linkedItem: "OPS-151"),
            PullRequest(id: 1425, title: "api-server:課程進度 API 加上 Redis 快取(TTL 60s)", repo: "api-server",
                        author: .person("jason"), branch: "jason/progress-cache", status: .approved, checks: .passed,
                        reviewers: ["mei"],
                        aiReview: AIReview(summary: "快取鍵包含 user_id 與 course_id,失效時機正確。預估 p95 降低約 40ms。",
                                           risk: .low, findings: ["建議在 invalidate 時一併清除 /me/summary 的快取"]),
                        additions: 186, deletions: 42, createdAt: ago(hours: 20), updatedAt: ago(hours: 2), linkedItem: "OPS-138"),
            PullRequest(id: 1424, title: "chore(deps):升級 golang.org/x/net 到 v0.31.0(修補 CVE-2024-45338)", repo: "auth",
                        author: .ai("ai-deps"), branch: "ai/deps-x-net-0.31", status: .open, checks: .passed,
                        reviewers: ["jason"],
                        aiReview: AIReview(summary: "僅更新 go.mod / go.sum,無 API 變更;已在 staging 跑完整合測試。",
                                           risk: .low, findings: ["修補 html tokenizer 的 DoS 弱點(CVSS 5.3)"]),
                        additions: 6, deletions: 6, createdAt: ago(hours: 4), updatedAt: ago(hours: 3.2), linkedItem: "OPS-147"),
            PullRequest(id: 1423, title: "course-web:結帳頁改用 Paddle.js v2 inline checkout", repo: "course-web",
                        author: .person("yuting"), branch: "yuting/paddle-inline", status: .changesRequested, checks: .failed,
                        reviewers: ["kai", "mei"],
                        aiReview: AIReview(summary: "Inline checkout 流程正確,但 e2e 測試 checkout.spec.ts 在 Safari 失敗。",
                                           risk: .medium,
                                           findings: ["`customer.email` 預填時沒有 escape,可能造成 XSS(低機率)",
                                                      "缺少 checkout.closed 事件處理,使用者關掉視窗後按鈕會一直 loading",
                                                      "e2e 失敗原因:iframe 載入逾時 10s,建議提高到 20s 或 mock Paddle"]),
                        additions: 538, deletions: 211, createdAt: ago(days: 2), updatedAt: ago(hours: 1.5), linkedItem: "OPS-133"),
            PullRequest(id: 1422, title: "live-player:修正 Safari 17 上字幕與影片不同步", repo: "live-player",
                        author: .person("kai"), branch: "kai/safari-subtitle-sync", status: .open, checks: .passed,
                        reviewers: ["yuting"], aiReview: nil,
                        additions: 74, deletions: 19, createdAt: ago(hours: 6), updatedAt: ago(hours: 5), linkedItem: "OPS-141"),
            PullRequest(id: 1421, title: "fix(ci):穩定 flaky 的 enrollment e2e 測試(等待 network idle)", repo: "course-web",
                        author: .ai("ai-ci-triage"), branch: "ai/fix-flaky-enrollment-e2e", status: .open, checks: .passed,
                        reviewers: ["yuting"],
                        aiReview: AIReview(summary: "過去 14 天此測試失敗 9 次,全部是 race condition。改為等待 API 回應後再斷言。",
                                           risk: .low, findings: ["只動測試程式,不影響產品程式碼"]),
                        additions: 23, deletions: 11, createdAt: ago(hours: 9), updatedAt: ago(hours: 8), linkedItem: "OPS-144"),
            PullRequest(id: 1420, title: "api-server:v2.14.0 發版(課程推薦、批次匯入學員)", repo: "api-server",
                        author: .person("mei"), branch: "release/v2.14.0", status: .merged, checks: .passed,
                        reviewers: ["jason", "tim"],
                        aiReview: AIReview(summary: "包含 23 個 commit,新增 2 個 migration(皆可回滾)。",
                                           risk: .medium, findings: ["migration 0085 新增欄位帶預設值,PostgreSQL 16 不會重寫表,安全"]),
                        additions: 2140, deletions: 680, createdAt: ago(days: 1.2), updatedAt: ago(hours: 3), linkedItem: "OPS-130"),
            PullRequest(id: 1419, title: "infra:staging 夜間縮到 0 節點(省 ~US$310/月)", repo: "infra",
                        author: .ai("ai-cost"), branch: "ai/staging-night-scale", status: .draft, checks: .passed,
                        reviewers: ["tim"],
                        aiReview: AIReview(summary: "terraform plan:修改 1 個 node pool 的 autoscaling 與 1 個 Cloud Scheduler。",
                                           risk: .medium, findings: ["夜間排程 CI 若依賴 staging 會失敗,需同步調整 nightly e2e 時間"]),
                        additions: 48, deletions: 4, createdAt: ago(days: 1), updatedAt: ago(hours: 22), linkedItem: "OPS-149"),
            PullRequest(id: 1418, title: "course-web:課程地圖支援「零基礎」篩選與學習路徑", repo: "course-web",
                        author: .person("yuting"), branch: "yuting/course-map-filter", status: .open, checks: .failed,
                        reviewers: ["kai", "sharon"], aiReview: nil,
                        additions: 322, deletions: 97, createdAt: ago(days: 1.5), updatedAt: ago(minutes: 50), linkedItem: "OPS-135"),
            PullRequest(id: 1417, title: "auth:Google One Tap 登入", repo: "auth",
                        author: .person("jason"), branch: "jason/google-one-tap", status: .merged, checks: .passed,
                        reviewers: ["mei"],
                        aiReview: AIReview(summary: "ID token 驗證流程正確,有檢查 aud 與 nonce。", risk: .medium,
                                           findings: ["建議把 Google 公鑰快取時間從 24h 改成依 Cache-Control"]),
                        additions: 296, deletions: 31, createdAt: ago(days: 4), updatedAt: ago(days: 2), linkedItem: "OPS-128"),
            PullRequest(id: 1416, title: "live-player:回滾 v1.8.1 的 HLS 預載策略", repo: "live-player",
                        author: .ai("ai-rollback"), branch: "ai/revert-hls-prefetch", status: .merged, checks: .passed,
                        reviewers: ["kai"],
                        aiReview: AIReview(summary: "Revert commit 7f3a1c2;v1.8.1 上線後 5xx 從 0.2% 升到 3.1%。", risk: .low,
                                           findings: ["已自動部署 v1.8.0,詳見 INC-2036"]),
                        additions: 31, deletions: 88, createdAt: ago(hours: 5.5), updatedAt: ago(hours: 5), linkedItem: "OPS-145"),
            PullRequest(id: 1415, title: "worker:billing queue 消費者改為可水平擴展", repo: "api-server",
                        author: .person("jason"), branch: "jason/billing-consumer-scale", status: .draft, checks: .pending,
                        reviewers: [], aiReview: nil,
                        additions: 158, deletions: 64, createdAt: ago(minutes: 20), updatedAt: ago(minutes: 4), linkedItem: "OPS-152"),
            PullRequest(id: 1414, title: "notifier:LINE 推播範本改版(開課提醒)", repo: "api-server",
                        author: .person("kai"), branch: "kai/line-template", status: .closed, checks: .passed,
                        reviewers: ["sharon"], aiReview: nil,
                        additions: 88, deletions: 20, createdAt: ago(days: 6), updatedAt: ago(days: 3), linkedItem: nil),
            PullRequest(id: 1413, title: "payments:v3.4.0(年繳方案、優惠碼)", repo: "payments",
                        author: .person("mei"), branch: "release/v3.4.0", status: .merged, checks: .passed,
                        reviewers: ["jason", "sharon"],
                        aiReview: AIReview(summary: "新增年繳 price 與 discount 處理;已對 Paddle sandbox 跑完 webhook 模擬。", risk: .high,
                                           findings: ["金額計算全部使用整數最小單位,OK",
                                                      "subscription.updated 的處理順序依賴 occurred_at,建議加上版本檢查避免亂序"]),
                        additions: 980, deletions: 245, createdAt: ago(days: 3), updatedAt: ago(hours: 2.2), linkedItem: "OPS-131"),
        ]
    }

    func makeWorkItems() -> [WorkItem] {
        func w(_ id: String, _ title: String, _ type: WorkType, _ status: WorkStatus, _ who: Actor?, _ pts: Int,
               _ svc: String?, ai: Bool = false) -> WorkItem {
            WorkItem(id: id, title: title, type: type, status: status, assignee: who, points: pts, service: svc, createdByAI: ai)
        }
        return [
            w("OPS-128", "Google One Tap 登入", .feature, .done, .person("jason"), 5, "auth"),
            w("OPS-129", "會員中心顯示訂閱到期日", .feature, .done, .person("yuting"), 3, "course-web"),
            w("OPS-130", "api-server v2.14.0 發版", .chore, .done, .person("mei"), 2, "api-server"),
            w("OPS-131", "年繳方案與優惠碼", .feature, .review, .person("mei"), 8, "payments"),
            w("OPS-132", "課程推薦 API(依學習進度)", .feature, .done, .person("jason"), 5, "api-server"),
            w("OPS-133", "結帳頁改用 Paddle inline checkout", .feature, .review, .person("yuting"), 5, "course-web"),
            w("OPS-134", "批次匯入學員(CSV)", .feature, .inProgress, .person("kai"), 5, "api-server"),
            w("OPS-135", "課程地圖「零基礎」篩選", .feature, .inProgress, .person("yuting"), 3, "course-web"),
            w("OPS-136", "播放器課中練習作答紀錄", .feature, .todo, .person("kai"), 8, "live-player"),
            w("OPS-137", "退款後自動停用課程權限", .feature, .todo, .person("mei"), 3, "payments"),
            w("OPS-138", "課程進度 API 加快取", .chore, .review, .person("jason"), 2, "api-server"),
            w("OPS-139", "Grafana 儀表板改用 SLO burn rate", .chore, .todo, .person("hao"), 3, nil),
            w("OPS-140", "手機直式播放窗字幕被切掉", .bug, .done, .person("kai"), 2, "live-player"),
            w("OPS-141", "Safari 17 字幕不同步", .bug, .review, .person("kai"), 2, "live-player"),
            w("OPS-142", "pg-prod-primary 連線數接近上限", .bug, .inProgress, .person("tim"), 3, "postgres", ai: true),
            w("OPS-143", "auth 錯誤預算剩 18%,檢查 OAuth callback 重試", .bug, .todo, .person("jason"), 3, "auth", ai: true),
            w("OPS-144", "穩定 flaky enrollment e2e 測試", .chore, .review, .ai("ai-ci-triage"), 1, "course-web", ai: true),
            w("OPS-145", "INC-2036 檢討:HLS 預載上線前缺少壓測", .incident, .inProgress, .person("kai"), 2, "live-player"),
            w("OPS-146", "INC-2031 檢討:OAuth state cookie SameSite 設定", .incident, .done, .person("jason"), 2, "auth"),
            w("OPS-147", "修補 CVE-2024-45338(golang.org/x/net)", .chore, .review, .ai("ai-deps"), 1, "auth", ai: true),
            w("OPS-148", "INC-2027 檢討:WAL 磁碟告警門檻與自動擴充", .incident, .done, .person("tim"), 3, "postgres"),
            w("OPS-149", "staging 夜間縮容省成本", .chore, .backlog, .ai("ai-cost"), 2, nil, ai: true),
            w("OPS-150", "INC-2022 檢討:CDN 快取規則改為 Terraform 管理", .incident, .done, .person("hao"), 3, "media-cdn"),
            w("OPS-151", "Paddle webhook 改 outbox 非同步處理", .incident, .inProgress, .person("mei"), 5, "payments"),
            w("OPS-152", "billing queue 消費者水平擴展", .incident, .inProgress, .person("jason"), 3, "worker"),
            w("OPS-153", "webhook 處理延遲的 SLO 與告警", .incident, .backlog, nil, 2, "payments", ai: true),
            w("OPS-154", "兒童課家長儀表板", .feature, .backlog, nil, 8, "course-web"),
            w("OPS-155", "影片起播時間改用 RUM 量測", .chore, .backlog, .person("hao"), 3, "live-player"),
        ]
    }

    func makeSprint() -> Sprint {
        let cal = Calendar(identifier: .gregorian)
        let today = cal.startOfDay(for: now)
        // 兩週 sprint 的 10 個工作天,今天是第 10 天,明天收尾
        let start = today.addingTimeInterval(-9 * 86400)
        let end = today.addingTimeInterval(86400 + 18 * 3600)
        let total = 58.0
        let remaining: [Double] = [58, 57, 54, 54, 49, 45, 41, 36, 30, 25]
        var burndown: [MetricPoint] = []
        var ideal: [MetricPoint] = []
        for d in 0...10 {
            let date = start.addingTimeInterval(Double(d) * 86400)
            ideal.append(MetricPoint(date: date, value: total - total * Double(d) / 10))
            if d < remaining.count { burndown.append(MetricPoint(date: date, value: remaining[d])) }
        }
        return Sprint(name: "Sprint 47", start: start, end: end,
                      goal: "年繳方案上線、Paddle inline checkout、播放器穩定度(5xx < 0.3%)",
                      burndown: burndown, idealBurndown: ideal)
    }
}

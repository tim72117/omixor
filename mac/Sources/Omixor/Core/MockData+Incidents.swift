import Foundation

// 事件與值班
extension MockDataBuilder {
    private mutating func ev(_ minutesAgo: Double, _ actor: Actor, _ kind: IncidentEvent.Kind, _ text: String) -> IncidentEvent {
        IncidentEvent(id: uuid(), date: ago(minutes: minutesAgo), actor: actor, kind: kind, text: text)
    }

    mutating func makeIncidents() -> [Incident] {
        let inc2041: [IncidentEvent] = [
            ev(54, .system, .alert, "ALT-5121 觸發:Paddle webhook 處理 p95 2.3s(門檻 2s)"),
            ev(53, .ai("ai-alert-triage"), .aiFinding, "初判:延遲與 pg-prod-primary 的 lock wait 同時上升;過去 24 小時沒有 payments 部署,排除變更造成。"),
            ev(52, .person("tim"), .status, "開立 INC-2041(SEV2),擔任指揮官,狀態「調查中」"),
            ev(50, .person("tim"), .note, "PagerDuty 呼叫 mei(payments 負責人)加入;在 #inc-2041 開戰情室"),
            ev(47, .person("sharon"), .note, "客服回報:有 12 位學員付款成功但課程還沒開通,已先在 FAQ 公告「開通可能延遲 30 分鐘」"),
            ev(44, .ai("ai-alert-triage"), .aiFinding, "查詢 pg_locks:payments 的 `UPDATE subscriptions` 交易平均持有鎖 4.1s,交易內同步呼叫 notifier 寄信(外部 SMTP 偶發 3s 以上)。webhook 逾時後 Paddle 重送,同一個 subscription 產生鎖競爭。"),
            ev(41, .system, .alert, "ALT-5122 觸發:billing 佇列積壓 612 筆"),
            ev(38, .person("mei"), .note, "確認 AI 的判讀:v3.3.7 的 grant_access 在交易內 await 寄信,平常 SMTP 很快所以沒被發現"),
            ev(35, .person("mei"), .status, "狀態改為「已找到原因」"),
            ev(33, .system, .alert, "ALT-5123 觸發:pg-prod-primary 連線數 182 / 200"),
            ev(30, .ai("ai-autoscale"), .action, "準備把 worker billing 消費者從 4 擴到 10(等待核准 APV-311)"),
            ev(27, .ai("ai-alert-triage"), .action, "準備終止 64 個 idle in transaction 超過 10 分鐘的連線(等待核准 APV-312)"),
            ev(22, .person("tim"), .action, "暫時把 notifier 的 SMTP 逾時從 30s 降到 3s(feature flag smtp_fast_fail)"),
            ev(18, .person("jason"), .note, "PR #1415 billing 消費者水平擴展,dev 驗證中"),
            ev(12, .ai("ai-alert-triage"), .aiFinding, "降低 SMTP 逾時後,webhook p95 從 4.8s 降到 3.9s,但佇列仍在增加(+9/s)。單靠降逾時不夠,需要擴容消費者或清掉卡住的連線。"),
            ev(6, .person("mei"), .deploy, "PR #1426(webhook outbox)已開,預計修補後走 hotfix v3.3.8"),
        ]
        let inc2039: [IncidentEvent] = [
            ev(192, .system, .alert, "ALT-5118 觸發:台灣地區起播時間 p95 2.7s"),
            ev(190, .ai("ai-alert-triage"), .aiFinding, "影響集中在 HiNet(AS3462),其他 ISP 正常。Cloudflare TPE POP 有 42% 請求被導到 LAX 回源。"),
            ev(185, .person("hao"), .status, "開立 INC-2039(SEV3),狀態「調查中」"),
            ev(171, .person("hao"), .note, "Cloudflare 狀態頁確認台北機房路由問題,非我方變更"),
            ev(160, .person("hao"), .action, "把 HLS 切片的原點暫時改指 asia-east1 bucket,減少跨洋回源"),
            ev(140, .ai("ai-alert-triage"), .aiFinding, "調整後 p95 從 3.1s 降到 2.4s,快取命中率回升到 91%。"),
            ev(132, .person("hao"), .status, "狀態改為「觀察中」,等待 Cloudflare 修復"),
        ]
        let inc2036: [IncidentEvent] = [
            ev(336, .person("kai"), .deploy, "開始部署 live-player v1.8.1(canary 10%)"),
            ev(331, .system, .alert, "ALT-5112 觸發:live-player 5xx 3.1%"),
            ev(330, .ai("ai-rollback"), .aiFinding, "canary 5xx 3.1% 超過門檻 1%,p95 2.8s;stable 正常。判定為 v1.8.1 造成。"),
            ev(329, .ai("ai-rollback"), .action, "自動回滾到 v1.8.0(自動執行模式)"),
            ev(324, .ai("ai-rollback"), .status, "回滾完成,5xx 回到 0.21%"),
            ev(320, .person("kai"), .status, "確認回滾,狀態改為「已解決」"),
            ev(300, .ai("ai-postmortem"), .note, "已產生事件檢討草稿,等待 kai 補充"),
        ]

        return [
            Incident(id: "INC-2041", title: "Paddle 付款 webhook 處理延遲,訂閱開通延後", sev: 2, status: .identified,
                     services: ["payments", "worker", "postgres"], commander: "tim", startedAt: ago(minutes: 52), resolvedAt: nil,
                     impact: "約 9% 新付款學員課程開通延遲 5–40 分鐘;付款本身成功,沒有重複扣款。已收到 12 筆客服詢問。",
                     timeline: inc2041,
                     aiSummary: "根因:payments v3.3.7 的 grant_access 在資料庫交易裡同步寄信,SMTP 變慢時交易持鎖過久,webhook 逾時觸發 Paddle 重送,鎖競爭與重送互相放大。目前:SMTP 快速失敗已部分緩解(p95 4.8s → 3.9s),billing 佇列 1,284 筆仍在增加。下一步:核准 APV-311(消費者擴到 10)與 APV-312(清除卡住連線),並以 PR #1426 的 outbox 作為根本修正。",
                     followUps: ["OPS-151", "OPS-152", "OPS-153"]),
            Incident(id: "INC-2039", title: "台灣部分用戶影片起播變慢", sev: 3, status: .monitoring,
                     services: ["live-player", "media-cdn"], commander: "hao", startedAt: ago(minutes: 185), resolvedAt: nil,
                     impact: "HiNet 用戶起播時間 p95 從 1.6s 升到 3.1s,目前 2.4s;未有播放失敗。",
                     timeline: inc2039,
                     aiSummary: "外部因素:Cloudflare 台北 POP 路由異常導致跨洋回源。已改用 asia-east1 原點緩解,等待供應商修復後恢復原設定。",
                     followUps: ["OPS-155"]),
            Incident(id: "INC-2036", title: "live-player v1.8.1 上線後 5xx 升高", sev: 2, status: .resolved,
                     services: ["live-player", "media-cdn"], commander: "kai", startedAt: ago(minutes: 331), resolvedAt: ago(minutes: 320),
                     impact: "10% canary 流量的學員約 6 分鐘無法載入影片片段。",
                     timeline: inc2036,
                     aiSummary: "v1.8.1 的 HLS 預載讓每個播放器同時發出 3 倍 segment 請求,超過原點連線上限。AI 在 1 分鐘內自動回滾,影響時間 6 分鐘。",
                     followUps: ["OPS-145"]),
            Incident(id: "INC-2031", title: "Google 登入回呼失敗(Safari)", sev: 3, status: .resolved,
                     services: ["auth", "course-web"], commander: "jason", startedAt: ago(days: 9.2), resolvedAt: ago(days: 9.05),
                     impact: "Safari 使用者約 3.5 小時無法用 Google 登入,影響約 640 人次。",
                     timeline: [
                        ev(9.2 * 1440, .system, .alert, "ALT-4877 觸發:登入失敗率 9.4%"),
                        ev(9.19 * 1440, .ai("ai-alert-triage"), .aiFinding, "失敗全部來自 Safari,錯誤為 state mismatch;與 2 小時前的 auth v1.9.2 部署時間相符。"),
                        ev(9.15 * 1440, .person("jason"), .action, "確認 state cookie 被設為 SameSite=Strict,修正並部署 v1.9.3"),
                        ev(9.05 * 1440, .person("jason"), .status, "狀態改為「已解決」"),
                     ],
                     aiSummary: "OAuth state cookie 設成 SameSite=Strict,Safari 跨站回呼時不送 cookie。修正為 Lax。",
                     followUps: ["OPS-146", "OPS-143"]),
            Incident(id: "INC-2027", title: "pg-prod-primary WAL 磁碟寫滿,API 唯讀 12 分鐘", sev: 1, status: .resolved,
                     services: ["postgres", "api-server", "payments"], commander: "tim", startedAt: ago(days: 21.4), resolvedAt: ago(days: 21.3),
                     impact: "所有寫入失敗 12 分鐘(學習進度、付款開通);讀取正常。",
                     timeline: [
                        ev(21.4 * 1440, .system, .alert, "ALT-4702 觸發:pg-prod-primary 磁碟 98%"),
                        ev(21.39 * 1440, .person("tim"), .status, "開立 INC-2027(SEV1)"),
                        ev(21.38 * 1440, .ai("ai-alert-triage"), .aiFinding, "replication slot `analytics_cdc` 已 3 天沒消費,WAL 累積 410 GB。"),
                        ev(21.36 * 1440, .person("tim"), .action, "刪除失效的 replication slot,磁碟回到 61%"),
                        ev(21.3 * 1440, .person("tim"), .status, "狀態改為「已解決」"),
                     ],
                     aiSummary: "失效的 CDC replication slot 讓 WAL 無法回收。已移除並加上 slot lag 告警。",
                     followUps: ["OPS-148"]),
            Incident(id: "INC-2022", title: "課程頁 CDN 快取錯誤,顯示其他人的會員資訊", sev: 2, status: .resolved,
                     services: ["course-web", "media-cdn"], commander: "hao", startedAt: ago(days: 34.5), resolvedAt: ago(days: 34.45),
                     impact: "約 40 分鐘內,少數登入使用者的課程頁標頭顯示錯誤名稱;沒有付款或個資欄位外洩。",
                     timeline: [
                        ev(34.5 * 1440, .person("sharon"), .note, "客服回報有學員看到別人的名字"),
                        ev(34.49 * 1440, .person("hao"), .status, "開立 INC-2022(SEV2)"),
                        ev(34.48 * 1440, .person("hao"), .action, "清除 CDN 快取,對 /courses/* 關閉快取"),
                        ev(34.45 * 1440, .person("hao"), .status, "狀態改為「已解決」"),
                     ],
                     aiSummary: "手動在 Cloudflare 新增的快取規則忽略了 Cookie,快取了已登入版本的 HTML。",
                     followUps: ["OPS-150"]),
        ]
    }

    func makeOnCall() -> [OnCallShift] {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "Asia/Taipei") ?? .current
        var anchor = cal.date(bySettingHour: 9, minute: 0, second: 0, of: now) ?? now
        if anchor > now { anchor = anchor.addingTimeInterval(-86400) }
        // 從 3 天前到 3 天後,共 7 個班次;今天的 Primary 是 tim
        let primary = ["hao", "jason", "mei", "tim", "hao", "jason", "mei"]
        let secondary = ["tim", "mei", "kai", "hao", "jason", "kai", "tim"]
        var shifts: [OnCallShift] = []
        for (i, k) in (-3...3).enumerated() {
            let start = anchor.addingTimeInterval(Double(k) * 86400)
            let end = start.addingTimeInterval(86400)
            shifts.append(OnCallShift(id: "oncall-\(i)-p", person: primary[i], role: "Primary", start: start, end: end))
            shifts.append(OnCallShift(id: "oncall-\(i)-s", person: secondary[i], role: "Secondary", start: start, end: end))
        }
        return shifts
    }
}

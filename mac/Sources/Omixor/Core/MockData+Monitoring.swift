import Foundation

// 告警、告警規則、日誌、儀表板
extension MockDataBuilder {
    func makeAlerts() -> [Alert] {
        [
            Alert(id: "ALT-5121", title: "Paddle webhook 處理延遲過高", severity: .critical, service: "payments", state: .firing,
                  rule: "histogram_quantile(0.95, paddle_webhook_duration_seconds) > 2 for 5m", value: "p95 4.8s",
                  startedAt: ago(minutes: 54), acknowledgedBy: nil, incidentId: "INC-2041",
                  aiNote: "延遲從 19:12 開始上升,和 pg-prod-primary 上 `subscriptions` 表的 lock wait 同時發生。webhook 在同一個交易裡同步開通權限並寄信,一筆卡住就整條佇列排隊。Paddle 會在 5 秒內未回 200 時重送,重送又放大流量。"),
            Alert(id: "ALT-5122", title: "billing 佇列積壓", severity: .warning, service: "worker", state: .firing,
                  rule: "queue_depth{queue=\"billing\"} > 500 for 10m", value: "1,284 筆",
                  startedAt: ago(minutes: 41), acknowledgedBy: nil, incidentId: "INC-2041",
                  aiNote: "是 ALT-5121 的連帶影響,不是獨立問題。worker 消費速率 ~22/s,進入速率 ~31/s。擴容到 10 個消費者預計 25 分鐘內清空(已送出核准請求)。"),
            Alert(id: "ALT-5123", title: "pg-prod-primary 連線數接近上限", severity: .warning, service: "postgres", state: .firing,
                  rule: "pg_stat_activity_count / pg_settings_max_connections > 0.85 for 5m", value: "182 / 200",
                  startedAt: ago(minutes: 33), acknowledgedBy: nil, incidentId: "INC-2041",
                  aiNote: "有 64 個連線處於 idle in transaction 超過 10 分鐘,來源都是 payments。建議終止這些連線,並把 payments 的 pgbouncer pool_mode 改成 transaction。"),
            Alert(id: "ALT-5124", title: "api-server canary 錯誤率高於基準", severity: .warning, service: "api-server", state: .firing,
                  rule: "rate(http_5xx{track=\"canary\"}[5m]) / rate(http_5xx{track=\"stable\"}[5m]) > 2", value: "0.42% vs 0.18%",
                  startedAt: ago(minutes: 6), acknowledgedBy: nil, incidentId: nil,
                  aiNote: "錯誤集中在 `POST /v1/enrollments/import`(新功能)回 500,原因是 CSV 有 BOM 時解析失敗,不影響既有流量。尚未超過自動回滾門檻 1%,建議繼續觀察,同時修正 BOM 處理。"),
            Alert(id: "ALT-5118", title: "台灣地區影片起播時間 p95 上升", severity: .warning, service: "live-player", state: .acknowledged,
                  rule: "histogram_quantile(0.95, player_start_seconds{country=\"TW\"}) > 2.5 for 10m", value: "p95 3.1s",
                  startedAt: ago(hours: 3.2), acknowledgedBy: .person("hao"), incidentId: "INC-2039",
                  aiNote: "問題集中在 HiNet 用戶,Cloudflare 台北 POP 回源到 us-central1。和 Cloudflare 狀態頁的 TPE 路由事件時間吻合。"),
            Alert(id: "ALT-5117", title: "CDN 快取命中率下降", severity: .info, service: "media-cdn", state: .acknowledged,
                  rule: "cdn_cache_hit_ratio < 0.9 for 15m", value: "86.4%",
                  startedAt: ago(hours: 3), acknowledgedBy: .ai("ai-alert-triage"), incidentId: "INC-2039",
                  aiNote: "與 ALT-5118 同源,已併入 INC-2039。"),
            Alert(id: "ALT-5112", title: "live-player 5xx 比例過高", severity: .critical, service: "live-player", state: .resolved,
                  rule: "rate(http_5xx[5m]) / rate(http_requests[5m]) > 0.02", value: "3.1%",
                  startedAt: ago(hours: 5.5), acknowledgedBy: .ai("ai-rollback"), incidentId: "INC-2036",
                  aiNote: "與 5 分鐘前的 v1.8.1 canary 部署相關,已自動回滾。"),
            Alert(id: "ALT-5108", title: "gke-prod-ae1-a3 記憶體壓力", severity: .warning, service: "api-server", state: .resolved,
                  rule: "node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes < 0.1", value: "可用 7.8%",
                  startedAt: ago(hours: 9), acknowledgedBy: .person("hao"), incidentId: nil, aiNote: nil),
            Alert(id: "ALT-5104", title: "dev-ae1-1 磁碟使用率 85%", severity: .info, service: "api-server", state: .silenced,
                  rule: "disk_used_percent > 85", value: "86%",
                  startedAt: ago(hours: 14), acknowledgedBy: .person("tim"), incidentId: nil,
                  aiNote: "主要是舊的 docker image(41 GB),可安全執行 docker image prune。"),
            Alert(id: "ALT-5099", title: "登入失敗率上升", severity: .warning, service: "auth", state: .resolved,
                  rule: "rate(auth_login_failed[10m]) / rate(auth_login_total[10m]) > 0.08", value: "11.2%",
                  startedAt: ago(days: 1.3), acknowledgedBy: .person("jason"), incidentId: nil,
                  aiNote: "來自 3 個 IP 段的撞庫嘗試,rate limit 已生效,不是系統問題。"),
        ]
    }

    func makeAlertRules() -> [AlertRule] {
        [
            AlertRule(id: "rule-webhook-latency", name: "Paddle webhook 延遲", expression: "histogram_quantile(0.95, paddle_webhook_duration_seconds) > 2 for 5m",
                      severity: .critical, enabled: true, notify: ["PagerDuty", "Slack #incident"], autoRemediation: "ai-alert-triage"),
            AlertRule(id: "rule-queue-backlog", name: "佇列積壓", expression: "queue_depth > 500 for 10m",
                      severity: .warning, enabled: true, notify: ["Slack #ops"], autoRemediation: "ai-autoscale"),
            AlertRule(id: "rule-5xx-ratio", name: "服務 5xx 比例", expression: "rate(http_5xx[5m]) / rate(http_requests[5m]) > 0.02",
                      severity: .critical, enabled: true, notify: ["PagerDuty", "Slack #incident"], autoRemediation: "ai-rollback"),
            AlertRule(id: "rule-canary-errors", name: "Canary 錯誤率偏離基準", expression: "canary_5xx_ratio / stable_5xx_ratio > 2 for 5m",
                      severity: .warning, enabled: true, notify: ["Slack #deploy"], autoRemediation: "ai-rollback"),
            AlertRule(id: "rule-p95-latency", name: "API p95 延遲", expression: "histogram_quantile(0.95, http_request_duration_seconds) > 0.8 for 5m",
                      severity: .warning, enabled: true, notify: ["Slack #ops"], autoRemediation: "ai-alert-triage"),
            AlertRule(id: "rule-pg-connections", name: "資料庫連線數", expression: "pg_stat_activity_count / max_connections > 0.85 for 5m",
                      severity: .warning, enabled: true, notify: ["Slack #ops", "PagerDuty(上班時間)"], autoRemediation: "ai-alert-triage"),
            AlertRule(id: "rule-cpu-saturation", name: "節點 CPU 飽和", expression: "avg by (node) (cpu_usage) > 0.85 for 15m",
                      severity: .warning, enabled: true, notify: ["Slack #ops"], autoRemediation: "ai-autoscale"),
            AlertRule(id: "rule-disk", name: "磁碟使用率", expression: "disk_used_percent > 85",
                      severity: .info, enabled: true, notify: ["Slack #ops"], autoRemediation: nil),
            AlertRule(id: "rule-player-start", name: "影片起播時間", expression: "histogram_quantile(0.95, player_start_seconds) > 2.5 for 10m",
                      severity: .warning, enabled: true, notify: ["Slack #player"], autoRemediation: "ai-alert-triage"),
            AlertRule(id: "rule-error-budget", name: "錯誤預算燃燒速度", expression: "slo_burn_rate{window=\"1h\"} > 14.4",
                      severity: .critical, enabled: true, notify: ["PagerDuty", "Slack #incident"], autoRemediation: nil),
            AlertRule(id: "rule-cert-expiry", name: "TLS 憑證到期", expression: "ssl_cert_expiry_days < 14",
                      severity: .info, enabled: false, notify: ["Email"], autoRemediation: nil),
        ]
    }

    mutating func makeLogs() -> [LogEntry] {
        struct T { let level: LogLevel; let service: String; let hosts: [String]; let msg: String; let weight: Int; let incidentOnly: Bool }
        let gkeAE = ["gke-prod-ae1-a1", "gke-prod-ae1-a2", "gke-prod-ae1-a3"]
        let templates: [T] = [
            T(level: .info, service: "api-server", hosts: gkeAE + ["gke-prod-uc1-a1"], msg: "GET /v1/courses/{id}/progress 200 in {ms}ms", weight: 10, incidentOnly: false),
            T(level: .info, service: "api-server", hosts: gkeAE, msg: "POST /v1/lessons/{id}/events 204 in {ms}ms", weight: 7, incidentOnly: false),
            T(level: .debug, service: "api-server", hosts: gkeAE, msg: "cache hit key=progress:{uid}:{cid}", weight: 4, incidentOnly: false),
            T(level: .error, service: "api-server", hosts: ["gke-prod-ae1-a2"], msg: "POST /v1/enrollments/import 500: csv: bare \" in non-quoted field (line 1, BOM detected) [canary]", weight: 2, incidentOnly: false),
            T(level: .info, service: "course-web", hosts: ["gke-prod-ae1-a1", "gke-prod-uc1-a1"], msg: "SSR /courses/vibe-coding rendered in {ms}ms", weight: 6, incidentOnly: false),
            T(level: .warn, service: "course-web", hosts: ["gke-prod-ae1-a1"], msg: "upstream api-server slow response {ms}ms (/v1/me/summary)", weight: 2, incidentOnly: false),
            T(level: .info, service: "auth", hosts: ["gke-prod-ae1-a2", "gke-prod-ae1-a3"], msg: "login ok provider=google uid={uid}", weight: 5, incidentOnly: false),
            T(level: .warn, service: "auth", hosts: ["gke-prod-ae1-a3"], msg: "rate limit exceeded ip=203.0.113.{n} route=/v1/login", weight: 1, incidentOnly: false),
            T(level: .info, service: "live-player", hosts: ["gke-prod-ae1-a1", "gke-prod-uc1-a1"], msg: "session start lesson=l2-4.{n} first_frame={ms}ms", weight: 6, incidentOnly: false),
            T(level: .warn, service: "live-player", hosts: ["gke-prod-ae1-a1"], msg: "rebuffer event country=TW isp=HiNet duration={ms}ms", weight: 3, incidentOnly: false),
            T(level: .info, service: "notifier", hosts: ["worker-prod-ae1-1"], msg: "email sent template=lesson_reminder to=user:{uid}", weight: 3, incidentOnly: false),
            T(level: .info, service: "worker", hosts: ["worker-prod-ae1-2"], msg: "transcode done video=v_{n}{n} renditions=4 took {ms}ms", weight: 2, incidentOnly: false),
            T(level: .debug, service: "redis", hosts: ["redis-prod-ae1-1"], msg: "keyspace hits=98.7% evicted_keys=0", weight: 1, incidentOnly: false),
            T(level: .info, service: "payments", hosts: ["gke-prod-ae1-a3"], msg: "webhook received event=transaction.completed id=evt_{hex}", weight: 4, incidentOnly: false),
            T(level: .warn, service: "payments", hosts: ["gke-prod-ae1-a3", "gke-prod-uc1-a2"], msg: "webhook handler slow: {ms}ms event=subscription.activated (threshold 2000ms)", weight: 6, incidentOnly: true),
            T(level: .error, service: "payments", hosts: ["gke-prod-ae1-a3"], msg: "webhook handler timeout after 5000ms: context deadline exceeded (tx: UPDATE subscriptions)", weight: 4, incidentOnly: true),
            T(level: .warn, service: "payments", hosts: ["gke-prod-ae1-a3"], msg: "duplicate webhook delivery evt_{hex} (paddle retry #{n})", weight: 3, incidentOnly: true),
            T(level: .error, service: "worker", hosts: ["worker-prod-ae1-1"], msg: "billing job grant_access failed: pq: canceling statement due to lock timeout", weight: 3, incidentOnly: true),
            T(level: .warn, service: "worker", hosts: ["worker-prod-ae1-1"], msg: "queue billing depth={depth} consumers=4 lag={n}m", weight: 3, incidentOnly: true),
            T(level: .warn, service: "postgres", hosts: ["pg-prod-primary"], msg: "process {pid} still waiting for ShareLock on transaction after 1000.112 ms", weight: 3, incidentOnly: true),
            T(level: .error, service: "postgres", hosts: ["pg-prod-primary"], msg: "remaining connection slots are reserved for non-replication superuser connections", weight: 1, incidentOnly: true),
        ]
        let incidentStart = 52.0 // 分鐘前
        var total = 0
        for t in templates { total += t.weight }
        var logs: [LogEntry] = []
        logs.reserveCapacity(150)
        for i in 0..<150 {
            let minutesAgo = 60.0 * Double(149 - i) / 149 + random(0...0.35)
            let inIncident = minutesAgo < incidentStart
            var t = templates[0]
            for _ in 0..<6 {
                var r = int(0...(total - 1))
                for cand in templates {
                    if r < cand.weight { t = cand; break }
                    r -= cand.weight
                }
                if !t.incidentOnly || inIncident { break }
            }
            if t.incidentOnly && !inIncident { t = templates[0] }
            var msg = t.msg
            let ms = t.level == .info || t.level == .debug ? int(18...240) : int(1200...4900)
            msg = msg.replacingOccurrences(of: "{ms}", with: "\(ms)")
            msg = msg.replacingOccurrences(of: "{uid}", with: "\(int(10000...99999))")
            msg = msg.replacingOccurrences(of: "{cid}", with: "c\(int(100...160))")
            msg = msg.replacingOccurrences(of: "{id}", with: "\(int(100...999))")
            msg = msg.replacingOccurrences(of: "{n}", with: "\(int(1...9))")
            msg = msg.replacingOccurrences(of: "{pid}", with: "\(int(20000...40000))")
            msg = msg.replacingOccurrences(of: "{hex}", with: hex(12))
            let depth = Int(80 + (incidentStart - minutesAgo) / incidentStart * 1200)
            msg = msg.replacingOccurrences(of: "{depth}", with: "\(max(0, depth))")
            let host = pick(t.hosts)
            let trace: String? = (t.service == "redis" || t.service == "postgres" || t.level == .debug) ? nil : hex(32)
            logs.append(LogEntry(id: uuid(), date: ago(minutes: minutesAgo), level: t.level, service: t.service,
                                 host: host, message: msg, traceId: trace))
        }
        logs.sort { $0.date > $1.date }
        return logs
    }

    mutating func makeDashboards() -> [Dashboard] {
        let n = 288, step: TimeInterval = 300 // 24 小時,每 5 分鐘一點
        let incidentIdx = n - 11 // 大約 52 分鐘前開始
        return [
            Dashboard(id: "dash-overview", name: "平台總覽", panels: [
                MetricSeries(id: "overview-rps", name: "總請求量", unit: "req/s",
                             points: series(count: n, step: step, base: 2600, amplitude: 1400, noise: 90, min: 300)),
                MetricSeries(id: "overview-error-rate", name: "5xx 比例", unit: "%",
                             points: series(count: n, step: step, base: 0.15, amplitude: 0.04, noise: 0.03, min: 0, spikeFrom: incidentIdx, spike: 0.35)),
                MetricSeries(id: "overview-p95", name: "p95 延遲", unit: "ms",
                             points: series(count: n, step: step, base: 210, amplitude: 40, noise: 12, min: 60, spikeFrom: incidentIdx, spike: 60)),
                MetricSeries(id: "overview-active-learners", name: "線上學員", unit: "人",
                             points: series(count: n, step: step, base: 3200, amplitude: 2100, noise: 120, min: 150)),
            ]),
            Dashboard(id: "dash-api", name: "API 延遲與錯誤", panels: [
                MetricSeries(id: "api-p50", name: "api-server p50", unit: "ms",
                             points: series(count: n, step: step, base: 58, amplitude: 9, noise: 3, min: 20)),
                MetricSeries(id: "api-p95", name: "api-server p95", unit: "ms",
                             points: series(count: n, step: step, base: 205, amplitude: 35, noise: 10, min: 80)),
                MetricSeries(id: "api-error-rate", name: "api-server 5xx", unit: "%",
                             points: series(count: n, step: step, base: 0.18, amplitude: 0.03, noise: 0.02, min: 0, spikeFrom: n - 2, spike: 0.2)),
                MetricSeries(id: "api-rps", name: "api-server 請求量", unit: "req/s",
                             points: series(count: n, step: step, base: 1800, amplitude: 950, noise: 60, min: 200)),
            ]),
            Dashboard(id: "dash-payments", name: "金流 / Paddle webhook", panels: [
                MetricSeries(id: "payments-webhook-p95", name: "webhook 處理 p95", unit: "ms",
                             points: series(count: n, step: step, base: 420, amplitude: 60, noise: 30, min: 120, spikeFrom: incidentIdx, spike: 4300)),
                MetricSeries(id: "payments-queue-depth", name: "billing 佇列深度", unit: "筆",
                             points: series(count: n, step: step, base: 12, amplitude: 6, noise: 4, min: 0, spikeFrom: incidentIdx - 1, spike: 1270)),
                MetricSeries(id: "payments-success-rate", name: "webhook 成功率", unit: "%",
                             points: series(count: n, step: step, base: 99.9, amplitude: 0.03, noise: 0.03, min: 90, max: 100, spikeFrom: incidentIdx, spike: -2.6)),
            ]),
            Dashboard(id: "dash-player", name: "影片播放", panels: [
                MetricSeries(id: "player-start-p95", name: "起播時間 p95(台灣)", unit: "s",
                             points: series(count: n, step: step, base: 1.6, amplitude: 0.3, noise: 0.08, min: 0.5, spikeFrom: n - 39, spike: 1.4)),
                MetricSeries(id: "player-rebuffer", name: "卡頓比例", unit: "%",
                             points: series(count: n, step: step, base: 0.9, amplitude: 0.2, noise: 0.08, min: 0, spikeFrom: n - 39, spike: 0.8)),
                MetricSeries(id: "player-cdn-hit", name: "CDN 快取命中率", unit: "%",
                             points: series(count: n, step: step, base: 95.5, amplitude: 1, noise: 0.3, min: 50, max: 100, spikeFrom: n - 36, spike: -9)),
            ]),
        ]
    }
}

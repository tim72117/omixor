import Foundation

// 人員、服務、伺服器
extension MockDataBuilder {
    func makePeople() -> [Person] {
        [
            Person(id: "tim", name: "蔡庭宇", role: "SRE / 平台負責人", colorHex: 0x5B8DEF),
            Person(id: "mei", name: "林美君", role: "後端 Tech Lead", colorHex: 0xE0719C),
            Person(id: "jason", name: "陳建宏", role: "後端工程師", colorHex: 0x3FB59E),
            Person(id: "yuting", name: "王郁婷", role: "前端工程師", colorHex: 0xF2A541),
            Person(id: "hao", name: "張志豪", role: "SRE", colorHex: 0x8E6CDF),
            Person(id: "kai", name: "李冠廷", role: "全端工程師", colorHex: 0x4AB3D6),
            Person(id: "sharon", name: "黃詩涵", role: "產品經理", colorHex: 0xD96C4F),
        ]
    }

    func makeServices() -> [Service] {
        [
            Service(id: "course-web", name: "course-web", summary: "課程網站前台(React + Vite),課程地圖、會員中心、結帳頁",
                    team: "前端", owner: "yuting", language: "TypeScript", repo: "course-web", health: .healthy,
                    versions: [.production: "v5.21.0", .staging: "v5.22.0-rc.1", .development: "v5.22.0-dev.8"],
                    tier: 1, uptime30d: 99.97, sloTarget: 99.9, latencyP95: 182, errorRate: 0.06, rps: 640,
                    dependencies: ["api-server", "auth", "live-player", "media-cdn"],
                    hostIds: ["gke-prod-ae1-a1", "gke-prod-ae1-a2", "gke-prod-uc1-a1", "gke-stg-ae1-1", "dev-ae1-1"],
                    errorBudgetRemaining: 0.82),
            Service(id: "api-server", name: "api-server", summary: "主要 REST / GraphQL API(Go),課程、進度、會員資料",
                    team: "後端", owner: "mei", language: "Go", repo: "api-server", health: .healthy,
                    versions: [.production: "v2.13.2", .staging: "v2.14.0", .development: "v2.15.0-dev.3"],
                    tier: 1, uptime30d: 99.94, sloTarget: 99.9, latencyP95: 214, errorRate: 0.21, rps: 1830,
                    dependencies: ["auth", "postgres", "redis", "payments", "worker"],
                    hostIds: ["gke-prod-ae1-a1", "gke-prod-ae1-a2", "gke-prod-ae1-a3", "gke-prod-uc1-a1", "gke-prod-uc1-a2", "gke-stg-ae1-1", "dev-ae1-1"],
                    errorBudgetRemaining: 0.61),
            Service(id: "auth", name: "auth", summary: "登入、註冊、Google OAuth、session 與權杖簽發",
                    team: "後端", owner: "jason", language: "Go", repo: "auth", health: .healthy,
                    versions: [.production: "v1.9.4", .staging: "v1.9.5", .development: "v1.10.0-dev.1"],
                    tier: 1, uptime30d: 99.91, sloTarget: 99.95, latencyP95: 96, errorRate: 0.12, rps: 410,
                    dependencies: ["postgres", "redis"],
                    hostIds: ["gke-prod-ae1-a2", "gke-prod-ae1-a3", "gke-prod-uc1-a2", "gke-stg-ae1-1"],
                    errorBudgetRemaining: 0.18),
            Service(id: "payments", name: "payments", summary: "Paddle Billing 訂閱、結帳、webhook 處理與開通權限",
                    team: "後端", owner: "mei", language: "Go", repo: "payments", health: .degraded,
                    versions: [.production: "v3.3.7", .staging: "v3.4.0", .development: "v3.4.0"],
                    tier: 1, uptime30d: 99.82, sloTarget: 99.9, latencyP95: 4820, errorRate: 2.4, rps: 38,
                    dependencies: ["postgres", "worker", "notifier"],
                    hostIds: ["gke-prod-ae1-a3", "gke-prod-uc1-a2", "gke-stg-ae1-1"],
                    errorBudgetRemaining: 0.07),
            Service(id: "live-player", name: "live-player", summary: "互動課程播放器:影片、字幕、課中練習與進度回報",
                    team: "前端", owner: "kai", language: "TypeScript", repo: "live-player", health: .degraded,
                    versions: [.production: "v1.8.0", .staging: "v1.8.2", .development: "v1.9.0-dev.2"],
                    tier: 1, uptime30d: 99.88, sloTarget: 99.9, latencyP95: 1240, errorRate: 0.34, rps: 920,
                    dependencies: ["media-cdn", "api-server"],
                    hostIds: ["gke-prod-ae1-a1", "gke-prod-uc1-a1", "gke-stg-ae1-1"],
                    errorBudgetRemaining: 0.34),
            Service(id: "media-cdn", name: "media-cdn", summary: "影片與教材的 Cloud CDN + Cloud Storage 原點,HLS 切片",
                    team: "平台", owner: "hao", language: "Terraform", repo: "infra", health: .healthy,
                    versions: [.production: "cfg-142", .staging: "cfg-145", .development: "cfg-145"],
                    tier: 1, uptime30d: 99.99, sloTarget: 99.95, latencyP95: 48, errorRate: 0.02, rps: 5400,
                    dependencies: [],
                    hostIds: ["edge-proxy-uc1"],
                    errorBudgetRemaining: 0.93),
            Service(id: "worker", name: "worker", summary: "背景任務:轉檔、寄信排程、Paddle 事件處理、報表",
                    team: "後端", owner: "jason", language: "Go", repo: "api-server", health: .degraded,
                    versions: [.production: "v2.13.2", .staging: "v2.14.0", .development: "v2.15.0-dev.3"],
                    tier: 2, uptime30d: 99.9, sloTarget: 99.5, latencyP95: 3600, errorRate: 1.1, rps: 64,
                    dependencies: ["postgres", "redis", "notifier"],
                    hostIds: ["worker-prod-ae1-1", "worker-prod-ae1-2", "gke-stg-ae1-1"],
                    errorBudgetRemaining: 0.44),
            Service(id: "notifier", name: "notifier", summary: "Email / LINE / App 推播通知,範本與退訂管理",
                    team: "後端", owner: "jason", language: "Go", repo: "api-server", health: .healthy,
                    versions: [.production: "v2.13.2", .staging: "v2.14.0", .development: "v2.15.0-dev.3"],
                    tier: 3, uptime30d: 99.96, sloTarget: 99.5, latencyP95: 320, errorRate: 0.08, rps: 22,
                    dependencies: ["redis"],
                    hostIds: ["worker-prod-ae1-1", "gke-stg-ae1-1"],
                    errorBudgetRemaining: 0.88),
            Service(id: "postgres", name: "postgres", summary: "Cloud SQL 外的自管 PostgreSQL 16:主庫 + 唯讀副本",
                    team: "平台", owner: "tim", language: "PostgreSQL", repo: "infra", health: .degraded,
                    versions: [.production: "16.4", .staging: "16.4", .development: "16.4"],
                    tier: 1, uptime30d: 99.99, sloTarget: 99.95, latencyP95: 12, errorRate: 0.01, rps: 4200,
                    dependencies: [],
                    hostIds: ["pg-prod-primary", "pg-prod-replica", "pg-stg-1"],
                    errorBudgetRemaining: 0.9),
            Service(id: "redis", name: "redis", summary: "Session、快取、rate limit 與任務佇列",
                    team: "平台", owner: "hao", language: "Redis", repo: "infra", health: .healthy,
                    versions: [.production: "7.2.5", .staging: "7.2.5", .development: "7.4.0"],
                    tier: 1, uptime30d: 100, sloTarget: 99.95, latencyP95: 1.8, errorRate: 0, rps: 12800,
                    dependencies: [],
                    hostIds: ["redis-prod-ae1-1", "gke-stg-ae1-1"],
                    errorBudgetRemaining: 1.0),
        ]
    }

    mutating func makeServers() -> [Server] {
        struct Spec {
            let id: String, role: String, region: String, env: DeployEnv, ip: String
            let cores: Int, mem: Int, health: Health, cpu: Double, memUse: Double, disk: Double, net: Double
            let uptime: Int, tags: [String], containers: [(String, String, Health, Int)]
        }
        let ae1 = "asia-east1", uc1 = "us-central1"
        let specs: [Spec] = [
            Spec(id: "gke-prod-ae1-a1", role: "GKE 節點", region: ae1, env: .production, ip: "10.20.0.11", cores: 8, mem: 32,
                 health: .healthy, cpu: 46, memUse: 61, disk: 38, net: 420, uptime: 41, tags: ["gke", "pool-a", "台灣"],
                 containers: [("course-web", "course-web:v5.21.0", .healthy, 0), ("api-server", "api-server:v2.13.2", .healthy, 0),
                              ("live-player", "live-player:v1.8.0", .healthy, 1), ("otel-collector", "otel-collector:0.108", .healthy, 0)]),
            Spec(id: "gke-prod-ae1-a2", role: "GKE 節點", region: ae1, env: .production, ip: "10.20.0.12", cores: 8, mem: 32,
                 health: .healthy, cpu: 52, memUse: 66, disk: 41, net: 465, uptime: 41, tags: ["gke", "pool-a", "台灣"],
                 containers: [("course-web", "course-web:v5.21.0", .healthy, 0), ("api-server", "api-server:v2.14.0", .healthy, 0),
                              ("auth", "auth:v1.9.4", .healthy, 0), ("otel-collector", "otel-collector:0.108", .healthy, 0)]),
            Spec(id: "gke-prod-ae1-a3", role: "GKE 節點", region: ae1, env: .production, ip: "10.20.0.13", cores: 8, mem: 32,
                 health: .degraded, cpu: 78, memUse: 74, disk: 44, net: 380, uptime: 12, tags: ["gke", "pool-a", "台灣", "payments"],
                 containers: [("api-server", "api-server:v2.13.2", .healthy, 0), ("auth", "auth:v1.9.4", .healthy, 0),
                              ("payments", "payments:v3.3.7", .degraded, 4), ("otel-collector", "otel-collector:0.108", .healthy, 0)]),
            Spec(id: "gke-prod-uc1-a1", role: "GKE 節點", region: uc1, env: .production, ip: "10.40.0.11", cores: 8, mem: 32,
                 health: .healthy, cpu: 31, memUse: 52, disk: 35, net: 210, uptime: 63, tags: ["gke", "pool-a", "美國"],
                 containers: [("course-web", "course-web:v5.21.0", .healthy, 0), ("api-server", "api-server:v2.13.2", .healthy, 0),
                              ("live-player", "live-player:v1.8.0", .healthy, 0)]),
            Spec(id: "gke-prod-uc1-a2", role: "GKE 節點", region: uc1, env: .production, ip: "10.40.0.12", cores: 8, mem: 32,
                 health: .healthy, cpu: 28, memUse: 49, disk: 33, net: 190, uptime: 63, tags: ["gke", "pool-a", "美國"],
                 containers: [("api-server", "api-server:v2.13.2", .healthy, 0), ("auth", "auth:v1.9.4", .healthy, 0),
                              ("payments", "payments:v3.3.7", .healthy, 1)]),
            Spec(id: "pg-prod-primary", role: "PostgreSQL 主庫", region: ae1, env: .production, ip: "10.20.1.5", cores: 16, mem: 64,
                 health: .degraded, cpu: 71, memUse: 83, disk: 67, net: 310, uptime: 118, tags: ["database", "primary", "備份:每小時"],
                 containers: [("postgres", "postgres:16.4", .degraded, 0), ("pgbouncer", "pgbouncer:1.23", .healthy, 0),
                              ("postgres-exporter", "postgres-exporter:0.15", .healthy, 0)]),
            Spec(id: "pg-prod-replica", role: "PostgreSQL 唯讀副本", region: ae1, env: .production, ip: "10.20.1.6", cores: 8, mem: 32,
                 health: .healthy, cpu: 34, memUse: 70, disk: 66, net: 140, uptime: 118, tags: ["database", "replica"],
                 containers: [("postgres", "postgres:16.4", .healthy, 0), ("postgres-exporter", "postgres-exporter:0.15", .healthy, 0)]),
            Spec(id: "redis-prod-ae1-1", role: "Redis", region: ae1, env: .production, ip: "10.20.1.20", cores: 4, mem: 16,
                 health: .healthy, cpu: 22, memUse: 58, disk: 21, net: 520, uptime: 96, tags: ["cache", "queue"],
                 containers: [("redis", "redis:7.2.5", .healthy, 0), ("redis-exporter", "redis-exporter:1.62", .healthy, 0)]),
            Spec(id: "worker-prod-ae1-1", role: "背景任務", region: ae1, env: .production, ip: "10.20.2.31", cores: 4, mem: 16,
                 health: .degraded, cpu: 88, memUse: 72, disk: 48, net: 95, uptime: 9, tags: ["worker", "billing-queue"],
                 containers: [("worker", "worker:v2.13.2", .degraded, 2), ("notifier", "notifier:v2.13.2", .healthy, 0)]),
            Spec(id: "worker-prod-ae1-2", role: "背景任務", region: ae1, env: .production, ip: "10.20.2.32", cores: 4, mem: 16,
                 health: .healthy, cpu: 64, memUse: 60, disk: 45, net: 80, uptime: 9, tags: ["worker", "transcode"],
                 containers: [("worker", "worker:v2.13.2", .healthy, 0), ("ffmpeg-sidecar", "ffmpeg:7.0", .healthy, 0)]),
            Spec(id: "edge-proxy-uc1", role: "邊緣代理 / CDN 原點", region: uc1, env: .production, ip: "10.40.3.2", cores: 4, mem: 8,
                 health: .healthy, cpu: 19, memUse: 41, disk: 29, net: 880, uptime: 152, tags: ["edge", "nginx", "cdn-origin"],
                 containers: [("nginx", "nginx:1.27", .healthy, 0), ("cloudflared", "cloudflared:2024.10", .healthy, 0)]),
            Spec(id: "gke-stg-ae1-1", role: "GKE 節點(Staging)", region: ae1, env: .staging, ip: "10.21.0.11", cores: 4, mem: 16,
                 health: .healthy, cpu: 24, memUse: 57, disk: 52, net: 40, uptime: 6, tags: ["gke", "staging", "spot"],
                 containers: [("course-web", "course-web:v5.22.0-rc.1", .healthy, 0), ("api-server", "api-server:v2.14.0", .healthy, 0),
                              ("payments", "payments:v3.4.0", .healthy, 0), ("live-player", "live-player:v1.8.2", .healthy, 0),
                              ("worker", "worker:v2.14.0", .healthy, 0), ("redis", "redis:7.2.5", .healthy, 0)]),
            Spec(id: "pg-stg-1", role: "PostgreSQL(Staging)", region: ae1, env: .staging, ip: "10.21.1.5", cores: 4, mem: 16,
                 health: .healthy, cpu: 12, memUse: 46, disk: 58, net: 12, uptime: 40, tags: ["database", "staging"],
                 containers: [("postgres", "postgres:16.4", .healthy, 0)]),
            Spec(id: "dev-ae1-1", role: "開發環境", region: ae1, env: .development, ip: "10.22.0.11", cores: 4, mem: 16,
                 health: .healthy, cpu: 15, memUse: 63, disk: 86, net: 8, uptime: 22, tags: ["dev", "preview"],
                 containers: [("course-web", "course-web:v5.22.0-dev.8", .healthy, 0), ("api-server", "api-server:v2.15.0-dev.3", .healthy, 3),
                              ("postgres", "postgres:16.4", .healthy, 0)]),
            Spec(id: "ci-runner-ae1-1", role: "CI Runner", region: ae1, env: .development, ip: "10.22.5.3", cores: 16, mem: 32,
                 health: .healthy, cpu: 67, memUse: 55, disk: 71, net: 150, uptime: 3, tags: ["ci", "self-hosted", "github-actions"],
                 containers: [("actions-runner", "actions-runner:2.320", .healthy, 0), ("buildkitd", "buildkit:0.16", .healthy, 0)]),
        ]

        var servers: [Server] = []
        for spec in specs {
            let cpuSeries = series(count: 60, step: 60, base: spec.cpu, amplitude: 0, noise: spec.cpu * 0.06, min: 1, max: 99)
            let memSeries = series(count: 60, step: 60, base: spec.memUse, amplitude: 0, noise: 1.0, min: 5, max: 98)
            var containers: [Container] = []
            for (k, c) in spec.containers.enumerated() {
                containers.append(Container(id: "\(spec.id)/\(c.0)-\(k)", name: c.0, image: "asia-docker.pkg.dev/coursely/\(c.1)",
                                            status: c.2, restarts: c.3,
                                            cpu: (random(0.05...0.6) * Double(spec.cores) * 10).rounded() / 10,
                                            memoryMB: (random(180...2400)).rounded()))
            }
            servers.append(Server(id: spec.id, name: spec.id, role: spec.role, provider: "GCP", region: spec.region, env: spec.env,
                                  os: spec.tags.contains("gke") ? "Container-Optimized OS 117" : "Ubuntu 24.04 LTS",
                                  ip: spec.ip, cores: spec.cores, memoryGB: spec.mem, health: spec.health,
                                  cpu: cpuSeries.last?.value ?? spec.cpu, memory: memSeries.last?.value ?? spec.memUse,
                                  disk: spec.disk, networkMbps: spec.net, cpuSeries: cpuSeries, memorySeries: memSeries,
                                  uptimeDays: spec.uptime, containers: containers, tags: spec.tags))
        }
        return servers
    }
}

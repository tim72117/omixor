# omixor-core

omixor 的核心後端模組(Go)。目前有:

| 套件 | 內容 |
|---|---|
| `model` | 共用的資料型別(Issue、組織、成員、使用者、token、操作紀錄),JSON 格式和 app、omixor-web 相同 |
| `store` | 存放介面,實作有 PostgreSQL(`store.NewPG`)與記憶體(`store.NewMem`,測試用) |
| `service` | 商業邏輯與權限:授權(用 evoleap 證明身分,發 omixor 自己的 token)、組織與成員、Issue |
| `api` | HTTP 路由(`api.New(svc).Handler()`) |
| `client` | 連線上 omixor server 的用戶端,提供和 `service` 一樣的 Issue/組織操作 |
| (已移到 `../server`) | 線上服務 omixor-server(Cloud Run),引用本模組 |
| `cmd/omixor` | 管理 CLI |

## 架構

- **核心**(本模組):授權、組織、Issue 等後端模組。
- **web**(`omixor-web`):自己做網頁與介面,編譯時引入核心。啟動時二選一:
  - 內建:`OPS_DATABASE_URL` 連 PostgreSQL,核心在同一個程式裡跑
  - 遠端:`OMIXOR_SERVER` 指向線上 omixor server,授權、組織、Issue 都交給它
- **app**(`ops`,Swift):介面殼層與資料快取,重要資料從 omixor server 取得。

## 權限

- 伺服器管理員(`users.role = admin`):管使用者、token、操作紀錄,建立組織,看得到所有組織
- 組織管理員(`org_members.role = admin`):管自己組織的成員
- 組織成員:讀寫自己組織的 Issue;作者一律是自己(不能冒名)

## 組織

組織的所有資訊都存在核心(`omixor_orgs`):`id`(建立後不能改)、`name`、`symbol`(圖示名稱)、`color`(顏色)、`note`(備註)、`createdAt`;列出「我的組織」時另外帶 `myRole`。圖示、顏色、備註沒設是空字串,怎麼顯示由介面決定。

- `POST /api/orgs`:`{id, name, symbol?, color?, note?}` 建立(伺服器管理員)
- `PUT /api/orgs/{org}`:`{name, symbol?, color?, note?}` 修改(伺服器管理員;name 不能空白)
- 刪除組織只有 `service.DeleteOrg`(連成員與 Issue 一起刪,不能復原),沒有 HTTP 路由

舊的 PostgreSQL 資料庫啟動時會自動補上 `symbol`、`color`、`note` 三欄(`alter table … add column if not exists`)。

## MCP 端點

核心(`mcp` 套件)提供三種 MCP 端點,工具白名單與認證都不同;雲端 server 與本機 web 只負責掛路由與認證。

| | 管理 `/mcp` | 角色 `/mcp/agent` | 執行者 `/mcp/worker` |
|---|---|---|---|
| 給誰 | 人或管理用的 LLM | 領了任務、以虛擬角色工作的 LLM | 領取任務的 LLM(找任務、領取) |
| 認證 | 雲端 `/mcp/org/{org}`:omixor token;本機 `/mcp`:綁 127.0.0.1 | `Authorization: Bearer <agentId>:<secret>`(領取任務發的憑證,綁一筆任務) | 同管理端點:雲端 `/mcp/worker/{org}` 用 omixor token;本機 `/mcp/worker` 綁 127.0.0.1 |
| 身分 | 呼叫者本人 | 憑證的角色(署名一律是角色) | 呼叫者本人(組織成員) |
| 工具 | 組織、Issue、角色(含新增修改)、派任務、領取、回報… | `ops_agent_context`、`ops_agent_tasks`、`ops_agent_task_report`、Issue 讀/建/留言(web 另有文件櫃) | 只有 `dispatch_queue`、`dispatch_claim`、`dispatch_status`、`dispatch_release` |
| 遠端模式(web) | 轉給雲端 | 一律 401(憑證在雲端,要連雲端的 `/mcp/agent`) | 轉給雲端(`credential.endpoint` 指向雲端 `/mcp/agent`) |

執行者的標準流程:

1. `dispatch_queue`:看有哪些任務可領(全部啟用角色的待領取、需要協助、我領取中的),依優先度排序
2. `dispatch_claim`:領取(給 `id` 領那一筆;不給就自動挑最優先的,可用 `agent` 限定角色)→ 回傳任務、角色設定、憑證(`credential`:`agentId`、`secret`、`endpoint`、`expiresAt`)與 `nextSteps`
3. 用憑證連 `credential.endpoint`(角色端點),HTTP 標頭 `Authorization: Bearer <agentId>:<secret>`,以該角色身分工作(`ops_agent_context` 讀設定與任務)
4. 定期(至少每小時)`ops_agent_task_report` `status: inProgress` 續約(領取即鎖定,逾時會被自動退回待領取)
5. 做完 `status: done` 並寫 `result`;需要人決定 `status: blocked` 並留言提問。`done`/`blocked` 後憑證立刻失效
6. 中途放棄:`dispatch_release`(領取者本人或組織管理員),任務退回待領取;`dispatch_status` 可查自己領取中的任務與鎖定剩餘時間

相關 API:`POST /api/orgs/{org}/agent-tasks/claim-next`(`{agentId?}`)、`POST /api/orgs/{org}/agent-tasks/{id}/release`(`{reason?}`)。

## 測試

```bash
go test ./...
```

# 從 Swift 版搬到 Go + React 的規則

Swift 版在 `../ops`(macOS SwiftUI app)。Go 版的 **MCP 規格必須和 Swift 版一模一樣**:同樣的工具、參數、輸出欄位、錯誤訊息、預設值、副作用(含寫進動態牆的文字)。畫面盡量照 Swift 版的版面與文字。

## 架構

```
spec/                 從 Swift 版匯出的標準答案(不要手改):tools.json、initialize.json
../templates/         範本資料(文件櫃結構、示範資料、文件轉換器),預設不載入,OMIXOR_TEMPLATES=1 才讀
internal/core/        狀態與功能邏輯(package core,一個功能一個檔案)
internal/mcp/         MCP 協定與工具(package mcp,一個功能一個 tools_<功能>.go)
internal/web/         給畫面的 HTTP API
cmd/omixor-web/          主程式:MCP 127.0.0.1:3765/mcp + 畫面 127.0.0.1:5191
cmd/omixor-mcp/          stdio 轉接(同 Swift 版 OpsMCP)
web/                  React + Vite(TypeScript),web/src/sections/<頁面>/ 一頁一個資料夾
conformance/          一致性比對工具
```

## 核心約定(internal/core)

- `App.Mu` 是全域鎖(對應 Swift 的 `@MainActor`)。讀寫狀態要拿鎖;**發網路請求或跑外部程式時不能拿著鎖**:鎖內讀設定 → 放鎖 → 發請求 → 拿鎖寫結果。
- 改完資料呼叫 `a.Changed("<主題>")`(不用拿鎖)。**主題 = 畫面讀的 view 名稱**,畫面收到同名主題就重讀。
- 範例資料(Swift 的 MockData)在 `a.C["<集合>"]`(`[]map[string]any`)與 `a.Sprint`,JSON 格式和 Swift 的 MCPJSON 輸出一樣。時間已經平移到現在。
- 每個組織的資料在 `a.DataDir()`。用 `core.OnOrgLoad` 在啟動與切換組織時重新讀檔,`core.OnOrgCreate` 寫新組織的初始檔(template `blank` / `copyCurrent`),`core.OnOrgDelete` 清掉該組織的東西。檔案格式要和 Swift 版一樣(Swift 版能讀 Go 版寫的檔案,反過來也行)。
- 動態牆:`a.Log(actor, icon, text, section)`(拿著鎖呼叫),文字和 Swift 版一字不差。
- 顯示名稱:`a.Name(actor)`;功能可以用 `core.OnAIName` 登記 ai id → 名稱(例如虛擬角色)。
- 時間欄位用 `core.Time`(編成 `2026-11-11T00:15:28Z`,和 Swift 的 .iso8601 一樣,沒有小數秒)。JSON 用 `core.Marshal`(key 排序、不跳脫 / 與中文)。
- struct 要和 Swift 的 Codable 欄位名一致;Swift 有寬鬆解碼(缺欄位用預設值)的地方,Go 也要寬鬆。
- 計時器(例行任務、每 5 分鐘更新…)用 `core.OnStart` 啟動的 goroutine;測試時不要啟動(看 `OPS_NO_TIMERS=1`)。
- 側邊欄徽章:`core.OnBadge(func(a) map[string]int)`,key 是頁面 id(overview、development、pipelines、infrastructure、monitoring、incidents、automation、cabinet、settings)。
- 鑰匙圈:token、client secret 存 macOS 鑰匙圈,服務名稱 `com.tim72117.ops-web.connection`(和 Swift 版分開),帳號名稱規則同 Swift(`<組織前綴><連線 id>:token`)。用 `/usr/bin/security`,**密碼不能放在指令參數裡**(其他程式看得到),要用 `security -i` 從 stdin 送。
- 呼叫外部服務(同 Swift 的 `ConnectionStore.request`):`core.ConnRequest(a, "<連線 id>", "GET"/"PUT"…, "/api/...", body []byte) (any, error)`,帶該連線的認證(token 快過期自動換新、401 換 token 重試一次),回傳解析後的 JSON;不是 2xx 時回傳 `core.ConnError{Status, Message}`(`Error()` 等於 Swift 的 localizedDescription)。要原始回應用 `core.ConnRequestRaw`。連線可以有 `caCert`(CA 憑證 PEM 路徑)與 `insecure`(不驗證憑證)兩個可省略欄位(Go 版才有,Swift 讀檔時忽略),ConnRequest 會自動套用。**呼叫時不能拿著 a.Mu**。其他:`core.ConnFind(a, id)`(拿著鎖)找連線、`core.ConnHasToken(a, c)`(不能拿著鎖)判斷有沒有登入、`core.EscapePath`、`core.FormEncode` 同 Swift 的 ConnHTTP。測試時 `core.ConnTransport` 換成假的 RoundTripper,`core.Vault = core.NewMemoryVault()`(跑 go test 時預設就是記憶體版、且拒絕連外)。

## MCP 工具(internal/mcp)

- `mcp.Register("ops_xxx", func(c *mcp.Call) (any, error))`,在自己的 `tools_<功能>.go` 的 `init()` 登記。
- 參數用 `c.Args`(`String`、`Str`、`Int`、`Bool`、`Strings`、`Require`、`RequireInt`、`RequireEnum`、`OptionalEnum`),行為同 Swift 的 MCPArgs。
- 失敗:`return nil, mcp.ToolError{"訊息"}` 或 `mcp.Errorf(...)`,訊息和 Swift 版一字不差。
- 執行者:`c.Actor`(MCP 呼叫是 `core.MCPActor`;畫面用 /api/tool 呼叫時是目前使用者)。Swift 版寫死 `.mcp` 的地方用 `c.Actor`。
- 回傳值會用 `core.Marshal` 轉成文字。數字:Swift 的 Int 輸出整數、Double 輸出小數,Go 用對應型別。
- 參照 Swift 的 `app/Sources/Omixor/MCP/MCPHandler.swift`,逐行對照搬。

## 畫面 API(internal/web)

- `core.RegisterView("<名稱>", func(a) any)`:GET /api/view/<名稱>(呼叫時已拿鎖,不能發網路請求)。
- `core.RegisterAction("<名稱>", func(a, args) (any, error))`:POST /api/action/<名稱>,給只有人能在畫面上做的事(MCP 沒有工具的)。呼叫時沒拿鎖。會花時間的(登入、讀 GitHub)可以直接等結果回傳。
- MCP 有的動作,畫面直接呼叫 POST /api/tool/<工具名稱>。

### View 與 Action 一覽(前端與後端的約定)

View 的資料格式 = Swift 模型經 MCPJSON 編出來的 JSON(欄位名看 Swift 原始碼)。

| View | 內容 | 負責 |
|---|---|---|
| `people` `services` `servers` `repos` `pull_requests` `work_items` `pipeline_runs` `deployments` `alerts` `alert_rules` `logs` `dashboards` `incidents` `on_call` `automations` `automation_runs` `approvals` `activity` `integrations` | 範例資料集合(已有) | core |
| `sprint` `nav` `mcp` `me` `orgs` `badges` | (已有) | core |
| `issues` | `[OpsIssue]` | Issue/角色/文件櫃 |
| `agents` | `{"agents":[Agent],"tasks":[AgentTask]}` | Issue/角色/文件櫃 |
| `cabinet` | `{"layout":[CabinetSection],"docs":[CabinetDoc]}` | Issue/角色/文件櫃 |
| `connections` | `{"connections":[Connection],"states":{"<id>":{"account","busy","error","lastTest"}}}` | 連線 |
| `github` | `{"settings":GHSettings,"snapshot":{"repos","pulls","runs","fetchedAt","busy","errors","error"},"connection":Connection或null,"loggedIn":bool,"state":ConnectionState或null}` | 連線 |
| `gcp` | `{"settings":GCPSettings,"snapshot":{"services","jobs","fetchedAt","busy","error"},"connection","loggedIn","configured","state"}` | 連線 |
| `gitlab` | `{"settings":{"projects","connection"},"snapshot":{"projects","mrs","pipelines","fetchedAt","busy","errors","error"},"connection","loggedIn","configured","state","baseURL","caCert","insecure","callback","scopes","applicationsURL","tokenURL"}`(Go 版才有,Swift 沒有、也沒有 MCP 工具;元件 web/src/gitlab/GitLabTab.tsx) | 連線 |
| `voice_jobs` | `[VoiceJob]`(完整內容,含 baseline;最新的在最前面) | 旁白 |
| `voice_settings` | `{connection, pollMinutes, shuttlePath, target: {id,name,baseURL,production} 或 null, hasToken}`(發佈目的地與輪詢設定,Swift 存在 UserDefaults;Go 存在資料夾根目錄的 voice-settings.json) | 旁白 |
| `facebook` | `{settings: {apiVersion, mode, pages: [FBPage]}, drafts: [草稿,格式同 ops_fb_drafts 的一筆], snapshot: {posts: {pageId: [貼文]}, scheduled: {pageId: [貼文]}, insights: {pageId: {pageId,period,since,until,metrics}}, errors: {pageId: 訊息}, error, busy, fetchedAt, tokenExpired}, tokens: {pageId: bool}, connected, notConnected, connection: Connection 或 null, hasAppSecret, callback, scopes, periods, minLeadSeconds, maxLeadSeconds}`。貼文 / 指標格式同 ops_fb_posts / ops_fb_insights。設定與草稿存組織資料夾的 facebook.json(格式同 Swift);專頁權杖存 core.Vault,帳號 `<組織前綴>facebook:page:<id>`,Meta 登入的使用者權杖是連線 `facebook` 的 `:token`。tokens 只看鑰匙圈快取 | 粉絲專頁 |

| Action | 參數 → 回傳 | 負責 |
|---|---|---|
| `navigate` | `{section,focusId,env,timeRange}` → Nav | core(已有) |
| `org_create` `org_update` `org_delete` `org_switch` | 見 core/orgs.go | core(已有) |
| `approval_decide` | `{id, approve}` | 範例資料 |
| `deployment_approve` `deployment_rollback` | `{id}` | 範例資料 |
| `automation_autonomy` `automation_toggle` | `{id, autonomy}` / `{id}` | 範例資料 |
| `integration_set` | `{id, connected}` | 範例資料 |
| `incident_note` | `{id, text}`(畫面加註記,人署名) | 範例資料 |
| `assistant_ask` | `{section?, question}` → `{text, actions: [AssistAction], question, section}`。section 沒給用目前頁面;question 去頭尾空白後是空的回錯誤「缺少參數 question」。text 用 `**粗體**` 與 `• ` 開頭的行。AssistAction 共通欄位 `{kind, key, label, icon, prominent}`,依 kind 另有:`acknowledge {alertId, title}`(→ 確認告警)、`approve`/`reject` `{approvalId}`(→ approval_decide)、`retry {runId}`(→ 重新執行 CI)、`go {section, focus 或 null}`(→ navigate)、`copy {text}`(複製到剪貼簿)。`key` 同 Swift 的 id,按過就停用;核准與拒絕是一組,按了一個兩個都停用 | 旁白 |
| `assistant_briefing` | `{section?}` → `{headline, tone, lines: [{icon, color, text}], suggestions: [string], focusLabel 或 null, section, sectionTitle}`。tone / color 是 Swift 的顏色名稱:green、orange、red、blue、purple、indigo、teal、gray、secondary;icon 是 SF Symbol 名稱 | 旁白 |
| `issue_delete` | `{id}` | Issue/角色/文件櫃 |
| `agent_upsert` `agent_delete` | Agent / `{id}` | Issue/角色/文件櫃 |
| `agent_task_cancel` `agent_task_requeue` | `{id}` | Issue/角色/文件櫃 |
| `agent_routine_run` | `{agent, routine}`(「現在排一筆」) | Issue/角色/文件櫃 |
| `agent_prompt` | `{agent, task?}` → `{text}`(複製給 Claude Code 的那段話) | Issue/角色/文件櫃 |
| `cabinet_layout_save` | `{layout}` | Issue/角色/文件櫃 |
| `cabinet_doc_delete` | `{id}` | Issue/角色/文件櫃 |
| `connection_upsert` `connection_delete` | Connection / `{id}` | 連線 |
| `connection_login` `connection_logout` `connection_test` `connection_discover` | `{id}`(login 會開瀏覽器、等回呼,完成才回傳) | 連線 |
| `connection_secret` `connection_bearer` | `{id, secret}` / `{id, token}` | 連線 |
| `github_ensure_connection` `github_add_repo` `github_remove_repo` `github_refresh` | `{}` / `{repo}` | 連線 |
| `github_jobs` `github_job_log` `github_pull` | `{repo,run}` / `{repo,job}` / `{repo,number}` → 資料 | 連線 |
| `gcp_ensure_connection` `gcp_settings` `gcp_refresh` | `{}` / GCPSettings 欄位 | 連線 |
| `gcp_revisions` `gcp_executions` `gcp_logs` `gcp_metrics` | `{service}` / `{job}` / `{service?,job?,severity,hours,contains?,limit}` / `{service,hours}` → 資料 | 連線 |
| `gitlab_ensure_connection` `gitlab_settings` `gitlab_add_project` `gitlab_remove_project` `gitlab_refresh` | `{baseURL?}` / `{baseURL?, caCert?, insecure?}` / `{project}` / `{project}` / `{}` | 連線 |
| `gitlab_jobs` `gitlab_job_log` `gitlab_mr` | `{project,pipeline}` → `{jobs,stages}` / `{project,job,lines?}` → `{tail}` / `{project,iid}` → MR 詳情 | 連線 |
| `site_visitors` | `{range, connection?}` → SiteOverview(網站訪客頁) | 連線 |
| `voice_publish` `voice_cancel` `voice_retry` `voice_discard` | `{id}` → VoiceJob(discard 回 `{}`)。publish 只能由人按,等全部送完才回傳(state 是 ready 才會動;結果寫在 job 的 log 與 changes[].publish);cancel 只對 queued / failed;retry = Swift 的「立即檢查」(processing 時顯示);discard 從清單移除已結束的批次(Go 版才有)。找不到 id 回「找不到旁白批次 <id>」 | 旁白 |
| `voice_settings` | `{connection?, pollMinutes?(1–120), shuttlePath?}` → 同 View voice_settings | 旁白 |
| `facebook_ensure_connection` `facebook_app` | `{}` → Connection / `{appId, secret?}` → View facebook(Meta App 的 App ID 與 App secret;appId 空白沿用原本的) | 粉絲專頁 |
| `facebook_login` `facebook_token` `facebook_disconnect` | `{}`(Meta 登入:開瀏覽器、等回呼,完成才回傳)/ `{token}`(貼 Page access token 或使用者權杖)/ `{}`(清掉所有權杖,專頁與草稿保留)→ View facebook;動態牆以登入者署名 | 粉絲專頁 |
| `facebook_managed` `facebook_refresh` `facebook_insights` | `{pageId, managed}` → View / `{}`(重讀管理中專頁的資料,讀完才回傳)→ View / `{pageId, period?, days?}` → `{pageId,period,since,until,metrics}` | 粉絲專頁 |
| `facebook_draft_new` `facebook_draft_update` `facebook_draft_delete` | `{}` → 草稿(登入者署名,專頁預設第一個管理中的)/ `{id, pageId?, message?, link?, imageURL?, localImage?, proposedTime?(ISO,空字串清掉), source?}` → 草稿(只能改 status=draft 的,否則「草稿 FD-n 已排程,不能修改」)/ `{id}` → `{}` | 粉絲專頁 |
| `facebook_publish` `facebook_cancel_scheduled` | `{id, schedule?(ISO 8601)}` → `{postId, draft, message}`(立即發佈或排程,排程要在 10 分鐘到 30 天後;以登入者署名,等送完才回傳)/ `{postId, pageId}` → View。只有人能按,MCP 沒有發佈工具。Swift 的 Touch ID 確認在 Go 是 `core.FBConfirmOwner`(預設通過:畫面登入就是身分確認) | 粉絲專頁 |

## 登入與署名

- 畫面可以啟用 LDAP 登入(internal/auth、internal/web/auth.go)。MCP 不經過登入。
- **Action 要以登入者署名**:後端把登入者 id 放在 `args[core.UserKey]`。拿鎖後用 `a.As(core.UserActor(a, args), func(){ … })` 包住要署名的動作,裡面用 `a.Actor()` 取代 `Person(a.Me)`。不拿鎖的長動作(例如發佈)直接把 `core.UserActor(a, args)` 傳下去。
- MCP 工具用 `c.Actor`(畫面透過 /api/tool 呼叫時就是登入者)。

## 一致性比對(conformance/)

每個人用自己的連接埠,互不干擾:

```bash
conformance/run_swift.sh <埠A>            # 全新暫存資料夾的 Swift 版
conformance/run_go.sh <埠B> <畫面埠>       # 全新暫存資料夾的 Go 版(會重新編譯)
python3 conformance/compare.py conformance/scenarios/<功能>.json --swift http://127.0.0.1:<埠A>/mcp --go http://127.0.0.1:<埠B>/mcp
conformance/stop.sh <埠A> <埠B> <畫面埠>
```

改了 Go 程式要重跑 `run_go.sh`(先 stop 再 run),而且 Swift 那邊也要重開(情境會改資料,兩邊要從同樣的起點開始)。
情境檔寫在 `conformance/scenarios/<功能>.json`,涵蓋每個工具的正常路徑、每個錯誤訊息、邊界參數。會隨時間變動的欄位(例如伺服器 CPU)用 `ignore`。

**安全**:Swift 比對實例用的是正式的鑰匙圈,所以**不要**在比對情境裡呼叫會連外部服務的工具(`ops_site_visitors`、真的送出的 `ops_voice_submit`、登入後的 GitHub/GCP)。這些用 Go 的單元測試(httptest 假伺服器)驗證。

## 不能做的事

- 不能直連任何資料庫;不能改 `../ops`(Swift 版)與 `../../shuttle`(evoleap)的檔案。
- 不能呼叫會改變外部服務的 API(部署、發佈、送出旁白批次、GitHub 寫入…)。
- 不能讀或印出鑰匙圈裡的 token。
- 不要 git commit(由主持的人統一提交)。
- 跑測試一律設 `OPS_DATA_DIR` 為暫存資料夾。

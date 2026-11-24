# 2026-11 版本說明

> 這份文件由多位作者分章節加入,新增內容請加在最後並用清楚的章節標題,不要改別人的章節。

## API 行為變更:錯誤一律使用正確的 HTTP 狀態碼

內容格式不變:失敗時回應仍是 `{"error": "訊息", ...其他欄位}`(例如版本衝突的 `current`、`currentVersion`,刪角色的 `taskCounts`)。
前端不用改(`console/ui/src/api.ts` 的 `post()`、`getJSON()` 不論狀態碼都會讀內容,丟出帶 `status` 與 `data` 的 `ApiError`)。
外部腳本如果依賴「這些端點失敗也一定是 200」,需要調整成看狀態碼。

### console 網頁版:原本失敗也回 200 的端點

| 端點 | 情境 | 以前 | 現在 |
|---|---|---|---|
| `POST /api/auth/login` | 帳號不存在、密碼錯誤(同一個 401、同一句「帳號或密碼錯誤」,不洩漏帳號存不存在)、沒有啟用 LDAP、不在允許的群組 | 200 | 401 |
| `POST /api/auth/login` | 登入失敗太多次被鎖(同一帳號同一來源 5 分鐘內 5 次) | 200(已經改成 401 的版本也是 401) | **429**,帶 `Retry-After`(秒)與 `retryAfter` 欄位 |
| `POST /api/auth/login` | LDAP 連不上、服務帳號錯誤、搜尋使用者失敗等診斷類失敗 | 200(已經改成 401 的版本是 401) | **503**(固定句子,位址與原因只進 log) |
| `POST /api/auth/login` | 存 session 失敗(資料庫) | 200 | **500**(固定句子,細節只進 log;沒存起來的 session 不保留) |
| `POST /api/auth/test`(管理員診斷) | 設定驗證不過、測試帳密不對 | 200 | 400 |
| `POST /api/auth/test` | 連不上 LDAP(拒絕連線、DNS 失敗…) | 200 | 502 |
| `POST /api/auth/test` | 連 LDAP 逾時 | 200 | 504 |
| `POST /api/auth/test` | 其他內部錯誤 | 200 | 500 |
| `POST /api/auth/config` | 測試帳號登入失敗(設定沒存)、測試帳號不在管理員群組 | 200 | 400 |
| `POST /api/auth/config` | 測試時連不上 LDAP / 逾時 | 200 | 502 / 504 |
| `POST /api/auth/config` | 存設定失敗:設定內容有問題 / 內部錯誤 | 200 | 400 / 500 |
| `POST /api/tool/{name}` | 工具不存在 | 400 | **404**(與不存在的動作一致) |
| `POST /api/tool/{name}` | 給人看的說明(`ToolError`、`UserError`,沒有指定狀態碼) | 200 | 400 |
| `POST /api/tool/{name}` | `service.Error`(例如 403、404、409) | 200 | 它自己的 Code |
| `POST /api/tool/{name}` | 內部錯誤(資料庫、逾時、路徑…) | 200 | 500(訊息換成通用句子) |
| `POST /api/action/{name}` | 動作不存在 | 500 | **404** |
| `POST /api/action/{name}` | `UserError`(含被 `%w` 包過的) | 200 | 400 |
| `POST /api/action/{name}` | `StatusError`、`service.Error` | 它自己的狀態碼 | 不變 |
| `POST /api/action/{name}` | 內部錯誤 | 200 | 500 |
| `POST /api/attachments` | 空檔案、不是 PNG / JPEG / GIF / WebP | 200 | 400 |
| `POST /api/attachments` | 超過 10 MB | 200 | 413 |
| `POST /api/attachments` | 資料庫錯誤 | 200 | 500 |

不變的例外(刻意回 200):

- MCP 的 JSON-RPC 協定層(`/mcp` 端點)。協定規格要求工具失敗以 `isError: true` 放在回應內容裡、HTTP 回 200。
- `POST /local/orgs/{org}/import` 這類「每一類資料各自成功或失敗」的彙總結果(`ImportSection.Status = "error"`)。整體 200,各類的結果看內容。匯入紀錄寫失敗時仍是 500。

### 核心的資料層錯誤對照(所有出口一致:core API、console 網頁、本機管理端點、MCP)

| 錯誤 | 以前 | 現在 |
|---|---|---|
| `store.ErrNotFound`(找不到) | 500 | 404 |
| `store.ErrVersionConflict`(版本衝突;`*store.VersionConflictError` 帶 `currentVersion`) | 500 | 409,回應多一個 `currentVersion` |
| `store.ErrLocked`(任務已被別人鎖住) | 500 | 409 |
| `store.ErrProjectNoKey`(專案還沒設定代號) | 500 | 400 |
| `store.ErrInUse` | 409 | 不變 |
| `store.ErrBadRef` | 400 | 不變 |

`service.Code` 與 `core/httperr` 的對照由測試(`TestCodeMatchesService`)保持一致。
`Public` 型別(`client.Error`、`ToolError`、`UserError` 等)沒有指定狀態碼時,core API 用 500,console 的動作與工具出口用 400。

## 錯誤訊息的遮蔽

資料庫錯誤、逾時、檔案路徑、內網位址等內部細節不再回給呼叫端(包括未登入就能呼叫的 `POST /api/auth/login` 與 MCP 工具結果),
對外只有固定的通用訊息,原文連同操作名稱寫進 log(去除換行、同一種錯誤每 10 秒最多 5 行)。
刻意給人看的錯誤(GitHub、GitLab、GCP、Facebook 的連線錯誤、驗證說明、`service.Error`)照給。
管理員診斷(測試 LDAP、存 LDAP 設定)仍看得到連線失敗的原因。

## 2026-11-20 安全審查後的變更

### console MCP 連接埠

| 項目 | 變更 |
|---|---|
| 限流語意 | 失敗次數的限制**只拖慢錯誤的嘗試**:先驗證權杖,正確的請求永遠放行(不看失敗計數、不會 429);權杖錯誤或沒帶時,同一來源 1 分鐘內超過 10 次回 429(帶 `Retry-After: 60`),否則 401。以前是「被擋的來源連正確權杖也 429」,別人灌錯誤嘗試就能把本人擋掉。 |
| 失敗計數表 | 以前追蹤的來源超過 1000 個就整張清空;現在固定上限 1000 個來源,滿了先清過期的、再淘汰最舊的,灌來源洗不掉別人的紀錄。 |
| Host 白名單 | 整個 MCP 連接埠(含 `/mcp/agent`)先檢查 `Host` 標頭:只放行 `localhost`、`127.0.0.1`、`[::1]`(可含連接埠);`OMIXOR_MCP_ADDR` 設了非本機位址(不含 `0.0.0.0`、`::`)時,再放行該位址。不符回 **403**,而且不計驗證失敗。(原本只擋帶 Origin 的瀏覽器請求,DNS rebinding 時 Host 是攻擊者的網域。)用 Docker 發布到主機 `127.0.0.1` 的連線,Host 是 `localhost` 或 `127.0.0.1`,不受影響;若從別台電腦用主機名稱連,要把它設成 `OMIXOR_MCP_ADDR` 或改用 SSH 轉發。 |
| 環境變數 `OMIXOR_CONSOLE_MCP_TOKEN` | 最短長度由 16 提高到 **32 字元**。有設但無效(太短、含空白、換行或控制字元,或超過 512 字元)時,**console 與 omixor-mcp 啟動失敗並說明原因**(以前是悄悄忽略、改用 console 自己保存的權杖,使用者以為在用自己給的那把)。讀取時會先去掉結尾的換行與空白(常見於 `echo` 產生的檔案內容)。沒設仍是 console 自己產生並保存(產生的權杖是 `omxm_` 加 43 字元,不受影響)。已經用 16 到 31 字元環境變數權杖的部署,升級前要換成至少 32 字元。 |
| `omixor-mcp --channel` | 執行中讀不到權杖(例如 console 資料夾被清掉)時,這一輪**不送請求**(以前會送出沒有 `Authorization` 的請求),回錯誤、等下一輪重讀,權杖回來後自動恢復。 |
| Windows 檔案權限 | `omixor-mcp --init-worker` 寫的 `.mcp.json` 與 console 的 `mcp_token` 檔,在 Windows 用 `icacls <檔案> /inheritance:r /grant:r "<網域\使用者>:F"` 收緊成只有本人可讀寫(Windows 的 0600 沒有作用);失敗時印出警告(不再默默略過)。非 Windows 維持 0600。 |
| 畫面上的 MCP 狀態 | `mcp_access` 與 `mcp` view 的 `error` 不再含路徑、資料庫文字:權杖讀取失敗、連接埠開不了、伺服器異常停止都是固定句子(「詳情看 console 的啟動紀錄」),原文進 log。 |

### 登入(`POST /api/auth/login`)狀態碼

見上面「console 網頁版」表格:帳號不存在與密碼錯誤維持同一個 401 與同一句話;被鎖 **429**(帶 `Retry-After`);LDAP 連不上或服務帳號錯誤 **503**;存 session 失敗 **500**。
前端登入頁會顯示後端的訊息(429 補上約幾分鐘後可再試);回應不是 JSON(例如反向代理的錯誤頁)時顯示「伺服器回應格式不對(HTTP 狀態碼)」,不再是 JSON 解析的英文錯誤。
失敗紀錄表(`fails`)也加了上限:只查詢不會為每個帳號建紀錄,最多同時追蹤 5000 組(帳號、來源),滿了先清過期的、再淘汰最舊的一成。

### `/api/action/{name}`、`/api/tool/{name}` 不存在時一致回 404

以前不存在的工具回 400、不存在的動作回 500;現在兩者都是 **404**,內容 `{"error": "沒有這個工具:<名稱>"}` / `{"error": "沒有這個動作:<名稱>"}`(名稱最多 60 字元)。

### 哨兵錯誤一律固定句子(`core/httperr`)

`store.ErrNotFound`、`ErrVersionConflict`、`ErrLocked`、`ErrProjectNoKey` 對外一律是固定句子(`找不到指定的資料`、`這筆資料已經被別人改過,請重新載入後再試`、`這筆資料目前被鎖定…`、`這個專案還沒設定代號…`),狀態碼不變(404、409、409、400)。
被 `fmt.Errorf("…%w")` 包在後面的原文(例如夾帶的組織、專案、資料內容)只進 log。`ErrVersionConflict` 的 `currentVersion` 保留。
以前這幾種錯誤的對外訊息是英文 `not found`、`version conflict`、`locked`、`project has no key`;依賴這些英文字串的外部腳本要改看狀態碼或 `currentVersion`。
要刻意給人看特定文字,用 `httperr.Human(err, 訊息)`(狀態碼與 data 沿用被包住的錯誤),或在 service 層轉成 `*service.Error`;不要靠 `%w` 夾帶文字。

### 其他錯誤出口

- `incident_note`(事件註記)、`jira_import`、Facebook 的 5 個動作不再把任意 `err.Error()` 包成「刻意給人看」的訊息:`service.Error` 與 Graph API 的說明文字(不含內部細節時)照給,其餘照 `httperr` 分類,資料庫文字、路徑、連線位址換成通用訊息、原文進 log。
- 錯誤原文 log 的節流改成 `op` 加原文的雜湊(固定長度,原文再長也不吃記憶體),原文與 `op` 都截斷(1000 與 100 字元),並加全域速率上限:整體每秒最多 50 行,超過的只累計,下一次輸出補「已略過 M 筆」。
- 遮蔽規則補了 `no such host`、`Bearer <權杖>`、`xxx_secret=`、`xxx_token=`、UNC 路徑(`\\主機\共用`)。

## 專案成員與受限專案(ISS-34 第 3 階段)

- 專案新增 `restricted`(受限)旗標與專案成員(`GET/POST /api/orgs/{org}/projects/{id}/members`、`…/members/remove`;MCP `project_save` 的 `restricted`、`add_members`、`remove_members`、`member_role`;CLI `omixor projects restrict|unrestrict|members|member-add|member-remove`;console action `project_members`、`project_member_set`、`project_member_remove`、`project_update`/`project_create` 帶 `restricted`)。
- **行為變更只影響受限專案**:受限專案只有專案成員、組織管理員、伺服器管理員看得到;其他人(含角色憑證)看到的和「不存在」一樣(列表沒有、單筆 404)。沒有受限專案時行為完全不變。
- `Project` 的 JSON 多了 `restricted`(bool)與 `myRole`(admin、member,不是成員沒有);新增 `ProjectMember` 輸出(`project_member_list.json`)。Swift 未動(新欄位照舊可解碼)。
- 角色憑證預設視為非成員:受限專案的 Issue 要先把角色加為專案成員才能指派給它。
- console 限制見 `docs/authorization.md` §0.2。

## v0.5.9 到 v0.5.13:MCP 新協定、OAuth 管理員、執行日誌、資料表改名

### 升級須知(先看這裡)

- **資料表改名不向後相容**:結構異動步驟 3 把 `ops_issues`、`ops_issue_comments` 改成 `omixor_issues`、`omixor_issue_comments`(連同約束、外鍵、索引)。舊版程式的 SQL 寫死舊名,新舊版不能同時連同一個資料庫,**必須停機部署**(先停舊版、再起新版)。
- **改名是單向的,而且 Cloud SQL 沒有 PITR**:部署前先備份 Cloud SQL 並確認可還原。備份與部署由人執行;回滾步驟與理由見 [design-schema-rename.md](design-schema-rename.md)。
- console 本機資料庫的 `ops_*` 表(`ops_documents`、`ops_secrets`、`ops_sessions`、`ops_activity`、`ops_state`…)啟動時自動改成 `omixor_*`、資料保留;新舊表同時存在會報錯、不覆蓋(見 [console/README.md](../console/README.md))。
- 所有 MCP 給 LLM 看的文字(工具說明、instructions、錯誤訊息、`agent_context` 的原因)改成英文;依賴舊中文字串的腳本要改看穩定代碼(`error.code`、`reason`)。

### MCP

- **2026-07-28 協定的最小子集**:新增無狀態的 `server/discover`;modern 請求的結果帶 `resultType`(`complete`);`server/discover`、`tools/list`、`resources/list`、`resources/read` 的結果帶 `ttlMs`、`cacheScope`(秒數由 `OMIXOR_MCP_LIST_TTL_SECONDS` 決定,見 [configuration.md](configuration.md));路由標頭與 `_meta` 不一致時回錯誤。**舊協定版本(2025-06-18、2025-03-26、2024-11-05)照舊提供**,回應不帶快取提示。
- **OAuth 新增 `mcp:admin`**:在 `mcp:write` 之上多開組織結構工具,只有組織管理員能授予,**每次呼叫即時檢查**目前角色;`agent_task_create` 不開放給 OAuth。工具清單與流程見 [authorization.md](authorization.md)。
- **結果格式統一**:成功與失敗都有 `structuredContent`(物件),`content[0].text` 是同一份 JSON;失敗是 `{"error":{"code","message","status","data"}}`;`error.data` 裡太大的欄位只放 `structuredContent`,text 版改帶 `truncated: true`。細節見 [mcp-vocabulary.md](mcp-vocabulary.md)。
- `doc_save` 與 `docs` 的結果帶 `version`,可直接拿去做下一次局部修改。
- **MCP Apps 畫面(`ui://`)**:flowchart、items、decisions 三個畫面;decisions v0.5.13 接上 `issue_decisions`;下一版起不再唯讀:宿主支援 View 發起工具呼叫時可重新整理、回覆 decision(需 `mcp:write`),`help` 仍只顯示。對照表見 [mcp-vocabulary.md](mcp-vocabulary.md)。
- `agent_task_create` 可帶 `issue` 連結;`agent_context` 的不可用工具原因改為穩定代碼 `reason` 加英文 `reason_message`。

### commits

- `omixor commits sync`(commits 讀入與 Issue 連結),用法見 [commits.md](commits.md)、設計見 [design-commits.md](design-commits.md)。
- 這個 CLI 的設定一律走 `config`(例如 `config.CI`),不直接讀環境變數(修正 v0.5.11 部署被測試擋下的問題)。

### 角色執行與畫面

- **派發自動/手動**:每個角色有自動執行開關;關著時,每筆待領取任務有「開始執行」按鈕(一次只跑一筆)。領任務前先檢查 claude 是否已登入;MCP 沒連上或工具全部失敗時不會補報 `done`。人可以回覆被擋住的任務。
- **執行日誌**:記錄工具呼叫、伺服器確認、遮蔽結果,每個組織保留最近 50 筆;`reportedBy` 為 `role`(角色自己回報)或 `runner`(執行者代報)。
- console 新增「需要處理」「執行日誌」頁籤,開發進度會顯示需要協助;瀏覽器歷史(上一頁、重新整理還原頁面與子狀態);輸入框統一;登出圖示、透明 Logo。
- **Mac App 同步**:需要處理、執行日誌分頁、開發進度需要協助、輸入框外觀、返回(⌘[)、App 圖示、登出圖示。

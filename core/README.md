# omixor-core

omixor 的核心後端模組(Go)。目前有:

| 套件 | 內容 |
|---|---|
| `model` | 共用的資料型別(Issue、組織、成員、使用者、token、操作紀錄、虛擬角色、文件櫃),JSON 格式和 app、omixor-web 相同 |
| `store` | 存放介面,實作有 PostgreSQL(`store.NewPG`)與記憶體(`store.NewMem`,測試用) |
| `service` | 商業邏輯與權限:授權(用 evoleap 證明身分,發 omixor 自己的 token)、組織與成員、Issue、虛擬角色與任務、文件櫃 |
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
- 組織成員:讀寫自己組織的 Issue 與文件櫃的文件;作者一律是自己(不能冒名)
- 文件櫃的結構(區塊與頁籤):組織管理員
- 角色身分(角色憑證):文件櫃只能讀寫角色 `docAccess` 範圍內的位置,署名是角色;不能刪文件、不能改結構

## 組織

組織的所有資訊都存在核心(`omixor_orgs`):`id`(建立後不能改)、`name`、`symbol`(圖示名稱)、`color`(顏色)、`note`(備註)、`createdAt`;列出「我的組織」時另外帶 `myRole`。圖示、顏色、備註沒設是空字串,怎麼顯示由介面決定。

- `POST /api/orgs`:`{id, name, symbol?, color?, note?}` 建立(伺服器管理員)
- `PUT /api/orgs/{org}`:`{name, symbol?, color?, note?}` 修改(伺服器管理員;name 不能空白)
- 刪除組織只有 `service.DeleteOrg`(連成員與 Issue 一起刪,不能復原),沒有 HTTP 路由

舊的 PostgreSQL 資料庫啟動時會自動補上 `symbol`、`color`、`note` 三欄(`alter table … add column if not exists`)。

## 文件櫃

Markdown 文件依區塊與頁籤分類(同 app 的 Cabinet)。一個組織一份結構與一份文件清單,存在 `omixor_cabinet_layouts`(org → layout jsonb)與 `omixor_docs`(主鍵 (org, id),id 是 `DOC-<n>`,`pos` 大的在前)。刪除組織一併刪。

- 結構:區塊 `{id, label, path, tabs}`,頁籤 `{path(用 / 開頭,當 id), label, code?, group?}`;組織沒存過結構時是空白結構(總覽、文件),`layoutSaved: false`
- 改結構前檢查(`model.ValidateCabinetLayout`):名稱不空、id 不重複、頁籤用 `/` 開頭、被拿掉的區塊或頁籤裡不能還有文件
- 存文件前依範本檢查(`model.ValidateDocBody`):內文開頭 `---` 欄位的 `kind` 有對應範本(`kind: template`、`for: <kind>`、`required: …`,內含 ```` ```html ```` 版面)時,必填欄位要有;範本本身要有 `for` 與 html 區塊
- 顯示用的整頁 HTML 不在核心:介面用核心給的文件與範本自己產生(omixor-web 的 `DocPage`、app 的 `DocTemplates.page`,共用 repo 的 `templates/doc/docrender.js`)
- 角色的 `docAccess`:key 是區塊 id 或頁籤網址,值 `read`/`write`(頁籤的設定優先於區塊);`ops_agent_upsert` 的 `doc_access` key 也可以給區塊名稱、頁籤名稱或代號,核心依結構換成 id

API:

- `GET /api/orgs/{org}/cabinet`:`{layout, docs, layoutSaved}`(角色憑證只看到能讀的文件)
- `POST /api/orgs/{org}/cabinet/layout`:`{layout}` 改結構(組織管理員)
- `POST /api/orgs/{org}/cabinet/import`:`{layout?, docs?}` 第一次搬資料用(組織管理員;已經有文件就 409,已經有結構就略過結構)
- `GET /api/orgs/{org}/docs`、`GET /api/orgs/{org}/docs/{id}`(id 不分大小寫)
- `POST /api/orgs/{org}/docs`:`{title, body, section, tab?, tags, author?}` 新增(`author` 只有伺服器管理員給的算數)
- `POST /api/orgs/{org}/docs/{id}`:`{title?, body?, appendBody?, section?, tab?, tags?}` 修改;給 `section` 就是搬移
- `POST /api/orgs/{org}/docs/{id}/delete`(成員;角色憑證不行)
- `POST /api/orgs/{org}/docs/{id}/read`:分段讀(像讀真實檔案,省 agent 的上下文):`{outline}` 大綱(各段 id、標題、行號範圍、字數)、`{section}` 讀一段(id 或標題)、`{from, to}` 讀行號範圍、`{grep, context}` 搜尋;都會回 `version`(內文雜湊)
- `POST /api/orgs/{org}/docs/{id}/edit`:局部修改(三種擇一):`{edits: [{old, new, all?}]}` 字串取代(old 必須唯一,任何一個失敗都不改)、`{replaceSection, content}` 整段替換(保留標題)、`{appendToSection, content}` 接在段尾(content 自帶標題就是插入新段落);帶 `baseVersion` 時版本不符:字串取代在最新內文上再套一次,其他回 409。只回改到的那幾行與新版本。權限、範本必填欄位同一般修改。段落規則見 `model/docsections.go`
- MCP:`ops_docs` 給 id 時可帶 `outline`、`part`、`from`/`to`、`grep`/`context`、`full`(超過 6000 字的文件只給 id 時只回大綱與開頭);`ops_doc_save` 可帶 `edits`、`replace_part`、`append_to_part` + `content`、`base_version`

## 組織鏈

資訊流動方向:CEO 給研發長「目標」,研發長給產品經理、設計師、工程師「任務」,做完的人回報給派工的人。一個組織一份 `OrgChain{edges, updatedAt}`,存在 `omixor_org_chains`(org 主鍵、edges jsonb;刪除組織一併刪)。
一條邊 `{from, to, label}` 表示「from 可以派工作給 to(label 例如 目標、任務;空白 = 任務),to 做完回報給 from」。節點是 `agent:<角色 id>` 或 `user:<email 小寫>`。資料模型在 `model/chain.go`,規則在 `service/chain.go`。

- 讀:組織成員(含角色身分);改:組織管理員,整份取代
- 驗證(400 並說明):節點存在(角色存在、user 是組織成員;角色可寫 id 或名稱、不分大小寫,存成角色 id)、不能指向自己、同一對 from → to 不能重複、不能有循環(錯誤會列出循環的路徑)、label 最多 40 字、最多 500 條邊
- 刪角色、移除成員:相關的邊從存檔拿掉;讀取時也會再過濾一次不存在的節點(規則只看得到還存在的節點)
- 派工:
  - 組織管理員:照舊可以派給任何角色(說明不加前綴)
  - 一般成員:鏈上要有 `user:<自己>` → `agent:<目標>`,否則 403,訊息列出依鏈可以派給誰
  - 角色身分(角色憑證、角色端點):`DelegateTask`,鏈上要有 `agent:<自己>` → `agent:<目標>`;任務 `createdBy = agent:<自己>`,`links` = [憑證綁定的那筆任務] + 那筆任務的 links。伺服器管理員可以帶 `from`(、`taskId`)代角色委派(同 `author` 的規則,例如 omixor-web 內建核心)
  - 執行者(`/mcp/worker`):`dispatch_delegate`,規則同角色身分,要領取驗證碼 `code`
  - 依鏈派的任務(成員、角色),label 寫在說明開頭:`【目標】` + 空行 + 原本的說明(不加新欄位,所以不用改資料表、app 與 web 的任務格式,執行任務的 LLM 也直接看得到)
- 回報:任務變成 `done` 或 `blocked`,建立者是鏈上節點(`agent:<id>` 或 email)而且鏈上有 建立者 → 任務角色 的邊:
  - 建立者是角色:替它排一筆待領取任務「檢視回報:<原標題>」(優先度同原任務;說明帶 done 的 result 或 blocked 的 note 與原任務 id;links = [原任務 id] + 原 links;`createdBy = chain`,所以檢視任務做完不會再回報,不會連鎖)。同一筆原任務已有未結束的檢視任務就不重複排。原任務留一則 `chain` 署名的備註「已回報給 <角色>(T-n)」
  - 建立者是真人:不建任務,原任務留備註「已回報給 <email>」;用 `GET /api/orgs/{org}/agent-tasks?reportTo=me`(或 `user:<email>`)查「回報給我的任務」
- 回報失敗(例如資料庫錯誤)只記操作紀錄(`chain.report.error`),不讓原本的回報失敗

API:

- `GET /api/orgs/{org}/chain`:`{edges, updatedAt}`(成員;角色憑證 `Bearer <agentId>:<secret>` 也可以)
- `POST /api/orgs/{org}/chain`:`{edges}` 整份取代(組織管理員)
- `POST /api/orgs/{org}/agent-tasks/delegate`:`{to, title, instructions?, priority?}` 角色委派(角色憑證;伺服器管理員用 token 可帶 `from`、`taskId`)
- `POST /api/orgs/{org}/agent-tasks/{id}/worker/delegate`:`{code, to, title, instructions?, priority?}` 執行者以任務角色委派
- `GET /api/orgs/{org}/agent-tasks?reportTo=me|user:<email>|agent:<id>`:依鏈回報給這個節點的任務(角色身分只能查 `agent:<自己>`)

MCP:

- 管理端點:`ops_chain`(讀,邊附 `fromName`、`toName`)、`ops_chain_save`(組織管理員;`edges` 取代全部,或 `add`、`remove`)
- 角色端點:`ops_agent_task_delegate`(`to`、`title`、`instructions?`、`priority?`;來源任務一律是憑證綁定的那筆);`ops_agent_context` 多 `chain: {upstream, downstream}`(每筆 `{node, name, label}`:上游 = 回報給誰,下游 = 可以派給誰)
- 執行者端點:`dispatch_delegate`;`dispatch_claim` 的 `nextSteps` 會列出這個角色依鏈可以委派給誰

## MCP 端點

核心(`mcp` 套件)提供三種 MCP 端點,工具白名單與認證都不同;雲端 server 與本機 web 只負責掛路由與認證。

| | 管理 `/mcp` | 角色 `/mcp/agent` | 執行者 `/mcp/worker` |
|---|---|---|---|
| 給誰 | 人或管理用的 LLM | 領了任務、以虛擬角色工作的 LLM | 領取任務的 LLM(找任務、領取) |
| 認證 | 雲端 `/mcp/org/{org}`:omixor token;本機 `/mcp`:綁 127.0.0.1 | `Authorization: Bearer <agentId>:<secret>`(領取任務發的憑證,綁一筆任務) | 同管理端點:雲端 `/mcp/worker/{org}` 用 omixor token;本機 `/mcp/worker` 綁 127.0.0.1 |
| 身分 | 呼叫者本人 | 憑證的角色(署名一律是角色) | 呼叫者本人(組織成員) |
| 工具 | 組織、Issue、角色(含新增修改)、派任務、領取、回報、文件櫃(`ops_cabinet`、`ops_docs`、`ops_doc_save`;帶 `agent` 時依該角色的 docAccess)、組織鏈(`ops_chain`、`ops_chain_save`) | `ops_agent_context`(含能存取的文件櫃區塊與文件、組織鏈的上下游)、`ops_agent_tasks`、`ops_agent_task_report`、`ops_agent_task_delegate`、Issue 讀/建/留言、文件櫃(依憑證角色的 docAccess) | `dispatch_queue`、`dispatch_claim`、`dispatch_status`、`dispatch_report`、`dispatch_comment`、`dispatch_delegate`、`dispatch_release` |
| 遠端模式(web) | 轉給雲端 | 一律 401(憑證在雲端,要連雲端的 `/mcp/agent`) | 轉給雲端(`credential.endpoint` 指向雲端 `/mcp/agent`) |

執行者的標準流程:

1. `dispatch_queue`:看有哪些任務可領(全部啟用角色的待領取、需要協助、我領取中的),依優先度排序
2. `dispatch_claim`:領取(給 `id` 領那一筆;不給就自動挑最優先的,可用 `agent` 限定角色)→ 回傳任務、角色設定、憑證(`credential`:`agentId`、`secret`、`code`、`endpoint`、`expiresAt`)與 `nextSteps`。`code` 是 6 位數的領取驗證碼(由憑證算出,同一次領取不變)
3. 照角色設定與任務說明工作;以角色身分寫入一律用執行者端點的工具,帶任務 `id` 與 `code`(伺服器確認呼叫者是領取者、驗證碼是這次領取的,避免選錯角色),不需要密鑰:
   - `dispatch_report`:`status: inProgress`(附 `note`)回報進度並續約(至少每小時;逾時會被自動退回待領取);做完 `status: done` 並寫 `result`;需要人決定 `status: blocked` 並在 `note` 提問。`done`/`blocked` 後憑證與驗證碼立刻失效
   - `dispatch_comment`:在任務 `links` 裡的 Issue 留言,署名 `agent:<角色 id>`
   - `dispatch_delegate`:依組織鏈把工作委派給這個角色的下游角色(`to`、`title`、`instructions?`、`priority?`;`dispatch_claim` 的 `nextSteps` 會列出可以委派給誰)
   - 操作紀錄除了角色,另記一筆實際呼叫的人(`agent.task.worker.report`、`agent.task.worker.comment`、`agent.task.worker.delegate`)
4. 直接連角色端點的程式(不經執行者端點)才用 `secret`:`Authorization: Bearer <agentId>:<secret>` 連 `credential.endpoint`
5. 中途放棄:`dispatch_release`(領取者本人或組織管理員),任務退回待領取;`dispatch_status` 可查自己領取中的任務與鎖定剩餘時間

相關 API:`POST /api/orgs/{org}/agent-tasks/{id}/worker/report`(`{code, status?, result?, note?, links?}`)、`/worker/comment`(`{code, issueId, comment}`)、`POST /api/orgs/{org}/agents-import`(`{agents, tasks}`,第一次搬資料)、`POST /api/orgs/{org}/agent-tasks/claim-next`(`{agentId?}`)、`POST /api/orgs/{org}/agent-tasks/{id}/release`(`{reason?}`)。

## 測試

```bash
go test ./...
```

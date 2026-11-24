# omixor(macOS app)

維運線上服務的原生 macOS app(SwiftUI)。把開發進度、CI/CD、伺服器狀態、監控告警、事件處理放在同一個地方,並讓 AI 在人的監督下自動化部分環節。

目前是**介面原型**:資料來自 `Core/MockData.swift` 的模擬資料,動作只改本機狀態,尚未連接真實服務。

## 畫面

| 區塊 | 內容 |
|---|---|
| 總覽 | 整體狀態、關鍵指標、服務健康、觸發中的告警、進行中的事件、CI/CD 進度、待核准的 AI 請求、動態牆 |
| 開發進度 | Sprint 燃盡圖與看板、Pull Requests(含 AI 審查)、儲存庫 |
| CI/CD | 執行紀錄與階段圖、日誌、AI 失敗診斷、部署(canary、回滾)、各環境版本與 DORA 指標 |
| 伺服器與服務 | 服務 SLO 與錯誤預算、主機即時指標與容器、服務依賴地圖 |
| 監控與告警 | 告警處理、儀表板、日誌查詢、告警規則與自動修復 |
| 事件 | 事件時間軸、狀態流程、AI 摘要、檢討報告草稿、值班表 |
| AI 自動化 | 待核准請求、自動化流程與自主程度(只建議／需核准／自動執行)、執行紀錄、成效 |
| 設定 | 組織、整合服務、連線、LINE、團隊與值班、AI 治理政策、通知 |

右側是 AI 助理(⌘J),依目前頁面整理狀況並回答問題(目前為模擬回應)。⌘K 開指令面板,⌘1–⌘8 切換頁面。

## AI 自動化的設計原則

- 每個自動化流程有**自主程度**:只建議、需核准、自動執行。正式環境預設最多到「需核准」。
- 需核准的動作會列出要執行的指令、理由、依據與回滾方式,由人決定。
- 所有 AI 動作都標示為 AI,並記錄在動態牆與事件時間軸,可追溯。

## 執行

```bash
cd ops
swift run
./scripts/build-app.sh && open build/omixor.app
```

需要 macOS 14 以上與 Swift 6 工具鏈。

App 圖示是 `Resources/AppIcon.icns`(已提交),build-app.sh 會複製進 `Contents/Resources/` 並在 Info.plist 設 `CFBundleIconFile`。
圖樣取自 `site/favicon.svg`;改了 SVG 之後跑 `sh scripts/gen-appicon.sh` 重產(只用 macOS 內建的 qlmanage、sips、iconutil),再提交新的 .icns。

### 功能開關(系統層/組織層)

所有功能都編進 App 與包進去的 omixor-local(不再有正式版/開發版、`OMIXOR_DEV`、`OMIXOR_EDITION`)。
開放哪些功能由 core 資料庫裡的兩層開關決定(清單在 `core/model/features.go`,規則在 `core/service/system.go`):

- **系統層**(整台伺服器一份,`/api/system/features`):伺服器管理員在「系統」頁決定這台伺服器開放哪些功能;
  本機模式的本機使用者就是伺服器管理員。沒設定過時,原本正式版的功能開放,實驗、範例資料、家庭助理、外部 MCP 閘道等不開放。
- **組織層**(每個組織一份,`/api/orgs/<組織>/system`):組織管理員在 設定 › 功能 開關自己組織要用的功能(只列系統層開放的)。

實際可用 = 系統層開放 && 組織層開 && 頁面規則(頁面關了分頁不顯示;開放的分頁全關時頁面不顯示;總覽、設定、系統永遠開)。
讀不到 core 時用這次執行最後一份;都沒有時用保守預設(`FeatureFallback`)。
`OMIXOR_APP=<路徑>/omixor.app` 可以把 App 打包到別的位置。

## MCP 伺服器

app 開著時會在 `http://127.0.0.1:3765/mcp` 提供 MCP 伺服器(Streamable HTTP,只接受本機連線)。MCP 用戶端可以讀取 app 裡的即時資料,也能寫回去,變更立刻出現在畫面上,並以「MCP 用戶端」記在動態牆與事件時間軸。狀態與連線指令在 設定 › 整合 最上方。

`/mcp` 一定要帶 `Authorization: Bearer <token>`(沒帶或不對回 401):token 由 App 產生、存在這台的鑰匙圈,代表「這台 Mac 的使用者」(確認卡片與動態牆都用這個身分,不用用戶端自報的名稱)。在 設定 › 整合 › MCP 伺服器 可以顯示、重新產生,並「複製 Claude Code 設定指令」。

加到 Claude Code(已經加過的要先 `claude mcp remove omixor`,改名前加的叫 `ops`,改用 `claude mcp remove ops`;當初用 `-s user` 加的要加 `-s user`):

```bash
claude mcp add --transport http omixor http://127.0.0.1:3765/mcp --header "Authorization: Bearer <App 複製的 token>"
```

只支援 stdio 的用戶端(例如 Claude Desktop)用轉接程式 `omixor-mcp`:

```bash
swift build -c release --product omixor-mcp   # 產生 .build/release/omixor-mcp
```

```json
{ "mcpServers": { "omixor": { "command": "/絕對路徑/mac/.build/release/omixor-mcp", "env": { "OMIXOR_MCP_TOKEN": "<App 複製的 token>" } } } }
```

連接埠可用環境變數 `OMIXOR_MCP_PORT`(app 與 `omixor-mcp` 都讀;`omixor-mcp` 另外讀 `OMIXOR_MCP_URL`)或 `defaults write com.tim72117.ops mcp.port 4000`(打包後的 omixor.app;bundle id 是改名前留下的,刻意不改)修改。

改名前的環境變數(`OPS_*`)暫時仍可用:一律先讀新名稱(`OMIXOR_*`),沒設才讀舊名稱,讀到時記一次 log。鑰匙圈的 service 從 `com.tim72117.ops.*` 改成 `com.tim72117.omixor.*`:新的讀不到才讀舊的,讀到就複製一份到新的,刪除時兩邊一起刪。偏好設定 `ops.settings.*` 啟動時搬到 `omixor.settings.*`。資料夾 `~/Library/Application Support/Ops` 與 bundle id `com.tim72117.ops` 刻意不改。

| 工具 | 作用 |
|---|---|
| `overview` | 整體狀況:服務健康、觸發中的告警、進行中的事件、CI/CD、待核准請求、值班、最近動態 |
| `list` | 列出任一資料集合,可用 `where` 依欄位篩選、`search` 全文搜尋、分頁 |
| `get` | 依 id 取得完整一筆(含時間序列、工作日誌、事件時間軸) |
| `navigate` | 切換 app 的頁面、選中項目、環境與時間範圍 |
| `alert_update` / `alert_create` | 確認、解除、靜音告警或寫入 AI 判讀;從外部送進新告警 |
| `incident_declare` / `incident_update` | 宣告事件(可關聯告警);改狀態、加註記與 AI 發現、寫入摘要 |
| `work_item_create` / `work_item_move` | 建立工作項目(可掛到事件後續);移動看板欄位 |
| `pipeline_update` | 重新執行、取消 run,或寫入 AI 失敗診斷 |
| `request_approval` | 送出待人核准的請求 |
| `log_append` / `activity_post` | 寫日誌、發動態 |
| `voice_submit` / `voice_jobs` | 分派旁白語音批次、查批次狀態(發佈只能由人在 app 裡按) |
| `site_visitors` | 讀 evoleap 網站的會員與訪客統計 |
| `issue_create` / `issue_update` / `issues` | 存入、更新、查詢 Issue |
| `agents` / `agent_upsert` | 列出虛擬角色;新增或修改角色與例行任務 |
| `agent_task_create` / `agent_tasks` | 指派任務給角色、查任務 |
| `agent_context` | 用角色 id 或名稱取得它能讀取的資訊:角色設定、未完成的任務、文件櫃權限與能讀的文件(`include_bodies` 附全文) |
| `agent_task_claim` / `agent_task_report` | 以角色身分領取任務、回報進度與結果 |
| `orgs` | 列出組織與目前使用中的組織 |
| `gcp` / `gcp_logs` / `gcp_metrics` | Cloud Run 服務與 Job 狀態、版本、執行;Cloud Logging 日誌;Cloud Monitoring 請求量、5xx、p95 延遲(只讀) |
| `github` / `github_runs` / `github_run` / `github_pull` | GitHub 概況、Actions 執行、執行的工作步驟與失敗紀錄、PR 詳情(只讀) |
| `cabinet` / `docs` / `doc_save` | 文件櫃結構、查文件、存文件(帶 agent 時依角色的存取範圍把關) |
| `wireframe_render` / `flowchart_render` | 用描述畫線框圖、流程圖(JSON 或 Mermaid flowchart)回傳 SVG,可順便存進文件櫃;core 的工具,App 連 server 時轉送 |
| `fb_pages` / `fb_posts` / `fb_insights` / `fb_drafts` | Facebook 粉絲專頁清單、最近與已排程貼文(含成效)、專頁成效、貼文草稿(只讀) |
| `fb_draft_save` | 新增或修改貼文草稿(帶 agent 時署名該角色);發佈與排程只能由人在 app 裡按 |

依 AI 治理原則,**核准 AI 請求、核准部署、回滾、調整自動化自主程度、切換整合、發佈 Facebook 貼文**只能由人在 app 裡做。MCP 用戶端需要這類變更時,用 `request_approval` 送出請求,之後用 `get approvals` 查結果。

程式在 `Sources/Omixor/MCP/`:`MCPHandler` 是協定與工具(直接讀寫 `AppState`),`MCPServer` 是 HTTP 傳輸;`Sources/OmixorMCP/` 是 stdio 轉接。測試:`swift test`。

## 連線(設定 › 連線)

omixor 呼叫外部服務都透過「連線」:每個連線是一個網址加上一種通用的認證方式,token 與 client secret 存在鑰匙圈(只限這台 Mac),設定存在 `~/Library/Application Support/Ops/connections.json`。

| 認證方式 | 做法 |
|---|---|
| OAuth 2.0 / OIDC | 授權碼 + PKCE,回呼 `http://127.0.0.1:<埠>/callback`(RFC 8252)。只填網址時自動探索:`/.well-known/oauth-protected-resource`(RFC 9728)→ 授權伺服器的 `openid-configuration` / `oauth-authorization-server`(RFC 8414);沒填 client_id 且伺服器支援時自動註冊(RFC 7591)。token 快過期或收到 401 時用 refresh token 換新;可選擇送 id_token(Google IAP、Cloud Run) |
| 瀏覽器核准 | evoleap CLI 的登入方式:POST 登記回呼 → 管理員在核准頁按核准 → 用 id + code 換 token。路徑可改 |
| 固定 Token | 貼上 API key / 個人存取權杖 |
| 不用認證 | 公開 API |

預設有「evoleap 正式站」與「evoleap 本機」兩個連線。其他功能用 `ConnectionStore.request(連線 id, 方法, 路徑)` 呼叫,不用自己處理認證。

## 旁白發佈(CI/CD › 旁白發佈)

把 shuttle 專案的旁白語音批次接進 omixor:送出(`npm run voice -- <腳本> --batch`)→ 每 10 分鐘 `--collect` → 收完列出變動的 `.voice.json`、可試聽 → 人按發佈(Touch ID)→ 透過選定的連線 `PUT /api/admin/scripts/<路徑>`,再用 API 讀回比對。不修改 shuttle 的程式,也不直連資料庫。AI 可以用 MCP 的 `voice_submit`、`voice_jobs` 分派與查詢,發佈沒有 MCP 工具。工作紀錄存在 `~/Library/Application Support/Ops/voice-jobs.json`。

## 網站訪客(監控與告警 › 網站訪客)

透過連線(預設 evoleap 正式站)以管理員身分讀 `GET /api/admin/overview?range=…`,也就是 evoleap 後台「會員」頁那份資料:會員數、訪客漏斗(打開 → 載入完成 → 有播放)、各課概況、裝置／來源／載入錯誤、從首頁進來的路徑、首頁停留、登入頁、最近沒播放的造訪足跡。可選全部／30 分鐘／12 小時／1 天／7 天,開著時每 60 秒更新。AI 可用 MCP 的 `site_visitors` 讀同一份資料。不改 evoleap、不連資料庫。

## Issue(開發 › Issue)

要追蹤的問題或待辦,存在 `~/Library/Application Support/Ops/issues.json`(真實資料,重開 app 也還在;看板上的工作項目則是範例資料)。人可以在畫面上新增、編輯、留言、關閉;AI 透過 MCP 存入:

- `issue_create`:標題、Markdown 內文、優先度(urgent / high / normal / low)、標籤、來源、網址、所屬倉庫 `repo`;`return`(`ack` 預設只回精簡確認 key/id/status/version/updatedAt/changed,`full` 回完整 Issue)
- `issue_update`:改欄位、`append_body` 補內文、`comment` 留言、`status` 關閉或重新打開、`repo`(空字串 = 清除);同樣有 `return`(ack / full,ack 另含新 version 與 `comment_id`)
- `issues`:給 id 看完整內容(留言預設只回最新 5 則,`comments_limit` 調數量、0 以下 = 不回、`comments_before` 給留言 id 或 RFC3339 時間往前翻;回 `comments_total` / `comments_returned` / `comments_truncated`,每則留言帶 `id`);不給則依狀態、標籤、倉庫、關鍵字列出
- `report_problem(title, what_happened, expected, tool, error, severity)`: files an mcp-feedback issue with non-secret diagnostics; repeated identical reports are merged into the open issue (forwarded to core)
- `commits(repo?, sha?, issue?, query?, limit?)`, `commits_report(repo, commits[])`, `issue_commit_link` / `issue_commit_unlink(issue, repo, sha, kind?, note?)`: commit ingestion and issue links (forwarded to core)
- 以上工具與 `issue_accept` 都由 core server 執行,App 只轉送(參數與結果,含 `_meta["omixor/server"]`,原樣帶過),沒有 Swift 實作

所屬倉庫 `repo` 是 owner/name(GitHub)或 group/sub/project(GitLab),可加 `github:`、`gitlab:` 前綴;只允許英數與 `- _ . / :`、最長 200(同 core 的 `model.CleanRepo`)。篩選不分大小寫,前綴有一邊沒寫就忽略。列表可依倉庫篩選,新增與編輯時從 GitHub 追蹤的 repo、其他 Issue 用過的倉庫選,或「其他…」自己輸入。

緊急與高優先度的未解決 Issue 會算進側邊欄「開發進度」的徽章。

## 虛擬角色(AI 自動化 › 虛擬角色)

每個角色有職責、角色設定(給 AI 的說明)與例行任務,存在 `agents.json`。omixor 不自己跑 AI:任務(人在畫面上指派、例行任務到時間自動排,或 AI 用 `agent_task_create` 指派)排著等領取;在 Claude Code 貼上「複製給 Claude Code」的那段話,AI 就會以角色身分用 `agent_task_claim` 領取、照角色設定做,用 `agent_task_report` 回報過程與結果,需要人決定時標成「需要協助」。角色可在畫面上按「新增角色」,或請 AI 用 `agent_upsert` 隨時新增或修改。預設角色:訪客分析師、旁白製作、Issue 整理員、課程開發、行銷。

## 組織(側邊欄最上方 / 設定 › 組織)

每個組織有自己的工作空間:Issue、虛擬角色與任務、文件櫃(含結構)、連線(鑰匙圈裡的登入也分開)、追蹤的 GitHub repo、GCP 專案設定。資料在 `~/Library/Application Support/Ops/orgs/<組織 id>/`,`orgs.json` 記組織清單與目前的組織。旁白批次(`voice-jobs.json`)與畫面上的範例資料是共用的。

- 側邊欄最上方切換組織;設定 › 組織 可以新增(全新空白,或複製目前組織的角色、文件櫃結構與連線設定)、改名、換圖示、刪除(資料夾移到垃圾桶)。
- 目前組織的工作空間都能重新設定:虛擬角色、文件櫃結構(區塊與頁籤可新增、改名、排序、刪除,有文件的不能刪)、連線、Issue。
- 舊版直接放在資料夾根目錄的檔案,第一次啟動時會複製到預設組織「evoleap」(原檔保留)。
- MCP 一律作用在目前的組織,`orgs` 看是哪一個;切換組織只能由人在 app 裡做。
- 跑測試卻沒指定 `OMIXOR_APP_DATA_DIR` 時,資料夾會改用暫存資料夾,不會碰到真正的資料。

## GitHub(開發進度 › GitHub;設定在 設定 › 程式碼倉庫)

用「GitHub」連線讀追蹤中 repo 的 Actions 執行(工作、步驟、失敗工作紀錄的最後幾行)和開著的 PR(檢查結果、說明、變更檔案)。**只讀**:omixor 不會開 PR、留言或重跑 workflow。

- 登入:網頁 OAuth。GitHub 不支援自動註冊,第一次要在 GitHub 註冊一個 OAuth App(Homepage `http://127.0.0.1`、callback `http://127.0.0.1/callback`),把 Client ID / secret 貼進 設定 › 程式碼倉庫 的引導,之後按「用瀏覽器登入」;同一頁可以登出、加入或停止追蹤 repo。開發進度 › GitHub 只看資料,沒設定好時有「前往 設定 › 程式碼倉庫」按鈕。secret 與 token 存在鑰匙圈。權限只要 `read:user`(公開 repo);私有 repo 要在 設定 › 連線 › GitHub 的 scopes 加 `repo`。也可以改貼 fine-grained 個人存取權杖。
- 追蹤哪些 repo 存在組織資料夾的 `github.json`(每個組織各自一份);有登入時每 5 分鐘更新。
- GitHub 紀錄會轉址到暫時的下載網址;轉址到別的網站時 omixor 會拿掉 Authorization,token 不會外流。

## GCP(伺服器與服務 › GCP)

用「Google Cloud」連線讀設定專案(預設組織:`shuttle-045094509`、`asia-east1`)的 Cloud Run 服務(狀態、目前版本、流量、映像、最近的版本)、Cloud Run Job 的執行、Cloud Logging 的日誌(預設 ERROR 以上),以及 Cloud Monitoring 的請求量(2xx/4xx/5xx)、p95 延遲與執行個體數。**只讀**(`cloud-platform.read-only`):omixor 不會部署、改流量或執行 Job。

- 登入:Google 網頁 OAuth。第一次要在 Google Cloud Console 建一個「電腦版應用程式」OAuth 用戶端,把用戶端 ID / 密鑰貼進分頁上的引導,再按「用瀏覽器登入」。同意畫面在「測試」狀態時登入約 7 天過期;改成「正式版」就不會。登入的帳號要有專案的檢視權限。
- 專案、區域、追蹤哪些服務與 Job 存在組織資料夾的 `gcp.json`(空白表示全部);有登入時每 5 分鐘更新狀態,日誌與指標打開時才讀。
- 連線的 token 只會送到和連線網址同一個網域的主機(`*.googleapis.com`)。

## 文件櫃(側邊欄 › 文件櫃)

純文字(Markdown)文件;```wireframe、```flowchart 與 flowchart 語法的 ```mermaid 區塊會畫成圖(core server 的 `wireframe_render` / `flowchart_render` 渲染,App 只把 SVG 當圖片顯示,失敗時顯示錯誤與原始描述)。預設組織依 evoleap 研發基地 `/studio` 的區塊(總覽、待辦、會員、課程、開發、構想、文件、工具)與頁籤分類;結構照 shuttle 的 `web/src/studio/proposal/ProposalLayout.tsx` 與 `studio/articles/registry.ts`,研發基地改版時要改 `Sources/Omixor/Cabinet/Cabinet.swift`。每個角色在「編輯…」設定文件櫃存取範圍(區塊或頁籤,可讀/可讀寫;頁籤優先於區塊),也可用 `agent_upsert` 的 `doc_access` 調整。AI 以角色身分讀寫文件時帶 `agent`,超出範圍會被拒絕;不帶 `agent` 視為 MCP 用戶端,全部可讀寫。結構可在文件櫃左下「編輯結構」修改(每個組織各自一份 `cabinet-layout.json`)。文件存在組織資料夾的 `cabinet.json`;檔案讀不出來時會改名保留(`cabinet.unreadable-*.json`),不會被覆蓋。


## Facebook 粉絲專頁(AI 自動化 › 粉絲專頁)

用 Meta Graph API(預設 `v24.0`,存在 `facebook.json` 的 `apiVersion`,可改成更新的版本)管理粉絲專頁:撰寫、預覽、立即發佈或排程、取消排程、看貼文與專頁成效。放在 AI 自動化而不是另開一頁:草稿多半由行銷角色寫好、人在這裡把關後發佈,和「待核准」同一種流程;另開側邊欄頁面也會改到 `navigate` 的頁面列舉與 ⌘1–⌘9。

連線(擇一,分頁上有引導):

1. **用 Meta 帳號登入**:在 developers.facebook.com 建立應用程式(類型「商家」),加入「Facebook 登入」產品,「有效的 OAuth 重新導向 URI」填 `http://127.0.0.1:3767/callback`;把應用程式編號與密鑰貼進分頁,按「用瀏覽器登入」。權限:`pages_show_list`、`pages_read_engagement`、`pages_manage_posts`、`read_insights`。omixor 用授權碼換短期使用者權杖 → 換長期權杖(`fb_exchange_token`)→ 讀 `/me/accounts` 取得每個專頁的 Page access token。Meta 若要求重新導向網址必須是 https,改用方法二。
2. **直接貼權杖**:在 Graph API Explorer 取得 Page access token(或使用者權杖,omixor 會讀出它管理的專頁),建議先在存取權杖偵錯工具延長成長期權杖。

- 權杖(使用者權杖、各專頁權杖、App secret)存在鑰匙圈,依組織分開;`facebook.json` 只有專頁清單、要管理哪些專頁與草稿。權杖放在 `Authorization: Bearer` 標頭,只送到 `graph.facebook.com`,`paging.next` 也只跟同一台主機。
- 草稿可從文件櫃的文件或行銷角色的任務結果帶入,也可以由角色用 MCP 的 `fb_draft_save` 寫好。發佈與排程要先確認、再過 Touch ID(和旁白發佈一樣),動態牆以人署名,註明草稿是誰寫的。
- 排程:`published=false` + `scheduled_publish_time`,限制 10 分鐘到 30 天後,時間一律用台北時間。
- 成效:專頁的觸及、瀏覽、貼文互動、追蹤數與新增追蹤(趨勢);每篇的觸及、點擊、心情分類,以及心情 / 留言 / 分享與互動合計。Meta 2025 年起淘汰了部分 insights 指標:omixor 先一次問全部候選指標,收到「不是有效的指標」(code 100)就逐一問、改用替代指標,取不到的顯示「已淘汰」或「無資料」,不會整頁壞掉。權杖過期(code 190)會提示重新連線。
- AI 透過 MCP 只能讀與存草稿(`fb_pages`、`fb_posts`、`fb_insights`、`fb_drafts`、`fb_draft_save`),沒有發佈工具。

程式在 `Sources/Omixor/Facebook/`,畫面在 `Features/Automation/AutoFacebookView.swift`,測試 `Tests/OmixorTests/FacebookTests.swift`(URLProtocol 攔截,不會連到 Meta)。

## LINE(設定 › LINE)

用 LINE 官方帳號的 Messaging API(LINE Notify 已停止服務)做三件事:**推播通知**、**在 LINE 核准 AI 請求**、**在 LINE 問狀況**。只有 Swift app 有這個功能,沒有對應的 MCP 工具。

設定步驟:

1. 在 [LINE Developers](https://developers.line.biz/) 建一個 Provider 與 Messaging API channel(會同時建立官方帳號)。Basic settings 抄 **Channel secret**;Messaging API 分頁最下方發行長期 **Channel access token**。
2. omixor 的 設定 › LINE:貼上 token 與 secret,按「儲存到鑰匙圈」。兩者存在鑰匙圈(帳號名稱帶組織前綴,每個組織各自一組),不會寫進 JSON。
3. 用手機掃 Messaging API 分頁的 QR code 加官方帳號好友(要推到群組就把官方帳號邀進群組)。
4. 把公開 https 網址轉到 omixor 的 webhook 埠(預設 `127.0.0.1:3766`,設定頁可改),擇一:
   ```bash
   cloudflared tunnel --url http://127.0.0.1:3766
   ngrok http 127.0.0.1:3766
   ```
   把工具給的網址貼到設定頁「公開網址」,複製組好的 Webhook URL(`https://…/line/webhook`)。
5. LINE Developers › Messaging API › Webhook settings:貼上 Webhook URL、打開 **Use webhook**,按 **Verify**。LINE Official Account Manager › 回應設定 關閉「自動回應訊息」。
6. 在 LINE 傳一則訊息給官方帳號;你的 userId 會出現在設定頁「最近傳訊息的人」,按「加入白名單」(可選在 omixor 以誰署名)與「加為接收者」(群組的 groupId 也會出現在這裡),再按「傳一則測試訊息」確認。

行為:

- **推播**(push API,每個接收者各送一則 Flex Message:標題、摘要、欄位、在 omixor 哪裡看):告警觸發(依嚴重度門檻)、宣告事件與狀態變更、新的待核准 AI 請求、旁白批次待發佈、虛擬角色任務需要協助。各類可以分別關掉。同一件事只推一次;app 啟動或切換組織時已存在的狀況不推。每則帶 `X-Line-Retry-Key`,網路錯誤或 5xx 重試一次。失敗記在設定頁的「紀錄」,不會擋住 app。
- **Webhook**:只聽 127.0.0.1 的 `POST /line/webhook`;每個請求先用 channel secret 驗 `X-Line-Signature`(HMAC-SHA256、base64、常數時間比較),不符或缺少就回 401、不處理。回覆用 reply API。沒填 channel secret 時伺服器不開。
- **在 LINE 核准**:待核准請求的推播帶「核准 / 拒絕」按鈕(postback,data 帶請求 id 與一次性 nonce,nonce 存在組織資料夾的 `line-state.json`)。按下後檢查發送者在白名單、nonce 有效且沒用過、請求還在待核准,才核准或拒絕,動態牆以白名單設定的人署名並註明「經 LINE」。核准仍是人做的決定,AI 不能透過 LINE 核准。
- **在 LINE 問狀況**:白名單的人傳文字,用 AI 助理同一套回答邏輯(`AssistResponder`)產生回答,轉成純文字回覆。
- 不在白名單的人傳訊息或按按鈕,一律只回「沒有權限」,不洩漏任何資料。
- 非機密設定(接收者、白名單、通知開關、埠、公開網址)存在組織資料夾的 `line.json`。

程式在 `Sources/Omixor/Line/`:`LineStore` 是推播、去重與 webhook 處理,`LineAPI` 是 push / reply 與簽章,`LineWebhookServer` 是 HTTP 傳輸(沿用 MCP 的 `HTTPRequest` / `HTTPResponse`)。測試 `Tests/OmixorTests/LineTests.swift` 用 URLProtocol 攔截,不會連到 LINE。

```
Sources/Omixor/
  Core/          資料模型、AppState(全域狀態與動作)、MockData
  Components/    共用元件(卡片、徽章、指標、圖表、格式化)
  Features/      各頁面,一個資料夾一頁
  MCP/           內建 MCP 伺服器(協定、工具、HTTP 傳輸)
  Connections/   連線與通用認證(OAuth、瀏覽器核准、Token)
  Voice/         旁白語音批次與發佈
  Visitors/      網站訪客統計(evoleap /api/admin/overview)
  Issues/        Issue 資料與存檔(issues.json)
  Agents/        虛擬角色、例行任務與角色任務(agents.json)
  Cabinet/       文件櫃:區塊結構與文件(cabinet-layout.json、cabinet.json)
  Orgs/          組織與各組織的資料夾
  GitHub/        GitHub 的 PR、檢查與 Actions(只讀,github.json)
  GCP/           Cloud Run、Logging、Monitoring(只讀,gcp.json)
  Line/          LINE 官方帳號:推播、webhook、在 LINE 核准與提問(line.json)
  Facebook/      Facebook 粉絲專頁:Graph API、草稿、發佈與成效(facebook.json)
  RootView.swift 側邊欄、工具列、頁面切換
```

接真實服務時,把 `MockData` 換成各整合的資料來源,並把 `AppState` 裡的動作改成呼叫 API;畫面不需要改。

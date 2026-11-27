# omixor console(Go + React + Vite 版)

`../mac`(macOS SwiftUI)的 Web 版本:後端 Go、畫面 React + Vite。MCP 走畫面連接埠的 `/mcp`,和 omixor server 同一套(OAuth 登入),見「MCP」一節。

## 執行

```bash
make build      # 先 npm run build 建畫面,再把它包進 Go 執行檔
./bin/omixor-web   # 畫面:http://127.0.0.1:5191(會自動開瀏覽器)  MCP:http://127.0.0.1:5191/mcp
```

環境變數以前叫 `OPS_*`(例如 `OPS_DATA_DIR`、`OPS_DATABASE_URL`),舊名稱仍然讀得到(新名稱沒設時才讀,啟動時 log 提醒改名),新的設定請用下表的名稱。

所有環境變數(名稱、舊名稱、型別、預設、說明、是否機密)集中定義在 [`core/config`](../core/config/vars.go),完整列表見 [docs/configuration.md](../docs/configuration.md),`omixor-web -h` 也會列出。常用的:`OMIXOR_WEB_PORT`、`OMIXOR_CONSOLE_DATA_DIR`、`OMIXOR_DATABASE_URL` + `OMIXOR_SECRET_KEY`(存資料庫)、`OMIXOR_NO_BROWSER`、`OMIXOR_PUBLIC_URL`。

> **升級須知(移除 3765 與 `omxm_`;MCP 只剩畫面連接埠的 `/mcp`)**——升級前先看,既有的 MCP 用戶端都要改:
>
> - **3765 連接埠、`omxm_` 權杖、`/mcp/worker`(不帶組織)沒有了**。`OMIXOR_MCP_PORT`、`OMIXOR_MCP_ADDR`、`OMIXOR_CONSOLE_MCP_TOKEN` 沒有作用(還設著時啟動會提醒),`.env` 的 `OMIXOR_MCP_HOST_PORT` 可以刪;console 資料夾裡的 `mcp_token` 檔可以刪。action `mcp_token_reveal`、`mcp_token_rotate` 與 view `mcp_access` 移除。
> - **Claude Code**:`claude mcp remove omixor` 後 `claude mcp add --transport http omixor <網址>/mcp`(不帶標頭),在瀏覽器授權。工具呼叫現在都要帶 `org`。
> - **omixor-mcp 要一起換新版**,先登入:`omixor-mcp login`(轉接)、`omixor-mcp login --worker --org <組織>`(任務頻道)。
> - **執行者資料夾要重新產生**:`omixor-mcp --init-worker <資料夾> --org <組織> --force`(舊的 `.mcp.json` 含 `omxm_`、指向 3765),進 Claude Code 後用 `/mcp` 授權。執行者只剩 `dispatch_*` 工具,讀不到 Issue 與文件。
> - **要有能登入的身分**:授權頁的帳號是 LDAP(認證設定啟用後)、GitLab 或 evoleap,而且是組織成員。**沒啟用任何登入(免登入模式)就沒有可用的 MCP**(角色端點除外)。
> - **沒有 MCP 的部署**:沒有 PostgreSQL。`voice_submit` 沒有任何 MCP 途徑(畫面仍可用)。
> - **OAuth 授權不能用的工具**:`agent_task_create`、`agent_task_claim`、`issue_item_claim`、`agent_task_transfer`(執行者改用 `dispatch_claim`);`commits_report`、`issue_commit_link`、`issue_commit_unlink`、`agent_context`、`channel_messages`、`wireframe_render` 也還沒開放。
> - **Docker**:不再發布 3765;compose 把 `OMIXOR_PUBLIC_URL` 預設成 `http://127.0.0.1:5191`(容器裡分不出「同一台電腦」)。
> - 自動執行的角色端點改成 `http://127.0.0.1:<畫面連接埠>/mcp/agent`,不用設定。
> - `omixor` CLI、手機、Mac App 與它自己的內建 MCP、omixor server 的 `omx_` 都不受影響。

`OMIXOR_CONSOLE_DATA_DIR` 的預設是 `~/Library/Application Support/omixor-console`。以前的預設是 `…/OpsWeb`:沒設環境變數、新資料夾不存在時,啟動會把舊資料夾改名成新的(改名失敗就繼續用舊的)。檔案格式和 Swift 版相同,可以指到 macOS App 的 `~/Library/Application Support/Ops` 共用資料,但兩個 app 不要同時開著改同一份。不叫 `OMIXOR_DATA_DIR` 是因為那個名稱是 omixor-local(核心守護服務)的資料夾。

啟動時環境變數會一次驗證(型別、範圍、互斥),有問題用中文一次列完再結束;機密(`OMIXOR_SECRET_KEY`、各種 token、密碼、資料庫網址裡的密碼)不會出現在日誌或錯誤訊息。

> **升級須知(移除遠端模式)**:console 以前可以不用自己的資料、改連另一台 omixor server(登入畫面的「選擇連接對象」)。這個模式已經移除,console 現在只是自建伺服器,資料在自己的 PostgreSQL。
>
> - **還設定成遠端模式的安裝會拒絕啟動**(不會悄悄開出一個空看板):`mode.json` 記著 `remote`,或沒有 `mode.json` 而設了 `OMIXOR_SERVER`。以前畫面上是那台 server 的資料的話,組織與 Issue 本來就在那台 server,請直接使用它。
> - **例外:紀錄是遠端、資料其實在本機**。舊版在 `mode.json` 寫 `remote`、但那台伺服器已經不在 `OMIXOR_SERVERS` / `servers.json` / `OMIXOR_SERVER` 裡時,會退回本機模式(不改 `mode.json`)。新版不再讀這些設定、分不出來,一樣拒絕啟動;如果你一直用的就是這台自己的資料,直接執行下面的 `leave-remote`(它只把模式記成本機,不刪任何東西)。
> - 要改用這台 console 自己的資料庫:用同樣的環境變數執行一次 `omixor-web leave-remote`(Docker:`docker compose run --rm <服務名稱> leave-remote`),再啟動。只靠 `OMIXOR_SERVER` 進遠端模式的,拿掉那個變數也可以。
> - `OMIXOR_SERVERS`、`servers.json`、`OMIXOR_SERVER_TOKEN`、`OMIXOR_EVOLEAP` 不再讀取;`OMIXOR_SERVER` 只剩 `omixor` CLI 使用(console 看到時只提醒)。
> - 以前的 `mode.json`、`servers.json`、`orgs-remote*.json`、鑰匙圈裡的登入**都不會刪**。遠端模式期間匯入、只存在這台的 Jira 圖片,檔案(或 `omixor_files` 的列)會保留,但 console **不再提供讀取**(它們屬於遠端組織,這台沒有那些組織)。
> - `omixor-web`、`omixor-mcp`、CLI、Mac App 連 omixor server 的方式不受影響(只有 console 自己的遠端模式移除)。

## PostgreSQL 與 Docker

console 一定要有 PostgreSQL(`OMIXOR_DATABASE_URL`):組織、Issue、角色與任務、文件櫃、組織鏈、訊息頻道、事件都在核心的資料表,沒設就不能用,不會退回存檔案。沒設資料庫時,只有這台機器的設定與狀態存成資料夾裡的 JSON 檔。

除了核心的資料表,console 自己用的有:

| 資料表 | 內容 |
|---|---|
| `omixor_documents` | 這台機器的設定 JSON 檔(目前組織、連線設定、GitHub/GCP/GitLab 設定、認證設定、旁白批次等),依路徑存成 jsonb;組織、Issue 等資料不在這裡 |
| `omixor_secrets` | 連線 token、client secret、LDAP 服務帳號密碼,用 `OMIXOR_SECRET_KEY` 以 AES-256-GCM 加密(帳號名稱當 AAD) |
| `omixor_sessions` | 只剩管理員修復登入的 session(只存 token 的 SHA-256);一般登入改存核心的 `omixor_tokens`(裝置名稱 `web:<帳號>`) |
| `omixor_activity` | 動態牆全部歷史(畫面載入最新 1000 筆,`activity_history` 查更早的) |
| `omixor_state` | 範例資料的變更(告警、事件、工作項目、部署…),每 10 秒與結束前存 |
| `omixor_files` | 舊版 console 自己存的附件;不再寫入(附件在核心的 `omixor_attachments`) |

> **升級須知(附件只存一份)**:Issue 的圖片(含 Jira 匯入的)都存在核心,畫面與 REST API 讀同一份。啟動時會把舊的附件(`omixor_files`、`<資料夾>/orgs/<組織>/attachments/`)複製進核心,id 與網址不變、可以重跑,舊資料保留(刪除組織時一起刪)。

舊版的表叫 `ops_` 開頭(`ops_documents`…);啟動時(`ensurePGSchema`)若發現舊表,連同索引、序號、主鍵一起改成 `omixor_` 開頭、資料保留,再建缺的表。新舊同時存在(改名後又跑過舊版程式)會報錯、不覆蓋,細節與回滾見 omixor 文件櫃 DOC-25《docs/design-schema-rename.md》。

用 Docker 開(含 PostgreSQL):

```bash
cp .env.example .env      # 填 POSTGRES_PASSWORD 與 OMIXOR_SECRET_KEY(openssl rand -base64 32)
docker compose up -d --build
```

畫面 http://127.0.0.1:5191、MCP http://127.0.0.1:5191/mcp,只發布到本機。

把現有資料搬進資料庫:`.env` 設 `OMIXOR_IMPORT_DIR`(例如 macOS App 的 `~/Library/Application Support/Ops`),再 `docker compose run --rm omixor-web import /import`。鑰匙圈裡的登入搬不過來,連線要重新登入。

- `OMIXOR_SECRET_KEY` 換掉的話,已存的 token 與密碼會解不開,要重新登入。
- 這個版本只支援一台 omixor-web 連同一個資料庫。
- 旁白批次要在有 shuttle 專案與 npm 的電腦上跑,Docker 映像裡沒有。
- 資料庫相關測試:`scripts/test-pg.sh`(用 Docker 開暫時的 PostgreSQL)。

## 登入(LDAP)

> **升級須知(管理員改看帳號名單)**:舊版的「管理員群組」不再使用,只有**帳號名單**裡的人是網頁管理員;名單空白就沒有任何管理員(以前管理員群組空白是「所有人都是管理員」)。
> 救援與設定:在主機上設 `OMIXOR_LDAP_ADMINS=帳號1,帳號2` 重新啟動;或用 `OMIXOR_AUTH=off`(畫面只開本機)啟動,到 **設定 › 認證** 填名單、用名單裡的帳號測試並儲存,再拿掉 `OMIXOR_AUTH=off`。

> **升級須知(登入改用核心的 web session,同埠多了 REST API)**:
> - **升級後所有人要重新登入一次**(舊的登入不再認得)。認證設定、已存的 LDAP 服務帳號密碼與環境變數照舊,不用重填。
> - 畫面的登入端點改成 `/api/auth/web/status|login|logout`(舊的 `/api/auth/status|login` 不存在了);登入回應改成 `{"result":{"user":…}}`。開著的舊分頁重新整理即可;自己寫的腳本要改路徑。
> - 登入限流:除了原本的帳號失敗鎖定(和 MCP 授權共用計數),多了**每個來源每分鐘 10 次、全部 60 次**。網頁登入和 MCP 授權頁**各算各的**;網頁登入密碼驗證通過的不佔名額(只有沒通過與進行中的佔),MCP 授權頁成功也計。放在反向代理後面一定要設 `OMIXOR_TRUSTED_PROXY_HOPS`(一層是 1,代理要帶 `X-Forwarded-For`;沒設時所有人算同一個來源,有人連續打錯就會讓大家暫時登不進)。層數是 0 卻收到轉送標頭時,log 會警告一次。
> - 署名與畫面上的使用者 email 取自核心使用者(帳號大小寫照目錄)。
> - 畫面連接埠多了 REST API(`/api/orgs…`),見下面「REST API」。

### 升級與救援(誰能登入、組織怎麼選)

**升級前先確認**(啟用 LDAP 的伺服器):

- **要是組織成員才能登入**。帳號要對得上 omixor 使用者、沒停用,而且是某個組織的成員、核心的伺服器管理員,或管理員帳號名單上的人。其他人登入回 403 並顯示原因;已登入的 session 下一個請求就失效。第一次登入被拒的人,使用者已經建好,到成員頁用他的 email 加入即可。啟動紀錄會列出對不上或停用的管理員、不屬於任何組織的 LDAP 帳號數。
- **每個人各自的目前組織**。畫面把選的組織存在瀏覽器,每個請求帶 `X-Omixor-Org`;沒帶用預設,帶了不能用的組織回 409 `orgChanged`。自己寫腳本呼叫 `/api` 的,要指定組織就帶這個標頭。
- **系統工作組織**。原本「全站的目前組織」改成系統工作組織(管理員用 `system_org_set` 換):整合、範例資料、監控、旁白、助理、虛擬辦公室、背景工作都只用它。**自動執行只執行系統工作組織的任務**。其他組織的成員用得到 Issue、待決定、事件、角色與任務、組織鏈、對話、文件櫃與設定;其他頁面在那些組織不顯示。換系統工作組織時,進行中的請求超過幾秒沒做完會回 409,稍後再試。
- **console 專有工具現在也在 OAuth MCP 入口**(`/mcp`),分級與限制見下面「遠端 MCP 用戶端」。已經授權過的連線會看到這些工具。
- **受限專案畫面和 API 同一套規則**:專案成員、組織管理員、伺服器管理員看得到(以前畫面只有管理員帳號名單的人看得到),詳見下面「受限專案」。

### 受限專案

誰看得到只有核心的一條規則(`core/service/project_access.go`):專案成員、組織管理員、伺服器管理員;其他人和不存在一樣。畫面用登入者本人的身分判斷,移出專案後下一個請求就看不到;寫入也照核心的權限(例如把 Issue 搬出受限專案要專案管理員)。

- 網頁管理員(管理員帳號名單)在畫面與 cookie 登入的 REST API 上是伺服器管理員,看得到全部;用 `omx_` Bearer token 與 MCP OAuth 授權時不升,要是專案成員或組織管理員。
- 沒啟用登入、`OMIXOR_API_TOKEN`、背景工作是系統身分,看得到全部。
- 專案成員的管理在畫面上仍只限網頁管理員。
- 事件與附件屬於整個組織、不分專案:組織成員看得到事件;知道附件網址(內容雜湊)的組織成員讀得到附件。

**救援**(進不去時):

1. **你在管理員名單上,但對不上 omixor 使用者**(例如先用 email 被預先加入而沒開 trustMail、目錄沒有 mail 也沒設 email 網域):照常登入,畫面只會給認證設定。開 trustMail 或填 email 網域,用自己的帳號測試並儲存,再重新登入。這個登入看不到任何組織資料,也不能關掉 LDAP。email 已經是另一個身分在用時,設定修不好:改走第 3 步。
2. **管理員的使用者被停用**:照樣不能登入。請另一位管理員重新啟用;沒有別人時,用 `OMIXOR_LDAP_ADMINS` 加一個能登入的帳號重新啟動,由他啟用。
3. **還是進不去**:在主機上用 `OMIXOR_AUTH=off` 啟動(畫面只開本機),到 設定 › 認證 修設定,或到成員頁加人、啟用使用者,再拿掉 `OMIXOR_AUTH=off`。

預設是「不用登入、只限本機」。要讓同事連進來:

1. 先不啟用登入、只聽本機開 omixor console,到 **設定 › 認證** 選「LDAP 帳號登入」,填伺服器(ldaps:// 或 ldap:// + StartTLS、自簽 CA)、找使用者的方式(服務帳號搜尋,或直接用 DN 範本 / AD 的 `%s@網域`)、允許登入的群組、**管理員帳號名單**。
2. 用名單裡的帳號按「測試登入」,成功後才能儲存(避免把自己鎖在外面;登入中的人也不能把自己移出名單)。服務帳號密碼存在鑰匙圈,不寫進 `auth.json`。
3. 用 `OMIXOR_WEB_ADDR=0.0.0.0:5191 OMIXOR_WEB_HOSTS=omixor.corp.example.com ./bin/omixor-web` 重開,前面放 HTTPS 反向代理。

- 畫面上的動作(確認告警、核准、發佈旁白、切換組織…)以登入者署名,記在動態牆。
- 登入是核心的 web session(一個一定會到期的 `omx_` token,有效「登入有效時數」),放在 HttpOnly + SameSite=Strict 的 cookie `omx_session`(Path=/api;HTTPS 下加 Secure),重開伺服器不用重新登入;存認證設定時全部失效。管理員修復登入(與沒有資料庫時)用 cookie `ops_session`。
- 同一帳號 10 分鐘內失敗 5 次就鎖(和 MCP 授權同一份計數),另有每來源與全部的每分鐘次數限制(網頁登入成功不佔名額;反向代理後面要設 `OMIXOR_TRUSTED_PROXY_HOPS`,見上面升級須知);空密碼一律擋(LDAP 的空密碼會被當成匿名登入成功);帳號會依篩選與 DN 規則跳脫。
- 只接受本機名稱與 `OMIXOR_WEB_HOSTS` 列的 Host / Origin;POST 一律要 JSON(防 CSRF)。
- 設定錯了進不去:用 `OMIXOR_AUTH=off` 啟動回到本機模式。
- 環境變數 `OMIXOR_LDAP_ADMINS`、`OMIXOR_LDAP_EMAIL_DOMAIN`、`OMIXOR_LDAP_TRUST_MAIL` 有設時蓋掉畫面的值。
- MCP 用戶端用 OAuth 登入(LDAP 帳號),見「MCP」一節;別台電腦要連得到,要設 `OMIXOR_PUBLIC_URL`。

### REST API

畫面連接埠提供和 omixor server 同一套的 REST API(`/api/orgs…`、`/api/admin/…`、`/api/system/…`、`/api/modules`、`GET /api/auth/me`、`POST /api/auth/logout`;契約見 `core/api`,實作與限制見 `internal/web/coreapi.go`)。需要 PostgreSQL(沒有時 404)。

- 認證:畫面登入的 cookie,或 `Authorization: Bearer omx_…`。cookie 的寫入要帶 `X-Omixor-CSRF: 1`,有 `Origin` 時要和 `Host` 相同(反向代理要保留 `Host`)。Host / Origin 白名單、POST 要 JSON 的規則和其他 `/api` 一樣。角色憑證不收(角色用 `/mcp/agent`)。
- 啟用 LDAP 時:只有 LDAP 帳號的使用者,而且和網頁登入同一套限制(沒停用、是某個組織成員或管理員);能做什麼照核心的組織權限。管理員帳號名單只讓 **cookie 登入** 的人成為伺服器管理員,Bearer 的 token 不升。
- 不在名單上的人,寫入只開放日常操作(Issue、子項、待決定、文件、頻道、事件、派任務與回報),其他寫入、`/api/admin/`、結構檢核一律 403;組織管理員在 API 上也一樣。
- 誰都不能用 API 建立、修改、刪除組織,或改使用者角色與停用(`POST /api/admin/users`)。
- 沒有登入與配對端點(`/api/auth/ldap|evoleap|gitlab|pair`),所以 console 目前不發給個人用的 `omx_` token。畫面的 web session(也是 `omx_`)在 MCP 端點不收。舊路徑 `/api/issues/{org}`(含 `OMIXOR_API_TOKEN`)行為不變。
- 沒啟用登入(`OMIXOR_AUTH=off`):以本機使用者身分,只接受 Host 是本機名稱的呼叫;不收 Bearer。

對外開放相關的變數(`OMIXOR_WEB_ADDR`、`OMIXOR_WEB_HOSTS`、`OMIXOR_AUTH=off`、`OMIXOR_LDAP_BIND_PASSWORD`、`OMIXOR_API_TOKEN`)的說明見 [docs/configuration.md](../docs/configuration.md)。

鑰匙圈服務名稱是 `com.tim72117.omixor-console.connection`(以前是 `com.tim72117.ops-web.connection`:新名稱讀不到時讀舊的,讀到就寫一份到新名稱)。
本機模式的身分是 `omixor-console`(以前是 `ops-web`):連本機 PostgreSQL 時,啟動會把核心資料裡的 `ops-web`(任務領取者、頻道成員、建立者…)改成新身分,可以重跑,失敗不影響啟動。

開發畫面:`./bin/omixor-web` 開著,另外 `cd ui && npm run dev`(http://127.0.0.1:5173,`/api` 轉給 5191)。

只支援 stdio 的 MCP 用戶端用 `bin/omixor-mcp`(見「MCP」一節)。

## MCP

console 只有一個 MCP 入口,在**畫面連接埠**,和 omixor server 同一套(`core/oauth` 的 `MCPMount`):`/mcp`(統一入口,每次呼叫帶 `org`)、`/mcp/org/{org}`、`/mcp/agent`(角色憑證)、`/mcp/worker/{org}`(任務執行者),以及 `/oauth/*`、`/.well-known/*`。使用者端點用 OAuth 登入。

| 部署 | 入口 |
|---|---|
| 有合格的 `OMIXOR_PUBLIC_URL`(https;本機位址可用 http) | 對外服務,網址 `<OMIXOR_PUBLIC_URL>/mcp` |
| 沒有(或不合格) | `http://127.0.0.1:<畫面連接埠>/mcp`,**只收同一台電腦直接連進來的**(來源與 Host 都是本機、沒有代理的轉送標頭)。要用 `127.0.0.1`,不要寫 `localhost` |
| 設了 `OMIXOR_OAUTH_ADDR` | 在那個獨立連接埠(見下面);畫面連接埠只剩本機的 `/mcp/agent` |
| 沒有 PostgreSQL、畫面只聽特定的非本機位址又沒設 `OMIXOR_PUBLIC_URL` | 沒有(`/mcp`、`/oauth/*` 回 404 說明原因) |

> **放在反向代理後面時,一定要設 `OMIXOR_PUBLIC_URL`,並讓代理帶 `X-Forwarded-For`**(同時設 `OMIXOR_TRUSTED_PROXY_HOPS`,見下一節)。沒設 `OMIXOR_PUBLIC_URL` 時,「只收本機」是看連線來源、`Host` 與轉送標頭判斷的;同一台電腦上的代理如果不加任何轉送標頭,console 分不出請求是不是外面來的:代理把 `Host` 改成 `127.0.0.1`(例如 nginx 只寫 `proxy_pass http://127.0.0.1:5191;`),或代理什麼 `Host` 都收、原樣轉發(外面的人自己送 `Host: 127.0.0.1`),**外面的人就連得到本機入口**(`/mcp`、`/oauth/*`、`/mcp/agent`;仍要通過 OAuth 或角色憑證,但授權頁可以被外人嘗試密碼)。所以只讓代理保留原本的 `Host` 並不夠。`/mcp/agent` 的本機判斷在設了 `OMIXOR_PUBLIC_URL` 後仍看轉送標頭,因此兩項都要做。這種代理設定對畫面本身也要小心:免登入模式下等於整個畫面對外公開。

授權頁要有能登入的身分(LDAP、GitLab、evoleap),而且是組織成員;本機使用者 `omixor-console` 不能授權,所以免登入模式沒有可用的 MCP(角色端點除外)。

```sh
claude mcp add --transport http omixor http://127.0.0.1:5191/mcp   # 之後在瀏覽器授權
```

### omixor-mcp(stdio 轉接、任務頻道)

```sh
omixor-mcp login [--url <…/mcp>] [--no-browser]        # 授權碼 + PKCE,本機回呼
omixor-mcp login --worker --org <組織>                 # 任務頻道用的執行者授權
omixor-mcp                                             # stdio 轉接到統一入口
omixor-mcp logout [--worker --org <組織>]
```

- 網址:`--url` 或 `OMIXOR_MCP_URL`;沒設是 `http://127.0.0.1:<OMIXOR_WEB_PORT>/mcp`。要和伺服器公告的完全相同。
- **沒有瀏覽器的機器**:`--no-browser` 印出授權網址,在別台開啟、允許後,瀏覽器會停在打不開的 `http://127.0.0.1:…/callback?…`,把整串貼回終端機(或用 SSH 轉發那個連接埠)。伺服器要能從那台瀏覽器連到(也就是要有 `OMIXOR_PUBLIC_URL`,或用 SSH 轉發畫面連接埠)。
- 登入資料在 `<OMIXOR_CONFIG_DIR 或 ~/.config/omixor>/mcp/`,只有本人可讀寫;access token 10 分鐘自動更新,30 天沒用要重新登入;多個行程共用時以檔案鎖避免 refresh token 重複使用(重複使用會撤銷整組授權)。
- 沒有 client credentials:無人值守的腳本要有人先登入一次。

## 用 LDAP 帳號授權 MCP

要讓別台電腦連,設 `OMIXOR_PUBLIC_URL`(對外的 https 網址),反向代理要轉發 `/mcp`、`/oauth/`、`/.well-known/`;不是本機名稱時,畫面也要照上面設 `OMIXOR_WEB_HOSTS`。放在反向代理後面要設 `OMIXOR_TRUSTED_PROXY_HOPS`(前面有幾層代理,一層是 1):沒設的話所有人在每 IP 限制裡算成同一個來源(代理的位址),稽核的來源也不對;沒有代理時不要設,否則呼叫者可以自己填來源。

同意頁在認證設定啟用 LDAP 時提供「LDAP 帳號登入」,通過後有確認頁;寫入署名是本人(核心使用者 `ldap:<帳號>`)。不需要 evoleap 時設 `OMIXOR_OAUTH_EVOLEAP_LOGIN=false`。

- 目錄沒有 mail 的帳號要設 email 網域,否則授權失敗。
- 組織成員手動加入:對方先授權一次(建立使用者,看不到任何組織),管理員再到成員頁用 email 加入。事先加入還沒授權過的人,要開「信任目錄的 mail」。
- 失敗鎖定:同一帳號 10 分鐘內錯 5 次,網頁登入與 MCP 授權共用同一份計數(`CORP\帳號`、`帳號@網域`、大小寫、全半形算同一個);密碼正確只清掉同一種寫法記的次數,其他寫法記的要等這一輪過期。
- 續期(約每 10 分鐘):服務帳號搜尋的設定會向目錄重新確認,帳號不存在、停用(AD `userAccountControl`、`accountExpires`、`nsAccountLock`,或被使用者篩選排除)或不在允許群組就撤銷授權;目錄連不上或 LDAP 登入關掉時暫停續期(不撤銷)。直接用帳號組 DN 的設定查不了,授權最長維持「登入有效時數」(預設 12 小時)就要重新輸入密碼。
- 管理員帳號名單只決定網頁管理員,不會讓人在 MCP 裡變成伺服器管理員。
- 撤銷與停用(web 管理員,目前只有 API):`POST /api/action/user_oauth_revoke {"email":…}`、`POST /api/action/user_disable {"email":…, "disabled": true}`。
- 資料庫裡既有的 `omx_` 使用者 token 在這些端點照舊可用(本機使用者與畫面的 web session 除外)。

## 遠端 MCP 用戶端(對外 MCP 連接埠:claude.ai、omx_ token、角色憑證)

要讓**遠端** MCP 用戶端連進來,除了掛在畫面連接埠的入口(`OMIXOR_PUBLIC_URL`),也可以改用獨立的對外 MCP 連接埠(只想公開 MCP、不公開畫面時),端點、認證與權限和 omixor server 相同。**預設不啟用**。

- **條件**:console 連 PostgreSQL(`OMIXOR_DATABASE_URL` + `OMIXOR_SECRET_KEY`);`OMIXOR_PUBLIC_URL` 是對外的 https 網址(只放網址,不含路徑;`localhost`、`127.0.0.1`、`[::1]` 可用 http,開發用);`OMIXOR_OAUTH_EVOLEAP_LOGIN` 在啟用 GitLab 或認證設定的 LDAP 時可以設 false(三種都沒有只會在啟動紀錄警告)。條件不合 console 直接拒絕啟動並說明原因。
- **環境變數**:`OMIXOR_OAUTH_ADDR`(例如 `127.0.0.1:8771`;設了才啟用)、`OMIXOR_PUBLIC_URL`、`OMIXOR_TRUSTED_PROXY_HOPS`(前面有幾層代理;Tailscale Funnel 是 1)、`EVOLEAP_URL`、`OMIXOR_EVOLEAP_CONSOLE`(多半不用設);要 GitLab 登入再設 `OMIXOR_GITLAB_*`(同 server,見 [docs/configuration.md](../docs/configuration.md);`OMIXOR_GITLAB_LINK_LDAP` 不支援)。
- **登入回呼**:evoleap 登記 `<OMIXOR_PUBLIC_URL>/oauth/evoleap/callback`;GitLab Application 的 Redirect URI 登記 `<OMIXOR_PUBLIC_URL>/oauth/gitlab/callback`。
- **端點與認證**(除了 `/oauth/*`、`/.well-known/*` 只有這四個):

  | 端點 | 認證 |
  |---|---|
  | `/mcp`(統一入口,每次呼叫帶 `org`) | `omx_` 使用者 token,或綁 `<public>/mcp` 的 OAuth access token |
  | `/mcp/org/{org}` | `omx_`,或綁這個組織網址的 OAuth access token |
  | `/mcp/agent` | 角色憑證 `<agentId>:<secret>`(領取任務時拿到,附的網址是 `<public>/mcp/agent`) |
  | `/mcp/worker/{org}` | `omx_`,或綁這個網址的執行者 OAuth 授權(scope `mcp:worker`;只有 `dispatch_*`,`dispatch_claim` 不回角色憑證密鑰) |

- **誰能登入**:evoleap 帳號要對得上 console 資料庫裡已經有的使用者(管理員先用 email 把人加進組織),對不上就拒絕、不自動建帳號。GitLab 同 server 的規則:通過 email 網域等檢查的新帳號會自動建成一般使用者(要加進組織才看得到東西),開放註冊的 GitLab 一定要設 `OMIXOR_GITLAB_EMAIL_DOMAINS`。**沒有配對碼**。認證設定啟用 LDAP 時也能用 LDAP 帳號(見上一節)。
- **`omx_` token 從哪來**:console 本機模式**沒有發放 `omx_` 的功能**(沒有登入與配對端點,畫面也沒有)。只有資料庫裡本來就有的(例如和 omixor-server 共用同一個資料庫)才能用,console 也不能列出或撤銷它們。
- **不開放**:本機使用者 `omixor-console`(伺服器管理員)不管用什麼 token 都會被拒;server 的 `/api/*` 一概沒有(所以 `omixor` CLI 不能對這個連接埠登入)。
- **工具**:核心工具的權限與 server 相同(OAuth 預設唯讀,同意畫面選「讀寫」才多開協作寫入且不是伺服器管理員;`omx_` 權限同該使用者)。寫入署名是登入的那位使用者或角色。console 專有工具見下一項。
- **console 專有工具**(兩種入口都一樣;只在 `/mcp`、只給 OAuth 授權,`omx_` 與其他端點看不到;每次帶 `org`,要是成員):
  - 唯讀:overview、list、get、server、voice_jobs、office_state、site_visitors、github、github_runs、github_run、github_pull、gcp、gcp_logs、gcp_metrics、fb_pages、fb_posts、fb_insights、fb_drafts(唯讀授權不能帶 `refresh`)
  - 讀寫:alert_update、work_item_create、work_item_move、pipeline_update、activity_post、office_move、issue_decision_withdraw、issue_decision_reopen、navigate
  - 組織管理員(`mcp:admin`、該組織管理員,**而且**在 console 的管理員帳號名單;沒啟用 LDAP 時改要核心的伺服器管理員):alert_create、log_append、request_approval、fb_draft_save
  - 不開放:voice_submit(在主機執行程式)
  - 受限專案照上面「受限專案」的規則看授權者本人(和同一入口的核心工具一樣)
  - issue_decision_withdraw、issue_decision_reopen、navigate 可用在自己的任何組織;其餘是整台一份的功能,`org` 只能是系統工作組織。啟用 LDAP 時要用 LDAP 帳號授權。navigate 只移動自己的畫面,沒啟用 LDAP 時拒絕。work_item_create 帶 `incident_id` 要 `mcp:admin` 且是組織管理員。
- **畫面快取**:從這個連接埠寫入後,console 重讀系統工作組織的快取,並通知看著各組織的畫面重讀。

用 Tailscale Funnel 在本機試(`<機器>.<tailnet>.ts.net` 換成自己的):

```sh
# 1. 只把對外 MCP 連接埠發布到公網(8771;不要轉發 5191)
tailscale funnel --bg 8771
# 2. 啟動 console(要有 PostgreSQL;Funnel 前面有 1 層代理)
OMIXOR_DATABASE_URL=postgres://… OMIXOR_SECRET_KEY=$(openssl rand -base64 32) \
OMIXOR_OAUTH_ADDR=127.0.0.1:8771 OMIXOR_PUBLIC_URL=https://<機器>.<tailnet>.ts.net OMIXOR_TRUSTED_PROXY_HOPS=1 \
./bin/omixor-web
# 3. evoleap 登記回呼 https://<機器>.<tailnet>.ts.net/oauth/evoleap/callback
# 4. claude.ai → 自訂 connector,網址填 https://<機器>.<tailnet>.ts.net/mcp
```

(`OMIXOR_SECRET_KEY` 要固定保存,每次換新會解不開已存的資料。)

## 任務執行者(Claude Code 自動領任務)

派發出去的任務(手動派、Issue 指派給角色、例行任務)可以由開著的 Claude Code 工作階段自動領取、執行、回報。
用的是 Claude Code 的 channels(研究預覽):`bin/omixor-mcp --channel` 定時讀某個組織執行者端點(`/mcp/worker/{org}`)的待領取任務,有新的就推進工作階段。console 和 omixor server 都能用。

```bash
./bin/omixor-mcp login --worker --org <組織>
./bin/omixor-mcp --init-worker <資料夾> --org <組織> [--url <…/mcp>] [--agent 角色] [--no-subagent]
cd <資料夾>
claude --dangerously-load-development-channels server:omixor-tasks   # 第一次用 /mcp 授權 omixor 伺服器
```

- `--init-worker` 產生 `.mcp.json`(註冊頻道 `omixor-tasks` 與執行者端點;**沒有任何 token**)與 `CLAUDE.md`(長期授權:收到通知不用問就領取,預設交給子代理執行,做完用 `dispatch_report` 回報)。範本在 `omixor-core/mcp/worker_template.go`。
- 執行者授權的身分是登入的那個人(要是組織成員),只能用 `dispatch_*`:領取、狀態、回報、留言、委派、放棄;拿不到角色憑證密鑰。這個資料夾讀不到 Issue 與文件。
- 授權一定要寫在 `CLAUDE.md`(或啟動時的第一則訊息):頻道事件在 Claude Code 裡不算使用者指令,光靠頻道的說明它會停下來問。
- 第一次啟動會問要不要啟用 MCP 伺服器與開發頻道,都選同意。工作階段要一直開著才收得到;只能在終端機用,桌面 app 的 Code 分頁不支援 channels。
- **自動/手動**:每個角色有「自動執行」開關(console 的執行者;只執行系統工作組織的任務,人工「開始執行」以按的人署名領取),關著時待領取的任務不會被自動領走,每筆任務有「開始執行」按鈕(一次只跑一筆);領任務前會先檢查 claude 是否已登入。
- **執行日誌**:每次執行的工具呼叫、伺服器確認、遮蔽結果都記下來,每個組織保留最近 50 筆(`reportedBy` 為 `role` 或 `runner`);畫面在「需要處理」(被擋住等人回覆的任務)與「執行日誌」兩個頁籤。細節見 omixor 文件櫃 DOC-28《docs/release-notes-2026-11.md》。
- 組織固定是 `--org` 指定的那一個;要接多個組織就各建一個資料夾。

## 3D 虛擬辦公室(/office/)

repo 最外層的 `office3d/`(three.js 檢視器)由 console 的畫面連接埠提供:開 http://127.0.0.1:5191/office/ 。
**後端下命令、瀏覽器(檢視器)負責尋路與走路**,抵達後回報後端。程式在 `internal/office/`(Hub、命令解析、靜態檔)、
`internal/web/office.go`(HTTP)、`internal/mcp/tools_office.go`(MCP)。

`OMIXOR_OFFICE_DIR`(設定表見 [docs/configuration.md](../docs/configuration.md))指定 office3d 資料夾,沒設時依序找 `<執行檔>/../../office3d`、`<執行檔>/../office3d`、`<執行檔>/office3d`、`<工作目錄>/office3d`、`<工作目錄>/../office3d`;有 `places.json` 才算找到。找不到只記 log:`/office/` 回 404、`/api/office/command` 回 503,console 照常啟動。

- 靜態檔每次從磁碟讀(不嵌進執行檔),只開放白名單:`office.glb`、`characters.glb`、`navgrid.json`、`places.json`、`viewer/index.html`、`viewer/js/<名稱>.js`;其他(.blend、.py、renders…)一律 404。用 `os.Root` 讀,`..` 與指到外面的符號連結都出不去。`/office/` 轉到 `/office/viewer/`。
- `/api/office/…` 和其他 `/api` 一樣經過本機 Host / Origin 檢查、POST 要 `Content-Type: application/json`、啟用 LDAP 時要登入。

### API

| 方法與路徑 | 說明 |
|---|---|
| `POST /api/office/command` | `{"text":"工程師 去 會議室"}` 或 `{"action":"goto"\|"find"\|"return"\|"stop","agents":["engineer"],"target":"meeting"}`,可加 `"wait":秒數`(0–120,等抵達才回)。回 `{"result":{"id","ids","viewers","commands":[命令],"waited"?,"finished"?}}`;角色或地點不合法 400 `{"error"}`;找不到 office3d 503 |
| `GET /api/office/events` | SSE(給檢視器):`hello {viewerId, primary}`、`snapshot {viewerId, primary, agents, active:[命令]}`、`command {id, action:{type,agents,target:{kind,id}}, description}`、`role {primary:true}`(接手當主檢視器)、`ping`(每 15 秒)。同時最多 8 個連線(超過 503);送不進去(太慢)的連線會被斷掉,檢視器重連時拿 snapshot |
| `POST /api/office/report` | 檢視器回報 `{viewerId, type:"ack"\|"arrived"\|"superseded"\|"state", commandId?, agent?, results?:[{agent,ok,error}], arrived?:{target,position,seated,where}, agents?:[office.state()]}` → `{"result":{"accepted","primary","reason"?}}` |
| `GET /api/office/state` | `{available, viewers:[{id,primary,connectedAt}], viewerCount, primary, strategy, lastReportAt, agents:[{id,name,reported,state,stateLabel,seated,position,yaw,task,where,place,commandId,updatedAt}], commands:[最近 50 個,新的在前]}` |

- **text 命令**:後端用 Go 版的命令解析(`internal/office/parse.go`,照 `office3d/viewer/js/command.js` 搬,測試會直接跑 command.js 比對結果)先驗證角色與地點,不合法回 400;多條(`;`、`;`、換行)拆成多個命令。檢視器只收到結構化的動作。後端沒有「目前選取的角色」,省略角色是錯誤。
- **命令狀態**(每個角色一個,命令的 `status` 是整理過的):`pending`(還沒寫出)、`delivered`(已送達檢視器)、`running`(檢視器開始執行)、`arrived`、`done`(stop)、`partial`(有的失敗)、`failed`(找不到路、角色沒載入、15 秒沒回應、檢視器全部斷線)、`superseded`(還沒抵達就被新命令或檢視器上的手動操作取代)、`no_viewer`(建立時沒有檢視器連線:不執行、之後也不補送)。最多保留 200 個命令(記憶體,重開就清空)。
- **多個檢視器**:命令推給全部(每個畫面都會動),但只採用**主檢視器**的回報 = 目前連線中最早連上的那一個;它斷線時下一個最早的接手(收到 `role`)。其他檢視器的回報回 `accepted:false`、不改狀態,所以各自模擬的小差異不會互相打架。

### MCP 工具(Go 版才有,定義在 `spec/tools_console.json`)

- `office_move`:參數同 `POST /api/office/command`(`text`,或 `action` + `agents` + `target`;`wait` 秒數)。回傳同上的 `result`。
- `office_state`:`{command?, limit?}` → 同 `GET /api/office/state`,另加 `places:[{id,name,kind,owner?}]`;給 `command` 只回那個命令。

`spec/tools.json` 是從 Swift 版匯出的(`dump_spec.py` 會整份覆蓋),所以 Go 版才有的工具手寫在 `spec/tools_console.json`,`tools/list` 時接在後面(不能和核心或 tools.json 撞名,`spec_test.go` 擋)。conformance 比對 Swift 版時,這兩個工具會是多出來的差異。

## MCP 規格一致

- `tools/list` = web 自己的工具(`spec/tools.json`)+ 核心工具(定義直接取自 `omixor-core/mcp.CoreToolDefs`,和 `/mcp/org` 同一份;
  開放 `model.CoreTools` 裡 Admin 的,另加 `agent_task_delegate`,見 `internal/mcp/handler.go`)。核心改了工具定義,web 自動跟著變,不用匯出。
- `spec/tools.json`(只有 web 自己的工具,和 Swift App 自己的工具同一份定義)、`spec/initialize.json` 是從執行中的 Swift 版匯出的
  (`python3 spec/dump_spec.py`,會濾掉核心工具)。Swift 版改了自己的工具就重新匯出;web 自己的工具不能和核心工具同名(`internal/mcp/spec_test.go` 擋)。
- 範本(文件櫃結構、示範資料、文件轉換器)在 repo 最外層的 `../templates/`,預設不載入;要用就設 `OMIXOR_TEMPLATES=1`(或指定資料夾),執行時才從磁碟讀,不編進執行檔。示範資料載入時時間平移到現在。細節見 `../templates/README.md`。
- 傳輸同 Swift 版:POST /mcp、通知回 202、GET/DELETE 回 405、帶非本機 Origin 回 403、不接受 chunked、body 上限 4 MB、Connection: close。
  (認證見上面「MCP」;上面的比對用工具集是不分身分的完整版,只由開發工具 `tools/conformance-mcp` 開在本機。)
- `conformance/`:同一串呼叫送給兩個版本比對回應(見 omixor 文件櫃 DOC-32《console/docs/PORTING.md》)。

## 程式結構

見 omixor 文件櫃 DOC-32《console/docs/PORTING.md》。

選用模組(編譯時決定,見 `internal/core/modules.go`):`cmd/omixor-web/modules.go` 用 `import _` 列出要編進去的模組,
`go build -tags omixor_minimal ./cmd/omixor-web` 只有核心。目前有 `internal/modules/integrations`(GitHub、GitLab:
PR、Actions、MR、Pipeline、設定 › 程式碼倉庫、github* MCP 工具)。沒編進來時 view / action 不存在、github* 工具不列在
tools/list(規格 `spec/tools.json` 照舊有,呼叫回「這台伺服器沒有編進這個工具」),畫面依 view「system」的 `modules`、
`webOnlyTabs` 與功能清單隱藏。

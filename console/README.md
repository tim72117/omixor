# omixor console(Go + React + Vite 版)

`../mac`(macOS SwiftUI)的 Web 版本:後端 Go、畫面 React + Vite。**MCP 橋接的規格和 Swift 版一模一樣**,Claude Code 等 MCP 用戶端換成連這個版本不用改任何設定。

## 執行

```bash
make build      # 先 npm run build 建畫面,再把它包進 Go 執行檔
./bin/omixor-web   # MCP:http://127.0.0.1:3765/mcp  畫面:http://127.0.0.1:5191(會自動開瀏覽器)
```

環境變數以前叫 `OPS_*`(例如 `OPS_DATA_DIR`、`OPS_DATABASE_URL`),舊名稱仍然讀得到(新名稱沒設時才讀,啟動時 log 提醒改名),新的設定請用下表的名稱。

所有環境變數(名稱、舊名稱、型別、預設、說明、是否機密)集中定義在 [`core/config`](../core/config/vars.go),完整列表見 [docs/configuration.md](../docs/configuration.md),`omixor-web -h` 也會列出。常用的:`OMIXOR_WEB_PORT`、`OMIXOR_MCP_PORT`、`OMIXOR_CONSOLE_DATA_DIR`、`OMIXOR_DATABASE_URL` + `OMIXOR_SECRET_KEY`(存資料庫)、`OMIXOR_NO_BROWSER`、`OMIXOR_MCP_ADDR`。

`OMIXOR_CONSOLE_DATA_DIR` 的預設是 `~/Library/Application Support/omixor-console`。以前的預設是 `…/OpsWeb`:沒設環境變數、新資料夾不存在時,啟動會把舊資料夾改名成新的(改名失敗就繼續用舊的)。檔案格式和 Swift 版相同,可以指到 macOS App 的 `~/Library/Application Support/Ops` 共用資料,但兩個 app 不要同時開著改同一份。不叫 `OMIXOR_DATA_DIR` 是因為那個名稱是 omixor-local(核心守護服務)的資料夾。

啟動時環境變數會一次驗證(型別、範圍、互斥),有問題用中文一次列完再結束;機密(`OMIXOR_SECRET_KEY`、各種 token、密碼、資料庫網址裡的密碼)不會出現在日誌或錯誤訊息。

## PostgreSQL 與 Docker

本機模式一定要有 PostgreSQL(`OMIXOR_DATABASE_URL`):組織、Issue、角色與任務、文件櫃、組織鏈、訊息頻道、事件都在核心的資料表,沒設就不能用,不會退回存檔案。遠端模式的這些資料在 omixor server;沒設資料庫時,只有這台機器的設定與狀態存成資料夾裡的 JSON 檔。

除了核心的資料表,console 自己用的有:

| 資料表 | 內容 |
|---|---|
| `omixor_documents` | 這台機器的設定 JSON 檔(模式、目前組織、連線設定、GitHub/GCP/GitLab 設定、認證設定、旁白批次等),依路徑存成 jsonb;組織、Issue 等資料不在這裡 |
| `omixor_secrets` | 連線 token、client secret、LDAP 服務帳號密碼,用 `OMIXOR_SECRET_KEY` 以 AES-256-GCM 加密(帳號名稱當 AAD) |
| `omixor_sessions` | 網頁登入的 session(只存 token 的 SHA-256),重開不用重新登入 |
| `omixor_activity` | 動態牆全部歷史(畫面載入最新 1000 筆,`activity_history` 查更早的) |
| `omixor_state` | 範例資料的變更(告警、事件、工作項目、部署…),每 10 秒與結束前存 |

舊版的表叫 `ops_` 開頭(`ops_documents`…);啟動時(`ensurePGSchema`)若發現舊表,連同索引、序號、主鍵一起改成 `omixor_` 開頭、資料保留,再建缺的表。新舊同時存在(改名後又跑過舊版程式)會報錯、不覆蓋,細節與回滾見 omixor 文件櫃 DOC-25《docs/design-schema-rename.md》。

用 Docker 開(含 PostgreSQL):

```bash
cp .env.example .env      # 填 POSTGRES_PASSWORD 與 OMIXOR_SECRET_KEY(openssl rand -base64 32)
docker compose up -d --build
```

畫面 http://127.0.0.1:5191、MCP http://127.0.0.1:3765/mcp,兩個都只發布到本機。macOS App(Swift 版)開著時把 `.env` 的 `OMIXOR_MCP_HOST_PORT` 改成 3766。

把現有資料搬進資料庫:`.env` 設 `OMIXOR_IMPORT_DIR`(例如 macOS App 的 `~/Library/Application Support/Ops`),再 `docker compose run --rm omixor-web import /import`。鑰匙圈裡的登入搬不過來,連線要重新登入。

- `OMIXOR_SECRET_KEY` 換掉的話,已存的 token 與密碼會解不開,要重新登入。
- 這個版本只支援一台 omixor-web 連同一個資料庫。
- 旁白批次要在有 shuttle 專案與 npm 的電腦上跑,Docker 映像裡沒有。
- 資料庫相關測試:`scripts/test-pg.sh`(用 Docker 開暫時的 PostgreSQL)。

## 登入(LDAP)

> **升級須知(管理員改看帳號名單)**:舊版的「管理員群組」不再使用,只有**帳號名單**裡的人是網頁管理員;名單空白就沒有任何管理員(以前管理員群組空白是「所有人都是管理員」)。
> 救援與設定:在主機上設 `OMIXOR_LDAP_ADMINS=帳號1,帳號2` 重新啟動;或用 `OMIXOR_AUTH=off`(畫面只開本機)啟動,到 **設定 › 認證** 填名單、用名單裡的帳號測試並儲存,再拿掉 `OMIXOR_AUTH=off`。

預設是「不用登入、只限本機」。要讓同事連進來:

1. 用本機模式開 omixor console,到 **設定 › 認證** 選「LDAP 帳號登入」,填伺服器(ldaps:// 或 ldap:// + StartTLS、自簽 CA)、找使用者的方式(服務帳號搜尋,或直接用 DN 範本 / AD 的 `%s@網域`)、允許登入的群組、**管理員帳號名單**。
2. 用名單裡的帳號按「測試登入」,成功後才能儲存(避免把自己鎖在外面;登入中的人也不能把自己移出名單)。服務帳號密碼存在鑰匙圈,不寫進 `auth.json`。
3. 用 `OMIXOR_WEB_ADDR=0.0.0.0:5191 OMIXOR_WEB_HOSTS=omixor.corp.example.com ./bin/omixor-web` 重開,前面放 HTTPS 反向代理。

- 畫面上的動作(確認告警、核准、發佈旁白、切換組織…)以登入者署名,記在動態牆。
- session 存在記憶體(重開要重新登入),cookie 是 HttpOnly + SameSite=Strict;同一帳號同一來源失敗 5 次鎖 5 分鐘;空密碼一律擋(LDAP 的空密碼會被當成匿名登入成功);帳號會依篩選與 DN 規則跳脫。
- 只接受本機名稱與 `OMIXOR_WEB_HOSTS` 列的 Host / Origin;POST 一律要 JSON(防 CSRF)。
- 設定錯了進不去:用 `OMIXOR_AUTH=off` 啟動回到本機模式。
- 環境變數 `OMIXOR_LDAP_ADMINS`、`OMIXOR_LDAP_EMAIL_DOMAIN`、`OMIXOR_LDAP_TRUST_MAIL` 有設時蓋掉畫面的值。
- **MCP(3765)不受影響**:和 Swift 版一樣只聽 127.0.0.1、不用登入(規格要一模一樣),所以 MCP 用戶端要在同一台電腦上。

對外開放相關的變數(`OMIXOR_WEB_ADDR`、`OMIXOR_WEB_HOSTS`、`OMIXOR_AUTH=off`、`OMIXOR_LDAP_BIND_PASSWORD`、`OMIXOR_API_TOKEN`、`OMIXOR_SERVER` / `OMIXOR_SERVER_TOKEN`)的說明見 [docs/configuration.md](../docs/configuration.md)。

鑰匙圈服務名稱是 `com.tim72117.omixor-console.connection`(以前是 `com.tim72117.ops-web.connection`:新名稱讀不到時讀舊的,讀到就寫一份到新名稱)。
本機模式的身分是 `omixor-console`(以前是 `ops-web`):連本機 PostgreSQL 時,啟動會把核心資料裡的 `ops-web`(任務領取者、頻道成員、建立者…)改成新身分,可以重跑,失敗不影響啟動。

開發畫面:`./bin/omixor-web` 開著,另外 `cd ui && npm run dev`(http://127.0.0.1:5173,`/api` 轉給 5191)。

只支援 stdio 的 MCP 用戶端用 `bin/omixor-mcp`(同 Swift 版的 omixor-mcp,`OMIXOR_MCP_URL` / `OMIXOR_MCP_PORT` 指定位址)。

## MCP 驗證(權杖)

console 的 MCP 連接埠(預設 `127.0.0.1:3765`)**一律要驗證**:`/mcp`、`/mcp/worker` 與其餘所有路徑都要帶 `Authorization: Bearer <權杖>`,
沒帶或不符回 401(`WWW-Authenticate: Bearer realm="omixor"`),同一來源失敗太多次回 429;沒有「本機免驗證」的例外。`/mcp/agent` 不受影響(照舊驗角色憑證)。

- **權杖是什麼**:`omxm_` 加 32 位元組隨機。console 第一次啟動時產生,之後沿用(不寫進 log)。`OMIXOR_CONSOLE_MCP_TOKEN`(至少 32 字元、不能含空白或換行)有設就以它為準(Docker、自動化用,設了之後畫面不能換新);有設但無效時 console 與 omixor-mcp 會拒絕啟動並說明,不會悄悄改用別把。
- **存在哪**:有資料庫時存在加密的機密表(`omixor_secrets`,用 `OMIXOR_SECRET_KEY` 加密),並鏡射一份到 console 資料夾的 `mcp_token`(權限 0600,給同一台機器上的 `omixor-mcp` 讀);沒有資料庫時只存 `mcp_token`。
- **怎麼取得**:管理員呼叫 action `mcp_token_reveal`(完整權杖,動態牆會記「查看了 MCP 權杖」);`mcp_access` view 只回遮罩的 `tokenHint`(`omxm_…` 加最後 4 碼)、`tokenSet`、範例指令。也可以直接讀 console 資料夾的 `mcp_token`(本機單人模式)。
  `mcp_token_rotate` 換新權杖,舊的立刻失效(環境變數指定的不能換)。啟用 LDAP 時這兩個 action 只有 web 管理員能用;沒啟用登入的本機單人模式,web 使用者就是本機管理員。
- **Claude Code(或其他支援 HTTP 的用戶端)**:

  ```bash
  claude mcp add --transport http omixor http://127.0.0.1:3765/mcp --header "Authorization: Bearer <TOKEN>"
  ```

  其他用戶端的 JSON 設定同理:`{"type":"http","url":"http://127.0.0.1:3765/mcp","headers":{"Authorization":"Bearer <TOKEN>"}}`。這份設定含權杖,不要提交到 git。
  已經連過的人:原本的設定要補上 `--header`(`claude mcp remove omixor` 後重新 add),否則會收到 401。
- **omixor-mcp**(stdio 轉接、`--channel`、`--init-worker`)自己帶權杖,來源優先序:`OMIXOR_CONSOLE_MCP_TOKEN` 環境變數 → console 資料夾的 `mcp_token`;兩處都讀不到會報錯並說明怎麼取得,不會無驗證硬連。
  `--init-worker` 產生的 `.mcp.json` 含權杖(權限 0600),資料夾在 git repo 裡時會自動加進該資料夾的 `.gitignore`,請不要提交。
- **Docker**:`.env` 設 `OMIXOR_CONSOLE_MCP_TOKEN=omxm_…`(`openssl rand -hex 24` 產生後加前綴),compose 會傳進容器;不設的話用畫面的 MCP 設定查看,或 `docker compose exec omixor-web cat <資料夾>/mcp_token`。
- 目前是**一把共享權杖**:持有者都是同一個身分(本機管理員),寫入的署名仍是 `MCP 用戶端`。每人各一把、署名成本機使用者隨 ISS-36。
- conformance:`conformance/run_go.sh` 啟動的 Go 版有自己的權杖(存在它的資料夾),`compare.py` 加 `--go-data-dir <資料夾>` 或設 `OMIXOR_CONSOLE_MCP_TOKEN` 帶上。

## 用 LDAP 帳號授權 MCP(和 omixor server 相同的入口)

設 `OMIXOR_PUBLIC_URL`(對外的 https 網址;本機試用可用 `http://127.0.0.1:5191`)、不設 `OMIXOR_OAUTH_ADDR`,畫面連接埠就多出 `/mcp`、`/mcp/org/{org}`、`/mcp/agent`、`/mcp/worker/{org}` 與 `/oauth/*`、`/.well-known/*`(需要 PostgreSQL、非遠端模式;條件不合只在啟動紀錄警告)。反向代理要一起轉發這些路徑;不是本機名稱時,畫面也要照上面設 `OMIXOR_WEB_HOSTS`。放在反向代理後面要設 `OMIXOR_TRUSTED_PROXY_HOPS`(前面有幾層代理,一層是 1):沒設的話所有人在每 IP 限制裡算成同一個來源(代理的位址),稽核的來源也不對;沒有代理時不要設,否則呼叫者可以自己填來源。

```sh
claude mcp add --transport http omixor-console <OMIXOR_PUBLIC_URL>/mcp   # 不用帶標頭,之後在瀏覽器授權
```

同意頁在認證設定啟用 LDAP 時提供「LDAP 帳號登入」,通過後有確認頁;寫入署名是本人(核心使用者 `ldap:<帳號>`)。不需要 evoleap 時設 `OMIXOR_OAUTH_EVOLEAP_LOGIN=false`。

- 目錄沒有 mail 的帳號要設 email 網域,否則授權失敗。
- 組織成員手動加入:對方先授權一次(建立使用者,看不到任何組織),管理員再到成員頁用 email 加入。事先加入還沒授權過的人,要開「信任目錄的 mail」。
- 失敗鎖定:同一帳號 10 分鐘內錯 5 次,網頁登入與 MCP 授權共用同一份計數(`CORP\帳號`、`帳號@網域` 算同一個)。
- 續期(約每 10 分鐘):服務帳號搜尋的設定會向目錄重新確認,帳號不存在、停用(AD `userAccountControl`、`accountExpires`、`nsAccountLock`,或被使用者篩選排除)或不在允許群組就撤銷授權;目錄連不上或 LDAP 登入關掉時暫停續期(不撤銷)。直接用帳號組 DN 的設定查不了,授權最長維持「登入有效時數」(預設 12 小時)就要重新輸入密碼。
- 管理員帳號名單只決定網頁管理員,不會讓人在 MCP 裡變成伺服器管理員。
- 撤銷與停用(web 管理員,目前只有 API):`POST /api/action/user_oauth_revoke {"email":…}`、`POST /api/action/user_disable {"email":…, "disabled": true}`。
- 3765、`omxm_`、`omx_` 照常可用。

## 遠端 MCP 用戶端(對外 MCP 連接埠:claude.ai、omx_ token、角色憑證)

console 的 MCP 連接埠(3765)只給同一台電腦、用 `omxm_` 權杖;要讓**遠端** MCP 用戶端連進來,除了上一節掛在畫面連接埠的入口,也可以改用獨立的對外 MCP 連接埠(只想公開 MCP、不公開畫面時),端點、認證與權限和 omixor server 相同。**預設不啟用**,既有的 MCP 連接埠、畫面連接埠、Claude Code 與 omixor-mcp 完全不受影響。

- **條件**:console 連 PostgreSQL(`OMIXOR_DATABASE_URL` + `OMIXOR_SECRET_KEY`)、不是遠端模式(沒設 `OMIXOR_SERVER`);`OMIXOR_PUBLIC_URL` 是對外的 https 網址(只放網址,不含路徑;`localhost`、`127.0.0.1`、`[::1]` 可用 http,開發用);`OMIXOR_OAUTH_EVOLEAP_LOGIN` 在啟用 GitLab 或認證設定的 LDAP 時可以設 false(三種都沒有只會在啟動紀錄警告)。條件不合 console 直接拒絕啟動並說明原因。
- **環境變數**:`OMIXOR_OAUTH_ADDR`(例如 `127.0.0.1:8771`;設了才啟用)、`OMIXOR_PUBLIC_URL`、`OMIXOR_TRUSTED_PROXY_HOPS`(前面有幾層代理;Tailscale Funnel 是 1)、`EVOLEAP_URL`、`OMIXOR_EVOLEAP_CONSOLE`(多半不用設);要 GitLab 登入再設 `OMIXOR_GITLAB_*`(同 server,見 [docs/configuration.md](../docs/configuration.md);`OMIXOR_GITLAB_LINK_LDAP` 不支援)。
- **登入回呼**:evoleap 登記 `<OMIXOR_PUBLIC_URL>/oauth/evoleap/callback`;GitLab Application 的 Redirect URI 登記 `<OMIXOR_PUBLIC_URL>/oauth/gitlab/callback`。
- **端點與認證**(除了 `/oauth/*`、`/.well-known/*` 只有這四個):

  | 端點 | 認證 |
  |---|---|
  | `/mcp`(統一入口,每次呼叫帶 `org`) | `omx_` 使用者 token,或綁 `<public>/mcp` 的 OAuth access token |
  | `/mcp/org/{org}` | `omx_`,或綁這個組織網址的 OAuth access token |
  | `/mcp/agent` | 角色憑證 `<agentId>:<secret>`(領取任務時拿到,附的網址是 `<public>/mcp/agent`) |
  | `/mcp/worker/{org}` | `omx_` |

- **誰能登入**:evoleap 帳號要對得上 console 資料庫裡已經有的使用者(管理員先用 email 把人加進組織),對不上就拒絕、不自動建帳號。GitLab 同 server 的規則:通過 email 網域等檢查的新帳號會自動建成一般使用者(要加進組織才看得到東西),開放註冊的 GitLab 一定要設 `OMIXOR_GITLAB_EMAIL_DOMAINS`。**沒有配對碼**。認證設定啟用 LDAP 時也能用 LDAP 帳號(見上一節)。
- **`omx_` token 從哪來**:console 本機模式**沒有發放 `omx_` 的功能**(沒有 `/api/auth/*`,畫面也沒有)。只有資料庫裡本來就有的(例如和 omixor-server 共用同一個資料庫)才能用,console 也不能列出或撤銷它們。
- **不開放**:`omxm_` 權杖在這個連接埠無效;本機使用者 `omixor-console`(伺服器管理員)不管用什麼 token 都會被拒;server 的 `/api/*` 一概沒有(所以 `omixor` CLI 不能對這個連接埠登入)。
- **工具**:全部是核心的工具,權限與 server 相同(OAuth 預設唯讀,同意畫面選「讀寫」才多開協作寫入且不是伺服器管理員;`omx_` 權限同該使用者)。console 自己的工具(gcp、github、fb_、voice_、server、範例資料、office)**不會**出現。寫入署名是登入的那位使用者或角色。
- **畫面快取**:從這個連接埠寫入後,console 重讀目前組織的 Issue、角色、文件櫃;寫到別的組織的,切換過去時才讀。

用 Tailscale Funnel 在本機試(`<機器>.<tailnet>.ts.net` 換成自己的):

```sh
# 1. 只把對外 MCP 連接埠發布到公網(8771;不要轉發 3765 或 5191)
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
用的是 Claude Code 的 channels(研究預覽):`bin/omixor-mcp --channel` 定時讀這台 console 目前組織的待領取任務,有新的就推進工作階段。

```bash
./bin/omixor-mcp --init-worker <資料夾> [--agent 角色] [--no-subagent]
cd <資料夾>
claude --dangerously-load-development-channels server:omixor-tasks
```

- `--init-worker` 產生 `.mcp.json`(註冊頻道 `omixor-tasks` 與本機 omixor MCP)與 `CLAUDE.md`(長期授權:收到通知不用問就領取,預設交給子代理執行,做完用 `agent_task_report` 回報)。範本在 `omixor-core/mcp/worker_template.go`。
- 授權一定要寫在 `CLAUDE.md`(或啟動時的第一則訊息):頻道事件在 Claude Code 裡不算使用者指令,光靠頻道的說明它會停下來問。
- 第一次啟動會問要不要啟用 MCP 伺服器與開發頻道,都選同意。工作階段要一直開著才收得到;只能在終端機用,桌面 app 的 Code 分頁不支援 channels。
- **自動/手動**:每個角色有「自動執行」開關(console 的執行者),關著時待領取的任務不會被自動領走,每筆任務有「開始執行」按鈕(一次只跑一筆);領任務前會先檢查 claude 是否已登入。
- **執行日誌**:每次執行的工具呼叫、伺服器確認、遮蔽結果都記下來,每個組織保留最近 50 筆(`reportedBy` 為 `role` 或 `runner`);畫面在「需要處理」(被擋住等人回覆的任務)與「執行日誌」兩個頁籤。細節見 omixor 文件櫃 DOC-28《docs/release-notes-2026-11.md》。
- 頻道跟著 console 目前的組織(每次查詢重讀):console 晚開、中途換組織都跟得上,換組織時會先送一則新組織的待領清單。

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
- 傳輸同 Swift 版:只聽 127.0.0.1、POST /mcp、通知回 202、GET/DELETE 回 405、帶非本機 Origin 回 403、不接受 chunked、body 上限 4 MB、Connection: close。
  (Go 版比 Swift 版多一道驗證:要 Bearer 權杖,見上面「MCP 驗證」;Swift 版沒有。)
- `conformance/`:同一串呼叫送給兩個版本比對回應(見 omixor 文件櫃 DOC-32《console/docs/PORTING.md》)。

## 程式結構

見 omixor 文件櫃 DOC-32《console/docs/PORTING.md》。

選用模組(編譯時決定,見 `internal/core/modules.go`):`cmd/omixor-web/modules.go` 用 `import _` 列出要編進去的模組,
`go build -tags omixor_minimal ./cmd/omixor-web` 只有核心。目前有 `internal/modules/integrations`(GitHub、GitLab:
PR、Actions、MR、Pipeline、設定 › 程式碼倉庫、github* MCP 工具)。沒編進來時 view / action 不存在、github* 工具不列在
tools/list(規格 `spec/tools.json` 照舊有,呼叫回「這台伺服器沒有編進這個工具」),畫面依 view「system」的 `modules`、
`webOnlyTabs` 與功能清單隱藏。

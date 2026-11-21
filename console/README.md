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

設了 `OMIXOR_DATABASE_URL` 就改存 PostgreSQL(沒設照舊存資料夾裡的 JSON 檔,格式和 Swift 版相同):

| 資料表 | 內容 |
|---|---|
| `ops_documents` | 原本的 JSON 檔(組織、Issue、角色與任務、文件櫃、連線設定、GitHub/GCP/GitLab 設定、認證設定、旁白批次),依路徑存成 jsonb |
| `ops_secrets` | 連線 token、client secret、LDAP 服務帳號密碼,用 `OMIXOR_SECRET_KEY` 以 AES-256-GCM 加密(帳號名稱當 AAD) |
| `ops_sessions` | 網頁登入的 session(只存 token 的 SHA-256),重開不用重新登入 |
| `ops_activity` | 動態牆全部歷史(畫面載入最新 1000 筆,`activity_history` 查更早的) |
| `ops_state` | 範例資料的變更(告警、事件、工作項目、部署…),每 10 秒與結束前存 |

資料表名稱沿用舊的 `ops_` 前綴(改名要搬既有資料庫,目前不改)。

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

預設是「不用登入、只限本機」。要讓同事連進來:

1. 用本機模式開 omixor console,到 **設定 › 認證** 選「LDAP 帳號登入」,填伺服器(ldaps:// 或 ldap:// + StartTLS、自簽 CA)、找使用者的方式(服務帳號搜尋,或直接用 DN 範本 / AD 的 `%s@網域`)、允許登入與管理員的群組。
2. 用一組**管理員群組**的帳號按「測試登入」,成功後才能儲存(避免把自己鎖在外面)。服務帳號密碼存在鑰匙圈,不寫進 `auth.json`。
3. 用 `OMIXOR_WEB_ADDR=0.0.0.0:5191 OMIXOR_WEB_HOSTS=omixor.corp.example.com ./bin/omixor-web` 重開,前面放 HTTPS 反向代理。

- 畫面上的動作(確認告警、核准、發佈旁白、切換組織…)以登入者署名,記在動態牆。
- session 存在記憶體(重開要重新登入),cookie 是 HttpOnly + SameSite=Strict;同一帳號同一來源失敗 5 次鎖 5 分鐘;空密碼一律擋(LDAP 的空密碼會被當成匿名登入成功);帳號會依篩選與 DN 規則跳脫。
- 只接受本機名稱與 `OMIXOR_WEB_HOSTS` 列的 Host / Origin;POST 一律要 JSON(防 CSRF)。
- 設定錯了進不去:用 `OMIXOR_AUTH=off` 啟動回到本機模式。
- **MCP 不受影響**:和 Swift 版一樣只聽 127.0.0.1、不用登入(規格要一模一樣),所以 MCP 用戶端要在同一台電腦上。

對外開放相關的變數(`OMIXOR_WEB_ADDR`、`OMIXOR_WEB_HOSTS`、`OMIXOR_AUTH=off`、`OMIXOR_LDAP_BIND_PASSWORD`、`OMIXOR_API_TOKEN`、`OMIXOR_SERVER` / `OMIXOR_SERVER_TOKEN`)的說明見 [docs/configuration.md](../docs/configuration.md)。

鑰匙圈服務名稱是 `com.tim72117.omixor-console.connection`(以前是 `com.tim72117.ops-web.connection`:新名稱讀不到時讀舊的,讀到就寫一份到新名稱)。
本機模式的身分是 `omixor-console`(以前是 `ops-web`):連本機 PostgreSQL 時,啟動會把核心資料裡的 `ops-web`(任務領取者、頻道成員、建立者…)改成新身分,可以重跑,失敗不影響啟動。

開發畫面:`./bin/omixor-web` 開著,另外 `cd ui && npm run dev`(http://127.0.0.1:5173,`/api` 轉給 5191)。

只支援 stdio 的 MCP 用戶端用 `bin/omixor-mcp`(同 Swift 版的 omixor-mcp,`OMIXOR_MCP_URL` / `OMIXOR_MCP_PORT` 指定位址)。

## MCP 驗證(權杖)

console 的 MCP 連接埠(預設 `127.0.0.1:3765`)**一律要驗證**:`/mcp`、`/mcp/worker` 與其餘所有路徑都要帶 `Authorization: Bearer <權杖>`,
沒帶或不符回 401(`WWW-Authenticate: Bearer realm="omixor"`),同一來源失敗太多次回 429;沒有「本機免驗證」的例外。`/mcp/agent` 不受影響(照舊驗角色憑證)。

- **權杖是什麼**:`omxm_` 加 32 位元組隨機。console 第一次啟動時產生,之後沿用(不寫進 log)。`OMIXOR_CONSOLE_MCP_TOKEN`(至少 32 字元、不能含空白或換行)有設就以它為準(Docker、自動化用,設了之後畫面不能換新);有設但無效時 console 與 omixor-mcp 會拒絕啟動並說明,不會悄悄改用別把。
- **存在哪**:有資料庫時存在加密的機密表(`ops_secrets`,用 `OMIXOR_SECRET_KEY` 加密),並鏡射一份到 console 資料夾的 `mcp_token`(權限 0600,給同一台機器上的 `omixor-mcp` 讀);沒有資料庫時只存 `mcp_token`。
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
- `conformance/`:同一串呼叫送給兩個版本比對回應(見 `docs/PORTING.md`)。

## 程式結構

見 `docs/PORTING.md`。

選用模組(編譯時決定,見 `internal/core/modules.go`):`cmd/omixor-web/modules.go` 用 `import _` 列出要編進去的模組,
`go build -tags omixor_minimal ./cmd/omixor-web` 只有核心。目前有 `internal/modules/integrations`(GitHub、GitLab:
PR、Actions、MR、Pipeline、設定 › 程式碼倉庫、github* MCP 工具)。沒編進來時 view / action 不存在、github* 工具不列在
tools/list(規格 `spec/tools.json` 照舊有,呼叫回「這台伺服器沒有編進這個工具」),畫面依 view「system」的 `modules`、
`webOnlyTabs` 與功能清單隱藏。

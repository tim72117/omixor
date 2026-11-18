# omixor console(Go + React + Vite 版)

`../mac`(macOS SwiftUI)的 Web 版本:後端 Go、畫面 React + Vite。**MCP 橋接的規格和 Swift 版一模一樣**,Claude Code 等 MCP 用戶端換成連這個版本不用改任何設定。

## 執行

```bash
make build      # 先 npm run build 建畫面,再把它包進 Go 執行檔
./bin/omixor-web   # MCP:http://127.0.0.1:3765/mcp  畫面:http://127.0.0.1:5191(會自動開瀏覽器)
```

環境變數以前叫 `OPS_*`(例如 `OPS_DATA_DIR`、`OPS_DATABASE_URL`),舊名稱仍然讀得到(新名稱沒設時才讀,啟動時 log 提醒改名),新的設定請用下表的名稱。

| 環境變數 | 預設 | 說明 |
|---|---|---|
| `OMIXOR_MCP_PORT` | 3765 | MCP 連接埠(和 Swift 版相同;兩個同時開時給其中一個換埠) |
| `OMIXOR_WEB_PORT` | 5191 | 畫面與 API 的連接埠 |
| `OMIXOR_CONSOLE_DATA_DIR`(舊 `OPS_DATA_DIR`) | `~/Library/Application Support/omixor-console` | 資料夾。以前的預設是 `…/OpsWeb`:沒設環境變數、新資料夾不存在時,啟動會把舊資料夾改名成新的(改名失敗就繼續用舊的)。檔案格式和 Swift 版相同,可以指到 macOS App 的 `~/Library/Application Support/Ops` 共用資料,但兩個 app 不要同時開著改同一份。不叫 `OMIXOR_DATA_DIR` 是因為那個名稱是 omixor-local(核心守護服務)的資料夾 |
| `OMIXOR_NO_BROWSER` | | 設了就不自動開瀏覽器 |
| `OMIXOR_NO_TIMERS` | | 設了就不開計時器(測試用) |
| `OMIXOR_DATABASE_URL` | | PostgreSQL 連線網址;設了就存資料庫 |
| `OMIXOR_SECRET_KEY` | | 用資料庫時必填:base64 的 32 bytes,加密 token 與密碼 |
| `OMIXOR_MCP_ADDR` | 127.0.0.1 | MCP 聽的位址(Docker 裡設 0.0.0.0,連接埠只發布到主機的 127.0.0.1) |

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

| 環境變數 | 說明 |
|---|---|
| `OMIXOR_WEB_ADDR` | 畫面聽的位址,例如 `0.0.0.0:5191`;不是本機位址時必須先啟用 LDAP |
| `OMIXOR_WEB_HOSTS` | 允許的主機名稱(逗號分隔),反向代理的網域要列在這裡 |
| `OMIXOR_AUTH=off` | 暫時關掉登入 |
| `OMIXOR_LDAP_BIND_PASSWORD` | 服務帳號密碼改從環境變數讀(不想用鑰匙圈時) |
| `OMIXOR_API_TOKEN` | `/api/issues/` 可以用的 Bearer token(給 macOS App 的「Issue 伺服器」) |
| `OMIXOR_SERVER` / `OMIXOR_SERVER_TOKEN` | 遠端模式的 omixor server 網址與固定 token(舊名稱 `OPS_ISSUE_SERVER` / `OPS_ISSUE_SERVER_TOKEN`) |

鑰匙圈服務名稱是 `com.tim72117.omixor-console.connection`(以前是 `com.tim72117.ops-web.connection`:新名稱讀不到時讀舊的,讀到就寫一份到新名稱)。
本機模式的身分是 `omixor-console`(以前是 `ops-web`):連本機 PostgreSQL 時,啟動會把核心資料裡的 `ops-web`(任務領取者、頻道成員、建立者…)改成新身分,可以重跑,失敗不影響啟動。

開發畫面:`./bin/omixor-web` 開著,另外 `cd ui && npm run dev`(http://127.0.0.1:5173,`/api` 轉給 5191)。

只支援 stdio 的 MCP 用戶端用 `bin/omixor-mcp`(同 Swift 版的 omixor-mcp,`OMIXOR_MCP_URL` / `OMIXOR_MCP_PORT` 指定位址)。

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

## MCP 規格一致

- `tools/list` = web 自己的工具(`spec/tools.json`)+ 核心工具(定義直接取自 `omixor-core/mcp.CoreToolDefs`,和 `/mcp/org` 同一份;
  開放 `model.CoreTools` 裡 Admin 的,另加 `agent_task_delegate`,見 `internal/mcp/handler.go`)。核心改了工具定義,web 自動跟著變,不用匯出。
- `spec/tools.json`(只有 web 自己的工具,和 Swift App 自己的工具同一份定義)、`spec/initialize.json` 是從執行中的 Swift 版匯出的
  (`python3 spec/dump_spec.py`,會濾掉核心工具)。Swift 版改了自己的工具就重新匯出;web 自己的工具不能和核心工具同名(`internal/mcp/spec_test.go` 擋)。
- 範本(文件櫃結構、示範資料、文件轉換器)在 repo 最外層的 `../templates/`,預設不載入;要用就設 `OMIXOR_TEMPLATES=1`(或指定資料夾),執行時才從磁碟讀,不編進執行檔。示範資料載入時時間平移到現在。細節見 `../templates/README.md`。
- 傳輸同 Swift 版:只聽 127.0.0.1、POST /mcp、通知回 202、GET/DELETE 回 405、帶非本機 Origin 回 403、不接受 chunked、body 上限 4 MB、Connection: close。
- `conformance/`:同一串呼叫送給兩個版本比對回應(見 `docs/PORTING.md`)。

## 程式結構

見 `docs/PORTING.md`。

選用模組(編譯時決定,見 `internal/core/modules.go`):`cmd/omixor-web/modules.go` 用 `import _` 列出要編進去的模組,
`go build -tags omixor_minimal ./cmd/omixor-web` 只有核心。目前有 `internal/modules/integrations`(GitHub、GitLab:
PR、Actions、MR、Pipeline、設定 › 程式碼倉庫、github* MCP 工具)。沒編進來時 view / action 不存在、github* 工具不列在
tools/list(規格 `spec/tools.json` 照舊有,呼叫回「這台伺服器沒有編進這個工具」),畫面依 view「system」的 `modules`、
`webOnlyTabs` 與功能清單隱藏。

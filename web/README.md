# Ops(Go + React + Vite 版)

`../ops`(macOS SwiftUI)的 Web 版本:後端 Go、畫面 React + Vite。**MCP 橋接的規格和 Swift 版一模一樣**,Claude Code 等 MCP 用戶端換成連這個版本不用改任何設定。

## 執行

```bash
make build      # 先 npm run build 建畫面,再把它包進 Go 執行檔
./bin/omixor-web   # MCP:http://127.0.0.1:3765/mcp  畫面:http://127.0.0.1:5191(會自動開瀏覽器)
```

| 環境變數 | 預設 | 說明 |
|---|---|---|
| `OPS_MCP_PORT` | 3765 | MCP 連接埠(和 Swift 版相同;兩個同時開時給其中一個換埠) |
| `OPS_WEB_PORT` | 5191 | 畫面與 API 的連接埠 |
| `OPS_DATA_DIR` | `~/Library/Application Support/OpsWeb` | 資料夾。檔案格式和 Swift 版相同,可以指到 Swift 版的 `~/Library/Application Support/Ops` 共用資料,但兩個 app 不要同時開著改同一份 |
| `OPS_NO_BROWSER` | | 設了就不自動開瀏覽器 |
| `OPS_NO_TIMERS` | | 設了就不開計時器(測試用) |
| `OPS_DATABASE_URL` | | PostgreSQL 連線網址;設了就存資料庫 |
| `OPS_SECRET_KEY` | | 用資料庫時必填:base64 的 32 bytes,加密 token 與密碼 |
| `OPS_MCP_ADDR` | 127.0.0.1 | MCP 聽的位址(Docker 裡設 0.0.0.0,連接埠只發布到主機的 127.0.0.1) |

## PostgreSQL 與 Docker

設了 `OPS_DATABASE_URL` 就改存 PostgreSQL(沒設照舊存資料夾裡的 JSON 檔,格式和 Swift 版相同):

| 資料表 | 內容 |
|---|---|
| `ops_documents` | 原本的 JSON 檔(組織、Issue、角色與任務、文件櫃、連線設定、GitHub/GCP/GitLab 設定、認證設定、旁白批次),依路徑存成 jsonb |
| `ops_secrets` | 連線 token、client secret、LDAP 服務帳號密碼,用 `OPS_SECRET_KEY` 以 AES-256-GCM 加密(帳號名稱當 AAD) |
| `ops_sessions` | 網頁登入的 session(只存 token 的 SHA-256),重開不用重新登入 |
| `ops_activity` | 動態牆全部歷史(畫面載入最新 1000 筆,`activity_history` 查更早的) |
| `ops_state` | 範例資料的變更(告警、事件、工作項目、部署…),每 10 秒與結束前存 |

用 Docker 開(含 PostgreSQL):

```bash
cp .env.example .env      # 填 POSTGRES_PASSWORD 與 OPS_SECRET_KEY(openssl rand -base64 32)
docker compose up -d --build
```

畫面 http://127.0.0.1:5191、MCP http://127.0.0.1:3765/mcp,兩個都只發布到本機。Swift 版開著時把 `.env` 的 `OPS_MCP_HOST_PORT` 改成 3766。

把現有資料搬進資料庫:`.env` 設 `OPS_IMPORT_DIR`(例如 Swift 版的 `~/Library/Application Support/Ops`),再 `docker compose run --rm omixor-web import /import`。鑰匙圈裡的登入搬不過來,連線要重新登入。

- `OPS_SECRET_KEY` 換掉的話,已存的 token 與密碼會解不開,要重新登入。
- 這個版本只支援一台 omixor-web 連同一個資料庫。
- 旁白批次要在有 shuttle 專案與 npm 的電腦上跑,Docker 映像裡沒有。
- 資料庫相關測試:`scripts/test-pg.sh`(用 Docker 開暫時的 PostgreSQL)。

## 登入(LDAP)

預設是「不用登入、只限本機」。要讓同事連進來:

1. 用本機模式開 Ops,到 **設定 › 認證** 選「LDAP 帳號登入」,填伺服器(ldaps:// 或 ldap:// + StartTLS、自簽 CA)、找使用者的方式(服務帳號搜尋,或直接用 DN 範本 / AD 的 `%s@網域`)、允許登入與管理員的群組。
2. 用一組**管理員群組**的帳號按「測試登入」,成功後才能儲存(避免把自己鎖在外面)。服務帳號密碼存在鑰匙圈,不寫進 `auth.json`。
3. 用 `OPS_WEB_ADDR=0.0.0.0:5191 OPS_WEB_HOSTS=ops.corp.example.com ./bin/omixor-web` 重開,前面放 HTTPS 反向代理。

- 畫面上的動作(確認告警、核准、發佈旁白、切換組織…)以登入者署名,記在動態牆。
- session 存在記憶體(重開要重新登入),cookie 是 HttpOnly + SameSite=Strict;同一帳號同一來源失敗 5 次鎖 5 分鐘;空密碼一律擋(LDAP 的空密碼會被當成匿名登入成功);帳號會依篩選與 DN 規則跳脫。
- 只接受本機名稱與 `OPS_WEB_HOSTS` 列的 Host / Origin;POST 一律要 JSON(防 CSRF)。
- 設定錯了進不去:用 `OPS_AUTH=off` 啟動回到本機模式。
- **MCP 不受影響**:和 Swift 版一樣只聽 127.0.0.1、不用登入(規格要一模一樣),所以 MCP 用戶端要在同一台電腦上。

| 環境變數 | 說明 |
|---|---|
| `OPS_WEB_ADDR` | 畫面聽的位址,例如 `0.0.0.0:5191`;不是本機位址時必須先啟用 LDAP |
| `OPS_WEB_HOSTS` | 允許的主機名稱(逗號分隔),反向代理的網域要列在這裡 |
| `OPS_AUTH=off` | 暫時關掉登入 |
| `OPS_LDAP_BIND_PASSWORD` | 服務帳號密碼改從環境變數讀(不想用鑰匙圈時) |

開發畫面:`./bin/omixor-web` 開著,另外 `cd ui && npm run dev`(http://127.0.0.1:5173,`/api` 轉給 5191)。

只支援 stdio 的 MCP 用戶端用 `bin/omixor-mcp`(同 Swift 版的 omixor-mcp,`OPS_MCP_URL` / `OPS_MCP_PORT` 指定位址)。

## MCP 規格一致

- `spec/tools.json`、`spec/initialize.json` 是從執行中的 Swift 版匯出的(`python3 spec/dump_spec.py`),Go 版的 `tools/list`、`initialize` 直接回傳它們。Swift 版改了工具就重新匯出。
- 範本(文件櫃結構、示範資料、文件轉換器)在 repo 最外層的 `../templates/`,預設不載入;要用就設 `OMIXOR_TEMPLATES=1`(或指定資料夾),執行時才從磁碟讀,不編進執行檔。示範資料載入時時間平移到現在。細節見 `../templates/README.md`。
- 傳輸同 Swift 版:只聽 127.0.0.1、POST /mcp、通知回 202、GET/DELETE 回 405、帶非本機 Origin 回 403、不接受 chunked、body 上限 4 MB、Connection: close。
- `conformance/`:同一串呼叫送給兩個版本比對回應(見 `docs/PORTING.md`)。

## 程式結構

見 `docs/PORTING.md`。

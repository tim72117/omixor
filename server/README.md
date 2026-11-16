# omixor-server

omixor 的線上服務(Cloud Run,服務名稱 `omixor`,網址 https://omixor-340121279179.asia-east1.run.app;舊的服務 `omixor-issues` 用同一個資料庫)。這個模組只負責「建置與啟動」:
HTTP 加健康檢查(`server.go`)與進入點(`cmd/omixor-server`);授權、組織、Issue、附件的邏輯都在 `../core`(`omixor-core`,用 `replace` 引用)。

```
cd server
go build ./cmd/omixor-server
docker build -f server/Dockerfile -t omixor-server .   # 在 repo 根目錄
```

**功能開關(兩層,都存在資料庫,不再靠編譯)**:所有功能都編進去(以前的 build tag `dev`、`EDITION`、`OMIXOR_EDITION` 已拿掉)。

- **系統層**:伺服器管理員在「系統」頁決定這台伺服器開放哪些功能(`GET/POST /api/system/features`;任何登入者讀、伺服器管理員改)。
  沒設定過時:原本正式版的功能開放,原本只有開發版的(實驗、範例資料、家庭助理、外部 MCP 閘道)不開放;必要功能永遠開放。
  本機守護服務的本機使用者就是伺服器管理員。
- **組織層**:組織管理員在「設定 › 功能」開關自己組織要用的功能(`GET/POST /api/orgs/{org}/system`)。

實際可用 = 系統層開放 && 組織層開 && 頁面規則。系統層沒開放的功能:`GET /api/orgs/{org}/system` 不列(`serverFeatures` 標 false;
`edition` 是過時的相容欄位)、組織層開/關/reset 回 400、樣板 API 回 404;組織設定裡存著的舊值保留但不生效。

**選用模組(發行組合)**:有些功能做成選用模組(獨立的 Go 套件,在 `init()` 向核心註冊,見 `../core/module`),
由入口程式決定要不要編進去:`cmd/omixor-server/modules.go`、`cmd/omixor-local/modules.go` 用 `import _` 列出要的模組
(同 Caddy 的 xcaddy、CoreDNS 的 plugin.cfg)。沒 import 的模組完全不在執行檔裡:路由 404、功能清單沒有它的功能。

- 目前的模組:`omixor-core/modules/integrations`(程式碼倉庫:組織的 GitHub、GitLab 設定,功能 `development.repos`,開發進度 › Repositories)。
- 預設 `go build ./cmd/omixor-server` 編進全部模組(和以前一樣);`go build -tags omixor_minimal ./cmd/omixor-server` 只有核心。
- `GET /api/modules` 列出這台編進來的模組。

環境變數見 `server.go` 開頭(`OPS_DATABASE_URL`、`OMIXOR_ADMIN_BOOTSTRAP_EMAIL`、`OMIXOR_TOKEN_DAYS`、`EVOLEAP_URL`…)。
部署:`.github/workflows/deploy-issues.yml`(`core/**` 或 `server/**` 有變動就部署)。
測試:`.github/workflows/test.yml`(每次 push 與 pull_request,core/server/web 各跑 `go build`、`go vet`、`go test`)。

**目前只支援單一實例**:雲端部署必須維持 `--max-instances=1`(`deploy-issues.yml` 已設定,不要拿掉)。
部分「先讀再改」的保護(讀出資料、檢查、再寫回)只靠程式內的鎖,只在單一程式內有效;
開多個實例時,兩個實例同時改同一個組織的資料可能互相覆蓋。要等 store 提供組織鎖與交易之後才能多實例。

## 本機守護服務 omixor-local

`cmd/omixor-local`(`local.go`、`local_run.go`、`standalone.go`):只聽 127.0.0.1 的 omixor,資料存在資料夾的 JSON。

- **App 模式**:macOS App 本機模式在背景啟動(環境變數給 `OMIXOR_LOCAL_TOKEN`,每次換一把,隨機埠)。
- **獨立模式**:沒給 `OMIXOR_LOCAL_TOKEN` 或加 `-standalone`,可以直接執行(Windows 雙擊或命令列)。
  資料夾預設 `<使用者設定資料夾>/omixor/core`(Windows `%AppData%\omixor\core`,macOS `~/Library/Application Support/omixor/core`),
  位址預設 `127.0.0.1:8770`;token 第一次啟動產生、存在 `<資料夾>/credentials.json`(和 omixor CLI 的登入資料同格式),`-rotate-token` 換新的。
  `omixor-local -h` 看全部旗標。

```
OMIXOR_SERVER=http://127.0.0.1:8770 OMIXOR_CREDENTIALS=<資料夾>/credentials.json omixor orgs   # CLI 連獨立模式
server/scripts/build-local.sh   # 交叉編譯 windows/amd64、windows/arm64、darwin/arm64、darwin/amd64、linux/amd64 到 server/dist/
```

# omixor-server

omixor 的線上服務(Cloud Run,服務名稱 `omixor`,網址 https://omixor-340121279179.asia-east1.run.app;舊的服務 `omixor-issues` 用同一個資料庫)。這個模組只負責「建置與啟動」:
HTTP 加健康檢查(`server.go`)與進入點(`cmd/omixor-server`);授權、組織、Issue、附件的邏輯都在 `../core`(`omixor-core`,用 `replace` 引用)。

```
cd server
go build ./cmd/omixor-server
docker build -f server/Dockerfile -t omixor-server .   # 在 repo 根目錄
```

**版本(功能集合)**:編譯時決定(`core/model/edition.go`)。預設正式版(stable,只有已實作的功能);
開發版(dev)多了只有範例資料的實驗功能與還在框架階段的功能(例如家庭助理),用 build tag `dev`:

```
go build -tags dev -o bin/omixor-server ./cmd/omixor-server              # 開發版(本機測試用)
docker build -f server/Dockerfile --build-arg EDITION=dev -t omixor-server .
OMIXOR_EDITION=dev server/scripts/build-local.sh                          # 開發版的 omixor-local
go test ./... && go test -tags dev ./...                                  # 兩種版本都要測
```

沒編進去的功能:`GET /api/orgs/{org}/system` 不列(回應的 `edition` 是這次的版本)、不能開(400)、樣板 API 回 404;
組織設定裡存著的舊值保留但不生效。雲端部署(`deploy-issues.yml`)沒給 `EDITION`,是正式版。

環境變數見 `server.go` 開頭(`OPS_DATABASE_URL`、`OMIXOR_ADMIN_BOOTSTRAP_EMAIL`、`OMIXOR_TOKEN_DAYS`、`EVOLEAP_URL`…)。
部署:`.github/workflows/deploy-issues.yml`(`core/**` 或 `server/**` 有變動就部署)。

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

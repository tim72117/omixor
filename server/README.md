# omixor-server

omixor 的線上服務(Cloud Run,服務名稱 `omixor`,網址 https://omixor.io(Cloud Run 網域對應;原本的 https://omixor-340121279179.asia-east1.run.app 仍可用);舊的服務 `omixor-issues` 已關閉公開存取)。這個模組只負責「建置與啟動」:
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

環境變數集中定義在 `../core/config/vars.go`,完整列表(名稱、舊名稱、型別、預設、必填、機密)見 [`../docs/configuration.md`](../docs/configuration.md),`omixor-server -h` 也會列出;啟動時一次驗證,有問題用中文一次列完。

**LDAP / Active Directory 登入(自建伺服器用)**:啟動時設 `OMIXOR_LDAP_URL` 就啟用 `POST /api/auth/ldap`
(`{username, password, device}` → 同 `/api/auth/evoleap` 的 token);沒設就不啟用(回 404)。設了但不完整,伺服器**啟動失敗**並說明缺什麼,不會等到有人登入才發現。
LDAP 變數的完整說明在 [`../docs/configuration.md`](../docs/configuration.md)(「LDAP 登入」一節);連線與檢查邏輯在 `../core/ldapauth/ldapauth.go`、`ldap.go`。

- 流程:用服務帳號(`OMIXOR_LDAP_BIND_DN`)搜尋使用者,再用找到的 DN 和使用者輸入的密碼 bind。帳號不存在、被篩選掉(例如停用)、密碼錯,一律回同一句 401。
- 誰能進:通過 LDAP 的人**第一次登入自動建成一般使用者**(不屬於任何組織,要組織管理員用 email 加進去)。
  `OMIXOR_LDAP_ADMINS`(逗號分隔的帳號)是伺服器管理員:每次登入都確認,所以後來才加進名單的人下次登入會升級;從名單拿掉**不會**自動降級,要到使用者管理改。
- email 取目錄的 `mail`;沒有就用 `帳號@OMIXOR_LDAP_EMAIL_DOMAIN`;兩個都沒有就不能建立(403)。
  管理員先用 email 加進名單、還沒登入過的人:**預設不會**自動對上(回 403,說明原因)。目錄的 mail 如果使用者自己改得動(OpenLDAP 常見),能用自己填的 email 接手別人預先加入的身分和組織權限;確定 mail 只有管理員能改(AD 通常是)才設 `OMIXOR_LDAP_TRUST_MAIL=true`,之後第一次 LDAP 登入就會對上。
  email 已經被別的身分(evoleap 或別的 LDAP 帳號)用了就拒絕,不接管。
- LDAP 身分存在使用者的 `evoleap_id` 欄位,加前綴 `ldap:<帳號>`(欄位是唯一的文字,不用改資料庫)。
- 範例(Active Directory,只允許啟用中的帳號):

  ```
  OMIXOR_LDAP_URL=ldap://ad.example.com:389
  OMIXOR_LDAP_START_TLS=true
  OMIXOR_LDAP_BIND_DN=cn=svc-omixor,cn=Users,dc=example,dc=com
  OMIXOR_LDAP_BIND_PASSWORD_FILE=/run/secrets/ldap_password
  OMIXOR_LDAP_BASE_DN=ou=Staff,dc=example,dc=com
  OMIXOR_LDAP_USER_FILTER=(&(sAMAccountName=%s)(userAccountControl=512))
  OMIXOR_LDAP_ADMINS=alice,bob
  ```
- 安全:`ldap://` 沒開 StartTLS 時,使用者密碼在網路上是明碼,啟動紀錄會警告;`OMIXOR_LDAP_INSECURE_SKIP_VERIFY` 只給測試環境。登入有兩層限制:每個來源每分鐘 10 次(來源 IP 取 X-Forwarded-For 的第一段,可以被偽造,只能當輔助);**每個帳號 10 分鐘內連續輸錯 5 次就先擋 10 分鐘,不再送到 LDAP**(AD 的鎖定是整個網域的,不擋的話任何人都能一直送錯密碼把別人鎖住;不存在的帳號也一樣計算,連不上 LDAP 這類伺服器端錯誤不算輸錯)。
- 目前只有伺服器端:`omixor login`、網頁版、Mac App 還沒有 LDAP 登入畫面,先用 `curl -X POST …/api/auth/ldap` 取得 token。
部署:`.github/workflows/deploy-server.yml`(`core/**`、`server/**` 或 `site/**` 有變動就部署)。
測試:`.github/workflows/test.yml`(每次 push 與 pull_request,core/server/web 各跑 `go build`、`go vet`、`go test`)。

**目前只支援單一實例**:雲端部署必須維持 `--max-instances=1`(`deploy-server.yml` 已設定,不要拿掉)。
部分「先讀再改」的保護(讀出資料、檢查、再寫回)只靠程式內的鎖,只在單一程式內有效;
開多個實例時,兩個實例同時改同一個組織的資料可能互相覆蓋。要等 store 提供組織鎖與交易之後才能多實例。

## 本機守護服務 omixor-local

`cmd/omixor-local`(`local.go`、`local_run.go`、`standalone.go`):只聽 127.0.0.1 的 omixor,資料存在 PostgreSQL。

- **資料庫**:預設由守護服務自己啟動一個內建的 PostgreSQL 17(`embeddedpg.go`,用 `github.com/fergusstrange/embedded-postgres`):
  資料在 `<資料夾>/pgdata`、密碼在 `<資料夾>/pgpassword`、每次啟動解壓的程式在 `<資料夾>/pgruntime`(約 100 MB,可以刪,下次啟動會重建),
  只聽 127.0.0.1 的隨機埠,結束時乾淨關閉(上次沒關乾淨的孤兒 postgres 下次啟動會先被關掉)。
  **第一次啟動要連網下載 PostgreSQL 二進位檔**(約 10–60 MB,存在 `~/.embedded-postgres-go`,之後離線也能啟動),初始化加啟動約十幾秒。
  PostgreSQL 不能以 root / 系統管理員身分執行。windows/arm64 沒有現成的 PostgreSQL 二進位檔,請改用 windows/amd64 版本(ARM 上可模擬執行)或設 `OMIXOR_DATABASE_URL`。
  下載的二進位檔會用程式內寫死的 sha256 比對(`embeddedpg_pin.go`;快取檔每次啟動也比對,不符就重抓),升級 PostgreSQL 版本時要一起更新那張表。
  資料目錄保護:`pgdata` 有資料但 `PG_VERSION` 不見、讀不到或主版本和內建的不符時,**拒絕啟動**並說明處理方式(函式庫原本會直接清掉資料目錄重建);
  升 PostgreSQL 大版本需要自己先匯出/搬資料。
  設了 `OMIXOR_DATABASE_URL` 就直接連那個資料庫、不啟動內建的。
  以前的檔案版 store(`<資料夾>/store.json`)已經移除,**不會遷移,舊的 store.json 不會匯入**(程式不會刪它;啟動時若有 store.json 而且還沒有 pgdata 會印警告,不需要時請自行刪除)。
- **App 模式**:macOS App 本機模式在背景啟動(環境變數給 `OMIXOR_LOCAL_TOKEN`,每次換一把,隨機埠)。
- **獨立模式**:沒給 `OMIXOR_LOCAL_TOKEN` 或加 `-standalone`,可以直接執行(Windows 雙擊或命令列)。
  資料夾預設 `<使用者設定資料夾>/omixor/core`(Windows `%AppData%\omixor\core`,macOS `~/Library/Application Support/omixor/core`),
  位址預設 `127.0.0.1:8770`;token 第一次啟動產生、存在 `<資料夾>/credentials.json`(和 omixor CLI 的登入資料同格式),`-rotate-token` 換新的。
  `omixor-local -h` 看全部旗標。

```
OMIXOR_SERVER=http://127.0.0.1:8770 OMIXOR_CREDENTIALS=<資料夾>/credentials.json omixor orgs   # CLI 連獨立模式
server/scripts/build-local.sh   # 交叉編譯 windows/amd64(windows/arm64 會略過,見上)、darwin/arm64、darwin/amd64、linux/amd64 到 server/dist/
```

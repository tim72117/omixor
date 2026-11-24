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
- 安全:`ldap://` 沒開 StartTLS 時,使用者密碼在網路上是明碼,啟動紀錄會警告;`OMIXOR_LDAP_INSECURE_SKIP_VERIFY` 只給測試環境。登入有兩層限制:每個來源每分鐘 10 次、LDAP 登入入口整體每分鐘 60 次(與配對、evoleap 登入各自計數,互不影響;來源 IP 由 `OMIXOR_TRUSTED_PROXY_HOPS` 決定,預設 Cloud Run 取 X-Forwarded-For 由右數第 1 個、其他環境用連線位址,見 docs/configuration.md);**每個帳號 10 分鐘內連續輸錯 5 次就先擋 10 分鐘,不再送到 LDAP**(AD 的鎖定是整個網域的,不擋的話任何人都能一直送錯密碼把別人鎖住;不存在的帳號也一樣計算,連不上 LDAP 這類伺服器端錯誤不算輸錯)。
- 目前只有伺服器端:`omixor login`、網頁版、Mac App 還沒有 LDAP 登入畫面,先用 `curl -X POST …/api/auth/ldap` 取得 token。
**OAuth(Claude 自訂 connector 連 MCP;預設唯讀,可選受限寫入)**:設了 `OMIXOR_PUBLIC_URL` 才啟用(issuer、resource 都由它推導;沒設就不掛這些路由)。程式在 `../core/oauth`(HTTP)、`../core/service/oauth.go`(規則)、`../core/store/pg_oauth.go`(`omixor_oauth_clients`、`omixor_oauth_tokens`)。

路由(`<網址>` = `OMIXOR_PUBLIC_URL`):

| 路由 | 說明 |
|---|---|
| `GET /.well-known/oauth-protected-resource/mcp/org/{org}`(備援 `/.well-known/oauth-protected-resource`) | RFC 9728:`resource` = `<網址>/mcp/org/{org}`,`authorization_servers` = `[<網址>/oauth]`,`scopes_supported` = `[mcp:read]` |
| `GET /.well-known/oauth-authorization-server/oauth`、`GET /oauth/.well-known/openid-configuration` | RFC 8414:issuer `<網址>/oauth`、PKCE 只有 S256、公開用戶端(`token_endpoint_auth_method=none`)、`authorization_code` + `refresh_token` |
| `POST /oauth/register` | RFC 7591 動態註冊(JSON、≤16 KB)。`redirect_uris` 只收 `https://claude.ai/api/mcp/auth_callback`、`https://claude.com/api/mcp/auth_callback`、`claude://claude.ai/mcp-auth-callback/sdk`(桌面版 / Agent SDK,只收完全相同的字串)、本機 `http://127.0.0.1\|localhost[:埠]/callback`;用戶端上限 200(滿了先淘汰「註冊超過 1 小時、沒人完成授權」的,仍滿 429);每個來源每分鐘 5 次、全服務 30 次 |
| `GET /oauth/authorize` | 同意畫面(中文、手機版、深色模式):要有註冊過的 `client_id`、完全相同的 `redirect_uri`、`code_challenge` + `S256`、`resource` = 該組織的 MCP 網址;每次載入一個 CSRF token(只存雜湊,15 分鐘,用一次) |
| `POST /oauth/authorize` | 證明身分有兩種:**「用 evoleap 登入並允許」(主要;表單 `via=evoleap`)**,或輸入**配對碼**(備援,收在「改用配對碼」;已登入的 omixor「手機配對」產生;規則同 `ClaimPairCode`:5 分鐘、一次性、只存雜湊,但**不發登入 token**)。配對碼成功 303 導回 `redirect_uri?code=…&state=…`(授權碼 60 秒、用一次);配對碼錯一律同一句話,累計 10 次失敗全服務鎖 10 分鐘;帳號停用或不是該組織成員拒絕。evoleap 那條:重驗授權請求與 CSRF 後向 evoleap 登記回呼、設綁定瀏覽器的 cookie `omixor_oauth_evo`(HttpOnly、SameSite=Lax、Path=/oauth)並 303 到 evoleap 核准頁 |
| `GET /oauth/evoleap/callback` | evoleap 核准後導回(`?id=&code=`):核對 cookie 綁定密鑰、登入交易 10 分鐘單一用途,向 evoleap 換 token 對上 omixor 使用者後立刻撤銷該 token(成功失敗都撤銷),簽發授權碼並 303 導回客戶端;id 不認得、cookie 不對、逾時、重放一律同一個通用錯誤頁。沒有 evoleap(沒設 `EVOLEAP_URL`)或 `OMIXOR_OAUTH_EVOLEAP_LOGIN=false` 時同意畫面只有配對碼。`OMIXOR_EVOLEAP_CONSOLE` 可指定核准頁網址(預設同 evoleap 網址) |
| `POST /oauth/token` | 只收 `application/x-www-form-urlencoded`(其他 415)。`authorization_code`(PKCE 常數時間比對、code 用一次、redirect_uri 與 resource 要相同;重用 code 會撤銷它換出的 token)、`refresh_token`(每次輪替,舊的重用 → `invalid_grant` 並撤銷整個家族)。access token `omo_…` 600 秒、refresh 30 天,scope 只有 `mcp:read` |
| `GET /oauth/consent.css` | 同意畫面樣式(CSP 不允許行內樣式) |

- 同意畫面的 CSP:`default-src 'self'; frame-ancestors 'none'; form-action 'self' <redirect_uri 的來源>`。多出的來源是因為 Chrome 連「送出表單後的 3xx 導向」都檢查 `form-action`,只寫 `'self'` 會把導回 Claude 擋掉。
- **唯讀**(兩層):(1) `/mcp/org/{org}`:OAuth 身分只列 `model.CoreTools` 裡 `ReadOnly: true` 的工具(`orgs`、`issues`、`agents`、`agent_tasks`、`projects`、`cabinet`、`docs`、`chain`、`channels`、`incidents`、`issue_decisions`、`issue_items`、`flowchart_render`),
  `flowchart_render` 的 `save` 參數會被拒絕;呼叫其他工具回「此連線是唯讀授權」。(2) `Service.Authenticate` 看到 `omo_` 一律 403,所以 `/api/*`、`/mcp/worker/{org}` 都不收。`omx_` 使用者 token 的行為完全不變。
- 統一入口 `/mcp` 也收 OAuth(帳號層級、唯讀):resource = `<網址>/mcp`(metadata 在 `/.well-known/oauth-protected-resource/mcp`),同意畫面不選組織(說明「你帳號能存取的所有組織」),token 的 org 欄位為空、組織由每次呼叫的 `org` 與即時成員資格決定;綁定嚴格(`/mcp` 與 `/mcp/org/{org}` 的 token 不能互用);只開放唯讀工具。見 `docs/authorization.md` §0.5。
- **OAuth 寫入(scope `mcp:write`,預設關閉)**:客戶端要求 `mcp:write` 時同意畫面才出現「唯讀(預設)/ 讀寫」選項(讀寫要使用者主動選;伺服器驗證,不信表單送來超出請求的 scope;CSRF 綁定這一次請求);授予的 scope ≤ 請求 ∩ 使用者選擇,不會自動升級,refresh 不能升級(超出 → `invalid_scope`)。讀寫 token 多開放 `model.OAuthWriteTools` 那組協作寫入(`issue_create`、`issue_update`、`issue_accept`、`issue_decision_add/answer/to_issue`、`issue_item_add/update/report/promote`、`doc_save`、`channel_create`、`channel_post`、`agent_task_report`),其餘工具回英文 "This tool is not available over OAuth";**不是伺服器管理員**(`Principal.ServerAdmin()` 對 OAuth 寫入身分一律 false,以一般成員權限運作、授權表照舊檢查),不能領角色憑證、管理角色與組織結構、建立配對碼。`/mcp/org/{org}` 與 `/mcp` 行為一致,`omx_` 不變。稽核 `source = oauth`,`oauth.authorize` 帶 `scope`。目前沒有自助撤銷介面(退出組織、請管理員停用帳號,或等 token 到期)。見 `docs/authorization.md` §0.5。
- `/mcp/org/{org}` 沒帶 token:401,`WWW-Authenticate: Bearer realm="omixor", resource_metadata="<網址>/.well-known/oauth-protected-resource/mcp/org/{org}"`;OAuth token 過期、撤銷或不是這個組織的:再加 `error="invalid_token"`。
- **MCP Apps**:管理端點的 `flowchart_render`、`issue_items` 在 tools/list 帶 `_meta.ui.resourceUri`(`ui://omixor/flowchart`、`ui://omixor/items`),`resources/list`、`resources/read` 回 `text/html;profile=mcp-app`。
  畫面是 `../core/mcp/ui/*.html`(手寫、自包含、不載入外部來源,`go:embed`,Go 編譯不需要 node);協定的 View 端不靠 `@modelcontextprotocol/ext-apps`,直接手寫,已用 `tools/mcp-apps-pilot/harness`(真正的 AppBridge)驗證過能初始化、收結果、回報高度、切換深淺色。要改畫面就直接改 HTML(兩個檔的 COMMON 區塊要一致,`TestUIViewsCommonBlock`)。
- 在 Claude 設定:自訂 connector 網址 `<網址>/mcp/org/<組織 id>`,驗證選自動註冊(不填 client id);跳出同意畫面後按「用 evoleap 登入並允許」(瀏覽器登入);沒有 evoleap 的伺服器改輸入在 omixor「手機配對」產生的配對碼。

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
- **OAuth 組織管理員(scope `mcp:admin`,預設關閉)**:客戶端要求 `mcp:admin` 時同意畫面多出第三個單選鈕「組織管理員」(只有帳號層級 `/mcp` 授權;送出時使用者要是至少一個組織的管理員,否則被拒);授予後統一入口多開組織結構工具(角色、組織鏈、專案、文件櫃結構、事件),每次呼叫仍即時檢查呼叫者是該組織的管理員;不發角色憑證、不是伺服器管理員。metadata 的 `scopes_supported` 含 `mcp:admin`。

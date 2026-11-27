# 設定(環境變數)

<!-- 這份文件由 core/config 的設定定義產生,不要手改。重新產生:UPDATE_CONFIG_DOC=1 go test ./config -run TestDocFresh(在 core/ 底下) -->

omixor 的設定都是環境變數。每個程式啟動時一次讀完並驗證(型別、範圍、必填、互斥),有問題會用中文一次列出;機密(密碼、token、金鑰、資料庫網址裡的密碼)不會出現在日誌或錯誤訊息裡。

- 舊名稱(`OPS_*`):新名稱沒設才讀舊名稱,讀到時記一次 log 提醒改名;新名稱優先。舊名稱之後會拿掉。
- 布林值:`1`、`true`、`yes`、`on` 為開;`0`、`false`、`no`、`off` 或空白為關;其他值啟動時報錯。
- 程式欄:`server` = omixor-server、`local` = omixor-local、`web` = omixor-web(console)、`cli` = omixor、`mcp` = omixor-mcp。
- 每個程式的 `-h`(或不給參數時的說明)也列出它用到的變數,內容同這份文件。

## 共用

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_DATABASE_URL` | `OPS_DATABASE_URL` | 網址或 key=value(postgres/postgresql) |  | server | 是 | server、local、web | PostgreSQL 連線設定,網址形式(postgres://使用者:密碼@主機/資料庫,可有多個主機、unix socket)或 key=value 形式(host=/cloudsql/專案:區域:執行個體 user=… password=… dbname=…,Cloud SQL 常用);密碼(網址的密碼、query 的 password/sslpassword、key=value 的 password=)會被遮蔽。server 必填;local 沒設就啟動內建的 PostgreSQL;web 也必填(沒設就不能用組織與 Issue 等)。獨立模式的 omixor-local 也會使用它(例如開發用的 docker-compose 為 omixor-web 設的) |
| `OMIXOR_ADDR` |  | 字串 | server: :$PORT(沒有 PORT 就 :8080);local:獨立模式 127.0.0.1:8770、App 模式 127.0.0.1:0 |  |  | server、local | HTTP 監聽位址。local 只接受本機位址 |
| `OMIXOR_SERVER` | `OPS_ISSUE_SERVER` | 網址(http/https) | cli:上次登入的、舊版登入資料的、正式站 |  |  | cli、web | 連哪一台 omixor server。cli:--server 旗標優先於它。web:不再使用(遠端模式已移除);升級前只靠它進遠端模式的安裝會拒絕啟動,執行一次 omixor-web leave-remote 或拿掉這個變數 |
| `OMIXOR_SECRET_KEY` | `OPS_SECRET_KEY` | 金鑰(32 bytes) |  |  | 是 | server、web | 加密機密用的金鑰:32 bytes,寫成 64 個 hex 字元或 base64(openssl rand -base64 32)。console 用資料庫時必填(加密 token 與密碼);伺服器目前只檢查格式,還沒有用它加密任何資料。要固定保存:console 只用這一把解密,換掉後已存的密鑰會解不開(就算把舊的放進 OMIXOR_SECRET_KEYS_OLD 也一樣) |
| `OMIXOR_SECRET_KEYS_OLD` |  | 金鑰清單(逗號分隔) | 沒有舊金鑰 |  | 是 | server、web | 保留給金鑰輪替的舊金鑰清單,逗號分隔、格式同 OMIXOR_SECRET_KEY。目前只檢查格式:console 不讀它、伺服器也還沒有用到,不能靠它更換金鑰 |
| `OMIXOR_MCP_LIST_TTL_SECONDS` |  | 整數(0–86400) | `300` |  |  | server、local、web | MCP 2026-07-28 請求的快取提示:server/discover、tools/list、resources/list、resources/read 結果的 ttlMs 取自這個秒數(0 = 一律視為過期);舊版協定的回應不帶快取提示 |

## 伺服器(omixor-server)

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_PUBLIC_URL` |  | 網址(http/https) |  |  |  | server、web | 對外的網址(例如 https://omixor.io);領取任務時附上角色端點 <網址>/mcp/agent,也是 OAuth 的 issuer 與 resource 的來源。local 啟動時自己填上。web:對外 MCP(OAuth)的網址,必須是 https(本機位址 localhost、127.0.0.1、[::1] 除外),只放網址、不放路徑。沒設 OMIXOR_OAUTH_ADDR 時,畫面連接埠的 MCP 入口(/mcp、/oauth/*,和 server 相同;需要 PostgreSQL)以它為對外網址;沒設或不合格(只警告)時入口只收同一台電腦直接連進來的,網址是 http://127.0.0.1:<畫面連接埠>;設了 OMIXOR_OAUTH_ADDR 時入口在那個連接埠(這時必填) |
| `OMIXOR_SITE_DIR` |  | 路徑 |  |  |  | server | 形象網站檔案所在的資料夾(Docker 映像裡是 /site);沒設或不存在就不提供網站 |
| `OMIXOR_TOKEN_DAYS` |  | 整數(0–36500) | `90` |  |  | server | 使用者 token 的有效天數 |
| `OMIXOR_ADMIN_BOOTSTRAP_EMAIL` |  | 字串 |  |  |  | server | 第一位管理員的 email(資料庫還沒有管理員時,這個 email 登入就成為管理員) |
| `OMIXOR_AGENT_CRED_HOURS` |  | 小數 | 服務內建的預設 |  |  | server | 領取任務時發給角色的憑證有效小時數(要大於 0,否則用預設) |
| `OMIXOR_TRUSTED_PROXY_HOPS` |  | 整數(0–16) | server:有 K_SERVICE(Cloud Run)是 1,否則 0;web:0 |  |  | server、web | 信任的代理層數,決定怎麼取請求來源 IP(頻率限制、稽核用;web 用在登入與 MCP 授權)。0 = 不看 X-Forwarded-For,用連線位址;N = 取 X-Forwarded-For 由右數第 N 個(最右邊是離伺服器最近的代理加的;項目不足或不是合法 IP 就退回連線位址)。Cloud Run 直接對外是 1;前面再加 HTTPS 負載平衡器要設 2。設太大會讓呼叫者能自己填來源、繞過每 IP 限制;設太小會讓代理的位址被當成來源、全部人共用一個額度 |
| `K_SERVICE` |  | 字串 |  |  |  | server | Cloud Run 自動帶入的服務名稱(不是 omixor 專用名稱);有值就表示跑在 Cloud Run,OMIXOR_TRUSTED_PROXY_HOPS 預設 1 |
| `PORT` |  | 整數(1–65535) | `8080` |  |  | server | Cloud Run 等平台指定的連接埠;沒設 OMIXOR_ADDR 時監聽 :$PORT(不是 omixor 專用名稱,所以沒有 OMIXOR_ 前綴) |
| `EVOLEAP_URL` |  | 網址(http/https) | `https://evoleap.io` |  |  | server、web | evoleap 登入服務的網址(沒有 OMIXOR_ 前綴,沿用原名)。web:只有對外 MCP 連接埠(OMIXOR_OAUTH_ADDR)的 evoleap 登入用 |
| `OMIXOR_OAUTH_EVOLEAP_LOGIN` |  | 布林 | `true` |  |  | server、web | OAuth 同意畫面提供「用 evoleap 登入並允許」(瀏覽器登入,不用配對碼;需要 OMIXOR_PUBLIC_URL)。server:設 false 關掉,同意畫面只剩配對碼;web:同意畫面是 evoleap、GitLab 與 LDAP 帳號密碼(console 的認證設定啟用 LDAP 時;不提供配對碼);只用 LDAP 時可以設 false |

## LDAP 登入(omixor-server)

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_LDAP_BIND_PASSWORD` | `OPS_LDAP_BIND_PASSWORD` | 字串 |  |  | 是 | server、web | LDAP 服務帳號密碼。server 用在 OMIXOR_LDAP_BIND_DN;web 不想用鑰匙圈/資料庫保存時改從這裡讀 |
| `OMIXOR_LDAP_URL` |  | 網址(ldap/ldaps) |  |  |  | server | LDAP / Active Directory 位址,ldap://主機:389 或 ldaps://主機:636。沒設 = 不啟用 LDAP 登入;設了就必須設 BIND_DN 或 USER_DN_TEMPLATE 其中一個 |
| `OMIXOR_LDAP_START_TLS` |  | 布林 |  |  |  | server | ldap:// 連線升級成 StartTLS(ldaps:// 不用) |
| `OMIXOR_LDAP_CA_CERT` |  | 路徑 |  |  |  | server | 驗證 LDAP 伺服器憑證用的 CA 憑證檔(PEM) |
| `OMIXOR_LDAP_INSECURE_SKIP_VERIFY` |  | 布林 |  |  |  | server | 不驗證 LDAP 伺服器憑證(只建議測試用) |
| `OMIXOR_LDAP_BIND_DN` |  | 字串 |  |  |  | server | 服務帳號 DN(用服務帳號搜尋使用者的方式);要同時給密碼與 OMIXOR_LDAP_BASE_DN |
| `OMIXOR_LDAP_BIND_PASSWORD_FILE` |  | 路徑 |  |  |  | server | 服務帳號密碼存在這個檔案(Docker secret);OMIXOR_LDAP_BIND_PASSWORD 有設時以它為準 |
| `OMIXOR_LDAP_BASE_DN` |  | 字串 |  |  |  | server | 搜尋使用者的起點 DN(用服務帳號搜尋時必填) |
| `OMIXOR_LDAP_USER_FILTER` |  | 字串 | (<ID_ATTR>=%s) |  |  | server | 搜尋使用者的過濾條件,要剛好有一個 %s(換成帳號) |
| `OMIXOR_LDAP_USER_DN_TEMPLATE` |  | 字串 |  |  |  | server | 直接用使用者身分 bind 的 DN 樣板,要剛好有一個 %s(換成帳號) |
| `OMIXOR_LDAP_ID_ATTR` |  | 字串 | sAMAccountName |  |  | server | 帳號對應的屬性 |
| `OMIXOR_LDAP_NAME_ATTR` |  | 字串 | displayName |  |  | server | 顯示名稱對應的屬性 |
| `OMIXOR_LDAP_MAIL_ATTR` |  | 字串 | mail |  |  | server | email 對應的屬性 |
| `OMIXOR_LDAP_TIMEOUT_SECONDS` |  | 整數(≥1) | `10` |  |  | server | LDAP 連線與查詢的逾時秒數 |
| `OMIXOR_LDAP_ADMINS` |  | 字串 |  |  |  | server、web | 伺服器管理員的帳號(逗號分隔),每次登入都確認。web:console 的網頁管理員名單,有設時蓋掉認證設定裡的名單;名單空白就沒有網頁管理員 |
| `OMIXOR_LDAP_EMAIL_DOMAIN` |  | 字串 |  |  |  | server、web | 目錄沒有 mail 時,用 帳號@這個網域 當 email;只要網域(例如 corp.example.com)。web:有設時蓋掉認證設定裡的值 |
| `OMIXOR_LDAP_TRUST_MAIL` |  | 布林 |  |  |  | server、web | 信任目錄的 mail:預先加入的同 email 使用者登入時自動對上。web:有設時蓋掉認證設定裡的值 |

## GitLab 登入(omixor-server)

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_GITLAB_URL` |  | 網址(http/https) |  |  |  | server、web | 自建 GitLab 的網址(例如 https://gitlab.example.com,可帶子路徑)。沒設 = 不啟用 GitLab 登入;web 只有對外 MCP 連接埠(OMIXOR_OAUTH_ADDR)的同意畫面用;設了就必須同時有 CLIENT_ID、密鑰與 OMIXOR_PUBLIC_URL。GitLab Application 的 Redirect URI 要登記 <OMIXOR_PUBLIC_URL>/oauth/gitlab/callback 與 <OMIXOR_PUBLIC_URL>/api/auth/gitlab/callback(console 只要前者),scope 勾 read_user,勾 Confidential |
| `OMIXOR_GITLAB_CLIENT_ID` |  | 字串 |  |  |  | server、web | GitLab Application 的 Application ID |
| `OMIXOR_GITLAB_CLIENT_SECRET` |  | 字串 |  |  | 是 | server、web | GitLab Application 的 Secret |
| `OMIXOR_GITLAB_CLIENT_SECRET_FILE` |  | 路徑 |  |  | 是 | server、web | Application 的 Secret 存在這個檔案(Docker secret);OMIXOR_GITLAB_CLIENT_SECRET 有設時以它為準 |
| `OMIXOR_GITLAB_CA_CERT` |  | 路徑 |  |  |  | server、web | GitLab 用自簽憑證時的 CA 憑證檔(PEM;加在系統信任清單之外) |
| `OMIXOR_GITLAB_ADMINS` |  | 字串 |  |  |  | server、web | 伺服器管理員的 GitLab 使用者 id(數字,逗號分隔;不收 username,因為 username 改掉後別人可以拿去用)。每次登入都確認,只升不降 |
| `OMIXOR_GITLAB_EMAIL_DOMAINS` |  | 字串 |  |  |  | server、web | 允許用 GitLab 登入的 email 網域(逗號分隔,例如 corp.example.com;完全相同才算,不含子網域)。沒設 = 不限。GitLab 開放註冊時一定要設;另外 GitLab 要開啟 email 確認(omixor 只看 confirmed_at) |
| `OMIXOR_GITLAB_LINK_LDAP` |  | 布林 |  |  |  | server、web | 同一個人的 GitLab 與 LDAP 併到同一帳號。要同時啟用 LDAP 且有服務帳號(OMIXOR_LDAP_BIND_DN):用 GitLab 帳號的 LDAP 身分(DN)查目錄確認,email 也要相同才併;web 不支援(console 沒有核心的 LDAP 登入,設了會啟動失敗);開啟後,有 LDAP 身分但查不到目錄帳號或 email 不符的人不能登入;GitLab 的 LDAP 與 omixor 的要是同一個目錄 |
| `OMIXOR_GITLAB_TRUST_MAIL` |  | 布林 |  |  |  | server、web | 信任 GitLab 確認過的 email:預先加入的同 email 使用者登入時自動對上 |

## 本機守護服務(omixor-local)

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_DATA_DIR` |  | 路徑 | 獨立模式:<使用者設定資料夾>/omixor/core;App 模式必填 |  |  | local | 資料夾(PostgreSQL 資料、credentials.json 等)。-data 旗標優先 |
| `OMIXOR_LOCAL_TOKEN` |  | 字串 |  |  | 是 | local | macOS App 給的 token(omx_ 開頭,每次啟動換一把);有設就是 App 模式,沒設是獨立模式。讀取後立刻從環境移除,子程序不會繼承 |
| `OMIXOR_LOCAL_USER` |  | 字串 | local |  |  | local | 本機使用者的 email 或 id |
| `OMIXOR_LOCAL_NAME` |  | 字串 | 同 OMIXOR_LOCAL_USER |  |  | local | 本機使用者的顯示名稱 |
| `OMIXOR_LOCAL_ORG` |  | 字串 | default |  |  | local | 確保存在的預設組織 id(不能刪) |
| `OMIXOR_EXIT_ON_STDIN_EOF` |  | 布林 |  |  |  | local | 標準輸入關掉就結束(App 結束或當掉時守護服務跟著結束;只在 App 模式有作用) |
| `PGCTLTIMEOUT` |  | 整數(1–86400) | 依啟動逾時推算(至少 60) |  |  | local | pg_ctl 啟動內建 PostgreSQL 的等待秒數(pg_ctl 自己認的變數;自己設了就用你的) |

## 畫面與 API(omixor-web)

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_EVOLEAP_CONSOLE` |  | 網址(http/https) |  |  |  | server、web | evoleap 核准頁網址(沒設就用 evoleap 的網址)。伺服器用它把 OAuth 同意畫面的「用 evoleap 登入並允許」導去 <網址>/login/cli?id=… |
| `OMIXOR_NO_TIMERS` | `OPS_NO_TIMERS` | 布林 |  |  |  | web | 不開背景計時器(例行任務、輪詢;測試用) |
| `OMIXOR_NO_BROWSER` | `OPS_NO_BROWSER` | 旗標(有設就算) |  |  |  | web | 啟動時不自動開瀏覽器(Docker 映像預設開著)。有設就不開:任何非空值都算,包含 0、false |
| `OMIXOR_CONSOLE_DATA_DIR` | `OPS_DATA_DIR` | 路徑 | ~/Library/Application Support/omixor-console(依系統) |  |  | web、mcp | 資料夾(範例資料、設定、auth.json 等);測試一律指到暫存資料夾 |
| `OMIXOR_AUTH` | `OPS_AUTH` | 選項:off |  |  |  | web | off(不分大小寫)= 強制不用登入(設定錯了進不去時用它回到本機模式);其他值只在啟動時警告、一律忽略 |
| `OMIXOR_API_TOKEN` | `OPS_API_TOKEN` | 字串 |  |  | 是 | web | /api/issues/ 可以用的 Bearer token(至少 16 字元,不夠長視為沒設) |
| `OMIXOR_SHUTTLE_PATH` | `OPS_SHUTTLE_PATH` | 路徑 |  |  |  | web | 語音 shuttle 資料夾(優先於畫面裡的設定) |
| `OMIXOR_CLAUDE_BIN` |  | 路徑 | 常見安裝位置,再找 PATH |  |  | web | claude 執行檔路徑(自動執行角色任務用) |
| `OMIXOR_OFFICE_DIR` |  | 路徑 | <執行檔>/../../office3d、<執行檔>/../office3d、<執行檔>/office3d |  |  | web | 3D 辦公室(office3d)資料夾,裡面要有 places.json |
| `OMIXOR_TEMPLATES` |  | 字串 | 不載入範本 |  |  | web | 範本資料:1/on/true/yes/auto = 自動找 templates/;0/off/false/no = 不載入;其他值 = 範本資料夾路徑 |
| `OMIXOR_OAUTH_ADDR` |  | 字串 | 不啟用 |  |  | web | 對外 MCP 連接埠的監聽位址(例如 127.0.0.1:8771);設了才啟用,讓遠端 MCP 用戶端(claude.ai 等)連到這台 console。服務 /oauth/*、/.well-known/*、/mcp、/mcp/org/{org}、/mcp/agent、/mcp/worker/{org},認證與核心工具和 omixor server 相同(OAuth 登入用 evoleap 或 GitLab,也收 omx_ 使用者 token;console 專有工具只在 /mcp 給 OAuth 連線,見 console/README.md),反向代理或 Tailscale Funnel 只轉發這個連接埠。需要 PostgreSQL(OMIXOR_DATABASE_URL)、OMIXOR_PUBLIC_URL(https),evoleap 要登記回呼網址 <OMIXOR_PUBLIC_URL>/oauth/evoleap/callback |
| `OMIXOR_WEB_PORT` | `OPS_WEB_PORT` | 整數(1–65535) | `5191` |  |  | web、mcp | 畫面與 API 的連接埠(沒設 OMIXOR_WEB_ADDR 時聽 127.0.0.1:<連接埠>);MCP 入口也在這個連接埠,omixor-mcp 用它組出預設網址 |
| `OMIXOR_WEB_ADDR` | `OPS_WEB_ADDR` | 字串 | 127.0.0.1:<OMIXOR_WEB_PORT> |  |  | web | 畫面聽的位址,例如 0.0.0.0:5191;不是本機位址時必須先啟用 LDAP 登入(或設 OMIXOR_WEB_LOCAL_ONLY_PUBLISH) |
| `OMIXOR_WEB_HOSTS` | `OPS_WEB_HOSTS` | 字串 |  |  |  | web | 允許的主機名稱(逗號分隔);反向代理的網域要列在這裡 |
| `OMIXOR_WEB_LOCAL_ONLY_PUBLISH` | `OPS_WEB_LOCAL_ONLY_PUBLISH` | 閘門(只認 1) |  |  |  | web | 安全閘門:在 Docker 裡聽 0.0.0.0、但連接埠只發布到主機的 127.0.0.1 時設 1,可略過「不是本機位址必須啟用 LDAP」的檢查。只認精確的 1,其他值(true、yes、on…)一律視為沒設 |

## 命令列(omixor)

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_CREDENTIALS` |  | 路徑 | <設定資料夾>/credentials/<伺服器>.json |  |  | cli | 登入資料檔(指定單一檔案時只用那個檔,不記「上次登入的伺服器」) |
| `OMIXOR_CONFIG_DIR` |  | 路徑 | ~/.config/omixor |  |  | cli、mcp | CLI 設定資料夾(登入資料、目前的伺服器);omixor-mcp 的 OAuth 登入也存在它底下的 mcp/ |
| `CI` |  | 字串 |  |  |  | cli | CI 平台(GitHub Actions、GitLab CI 等)自動帶入的標記(不是 omixor 專用名稱,所以沒有 OMIXOR_ 前綴);值為 true 時,omixor commits sync 沒給 --source 就把來源記成 ci,否則記成 cli |

## MCP 轉接(omixor-mcp)

| 名稱 | 舊名稱 | 型別 | 預設 | 必填 | 機密 | 程式 | 說明 |
|---|---|---|---|---|---|---|---|
| `OMIXOR_MCP_URL` | `OPS_MCP_URL` | 網址(http/https) | http://127.0.0.1:<OMIXOR_WEB_PORT>/mcp |  |  | mcp | omixor 的 MCP 入口網址(統一入口,結尾是 /mcp;console 或 omixor server 的都可以)。--channel 與 --init-worker 的執行者端點是它後面接 /worker/<組織>。要和伺服器公告的網址完全相同(沒設 OMIXOR_PUBLIC_URL 的 console 是 http://127.0.0.1:<連接埠>/mcp) |

## 與舊版行為不同的啟動驗證

設定集中之後,下列情形在程式啟動時就報錯(舊版是靜默忽略,或到用到時才失敗)。升級前先確認部署的環境變數沒有這些值;其他的值(包含 `OMIXOR_AUTH` 的任何值、`OMIXOR_NO_BROWSER` 的任何值)維持舊版行為,不會讓啟動失敗。

| 設定 | 以前 | 現在 |
|---|---|---|
| `PORT`(server) | 不檢查,原樣當成監聽位址(0 = 隨機埠、不合法的值到監聽時才失敗) | 要是 1–65535 的整數,否則啟動失敗 |
| `OMIXOR_WEB_PORT`(web) | 不合法的值靜默改用預設 5191 | 要是 1–65535 的整數,否則啟動失敗 |
| `OMIXOR_TOKEN_DAYS`(server) | 非整數當 0(token 不到期),負數照用 | 要是 0–36500 的整數(0 仍是不到期),負數、非整數啟動失敗 |
| `OMIXOR_AGENT_CRED_HOURS`(server) | 不是數字就忽略、用預設 | 不是數字啟動失敗(0 或負數仍是「用預設」) |
| `OMIXOR_SERVER`(cli、web)、`OMIXOR_PUBLIC_URL`、`EVOLEAP_URL`、`OMIXOR_MCP_URL`、`OMIXOR_LDAP_URL` | 不檢查,沒有 scheme 的值到連線時才失敗(或被當成別的意思) | 一定要是含 scheme 的網址(`http`/`https`;LDAP 是 `ldap`/`ldaps`),否則啟動失敗 |
| `OMIXOR_DATABASE_URL` | 不檢查,連線時才由資料庫驅動程式報錯 | 網址形式(含 `://`)的 scheme 要是 `postgres`/`postgresql`;key=value 形式要有 `=`;否則啟動失敗。兩種形式都接受 |
| LDAP 部分設定(server) | 沒設 `OMIXOR_LDAP_URL` 時,其他 LDAP 設定被靜默忽略、LDAP 不啟用 | 設了其他 LDAP 項目卻沒設 `OMIXOR_LDAP_URL`(`OMIXOR_LDAP_BIND_PASSWORD` 除外)啟動失敗,避免漏設 |
| `OMIXOR_LDAP_START_TLS`、`OMIXOR_LDAP_INSECURE_SKIP_VERIFY`、`OMIXOR_LDAP_TRUST_MAIL` | 不認得的值當成「關」 | 要是 1/true/yes/on 或 0/false/no/off,否則啟動失敗 |
| `OMIXOR_WEB_ADDR` 與 `OMIXOR_WEB_PORT`(web) | 兩個都設而連接埠不同時,靜默以 `OMIXOR_WEB_ADDR` 為準 | 連接埠不同啟動失敗(只設其中一個,或讓兩者一致) |
| `OMIXOR_NO_TIMERS` 等布林設定 | 不認得的值當成「關」(各程式略有不同) | 要是 1/true/yes/on 或 0/false/no/off,否則啟動失敗 |
| 整數型設定(`PGCTLTIMEOUT` 等) | 不檢查,由使用它的程式或 pg_ctl 處理 | 非整數或超出範圍啟動失敗 |

## 測試/開發用變數

這些只給測試與開發流程用,不是正式設定:不進上面的表、不經過 `core/config`,測試程式碼直接讀。

| 名稱 | 說明 |
|---|---|
| `OMIXOR_TEST_DATABASE_URL` | 測試用 PostgreSQL 連線網址;沒設就自動起內建 PostgreSQL(core/storetest/testdb) |
| `OMIXOR_TEST_NO_EMBEDDED_PG` | 設 1:需要 PostgreSQL 的測試略過(不自動起內建的) |
| `OPS_TEST_DATABASE_URL` | OMIXOR_TEST_DATABASE_URL 的舊名稱(測試輔助仍支援) |
| `OPS_TEST_NO_EMBEDDED_PG` | OMIXOR_TEST_NO_EMBEDDED_PG 的舊名稱(測試輔助仍支援) |
| `FAKE_CLAUDE` | 設 1:測試用的假 claude 程序(core/claude) |
| `UPDATE_CONTRACT` | 設 1:重寫 core/model 的契約範例檔 |
| `UPDATE_CONFIG_DOC` | 設 1:重寫 docs/configuration.md(core/config 的測試) |
| `OMIXOR_KEYCHAIN_TEST` | 設 1:用暫存鑰匙圈檔案測 macOS /usr/bin/security(console) |
| `OMIXOR_STRICT` | 設 1:還沒實作的 MCP 工具會讓測試失敗(console/internal/mcp) |
| `FLOWCHART_OUT` | 指定資料夾:流程圖測試把圖存下來看 |
| `WIREFRAME_OUT` | 指定資料夾:線框圖測試把圖存下來看 |

## 系統變數

`PATH`、`SHELL` 是作業系統的變數,不屬於 omixor 設定,程式直接讀(找 claude 執行檔、取得登入 shell 的 PATH)。

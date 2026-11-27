package config

import "math"

// 這個檔案是 omixor 所有正式環境變數的唯一定義:名稱、舊名稱、型別、預設值、說明、是否機密、屬於哪些程式。
// 新增設定只改這裡;-h 的環境變數說明與 docs/configuration.md 都由這份定義產生(core/config 的測試會比對文件)。
// 程式碼不要直接讀環境變數(os.Getenv);啟動時 config.Load(程式) 讀一次、驗證,之後用回傳的 *Config。

// 群組(文件的章節順序)
const (
	GroupShared = "共用"
	GroupServer = "伺服器(omixor-server)"
	GroupLDAP   = "LDAP 登入(omixor-server)"
	GroupGitLab = "GitLab 登入(omixor-server)"
	GroupLocal  = "本機守護服務(omixor-local)"
	GroupWeb    = "畫面與 API(omixor-web)"
	GroupCLI    = "命令列(omixor)"
	GroupMCP    = "MCP 轉接(omixor-mcp)"
)

// Groups 文件裡群組的順序
var Groups = []string{GroupShared, GroupServer, GroupLDAP, GroupGitLab, GroupLocal, GroupWeb, GroupCLI, GroupMCP}

var portRange = &[2]int{1, 65535}

// ---- 共用 ----

var (
	DatabaseURL = add(&Var{Name: "OMIXOR_DATABASE_URL", Legacy: []string{"OPS_DATABASE_URL"}, Kind: Conn, Secret: true,
		Schemes: []string{"postgres", "postgresql"}, Group: GroupShared, Programs: progs(Server, Local, Web), RequiredFor: progs(Server),
		Doc: "PostgreSQL 連線設定,網址形式(postgres://使用者:密碼@主機/資料庫,可有多個主機、unix socket)或 key=value 形式" +
			"(host=/cloudsql/專案:區域:執行個體 user=… password=… dbname=…,Cloud SQL 常用);密碼(網址的密碼、query 的 password/sslpassword、key=value 的 password=)會被遮蔽。" +
			"server 必填;local 沒設就啟動內建的 PostgreSQL;web 也必填(沒設就不能用組織與 Issue 等)。" +
			"獨立模式的 omixor-local 也會使用它(例如開發用的 docker-compose 為 omixor-web 設的)"})
	LDAPBindPassword = add(&Var{Name: "OMIXOR_LDAP_BIND_PASSWORD", Legacy: []string{"OPS_LDAP_BIND_PASSWORD"}, Kind: String, Secret: true,
		Group: GroupLDAP, Programs: progs(Server, Web),
		Doc: "LDAP 服務帳號密碼。server 用在 OMIXOR_LDAP_BIND_DN;web 不想用鑰匙圈/資料庫保存時改從這裡讀"})
	Addr = add(&Var{Name: "OMIXOR_ADDR", Kind: String, Group: GroupShared, Programs: progs(Server, Local),
		DefaultDoc: "server: :$PORT(沒有 PORT 就 :8080);local:獨立模式 127.0.0.1:8770、App 模式 127.0.0.1:0",
		Doc:        "HTTP 監聽位址。local 只接受本機位址"})
	ServerURL = add(&Var{Name: "OMIXOR_SERVER", Legacy: []string{"OPS_ISSUE_SERVER"}, Kind: URL, Schemes: []string{"http", "https"},
		Group: GroupShared, Programs: progs(CLI, Web),
		DefaultDoc: "cli:上次登入的、舊版登入資料的、正式站",
		Doc: "連哪一台 omixor server。cli:--server 旗標優先於它。web:不再使用(遠端模式已移除);升級前只靠它進遠端模式的安裝會拒絕啟動," +
			"執行一次 omixor-web leave-remote 或拿掉這個變數"})
)

// ---- 共用:加密機密用的金鑰(目前只有 console 的機密表在用;core/secretbox 還沒有呼叫端) ----

var (
	SecretKey = add(&Var{Name: "OMIXOR_SECRET_KEY", Legacy: []string{"OPS_SECRET_KEY"}, Kind: Key, Secret: true, Group: GroupShared, Programs: progs(Server, Web),
		Doc: "加密機密用的金鑰:32 bytes,寫成 64 個 hex 字元或 base64(openssl rand -base64 32)。console 用資料庫時必填(加密 token 與密碼);" +
			"伺服器目前只檢查格式,還沒有用它加密任何資料。" +
			"要固定保存:console 只用這一把解密,換掉後已存的密鑰會解不開(就算把舊的放進 OMIXOR_SECRET_KEYS_OLD 也一樣)"})
	SecretKeysOld = add(&Var{Name: "OMIXOR_SECRET_KEYS_OLD", Kind: KeyList, Secret: true, Group: GroupShared, Programs: progs(Server, Web),
		DefaultDoc: "沒有舊金鑰", Doc: "保留給金鑰輪替的舊金鑰清單,逗號分隔、格式同 OMIXOR_SECRET_KEY。目前只檢查格式:console 不讀它、伺服器也還沒有用到," +
			"不能靠它更換金鑰"})
)

// ---- 伺服器 ----

var (
	PublicURL = add(&Var{Name: "OMIXOR_PUBLIC_URL", Kind: URL, Schemes: []string{"http", "https"}, Group: GroupServer, Programs: progs(Server, Web),
		Doc: "對外的網址(例如 https://omixor.io);領取任務時附上角色端點 <網址>/mcp/agent,也是 OAuth 的 issuer 與 resource 的來源。local 啟動時自己填上。" +
			"web:對外 MCP(OAuth)的網址,必須是 https(本機位址 localhost、127.0.0.1、[::1] 除外),只放網址、不放路徑。沒設 OMIXOR_OAUTH_ADDR 時,畫面連接埠的 MCP 入口(/mcp、/oauth/*,和 server 相同;需要 PostgreSQL)以它為對外網址;沒設或不合格(只警告)時入口只收同一台電腦直接連進來的,網址是 http://127.0.0.1:<畫面連接埠>;設了 OMIXOR_OAUTH_ADDR 時入口在那個連接埠(這時必填)"})
	SiteDir = add(&Var{Name: "OMIXOR_SITE_DIR", Kind: Path, Group: GroupServer, Programs: progs(Server),
		Doc: "形象網站檔案所在的資料夾(Docker 映像裡是 /site);沒設或不存在就不提供網站"})
	TokenDays = add(&Var{Name: "OMIXOR_TOKEN_DAYS", Kind: Int, Default: "90", Range: &[2]int{0, 36500}, Group: GroupServer, Programs: progs(Server),
		Doc: "使用者 token 的有效天數"})
	AdminBootstrapEmail = add(&Var{Name: "OMIXOR_ADMIN_BOOTSTRAP_EMAIL", Kind: String, Group: GroupServer, Programs: progs(Server),
		Doc: "第一位管理員的 email(資料庫還沒有管理員時,這個 email 登入就成為管理員)"})
	AgentCredHours = add(&Var{Name: "OMIXOR_AGENT_CRED_HOURS", Kind: Float, Group: GroupServer, Programs: progs(Server),
		DefaultDoc: "服務內建的預設",
		Doc:        "領取任務時發給角色的憑證有效小時數(要大於 0,否則用預設)"})
	TrustedProxyHops = add(&Var{Name: "OMIXOR_TRUSTED_PROXY_HOPS", Kind: Int, Range: &[2]int{0, 16}, Group: GroupServer, Programs: progs(Server, Web),
		DefaultDoc: "server:有 K_SERVICE(Cloud Run)是 1,否則 0;web:0",
		Doc: "信任的代理層數,決定怎麼取請求來源 IP(頻率限制、稽核用;web 用在登入與 MCP 授權)。0 = 不看 X-Forwarded-For,用連線位址;N = 取 X-Forwarded-For 由右數第 N 個" +
			"(最右邊是離伺服器最近的代理加的;項目不足或不是合法 IP 就退回連線位址)。Cloud Run 直接對外是 1;前面再加 HTTPS 負載平衡器要設 2。" +
			"設太大會讓呼叫者能自己填來源、繞過每 IP 限制;設太小會讓代理的位址被當成來源、全部人共用一個額度"})
	KService = add(&Var{Name: "K_SERVICE", Kind: String, Group: GroupServer, Programs: progs(Server),
		Doc: "Cloud Run 自動帶入的服務名稱(不是 omixor 專用名稱);有值就表示跑在 Cloud Run,OMIXOR_TRUSTED_PROXY_HOPS 預設 1"})
	Port = add(&Var{Name: "PORT", Kind: Int, Default: "8080", Range: portRange, Group: GroupServer, Programs: progs(Server),
		Doc: "Cloud Run 等平台指定的連接埠;沒設 OMIXOR_ADDR 時監聽 :$PORT(不是 omixor 專用名稱,所以沒有 OMIXOR_ 前綴)"})
	EvoleapURL = add(&Var{Name: "EVOLEAP_URL", Kind: URL, Default: "https://evoleap.io", Schemes: []string{"http", "https"}, Group: GroupServer, Programs: progs(Server, Web),
		Doc: "evoleap 登入服務的網址(沒有 OMIXOR_ 前綴,沿用原名)。web:只有對外 MCP 連接埠(OMIXOR_OAUTH_ADDR)的 evoleap 登入用"})
	OAuthEvoleapLogin = add(&Var{Name: "OMIXOR_OAUTH_EVOLEAP_LOGIN", Kind: Bool, Default: "true", Group: GroupServer, Programs: progs(Server, Web),
		Doc: "OAuth 同意畫面提供「用 evoleap 登入並允許」(瀏覽器登入,不用配對碼;需要 OMIXOR_PUBLIC_URL)。server:設 false 關掉,同意畫面只剩配對碼;" +
			"web:同意畫面是 evoleap、GitLab 與 LDAP 帳號密碼(console 的認證設定啟用 LDAP 時;不提供配對碼);只用 LDAP 時可以設 false"})
)

// ---- LDAP(omixor-server;ADMINS、EMAIL_DOMAIN、TRUST_MAIL、BIND_PASSWORD 另外也給 console,完整的檢查在 core/ldapauth,其他連線設定 console 用畫面) ----

var (
	LDAPURL = add(&Var{Name: "OMIXOR_LDAP_URL", Kind: URL, Schemes: []string{"ldap", "ldaps"}, Group: GroupLDAP, Programs: progs(Server),
		Doc: "LDAP / Active Directory 位址,ldap://主機:389 或 ldaps://主機:636。沒設 = 不啟用 LDAP 登入;設了就必須設 BIND_DN 或 USER_DN_TEMPLATE 其中一個"})
	LDAPStartTLS = add(&Var{Name: "OMIXOR_LDAP_START_TLS", Kind: Bool, Group: GroupLDAP, Programs: progs(Server),
		Doc: "ldap:// 連線升級成 StartTLS(ldaps:// 不用)"})
	LDAPCACert = add(&Var{Name: "OMIXOR_LDAP_CA_CERT", Kind: Path, Group: GroupLDAP, Programs: progs(Server),
		Doc: "驗證 LDAP 伺服器憑證用的 CA 憑證檔(PEM)"})
	LDAPInsecureSkipVerify = add(&Var{Name: "OMIXOR_LDAP_INSECURE_SKIP_VERIFY", Kind: Bool, Group: GroupLDAP, Programs: progs(Server),
		Doc: "不驗證 LDAP 伺服器憑證(只建議測試用)"})
	LDAPBindDN = add(&Var{Name: "OMIXOR_LDAP_BIND_DN", Kind: String, Group: GroupLDAP, Programs: progs(Server),
		Doc: "服務帳號 DN(用服務帳號搜尋使用者的方式);要同時給密碼與 OMIXOR_LDAP_BASE_DN"})
	LDAPBindPasswordFile = add(&Var{Name: "OMIXOR_LDAP_BIND_PASSWORD_FILE", Kind: Path, Group: GroupLDAP, Programs: progs(Server),
		Doc: "服務帳號密碼存在這個檔案(Docker secret);OMIXOR_LDAP_BIND_PASSWORD 有設時以它為準"})
	LDAPBaseDN = add(&Var{Name: "OMIXOR_LDAP_BASE_DN", Kind: String, Group: GroupLDAP, Programs: progs(Server),
		Doc: "搜尋使用者的起點 DN(用服務帳號搜尋時必填)"})
	LDAPUserFilter = add(&Var{Name: "OMIXOR_LDAP_USER_FILTER", Kind: String, Group: GroupLDAP, Programs: progs(Server),
		DefaultDoc: "(<ID_ATTR>=%s)", Doc: "搜尋使用者的過濾條件,要剛好有一個 %s(換成帳號)"})
	LDAPUserDNTemplate = add(&Var{Name: "OMIXOR_LDAP_USER_DN_TEMPLATE", Kind: String, Group: GroupLDAP, Programs: progs(Server),
		Doc: "直接用使用者身分 bind 的 DN 樣板,要剛好有一個 %s(換成帳號)"})
	LDAPIDAttr = add(&Var{Name: "OMIXOR_LDAP_ID_ATTR", Kind: String, DefaultDoc: "sAMAccountName", Group: GroupLDAP, Programs: progs(Server),
		Doc: "帳號對應的屬性"})
	LDAPNameAttr = add(&Var{Name: "OMIXOR_LDAP_NAME_ATTR", Kind: String, DefaultDoc: "displayName", Group: GroupLDAP, Programs: progs(Server),
		Doc: "顯示名稱對應的屬性"})
	LDAPMailAttr = add(&Var{Name: "OMIXOR_LDAP_MAIL_ATTR", Kind: String, DefaultDoc: "mail", Group: GroupLDAP, Programs: progs(Server),
		Doc: "email 對應的屬性"})
	LDAPTimeoutSeconds = add(&Var{Name: "OMIXOR_LDAP_TIMEOUT_SECONDS", Kind: Int, Default: "10", Range: &[2]int{1, math.MaxInt}, Group: GroupLDAP, Programs: progs(Server),
		Doc: "LDAP 連線與查詢的逾時秒數"})
	LDAPAdmins = add(&Var{Name: "OMIXOR_LDAP_ADMINS", Kind: String, Group: GroupLDAP, Programs: progs(Server, Web),
		Doc: "伺服器管理員的帳號(逗號分隔),每次登入都確認。web:console 的網頁管理員名單,有設時蓋掉認證設定裡的名單;名單空白就沒有網頁管理員"})
	LDAPEmailDomain = add(&Var{Name: "OMIXOR_LDAP_EMAIL_DOMAIN", Kind: String, Group: GroupLDAP, Programs: progs(Server, Web),
		Doc: "目錄沒有 mail 時,用 帳號@這個網域 當 email;只要網域(例如 corp.example.com)。web:有設時蓋掉認證設定裡的值"})
	LDAPTrustMail = add(&Var{Name: "OMIXOR_LDAP_TRUST_MAIL", Kind: Bool, Group: GroupLDAP, Programs: progs(Server, Web),
		Doc: "信任目錄的 mail:預先加入的同 email 使用者登入時自動對上。web:有設時蓋掉認證設定裡的值"})
)

// ---- 自建 GitLab 的 OAuth2 登入(omixor-server,以及 console 的對外 MCP 連接埠;設了 OMIXOR_GITLAB_URL 才啟用,需要 OMIXOR_PUBLIC_URL) ----

var (
	GitLabURL = add(&Var{Name: "OMIXOR_GITLAB_URL", Kind: URL, Schemes: []string{"http", "https"}, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "自建 GitLab 的網址(例如 https://gitlab.example.com,可帶子路徑)。沒設 = 不啟用 GitLab 登入;web 只有對外 MCP 連接埠(OMIXOR_OAUTH_ADDR)的同意畫面用;設了就必須同時有 CLIENT_ID、密鑰與 OMIXOR_PUBLIC_URL。" +
			"GitLab Application 的 Redirect URI 要登記 <OMIXOR_PUBLIC_URL>/oauth/gitlab/callback 與 <OMIXOR_PUBLIC_URL>/api/auth/gitlab/callback(console 只要前者),scope 勾 read_user,勾 Confidential"})
	GitLabClientID = add(&Var{Name: "OMIXOR_GITLAB_CLIENT_ID", Kind: String, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "GitLab Application 的 Application ID"})
	GitLabClientSecret = add(&Var{Name: "OMIXOR_GITLAB_CLIENT_SECRET", Kind: String, Secret: true, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "GitLab Application 的 Secret"})
	GitLabClientSecretFile = add(&Var{Name: "OMIXOR_GITLAB_CLIENT_SECRET_FILE", Kind: Path, Secret: true, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "Application 的 Secret 存在這個檔案(Docker secret);OMIXOR_GITLAB_CLIENT_SECRET 有設時以它為準"})
	GitLabCACert = add(&Var{Name: "OMIXOR_GITLAB_CA_CERT", Kind: Path, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "GitLab 用自簽憑證時的 CA 憑證檔(PEM;加在系統信任清單之外)"})
	GitLabAdmins = add(&Var{Name: "OMIXOR_GITLAB_ADMINS", Kind: String, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "伺服器管理員的 GitLab 使用者 id(數字,逗號分隔;不收 username,因為 username 改掉後別人可以拿去用)。每次登入都確認,只升不降"})
	GitLabEmailDomains = add(&Var{Name: "OMIXOR_GITLAB_EMAIL_DOMAINS", Kind: String, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "允許用 GitLab 登入的 email 網域(逗號分隔,例如 corp.example.com;完全相同才算,不含子網域)。沒設 = 不限。GitLab 開放註冊時一定要設;" +
			"另外 GitLab 要開啟 email 確認(omixor 只看 confirmed_at)"})
	GitLabLinkLDAP = add(&Var{Name: "OMIXOR_GITLAB_LINK_LDAP", Kind: Bool, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "同一個人的 GitLab 與 LDAP 併到同一帳號。要同時啟用 LDAP 且有服務帳號(OMIXOR_LDAP_BIND_DN):用 GitLab 帳號的 LDAP 身分(DN)查目錄確認," +
			"email 也要相同才併;web 不支援(console 沒有核心的 LDAP 登入,設了會啟動失敗);開啟後,有 LDAP 身分但查不到目錄帳號或 email 不符的人不能登入;GitLab 的 LDAP 與 omixor 的要是同一個目錄"})
	GitLabTrustMail = add(&Var{Name: "OMIXOR_GITLAB_TRUST_MAIL", Kind: Bool, Group: GroupGitLab, Programs: progs(Server, Web),
		Doc: "信任 GitLab 確認過的 email:預先加入的同 email 使用者登入時自動對上"})
)

// ---- 本機守護服務 ----

var (
	DataDir = add(&Var{Name: "OMIXOR_DATA_DIR", Kind: Path, Group: GroupLocal, Programs: progs(Local),
		DefaultDoc: "獨立模式:<使用者設定資料夾>/omixor/core;App 模式必填",
		Doc:        "資料夾(PostgreSQL 資料、credentials.json 等)。-data 旗標優先"})
	LocalToken = add(&Var{Name: "OMIXOR_LOCAL_TOKEN", Kind: String, Secret: true, Group: GroupLocal, Programs: progs(Local),
		Doc: "macOS App 給的 token(omx_ 開頭,每次啟動換一把);有設就是 App 模式,沒設是獨立模式。讀取後立刻從環境移除,子程序不會繼承"})
	LocalUser = add(&Var{Name: "OMIXOR_LOCAL_USER", Kind: String, DefaultDoc: "local", Group: GroupLocal, Programs: progs(Local),
		Doc: "本機使用者的 email 或 id"})
	LocalName = add(&Var{Name: "OMIXOR_LOCAL_NAME", Kind: String, DefaultDoc: "同 OMIXOR_LOCAL_USER", Group: GroupLocal, Programs: progs(Local),
		Doc: "本機使用者的顯示名稱"})
	LocalOrg = add(&Var{Name: "OMIXOR_LOCAL_ORG", Kind: String, DefaultDoc: "default", Group: GroupLocal, Programs: progs(Local),
		Doc: "確保存在的預設組織 id(不能刪)"})
	ExitOnStdinEOF = add(&Var{Name: "OMIXOR_EXIT_ON_STDIN_EOF", Kind: Bool, Group: GroupLocal, Programs: progs(Local),
		Doc: "標準輸入關掉就結束(App 結束或當掉時守護服務跟著結束;只在 App 模式有作用)"})
	PGCtlTimeout = add(&Var{Name: "PGCTLTIMEOUT", Kind: Int, Range: &[2]int{1, 86400}, Group: GroupLocal, Programs: progs(Local),
		DefaultDoc: "依啟動逾時推算(至少 60)",
		Doc:        "pg_ctl 啟動內建 PostgreSQL 的等待秒數(pg_ctl 自己認的變數;自己設了就用你的)"})
)

// ---- 畫面與 API(console / omixor-web) ----

var (
	EvoleapConsole = add(&Var{Name: "OMIXOR_EVOLEAP_CONSOLE", Kind: URL, Schemes: []string{"http", "https"}, Group: GroupWeb, Programs: progs(Server, Web),
		Doc: "evoleap 核准頁網址(沒設就用 evoleap 的網址)。伺服器用它把 OAuth 同意畫面的「用 evoleap 登入並允許」導去 <網址>/login/cli?id=…"})
	NoTimers = add(&Var{Name: "OMIXOR_NO_TIMERS", Legacy: []string{"OPS_NO_TIMERS"}, Kind: Bool, Group: GroupWeb, Programs: progs(Web),
		Doc: "不開背景計時器(例行任務、輪詢;測試用)"})
	NoBrowser = add(&Var{Name: "OMIXOR_NO_BROWSER", Legacy: []string{"OPS_NO_BROWSER"}, Kind: Flag, Group: GroupWeb, Programs: progs(Web),
		Doc: "啟動時不自動開瀏覽器(Docker 映像預設開著)。有設就不開:任何非空值都算,包含 0、false"})
	ConsoleDataDir = add(&Var{Name: "OMIXOR_CONSOLE_DATA_DIR", Legacy: []string{"OPS_DATA_DIR"}, Kind: Path, Group: GroupWeb, Programs: progs(Web, MCP),
		DefaultDoc: "~/Library/Application Support/omixor-console(依系統)", Doc: "資料夾(範例資料、設定、auth.json 等);測試一律指到暫存資料夾"})
	Auth = add(&Var{Name: "OMIXOR_AUTH", Legacy: []string{"OPS_AUTH"}, Kind: Enum, Soft: true, Choices: []string{"off"}, Group: GroupWeb, Programs: progs(Web),
		Doc: "off(不分大小寫)= 強制不用登入(設定錯了進不去時用它回到本機模式);其他值只在啟動時警告、一律忽略"})
	APIToken = add(&Var{Name: "OMIXOR_API_TOKEN", Legacy: []string{"OPS_API_TOKEN"}, Kind: String, Secret: true, Group: GroupWeb, Programs: progs(Web),
		Doc: "/api/issues/ 可以用的 Bearer token(至少 16 字元,不夠長視為沒設)"})
	ShuttlePath = add(&Var{Name: "OMIXOR_SHUTTLE_PATH", Legacy: []string{"OPS_SHUTTLE_PATH"}, Kind: Path, Group: GroupWeb, Programs: progs(Web),
		Doc: "語音 shuttle 資料夾(優先於畫面裡的設定)"})
	ClaudeBin = add(&Var{Name: "OMIXOR_CLAUDE_BIN", Kind: Path, Group: GroupWeb, Programs: progs(Web),
		DefaultDoc: "常見安裝位置,再找 PATH", Doc: "claude 執行檔路徑(自動執行角色任務用)"})
	OfficeDir = add(&Var{Name: "OMIXOR_OFFICE_DIR", Kind: Path, Group: GroupWeb, Programs: progs(Web),
		DefaultDoc: "<執行檔>/../../office3d、<執行檔>/../office3d、<執行檔>/office3d", Doc: "3D 辦公室(office3d)資料夾,裡面要有 places.json"})
	Templates = add(&Var{Name: "OMIXOR_TEMPLATES", Kind: String, Group: GroupWeb, Programs: progs(Web),
		DefaultDoc: "不載入範本", Doc: "範本資料:1/on/true/yes/auto = 自動找 templates/;0/off/false/no = 不載入;其他值 = 範本資料夾路徑"})
	MCPListTTLSeconds = add(&Var{Name: "OMIXOR_MCP_LIST_TTL_SECONDS", Kind: Int, Default: "300", Range: &[2]int{0, 86400}, Group: GroupShared, Programs: progs(Server, Local, Web),
		Doc: "MCP 2026-07-28 請求的快取提示:server/discover、tools/list、resources/list、resources/read 結果的 ttlMs 取自這個秒數(0 = 一律視為過期);舊版協定的回應不帶快取提示"})
	OAuthAddr = add(&Var{Name: "OMIXOR_OAUTH_ADDR", Kind: String, Group: GroupWeb, Programs: progs(Web),
		DefaultDoc: "不啟用",
		Doc: "對外 MCP 連接埠的監聽位址(例如 127.0.0.1:8771);設了才啟用,讓遠端 MCP 用戶端(claude.ai 等)連到這台 console。" +
			"服務 /oauth/*、/.well-known/*、/mcp、/mcp/org/{org}、/mcp/agent、/mcp/worker/{org},認證與核心工具和 omixor server 相同(OAuth 登入用 evoleap 或 GitLab,也收 omx_ 使用者 token;console 專有工具只在 /mcp 給 OAuth 連線,見 console/README.md)," +
			"反向代理或 Tailscale Funnel 只轉發這個連接埠。" +
			"需要 PostgreSQL(OMIXOR_DATABASE_URL)、OMIXOR_PUBLIC_URL(https),evoleap 要登記回呼網址 <OMIXOR_PUBLIC_URL>/oauth/evoleap/callback"})
	WebPort = add(&Var{Name: "OMIXOR_WEB_PORT", Legacy: []string{"OPS_WEB_PORT"}, Kind: Int, Default: "5191", Range: portRange, Group: GroupWeb, Programs: progs(Web, MCP),
		Doc: "畫面與 API 的連接埠(沒設 OMIXOR_WEB_ADDR 時聽 127.0.0.1:<連接埠>);MCP 入口也在這個連接埠,omixor-mcp 用它組出預設網址"})
	WebAddr = add(&Var{Name: "OMIXOR_WEB_ADDR", Legacy: []string{"OPS_WEB_ADDR"}, Kind: String, DefaultDoc: "127.0.0.1:<OMIXOR_WEB_PORT>", Group: GroupWeb, Programs: progs(Web),
		Doc: "畫面聽的位址,例如 0.0.0.0:5191;不是本機位址時必須先啟用 LDAP 登入(或設 OMIXOR_WEB_LOCAL_ONLY_PUBLISH)"})
	WebHosts = add(&Var{Name: "OMIXOR_WEB_HOSTS", Legacy: []string{"OPS_WEB_HOSTS"}, Kind: String, Group: GroupWeb, Programs: progs(Web),
		Doc: "允許的主機名稱(逗號分隔);反向代理的網域要列在這裡"})
	WebLocalOnlyPublish = add(&Var{Name: "OMIXOR_WEB_LOCAL_ONLY_PUBLISH", Legacy: []string{"OPS_WEB_LOCAL_ONLY_PUBLISH"}, Kind: Gate, Group: GroupWeb, Programs: progs(Web),
		Doc: "安全閘門:在 Docker 裡聽 0.0.0.0、但連接埠只發布到主機的 127.0.0.1 時設 1,可略過「不是本機位址必須啟用 LDAP」的檢查。只認精確的 1,其他值(true、yes、on…)一律視為沒設"})
)

// ---- 命令列(omixor) ----

var (
	Credentials = add(&Var{Name: "OMIXOR_CREDENTIALS", Kind: Path, Group: GroupCLI, Programs: progs(CLI),
		DefaultDoc: "<設定資料夾>/credentials/<伺服器>.json", Doc: "登入資料檔(指定單一檔案時只用那個檔,不記「上次登入的伺服器」)"})
	ConfigDir = add(&Var{Name: "OMIXOR_CONFIG_DIR", Kind: Path, Group: GroupCLI, Programs: progs(CLI, MCP),
		DefaultDoc: "~/.config/omixor", Doc: "CLI 設定資料夾(登入資料、目前的伺服器);omixor-mcp 的 OAuth 登入也存在它底下的 mcp/"})
	CI = add(&Var{Name: "CI", Kind: String, Group: GroupCLI, Programs: progs(CLI),
		Doc: "CI 平台(GitHub Actions、GitLab CI 等)自動帶入的標記(不是 omixor 專用名稱,所以沒有 OMIXOR_ 前綴);值為 true 時," +
			"omixor commits sync 沒給 --source 就把來源記成 ci,否則記成 cli"})
)

// ---- MCP 轉接(omixor-mcp) ----

var (
	MCPURL = add(&Var{Name: "OMIXOR_MCP_URL", Legacy: []string{"OPS_MCP_URL"}, Kind: URL, Schemes: []string{"http", "https"}, Group: GroupMCP, Programs: progs(MCP),
		DefaultDoc: "http://127.0.0.1:<OMIXOR_WEB_PORT>/mcp", Doc: "omixor 的 MCP 入口網址(統一入口,結尾是 /mcp;console 或 omixor server 的都可以)。--channel 與 --init-worker 的執行者端點是它後面接 /worker/<組織>。要和伺服器公告的網址完全相同(沒設 OMIXOR_PUBLIC_URL 的 console 是 http://127.0.0.1:<連接埠>/mcp)"})
)

// DevVar 測試/開發用的環境變數:不是正式設定,不進設定表與 Config,測試程式碼直接讀
type DevVar struct {
	Name string
	Doc  string
}

// DevVars 測試/開發用變數(文件另列一節;守門測試不檢查測試檔)
var DevVars = []DevVar{
	{"OMIXOR_TEST_DATABASE_URL", "測試用 PostgreSQL 連線網址;沒設就自動起內建 PostgreSQL(core/storetest/testdb)"},
	{"OMIXOR_TEST_NO_EMBEDDED_PG", "設 1:需要 PostgreSQL 的測試略過(不自動起內建的)"},
	{"OPS_TEST_DATABASE_URL", "OMIXOR_TEST_DATABASE_URL 的舊名稱(測試輔助仍支援)"},
	{"OPS_TEST_NO_EMBEDDED_PG", "OMIXOR_TEST_NO_EMBEDDED_PG 的舊名稱(測試輔助仍支援)"},
	{"FAKE_CLAUDE", "設 1:測試用的假 claude 程序(core/claude)"},
	{"UPDATE_CONTRACT", "設 1:重寫 core/model 的契約範例檔"},
	{"UPDATE_CONFIG_DOC", "設 1:重寫 docs/configuration.md(core/config 的測試)"},
	{"OMIXOR_KEYCHAIN_TEST", "設 1:用暫存鑰匙圈檔案測 macOS /usr/bin/security(console)"},
	{"OMIXOR_STRICT", "設 1:還沒實作的 MCP 工具會讓測試失敗(console/internal/mcp)"},
	{"FLOWCHART_OUT", "指定資料夾:流程圖測試把圖存下來看"},
	{"WIREFRAME_OUT", "指定資料夾:線框圖測試把圖存下來看"},
}

// SystemVars 不屬於 omixor 設定、保持原樣直接讀的系統變數
var SystemVars = []string{"PATH", "SHELL"}

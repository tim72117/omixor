package mcpauth

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"omixor-core/config"
	"omixor-core/model"
)

// ErrLoginRequired 這個 resource 沒有可用的登入(沒登入過、已登出、授權過期或被撤銷)
var ErrLoginRequired = errors.New("還沒登入,或授權已過期、被撤銷")

// Client 一個 resource 的 OAuth 用戶端
type Client struct {
	Store    Store
	Resource string
	HTTP     *http.Client // nil = HTTPClient(Resource)
	Now      func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// HTTPClient 連這個網址用的 client:本機位址不走系統代理;不跟隨轉址(token 不會被帶去別的地方)
func HTTPClient(raw string, timeout time.Duration) *http.Client {
	t := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if u, err := url.Parse(raw); err == nil && config.IsLoopbackHost(u.Hostname()) {
		t.Proxy = nil
	}
	return &http.Client{Timeout: timeout, Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return HTTPClient(c.Resource, 30*time.Second)
}

// IsWorkerResource 這個網址是不是執行者端點
func IsWorkerResource(resource string) bool {
	_, suffix, err := splitResource(resource)
	return err == nil && strings.HasPrefix(suffix, "/mcp/worker/")
}

var resourceSuffix = regexp.MustCompile(`/mcp(?:/(?:org|worker)/[^/]+)?$`)

// splitResource 把 MCP 網址拆成伺服器的對外網址與端點路徑(/mcp、/mcp/org/{org}、/mcp/worker/{org}),並檢查可以安全送 token:
// https(本機位址可用 http)、沒有帳號密碼、查詢與片段
func splitResource(resource string) (public, suffix string, err error) {
	u, err := url.Parse(resource)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("MCP 網址格式不對:%q", resource)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && config.IsLoopbackHost(u.Hostname())) {
		return "", "", fmt.Errorf("MCP 網址要是 https(只有本機位址可以用 http):%s", resource)
	}
	// 比對網址路徑的結尾,不能找最後一個 "/mcp":組織 id 可以用 mcp 開頭(/mcp/worker/mcp-team)
	path := u.EscapedPath()
	loc := resourceSuffix.FindStringIndex(path)
	if loc == nil || !strings.HasSuffix(resource, path) {
		return "", "", fmt.Errorf("MCP 網址要以 /mcp、/mcp/org/<組織> 或 /mcp/worker/<組織> 結尾:%s", resource)
	}
	suffix = path[loc[0]:]
	public = strings.TrimSuffix(resource, suffix)
	if suffix != "/mcp" {
		org, ok := strings.CutPrefix(suffix, "/mcp/org/")
		if !ok {
			org, ok = strings.CutPrefix(suffix, "/mcp/worker/")
		}
		if !ok || !model.ValidOrgID(org) {
			return "", "", fmt.Errorf("MCP 網址要以 /mcp、/mcp/org/<組織> 或 /mcp/worker/<組織> 結尾:%s", resource)
		}
	}
	return public, suffix, nil
}

type serverMeta struct {
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	Registration  string `json:"registration_endpoint"`
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("連不到 %s:%w", u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s 回應 HTTP %d:%s", u, resp.StatusCode, clip(b))
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s 的回應不是 JSON(這個伺服器沒有啟用 MCP 的 OAuth?)", u)
	}
	return nil
}

func clip(b []byte) string {
	s := strings.TrimSpace(string(b))
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300])
	}
	return s
}

// discover 查這個 resource 的授權伺服器。授權伺服器與各端點一定要在同一個 omixor 底下(<對外網址>/oauth/…)
func (c *Client) discover(ctx context.Context) (serverMeta, error) {
	var m serverMeta
	public, suffix, err := splitResource(c.Resource)
	if err != nil {
		return m, err
	}
	var pr struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
	}
	if err := c.getJSON(ctx, public+"/.well-known/oauth-protected-resource"+suffix, &pr); err != nil {
		return m, err
	}
	if pr.Resource != c.Resource {
		return m, fmt.Errorf("伺服器公告的 MCP 網址是 %s,和要連的 %s 不同;請改用伺服器公告的網址(--url 或 OMIXOR_MCP_URL,例如不要把 127.0.0.1 寫成 localhost)", pr.Resource, c.Resource)
	}
	issuer := public + "/oauth"
	if len(pr.Servers) != 1 || pr.Servers[0] != issuer {
		return m, fmt.Errorf("授權伺服器不是 %s,不繼續", issuer)
	}
	if err := c.getJSON(ctx, issuer+"/.well-known/openid-configuration", &m); err != nil {
		return m, err
	}
	for _, e := range []string{m.Authorization, m.Token, m.Registration} {
		if !strings.HasPrefix(e, issuer+"/") {
			return serverMeta{}, fmt.Errorf("授權伺服器的端點不在 %s 底下,不繼續", issuer)
		}
	}
	return m, nil
}

func (c *Client) register(ctx context.Context, endpoint, redirect string) (string, error) {
	body, _ := json.Marshal(map[string]any{"client_name": "omixor-mcp", "redirect_uris": []string{redirect},
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("註冊用戶端:%w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		ClientID string `json:"client_id"`
		Desc     string `json:"error_description"`
	}
	_ = json.Unmarshal(b, &out)
	if resp.StatusCode != 201 || out.ClientID == "" {
		return "", fmt.Errorf("註冊用戶端失敗(HTTP %d):%s", resp.StatusCode, out.Desc)
	}
	return out.ClientID, nil
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LoginOptions 登入時的互動方式
type LoginOptions struct {
	// Open 開瀏覽器;nil = 不開(沒有瀏覽器的機器),只印網址
	Open func(url string) error
	// Paste 沒有瀏覽器連得回這台機器時,使用者把最後轉到的網址(http://127.0.0.1:…/callback?code=…)貼進來;nil = 只等本機回呼
	Paste io.Reader
	// Out 給使用者看的說明(不含任何 token)
	Out io.Writer
	// NewClient 重新註冊用戶端(伺服器說不認得舊的時用)
	NewClient bool
	// Timeout 等使用者完成授權多久;0 = 10 分鐘
	Timeout time.Duration
}

// listen 本機回呼:沿用上次註冊的連接埠(redirect_uri 要完全相同);被占用或沒註冊過就換一個,並要重新註冊
func listen(prev string) (ln net.Listener, redirect string, reuse bool, err error) {
	if u, perr := url.Parse(prev); perr == nil && u.Hostname() == "127.0.0.1" && u.Port() != "" {
		if ln, err = net.Listen("tcp4", u.Host); err == nil {
			return ln, prev, true, nil
		}
	}
	if ln, err = net.Listen("tcp4", "127.0.0.1:0"); err != nil {
		return nil, "", false, fmt.Errorf("開不了本機回呼的連接埠:%w", err)
	}
	return ln, "http://" + ln.Addr().String() + "/callback", false, nil
}

type callback struct {
	code string
	err  error
}

// parseCallback 回呼網址的參數:state 不符的(不是這一次的登入)回 ok=false,不理會
func parseCallback(q url.Values, state string) (callback, bool) {
	if q.Get("state") != state {
		return callback{}, false
	}
	if e := q.Get("error"); e != "" {
		return callback{err: fmt.Errorf("授權沒有完成:%s %s", e, q.Get("error_description"))}, true
	}
	if q.Get("code") == "" {
		return callback{}, false
	}
	return callback{code: q.Get("code")}, true
}

// Login 授權碼 + PKCE:註冊(或沿用)用戶端、請使用者在瀏覽器登入並允許、用授權碼換 token、存檔
func (c *Client) Login(ctx context.Context, o LoginOptions) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	meta, err := c.discover(ctx)
	if err != nil {
		return err
	}
	cred, err := c.Store.Load(c.Resource)
	if err != nil {
		return err
	}
	prev := cred.RedirectURI
	if o.NewClient || cred.ClientID == "" {
		prev = ""
	}
	ln, redirect, reuse, err := listen(prev)
	if err != nil {
		return err
	}
	defer ln.Close()
	clientID := cred.ClientID
	if !reuse {
		if clientID, err = c.register(ctx, meta.Registration, redirect); err != nil {
			return err
		}
	}
	verifier, err := randomString()
	if err != nil {
		return err
	}
	state, err := randomString()
	if err != nil {
		return err
	}
	scope := strings.Join([]string{model.OAuthScopeRead, model.OAuthScopeWrite, model.OAuthScopeAdmin, model.OAuthScopeOffline}, " ")
	if IsWorkerResource(c.Resource) {
		scope = model.OAuthScopeWorker
	}
	sum := sha256.Sum256([]byte(verifier))
	authURL := meta.Authorization + "?" + url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {state}, "scope": {scope},
		"resource": {c.Resource}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}.Encode()

	got := make(chan callback, 2)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cb, ok := parseCallback(r.URL.Query(), state)
		if r.URL.Path != "/callback" || !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if cb.err != nil {
			_, _ = io.WriteString(w, "授權沒有完成,請回到終端機看原因。")
		} else {
			_, _ = io.WriteString(w, "授權完成,可以關掉這個分頁、回到終端機。")
		}
		select {
		case got <- cb:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer scancel()
		_ = srv.Shutdown(sctx)
	}()

	fmt.Fprintf(o.Out, "請在瀏覽器開啟下面的網址登入並允許(10 分鐘內):\n\n%s\n\n", authURL)
	if o.Open != nil {
		if err := o.Open(authURL); err != nil {
			fmt.Fprintf(o.Out, "開不了瀏覽器(%v),請自己開啟上面的網址。\n", err)
		}
	}
	if o.Paste != nil {
		fmt.Fprintf(o.Out, "瀏覽器和這台機器不是同一台時:允許後瀏覽器會停在一個打不開的 %s?… 網址,把整串貼到這裡按 Enter。\n", redirect)
		go func() {
			sc := bufio.NewScanner(o.Paste)
			sc.Buffer(make([]byte, 64<<10), 64<<10)
			for sc.Scan() {
				u, err := url.Parse(strings.TrimSpace(sc.Text()))
				if err != nil {
					continue
				}
				if cb, ok := parseCallback(u.Query(), state); ok {
					select {
					case got <- cb:
					default:
					}
					return
				}
				fmt.Fprintln(o.Out, "這不是這次登入的回呼網址,請貼完整的網址。")
			}
		}()
	}
	var cb callback
	select {
	case cb = <-got:
	case <-ctx.Done():
		return errors.New("等不到授權完成(逾時),請重新執行登入;瀏覽器顯示「未知的 client_id」時加 --new-client")
	}
	if cb.err != nil {
		return cb.err
	}
	unlock, err := c.Store.Lock(c.Resource)
	if err != nil {
		return err
	}
	defer unlock()
	cred = Cred{Resource: c.Resource, TokenEndpoint: meta.Token, ClientID: clientID, RedirectURI: redirect}
	if err := c.exchange(ctx, &cred, url.Values{"grant_type": {"authorization_code"}, "code": {cb.code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}); err != nil {
		return err
	}
	if err := c.Store.Save(cred); err != nil {
		return fmt.Errorf("登入資料存不起來:%w", err)
	}
	fmt.Fprintf(o.Out, "登入完成(權限 %s),登入資料存在 %s\n", cred.Scope, c.Store.Path(c.Resource))
	return nil
}

// exchange 呼叫 token 端點,把新的 token 放進 cred。伺服器拒絕授權(invalid_grant、invalid_client)回 ErrLoginRequired
func (c *Client) exchange(ctx context.Context, cred *Cred, form url.Values) error {
	form.Set("client_id", cred.ClientID)
	form.Set("resource", cred.Resource)
	req, err := http.NewRequestWithContext(ctx, "POST", cred.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("換 token:%w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int    `json:"expires_in"`
		Scope   string `json:"scope"`
		Error   string `json:"error"`
		Desc    string `json:"error_description"`
	}
	_ = json.Unmarshal(b, &out) // 錯誤訊息只用 error 與 error_description,不帶回應原文
	if resp.StatusCode != 200 || out.Access == "" || out.Refresh == "" {
		if out.Error == "invalid_grant" || out.Error == "invalid_client" {
			return fmt.Errorf("%w(%s)", ErrLoginRequired, out.Desc)
		}
		return fmt.Errorf("換 token 失敗(HTTP %d):%s %s", resp.StatusCode, out.Error, out.Desc)
	}
	cred.AccessToken, cred.RefreshToken, cred.Scope = out.Access, out.Refresh, out.Scope
	cred.ExpiresAt = c.now().Add(time.Duration(out.Expires) * time.Second).UTC()
	return nil
}

// refreshMargin 到期前多久就先更新
const refreshMargin = time.Minute

// Token 目前可用的 access token(快到期就更新);沒有可用的登入回 ErrLoginRequired
func (c *Client) Token(ctx context.Context) (string, error) {
	return c.token(ctx, "")
}

// Renew 伺服器拒絕了 rejected 這個 access token:別的行程還沒換過的話就換一個
func (c *Client) Renew(ctx context.Context, rejected string) (string, error) {
	return c.token(ctx, rejected)
}

func (c *Client) usable(cred Cred, rejected string) bool {
	return cred.AccessToken != "" && cred.AccessToken != rejected && c.now().Add(refreshMargin).Before(cred.ExpiresAt)
}

func (c *Client) token(ctx context.Context, rejected string) (string, error) {
	if _, _, err := splitResource(c.Resource); err != nil {
		return "", err
	}
	cred, err := c.Store.Load(c.Resource)
	if err != nil {
		return "", err
	}
	if c.usable(cred, rejected) {
		return cred.AccessToken, nil
	}
	if cred.RefreshToken == "" {
		return "", ErrLoginRequired
	}
	unlock, err := c.Store.Lock(c.Resource)
	if err != nil {
		return "", err
	}
	defer unlock()
	if cred, err = c.Store.Load(c.Resource); err != nil { // 拿到鎖後重讀:別的行程可能剛換過
		return "", err
	}
	if c.usable(cred, rejected) {
		return cred.AccessToken, nil
	}
	if cred.RefreshToken == "" {
		return "", ErrLoginRequired
	}
	// token 端點一定要在這個 resource 的伺服器上(檔案被改過也不會把 refresh token 送去別處)
	public, _, _ := splitResource(c.Resource)
	if cred.TokenEndpoint != public+"/oauth/token" {
		return "", ErrLoginRequired
	}
	err = c.exchange(ctx, &cred, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.RefreshToken}})
	if errors.Is(err, ErrLoginRequired) {
		cred.AccessToken, cred.RefreshToken, cred.ExpiresAt = "", "", time.Time{}
		_ = c.Store.Save(cred)
		return "", err
	}
	if err != nil {
		return "", err // 連不到等暫時的問題:保留 refresh token,下次再試
	}
	if err := c.Store.Save(cred); err != nil {
		return "", fmt.Errorf("更新後的登入資料存不起來:%w", err)
	}
	return cred.AccessToken, nil
}

// Logout 清掉本機存的 token(用戶端註冊保留,下次登入沿用)。伺服器端的授權要請管理員撤銷或等它過期
func (c *Client) Logout() error {
	unlock, err := c.Store.Lock(c.Resource)
	if err != nil {
		return err
	}
	defer unlock()
	cred, err := c.Store.Load(c.Resource)
	if err != nil {
		return err
	}
	if cred.AccessToken == "" && cred.RefreshToken == "" {
		return ErrLoginRequired
	}
	cred.AccessToken, cred.RefreshToken, cred.Scope, cred.ExpiresAt = "", "", "", time.Time{}
	return c.Store.Save(cred)
}

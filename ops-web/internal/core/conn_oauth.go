package core

import (
	"crypto/rand"
	"crypto/sha256"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// OAuth 2.0 / OIDC 與瀏覽器核准(同 Swift 的 Connectors.swift)。
//
// 標準的授權碼流程 + PKCE(RFC 6749、7636、8252),只給網址也能用:
//  1. 探索:連線網址的 /.well-known/oauth-protected-resource(RFC 9728)找到授權伺服器,
//     再讀它的 /.well-known/openid-configuration 或 /.well-known/oauth-authorization-server(RFC 8414)
//  2. 沒有 client_id 但伺服器支援動態註冊(RFC 7591)時,自動註冊一個公開用戶端
//  3. 開瀏覽器授權,導回 127.0.0.1 的回呼,用 code + code_verifier 換 token
//  4. token 快過期時用 refresh_token 換新

// OpenBrowser 開瀏覽器的方式(依系統用 open / rundll32 / xdg-open);測試時換成直接打回呼網址。跑 go test 時預設什麼都不做。
var OpenBrowser = func(u string) { _ = OpenURLCommand(u).Start() }

// OpenURLCommand 用系統預設瀏覽器開網址的指令。
// Windows 用 rundll32 而不是 cmd /c start:網址裡的 & 不會被 cmd 拆開。
func OpenURLCommand(u string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		return exec.Command("xdg-open", u)
	}
}

func init() {
	if testing.Testing() {
		OpenBrowser = func(string) {}
	}
}

// clientLabel 註冊用戶端與核准頁顯示的名稱,同 Swift 的 "Ops(<電腦名稱>)"
func clientLabel() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "Mac"
	}
	return "omixor(" + strings.TrimSuffix(name, ".local") + ")"
}

// OAuthMetadata 授權伺服器的端點
type OAuthMetadata struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	Resource              string
}

// oauthMetadataURLs 依序嘗試的中繼資料網址(RFC 8414 的路徑插在 /.well-known/ 後面;OIDC 接在 issuer 後面)
func oauthMetadataURLs(issuer string) []string {
	iss := strings.TrimSuffix(issuer, "/")
	u, err := url.Parse(iss)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return []string{}
	}
	origin := u.Scheme + "://" + u.Host
	path := u.Path
	if path == "/" {
		path = ""
	}
	out := []string{iss + "/.well-known/openid-configuration"}
	if path != "" {
		out = append(out, origin+"/.well-known/oauth-authorization-server"+path, origin+"/.well-known/openid-configuration"+path)
	}
	out = append(out, origin+"/.well-known/oauth-authorization-server")
	seen := map[string]bool{}
	uniq := []string{}
	for _, s := range out {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	return uniq
}

func oauthDiscover(c Connection) (OAuthMetadata, error) {
	issuer := strings.Trim(c.OAuth.Issuer, " ")
	resource := c.OAuth.Resource
	if issuer == "" {
		basePath := ""
		if u, err := url.Parse(c.BaseURL); err == nil {
			basePath = u.Path
		}
		for _, u := range []string{c.Origin() + "/.well-known/oauth-protected-resource" + basePath, c.Origin() + "/.well-known/oauth-protected-resource"} {
			r, err := connSend(&c, "GET", u, nil, nil)
			if err != nil || !r.OK() {
				continue
			}
			d, ok := r.JSON().(map[string]any)
			if !ok {
				continue
			}
			if servers, ok := d["authorization_servers"].([]any); ok && len(servers) > 0 {
				if s, ok := servers[0].(string); ok {
					issuer = s
				}
			}
			if res, ok := d["resource"].(string); ok && resource == "" {
				resource = res
			}
			break
		}
		if issuer == "" {
			issuer = c.Origin()
		}
	}
	for _, u := range oauthMetadataURLs(issuer) {
		r, err := connSend(&c, "GET", u, nil, nil)
		if err != nil || !r.OK() {
			continue
		}
		d, ok := r.JSON().(map[string]any)
		if !ok {
			continue
		}
		auth, ok1 := d["authorization_endpoint"].(string)
		token, ok2 := d["token_endpoint"].(string)
		if !ok1 || !ok2 {
			continue
		}
		m := OAuthMetadata{Issuer: issuer, AuthorizationEndpoint: auth, TokenEndpoint: token, Resource: resource}
		if s, ok := d["issuer"].(string); ok {
			m.Issuer = s
		}
		if s, ok := d["registration_endpoint"].(string); ok {
			m.RegistrationEndpoint = s
		}
		return m, nil
	}
	return OAuthMetadata{}, connErr("在 " + issuer + " 找不到 OAuth 設定(/.well-known/openid-configuration 或 oauth-authorization-server)。請手動填授權與 Token 端點")
}

// oauthEndpoints 已填的端點優先,缺的再探索
func oauthEndpoints(c Connection) (OAuthMetadata, error) {
	o := c.OAuth
	if o.AuthorizationEndpoint != "" && o.TokenEndpoint != "" {
		return OAuthMetadata{Issuer: o.Issuer, AuthorizationEndpoint: o.AuthorizationEndpoint, TokenEndpoint: o.TokenEndpoint,
			RegistrationEndpoint: o.RegistrationEndpoint, Resource: o.Resource}, nil
	}
	m, err := oauthDiscover(c)
	if err != nil {
		return m, err
	}
	if o.RegistrationEndpoint != "" {
		m.RegistrationEndpoint = o.RegistrationEndpoint
	}
	return m, nil
}

// MARK: PKCE

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64url(b)
}

// pkceChallenge S256
func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64url(h[:])
}

func queryEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

func oauthAuthorizationURL(m OAuthMetadata, clientID, redirect, scopes, state, challenge, extra string) (string, error) {
	u, err := url.Parse(m.AuthorizationEndpoint)
	if err != nil || m.AuthorizationEndpoint == "" {
		return "", connErr("授權網址格式錯誤:" + m.AuthorizationEndpoint)
	}
	type kv struct{ k, v string }
	items := []kv{{"response_type", "code"}, {"client_id", clientID}, {"redirect_uri", redirect}, {"state", state},
		{"code_challenge", challenge}, {"code_challenge_method", "S256"}}
	scope := strings.Join(strings.FieldsFunc(scopes, func(r rune) bool { return r == ' ' || r == ',' }), " ")
	if scope != "" {
		items = append(items, kv{"scope", scope})
	}
	if m.Resource != "" {
		items = append(items, kv{"resource", m.Resource})
	}
	for _, pair := range strings.Split(extra, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		if dv, err := url.PathUnescape(v); err == nil {
			v = dv
		}
		items = append(items, kv{k, v})
	}
	parts := []string{}
	if u.RawQuery != "" {
		parts = append(parts, u.RawQuery)
	}
	for _, it := range items {
		parts = append(parts, queryEscape(it.k)+"="+queryEscape(it.v))
	}
	u.RawQuery = strings.Join(parts, "&")
	return u.String(), nil
}

func optString(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// oauthParseTokens token 回應 → TokenSet
func oauthParseTokens(obj any, previous *TokenSet) (*TokenSet, error) {
	d, isMap := obj.(map[string]any)
	// GitHub 換 token 失敗時回 200 加上 error
	if isMap && d["access_token"] == nil {
		if e, ok := d["error"].(string); ok {
			if desc, ok := d["error_description"].(string); ok {
				return nil, connErr(desc)
			}
			return nil, connErr(e)
		}
	}
	access, ok := d["access_token"].(string)
	if !isMap || !ok {
		return nil, connErr("Token 回應裡沒有 access_token")
	}
	t := &TokenSet{AccessToken: access, TokenType: optString(d["token_type"])}
	t.RefreshToken = optString(d["refresh_token"])
	t.IDToken = optString(d["id_token"])
	if previous != nil {
		if t.RefreshToken == nil {
			t.RefreshToken = previous.RefreshToken
		}
		if t.IDToken == nil {
			t.IDToken = previous.IDToken
		}
	}
	var expires *float64
	switch x := d["expires_in"].(type) {
	case float64:
		expires = &x
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			expires = &f
		}
	}
	if expires != nil {
		t.ExpiresAt = &Time{time.Now().Add(time.Duration(*expires * float64(time.Second)))}
	}
	return t, nil
}

// oauthLogin 回傳 token,以及要寫回連線設定的變動(探索到的端點、動態註冊拿到的 client_id)
func oauthLogin(c Connection, scope string) (*TokenSet, OAuthSettings, error) {
	settings := c.OAuth
	m, err := oauthEndpoints(c)
	if err != nil {
		return nil, settings, err
	}
	// 探索到的結果寫回設定:之後換新 token 不用再探索,resource 也不會遺失
	if settings.Issuer == "" {
		settings.Issuer = m.Issuer
	}
	settings.AuthorizationEndpoint = m.AuthorizationEndpoint
	settings.TokenEndpoint = m.TokenEndpoint
	if settings.RegistrationEndpoint == "" {
		settings.RegistrationEndpoint = m.RegistrationEndpoint
	}
	if settings.Resource == "" {
		settings.Resource = m.Resource
	}
	cb, err := startLoopback(settings.RegisteredPort)
	if err != nil {
		return nil, settings, err
	}
	defer cb.stop()

	clientID := settings.ClientID
	if clientID == "" || (settings.RegisteredPort != nil && *settings.RegisteredPort != cb.Port) {
		if m.RegistrationEndpoint == "" {
			return nil, settings, connErr("這個伺服器不支援自動註冊,請在連線設定填 client_id(在服務的開發者主控台建立「桌面/原生」類型的 OAuth 用戶端)")
		}
		if _, err := url.Parse(m.RegistrationEndpoint); err != nil {
			return nil, settings, connErr("這個伺服器不支援自動註冊,請在連線設定填 client_id(在服務的開發者主控台建立「桌面/原生」類型的 OAuth 用戶端)")
		}
		r, err := connCheck(connJSON(&c, "POST", m.RegistrationEndpoint, map[string]any{
			"client_name":                clientLabel(),
			"redirect_uris":              []string{cb.RedirectURI()},
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
			"token_endpoint_auth_method": "none",
		}, nil))
		if err != nil {
			return nil, settings, err
		}
		d, _ := r.JSON().(map[string]any)
		id, ok := d["client_id"].(string)
		if !ok {
			return nil, settings, connErr("自動註冊沒有回傳 client_id")
		}
		clientID = id
		settings.ClientID = id
		port := cb.Port
		settings.RegisteredPort = &port
	}

	verifier := randomString(48)
	state := randomString(16)
	cb.expect("state", state)
	authURL, err := oauthAuthorizationURL(m, clientID, cb.RedirectURI(), settings.Scopes, state, pkceChallenge(verifier), settings.ExtraParams)
	if err != nil {
		return nil, settings, err
	}
	OpenBrowser(authURL)
	code, err := cb.waitForCode(LoginTimeout)
	if err != nil {
		return nil, settings, err
	}
	if _, err := url.Parse(m.TokenEndpoint); err != nil || m.TokenEndpoint == "" {
		return nil, settings, connErr("Token 端點格式錯誤")
	}
	fields := map[string]string{"grant_type": "authorization_code", "code": code, "redirect_uri": cb.RedirectURI(),
		"client_id": clientID, "code_verifier": verifier}
	if s, ok := vaultSecret(scope, c.ID); ok && s != "" {
		fields["client_secret"] = s
	}
	if m.Resource != "" {
		fields["resource"] = m.Resource
	}
	r, err := oauthTokenCheck(connForm(&c, m.TokenEndpoint, fields))
	if err != nil {
		return nil, settings, err
	}
	t, err := oauthParseTokens(r.JSON(), nil)
	return t, settings, err
}

// oauthTokenCheck 用授權碼換 token 的回應:失敗時保留伺服器的 error / error_description
// (connCheck 會把 401 一律改成「登入已失效」,換 token 時那通常是 invalid_client,會誤導)
func oauthTokenCheck(r *ConnResponse, err error) (*ConnResponse, error) {
	if err != nil {
		return nil, err
	}
	if r.OK() {
		return r, nil
	}
	o, _ := r.JSON().(map[string]any)
	code, _ := o["error"].(string)
	msg := "換 token 失敗(HTTP " + strconv.Itoa(r.Status) + "):"
	if desc, _ := o["error_description"].(string); code != "" && desc != "" {
		msg += code + ":" + desc
	} else {
		msg += connErrorMessage(r)
	}
	switch code {
	case "invalid_client":
		msg += "。用戶端驗證失敗:OAuth 應用程式如果是 Confidential(機密),要把 Secret 填進連線設定;或確認 client_id 正確"
	case "invalid_grant":
		msg += "。授權碼無效或回呼網址不符:請重新登入;一直失敗的話確認應用程式的回呼網址"
	}
	return nil, ConnError{Status: r.Status, Message: msg}
}

func oauthRefresh(c Connection, scope string, t *TokenSet) (*TokenSet, error) {
	if t.RefreshToken == nil {
		return nil, ConnError{Status: 401, Message: "登入已過期,請重新登入"}
	}
	m, err := oauthEndpoints(c)
	if err != nil {
		return nil, err
	}
	if _, err := url.Parse(m.TokenEndpoint); err != nil || m.TokenEndpoint == "" {
		return nil, connErr("Token 端點格式錯誤")
	}
	fields := map[string]string{"grant_type": "refresh_token", "refresh_token": *t.RefreshToken, "client_id": c.OAuth.ClientID}
	if s, ok := vaultSecret(scope, c.ID); ok && s != "" {
		fields["client_secret"] = s
	}
	if m.Resource != "" {
		fields["resource"] = m.Resource
	}
	r, err := connForm(&c, m.TokenEndpoint, fields)
	if err != nil {
		return nil, err
	}
	if !r.OK() {
		return nil, ConnError{Status: 401, Message: "登入已過期,請重新登入(" + connErrorMessage(r) + ")"}
	}
	return oauthParseTokens(r.JSON(), t)
}

// MARK: - 瀏覽器核准(evoleap CLI 的登入方式)
// POST start 登記回呼網址 → 瀏覽器開核准頁 → 回呼帶 id + code → POST exchange 換 token。

func approvalLogin(c Connection) (*TokenSet, error) {
	a := c.Approval
	cb, err := startLoopback(nil)
	if err != nil {
		return nil, err
	}
	defer cb.stop()
	start := c.TrimmedBase() + a.StartPath
	if _, err := url.Parse(start); err != nil {
		return nil, connErr("網址格式錯誤")
	}
	r, err := connCheck(connJSON(&c, "POST", start, map[string]any{"redirect_uri": cb.RedirectURI(), "name": clientLabel()}, nil))
	if err != nil {
		return nil, err
	}
	d, _ := r.JSON().(map[string]any)
	id, ok := d["id"].(string)
	if !ok {
		return nil, connErr("伺服器沒有回傳登入 id")
	}
	cb.expect("id", id)
	console := c.TrimmedBase()
	if a.ConsoleURL != "" {
		console = strings.TrimSuffix(a.ConsoleURL, "/")
	}
	approve := strings.ReplaceAll(a.ApprovePath, "{id}", percentEncode(id, "-._~!$&'()*+,;=:@/?"))
	approveURL := console + approve
	if _, err := url.Parse(approveURL); err != nil {
		return nil, connErr("核准頁網址格式錯誤")
	}
	OpenBrowser(approveURL)
	code, err := cb.waitForCode(LoginTimeout)
	if err != nil {
		return nil, err
	}
	ex := c.TrimmedBase() + a.ExchangePath
	if _, err := url.Parse(ex); err != nil {
		return nil, connErr("網址格式錯誤")
	}
	tr, err := connCheck(connJSON(&c, "POST", ex, map[string]any{"id": id, "code": code}, nil))
	if err != nil {
		return nil, err
	}
	td, _ := tr.JSON().(map[string]any)
	token, ok := td["token"].(string)
	if !ok {
		return nil, connErr("換不到 token")
	}
	return &TokenSet{AccessToken: token, TokenType: strPtr("Bearer")}, nil
}

func approvalLogout(c Connection, t *TokenSet) {
	if c.Approval.LogoutPath == "" {
		return
	}
	u := c.TrimmedBase() + c.Approval.LogoutPath
	if _, err := url.Parse(u); err != nil {
		return
	}
	_, _ = connSend(&c, "POST", u, map[string]string{"Authorization": "Bearer " + t.AccessToken}, nil)
}

// EvoleapLogin 用 evoleap 的瀏覽器核准登入拿一個 evoleap CLI token(給 omixor CLI 換 omixor 自己的 token 用)。
// base 是 evoleap 網址;consoleURL 是核准頁的網址(本機開發時前端和 API 不同埠,其他時候給 "")
func EvoleapLogin(base, consoleURL string) (string, error) {
	c := DefaultConnections()[0]
	c.BaseURL = strings.TrimRight(base, "/")
	c.Approval.ConsoleURL = consoleURL
	t, err := approvalLogin(c)
	if err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

// EvoleapLogout 撤銷 evoleap CLI token(換完 omixor token 就不留著)
func EvoleapLogout(base, token string) {
	c := DefaultConnections()[0]
	c.BaseURL = strings.TrimRight(base, "/")
	approvalLogout(c, &TokenSet{AccessToken: token})
}

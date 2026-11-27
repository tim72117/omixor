package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/config"
	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"

	"omixor-web/internal/core"
)

// 對外 MCP 連接埠的四種端點與認證(和 omixor server 同一份掛法);環境見 oauth_test.go 的 oauthRig

const listRPC = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

var remotePaths = []string{"/mcp", "/mcp/org/o", "/mcp/agent", "/mcp/worker/o"}

func (r *oauthRig) post(path, bearer, body string) *httptest.ResponseRecorder {
	hdr := []string{"Content-Type", "application/json"}
	if bearer != "" {
		hdr = append(hdr, "Authorization", "Bearer "+bearer)
	}
	return r.do("POST", path, body, hdr...)
}

func (r *oauthRig) userToken(userID, raw string) string {
	r.t.Helper()
	if err := r.svc.Store.CreateToken(model.Token{ID: "t_" + raw, UserID: userID, Device: "x", CreatedAt: model.Now(), Hash: service.HashToken(raw)}); err != nil {
		r.t.Fatal(err)
	}
	return raw
}

// tool 呼叫 path 上的工具,回傳是不是錯誤與第一段文字內容
func (r *oauthRig) tool(path, bearer, name string, args map[string]any) (bool, string) {
	r.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	w := r.post(path, bearer, string(body))
	if w.Code != 200 {
		r.t.Fatal(path, name, w.Code, w.Body.String())
	}
	res, ok := r.json(w)["result"].(map[string]any)
	if !ok {
		return true, w.Body.String()
	}
	isErr, _ := res["isError"].(bool)
	text := ""
	if c, ok := res["content"].([]any); ok && len(c) > 0 {
		text, _ = c[0].(map[string]any)["text"].(string)
	}
	return isErr, text
}

func (r *oauthRig) namesAt(path, bearer string) map[string]bool {
	r.t.Helper()
	w := r.post(path, bearer, listRPC)
	if w.Code != 200 {
		r.t.Fatal(path, w.Code, w.Body.String())
	}
	out := map[string]bool{}
	for _, d := range r.json(w)["result"].(map[string]any)["tools"].([]any) {
		out[d.(map[string]any)["name"].(string)] = true
	}
	return out
}

// credentialIn 從領取任務的結果取出角色憑證
func credentialIn(t *testing.T, text string) (bearer, endpoint string) {
	t.Helper()
	var out struct {
		Credential struct{ AgentID, Secret, Endpoint string }
	}
	i := strings.Index(text, "{")
	if i < 0 || json.NewDecoder(strings.NewReader(text[i:])).Decode(&out) != nil || out.Credential.Secret == "" {
		t.Fatal("沒有角色憑證:", text)
	}
	return out.Credential.AgentID + ":" + out.Credential.Secret, out.Credential.Endpoint
}

// omx_ 使用者 token:三種使用者端點可用;領任務拿到的角色端點網址是對外網址,角色憑證能連 /mcp/agent;console 的工具四種端點都沒有
func TestRemotePortFourEndpoints(t *testing.T) {
	r := newOAuthRig(t)
	if err := r.svc.Store.SaveMember(model.Member{Org: "o", UserID: "u_boss", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	tok := r.userToken("u_boss", "omx_boss")
	must := func(path, name string, args map[string]any) string {
		t.Helper()
		isErr, text := r.tool(path, tok, name, args)
		if isErr {
			t.Fatal(path, name, text)
		}
		return text
	}
	must("/mcp/org/o", "agent_upsert", map[string]any{"id": "dev", "name": "Dev"})
	must("/mcp/org/o", "agent_task_create", map[string]any{"assign_to": "dev", "title": "first"})
	must("/mcp", "agent_task_create", map[string]any{"org": "o", "assign_to": "dev", "title": "second"})

	cred, ep := credentialIn(t, must("/mcp/org/o", "agent_task_claim", map[string]any{"agent": "dev"}))
	if ep != oPublic+"/mcp/agent" {
		t.Fatal("組織端點給的角色端點網址:", ep)
	}
	_, ep = credentialIn(t, must("/mcp/worker/o", "dispatch_claim", map[string]any{"agent": "dev"}))
	if ep != oPublic+"/mcp/agent" {
		t.Fatal("執行者端點給的角色端點網址:", ep)
	}

	lists := map[string]map[string]bool{
		"/mcp": r.namesAt("/mcp", tok), "/mcp/org/o": r.namesAt("/mcp/org/o", tok),
		"/mcp/agent": r.namesAt("/mcp/agent", cred), "/mcp/worker/o": r.namesAt("/mcp/worker/o", tok),
	}
	if !lists["/mcp"]["issue_create"] || !lists["/mcp/org/o"]["agent_task_claim"] || !lists["/mcp/agent"]["agent_context"] || !lists["/mcp/worker/o"]["dispatch_claim"] {
		t.Fatal(lists)
	}
	for n := range lists["/mcp/worker/o"] {
		if !coremcp.WorkerToolAllow[n] {
			t.Errorf("執行者端點不該有 %s", n)
		}
	}
	local := 0
	for _, name := range ToolNames() {
		if isCoreToolName(name) {
			continue
		}
		local++
		for path, l := range lists {
			if l[name] {
				t.Errorf("%s 不該有 console 的工具 %s", path, name)
			}
		}
	}
	if local == 0 {
		t.Fatal("沒有找到 console 自己的工具,測試失效")
	}
	r.app.Mu.Lock()
	reqs := r.app.MCP.Requests
	r.app.Mu.Unlock()
	if reqs == 0 {
		t.Fatal("請求要計入 MCP 狀態")
	}
	// 角色端點只收角色憑證;角色憑證不能用在使用者端點
	if w := r.post("/mcp/agent", tok, listRPC); w.Code != 401 {
		t.Errorf("角色端點不收 omx_:%d", w.Code)
	}
	for _, p := range []string{"/mcp", "/mcp/org/o", "/mcp/worker/o"} {
		if w := r.post(p, cred, listRPC); w.Code != 401 {
			t.Errorf("%s 不收角色憑證:%d", p, w.Code)
		}
	}
}

// 組織端點的 OAuth:token 綁定那個組織的網址;唯讀授權不能寫;執行者端點不收一般授權的 omo_
func TestRemotePortOrgBoundOAuth(t *testing.T) {
	r := newOAuthRig(t)
	ro := r.connectTo("/mcp/org/o", "ev-boss", "boss@x", "mcp:read", "read")
	for n := range r.namesAt("/mcp/org/o", ro) {
		if !coremcp.ReadOnlyTools[n] {
			t.Errorf("唯讀授權不該有 %s", n)
		}
	}
	if isErr, text := r.tool("/mcp/org/o", ro, "issue_create", map[string]any{"title": "no"}); !isErr || !strings.Contains(text, coremcp.ReadOnlyMessage) {
		t.Fatal("唯讀授權要被拒:", text)
	}
	for _, p := range []string{"/mcp", "/mcp/org/p"} {
		if w := r.post(p, ro, listRPC); w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
			t.Errorf("%s:綁別的資源的 token 要 401 invalid_token,實際 %d", p, w.Code)
		}
	}
	unified := r.connect("ev-boss", "boss@x", "mcp:read", "read")
	if w := r.post("/mcp/org/o", unified, listRPC); w.Code != 401 {
		t.Error("統一入口的 token 不能用在組織端點:", w.Code)
	}
	if w := r.post("/mcp/worker/o", ro, listRPC); w.Code != 401 {
		t.Error("執行者端點不收一般授權的 omo_:", w.Code)
	}
	rw := r.connectTo("/mcp/org/o", "ev-boss", "boss@x", "mcp:read mcp:write", "write")
	if isErr, text := r.tool("/mcp/org/o", rw, "issue_create", map[string]any{"title": "ok"}); isErr {
		t.Fatal(text)
	}
	if r.namesAt("/mcp/org/o", rw)["agent_task_claim"] {
		t.Error("OAuth 讀寫授權不能領任務")
	}
}

// 本機使用者 omixor-console 不能從對外連接埠進來:它的 omx_ token、或屬於它的 omo_,都和無效 token 一樣
func TestRemotePortRejectsLocalUser(t *testing.T) {
	r := newOAuthRig(t)
	local := model.User{ID: "u_local", EvoleapID: "local:" + core.LocalIdentity, Email: core.LocalIdentity, Name: core.LocalIdentity, Role: "admin", CreatedAt: model.Now()}
	if err := r.svc.Store.SaveUser(local); err != nil {
		t.Fatal(err)
	}
	tok := r.userToken("u_local", "omx_local")
	if _, err := r.svc.Authenticate(tok); err != nil {
		t.Fatal("token 本身要有效,測試才有意義:", err)
	}
	for _, p := range []string{"/mcp", "/mcp/org/o", "/mcp/worker/o"} {
		got, bad := r.post(p, tok, listRPC), r.post(p, "omx_nope", listRPC)
		if got.Code != 401 || got.Code != bad.Code || got.Body.String() != bad.Body.String() || got.Header().Get("WWW-Authenticate") != bad.Header().Get("WWW-Authenticate") {
			t.Errorf("%s:本機使用者要和無效 token 一樣 401:%d / %d", p, got.Code, bad.Code)
		}
	}
}

// 屬於本機使用者的 omo_(正常流程簽不出來;這裡先簽給 boss,再把那一列改成本機使用者)一樣被擋
func TestRemotePortRejectsLocalUserOAuth(t *testing.T) {
	r := newOAuthRig(t)
	tokens := map[string]string{}
	for _, p := range []string{"/mcp", "/mcp/org/o"} {
		tokens[p] = r.connectTo(p, "ev-boss", "boss@x", "mcp:read", "read")
		if w := r.post(p, tokens[p], listRPC); w.Code != 200 {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	u, err := r.svc.Store.UserByEvoleapID("ev-boss")
	if err != nil {
		t.Fatal(err)
	}
	u.EvoleapID = "local:" + core.LocalIdentity
	if err := r.svc.Store.SaveUser(*u); err != nil {
		t.Fatal(err)
	}
	for p, access := range tokens {
		if w := r.post(p, access, listRPC); w.Code != 401 {
			t.Errorf("%s:本機使用者的 omo_ 要 401,實際 %d", p, w.Code)
		}
	}
}

// GitLab 登入:沒設不啟用;設了同意畫面多一顆按鈕(仍沒有配對碼)、有回呼路徑;設定不完整或要求併 LDAP 就拒絕啟動
func TestRemotePortGitLab(t *testing.T) {
	env := map[string]string{"OMIXOR_OAUTH_ADDR": "127.0.0.1:0", "OMIXOR_PUBLIC_URL": oPublic}
	load := func() *config.Config { return config.LoadFrom(func(k string) string { return env[k] }, config.Web) }
	if g, err := consoleGitLab(load()); err != nil || g != nil {
		t.Fatal("沒設不啟用:", g, err)
	}
	env["OMIXOR_GITLAB_URL"] = "https://gitlab.test"
	if _, err := consoleGitLab(load()); err == nil {
		t.Fatal("設定不完整要拒絕")
	}
	env["OMIXOR_GITLAB_CLIENT_ID"], env["OMIXOR_GITLAB_CLIENT_SECRET"] = "cid", "sekret-value"
	g, err := consoleGitLab(load())
	if err != nil || g == nil {
		t.Fatal(err)
	}
	env["OMIXOR_GITLAB_LINK_LDAP"] = "true"
	if _, err := consoleGitLab(load()); err == nil || !strings.Contains(err.Error(), "OMIXOR_GITLAB_LINK_LDAP") {
		t.Fatal("console 不支援併 LDAP:", err)
	}

	r := newOAuthRig(t)
	r.svc.GitLab = g
	w := r.do("POST", "/oauth/register", `{"client_name":"Claude","redirect_uris":["`+oCallback+`"],"token_endpoint_auth_method":"none"}`, "Content-Type", "application/json")
	client, _ := r.json(w)["client_id"].(string)
	page := r.do("GET", "/oauth/authorize?response_type=code&code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&code_challenge_method=S256&client_id="+client+
		"&redirect_uri="+oCallback+"&resource="+oPublic+"/mcp", "")
	b := page.Body.String()
	if page.Code != 200 || !strings.Contains(b, "用 GitLab 登入並允許") || !strings.Contains(b, "用 evoleap 登入並允許") || strings.Contains(b, "pairing_code") {
		t.Fatal(page.Code, b)
	}
	if w := r.do("GET", "/oauth/gitlab/callback?state=x&code=y", ""); w.Code == 404 {
		t.Fatal("要有 GitLab 回呼路徑")
	}
}

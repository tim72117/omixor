package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	coremcp "omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/mcpauth"
)

// 本機入口 + omixor-mcp 的登入:真的走一遍授權碼 + PKCE(同意畫面用假 evoleap)、本機回呼、換 token、更新;
// 執行者授權和一般授權互相不能用,REST 都不收,dispatch_claim 不回角色憑證密鑰,web session 不能當 MCP token

type liveRig struct {
	*oauthRig
	url   string
	store mcpauth.Store
}

func newLiveRig(t *testing.T) *liveRig {
	r := newOAuthRig(t)
	srv := httptest.NewUnstartedServer(nil)
	public := "http://" + srv.Listener.Addr().String()
	cfg := oauthConfig(t, public)
	srv.Config.Handler = newOAuthHandler(r.app, r.svc, cfg)
	srv.Start()
	t.Cleanup(srv.Close)
	return &liveRig{oauthRig: r, url: public, store: mcpauth.Store{Dir: t.TempDir()}}
}

// browser 像使用者一樣:開授權網址、按 evoleap 登入並允許、跟到本機回呼。grant 只在一般授權有作用
func (r *liveRig) browser(evoID, email, grant string) func(string) error {
	return func(authURL string) error {
		r.evo.loginAs(evoID, email)
		jar, _ := cookiejar.New(nil)
		cl := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := cl.Get(authURL)
		if err != nil {
			return err
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		m := oCSRF.FindStringSubmatch(string(page))
		if m == nil {
			return errors.New("沒有同意畫面:" + string(page))
		}
		u, _ := url.Parse(authURL)
		form := u.Query()
		form.Set("csrf", m[1])
		form.Set("via", "evoleap")
		form.Set("grant", grant)
		resp, err = cl.PostForm(r.url+"/oauth/authorize", form)
		if err != nil {
			return err
		}
		resp.Body.Close()
		login, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || resp.StatusCode != 303 {
			return errors.New("同意畫面沒有導去登入")
		}
		resp, err = cl.Get(r.url + "/oauth/evoleap/callback?" + url.Values{"id": {login.Query().Get("id")}, "code": {"c"}}.Encode())
		if err != nil {
			return err
		}
		resp.Body.Close()
		back := resp.Header.Get("Location")
		if back == "" {
			return errors.New("回呼沒有導回")
		}
		resp, err = http.Get(back)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
}

func (r *liveRig) login(path, grant string) (*mcpauth.Client, string, error) {
	c := &mcpauth.Client{Store: r.store, Resource: r.url + path}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out strings.Builder
	if err := c.Login(ctx, mcpauth.LoginOptions{Open: r.browser("ev-boss", "boss@x", grant), Out: &out, Timeout: 15 * time.Second}); err != nil {
		return c, "", err
	}
	tok, err := c.Token(ctx)
	if err == nil && strings.Contains(out.String(), tok) {
		err = errors.New("說明文字不能含 token")
	}
	return c, tok, err
}

func (r *liveRig) call(path, tok, method string, params any) (int, http.Header, map[string]any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest("POST", r.url+path, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, resp.Header, out
}

func toolText(out map[string]any) (string, bool) {
	res, _ := out["result"].(map[string]any)
	c, _ := res["content"].([]any)
	if len(c) == 0 {
		b, _ := json.Marshal(out)
		return string(b), true
	}
	text, _ := c[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func TestWorkerOAuthEndToEnd(t *testing.T) {
	r := newLiveRig(t)
	admin := &model.Principal{User: model.User{ID: "u_admin", Email: "admin@x", Role: "admin"}}
	if err := r.svc.Store.SaveUser(model.User{ID: "u_admin", Email: "admin@x", Role: "admin", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.SaveAgent(admin, "o", model.Agent{ID: "dev", Name: "工程師", Role: "開發", Instructions: "做事", Active: true}); err != nil {
		t.Fatal(err)
	}
	task, err := r.svc.AssignTask(admin, "o", service.TaskInput{AgentID: "dev", Title: "做一件事"})
	if err != nil {
		t.Fatal(err)
	}

	// 沒帶 token:401 帶執行者端點自己的 resource_metadata
	code, hdr, _ := r.call("/mcp/worker/o", "", "tools/list", map[string]any{})
	if code != 401 || !strings.Contains(hdr.Get("WWW-Authenticate"), `resource_metadata="`+r.url+`/.well-known/oauth-protected-resource/mcp/worker/o"`) {
		t.Fatal(code, hdr.Get("WWW-Authenticate"))
	}

	// 不是組織 p 的成員:拿不到執行者授權
	if _, _, err := r.login("/mcp/worker/p", ""); err == nil {
		t.Fatal("非成員不能授權")
	}
	wc, worker, err := r.login("/mcp/worker/o", "admin") // 表單送什麼等級都一樣是執行者授權
	if err != nil {
		t.Fatal(err)
	}
	if cred, _ := r.store.Load(r.url + "/mcp/worker/o"); cred.Scope != model.OAuthScopeWorker {
		t.Fatalf("scope %q", cred.Scope)
	}
	_, normal, err := r.login("/mcp", "write")
	if err != nil {
		t.Fatal(err)
	}

	// 互相不能用;REST(Authenticate)一律不收
	for _, c := range []struct{ path, tok string }{{"/mcp", worker}, {"/mcp/org/o", worker}, {"/mcp/worker/p", worker}, {"/mcp/worker/o", normal}, {"/mcp/agent", worker}} {
		if code, _, _ := r.call(c.path, c.tok, "tools/list", map[string]any{}); code != 401 {
			t.Errorf("%s 要 401,實際 %d", c.path, code)
		}
	}
	for _, tok := range []string{worker, normal} {
		if _, err := r.svc.Authenticate(tok); err == nil {
			t.Error("REST 不收 omo_")
		}
	}
	if _, err := r.svc.AuthenticateOAuth(worker, r.url+"/mcp/worker/o"); err == nil {
		t.Error("一般授權的驗證不收執行者 token(就算 resource 相同)")
	}

	// 只有 dispatch_ 工具;領取不回密鑰
	_, _, out := r.call("/mcp/worker/o", worker, "tools/list", map[string]any{})
	for _, d := range out["result"].(map[string]any)["tools"].([]any) {
		if n := d.(map[string]any)["name"].(string); !coremcp.WorkerToolAllow[n] {
			t.Errorf("執行者端點不該有 %s", n)
		}
	}
	src := &coremcp.WorkerTasks{URL: r.url + "/mcp/worker/o", Token: worker}
	list, err := src.ListAgentTasks("o", service.TaskFilter{Status: "queued"})
	if err != nil || len(list) != 1 || list[0].ID != task.ID || list[0].UpdatedAt.IsZero() {
		t.Fatalf("頻道來源:%+v %v", list, err)
	}
	_, _, out = r.call("/mcp/worker/o", worker, "tools/call", map[string]any{"name": "dispatch_claim", "arguments": map[string]any{"agent": "dev", "id": task.ID}})
	text, isErr := toolText(out)
	var claim struct {
		Credential map[string]any `json:"credential"`
		NextSteps  string         `json:"nextSteps"`
	}
	if isErr || json.Unmarshal([]byte(text), &claim) != nil {
		t.Fatal(text)
	}
	if _, has := claim.Credential["secret"]; has || claim.Credential["endpoint"] != nil || claim.Credential["code"] == "" || strings.Contains(claim.NextSteps, "/mcp/agent") {
		t.Fatalf("不能回密鑰:%s", text)
	}
	_, _, out = r.call("/mcp/worker/o", worker, "tools/call", map[string]any{"name": "dispatch_report",
		"arguments": map[string]any{"id": task.ID, "code": claim.Credential["code"], "status": "done", "result": "好了"}})
	if text, isErr := toolText(out); isErr {
		t.Fatal(text)
	}

	// 一般授權照舊不能領任務
	_, _, out = r.call("/mcp", normal, "tools/call", map[string]any{"name": "agent_task_claim", "arguments": map[string]any{"org": "o", "agent": "dev"}})
	if text, isErr := toolText(out); !isErr {
		t.Fatal("一般 OAuth 授權不能領任務:", text)
	}

	// 更新:被拒的 token 換新的,scope 不變;舊的 refresh 只用一次
	renewed, err := wc.Renew(context.Background(), worker)
	if err != nil || renewed == worker {
		t.Fatal(err)
	}
	if cred, _ := r.store.Load(r.url + "/mcp/worker/o"); cred.Scope != model.OAuthScopeWorker {
		t.Fatalf("更新後 scope %q", cred.Scope)
	}
	if code, _, _ := r.call("/mcp/worker/o", renewed, "tools/list", map[string]any{}); code != 200 {
		t.Fatal(code)
	}

	// web session(也是 omx_)在 MCP 端點不收;一般的 omx_ 照舊
	web, err := r.svc.IssueWebSession("u_admin", "admin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	api := r.userToken("u_admin", "omx_api")
	for _, p := range []string{"/mcp", "/mcp/org/o", "/mcp/worker/o"} {
		if code, _, _ := r.call(p, web.Token, "tools/list", map[string]any{}); code != 401 {
			t.Errorf("%s:web session 要 401,實際 %d", p, code)
		}
		if code, _, _ := r.call(p, api, "tools/list", map[string]any{}); code != 200 {
			t.Errorf("%s:omx_ 照舊,實際 %d", p, code)
		}
	}

	// 登出後要重新登入
	if err := wc.Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := wc.Token(context.Background()); !errors.Is(err, mcpauth.ErrLoginRequired) {
		t.Fatal(err)
	}
}

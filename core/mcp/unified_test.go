package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// 統一入口(/mcp):每次呼叫用 org 參數指定組織(必填、沒有預設)、每次驗證成員資格、結果附實際用的組織;舊端點不變。

type uni struct {
	svc          *service.Service
	boss         *model.Principal // lab、lab2 的成員
	unified, old http.Handler     // boss 的統一入口、boss 連 /mcp/org/lab 的舊端點
}

func newUni(t *testing.T) *uni {
	t.Helper()
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.SaveUser(model.User{ID: "u2", Email: "eve@x", Role: "member", CreatedAt: now})
	for _, o := range []model.Org{{ID: "lab", Name: "實驗室"}, {ID: "lab2", Name: "第二實驗室"}, {ID: "other", Name: "別人的組織"}} {
		o.CreatedAt = now
		_ = st.CreateOrg(o)
	}
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab2", UserID: "u1", Email: "boss@x", Role: "member", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "other", UserID: "u2", Email: "eve@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	return &uni{svc: svc, boss: boss,
		unified: mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{Resolve: func(*http.Request) (backend.Backend, error) {
			return &backend.Local{Svc: svc, P: boss}, nil
		}}),
		old: mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: boss}, "lab", nil
		}})}
}

func rpcPost(h http.Handler, body any, auth string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func rpcReq(id int, tool string, args map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}}
}

// result 一個 tools/call 的結果:是否錯誤、全部 text 區塊
type result struct {
	isErr bool
	texts []string
	meta  map[string]any
}

func parseResult(t *testing.T, m map[string]any) result {
	t.Helper()
	r, _ := m["result"].(map[string]any)
	if r == nil {
		t.Fatalf("沒有 result:%v", m)
	}
	var out result
	out.isErr, _ = r["isError"].(bool)
	out.meta, _ = r["_meta"].(map[string]any)
	for _, c := range r["content"].([]any) {
		out.texts = append(out.texts, c.(map[string]any)["text"].(string))
	}
	return out
}

func (r result) obj(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.texts[0]), &m); err != nil {
		t.Fatalf("第一個 text 不是 JSON 物件:%s", r.texts[0])
	}
	return m
}

func (r result) all() string { return strings.Join(r.texts, "\n") }

func callRes(t *testing.T, h http.Handler, tool string, args map[string]any) result {
	t.Helper()
	w := rpcPost(h, rpcReq(1, tool, args), "")
	if w.Code != 200 {
		t.Fatalf("%s:HTTP %d %s", tool, w.Code, w.Body.String())
	}
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return parseResult(t, m)
}

func TestUnifiedListAddsRequiredOrg(t *testing.T) {
	u := newUni(t)
	w := rpcPost(u.unified, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, "")
	var m struct {
		Result struct{ Tools []map[string]any }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	seen := map[string]bool{}
	for _, d := range m.Result.Tools {
		name := d["name"].(string)
		seen[name] = true
		sch := d["inputSchema"].(map[string]any)
		props := sch["properties"].(map[string]any)
		req, _ := sch["required"].([]any)
		hasReq := false
		for _, r := range req {
			hasReq = hasReq || r == "org"
		}
		if name == "orgs" || name == "schema_check" {
			if props["org"] != nil || hasReq {
				t.Errorf("%s 不需要 org", name)
			}
			continue
		}
		if p, _ := props["org"].(map[string]any); p == nil || p["type"] != "string" || !hasReq {
			t.Errorf("%s 要有必填的 org(string):%v", name, sch)
		}
	}
	if !seen["issues"] || !seen["orgs"] || seen["agent_task_delegate"] {
		t.Fatal("工具清單不對", seen)
	}
	// 舊端點規格不變,共用的核心規格也沒被改到
	w = rpcPost(u.old, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, "")
	if strings.Contains(w.Body.String(), `"org"`) {
		t.Fatal("舊端點規格不能有 org")
	}
	for _, d := range mcp.CoreToolDefs() {
		if _, bad := d.(map[string]any)["inputSchema"].(map[string]any)["properties"].(map[string]any)["org"]; bad {
			t.Fatal("共用規格被改寫")
		}
	}
	// initialize 的說明要求每次帶 org
	w = rpcPost(u.unified, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}}, "")
	if !strings.Contains(w.Body.String(), "每一次工具呼叫都必須帶 org") {
		t.Fatal(w.Body.String())
	}
}

func TestUnifiedMissingOrgRejectedWithAvailableList(t *testing.T) {
	u := newUni(t)
	for _, tool := range []string{"issues", "issue_create", "cabinet", "agents"} {
		for _, args := range []map[string]any{{"title": "x"}, {"title": "x", "org": ""}, {"title": "x", "org": "  "}, {"title": "x", "org": 7}, {"title": "x", "org": nil}} {
			r := callRes(t, u.unified, tool, args)
			txt := r.all()
			if !r.isErr || !strings.Contains(txt, "org") || !strings.Contains(txt, "lab(實驗室)") || !strings.Contains(txt, "lab2(第二實驗室)") {
				t.Fatalf("%s %v:要拒絕並列出可用組織:%v", tool, args, r)
			}
			if strings.Contains(txt, "other") || strings.Contains(txt, "別人的組織") {
				t.Fatal("不能列出沒有權限的組織:", txt)
			}
		}
	}
	// 沒寫入
	if l, _ := u.svc.ListIssues(u.boss, "lab"); len(l) != 0 {
		t.Fatal("缺 org 不能寫入", l)
	}
}

func TestUnifiedOrgScopedAndReportsOrg(t *testing.T) {
	u := newUni(t)
	r := callRes(t, u.unified, "issue_create", map[string]any{"org": "lab2", "title": "給二號"})
	if r.isErr || len(r.texts) != 1 || r.meta["omixor/org"] != "lab2" {
		t.Fatalf("成功結果要附實際用的組織:%v", r)
	}
	if r.obj(t)["key"] != "ISS-1" { // default ack
		t.Fatal(r.texts[0])
	}
	if l, _ := u.svc.ListIssues(u.boss, "lab2"); len(l) != 1 {
		t.Fatal("要寫進 lab2", l)
	}
	if l, _ := u.svc.ListIssues(u.boss, "lab"); len(l) != 0 {
		t.Fatal("lab 不能有", l)
	}
	// 讀取也要 org;查 lab 看不到 lab2 的 Issue
	if r := callRes(t, u.unified, "issues", map[string]any{"org": "lab", "status": "all"}); r.isErr || r.obj(t)["total"].(float64) != 0 || len(r.texts) != 1 || r.meta["omixor/org"] != "lab" {
		t.Fatal(r)
	}
	if r := callRes(t, u.unified, "issues", map[string]any{"org": " lab2 ", "status": "all"}); r.isErr || r.obj(t)["total"].(float64) != 1 || len(r.texts) != 1 || r.meta["omixor/org"] != "lab2" {
		t.Fatal(r)
	}
}

func TestUnifiedNonMemberDeniedWithoutLeaking(t *testing.T) {
	u := newUni(t)
	msg := func(org string) string {
		r := callRes(t, u.unified, "issue_create", map[string]any{"org": org, "title": "闖入"})
		if !r.isErr {
			t.Fatalf("%s 要被拒:%v", org, r)
		}
		return strings.ReplaceAll(r.all(), org, "<ORG>")
	}
	// 存在但不是成員 / 根本不存在 / 格式不合法:訊息除了組織名稱外完全一樣
	a, b, c := msg("other"), msg("no-such-org"), msg("bad.org/../x")
	if a != b || a != c {
		t.Fatalf("不能洩漏組織存不存在:\n%q\n%q\n%q", a, b, c)
	}
	if !strings.Contains(a, "lab(實驗室)") {
		t.Fatal(a)
	}
	for _, tool := range []string{"issues", "cabinet", "channels", "agents", "docs", "chain"} {
		if r := callRes(t, u.unified, tool, map[string]any{"org": "other"}); !r.isErr {
			t.Fatalf("%s 非成員要被拒", tool)
		}
	}
	if l, _ := u.svc.Store.ListIssues("other"); len(l) != 0 {
		t.Fatal("不能寫進別人的組織", l)
	}
}

func TestUnifiedBatchDifferentOrgs(t *testing.T) {
	u := newUni(t)
	batch := []any{
		rpcReq(1, "issue_create", map[string]any{"org": "lab", "title": "A"}),
		rpcReq(2, "issue_create", map[string]any{"org": "lab2", "title": "B"}),
		rpcReq(3, "issue_create", map[string]any{"org": "other", "title": "C"}), // 被拒,不影響其他
		rpcReq(4, "issue_create", map[string]any{"title": "D"}),                 // 缺 org
		rpcReq(5, "issues", map[string]any{"org": "lab", "status": "all"}),
		rpcReq(6, "issues", map[string]any{"org": "lab2", "status": "all"}),
	}
	w := rpcPost(u.unified, batch, "")
	var resp []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp) != 6 {
		t.Fatal(w.Body.String())
	}
	got := map[int]result{}
	for _, m := range resp {
		got[int(m["id"].(float64))] = parseResult(t, m)
	}
	if got[1].isErr || got[1].meta["omixor/org"] != "lab" || got[2].isErr || got[2].meta["omixor/org"] != "lab2" {
		t.Fatal(got[1], got[2])
	}
	if !got[3].isErr || !got[4].isErr {
		t.Fatal("非成員與缺 org 要被拒", got[3], got[4])
	}
	title := func(r result) string {
		is := r.obj(t)["issues"].([]any)
		if len(is) != 1 {
			t.Fatal(r)
		}
		return is[0].(map[string]any)["title"].(string)
	}
	if title(got[5]) != "A" || title(got[6]) != "B" {
		t.Fatal("每個呼叫要用各自的組織", got[5], got[6])
	}
}

func TestUnifiedOrgsNeedsNoOrg(t *testing.T) {
	u := newUni(t)
	r := callRes(t, u.unified, "orgs", map[string]any{})
	if r.isErr || len(r.texts) != 1 {
		t.Fatal(r)
	}
	m := r.obj(t)
	if _, has := m["current"]; has {
		t.Fatal("統一入口沒有目前組織:", m)
	}
	ids := []string{}
	for _, o := range m["orgs"].([]any) {
		om := o.(map[string]any)
		if _, has := om["current"]; has {
			t.Fatal("不標 current", om)
		}
		ids = append(ids, om["id"].(string))
	}
	if len(ids) != 2 || strings.Contains(strings.Join(ids, ","), "other") {
		t.Fatal(ids)
	}
	// 帶了 org 也不出錯(忽略)
	if r := callRes(t, u.unified, "orgs", map[string]any{"org": "lab"}); r.isErr {
		t.Fatal(r)
	}
}

func TestUnifiedMemberAbilityStillEnforced(t *testing.T) {
	// 成員資格過了,還是照權限表:lab2 的一般成員不能做要組織管理員的事(agent_upsert)
	u := newUni(t)
	r := callRes(t, u.unified, "agent_upsert", map[string]any{"org": "lab2", "id": "cto", "name": "研發長"})
	if !r.isErr || !strings.Contains(r.all(), "管理員") {
		t.Fatalf("一般成員不能新增角色:%v", r)
	}
	if r := callRes(t, u.unified, "agent_upsert", map[string]any{"org": "lab", "id": "cto", "name": "研發長"}); r.isErr {
		t.Fatalf("lab 的管理員可以:%v", r)
	}
}

func TestOldEndpointUnchangedAndRejectsOtherOrg(t *testing.T) {
	u := newUni(t)
	// 沒帶、帶相同的 org 都可以(結果沒有額外的 org 區塊,orgs 仍標 current)
	for _, args := range []map[string]any{{"title": "a"}, {"title": "b", "org": "lab"}, {"title": "c", "org": ""}} {
		r := callRes(t, u.old, "issue_create", args)
		if r.isErr || len(r.texts) != 1 {
			t.Fatalf("%v:%v", args, r)
		}
	}
	if l, _ := u.svc.ListIssues(u.boss, "lab"); len(l) != 3 {
		t.Fatal(len(l))
	}
	// 帶不同的 org(就算是自己有權限的 lab2):拒絕,什麼都不寫
	r := callRes(t, u.old, "issue_create", map[string]any{"title": "d", "org": "lab2"})
	if !r.isErr || !strings.Contains(r.all(), "lab") {
		t.Fatal(r)
	}
	if l, _ := u.svc.ListIssues(u.boss, "lab2"); len(l) != 0 {
		t.Fatal("不能寫進 lab2", l)
	}
	if r := callRes(t, u.old, "issues", map[string]any{"org": "other"}); !r.isErr {
		t.Fatal("要拒絕")
	}
	o := callRes(t, u.old, "orgs", map[string]any{}).obj(t)
	if o["current"] != "lab" {
		t.Fatal(o)
	}
}

// countingBackend 數 ListOrgs 被呼叫幾次
type countingBackend struct {
	backend.Backend
	n *int
}

func (c countingBackend) ListOrgs() ([]model.Org, error) { *c.n++; return c.Backend.ListOrgs() }

func TestUnifiedListOrgsOncePerRequest(t *testing.T) {
	u := newUni(t)
	n := 0
	h := mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{Resolve: func(*http.Request) (backend.Backend, error) {
		return countingBackend{&backend.Local{Svc: u.svc, P: u.boss}, &n}, nil
	}})
	batch := []any{rpcReq(1, "issues", map[string]any{"org": "lab"}), rpcReq(2, "issues", map[string]any{"org": "lab2"}), rpcReq(3, "agents", map[string]any{"org": "lab"})}
	if w := rpcPost(h, batch, ""); w.Code != 200 || n != 1 {
		t.Fatal(w.Code, "ListOrgs 次數", n)
	}
	n = 0
	callRes(t, h, "issues", map[string]any{"org": "lab"})
	if n != 1 {
		t.Fatal("每個請求各自查一次", n)
	}
}

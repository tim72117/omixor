package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"omixor-web/internal/auth"
	"omixor-web/internal/core"
	"omixor-web/internal/mcp"
)

// at 帶著「我選的組織」呼叫
func at(org string) map[string]string { return map[string]string{orgHeader: org} }

// text 回應的原始內容(陣列型的 view 也看得到)
func text(t *testing.T, c *client, method, path, org string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	if org != "" {
		req.Header.Set(orgHeader, org)
	}
	resp, err := c.c.Do(req)
	if err != nil {
		t.Error(err)
		return 0, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// 兩個帳號各在不同組織:同時讀寫互不影響、不外洩;切換只影響自己;移出組織後落到自己仍所屬的組織
func TestPerUserCurrentOrg(t *testing.T) {
	e := newCallerEnv(t, func(c *auth.Config) { c.LDAP.RequiredGroup = "" })
	alice := e.login("alice")
	jia := e.org // 系統工作組織
	made := e.ok(alice, "/api/action/org_create", map[string]any{"name": "Beta", "switch": true})
	beta, _ := made["id"].(string)
	system := func() string { return e.app.SystemOrgID() }
	if system() != jia {
		t.Fatal("有登入者時建立組織不換系統工作組織", system())
	}
	bobUser := joinOrg(t, e.app, e.au, "bob") // 在系統工作組織
	e.denied("carol", "還沒有加入任何組織")
	carolUser := e.userOf("carol")
	if code, out := alice.do("POST", "/api/action/member_save", map[string]any{"email": carolUser.Email}, at(beta)); code != 200 || out["error"] != nil {
		t.Fatal(code, out)
	}
	bob, carol := e.login("bob"), e.login("carol")

	// 沒帶組織:預設是自己所屬的
	_, v := carol.do("GET", "/api/view/orgs", nil, nil)
	if v["current"] != beta || v["perUser"] != true || strings.Contains(core2s(v["orgs"]), "id="+jia+";") || v["system"] != nil {
		t.Fatal("carol 的組織", v)
	}
	_, v = bob.do("GET", "/api/view/orgs", nil, nil)
	if v["current"] != jia || v["system"] != jia || strings.Contains(core2s(v), "Beta") {
		t.Fatal("bob 的組織", v)
	}

	// 同時讀寫
	var wg sync.WaitGroup
	work := func(who *client, org, mine, theirs string) {
		defer wg.Done()
		for i := range 5 {
			title := fmt.Sprintf("%s-%d", mine, i)
			for path, body := range map[string]map[string]any{
				"/api/tool/issue_create":     {"title": title},
				"/api/tool/doc_save":         {"title": title + "文件", "section": "docs", "body": "內文"},
				"/api/action/channel_create": {"name": title + "頻道"},
				"/api/action/project_create": {"name": title + "專案", "key": fmt.Sprintf("%s%d", mine[:2], i)},
			} {
				if strings.HasSuffix(path, "project_create") && who != alice {
					continue
				}
				if code, out := text(t, who, "POST", path, org, body); code != 200 || strings.Contains(out, `"error":"`) {
					t.Error(mine, path, code, out)
				}
			}
			for _, p := range []string{"issues", "cabinet", "channels", "projects", "members", "agents", "org_chain", "badges", "system", "incidents"} {
				code, out := text(t, who, "GET", "/api/view/"+p, org, nil)
				if code != 200 || strings.Contains(out, theirs+"-") {
					t.Error(mine, p, code, out)
				}
			}
			if code, out := text(t, who, "POST", "/api/action/issue_query", org, map[string]any{}); code != 200 || !strings.Contains(out, title) || strings.Contains(out, theirs+"-") {
				t.Error(mine, "issue_query", code, out)
			}
			if code, out := text(t, who, "POST", "/api/tool/issues", org, map[string]any{"status": "all"}); code != 200 || !strings.Contains(out, title) || strings.Contains(out, theirs+"-") {
				t.Error(mine, "tool issues", code, out)
			}
		}
	}
	wg.Add(2)
	go work(bob, jia, "jiaaa", "betaa")
	go work(carol, beta, "betaa", "jiaaa")
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	// 署名本人、寫在自己的組織
	for org, who := range map[string]string{jia: bobUser.ActorID, beta: carolUser.ActorID} {
		list, err := core.AgentService().Store.ListIssues(org)
		if err != nil || len(list) != 5 {
			t.Fatal(org, len(list), err)
		}
		for _, i := range list {
			if i.CreatedBy != who {
				t.Fatal(org, i.Title, i.CreatedBy)
			}
		}
	}

	// 不能用別人的組織
	for _, p := range []string{"/api/view/issues", "/api/view/members"} {
		if code, out := carol.do("GET", p, nil, at(jia)); code != 409 || out["orgChanged"] != true || out["org"] != beta {
			t.Fatal(p, code, out)
		}
	}
	if code, out := carol.do("POST", "/api/tool/issue_create", map[string]any{"title": "偷寫"}, at(jia)); code != 409 {
		t.Fatal(code, out)
	}
	if code, out := carol.do("POST", "/api/action/org_switch", map[string]any{"id": jia}, at(beta)); code != 403 {
		t.Fatal(code, out)
	}
	if code, out := carol.do("GET", "/api/issues/"+jia, nil, nil); code != 403 {
		t.Fatal(code, out)
	}
	if code, out := text(t, carol, "GET", "/api/issues/"+beta, "", nil); code != 200 || !strings.Contains(out, "betaa-0") || strings.Contains(out, "jiaaa-") {
		t.Fatal(code, out)
	}
	// 整台一份的功能:不是系統工作組織就不能用(管理員也一樣)
	for _, p := range []string{"/api/view/alerts", "/api/view/connections", "/api/view/voice_jobs", "/api/view/agent_runner", "/api/view/activity", "/api/view/people"} {
		if code, out := carol.do("GET", p, nil, at(beta)); code != 403 || out["notSystemOrg"] != true {
			t.Fatal(p, code, out)
		}
		if code, out := alice.do("GET", p, nil, at(beta)); code != 403 || out["notSystemOrg"] != true {
			t.Fatal("alice", p, code, out)
		}
	}
	if code, out := carol.do("GET", "/api/office/state", nil, nil); code != 403 {
		t.Fatal(code, out)
	}
	if code, out := carol.do("POST", "/api/action/activity_history", map[string]any{}, at(beta)); code != 403 {
		t.Fatal(code, out)
	}
	if code, out := text(t, bob, "GET", "/api/view/alerts", jia, nil); code != 200 {
		t.Fatal(code, out)
	}

	// 附件:圖片請求帶不了組織,只找自己能讀的組織
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	code, up := carol.do("POST", "/api/attachments", map[string]any{"data": png}, at(beta))
	if code != 200 {
		t.Fatal(code, up)
	}
	url := up["result"].(map[string]any)["url"].(string)
	if code, _ := text(t, carol, "GET", url, "", nil); code != 200 {
		t.Fatal("自己的附件", code)
	}
	if code, _ := text(t, bob, "GET", url, "", nil); code != 404 {
		t.Fatal("別的組織的附件", code)
	}
	// 同一份存放:畫面上傳的,同埠的 REST API 讀得到;非成員照核心的權限擋
	rest := "/api/orgs/" + beta + "/attachments/" + strings.TrimPrefix(url, "/api/attachments/")
	if code, out := carol.do("GET", rest, nil, nil); code != 200 || !strings.Contains(core2s(out), png) {
		t.Fatal("REST 讀畫面上傳的附件", code, out)
	}
	if code, _ := bob.do("GET", rest, nil, nil); code != 403 {
		t.Fatal("REST 讀別的組織的附件", code)
	}

	// 切換只影響自己;系統身分仍是系統工作組織
	if code, out := alice.do("POST", "/api/action/org_switch", map[string]any{"id": beta}, nil); code != 200 || core2s(out) == "" || out["result"].(map[string]any)["current"] != beta {
		t.Fatal(code, out)
	}
	if system() != jia {
		t.Fatal("org_switch 不換系統工作組織")
	}
	if _, v := bob.do("GET", "/api/view/orgs", nil, nil); v["current"] != jia {
		t.Fatal(v)
	}
	out, err := mcp.Run(e.app, "issues", map[string]any{"status": "all"}, core.MCPActor)
	if s := core2s(core.Object(out)); err != nil || !strings.Contains(s, "jiaaa-0") || strings.Contains(s, "betaa-") {
		t.Fatal(err, s)
	}
	if code, out := bob.do("POST", "/api/action/system_org_set", map[string]any{"id": beta}, nil); code != 403 {
		t.Fatal(code, out)
	}
	if code, out := alice.do("POST", "/api/action/system_org_set", map[string]any{"id": beta}, nil); code != 200 || system() != beta {
		t.Fatal(code, out, system())
	}
	// 換了系統工作組織,各人的目前組織不變
	if code, out := text(t, bob, "GET", "/api/view/issues", jia, nil); code != 200 || !strings.Contains(out, "jiaaa-0") || strings.Contains(out, "betaa-") {
		t.Fatal(code, out)
	}
	if code, out := text(t, carol, "GET", "/api/view/issues", beta, nil); code != 200 || !strings.Contains(out, "betaa-0") || strings.Contains(out, "jiaaa-") {
		t.Fatal(code, out)
	}
	if code, out := alice.do("POST", "/api/action/system_org_set", map[string]any{"id": jia}, nil); code != 200 {
		t.Fatal(code, out)
	}

	// 移出目前組織:落到仍所屬的組織(系統工作組織優先,否則清單第一個)
	third := e.ok(alice, "/api/action/org_create", map[string]any{"name": "Gamma"})["id"].(string)
	for _, org := range []string{third, jia} {
		if code, out := alice.do("POST", "/api/action/member_save", map[string]any{"email": carolUser.Email}, at(org)); code != 200 {
			t.Fatal(code, out)
		}
	}
	if code, out := alice.do("POST", "/api/action/member_remove", map[string]any{"email": carolUser.Email}, at(beta)); code != 200 {
		t.Fatal(code, out)
	}
	if code, out := carol.do("GET", "/api/view/issues", nil, at(beta)); code != 409 || out["org"] != jia {
		t.Fatal(code, out)
	}
	if _, v := carol.do("GET", "/api/view/orgs", nil, at(beta)); v["current"] != jia {
		t.Fatal(v)
	}
	if code, out := alice.do("POST", "/api/action/member_remove", map[string]any{"email": carolUser.Email}, at(jia)); code != 200 {
		t.Fatal(code, out)
	}
	if _, v := carol.do("GET", "/api/view/orgs", nil, at(beta)); v["current"] != third {
		t.Fatal(v)
	}
	// 刪掉別人正在用的組織:一樣落到別的
	if code, out := alice.do("POST", "/api/action/member_save", map[string]any{"email": bobUser.Email}, at(third)); code != 200 {
		t.Fatal(code, out)
	}
	e.ok(alice, "/api/action/org_delete", map[string]any{"id": third})
	if code, out := bob.do("GET", "/api/view/issues", nil, at(third)); code != 409 || out["org"] != jia {
		t.Fatal(code, out)
	}
}

// 分類表裡的名稱都存在(打錯字會悄悄變成只限系統工作組織)
func TestOrgScopeNamesExist(t *testing.T) {
	views := map[string]bool{}
	for _, v := range core.ViewNames() {
		views[v] = true
	}
	for _, set := range []map[string]bool{orgViews, hostViews} {
		for n := range set {
			if !views[n] {
				t.Error("沒有這個 view", n)
			}
		}
	}
	for _, set := range []map[string]bool{orgActions, hostActions} {
		for n := range set {
			if !core.HasAction(n) {
				t.Error("沒有這個 action", n)
			}
		}
	}
	for _, set := range []map[string]bool{orgTools, hostTools} {
		for n := range set {
			if !mcp.HasTool(n) {
				t.Error("沒有這個工具", n)
			}
		}
	}
}

// 畫面「任何組織都能讀的 view」清單(ui/src/lib/orgScope.ts)要和這裡的 orgViews、hostViews 一致
func TestOrgViewListMatchesUI(t *testing.T) {
	src, err := os.ReadFile("../../ui/src/lib/orgScope.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)ANY_ORG_VIEWS = new Set<string>\(\[(.*?)\]\)`).FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("orgScope.ts 找不到 ANY_ORG_VIEWS")
	}
	got := map[string]bool{}
	for _, x := range regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(m[1], -1) {
		got[x[1]] = true
	}
	want := map[string]bool{}
	for _, set := range []map[string]bool{orgViews, hostViews} {
		for k := range set {
			want[k] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("畫面 %v;後端 %v", got, want)
	}
}

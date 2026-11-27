package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"omixor-web/internal/core"
)

// 非 web 管理員的 members、org_chain view 的 canEdit 一律 false(畫面用它決定要不要顯示管理員按鈕),
// 走真正的 HTTP(view 的結果是轉過的 map,不是原本的 struct)。orgAdmin 另外照核心的組織角色
func TestViewCanEditFollowsWebAdminOverHTTP(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice") // 管理員帳號名單
	bobUser := joinOrg(t, e.app, e.au, "bob")
	bob := e.login("bob")

	view := func(c *client, name string) map[string]any {
		t.Helper()
		code, out := c.do("GET", "/api/view/"+name, nil, nil)
		if code != 200 {
			t.Fatalf("%s:%d %v", name, code, out)
		}
		return out
	}
	for _, name := range []string{"members", "org_chain"} {
		if v := view(alice, name); v["canEdit"] != true {
			t.Errorf("管理員的 %s canEdit 要是 true:%v", name, v["canEdit"])
		}
		if v := view(bob, name); v["canEdit"] != false {
			t.Errorf("非管理員的 %s canEdit 要是 false:%v", name, v["canEdit"])
		}
	}
	if v := view(bob, "members"); v["orgAdmin"] != false {
		t.Errorf("一般成員的 orgAdmin 要是 false:%v", v["orgAdmin"])
	}
	e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "admin"})
	if v := view(bob, "members"); v["orgAdmin"] != true || v["canEdit"] != false {
		t.Errorf("不在名單上的組織管理員:orgAdmin=true、canEdit=false:%v %v", v["orgAdmin"], v["canEdit"])
	}
	// 伺服器端的限制不變:組織管理員但不在名單上,改成員仍是 403
	if code, out := bob.do("POST", "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "member"}, nil); code != 403 {
		t.Errorf("非名單管理員改成員要 403:%d %v", code, out)
	}
}

// events 開一條真正的 SSE 連線,回傳每則通知的原始內容
func events(t *testing.T, c *client) <-chan string {
	t.Helper()
	resp, err := c.c.Get(c.srv.URL + "/api/events")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE:%v %v", resp, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	ch := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				ch <- data
			}
		}
	}()
	return ch
}

// 專案成員、受限旗標改了(畫面動作、工具、REST),開著的畫面要收到所有受影響的主題;通知只有版本與主題名稱
func TestVisibilityChangesNotifyOpenScreens(t *testing.T) {
	e := newCallerEnv(t, nil)
	alice := e.login("alice")
	bobUser := joinOrg(t, e.app, e.au, "bob")
	bob := e.login("bob")
	pid := e.ok(alice, "/api/action/project_create", map[string]any{"name": "機密案", "key": "SEC", "restricted": true})["id"].(string)
	ch := events(t, bob)

	notified := func(stage string, change func()) {
		t.Helper()
		for quiet := false; !quiet; { // 丟掉之前的通知
			select {
			case <-ch:
			case <-time.After(300 * time.Millisecond):
				quiet = true
			}
		}
		change()
		missing := []string{"issues", "cabinet", "projects", "agents", "activity", "badges", "agent_runner"}
		deadline := time.After(5 * time.Second)
		for len(missing) > 0 {
			select {
			case raw := <-ch:
				var ev map[string]any
				if err := json.Unmarshal([]byte(raw), &ev); err != nil || len(ev) != 2 || ev["version"] == nil {
					t.Fatalf("%s:通知只能有 version、topics:%s", stage, raw)
				}
				var got core.Event
				_ = json.Unmarshal([]byte(raw), &got)
				missing = slices.DeleteFunc(missing, func(x string) bool { return slices.Contains(got.Topics, x) })
			case <-deadline:
				t.Fatalf("%s:沒收到主題 %v", stage, missing)
			}
		}
	}
	rest := func(path string, body map[string]any) {
		t.Helper()
		if code, out := alice.do("POST", path, body, map[string]string{"X-Omixor-CSRF": "1"}); code != 200 {
			t.Fatalf("%s:%d %v", path, code, out)
		}
	}
	member := map[string]any{"id": pid, "actor": bobUser.Email, "role": "member"}
	notified("加成員(畫面)", func() { e.ok(alice, "/api/action/project_member_set", member) })
	notified("移成員(畫面)", func() {
		e.ok(alice, "/api/action/project_member_remove", map[string]any{"id": pid, "actor": bobUser.Email})
	})
	base := "/api/orgs/" + e.org + "/projects/" + pid
	// 被移出後,開著的文件櫃重查會拿到這個訊息,畫面靠它回到未分專案(CabinetView.tsx)
	if code, out := bob.do("POST", "/api/action/cabinet_query", map[string]any{"project": pid}, nil); code != 404 || !strings.Contains(core2s(out["error"]), "找不到專案") {
		t.Errorf("移出後查專案文件櫃:%d %v", code, out)
	}
	notified("加成員(REST)", func() { rest(base+"/members", map[string]any{"actor": bobUser.Email, "role": "member"}) })
	notified("移成員(REST)", func() { rest(base+"/members/remove", map[string]any{"actor": bobUser.Email}) })
	notified("取消受限(畫面)", func() {
		e.ok(alice, "/api/action/project_update", map[string]any{"id": pid, "restricted": false})
	})
	notified("設回受限(工具)", func() {
		if code, out := alice.do("POST", "/api/tool/project_save", map[string]any{"id": pid, "restricted": true}, nil); code != 200 || out["error"] != nil {
			t.Fatalf("project_save:%d %v", code, out)
		}
	})
	notified("改組織角色(畫面)", func() {
		e.ok(alice, "/api/action/member_save", map[string]any{"email": bobUser.Email, "role": "admin"})
	})
}

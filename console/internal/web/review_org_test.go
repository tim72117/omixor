package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"omixor-web/internal/core"

	"omixor-core/service"
)

func emailOf(t *testing.T, account string) string {
	t.Helper()
	u, err := core.AgentService().Store.UserByEvoleapID(service.LDAPIDPrefix + account)
	if err != nil {
		t.Fatal(err)
	}
	return u.Email
}

// 系統工作組織以外的組織可以宣告事件(指揮官是組織成員,沒給是自己)
func TestIncidentDeclareOutsideSystemOrg(t *testing.T) {
	bob, alice, app := adminTestClients(t)
	code, out := alice.do("POST", "/api/action/org_create", map[string]any{"name": "Beta"}, nil)
	r, _ := out["result"].(map[string]any)
	if code != 200 || r == nil {
		t.Fatal(code, out)
	}
	beta := r["id"].(string)
	if beta == app.SystemOrgID() {
		t.Fatal("不該換系統工作組織")
	}
	mail := emailOf(t, "bob")
	if code, out := alice.do("POST", "/api/action/member_save", map[string]any{"email": mail}, at(beta)); code != 200 || out["error"] != nil {
		t.Fatal(code, out)
	}
	declare := func(args map[string]any) map[string]any {
		t.Helper()
		code, out := bob.do("POST", "/api/tool/incident_declare", args, at(beta))
		r, _ := out["result"].(map[string]any)
		if code != 200 || r == nil {
			t.Fatal(code, out)
		}
		return r
	}
	declare(map[string]any{"title": "沒給指揮官", "sev": 3, "impact": "x"})
	declare(map[string]any{"title": "指定成員", "sev": 3, "impact": "x", "commander": mail})
	if code, out := bob.do("POST", "/api/tool/incident_declare", map[string]any{"title": "不存在的人", "sev": 3, "impact": "x", "commander": "nobody@nowhere.example"}, at(beta)); code == 200 {
		t.Fatal(out)
	}
	code, body := text(t, bob, "GET", "/api/view/incidents", beta, nil)
	if code != 200 || !strings.Contains(body, "指定成員") || !strings.Contains(body, mail) {
		t.Fatal(code, body)
	}
}

// 虛擬辦公室的長連線:被移出系統工作組織後斷線
func TestOfficeEventsDisconnectOnRemoval(t *testing.T) {
	old := officeRecheck
	officeRecheck = 100 * time.Millisecond
	t.Cleanup(func() { officeRecheck = old })
	bob, alice, _ := adminTestClients(t)
	mail := emailOf(t, "bob")
	req, _ := http.NewRequest("GET", bob.srv.URL+"/api/office/events", nil)
	resp, err := bob.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Skip("這台沒有 office3d:", resp.StatusCode, string(b))
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	select {
	case <-done:
		t.Fatal("還沒移出就斷了")
	case <-time.After(500 * time.Millisecond):
	}
	if code, out := alice.do("POST", "/api/action/member_remove", map[string]any{"email": mail}, nil); code != 200 || out["error"] != nil {
		t.Fatal(code, out)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("移出後沒有斷線")
	}
}

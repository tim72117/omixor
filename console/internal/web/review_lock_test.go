package web

import (
	"io"
	"net/http"
	"testing"
	"time"

	"omixor-web/internal/core"
)

// 系統工作組織的鎖:慢慢送內容的請求、非管理員的換組織請求、拿著讀鎖很久的呼叫,都不能讓整台卡住
func TestSystemOrgLockNeverFreezes(t *testing.T) {
	bob, alice, app := adminTestClients(t)
	first := app.SystemOrgID()
	code, out := alice.do("POST", "/api/action/org_create", map[string]any{"name": "第二組織"}, nil)
	r, _ := out["result"].(map[string]any)
	if code != 200 || r == nil {
		t.Fatal(code, out)
	}
	second := r["id"].(string)

	quick := func(what string, c *client, method, path string, body any, want int) {
		t.Helper()
		done := make(chan int, 1)
		go func() {
			code, _ := c.do(method, path, body, nil)
			done <- code
		}()
		select {
		case code := <-done:
			if code != want {
				t.Fatalf("%s:%d,要 %d", what, code, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal(what + ":卡住了")
		}
	}
	switchTo := func(org string, want int) {
		t.Helper()
		quick("換系統工作組織", alice, "POST", "/api/action/system_org_set", map[string]any{"id": org}, want)
	}

	// 請求內容送到一半停住(bob 的工具呼叫):換組織、其他人的請求照常
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("POST", bob.srv.URL+"/api/tool/issues", pr)
	req.Header.Set("Content-Type", "application/json")
	slow := make(chan struct{})
	go func() {
		defer close(slow)
		if resp, err := bob.c.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	if _, err := pw.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	switchTo(second, 200)
	quick("慢請求期間的讀取", alice, "GET", "/api/view/alerts", nil, 200)
	switchTo(first, 200)
	_, _ = pw.Write([]byte("}"))
	_ = pw.Close()
	<-slow

	// 有呼叫一直拿著讀鎖:換組織放棄(409),之後的請求照常;非管理員的換組織請求直接 403,不排寫鎖
	old := core.SystemSwitchWait
	core.SystemSwitchWait = 300 * time.Millisecond
	t.Cleanup(func() { core.SystemSwitchWait = old })
	lock := app.SystemOrgLock()
	lock.RLock()
	for _, n := range []string{"system_org_set", "org_delete", "org_create"} {
		quick("非管理員 "+n, bob, "POST", "/api/action/"+n, map[string]any{"id": second}, 403)
	}
	switchTo(second, 409)
	quick("放棄換組織之後的讀取", bob, "GET", "/api/view/alerts", nil, 200)
	quick("建立組織(要換鎖)", alice, "POST", "/api/action/org_delete", map[string]any{"id": second}, 409)
	lock.RUnlock()
	if app.SystemOrgID() != first {
		t.Fatal("放棄的換組織不能生效", app.SystemOrgID())
	}
	switchTo(second, 200)
	switchTo(first, 200)

	// office_move 等待中(沒有檢視器時可能立刻結束,至少不拿鎖):換組織不用等它
	waiting := make(chan struct{})
	go func() {
		defer close(waiting)
		bob.do("POST", "/api/tool/office_move", map[string]any{"text": "工程師 去 會議室", "wait": 3}, nil)
	}()
	time.Sleep(200 * time.Millisecond)
	switchTo(second, 200)
	<-waiting
}

// 等人操作的動作用 pin 放掉讀鎖:期間可以換組織,回來時知道換過了
func TestSystemPinAway(t *testing.T) {
	_, alice, app := adminTestClients(t)
	code, out := alice.do("POST", "/api/action/org_create", map[string]any{"name": "第二組織"}, nil)
	r, _ := out["result"].(map[string]any)
	if code != 200 || r == nil {
		t.Fatal(code, out)
	}
	second := r["id"].(string)
	pin := &gatePin{lock: app.SystemOrgLock(), app: app}
	pin.acquire()
	defer pin.release()
	if !pin.Away(func() {}) {
		t.Fatal("沒換組織要回 true")
	}
	if pin.Away(func() {
		if code, out := alice.do("POST", "/api/action/system_org_set", map[string]any{"id": second}, nil); code != 200 {
			t.Error("放掉讀鎖期間要能換組織", code, out)
		}
	}) {
		t.Fatal("換過組織要回 false")
	}
}

package mcp

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"opsweb/internal/core"
)

// Facebook MCP 工具:假的 graph.facebook.com(core.FBFakeGraph),照 Swift FacebookTests.swift 的 MCP 部分搬

func fbTestApp(t *testing.T) (*core.App, *core.FakeNet) {
	t.Helper()
	a := extTestApp(t)
	f := core.NewFakeNet(core.FBFakeGraph)
	saved := core.FBConfirmOwner
	core.FBConfirmOwner = func(string) bool { return true }
	t.Cleanup(func() { f.Close(); core.FBConfirmOwner = saved })
	return a, f
}

func fbGraphPosts(f *core.FakeNet) int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method == "POST" {
			n++
		}
	}
	return n
}

func TestMCPFBToolDefinitionsHaveNoPublish(t *testing.T) {
	a := extTestApp(t)
	h := &Handler{App: a}
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Annotations map[string]any `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(h.Handle(core.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})), &resp); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tl := range resp.Result.Tools {
		if strings.Contains(tl.Name, "fb_publish") || strings.Contains(tl.Name, "fb_schedule") {
			t.Fatalf("不能有發佈工具:%s", tl.Name)
		}
		if strings.HasPrefix(tl.Name, "ops_fb_") {
			names = append(names, tl.Name)
			if ro, _ := tl.Annotations["readOnlyHint"].(bool); ro != (tl.Name != "ops_fb_draft_save") {
				t.Fatalf("%s readOnlyHint = %v", tl.Name, ro)
			}
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "ops_fb_draft_save,ops_fb_drafts,ops_fb_insights,ops_fb_pages,ops_fb_posts" {
		t.Fatalf("%v", names)
	}
	var init struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	_ = json.Unmarshal(h.Handle(core.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "initialize", "params": map[string]any{}})), &init)
	if !strings.Contains(init.Result.Instructions, "ops_fb_draft_save") {
		t.Fatal("instructions 要提到 ops_fb_draft_save")
	}
}

func TestMCPFBNotConnectedErrors(t *testing.T) {
	a, f := fbTestApp(t)
	for _, name := range []string{"ops_fb_pages", "ops_fb_posts", "ops_fb_insights"} {
		if msg := extErr(t, a, name, nil); msg != core.FBNotConnected {
			t.Fatalf("%s: %s", name, msg)
		}
	}
	// 草稿是本機資料,沒連線也能用
	if out := extOK(t, a, "ops_fb_drafts", nil); out["total"] != float64(0) {
		t.Fatalf("%v", out)
	}
	if len(f.Requests()) != 0 {
		t.Fatal("沒連線不送請求")
	}
}

func TestMCPFBReadTools(t *testing.T) {
	a, f := fbTestApp(t)
	if err := core.FBAddToken(a, core.FBFakePageToken, core.Person("tim")); err != nil {
		t.Fatal(err)
	}
	pages := extOK(t, a, "ops_fb_pages", nil)
	if pages["connected"] != true || pages["mode"] != "page_token" || pages["apiVersion"] != "v24.0" {
		t.Fatalf("%v", pages)
	}
	p := pages["pages"].([]any)[0].(map[string]any)
	if p["id"] != core.FBFakePage1 || p["followersCount"] != float64(150) || p["hasToken"] != true || p["managed"] != true {
		t.Fatalf("%v", p)
	}
	if _, ok := p["accessToken"]; ok {
		t.Fatal("不能有權杖")
	}
	if strings.Contains(string(core.Marshal(pages)), "EAAG") {
		t.Fatal("輸出不能有權杖")
	}
	refreshed := extOK(t, a, "ops_fb_pages", map[string]any{"refresh": true})
	if rp := refreshed["pages"].([]any)[0].(map[string]any); rp["fanCount"] != float64(120) || rp["error"] != nil {
		t.Fatalf("%v", rp)
	}

	posts := extOK(t, a, "ops_fb_posts", map[string]any{"page": "evoleap", "limit": 2})
	pp := posts["pages"].([]any)[0].(map[string]any)
	list := pp["posts"].([]any)
	if len(list) != 2 || pp["pageName"] != "evoleap" {
		t.Fatalf("%v", pp)
	}
	first := list[0].(map[string]any)
	if first["engagement"] != float64(10) || first["createdTime"] != "2026-11-01T08:00:00Z" {
		t.Fatalf("%v", first)
	}
	var reach map[string]any
	for _, m := range first["insights"].([]any) {
		if m.(map[string]any)["key"] == "reach" {
			reach = m.(map[string]any)
		}
	}
	if reach["status"] != "ok" {
		t.Fatalf("%v", reach)
	}
	if s := pp["scheduled"].([]any)[0].(map[string]any); s["scheduledTime"] != "2027-01-15T08:00:00Z" || s["isPublished"] != false {
		t.Fatalf("%v", s)
	}
	noExtra := extOK(t, a, "ops_fb_posts", map[string]any{"insights": false, "scheduled": false, "limit": 0})
	np := noExtra["pages"].([]any)[0].(map[string]any)
	if len(np["posts"].([]any)) != 1 || len(np["scheduled"].([]any)) != 0 || len(np["posts"].([]any)[0].(map[string]any)["insights"].([]any)) != 0 {
		t.Fatalf("limit 最少 1、不附 insights 與排程:%v", np)
	}

	insights := extOK(t, a, "ops_fb_insights", map[string]any{"period": "day", "days": 7})
	ip := insights["pages"].([]any)[0].(map[string]any)
	if ip["fanCount"] != float64(120) || ip["period"] != "day" || ip["since"] == nil {
		t.Fatalf("%v", ip)
	}
	for _, m := range ip["metrics"].([]any) {
		mm := m.(map[string]any)
		switch mm["key"] {
		case "engagement":
			if mm["status"] != "deprecated" || mm["total"] != nil {
				t.Fatalf("%v", mm)
			}
		case "reach":
			if mm["total"] != float64(60) {
				t.Fatalf("%v", mm)
			}
		}
	}
	if msg := extErr(t, a, "ops_fb_insights", map[string]any{"period": "month"}); msg != "period 必須是 day、week、days_28 之一" {
		t.Fatal(msg)
	}
	if msg := extErr(t, a, "ops_fb_posts", map[string]any{"page": "nope"}); msg != "找不到專頁 nope。可用:evoleap(1001)" {
		t.Fatal(msg)
	}
	if fbGraphPosts(f) != 0 {
		t.Fatal("讀取工具不會送出 POST")
	}

	// 單一專頁的權杖過期:不讓整個工具失敗,錯誤放在該專頁
	tok := "EXPIRED"
	core.FBSetPageToken(a, &tok, core.FBFakePage1)
	bad := extOK(t, a, "ops_fb_posts", nil)
	bp := bad["pages"].([]any)[0].(map[string]any)
	if e, _ := bp["error"].(string); !strings.Contains(e, "重新連線") || len(bp["posts"].([]any)) != 0 {
		t.Fatalf("%v", bp)
	}
	bi := extOK(t, a, "ops_fb_insights", nil)["pages"].([]any)[0].(map[string]any)
	if _, ok := bi["since"]; ok || bi["error"] == nil || len(bi["metrics"].([]any)) != 0 {
		t.Fatalf("%v", bi)
	}
}

func TestMCPFBDraftSaveSignsAgentAndCannotPublish(t *testing.T) {
	a, f := fbTestApp(t)
	if err := core.FBAddToken(a, core.FBFakePageToken, core.Person("tim")); err != nil {
		t.Fatal(err)
	}
	d := extOK(t, a, "ops_fb_draft_save", map[string]any{"message": "週末限定", "link": "https://evoleap.io",
		"proposed_time": "2026-11-20T20:00:00+08:00", "agent": "marketing"})
	id := d["id"].(string)
	if id != "FD-1" || d["author"] != "agent:marketing" || d["authorName"] != "行銷" || d["status"] != "draft" ||
		d["pageId"] != core.FBFakePage1 || d["pageName"] != "evoleap" || d["proposedTime"] != "2026-11-20T12:00:00Z" || d["note"] == nil {
		t.Fatalf("%v", d)
	}
	a.Mu.Lock()
	act := a.C["activity"][0]
	a.Mu.Unlock()
	if core.ActorFrom(act["actor"]) != core.AI("marketing") || act["text"] != "新增 Facebook 草稿 FD-1「週末限定」" || act["section"] != "automation" {
		t.Fatalf("%v", act)
	}

	// 修改
	u := extOK(t, a, "ops_fb_draft_save", map[string]any{"id": "fd-1", "message": "週末限定!"})
	if u["message"] != "週末限定!" || u["author"] != "agent:marketing" {
		t.Fatalf("修改不換撰寫者:%v", u)
	}
	// 沒帶 agent 署名 MCP 用戶端
	m := extOK(t, a, "ops_fb_draft_save", map[string]any{"message": "另一篇"})
	if m["author"] != "mcp" || m["authorName"] != "MCP 用戶端" || m["id"] != "FD-2" {
		t.Fatalf("%v", m)
	}

	// 錯誤
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"message": "x", "agent": "nobody"}, "找不到角色 nobody"},
		{map[string]any{}, "草稿至少要有 message、link 或 image_url"},
		{map[string]any{"message": "   "}, "草稿至少要有 message、link 或 image_url"},
		{map[string]any{"message": "x", "link": "javascript:alert(1)"}, "link 必須是 http 或 https 網址"},
		{map[string]any{"message": "x", "image_url": "ftp://x"}, "image_url 必須是 http 或 https 網址"},
		{map[string]any{"message": "x", "proposed_time": "明天"}, "proposed_time 要用 ISO 8601,例如 2026-11-20T20:00:00+08:00"},
		{map[string]any{"message": "x", "page": "nope"}, "找不到專頁 nope。可用:evoleap(1001)"},
		{map[string]any{"id": "FD-99", "message": "x"}, "找不到草稿 FD-99"},
	} {
		if msg := extErr(t, a, "ops_fb_draft_save", c.args); msg != c.want {
			t.Fatalf("%v:%s", c.args, msg)
		}
	}
	if _, err := core.FBPublish(a, id, nil, time.Now(), core.Person("tim")); err != nil {
		t.Fatal(err)
	}
	if msg := extErr(t, a, "ops_fb_draft_save", map[string]any{"id": id, "message": "改"}); msg != "草稿 FD-1 已發佈,不能修改" {
		t.Fatal(msg)
	}
	// MCP 存草稿不會碰 Graph API 的寫入
	if fbGraphPosts(f) != 1 {
		t.Fatal("只有人按的那一次發佈")
	}

	if list := extOK(t, a, "ops_fb_drafts", nil); list["total"] != float64(1) {
		t.Fatalf("%v", list)
	}
	all := extOK(t, a, "ops_fb_drafts", map[string]any{"status": "all", "limit": 1})
	if all["total"] != float64(2) || len(all["drafts"].([]any)) != 1 || all["drafts"].([]any)[0].(map[string]any)["id"] != "FD-1" {
		t.Fatalf("新的在前:%v", all)
	}
	one := extOK(t, a, "ops_fb_drafts", map[string]any{"id": id})
	if one["status"] != "published" || one["postId"] != "1001_555" || one["publishedBy"] != "tim" {
		t.Fatalf("%v", one)
	}
	if msg := extErr(t, a, "ops_fb_drafts", map[string]any{"status": "x"}); msg != "status 必須是 draft、scheduled、published、all 之一" {
		t.Fatal(msg)
	}
	if msg := extErr(t, a, "ops_fb_drafts", map[string]any{"page": "nope"}); msg != "找不到專頁 nope" {
		t.Fatal(msg)
	}
	if byPage := extOK(t, a, "ops_fb_drafts", map[string]any{"page": "EVOLEAP", "status": "all"}); byPage["total"] != float64(2) {
		t.Fatalf("%v", byPage)
	}
}

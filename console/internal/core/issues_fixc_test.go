package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// 外部(別的用戶端)改了 Issue:取得 Issue 的目前版本與標題
func bumpExternally(t *testing.T, a *App, id, title string) {
	t.Helper()
	b, err := a.issueBackend()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.UpdateIssue(a.currentOrgNow(), id, service.IssuePatch{Title: &title, Author: "外部"}); err != nil {
		t.Fatal(err)
	}
}

func waitIssuesEvent(ch chan Event) bool {
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			for _, tp := range ev.Topics {
				if tp == "issues" {
					return true
				}
			}
		case <-deadline:
			return false
		}
	}
}

// H1:畫面握著舊版本動作 → 409;失敗後要通知 issues,快取是核心的最新版本;之後的動作(再讀版本)不再 409
func TestUpdateConflictNotifiesIssuesAndRefreshesCache(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	is := a.CreateIssueFor("衝突", "", "", nil, "", "", "mcp", "", "")
	a.Mu.Unlock()
	if is.ID == "" || is.Version == 0 {
		t.Fatalf("建立:%+v %v", is, a.IssueRemoteError())
	}
	bumpExternally(t, a, is.ID, "外部改過")
	ch := a.Subscribe()
	defer a.Unsubscribe(ch)

	a.Mu.Lock()
	got := a.UpdateIssueAtVersion(is.ID, "mcp", "", is.Version, func(i *model.Issue) { i.Priority = "high" })
	err := a.IssueRemoteErr()
	cached := a.Issue(is.ID)
	a.Mu.Unlock()
	if got != nil || err == nil {
		t.Fatalf("舊版本要 409:%v %v", got, err)
	}
	var se *service.Error
	if !errors.As(err, &se) || se.Code != 409 || se.Data["currentVersion"] == nil {
		t.Fatalf("409 要帶 currentVersion:%#v", err)
	}
	if !waitIssuesEvent(ch) {
		t.Fatal("409 之後要發 issues 通知,畫面才會重讀")
	}
	if cached == nil || cached.Title != "外部改過" || cached.Version <= is.Version {
		t.Fatalf("快取要是核心最新的:%+v(舊版 %d)", cached, is.Version)
	}
	// 用最新版本再動作成功
	a.Mu.Lock()
	ok := a.UpdateIssueAtVersion(is.ID, "mcp", "", cached.Version, func(i *model.Issue) { i.Priority = "high" })
	a.Mu.Unlock()
	if ok == nil || ok.Priority != "high" {
		t.Fatalf("最新版本要成功:%v", a.IssueRemoteError())
	}
}

// lastErr:A 失敗(409)之後 B 成功,IssueRemoteErr 不能殘留 A 的錯誤;409 不算連線錯誤(L9)
func TestLastErrNotLeftAfterSuccessAndUserErrNotConnErr(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	is := a.CreateIssueFor("A", "", "", nil, "", "", "mcp", "", "")
	a.Mu.Unlock()
	bumpExternally(t, a, is.ID, "外部")
	a.Mu.Lock()
	failed := a.UpdateIssueAtVersion(is.ID, "mcp", "", is.Version, func(i *model.Issue) { i.Priority = "high" })
	hasErr := a.IssueRemoteErr()
	conn := a.issueConnErr()
	a.Mu.Unlock()
	if failed != nil || hasErr == nil {
		t.Fatal("A 要失敗")
	}
	if conn != "" {
		t.Fatalf("409 是使用者錯誤,不能當連線錯誤顯示:%q", conn)
	}
	a.Mu.Lock()
	okIssue := a.UpdateIssue(is.ID, "mcp", func(i *model.Issue) { i.Priority = "low" })
	left := a.IssueRemoteErr()
	text := a.IssueRemoteError()
	a.Mu.Unlock()
	if okIssue == nil || left != nil || text != "" {
		t.Fatalf("B 成功後不能殘留 A 的錯誤:%v %q", left, text)
	}
	// 純連線型錯誤(未分類的 500)才算連線錯誤
	a.Mu.Lock()
	a.issueRemoteFailed(errors.New("連不上"))
	conn = a.issueConnErr()
	a.Mu.Unlock()
	if conn == "" {
		t.Fatal("一般(500)錯誤要算連線錯誤")
	}
}

// M3:沒給 version 時 patch 不帶 version(核心不檢查);給了才帶
func TestIssuePatchVersionOnlyWhenGiven(t *testing.T) {
	before := model.Issue{ID: "ISS-1", Title: "t", Status: "open", Priority: "normal", Version: 7}
	n := cloneIssue(before)
	n.Title = "新"
	p, _ := issuePatchFor(n, before, "mcp")
	if p.Version != nil {
		t.Fatalf("呼叫者沒給 version,不能自己帶 before.Version:%d", *p.Version)
	}
}

// L5:不存在的 id → issue_restore、issue_delete 回 404(StatusError)
func TestTrashActionsNotFound404(t *testing.T) {
	a := newTestApp(t)
	for _, name := range []string{"issue_restore", "issue_delete", "issue_purge"} {
		_, err := Action(a, name, map[string]any{"id": "NOPE-999"})
		var se StatusError
		if !errors.As(err, &se) || se.Status != 404 {
			t.Errorf("%s 不存在的 id 要 404:%#v", name, err)
		}
	}
}

// L8:專案清單背景讀回來時畫面已經換組織:舊組織的結果不能蓋進快取
func TestProjectsRefreshDropsStaleOrg(t *testing.T) {
	a := newTestApp(t)
	org := a.currentOrgNow()
	if _, err := a.CabinetOps().CreateProject(org, service.ProjectInput{Name: "網站", Key: "WEB"}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	list := a.projectsOf(org)
	a.Mu.Unlock()
	if len(list) != 1 {
		t.Fatalf("專案:%v", list)
	}
	st := a.projSt()
	st.mu.Lock()
	st.want = "other-org" // 畫面已經換去別的組織
	st.mu.Unlock()
	if a.projectsRefresh(org) {
		t.Fatal("過時的結果要丟掉")
	}
	st.mu.Lock()
	cur := st.org
	st.mu.Unlock()
	if cur != org {
		t.Fatalf("快取組織不能被改:%s", cur)
	}
	a.StopProjectsTimer()
	a.StopProjectsTimer() // 可重複呼叫
}

func lastActivityText(a *App) string {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	l := a.C["activity"]
	if len(l) == 0 {
		return ""
	}
	s, _ := l[0]["text"].(string)
	return s
}

// M6:issue_resolve 把舊的 key、垃圾桶裡的 Issue 解析成編號;找不到 404
func TestIssueResolveOldKeyAndTrashed(t *testing.T) {
	a := newTestApp(t)
	org := a.currentOrgNow()
	p1, err := a.CabinetOps().CreateProject(org, service.ProjectInput{Name: "甲", Key: "AAA"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := a.CabinetOps().CreateProject(org, service.ProjectInput{Name: "乙", Key: "BBB"})
	if err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	is := a.CreateIssueIn(service.IssueInput{Title: "搬家", Author: "mcp", Project: p1.ID})
	a.Mu.Unlock()
	if is.Key != "AAA-1" {
		t.Fatalf("key:%s %v", is.Key, a.IssueRemoteError())
	}
	a.Mu.Lock()
	moved := a.UpdateIssue(is.ID, "mcp", func(i *model.Issue) { i.Project = p2.ID })
	a.Mu.Unlock()
	if moved == nil || moved.Key == "AAA-1" {
		t.Fatalf("搬專案要換 key:%+v", moved)
	}
	out, err := Action(a, "issue_resolve", map[string]any{"ref": "aaa-1"})
	r, ok := out.(IssueRef)
	if err != nil || !ok || r.ID != is.ID || r.Key != moved.Key || r.Deleted {
		t.Fatalf("舊 key 要解析成目前的:%v %v", out, err)
	}
	if _, err := Action(a, "issue_delete", map[string]any{"id": is.ID}); err != nil {
		t.Fatal(err)
	}
	out, err = Action(a, "issue_resolve", map[string]any{"ref": is.ID})
	if r, ok = out.(IssueRef); err != nil || !ok || r.Key != moved.Key || !r.Deleted {
		t.Fatalf("垃圾桶裡的:%v %v", out, err)
	}
	_, err = Action(a, "issue_resolve", map[string]any{"ref": "ZZZ-99"})
	var se StatusError
	if !errors.As(err, &se) || se.Status != 404 {
		t.Fatalf("找不到要 404:%#v", err)
	}
}

// L1:動態牆不顯示內部 UUID(永久刪除、建立頻道都用 key)
func TestActivityShowsKeyNotUUID(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	is := a.CreateIssueFor("要刪的", "", "", nil, "", "", "mcp", "", "")
	a.Mu.Unlock()
	if is.Key == "" {
		t.Fatal("沒有 key")
	}
	if _, err := Action(a, "issue_delete", map[string]any{"id": is.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "issue_purge", map[string]any{"id": is.ID}); err != nil {
		t.Fatal(err)
	}
	if txt := lastActivityText(a); strings.Contains(txt, is.ID) || !strings.Contains(txt, is.Key) {
		t.Fatalf("永久刪除的動態要用 key:%q", txt)
	}
	a.Mu.Lock()
	is2 := a.CreateIssueFor("要開頻道", "", "", nil, "", "", "mcp", "", "")
	a.Mu.Unlock()
	if _, err := Action(a, "channel_create", map[string]any{"name": "討論", "issueId": is2.ID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if txt := lastActivityText(a); strings.Contains(txt, is2.ID) || !strings.Contains(txt, is2.Key) {
		t.Fatalf("建立頻道的動態要用 key:%q", txt)
	}
}

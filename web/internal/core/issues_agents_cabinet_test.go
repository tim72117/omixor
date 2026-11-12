package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// 每個測試用自己的暫存資料夾
func newTestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	a := New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	return a
}

func writeOrgFile(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(DataRoot(), "orgs", DefaultOrgID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOldFilesWithMissingFields(t *testing.T) {
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	writeOrgFile(t, "issues.json", `[{"id":"ISS-3","title":"舊的","createdBy":"tim","createdAt":"2026-01-01T00:00:00Z","comments":[{"author":"mcp","text":"hi"}]}]`)
	writeOrgFile(t, "cabinet.json", `[{"id":"DOC-2","title":"文件","section":"docs"}]`)
	writeOrgFile(t, "cabinet-layout.json", `[{"id":"docs","label":"文件"}]`)
	a := New()
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.LoadOrgData()

	is := a.Issues()
	if len(is) != 1 || is[0].Status != "open" || is[0].Priority != "normal" || is[0].Labels == nil || is[0].Comments[0].ID == "" {
		t.Fatalf("Issue 預設值不對:%+v", is)
	}
	d := a.FindDoc("doc-2")
	if d == nil || d.Author != MCPAuthor || d.Tags == nil || !d.UpdatedAt.Equal(d.CreatedAt.Time) {
		t.Fatalf("文件預設值不對:%+v", d)
	}
	if l := a.CabinetLayout(); len(l) != 1 || l[0].Tabs == nil {
		t.Fatalf("結構不對:%+v", l)
	}
	// 新的編號接在舊的後面
	if n := a.CreateIssue("新", "", "", nil, "", "", "tim"); n.ID != "ISS-4" {
		t.Fatalf("編號 %s", n.ID)
	}
}

func TestUnreadableFilesAreKept(t *testing.T) {
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	writeOrgFile(t, "cabinet.json", `{壞掉`)
	a := New()
	a.Mu.Lock()
	a.LoadOrgData()
	docs := a.CabinetDocs()
	a.Mu.Unlock()
	if len(docs) != 0 {
		t.Fatal("讀不出來的文件櫃應該是空的")
	}
	entries, _ := os.ReadDir(filepath.Join(DataRoot(), "orgs", DefaultOrgID))
	var kept []string
	for _, e := range entries {
		if strings.Contains(e.Name(), "unreadable") {
			kept = append(kept, e.Name())
		}
	}
	if len(kept) != 1 {
		t.Fatalf("讀不出來的檔案要改名保留:%v", kept)
	}
	if Exists(filepath.Join(DataRoot(), "orgs", DefaultOrgID, "cabinet.json")) {
		t.Fatal("cabinet.json 應該被改名")
	}
}

// 沒有結構檔時用研發基地的結構
func TestDefaultCabinetLayout(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.Name(MCPActor) != "MCP 用戶端" || a.Name(AI("zzz")) != "AI" {
		t.Fatal("名稱沒有登記到 Name")
	}
	if len(a.CabinetLayout()) != 8 {
		t.Fatal("沒有結構檔時用研發基地的結構")
	}
}

func TestOrgCreateAndSwitch(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.CreateIssue("預設組織的 Issue", "", "high", nil, "", "", "tim")
	a.SaveCabinetLayout(append(TemplateCabinetLayout(), CabinetSection{ID: "extra", Label: "額外", Tabs: []CabinetTab{}}))
	a.CreateDoc("文件", "", "docs", nil, nil, "tim")

	blank := a.CreateOrg("空白", "", "", "blank")
	cp := a.CreateOrg("複製", "", "", "copyCurrent")

	a.SwitchOrg(blank.ID)
	if len(a.Agents()) != 0 || len(a.AgentTasks()) != 0 || len(a.Issues()) != 0 || len(a.CabinetDocs()) != 0 {
		t.Fatal("空白組織應該沒有角色、任務、Issue、文件")
	}
	if l := a.CabinetLayout(); len(l) != 2 || l[0].ID != "overview" || l[1].ID != "docs" {
		t.Fatalf("空白組織的文件櫃結構不對:%+v", l)
	}

	a.SwitchOrg(cp.ID)
	if len(a.Agents()) != 5 || len(a.AgentTasks()) != 0 || len(a.Issues()) != 0 || len(a.CabinetDocs()) != 0 {
		t.Fatalf("複製組織:角色 %d 任務 %d Issue %d", len(a.Agents()), len(a.AgentTasks()), len(a.Issues()))
	}
	for _, g := range a.Agents() {
		for _, r := range g.Routines {
			if r.LastRunAt != nil {
				t.Fatal("複製的例行任務要重置上次執行時間")
			}
		}
	}
	if a.FindAgent("narrator").DocAccess["levels"] != "read" {
		t.Fatal("複製角色要帶文件存取範圍")
	}
	if l := a.CabinetLayout(); len(l) != 9 || l[8].ID != "extra" {
		t.Fatal("複製組織要帶文件櫃結構")
	}

	a.SwitchOrg(DefaultOrgID)
	if len(a.Issues()) != 1 || len(a.AgentTasks()) != 0 || len(a.CabinetDocs()) != 1 {
		t.Fatal("切回預設組織,資料要還在")
	}
}

func TestNextRun(t *testing.T) {
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.Local) // 週三
	r := AgentRoutine{Kind: "daily", Hour: 9, Minute: 0, Weekday: 2, EveryHours: 6, CreatedAt: model.Time{Time: base}}
	if got := r.NextRun(); !got.Equal(time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local)) {
		t.Fatalf("daily %v", got)
	}
	r.Hour = 11
	if got := r.NextRun(); !got.Equal(time.Date(2026, 11, 11, 11, 0, 0, 0, time.Local)) {
		t.Fatalf("daily 同一天 %v", got)
	}
	r.Kind, r.Weekday = "weekly", 2 // 週一
	if got := r.NextRun(); !got.Equal(time.Date(2026, 11, 16, 11, 0, 0, 0, time.Local)) {
		t.Fatalf("weekly %v", got)
	}
	r.Kind = "hourly"
	if got := r.NextRun(); !got.Equal(base.Add(6 * time.Hour)) {
		t.Fatalf("hourly %v", got)
	}
	if r.ScheduleLabel() != "每 6 小時" {
		t.Fatal(r.ScheduleLabel())
	}
	r.Kind, r.Hour, r.Minute, r.Weekday = "weekly", 9, 5, 6
	if r.ScheduleLabel() != "每週五 09:05" {
		t.Fatal(r.ScheduleLabel())
	}
}

// 例行任務排程在核心的 service(RunDueRoutines):到時間排一筆、上一筆沒做完不重複排、停用的角色不排
func TestRunDueRoutines(t *testing.T) {
	a := newTestApp(t)
	l := localCore()
	a.Mu.Lock()
	org := a.CurrentOrgID()
	n := len(a.Agents())
	a.Mu.Unlock()
	if n != 5 {
		t.Fatalf("預設角色 %d", n)
	}
	count := func() int { return len(a.TasksOf(org)) }
	if got, err := l.svc.RunDueRoutines(time.Now()); err != nil || len(got) != 0 {
		t.Fatalf("剛建立的例行任務還沒到時間:%v %v", got, err)
	}
	later := time.Now().Add(8 * 24 * time.Hour)
	if got, err := l.svc.RunDueRoutines(later); err != nil || len(got) != 5 {
		t.Fatalf("一週後每個角色都要排一筆,得到 %d %v", len(got), err)
	}
	a.agentRefresh(org)
	for _, x := range a.TasksOf(org) {
		if x.CreatedBy != RoutineAuthor || x.RoutineID == nil {
			t.Fatalf("例行任務的建立者不對:%+v", x)
		}
	}
	if got, _ := l.svc.RunDueRoutines(later.Add(8 * 24 * time.Hour)); len(got) != 0 {
		t.Fatalf("上一筆沒做完不重複排,得到 %d", len(got))
	}
	// 停用的角色不排
	g := *a.FindAgentIn(org, "narrator")
	g.Active = false
	if _, err := a.AgentOps().SaveAgent(org, g); err != nil {
		t.Fatal(err)
	}
	for _, x := range a.TasksOf(org) {
		done := "done"
		if _, err := a.AgentOps().ReportTask(org, x.ID, service.TaskReport{Status: &done}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := l.svc.RunDueRoutines(later.Add(16 * 24 * time.Hour)); len(got) != 4 {
		t.Fatalf("做完後再排(停用的不排),得到 %d", len(got))
	}
	a.agentRefresh(org)
	if count() != 9 {
		t.Fatalf("共 %d", count())
	}
}

func TestTaskFlowAndDelete(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	org := a.CurrentOrgID()
	_ = a.Agents() // 匯入預設角色
	a.Mu.Unlock()
	ops := a.AgentOps()
	t1, err := ops.AssignTask(org, service.TaskInput{AgentID: "narrator", Title: "一般", Author: MCPAuthor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ops.AssignTask(org, service.TaskInput{AgentID: "narrator", Title: "緊急", Priority: "urgent", Author: MCPAuthor}); err != nil {
		t.Fatal(err)
	}
	res, err := ops.ClaimTask(org, service.ClaimInput{AgentID: "narrator"})
	if err != nil || res.Task == nil || res.Task.Title != "緊急" || res.Task.ClaimedAt == nil || res.Task.Status != "inProgress" || res.Credential == nil {
		t.Fatalf("要先領緊急的:%+v %v", res, err)
	}
	a.Mu.Lock()
	base := testBadges(a)["automation"] // 範例資料的待核准也算在這頁
	a.Mu.Unlock()
	st := "blocked"
	if _, err := ops.ReportTask(org, res.Task.ID, service.TaskReport{Status: &st}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	n := testBadges(a)["automation"]
	a.Mu.Unlock()
	if n != base+1 {
		t.Fatal("blocked 要算進 AI 自動化徽章")
	}
	if err := ops.DeleteAgent(org, "narrator"); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	x := a.FindTask(t1.ID)
	name := a.Name(AI("narrator"))
	a.Mu.Unlock()
	if x.Status != "canceled" {
		t.Fatal("刪除角色要取消未完成的任務")
	}
	if _, err := ops.ReportTask(org, t1.ID, service.TaskReport{}); err == nil || err.Error() != t1.ID+" 已取消" {
		t.Fatalf("已取消的任務不能回報:%v", err)
	}
	if name != "AI" {
		t.Fatal("刪掉的角色不再有名稱")
	}
}

func TestIssueBadgeAndDelete(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.CreateIssue("a", "", "urgent", nil, "", "", "tim")
	a.CreateIssue("b", "", "high", nil, "", "", "tim")
	a.CreateIssue("c", "", "normal", nil, "", "", "tim")
	base := testBadges(a)["development"] - 2
	a.UpdateIssue("ISS-1", "tim", func(i *OpsIssue) { i.Status = "closed" })
	if testBadges(a)["development"] != base+1 || a.Issue("ISS-1").ClosedAt == nil {
		t.Fatal("關閉後不算")
	}
	a.DeleteIssue("ISS-2")
	if a.Issue("ISS-2") != nil || len(a.Issues()) != 2 {
		t.Fatal("刪除")
	}
}

func TestCabinetValidate(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	a.CreateDoc("L0 文件", "", "levels", cabSP("/studio/tools"), nil, "tim")
	cases := map[string][]CabinetSection{
		"區塊名稱不能是空的":               {{ID: "a", Label: " "}},
		"區塊 id 重複:a":              {{ID: "a", Label: "A"}, {ID: "a", Label: "B"}},
		"「A」有頁籤沒有名稱":              {{ID: "a", Label: "A", Tabs: []CabinetTab{{Path: "/x", Label: ""}}}},
		"頁籤 id 要用 / 開頭:x":         {{ID: "a", Label: "A", Tabs: []CabinetTab{{Path: "x", Label: "X"}}}},
		"頁籤 id 重複:/x":             {{ID: "a", Label: "A", Tabs: []CabinetTab{{Path: "/x", Label: "X"}, {Path: "/x", Label: "Y"}}}},
		"「L0 文件」還在被拿掉的區塊裡,先搬走或刪除": {{ID: "a", Label: "A"}},
		"「L0 文件」還在被拿掉的頁籤裡,先搬走或刪除": {{ID: "levels", Label: "課程"}},
	}
	for want, layout := range cases {
		if got := a.ValidateCabinetLayout(layout); got != want {
			t.Errorf("想要「%s」,得到「%s」", want, got)
		}
	}
	if got := a.ValidateCabinetLayout(TemplateCabinetLayout()); got != "" {
		t.Fatal(got)
	}
	a.Mu.Unlock()
	if _, err := Action(a, "cabinet_doc_delete", map[string]any{"id": "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestActions(t *testing.T) {
	a := newTestApp(t)
	out, err := Action(a, "agent_upsert", map[string]any{"name": "新角色", "routines": []any{
		map[string]any{"title": "保留"}, map[string]any{"title": "  "},
	}})
	if err != nil {
		t.Fatal(err)
	}
	g := out.(*Agent)
	if g.ID != "agent" || len(g.Routines) != 1 || g.Routines[0].ID == "" {
		t.Fatalf("新增角色:%+v", g)
	}
	if _, err := Action(a, "agent_routine_run", map[string]any{"agent": g.ID, "routine": g.Routines[0].ID}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	task := a.AgentTasks()[0]
	a.Mu.Unlock()
	if task.CreatedBy != "tim" || task.Title != "保留" {
		t.Fatalf("現在排一筆:%+v", task)
	}
	p, _ := Action(a, "agent_prompt", map[string]any{"agent": g.ID, "task": task.ID})
	if !strings.Contains(p.(map[string]any)["text"].(string), "處理任務 "+task.ID) {
		t.Fatal(p)
	}
	if _, err := Action(a, "agent_task_cancel", map[string]any{"id": task.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "agent_task_requeue", map[string]any{"id": task.ID}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	x := a.FindTask(task.ID)
	a.Mu.Unlock()
	if x.Status != "queued" || x.CompletedAt != nil {
		t.Fatalf("重新排入:%+v", x)
	}
	if _, err := Action(a, "agent_delete", map[string]any{"id": g.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "cabinet_layout_save", map[string]any{"layout": []any{map[string]any{"id": "a", "label": ""}}}); err == nil {
		t.Fatal("不合格的結構要擋下")
	}
	if _, err := Action(a, "cabinet_layout_save", map[string]any{"layout": []any{map[string]any{"id": "a", "label": "A"}}}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	a.LoadOrgData()
	l := a.CabinetLayout()
	a.Mu.Unlock()
	if len(l) != 1 || l[0].ID != "a" {
		t.Fatalf("結構要存檔:%+v", l)
	}
}

// testBadges 徽章數字(呼叫時拿著 Mu)
func testBadges(a *App) map[string]int {
	out := map[string]int{}
	for _, f := range badgeFuncs {
		for k, v := range f(a) {
			out[k] += v
		}
	}
	return out
}

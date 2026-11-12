package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	writeOrgFile(t, "agents.json", `{"agents":[{"id":"old","name":"舊角色","routines":[{"title":"r"}]}],
		"tasks":[{"id":"T-7","title":"t","agentId":"old"}]}`)
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
	g := a.FindAgent("old")
	if g == nil || g.Symbol != "person.crop.circle" || g.Color != "blue" || !g.Active || g.DocAccess == nil {
		t.Fatalf("角色預設值不對:%+v", g)
	}
	r := g.Routines[0]
	if r.Kind != "daily" || r.Hour != 9 || r.Weekday != 2 || r.EveryHours != 6 || !r.Enabled || r.Priority != "normal" || r.ID == "" {
		t.Fatalf("例行任務預設值不對:%+v", r)
	}
	task := a.FindTask("t-7")
	if task == nil || task.Status != "queued" || task.Links == nil || task.Notes == nil {
		t.Fatalf("任務預設值不對:%+v", task)
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
	if n := a.AssignTask("old", "x", "", "", "tim", nil); n == nil || n.ID != "T-8" {
		t.Fatalf("任務編號 %+v", n)
	}
}

func TestUnreadableFilesAreKept(t *testing.T) {
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	writeOrgFile(t, "cabinet.json", `{壞掉`)
	writeOrgFile(t, "agents.json", `[1,2`)
	a := New()
	a.Mu.Lock()
	a.LoadOrgData()
	docs, agents := a.CabinetDocs(), a.Agents()
	a.Mu.Unlock()
	if len(docs) != 0 {
		t.Fatal("讀不出來的文件櫃應該是空的")
	}
	if len(agents) != 5 {
		t.Fatalf("讀不出來的 agents.json 應該換成預設角色,得到 %d 個", len(agents))
	}
	entries, _ := os.ReadDir(filepath.Join(DataRoot(), "orgs", DefaultOrgID))
	var kept []string
	for _, e := range entries {
		if strings.Contains(e.Name(), "unreadable") {
			kept = append(kept, e.Name())
		}
	}
	if len(kept) != 2 {
		t.Fatalf("讀不出來的檔案要改名保留:%v", kept)
	}
	if Exists(filepath.Join(DataRoot(), "orgs", DefaultOrgID, "cabinet.json")) {
		t.Fatal("cabinet.json 應該被改名")
	}
}

func TestDefaultsWrittenOnFirstStart(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if !Exists(filepath.Join(a.DataDir(), "agents.json")) {
		t.Fatal("第一次啟動要寫預設角色")
	}
	g := a.FindAgent("訪客分析師")
	if g == nil || g.DocAccess["members"] != "write" || len(g.Routines) != 1 {
		t.Fatalf("預設角色不對:%+v", g)
	}
	if a.Name(AI("narrator")) != "旁白製作" || a.Name(MCPActor) != "MCP 用戶端" || a.Name(AI("zzz")) != "AI" {
		t.Fatal("角色名稱沒有登記到 Name")
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
	a.AssignTask("narrator", "任務", "", "", "tim", nil)
	// 讓例行任務有上次執行時間,複製時要重置
	a.RunRoutineNow("narrator", a.FindAgent("narrator").Routines[0].ID)
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
	if len(a.Issues()) != 1 || len(a.AgentTasks()) != 2 || len(a.CabinetDocs()) != 1 {
		t.Fatal("切回預設組織,資料要還在")
	}
}

func TestNextRun(t *testing.T) {
	base := time.Date(2026, 11, 11, 10, 0, 0, 0, time.Local) // 週三
	r := AgentRoutine{Kind: "daily", Hour: 9, Minute: 0, Weekday: 2, EveryHours: 6, CreatedAt: Time{base}}
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

func TestRunDueRoutines(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.RunDueRoutines(time.Now())
	if len(a.AgentTasks()) != 0 {
		t.Fatal("剛建立的例行任務還沒到時間")
	}
	later := time.Now().Add(8 * 24 * time.Hour)
	a.RunDueRoutines(later)
	if n := len(a.AgentTasks()); n != 5 {
		t.Fatalf("一週後每個角色都要排一筆,得到 %d", n)
	}
	for _, x := range a.AgentTasks() {
		if x.CreatedBy != RoutineAuthor || x.RoutineID == nil {
			t.Fatalf("例行任務的建立者不對:%+v", x)
		}
	}
	a.RunDueRoutines(later.Add(8 * 24 * time.Hour))
	if n := len(a.AgentTasks()); n != 5 {
		t.Fatalf("上一筆沒做完不重複排,得到 %d", n)
	}
	// 停用的角色不排
	g := *a.FindAgent("narrator")
	g.Active = false
	a.UpsertAgent(g)
	for _, x := range a.AgentTasks() {
		a.UpdateTask(x.ID, func(t *AgentTask) { t.Status = "done" })
	}
	a.RunDueRoutines(later.Add(16 * 24 * time.Hour))
	if n := len(a.AgentTasks()); n != 9 {
		t.Fatalf("做完後再排(停用的不排),得到 %d", n)
	}
	// 重新讀檔還在
	a.LoadOrgData()
	if n := len(a.AgentTasks()); n != 9 {
		t.Fatalf("重新讀檔後 %d", n)
	}
}

func TestTaskFlowAndDelete(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	t1 := a.AssignTask("narrator", "一般", "", "", MCPAuthor, nil)
	a.AssignTask("narrator", "緊急", "", "urgent", MCPAuthor, nil)
	got, err := a.ClaimTask("narrator", nil)
	if err != nil || got.Title != "緊急" || got.ClaimedAt == nil || got.Status != "inProgress" {
		t.Fatalf("要先領緊急的:%+v %v", got, err)
	}
	base := testBadges(a)["automation"] // 範例資料的待核准也算在這頁
	st := "blocked"
	a.ReportTask(got.ID, &st, nil, nil, nil)
	if testBadges(a)["automation"] != base+1 {
		t.Fatal("blocked 要算進 AI 自動化徽章")
	}
	a.DeleteAgent("narrator")
	if x := a.FindTask(t1.ID); x.Status != "canceled" {
		t.Fatal("刪除角色要取消未完成的任務")
	}
	if _, err := a.ReportTask(t1.ID, nil, nil, nil, nil); err == nil || err.Error() != t1.ID+" 已取消" {
		t.Fatalf("已取消的任務不能回報:%v", err)
	}
	if a.Name(AI("narrator")) != "AI" {
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
	g := out.(Agent)
	if g.ID != "agent-6" || len(g.Routines) != 1 || g.Routines[0].ID == "" {
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

package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

func taskStatuses(t *testing.T, st *store.PG) map[string]string {
	t.Helper()
	all, err := st.ListAgentTasks("o1")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, x := range all {
		out[x.ID] = x.Status
	}
	return out
}

// ISS-43:關閉 Issue 取消指派角色還沒領取的任務;進行中、子項產生的任務不動;重新打開不重排
func TestCloseIssueCancelsQueuedTasks(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	is, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "要做的", Assignee: "cto"}) // 自動排一筆
	if err != nil {
		t.Fatal(err)
	}
	var auto string
	for id := range taskStatuses(t, st) {
		auto = id
	}
	link := func(agent string) string {
		tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: agent, Title: "手動", Issue: is.Key})
		if err != nil {
			t.Fatal(err)
		}
		return tk.ID
	}
	running, others := link("cto"), link("pm")
	if _, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: running}); err != nil {
		t.Fatal(err)
	}
	// 子項產生、又退回待領取的任務
	it, err := svc.AddItem(boss, "o1", is.ID, service.ItemInput{Title: "子項", Assignee: "agent:cto"})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := svc.ClaimItem(boss, "o1", is.ID, it.Key, service.ItemClaimInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseTask(boss, "o1", cl.Task.ID, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.UpdateIssue(boss, "o1", is.ID, service.IssuePatch{Status: sp("closed")}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{auto: "canceled", running: "inProgress", others: "queued", cl.Task.ID: "queued"}
	check := func(when string) {
		t.Helper()
		got := taskStatuses(t, st)
		for id, w := range want {
			if got[id] != w {
				t.Errorf("%s:%s 要是 %s,得到 %s", when, id, w, got[id])
			}
		}
		if len(got) != len(want) {
			t.Errorf("%s:任務數 %d,要 %d", when, len(got), len(want))
		}
	}
	check("關閉後")
	if _, err := svc.UpdateIssue(boss, "o1", is.ID, service.IssuePatch{Status: sp("open")}); err != nil {
		t.Fatal(err)
	}
	check("重新打開後")
}

// ISS-43:任務完成時把結果貼回 Issue(只在變成 done 時、一則、不關閉);角色回報署名角色,人手動完成署名本人;子項任務不貼
func TestTaskDoneNotesIssue(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	is, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "要做的", Assignee: "cto"})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for k := range taskStatuses(t, st) {
		id = k
	}
	if task, _ := st.GetAgentTask("o1", id); !strings.Contains(task.Instructions, "不要再自己留一次") {
		t.Fatalf("任務說明:%s", task.Instructions)
	}
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: id})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AutoPostsResult { // runner 靠它決定提示詞
		t.Fatal("領取回應要明示 server 會自動貼結果")
	}
	cto, err := svc.AuthenticateAgent("cto", res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	comments := func() []model.Comment {
		i, err := svc.GetIssue(boss, "o1", is.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i.Status != "open" {
			t.Fatal("不能自動關閉")
		}
		return i.Comments
	}
	if _, err := svc.ReportTask(cto, "o1", "", service.TaskReport{Note: sp("進度")}); err != nil {
		t.Fatal(err)
	}
	if len(comments()) != 0 {
		t.Fatal("進度回報不留言")
	}
	done := "done"
	if _, err := svc.ReportTask(cto, "o1", "", service.TaskReport{Status: &done, Result: sp("修好了" + strings.Repeat("長", 5000))}); err != nil {
		t.Fatal(err)
	}
	c := comments()
	if len(c) != 1 || c[0].Author != "agent:cto" || !strings.Contains(c[0].Text, id) || !strings.Contains(c[0].Text, "修好了") || len([]rune(c[0].Text)) > 4100 {
		t.Fatalf("完成留言:%+v", c)
	}
	if _, err := svc.ReportTask(boss, "o1", id, service.TaskReport{Status: &done, Note: sp("再回一次")}); err != nil {
		t.Fatal(err)
	}
	if len(comments()) != 1 {
		t.Fatal("已經是 done 不再留言")
	}

	// 只給 note:貼 note
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "pm", Title: "只給 note", Issue: is.Key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReportTask(boss, "o1", tk.ID, service.TaskReport{Status: &done, Note: sp("備註結果")}); err != nil {
		t.Fatal(err)
	}
	if c := comments(); len(c) != 2 || c[1].Author != boss.User.Email || !strings.Contains(c[1].Text, "備註結果") {
		t.Fatalf("note 當結果:%+v", c)
	}

	// 子項產生的任務不貼
	it, err := svc.AddItem(boss, "o1", is.ID, service.ItemInput{Title: "子項", Assignee: "agent:cto"})
	if err != nil {
		t.Fatal(err)
	}
	cl, err := svc.ClaimItem(boss, "o1", is.ID, it.Key, service.ItemClaimInput{})
	if err != nil {
		t.Fatal(err)
	}
	before := len(comments())
	if _, err := svc.ReportTask(boss, "o1", cl.Task.ID, service.TaskReport{Status: &done, Result: sp("子項做完")}); err != nil {
		t.Fatal(err)
	}
	for _, x := range comments()[before:] {
		if strings.Contains(x.Text, "子項做完") {
			t.Fatalf("子項任務不貼結果:%+v", x)
		}
	}
}

// 完成留言的署名看是誰回報:人手動完成署名本人、沒填結果不留言;角色身分(含執行者代角色的 WorkerReport)署名角色,沒填也留言
func TestTaskDoneNoteAuthor(t *testing.T) {
	svc, _, boss, _, _ := chainEnv(t)
	is, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "要做的"})
	if err != nil {
		t.Fatal(err)
	}
	done := "done"
	for _, c := range []struct {
		name         string
		worker       bool
		earlier      *string // 完成前的進度回報先存的結果
		result, note *string
		author, text string // author 空 = 不留言
	}{
		{"人填結果", false, nil, sp("人做完了"), sp("備註"), boss.User.Email, "人做完了"},
		{"人只填備註", false, nil, nil, sp("只有備註"), boss.User.Email, "只有備註"},
		{"人都沒填", false, nil, nil, nil, "", ""},
		{"人只填空白", false, nil, sp("  "), nil, "", ""},
		{"先前存了結果、人沒填", false, sp("舊結果"), nil, nil, "", ""},
		{"先前存了結果、人只填備註", false, sp("舊結果"), nil, sp("本次備註"), boss.User.Email, "本次備註"},
		{"worker 代角色填結果", true, nil, sp("角色做完了"), nil, "agent:cto", "角色做完了"},
		{"worker 代角色沒填", true, nil, nil, nil, "agent:cto", "(沒有填結果)"},
		{"worker 代角色沿用先前結果", true, sp("舊結果"), nil, nil, "agent:cto", "舊結果"},
	} {
		tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "cto", Title: c.name, Issue: is.Key})
		if err != nil {
			t.Fatal(err)
		}
		before, err := svc.GetIssue(boss, "o1", is.ID)
		if err != nil {
			t.Fatal(err)
		}
		report := func(in service.TaskReport) error {
			_, err := svc.ReportTask(boss, "o1", tk.ID, in)
			return err
		}
		if c.worker {
			res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
			if err != nil {
				t.Fatal(err)
			}
			report = func(in service.TaskReport) error {
				_, err := svc.WorkerReport(boss, "o1", tk.ID, res.Credential.Code, in)
				return err
			}
		}
		if c.earlier != nil {
			if err := report(service.TaskReport{Result: c.earlier}); err != nil {
				t.Fatal(c.name, err)
			}
		}
		if err := report(service.TaskReport{Status: &done, Result: c.result, Note: c.note}); err != nil {
			t.Fatal(c.name, err)
		}
		after, err := svc.GetIssue(boss, "o1", is.ID)
		if err != nil {
			t.Fatal(err)
		}
		added := after.Comments[len(before.Comments):]
		switch {
		case c.author == "":
			if len(added) != 0 {
				t.Errorf("%s:不該留言:%+v", c.name, added)
			}
		case len(added) != 1 || added[0].Author != c.author || !strings.Contains(added[0].Text, c.text) ||
			(!c.worker && strings.Contains(added[0].Text, "舊結果")):
			t.Errorf("%s:要 %s 留一則含 %q:%+v", c.name, c.author, c.text, added)
		}
	}
}

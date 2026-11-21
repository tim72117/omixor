package service_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

func keysOf(l []model.Issue) string {
	out := ""
	for k, i := range l {
		if k > 0 {
			out += ","
		}
		out += i.Key
	}
	return out
}

// 軟刪除的權限:成員可以丟進垃圾桶;還原、永久刪除、垃圾桶列表、匯出含已刪除都要組織管理員;角色憑證身分不行
func TestIssueTrashPermissions(t *testing.T) {
	svc, root, boss, alice, bob := issueEnv(t, storetest.New(t))
	dev := agentP("dev")
	a, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteIssue(dev, "o1", a.Key); code(err) != 403 {
		t.Fatal("角色憑證身分不能刪", err)
	}
	if err := svc.DeleteIssue(bob, "o1", a.Key); err != nil {
		t.Fatal("成員可以丟進垃圾桶", err)
	}
	// 已刪除:讀、寫、留言、再刪都 404
	if _, err := svc.GetIssue(alice, "o1", a.Key); code(err) != 404 {
		t.Fatal("取得", err)
	}
	if _, err := svc.GetIssue(alice, "o1", a.ID); code(err) != 404 {
		t.Fatal("用 UUID 取得", err)
	}
	title := "x"
	if _, err := svc.UpdateIssue(alice, "o1", a.Key, service.IssuePatch{Title: &title}); code(err) != 404 {
		t.Fatal("修改", err)
	}
	if _, err := svc.AddIssueComment(alice, "o1", a.Key, service.CommentInput{Text: "x"}); code(err) != 404 {
		t.Fatal("留言", err)
	}
	if err := svc.DeleteIssue(alice, "o1", a.Key); code(err) != 404 {
		t.Fatal("再刪一次", err)
	}
	if l, _ := svc.ListIssues(alice, "o1"); len(l) != 0 {
		t.Fatal("列表看不到", l)
	}
	if l, _ := svc.QueryIssues(alice, "o1", model.IssueQuery{}); len(l) != 0 {
		t.Fatal("查詢看不到", l)
	}
	if b, err := svc.IssueBoard(alice, "o1", model.IssueQuery{}); err != nil || b.Matched != 0 {
		t.Fatal("看板看不到", b, err)
	}
	if l, err := svc.ExportIssues(boss, "o1"); err != nil || len(l) != 0 {
		t.Fatal("預設匯出不含已刪除", l, err)
	}

	// 成員不能還原、永久刪除、看垃圾桶、匯出含已刪除
	if _, err := svc.RestoreIssue(alice, "o1", a.Key); code(err) != 403 {
		t.Fatal("成員不能還原", err)
	}
	if err := svc.PurgeIssue(alice, "o1", a.Key); code(err) != 403 {
		t.Fatal("成員不能永久刪除", err)
	}
	if _, err := svc.ListDeletedIssues(alice, "o1"); code(err) != 403 {
		t.Fatal("成員不能看垃圾桶", err)
	}
	if _, err := svc.QueryIssues(alice, "o1", model.IssueQuery{Deleted: true}); code(err) != 403 {
		t.Fatal("成員不能用 Deleted 查詢", err)
	}
	if _, err := svc.ExportIssuesWith(alice, "o1", true); code(err) != 403 {
		t.Fatal("成員不能匯出含已刪除", err)
	}
	if _, err := svc.RestoreIssue(dev, "o1", a.Key); code(err) != 403 {
		t.Fatal("角色不能還原", err)
	}
	if err := svc.PurgeIssue(dev, "o1", a.Key); code(err) != 403 {
		t.Fatal("角色不能永久刪除", err)
	}
	if _, err := svc.ListDeletedIssues(dev, "o1"); code(err) != 403 {
		t.Fatal("角色不能看垃圾桶", err)
	}

	// 組織管理員:看得到、匯出含已刪除、還原;伺服器管理員也可以
	if l, err := svc.ListDeletedIssues(boss, "o1"); err != nil || keysOf(l) != "ISS-1" || l[0].DeletedAt == nil || l[0].DeletedBy != "bob@x" {
		t.Fatalf("垃圾桶 %+v %v", l, err)
	}
	if l, err := svc.QueryIssues(boss, "o1", model.IssueQuery{Deleted: true}); err != nil || keysOf(l) != "ISS-1" {
		t.Fatal(l, err)
	}
	if l, err := svc.ExportIssuesWith(boss, "o1", true); err != nil || len(l) != 1 {
		t.Fatal("含已刪除", l, err)
	}
	r, err := svc.RestoreIssue(boss, "o1", a.Key)
	if err != nil || r.DeletedAt != nil || r.Key != "ISS-1" {
		t.Fatal(r, err)
	}
	if _, err := svc.GetIssue(alice, "o1", a.Key); err != nil {
		t.Fatal("還原後看得到", err)
	}
	if _, err := svc.RestoreIssue(boss, "o1", a.Key); code(err) != 404 {
		t.Fatal("不在垃圾桶裡 404", err)
	}
	_ = svc.DeleteIssue(alice, "o1", a.Key)
	if err := svc.PurgeIssue(root, "o1", a.Key); err != nil {
		t.Fatal("伺服器管理員可以永久刪除", err)
	}
	if l, _ := svc.ListDeletedIssues(boss, "o1"); len(l) != 0 {
		t.Fatal("永久刪除後垃圾桶是空的", l)
	}
	if _, err := svc.RestoreIssue(boss, "o1", a.Key); code(err) != 404 {
		t.Fatal("永久刪除後不能還原", err)
	}
}

// 刪除時指派給角色、還沒領取的任務一起取消;已領取(進行中)的不動
func TestIssueTrashCancelsQueuedTasks(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "ops", Name: "維運", Active: true}); err != nil {
		t.Fatal(err)
	}
	queued, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "排隊中", Assignee: "dev"})
	taken, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "已領取", Assignee: "ops"})
	status := func(link string) string {
		l, err := svc.ListAgentTasks(boss, "o1", service.TaskFilter{Status: "all"})
		if err != nil {
			t.Fatal(err)
		}
		for _, tk := range l {
			for _, k := range tk.Links {
				if k == link {
					return tk.Status
				}
			}
		}
		return "(沒有)"
	}
	if status(queued.ID) != "queued" || status(taken.ID) != "queued" {
		t.Fatal("指派要自動排任務", status(queued.ID), status(taken.ID))
	}
	if _, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "ops"}); err != nil {
		t.Fatal(err)
	}
	if status(taken.ID) != "inProgress" {
		t.Fatal("ops 的任務要是進行中", status(taken.ID))
	}
	if err := svc.DeleteIssue(alice, "o1", queued.Key); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteIssue(alice, "o1", taken.Key); err != nil {
		t.Fatal(err)
	}
	if got := status(queued.ID); got != "canceled" {
		t.Fatal("還沒領取的任務要取消:", got)
	}
	if got := status(taken.ID); got != "inProgress" {
		t.Fatal("已領取的不動:", got)
	}
}

// key 與號碼不回收:刪除(含永久刪除)後再建,號碼繼續往後
func TestIssueKeyNotRecycled(t *testing.T) {
	svc, _, boss, alice, _ := issueEnv(t, storetest.New(t))
	a, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "1"})
	b, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "2"})
	if a.Key != "ISS-1" || b.Key != "ISS-2" || a.Num != 1 || b.Num != 2 {
		t.Fatal(a.Key, b.Key)
	}
	_ = svc.DeleteIssue(alice, "o1", b.Key)
	c, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "3"})
	if c.Key != "ISS-3" {
		t.Fatal("軟刪除後號碼不回收:", c.Key)
	}
	if err := svc.PurgeIssue(boss, "o1", b.Key); err != nil {
		t.Fatal(err)
	}
	_ = svc.DeleteIssue(alice, "o1", c.Key)
	_ = svc.PurgeIssue(boss, "o1", c.Key)
	d, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "4"})
	if d.Key != "ISS-4" || d.ID == b.ID || d.ID == c.ID {
		t.Fatal("永久刪除後號碼也不回收:", d.Key)
	}
}

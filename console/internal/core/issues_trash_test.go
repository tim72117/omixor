package core

import (
	"errors"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 核心回 403(不是組織管理員)時保留狀態碼;其他錯誤是 UserError
func TestTrashErrKeeps403(t *testing.T) {
	err := trashErr(&service.Error{Code: 403, Msg: "只有組織管理員可以這樣做"})
	var se StatusError
	if !errors.As(err, &se) || se.Status != 403 || se.Message == "" {
		t.Fatalf("要是 403 的 StatusError:%#v", err)
	}
	if err := trashErr(&service.Error{Code: 404, Msg: "找不到"}); !errors.As(err, &se) || se.Status != 404 {
		t.Fatalf("404 要保留狀態碼:%#v", err)
	}
	if err := trashErr(&service.Error{Code: 409, Msg: "衝突"}); !errors.As(err, &se) || se.Status != 409 {
		t.Fatalf("409 要保留狀態碼:%#v", err)
	}
	if _, ok := trashErr(&service.Error{Code: 400, Msg: "壞"}).(UserError); !ok {
		t.Fatal("400 是 UserError")
	}
	if trashErr(nil) != nil {
		t.Fatal("nil 原樣回傳")
	}
}

// issue_query 的 counts 依 project 計算;軟刪除後清單移除、垃圾桶 / 還原 / 永久刪除
func TestIssueQueryProjectCountsAndTrash(t *testing.T) {
	a := newTestApp(t)
	org := a.currentOrgNow()
	pr, err := a.CabinetOps().CreateProject(org, service.ProjectInput{Name: "網站", Key: "WEB"})
	if err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	in := a.CreateIssueIn(service.IssueInput{Title: "專案內", Author: "mcp", Project: &pr.ID})
	top := a.CreateIssueFor("組織層級", "", "", nil, "", "", "mcp", "", "")
	a.UpdateIssue(top.ID, "mcp", func(i *model.Issue) { i.Status = "closed" })
	a.Mu.Unlock()
	if in.Key != "WEB-1" {
		t.Fatalf("key:%s %v", in.Key, a.IssueRemoteError())
	}
	q := func(project string) IssueQueryResult {
		out, err := Action(a, "issue_query", map[string]any{"project": project})
		if err != nil {
			t.Fatal(err)
		}
		return out.(IssueQueryResult)
	}
	if r := q(pr.ID); len(r.Issues) != 1 || r.Counts["all"] != 1 || r.Counts["open"] != 1 || r.Counts["closed"] != 0 {
		t.Fatalf("專案:%+v", r)
	}
	if r := q("-"); len(r.Issues) != 1 || r.Counts["all"] != 1 || r.Counts["closed"] != 1 {
		t.Fatalf("組織層級:%+v", r)
	}
	if r := q(""); r.Counts["all"] != 2 {
		t.Fatalf("全部:%+v", r)
	}

	if _, err := Action(a, "issue_delete", map[string]any{"id": in.ID}); err != nil {
		t.Fatal(err)
	}
	if r := q(pr.ID); len(r.Issues) != 0 {
		t.Fatalf("軟刪除後清單要移除:%+v", r)
	}
	out, err := Action(a, "issue_trash", map[string]any{})
	if l := out.([]model.Issue); err != nil || len(l) != 1 || l[0].Key != "WEB-1" || l[0].DeletedAt == nil {
		t.Fatalf("垃圾桶:%v %v", out, err)
	}
	if _, err := Action(a, "issue_restore", map[string]any{"id": in.ID}); err != nil {
		t.Fatal(err)
	}
	if r := q(pr.ID); len(r.Issues) != 1 {
		t.Fatalf("還原後要回來:%+v", r)
	}
	Action(a, "issue_delete", map[string]any{"id": in.ID})
	if _, err := Action(a, "issue_purge", map[string]any{"id": in.ID}); err != nil {
		t.Fatal(err)
	}
	out, _ = Action(a, "issue_trash", map[string]any{})
	if len(out.([]model.Issue)) != 0 || len(q(pr.ID).Issues) != 0 {
		t.Fatal("永久刪除後不見")
	}
}

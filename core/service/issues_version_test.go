package service_test

import (
	"sync"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

func iptr(n int) *int { return &n }

// 版本檢查:Version>0 且不符回 409(Data 帶 current、currentVersion);沒帶照舊套用;留言不增加 Version;改本體才 +1
func TestIssueVersionCheck(t *testing.T) {
	svc, _, boss, alice, bob := issueEnv(t, storetest.New(t))
	i, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	if err != nil || i.Version != 1 {
		t.Fatalf("新 Issue 的 Version 是 1:%+v %v", i, err)
	}
	// 沒帶版本:照舊套用,版本 +1
	u, err := svc.UpdateIssue(alice, "o1", i.Key, service.IssuePatch{Title: sp("t2")})
	if err != nil || u.Version != 2 {
		t.Fatal("沒帶版本照舊套用", u, err)
	}
	// 帶對的版本
	u, err = svc.UpdateIssue(alice, "o1", i.Key, service.IssuePatch{Title: sp("t3"), Version: iptr(2)})
	if err != nil || u.Version != 3 {
		t.Fatal(u, err)
	}
	// 帶舊版本:409,Data 有 current、currentVersion;內容不變
	_, err = svc.UpdateIssue(bob, "o1", i.Key, service.IssuePatch{Title: sp("蓋掉"), Version: iptr(2)})
	if code(err) != 409 {
		t.Fatal("舊版本要 409", err)
	}
	se, ok := err.(*service.Error)
	if !ok {
		t.Fatalf("要是 *service.Error:%T", err)
	}
	cur, _ := se.Data["current"].(*model.Issue)
	if se.Data["currentVersion"] != int64(3) || cur == nil || cur.Title != "t3" || cur.Version != 3 {
		t.Fatalf("Data 要帶 current 與 currentVersion:%+v", se.Data)
	}
	if g, _ := svc.GetIssue(alice, "o1", i.Key); g.Title != "t3" || g.Version != 3 {
		t.Fatal("409 不能改到內容", g)
	}
	// 版本比目前還新也不符
	if _, err := svc.UpdateIssue(bob, "o1", i.Key, service.IssuePatch{Title: sp("x"), Version: iptr(99)}); code(err) != 409 {
		t.Fatal(err)
	}
	// 新增留言不增加 Version(兩種路徑)
	if _, err := svc.AddIssueComment(bob, "o1", i.Key, service.CommentInput{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	g, _ := svc.GetIssue(alice, "o1", i.Key)
	if g.Version != 3 || len(g.Comments) != 1 {
		t.Fatal("AddIssueComment 不增加 Version", g.Version)
	}
	// 沒有實際改到東西:不增加
	if u, err = svc.UpdateIssue(bob, "o1", i.Key, service.IssuePatch{Title: sp("t3"), Version: iptr(3)}); err != nil || u.Version != 3 {
		t.Fatal("沒改到東西不增加 Version", u, err)
	}

	// 改本體的每個欄位都 +1(內文、狀態、優先度、標籤、repo、指派、專案)
	prj, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "App", Key: "APP"})
	if err != nil {
		t.Fatal(err)
	}
	v := 3
	step := func(name string, p service.IssuePatch) {
		t.Helper()
		got, err := svc.UpdateIssue(alice, "o1", i.Key, p)
		if err != nil {
			t.Fatal(name, err)
		}
		v++
		if got.Version != v {
			t.Fatalf("%s:Version 應該 %d,得到 %d", name, v, got.Version)
		}
		if g, _ := svc.GetIssue(alice, "o1", i.Key); g.Version != v {
			t.Fatalf("%s:存起來的 Version 應該 %d,得到 %d", name, v, g.Version)
		}
	}
	step("內文", service.IssuePatch{Body: sp("b")})
	step("狀態", service.IssuePatch{Status: sp("closed")})
	step("優先度", service.IssuePatch{Priority: sp("high")})
	step("標籤", service.IssuePatch{Labels: &[]string{"x"}})
	step("repo", service.IssuePatch{Repo: sp("github:acme/web")})
	step("指派", service.IssuePatch{Assignee: sp("dev")})
	step("專案", service.IssuePatch{Project: sp(prj.ID)})
	if err := svc.DeleteIssue(alice, "o1", i.Key); err != nil {
		t.Fatal(err)
	}
	v++
	r, err := svc.RestoreIssue(boss, "o1", i.Key)
	if err != nil {
		t.Fatal(err)
	}
	v++
	if r.Version != v {
		t.Fatalf("刪除與還原各 +1:應該 %d,得到 %d", v, r.Version)
	}
}

// 同一個版本的兩個修改同時進來:只有一個成功,其餘 409
func TestIssueVersionConcurrentSameVersion(t *testing.T) {
	svc, _, _, alice, bob := issueEnv(t, storetest.New(t))
	i, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	const n = 8
	var wg sync.WaitGroup
	res := make(chan error, n)
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			who := alice
			if k%2 == 1 {
				who = bob
			}
			title := string(rune('A' + k))
			_, err := svc.UpdateIssue(who, "o1", i.Key, service.IssuePatch{Title: &title, Version: iptr(1)})
			res <- err
		}()
	}
	wg.Wait()
	close(res)
	ok, conflict := 0, 0
	for err := range res {
		switch code(err) {
		case 0:
			ok++
		case 409:
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("同一版本只能有一個成功:成功 %d、衝突 %d", ok, conflict)
	}
	if g, _ := svc.GetIssue(alice, "o1", i.Key); g.Version != 2 {
		t.Fatal(g.Version)
	}
}

// 經 UpdateIssue 帶整份留言(只新增留言)也不增加 Version(目前會 +1:疑似缺陷,見回報)
func TestIssueCommentViaUpdateKeepsVersion(t *testing.T) {
	svc, _, _, alice, bob := issueEnv(t, storetest.New(t))
	i, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	cs := []model.Comment{{Text: "經 UpdateIssue 新增"}}
	u, err := svc.UpdateIssue(bob, "o1", i.Key, service.IssuePatch{Comments: &cs, Version: iptr(1)})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := svc.GetIssue(alice, "o1", i.Key); g.Version != 1 || len(g.Comments) != 1 {
		t.Fatal("存起來的 Version 不能增加:", g.Version, len(g.Comments))
	}
	if u.Version != 1 {
		t.Fatal("回應的 Version 要等於存起來的(service.patchIssue 的 n.Version = cur.Version+1 沒排除只改留言):", u.Version)
	}
}

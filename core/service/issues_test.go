package service_test

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// countStore 記下 Issue 寫入走哪一條路(AddIssueComment 或 UpdateIssue 帶整份留言)
type countStore struct {
	store.Store
	adds, fullWrites atomic.Int32
}

func (c *countStore) AddIssueComment(org, id string, cm model.Comment) (*model.Issue, error) {
	c.adds.Add(1)
	return c.Store.AddIssueComment(org, id, cm)
}

func (c *countStore) UpdateIssue(org string, i model.Issue) error {
	if i.Comments != nil {
		c.fullWrites.Add(1)
	}
	return c.Store.UpdateIssue(org, i)
}

// issueEnv 組織 o1:伺服器管理員 root、組織管理員 boss、成員 alice、bob;角色 dev
func issueEnv(t *testing.T, st store.Store) (*service.Service, *model.Principal, *model.Principal, *model.Principal, *model.Principal) {
	t.Helper()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.CreateOrg(model.Org{ID: "o1", Name: "組織一", CreatedAt: now})
	for _, u := range []struct{ id, email, role string }{{"u1", "boss@x", "admin"}, {"u2", "alice@x", "member"}, {"u3", "bob@x", "member"}} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		_ = st.SaveMember(model.Member{Org: "o1", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	root := &model.Principal{User: model.User{ID: "u0", Email: "root@x", Role: "admin"}}
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	alice := &model.Principal{User: model.User{ID: "u2", Email: "alice@x", Role: "member"}}
	bob := &model.Principal{User: model.User{ID: "u3", Email: "bob@x", Role: "member"}}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "dev", Name: "工程師", Active: true}); err != nil {
		t.Fatal(err)
	}
	return svc, root, boss, alice, bob
}

func issueStores(t *testing.T) map[string]store.Store {
	f, err := store.OpenFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]store.Store{"mem": store.NewMem(), "file": f}
}

// 同時 N 個人留言、同時有人改標題與附加內文:留言一則都不少,附加的內文也都在
func TestIssueConcurrentCommentsAndEdits(t *testing.T) {
	for name, st := range issueStores(t) {
		t.Run(name, func(t *testing.T) {
			svc, _, _, alice, bob := issueEnv(t, st)
			iss, err := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Body: "開頭"})
			if err != nil {
				t.Fatal(err)
			}
			const n = 30
			var wg sync.WaitGroup
			errs := make(chan error, 3*n)
			for k := range n {
				wg.Add(3)
				go func() {
					defer wg.Done()
					who := alice
					if k%2 == 1 {
						who = bob
					}
					_, err := svc.AddIssueComment(who, "o1", strings.ToLower(iss.ID), service.CommentInput{Text: fmt.Sprintf("留言 %d", k)})
					errs <- err
				}()
				go func() {
					defer wg.Done()
					title := fmt.Sprintf("標題 %d", k)
					_, err := svc.UpdateIssue(bob, "o1", iss.ID, service.IssuePatch{Title: &title})
					errs <- err
				}()
				go func() {
					defer wg.Done()
					part := fmt.Sprintf("附加 %d", k)
					_, err := svc.UpdateIssue(alice, "o1", iss.ID, service.IssuePatch{AppendBody: &part})
					errs <- err
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := svc.GetIssue(alice, "o1", iss.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Comments) != n {
				t.Fatalf("留言應該 %d 則:%d", n, len(got.Comments))
			}
			if !strings.HasPrefix(got.Body, "開頭\n\n") {
				t.Fatal("原本的內文不見", got.Body)
			}
			for k := range n {
				if !strings.Contains(got.Body, fmt.Sprintf("附加 %d\n", k)) && !strings.HasSuffix(got.Body, fmt.Sprintf("附加 %d", k)) {
					t.Fatalf("附加 %d 不見:%q", k, got.Body)
				}
			}
		})
	}
}

// 作者:一般成員一律是自己;伺服器管理員可以指定(沒給是 mcp);角色身分是 agent:<id>
func TestAddIssueCommentAuthorAndChecks(t *testing.T) {
	svc, root, _, alice, _ := issueEnv(t, store.NewMem())
	iss, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	u, err := svc.AddIssueComment(alice, "o1", iss.ID, service.CommentInput{Text: " 看到了 ", Author: "冒名"})
	if err != nil || len(u.Comments) != 1 || u.Comments[0].Author != "alice@x" || u.Comments[0].Text != "看到了" || u.Comments[0].ID == "" {
		t.Fatalf("%+v %v", u, err)
	}
	if u, _ = svc.AddIssueComment(root, "o1", iss.ID, service.CommentInput{Text: "x", Author: "agent:ceo"}); u.Comments[1].Author != "agent:ceo" {
		t.Fatal(u.Comments[1])
	}
	if u, _ = svc.AddIssueComment(root, "o1", iss.ID, service.CommentInput{Text: "y"}); u.Comments[2].Author != "mcp" {
		t.Fatal(u.Comments[2])
	}
	if _, err := svc.AddIssueComment(alice, "o1", iss.ID, service.CommentInput{Text: "  "}); code(err) != 400 {
		t.Fatal("空白留言 400", err)
	}
	if _, err := svc.AddIssueComment(alice, "o1", "ISS-99", service.CommentInput{Text: "x"}); code(err) != 404 {
		t.Fatal("找不到 404", err)
	}
	outsider := &model.Principal{User: model.User{ID: "u9", Email: "out@x", Role: "member"}}
	if _, err := svc.AddIssueComment(outsider, "o1", iss.ID, service.CommentInput{Text: "x"}); code(err) != 403 {
		t.Fatal("不是成員 403", err)
	}
}

// IssuePatch.Comments:一般成員只能新增(不能改、沒送的不會被刪),伺服器管理員整份取代
func TestIssuePatchCommentsRules(t *testing.T) {
	svc, root, _, alice, bob := issueEnv(t, store.NewMem())
	iss, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	a, _ := svc.AddIssueComment(alice, "o1", iss.ID, service.CommentInput{Text: "alice 的留言"})

	// 改別人留言的文字:403
	edited := append([]model.Comment{}, a.Comments...)
	edited[0].Text = "被改掉了"
	if _, err := svc.UpdateIssue(bob, "o1", iss.ID, service.IssuePatch{Comments: &edited}); code(err) != 403 || !strings.Contains(err.Error(), "comments") {
		t.Fatal("一般成員不能改已有的留言", err)
	}
	// 送空的(少送):不會刪掉別人的留言
	empty := []model.Comment{}
	if u, err := svc.UpdateIssue(bob, "o1", iss.ID, service.IssuePatch{Comments: &empty}); err != nil || len(u.Comments) != 1 {
		t.Fatalf("少送不能刪留言:%+v %v", u, err)
	}
	// 舊的用戶端:讀整份 → 加一則 → 整份送回。新的那則附加,作者是自己
	old := append(append([]model.Comment{}, a.Comments...), model.Comment{Text: "bob 的", Author: "alice@x"})
	u, err := svc.UpdateIssue(bob, "o1", iss.ID, service.IssuePatch{Comments: &old})
	if err != nil || len(u.Comments) != 2 || u.Comments[1].Author != "bob@x" || u.Comments[0].Text != "alice 的留言" {
		t.Fatalf("%+v %v", u, err)
	}
	// 伺服器管理員:整份取代(維運用)
	keep := u.Comments[1:]
	if r, err := svc.UpdateIssue(root, "o1", iss.ID, service.IssuePatch{Comments: &keep}); err != nil || len(r.Comments) != 1 || r.Comments[0].Author != "bob@x" {
		t.Fatalf("%+v %v", r, err)
	}
}

// 執行者留言走 AddIssueComment(不整份寫回),署名是角色
func TestWorkerCommentUsesAddIssueComment(t *testing.T) {
	cs := &countStore{Store: store.NewMem()}
	svc, _, boss, alice, _ := issueEnv(t, cs)
	iss, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t"})
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "dev", Title: "做", Links: []string{iss.ID}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(alice, "o1", service.ClaimInput{AgentID: "dev", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddIssueComment(alice, "o1", iss.ID, service.CommentInput{Text: "人先留"}); err != nil {
		t.Fatal(err)
	}
	u, err := svc.WorkerComment(alice, "o1", tk.ID, res.Credential.Code, strings.ToLower(iss.ID), "做完了")
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Comments) != 2 || u.Comments[1].Author != model.AgentPrefix+"dev" || u.Comments[0].Text != "人先留" {
		t.Fatalf("%+v", u.Comments)
	}
	if cs.adds.Load() != 2 || cs.fullWrites.Load() != 0 {
		t.Fatalf("留言應該都走 AddIssueComment:adds=%d fullWrites=%d", cs.adds.Load(), cs.fullWrites.Load())
	}
}

// 一般修改(沒送 comments)不會整份寫回留言;刪角色時取消指派也不會
func TestUpdateIssueLeavesCommentsAlone(t *testing.T) {
	cs := &countStore{Store: store.NewMem()}
	svc, _, boss, alice, _ := issueEnv(t, cs)
	iss, _ := svc.CreateIssue(alice, "o1", service.IssueInput{Title: "t", Assignee: "dev"})
	_, _ = svc.AddIssueComment(alice, "o1", iss.ID, service.CommentInput{Text: "留言"})
	title := "新標題"
	if u, err := svc.UpdateIssue(alice, "o1", strings.ToLower(iss.ID), service.IssuePatch{Title: &title}); err != nil || u.Title != title || len(u.Comments) != 1 {
		t.Fatalf("%+v %v", u, err)
	}
	if err := svc.DeleteAgent(boss, "o1", "dev"); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetIssue(alice, "o1", iss.ID)
	if got.Assignee != "" || len(got.Comments) != 1 {
		t.Fatalf("%+v", got)
	}
	if cs.fullWrites.Load() != 0 {
		t.Fatal("不該整份寫回留言", cs.fullWrites.Load())
	}
	// 刪除也不分大小寫
	if err := svc.DeleteIssue(alice, "o1", strings.ToLower(iss.ID)); err != nil {
		t.Fatal(err)
	}
}

// ImportIssuesWith Merge:可以重跑;沒有 id、重複 id 略過;已經有的略過或整筆取代;新的排在既有的後面
func TestImportIssuesMerge(t *testing.T) {
	for name, st := range issueStores(t) {
		t.Run(name, func(t *testing.T) {
			svc, root, boss, alice, _ := issueEnv(t, st)
			if _, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "既有的"}); err != nil {
				t.Fatal(err)
			}
			list := []model.Issue{
				{ID: "ISS-9", Title: "九", Labels: []string{" a ", "A", ""}, Comments: []model.Comment{{Author: "tim", Text: "嗨"}}},
				{ID: "", Title: "沒有 id"},
				{ID: "iss-9", Title: "重複"},
				{ID: "ISS-1", Title: "蓋過既有的"},
			}
			if _, err := svc.ImportIssuesWith(alice, "o1", list, service.IssueImportOptions{Merge: true}); service.Code(err) != 403 {
				t.Fatal("成員不能匯入", err)
			}
			if _, err := svc.ImportIssuesWith(boss, "o1", []model.Issue{{ID: "ISS-5", Title: "五"}}, service.IssueImportOptions{}); service.Code(err) != 409 {
				t.Fatal("不是 Merge 時已經有 Issue 要 409", err)
			}
			r, err := svc.ImportIssuesWith(boss, "o1", list, service.IssueImportOptions{Merge: true})
			if err != nil || r.Inserted != 1 || r.Updated != 0 || r.Skipped != 3 {
				t.Fatal(r, err)
			}
			got, _ := svc.ListIssues(root, "o1")
			if len(got) != 2 || got[0].ID != "ISS-1" || got[0].Title != "既有的" || got[1].ID != "ISS-9" {
				t.Fatalf("順序與略過:%+v", got)
			}
			n := got[1]
			if n.Status != "open" || n.Priority != "normal" || len(n.Labels) != 1 || n.Labels[0] != "a" || n.CreatedAt.IsZero() ||
				len(n.Comments) != 1 || n.Comments[0].ID == "" || n.Comments[0].At.IsZero() {
				t.Fatalf("預設值:%+v", n)
			}
			// 重跑不重複;Overwrite 整筆取代
			r, err = svc.ImportIssuesWith(boss, "o1", list, service.IssueImportOptions{Merge: true, Overwrite: true})
			if err != nil || r.Inserted != 0 || r.Updated != 2 || r.Skipped != 2 {
				t.Fatal(r, err)
			}
			got, _ = svc.ListIssues(root, "o1")
			if len(got) != 2 || got[0].Title != "蓋過既有的" || got[1].Title != "九" {
				t.Fatalf("覆蓋:%+v", got)
			}
		})
	}
}

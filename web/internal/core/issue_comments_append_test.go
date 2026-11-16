package core

import (
	"fmt"
	"sync"
	"testing"

	"omixor-core/model"
)

// 本機模式:畫面/MCP 的留言走核心的只附加路徑。別的寫入者(同一個核心的其他用戶端)同時直接在核心新增留言,
// 兩邊加起來 N 則,最後要剛好 N 則(以前讀整份 → 加一則 → 整份取代,會蓋掉同時進來的)
func TestCommentIssueConcurrentAppendOnly(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	is := a.CreateIssueFor("並發留言", "", "normal", nil, "", "", "mcp", "", "")
	org := a.issueSt().Org
	a.Mu.Unlock()
	lc := localCore()
	if is.ID == "" || lc == nil {
		t.Fatal("建立失敗")
	}
	const n = 40
	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			if k%2 == 0 {
				a.Mu.Lock()
				defer a.Mu.Unlock()
				if a.CommentIssue(is.ID, fmt.Sprintf("web-%d", k), "mcp") == nil {
					t.Errorf("留言 %d 失敗:%s", k, a.IssueRemoteError())
				}
				return
			}
			// 別處直接寫進核心(另一個 omixor-web、App)
			if _, err := lc.Svc.Store.AddIssueComment(org, is.ID, model.Comment{ID: fmt.Sprintf("x-%d", k), Author: "x", Text: fmt.Sprintf("other-%d", k), At: model.Now()}); err != nil {
				t.Error(err)
			}
			// 夾雜修改其他欄位(標籤):不能帶整份留言蓋掉別人的
			a.Mu.Lock()
			defer a.Mu.Unlock()
			a.UpdateIssue(is.ID, "mcp", func(i *model.Issue) { i.Labels = []string{fmt.Sprintf("l%d", k)} })
		}(k)
	}
	wg.Wait()
	got, err := lc.Svc.Store.GetIssue(org, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Comments) != n {
		t.Fatalf("留言要 %d 則,得到 %d 則", n, len(got.Comments))
	}
}

// issuePatchFor 只放有變的欄位;commentsDiff 分得出「在最後新增」與「改了/刪了」
func TestIssuePatchOnlyChangedFields(t *testing.T) {
	before := model.Issue{ID: "ISS-1", Title: "t", Body: "b", Status: "open", Priority: "normal", Labels: []string{"a"},
		Comments: []model.Comment{{ID: "c1", Text: "x"}}}
	n := cloneIssue(before)
	n.Status = "closed"
	n.Comments = append(n.Comments, model.Comment{ID: "c2", Text: "y"})
	p, changed := issuePatchFor(n, before, "mcp")
	if !changed || p.Status == nil || *p.Status != "closed" || p.Title != nil || p.Body != nil || p.Labels != nil ||
		p.Priority != nil || p.Source != nil || p.Link != nil || p.Assignee != nil || p.Repo != nil || p.Comments != nil {
		t.Fatalf("只該送 status:%+v", p)
	}
	added, replace := commentsDiff(before.Comments, n.Comments)
	if replace || len(added) != 1 || added[0].Text != "y" {
		t.Fatalf("新增一則:%v %v", added, replace)
	}
	if _, replace := commentsDiff(before.Comments, nil); !replace {
		t.Fatal("刪掉留言要整份取代")
	}
	if _, replace := commentsDiff(before.Comments, []model.Comment{{ID: "c1", Text: "改了"}}); !replace {
		t.Fatal("改了留言要整份取代")
	}
	if _, changed := issuePatchFor(before, before, "mcp"); changed {
		t.Fatal("沒變不該送")
	}
}

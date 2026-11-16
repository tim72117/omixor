package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// testPG 用 url 連 PostgreSQL,建一個暫時的 schema 給這個測試用,測完刪掉(一致性測試的 pg 實作用;見 conformance_test.go)
func testPG(t *testing.T, url string) Store {
	t.Helper()
	ctx := context.Background()
	schema := fmt.Sprintf("core_t_%d", time.Now().UnixNano())
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatal(err)
	}
	c.Close(ctx)
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	p, err := NewPG(url + sep + "search_path=" + schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Close()
		if c, err := pgx.Connect(ctx, url); err == nil {
			_, _ = c.Exec(ctx, "drop schema "+schema+" cascade")
			c.Close(ctx)
		}
	})
	return p
}

func newTestIssue(t *testing.T, s Store, org string) model.Issue {
	t.Helper()
	if err := s.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	at := mt("2026-01-02T03:04:05Z")
	i, err := s.CreateIssue(org, model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "a@x",
		CreatedAt: at, UpdatedAt: at, Labels: []string{}, Comments: []model.Comment{}})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// 同時 N 則留言:一則都不會少(一致性測試的一項,見 conformance_test.go)
func suiteAddIssueCommentConcurrent(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	iss := newTestIssue(t, s, "o")
	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AddIssueComment("o", iss.ID, model.Comment{ID: fmt.Sprintf("c%d", k), Author: "a@x",
				Text: fmt.Sprintf("第 %d 則", k), At: mt("2026-02-01T00:00:00Z")})
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
	got, err := s.GetIssue("o", iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Comments) != n {
		t.Fatalf("留言應該 %d 則:%d", n, len(got.Comments))
	}
	seen := map[string]bool{}
	for _, c := range got.Comments {
		seen[c.ID] = true
	}
	if len(seen) != n {
		t.Fatal("留言 id 重複或遺失", len(seen))
	}
	if !got.UpdatedAt.Equal(mt("2026-02-01T00:00:00Z").Time) {
		t.Fatal("updatedAt 沒更新", got.UpdatedAt)
	}
	s = impl.reopen(t, s)
	if g, _ := s.GetIssue("o", iss.ID); len(g.Comments) != n {
		t.Fatal("讀回的留言數", len(g.Comments))
	}
}

// id 不分大小寫:Get、Update、AddIssueComment、Delete 一致;Issue 不存在回 ErrNotFound(一致性測試的一項,見 conformance_test.go)
func suiteIssueIDCaseInsensitive(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	iss := newTestIssue(t, s, "o")
	lower := strings.ToLower(iss.ID)
	a, err := s.AddIssueComment("o", lower, model.Comment{ID: "c1", Author: "a@x", Text: "hi", At: mt("2026-02-01T00:00:00Z")})
	if err != nil || a.ID != iss.ID || len(a.Comments) != 1 || a.Comments[0].Text != "hi" {
		t.Fatal("AddIssueComment", a, err)
	}
	if _, err := s.AddIssueComment("o", "ISS-999", model.Comment{ID: "c2", Text: "x", At: model.Now()}); err != ErrNotFound {
		t.Fatal("不存在的 Issue 應該 ErrNotFound", err)
	}
	// UpdateIssue:Comments 是 nil 時留言不動
	cur, _ := s.GetIssue("o", lower)
	cur.ID, cur.Title, cur.Comments = lower, "改過", nil
	if err := s.UpdateIssue("o", *cur); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIssue("o", iss.ID)
	if got.Title != "改過" || got.ID != iss.ID || len(got.Comments) != 1 {
		t.Fatal("UpdateIssue(nil 留言)", got)
	}
	// 不是 nil:整份取代
	got.Comments = []model.Comment{}
	if err := s.UpdateIssue("o", *got); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetIssue("o", iss.ID); len(g.Comments) != 0 {
		t.Fatal("UpdateIssue 整份取代", g.Comments)
	}
	if err := s.DeleteIssue("o", lower); err != nil {
		t.Fatal("DeleteIssue 小寫 id", err)
	}
	if _, err := s.GetIssue("o", iss.ID); err != ErrNotFound {
		t.Fatal("刪掉後還在", err)
	}
	if err := s.DeleteIssue("o", lower); err != ErrNotFound {
		t.Fatal("再刪一次應該 ErrNotFound", err)
	}
}

// File:附加的留言有存到磁碟
func TestFileAddIssueCommentPersists(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	iss := newTestIssue(t, f, "o")
	if _, err := f.AddIssueComment("o", iss.ID, model.Comment{ID: "c1", Author: "a@x", Text: "hi", At: model.Now()}); err != nil {
		t.Fatal(err)
	}
	g, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if i, err := g.GetIssue("o", iss.ID); err != nil || len(i.Comments) != 1 || i.Comments[0].Text != "hi" {
		t.Fatal("重開後留言不見", i, err)
	}
}

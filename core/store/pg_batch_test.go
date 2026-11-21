package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// PG.Batch:全有或全無。fn 失敗整批還原(包含批次裡自己開交易的方法,例如建 Issue),成功才一起 commit
func TestPGBatch(t *testing.T) {
	p := testPG(t).(*PG)
	if err := p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: mt("2026-01-01T00:00:00Z")}); err != nil {
		t.Fatal(err)
	}
	at := mt("2026-01-02T03:04:05Z")
	mk := func() model.Issue {
		return model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor",
			CreatedAt: at, UpdatedAt: at, Labels: []string{}, Comments: []model.Comment{}}
	}
	boom := errors.New("boom")
	err := p.Batch(func(tx *PG) error {
		if _, err := tx.CreateIssue("o", mk()); err != nil {
			return err
		}
		if _, err := tx.CreateIssue("o", mk()); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if list, _ := p.ListIssues("o"); len(list) != 0 {
		t.Fatal("失敗的批次不該留下 Issue", list)
	}
	err = p.Batch(func(tx *PG) error {
		if _, err := tx.CreateIssue("o", mk()); err != nil {
			return err
		}
		// 批次裡再開批次:同一個交易
		return tx.Batch(func(tx2 *PG) error {
			_, err := tx2.CreateIssue("o", mk())
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := p.ListIssues("o")
	if len(list) != 2 {
		t.Fatal("成功的批次要留下 2 筆", list)
	}
	if list[0].ID == list[1].ID {
		t.Fatal("編號重複", list)
	}
}

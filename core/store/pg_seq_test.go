package store

// 序號(PG 專屬):seqNext 自癒、(org, number) 部分唯一索引的升級與非數字 id、匯入與建立並發不死鎖

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
)

func seqIssue(at model.Time) model.Issue {
	return model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at}
}

// 序號表落後現有資料(舊版實例用 max+1 先發了號、手動 SQL、還原資料):seqNext 取 max(記錄, 現有最大號, floor) + 1,不會一直撞唯一索引
func TestPGSeqNextSelfHeals(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	a, err := p.CreateIssue("o", seqIssue(at))
	chk(t, err)
	if a.Key != "ISS-1" {
		t.Fatal(a.ID)
	}
	// 手動先插入 ISS-2(等於舊版實例搶先發的號),序號表還停在 1
	x := seqIssue(at)
	x.ID = "ISS-2"
	_, err = p.MergeIssues("o", []model.Issue{x}, false)
	chk(t, err)
	pgExec(t, p, `update omixor_seq set last = 1 where org = 'o' and scope = 'issue'`)
	b, err := p.CreateIssue("o", seqIssue(at))
	if err != nil {
		t.Fatal("序號落後時要自癒,不能撞主鍵/唯一索引:", err)
	}
	if b.Key != "ISS-3" {
		t.Fatal("要接在現有最大號後面", b.ID)
	}
	// 序號列被整個刪掉(還原資料)也一樣
	pgExec(t, p, `delete from omixor_seq where org = 'o'`)
	c, err := p.CreateIssue("o", seqIssue(at))
	chk(t, err)
	if c.Key != "ISS-4" {
		t.Fatal(c.ID)
	}
	// 事件的 floor 與訊息 scope 也走同一條路
	ch, err := p.CreateChannel("o", model.Channel{Name: "c", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	for range 3 {
		_, err = p.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Author: lid(t, p, "o", "user:a"), Text: "hi", At: at})
		chk(t, err)
	}
	pgExec(t, p, `delete from omixor_seq where scope like 'msg:%'`)
	m, err := p.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Author: lid(t, p, "o", "user:a"), Text: "hi", At: at})
	chk(t, err)
	if m.ID != "M-4" {
		t.Fatal("訊息號碼自癒", m.ID)
	}
}

// 舊版建的完整唯一索引(含 number = 0 的列)升級成部分索引;非數字 id 與超過 int32 的 id 可以匯入
func TestPGSeqPartialIndexAndNonNumeric(t *testing.T) {
	url := fkSchema(t)
	p := fkPG(t, url)
	at := mt("2026-01-02T03:04:05Z")
	chk(t, p.CreateOrg(model.Org{ID: "o", Name: "o", CreatedAt: at}))
	// 模擬舊版:完整的唯一索引
	pgExec(t, p, `drop index ops_issues_org_number`)
	pgExec(t, p, `create unique index ops_issues_org_number on ops_issues (org, number)`)
	chk(t, p.migrateSeq())
	if n := pgCount(t, p, `select count(*) from pg_index i join pg_class c on c.oid = i.indexrelid where c.relname = 'ops_issues_org_number' and i.indpred is not null and i.indisvalid`); n != 1 {
		t.Fatal("舊的完整索引要換成有效的部分索引", n)
	}
	chk(t, p.migrateSeq()) // 冪等
	mk := func(id string) model.Issue { x := seqIssue(at); x.ID = id; return x }
	if _, err := p.ImportIssues("o", []model.Issue{mk("gh-1"), mk("gh-2"), mk("ISS-99999999999"), mk("ISS-5")}); err != nil {
		t.Fatal("非數字 id 不能撞索引:", err)
	}
	x, err := p.CreateIssue("o", seqIssue(at))
	chk(t, err)
	if x.Key != "ISS-9" { // gh-1、gh-2、ISS-99999999999 匯入時發 ISS-6、7、8
		t.Fatal(x.ID)
	}
	// 殘留的 invalid 索引:重建
	pgExec(t, p, `update pg_index set indisvalid = false where indexrelid = 'ops_issues_org_number'::regclass`)
	chk(t, p.migrateSeq())
	if n := pgCount(t, p, `select count(*) from pg_index i join pg_class c on c.oid = i.indexrelid where c.relname = 'ops_issues_org_number' and i.indisvalid`); n != 1 {
		t.Fatal("invalid 的索引要重建", n)
	}
}

// ImportIssues 先鎖序號列:和並發的 CreateIssue 交錯不死鎖、不報錯(匯入要嘛成功要嘛因為已經有資料而被拒)
func TestPGImportIssuesConcurrentCreateNoDeadlock(t *testing.T) {
	p := fkPG(t, fkSchema(t))
	at := mt("2026-01-02T03:04:05Z")
	for round := range 8 {
		org := fmt.Sprintf("o%d", round)
		chk(t, p.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: at}))
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := p.CreateIssue(org, seqIssue(at)); err != nil {
					errs <- err
				}
			}()
		}
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				x := seqIssue(at)
				x.ID = "ISS-50"
				if _, err := p.ImportIssues(org, []model.Issue{x}); err != nil && !strings.Contains(err.Error(), "已經有") {
					errs <- err
				}
			}()
		}
		within(t, 30*time.Second, "並發建立與匯入", wg.Wait)
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
	}
}

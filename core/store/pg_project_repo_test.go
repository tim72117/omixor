package store

import (
	"errors"
	"slices"
	"testing"
	"time"

	"omixor-core/model"
)

func prList(t *testing.T, p *PG, org string) []string {
	t.Helper()
	l, err := p.ListProjectRepos(org)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, x := range l {
		out = append(out, x.Project+" "+x.Repo)
	}
	return out
}

// 專案與 repo 的連結:不分大小寫、github:/gitlab: 前綴可省、重複連結不新增;多對多;取消;專案不存在 ErrBadRef;刪專案連結跟著刪;組織隔離
func TestProjectReposLinkUnlink(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	mkOrgs(t, p, "o", "x")
	now := model.Now()
	a, err := p.CreateProject("o", model.Project{Name: "A", Key: "AAA", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.CreateProject("o", model.Project{Name: "B", Key: "BBB", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if c, err := p.LinkProjectRepo("o", a.ID, "Acme/App", at); err != nil || !c {
		t.Fatal("第一次連結", c, err)
	}
	for _, same := range []string{"acme/app", "github:ACME/app", "gitlab:acme/app"} { // 同一個 repo(比對規則)
		if c, err := p.LinkProjectRepo("o", a.ID, same, at.Add(time.Second)); err != nil || c {
			t.Fatal("重複連結不新增", same, c, err)
		}
	}
	if got := prList(t, p, "o"); !slices.Equal(got, []string{"PRJ-1 Acme/App"}) {
		t.Fatal("保留第一次寫的樣子", got)
	}
	// 多對多:同一個 repo 連兩個專案、一個專案連兩個 repo
	for _, x := range [][2]string{{b.ID, "acme/app"}, {a.ID, "gitlab:g/sub/proj"}} {
		if c, err := p.LinkProjectRepo("o", x[0], x[1], at.Add(2*time.Second)); err != nil || !c {
			t.Fatal(x, c, err)
		}
	}
	if got := prList(t, p, "o"); !slices.Equal(got, []string{"PRJ-1 Acme/App", "PRJ-1 gitlab:g/sub/proj", "PRJ-2 acme/app"}) {
		t.Fatal("依專案號碼、連結時間", got)
	}
	// 組織隔離
	if got := prList(t, p, "x"); len(got) != 0 {
		t.Fatal("別的組織看不到", got)
	}
	if _, err := p.LinkProjectRepo("x", a.ID, "acme/app", at); !errors.Is(err, ErrBadRef) {
		t.Fatal("別的組織的專案不能連", err)
	}
	if _, err := p.LinkProjectRepo("o", "PRJ-99", "acme/app", at); !errors.Is(err, ErrBadRef) {
		t.Fatal("專案不存在要 ErrBadRef", err)
	}
	// 取消:前綴與大小寫不拘;沒連過回 false
	if r, err := p.UnlinkProjectRepo("o", a.ID, "GITHUB:acme/APP"); err != nil || !r {
		t.Fatal("取消", r, err)
	}
	if r, err := p.UnlinkProjectRepo("o", a.ID, "acme/app"); err != nil || r {
		t.Fatal("已經取消過", r, err)
	}
	if got := prList(t, p, "o"); !slices.Equal(got, []string{"PRJ-1 gitlab:g/sub/proj", "PRJ-2 acme/app"}) {
		t.Fatal(got)
	}
	// 刪專案:連結跟著刪(cascade);別的專案不受影響
	if err := p.DeleteProject("o", a.ID); err != nil {
		t.Fatal(err)
	}
	if got := prList(t, p, "o"); !slices.Equal(got, []string{"PRJ-2 acme/app"}) {
		t.Fatal("刪專案連結跟著刪", got)
	}
	// 刪組織:連結清掉
	if err := p.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, p, `select count(*) from omixor_project_repos`); n != 0 {
		t.Fatal("刪組織連結要清掉", n)
	}
}

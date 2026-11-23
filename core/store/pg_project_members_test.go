package store

import (
	"errors"
	"slices"
	"testing"
	"time"

	"omixor-core/model"
)

func pmNodes(t *testing.T, p *PG, org, project string) []string {
	t.Helper()
	l, err := p.ListProjectMembers(org, project)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, m := range l {
		out = append(out, m.Project+" "+m.ActorID+" "+m.Role)
	}
	return out
}

// 專案成員:加入、改角色(冪等)、移除、依 actor 查資格、專案或 actor 不存在 ErrBadRef、role 只能是 admin/member、
// 刪專案(成員跟著刪)、刪 actor(成員跟著刪)、刪組織;restricted 欄位的讀寫
func TestProjectMembersCRUD(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	mkOrgs(t, p, "o", "x")
	now := model.Now()
	a, err := p.CreateProject("o", model.Project{Name: "A", Key: "AAA", Restricted: true, CreatedAt: now, UpdatedAt: now})
	if err != nil || !a.Restricted {
		t.Fatal(a, err)
	}
	b, err := p.CreateProject("o", model.Project{Name: "B", Key: "BBB", CreatedAt: now, UpdatedAt: now})
	if err != nil || b.Restricted {
		t.Fatal(b, err)
	}
	if g, err := p.GetProject("o", a.ID); err != nil || !g.Restricted {
		t.Fatal("restricted 要存下來", g, err)
	}
	u1, u2 := mkUserActor(t, p, "u1@x"), mkUserActor(t, p, "u2@x")
	at := time.Now().UTC()
	if ch, err := p.SetProjectMember("o", a.ID, u1.ID, "member", at); err != nil || !ch {
		t.Fatal("加入", ch, err)
	}
	if ch, err := p.SetProjectMember("o", a.ID, u1.ID, "member", at.Add(time.Second)); err != nil || ch {
		t.Fatal("同角色再加不算變動", ch, err)
	}
	if ch, err := p.SetProjectMember("o", a.ID, u1.ID, "admin", at.Add(2*time.Second)); err != nil || !ch {
		t.Fatal("改角色", ch, err)
	}
	for _, x := range [][3]string{{a.ID, u2.ID, "member"}, {b.ID, u1.ID, "member"}} {
		if _, err := p.SetProjectMember("o", x[0], x[1], x[2], at.Add(3*time.Second)); err != nil {
			t.Fatal(x, err)
		}
	}
	if got := pmNodes(t, p, "o", a.ID); !slices.Equal(got, []string{a.ID + " " + u1.ID + " admin", a.ID + " " + u2.ID + " member"}) {
		t.Fatal(got)
	}
	if got := pmNodes(t, p, "o", ""); len(got) != 3 {
		t.Fatal("空字串 = 整個組織", got)
	}
	m, err := p.ProjectMemberships("o", u1.ID)
	if err != nil || len(m) != 2 || m[a.ID] != "admin" || m[b.ID] != "member" {
		t.Fatal(m, err)
	}
	if m, _ := p.ProjectMemberships("x", u1.ID); len(m) != 0 {
		t.Fatal("組織隔離", m)
	}
	if _, err := p.SetProjectMember("o", "PRJ-9", u1.ID, "member", at); !errors.Is(err, ErrBadRef) {
		t.Fatal("專案不存在要 ErrBadRef", err)
	}
	if _, err := p.SetProjectMember("x", a.ID, u1.ID, "member", at); !errors.Is(err, ErrBadRef) {
		t.Fatal("別的組織的專案要 ErrBadRef", err)
	}
	if _, err := p.SetProjectMember("o", a.ID, "usr_nobody", "member", at); !errors.Is(err, ErrBadRef) {
		t.Fatal("actor 不存在要 ErrBadRef", err)
	}
	if _, err := p.SetProjectMember("o", a.ID, u1.ID, "boss", at); err == nil {
		t.Fatal("role 只能是 admin 或 member")
	}
	if r, err := p.RemoveProjectMember("o", a.ID, u2.ID); err != nil || !r {
		t.Fatal(r, err)
	}
	if r, err := p.RemoveProjectMember("o", a.ID, u2.ID); err != nil || r {
		t.Fatal("已經移除", r, err)
	}
	// 一次拿掉某 actor 在組織的全部資格
	if ids, err := p.RemoveProjectMembershipsOf("o", u1.ID); err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	if got := pmNodes(t, p, "o", ""); len(got) != 0 {
		t.Fatal(got)
	}
	// 改旗標
	a.Restricted = false
	a.UpdatedAt = model.Now()
	if err := p.UpdateProject("o", a); err != nil {
		t.Fatal(err)
	}
	if g, _ := p.GetProject("o", a.ID); g.Restricted {
		t.Fatal("改回公開")
	}
	if l, _ := p.ListProjects("o"); len(l) != 2 || l[0].Restricted || l[1].Restricted {
		t.Fatal(l)
	}
	// 刪專案:成員跟著刪
	pgExec(t, p, `select 1`)
	if _, err := p.SetProjectMember("o", a.ID, u1.ID, "member", at); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetProjectMember("o", b.ID, u2.ID, "member", at); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteProject("o", a.ID); err != nil {
		t.Fatal(err)
	}
	if got := pmNodes(t, p, "o", ""); !slices.Equal(got, []string{b.ID + " " + u2.ID + " member"}) {
		t.Fatal("刪專案成員跟著刪", got)
	}
	// 刪 actor(外鍵 cascade)
	pgExec(t, p, `delete from omixor_actors where id = $1`, u2.ID)
	if n := pgCount(t, p, `select count(*) from omixor_project_members`); n != 0 {
		t.Fatal("刪 actor 成員跟著刪", n)
	}
	// 刪組織
	if _, err := p.SetProjectMember("o", b.ID, u1.ID, "member", at); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	if n := pgCount(t, p, `select count(*) from omixor_project_members`); n != 0 {
		t.Fatal("刪組織成員要清掉", n)
	}
}

func mkUserActor(t *testing.T, p *PG, email string) model.ActorRecord {
	t.Helper()
	a, err := p.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: email, Handle: email})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

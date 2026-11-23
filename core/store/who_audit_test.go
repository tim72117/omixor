package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// whoID 測試用:email → 使用者 actor id(沒有就建未註冊使用者)
func emailActor(t *testing.T, s Store, email string) string {
	t.Helper()
	id, err := s.ResolveLegacy("", email)
	if err != nil || id == "" {
		t.Fatalf("解析 %s:%q %v", email, id, err)
	}
	return id
}

// 稽核:actor、email 快照、source 寫入與讀回;沒有 actor 是 NULL;不存在的 actor 被外鍵擋;已有的紀錄(使用者改 email 之後)仍是當時的 email
func TestAuditActorSourceRoundTrip(t *testing.T) {
	s := testPG(t)
	p := s.(*PG)
	at := mt("2026-11-20T01:02:03Z")
	u := emailActor(t, s, "amy@x")
	chk(t, s.AddAudit(model.AuditEntry{At: at, Actor: u, Email: "amy@x", Source: "mcp", Action: "a1"}))
	chk(t, s.AddAudit(model.AuditEntry{At: at, Actor: model.SystemActorOmixor, Action: "a2", Source: "any-future-source"}))
	chk(t, s.AddAudit(model.AuditEntry{At: at, Action: "login.denied"}))
	l, err := s.ListAudit(10)
	chk(t, err)
	if len(l) != 3 || l[2].Actor != u || l[2].Email != "amy@x" || l[2].Source != "mcp" ||
		l[1].Actor != model.SystemActorOmixor || l[1].Source != "any-future-source" || l[0].Actor != "" || l[0].Source != "" {
		t.Fatalf("讀回:%+v", l)
	}
	if n := pgCount(t, p, `select count(*) from omixor_audit where actor is null`); n != 1 {
		t.Fatalf("沒有 actor 要是 NULL,不是空字串:%d", n)
	}
	// actor 改了 handle/email 之後,舊紀錄的 email 快照不變
	pgExec(t, p, `update omixor_actors set handle = 'amy2@x', name = 'amy2@x' where id = '`+u+`'`)
	l, _ = s.ListAudit(10)
	if l[2].Email != "amy@x" || l[2].Actor != u {
		t.Fatalf("快照不隨之後的改名變:%+v", l[2])
	}
}

// updated_by 往返:存 actor id、NULL(沒有署名)讀回空字串、不存在的 actor 是 ErrBadRef
func TestUpdatedByActorRoundTrip(t *testing.T) {
	s := testPG(t)
	at := mt("2026-11-20T01:02:03Z")
	chk(t, s.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at}))
	u := emailActor(t, s, "amy@x")
	chk(t, s.SaveSystemSettings("o", model.OrgSystemSettings{UpdatedAt: &at, UpdatedBy: u}))
	chk(t, s.SaveServerFeatures(model.ServerFeatures{UpdatedAt: &at, UpdatedBy: model.SystemActorOmixor}))
	chk(t, s.SaveOrgDoc("o", "github", OrgDoc{UpdatedAt: &at, UpdatedBy: u}))
	chk(t, s.SaveMCPConfig("o", model.OrgMCPConfig{UpdatedAt: &at, UpdatedBy: u}))
	a, _ := s.GetSystemSettings("o")
	b, _ := s.GetServerFeatures()
	c, _ := s.GetOrgDoc("o", "github")
	d, _ := s.GetMCPConfig("o")
	if a.UpdatedBy != u || b.UpdatedBy != model.SystemActorOmixor || c.UpdatedBy != u || d.UpdatedBy != u {
		t.Fatalf("%q %q %q %q", a.UpdatedBy, b.UpdatedBy, c.UpdatedBy, d.UpdatedBy)
	}
	chk(t, s.SaveSystemSettings("o", model.OrgSystemSettings{UpdatedAt: &at}))
	if a, _ := s.GetSystemSettings("o"); a.UpdatedBy != "" {
		t.Fatalf("沒有署名讀回空字串:%q", a.UpdatedBy)
	}
	const missing = "usr_00000000-0000-7000-8000-000000000000"
	for name, err := range map[string]error{
		"system": s.SaveSystemSettings("o", model.OrgSystemSettings{UpdatedBy: missing}),
		"server": s.SaveServerFeatures(model.ServerFeatures{UpdatedBy: missing}),
		"doc":    s.SaveOrgDoc("o", "github", OrgDoc{UpdatedBy: missing}),
		"mcp":    s.SaveMCPConfig("o", model.OrgMCPConfig{UpdatedBy: missing}),
	} {
		if !errors.Is(err, ErrBadRef) {
			t.Errorf("%s:不存在的 actor 要 ErrBadRef:%v", name, err)
		}
	}
}

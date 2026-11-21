package service_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

func auditOf(t *testing.T, svc *service.Service, admin *model.Principal, action string) model.AuditEntry {
	t.Helper()
	l, err := svc.ListAudit(admin, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l { // 新的在前
		if e.Action == action {
			return e
		}
	}
	t.Fatalf("沒有稽核 %s", action)
	return model.AuditEntry{}
}

// 稽核:存 actor id、email 快照、source;輸出仍有 userId、email;使用者改 email、被停用之後舊紀錄照讀
func TestAuditActorEmailSourceAndDisabledUser(t *testing.T) {
	svc, st, boss := actorEnv(t)
	svc.Audit(boss, "t.user", "o1", map[string]any{"via": "ldap"})
	raw, _ := st.ListAudit(10)
	if len(raw) == 0 || raw[0].Actor != boss.Actor.ID || raw[0].Email != "Boss@X" || raw[0].Source != model.ViaLogin || raw[0].UserID != "" {
		t.Fatalf("store 存 actor id、email 快照、source(login):%+v", raw)
	}
	if raw[0].Detail["via"] != "ldap" {
		t.Fatal("detail 的 via(登入方式)不受 source 影響", raw[0].Detail)
	}
	e := auditOf(t, svc, boss, "t.user")
	if e.UserID != "u1" || e.Email != "Boss@X" || e.Source != model.ViaLogin {
		t.Fatalf("相容輸出 userId、email:%+v", e)
	}
	// 使用者改 email、被停用:舊稽核的 email 仍是當時的、userId 仍在
	if err := st.SaveUser(model.User{ID: "u1", Email: "boss2@x", Name: "U1", Role: "admin", Disabled: true, CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	admin := &model.Principal{User: model.User{ID: "u9", Email: "root@x", Role: "admin"}} // 伺服器管理員(看稽核用)
	e = auditOf(t, svc, admin, "t.user")
	if e.UserID != "u1" || e.Email != "Boss@X" {
		t.Fatalf("改 email、停用之後舊稽核照讀:%+v", e)
	}
	// 沒有使用者列的使用者 actor(未註冊):userId 退回 actor id
	carol, err := st.ResolveLegacy("o1", "carol@x")
	if err != nil {
		t.Fatal(err)
	}
	svc.Audit(&model.Principal{User: model.User{Email: "carol@x"}, Actor: model.ActorRef{ID: carol, Kind: model.ActorKindUser}, Via: model.ViaMCP}, "t.carol", "", nil)
	if e = auditOf(t, svc, admin, "t.carol"); e.UserID != carol || e.Email != "carol@x" || e.Source != "mcp" {
		t.Fatalf("%+v", e)
	}
}

// 角色憑證的稽核:actor 是角色、email 空、userId = agent:<handle>、source = agent-credential;Audit(nil) 的 actor 是 NULL
func TestAuditAgentAndAnonymous(t *testing.T) {
	svc, st, boss := actorEnv(t)
	cto := actorOf(t, st, model.ActorKindAgent, "o1", "cto")
	agent := &model.Principal{User: model.User{ID: "agent:cto", Email: "agent:cto", Role: "member"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto"},
		Actor: model.ActorRef{ID: cto.ID, Kind: model.ActorKindAgent}, Via: model.ViaAgentCredential}
	svc.Audit(agent, "t.agent", "o1", nil)
	svc.Audit(nil, "login.denied", "", map[string]any{"via": "ldap"})
	raw, _ := st.ListAudit(10)
	if raw[1].Actor != cto.ID || raw[1].Email != "" || raw[1].Source != "agent-credential" {
		t.Fatalf("角色:%+v", raw[1])
	}
	if raw[0].Actor != "" || raw[0].Source != "" {
		t.Fatalf("Audit(nil) 的 actor 是 NULL:%+v", raw[0])
	}
	if e := auditOf(t, svc, boss, "t.agent"); e.UserID != "agent:cto" || e.Email != "" {
		t.Fatalf("%+v", e)
	}
	if e := auditOf(t, svc, boss, "login.denied"); e.UserID != "" || e.Email != "" {
		t.Fatalf("%+v", e)
	}
	// source 能接任意字串
	svc.Audit(&model.Principal{Actor: boss.Actor, User: boss.User, Via: "future-source"}, "t.future", "", nil)
	if e := auditOf(t, svc, boss, "t.future"); e.Source != "future-source" {
		t.Fatalf("%+v", e)
	}
}

// 各 updated_by:存 actor id,輸出相容字串(email);server_features 系統層由伺服器管理員改
func TestUpdatedByStoresActorOutputsEmail(t *testing.T) {
	svc, st, boss := actorEnv(t)
	if _, err := svc.SaveServerFeatures(boss, service.SystemSettingsInput{Features: map[string]bool{model.FeatureMCPGateway: true}}); err != nil {
		t.Fatal(err)
	}
	v, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{model.FeatureMCPGateway: true}})
	if err != nil || v.UpdatedBy != "Boss@X" {
		t.Fatalf("%+v %v", v, err)
	}
	if g, _ := svc.GetSystemSettings(boss, "o1"); g.UpdatedBy != "Boss@X" {
		t.Fatalf("讀回相容輸出:%+v", g)
	}
	srvF, _ := st.GetServerFeatures()
	sysF, _ := st.GetSystemSettings("o1")
	if srvF.UpdatedBy != boss.Actor.ID || sysF.UpdatedBy != boss.Actor.ID {
		t.Fatalf("store 存 actor id:%q %q", srvF.UpdatedBy, sysF.UpdatedBy)
	}
	if g, _ := svc.GetServerFeatures(boss); g.UpdatedBy != "Boss@X" {
		t.Fatalf("%+v", g)
	}
	// MCP 設定:設定本身、單台伺服器的 updatedBy 都存 actor id、輸出 email
	x, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "files", Name: sp("檔案"), Transport: sp("stdio"), Command: sp("/usr/bin/fake")})
	if err != nil || x.UpdatedBy != "Boss@X" {
		t.Fatalf("%+v %v", x, err)
	}
	c, _ := st.GetMCPConfig("o1")
	if c.UpdatedBy != boss.Actor.ID || c.Servers[0].UpdatedBy != boss.Actor.ID {
		t.Fatalf("store 存 actor id:%+v", c)
	}
	g, err := svc.GetMCP(boss, "o1")
	if err != nil || g.UpdatedBy != "Boss@X" || len(g.Servers) != 1 || g.Servers[0].UpdatedBy != "Boss@X" {
		t.Fatalf("%+v %v", g, err)
	}
	if c2, _ := st.GetMCPConfig("o1"); c2.Servers[0].UpdatedBy != boss.Actor.ID {
		t.Fatal("輸出轉換不能改到存著的")
	}
	// 呼叫者本人換了 email:輸出跟著現在的 email(actor 不變)
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss3@x", Role: "admin", CreatedAt: model.Now()})
	if g, _ := svc.GetSystemSettings(boss, "o1"); g.UpdatedBy != "boss3@x" {
		t.Fatalf("%+v", g)
	}
}

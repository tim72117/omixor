package api_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 外部 MCP 登記的 API:功能沒開時列表空、寫入 403;開了以後成員讀、管理員改,回應沒有任何密鑰值
func TestMCPAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveUser("alice@example.com", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("o1", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	v, err := alice.GetMCP("o1")
	if err != nil || v.Enabled || len(v.Servers) != 0 || v.Note == "" {
		t.Fatal(v, err)
	}
	cmd, tr := "/bin/fake", "stdio"
	in := service.MCPServerInput{ID: "fs", Transport: &tr, Command: &cmd, EnvNames: &[]string{"API_KEY"}}
	if _, err := boss.SaveMCPServer("o1", in); status(err) != 403 {
		t.Fatal("沒開時寫入 403", err)
	}
	if model.BuildEdition != model.EditionDev {
		return
	}
	if _, err := boss.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{model.FeatureMCPGateway: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.SaveMCPServer("o1", in); status(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	s, err := boss.SaveMCPServer("o1", in)
	if err != nil || s.ID != "fs" || len(s.EnvNames) != 1 {
		t.Fatal(s, err)
	}
	bad := in
	bad.Env = map[string]string{"API_KEY": "sk-1"}
	if _, err := boss.SaveMCPServer("o1", bad); status(err) != 400 {
		t.Fatal("密鑰值 400", err)
	}
	if _, err := boss.SaveMCPTools("o1", "fs", service.MCPToolsInput{Sync: []service.MCPToolInfo{{Name: "read"}},
		Rules: []service.MCPToolRuleInput{{Name: "read", Enabled: &[]bool{true}[0]}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveAgent("o1", agentNamed("cto")); err != nil {
		t.Fatal(err)
	}
	if err := boss.SaveMCPGrants("o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "fs"}}}); err != nil {
		t.Fatal(err)
	}
	v, err = alice.GetMCP("o1")
	if err != nil || !v.Enabled || len(v.Servers) != 1 || len(v.Grants["cto"]) != 1 || v.CanEdit {
		t.Fatal(v, err)
	}
	r, err := alice.ResolveMCPTools("o1", service.MCPResolveInput{AgentID: "cto", Tools: []service.MCPResolveTool{{Server: "fs", Name: "read"}, {Server: "fs", Name: "x"}}})
	if err != nil || !r.Tools[0].Allowed || r.Tools[1].Allowed {
		t.Fatal(r, err)
	}
	if err := boss.DeleteMCPServer("o1", "fs"); err != nil {
		t.Fatal(err)
	}
}

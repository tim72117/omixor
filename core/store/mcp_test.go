package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// 外部 MCP 登記:讀寫(深拷貝)、File 重開讀得回來、刪組織一併刪除(一致性測試的一項,見 conformance_test.go)
func suiteMCPConfig(t *testing.T, impl storeImpl) {
	f := impl.open(t)
	at := mt("2026-11-13T01:02:03Z")
	if err := f.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetMCPConfig("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("沒存過回 ErrNotFound", err)
	}
	c := model.OrgMCPConfig{
		Servers: []model.MCPServerReg{{ID: "fs", Name: "檔案", Transport: "stdio", Command: "x", Args: []string{"a"}, EnvNames: []string{"K"},
			Enabled: true, Tools: []model.MCPToolRule{{Name: "read", Enabled: true}}}},
		Grants:    map[string][]model.MCPGrant{"cto": {{Server: "fs", Tools: []string{"read"}}}},
		UpdatedAt: &at, UpdatedBy: "boss@x",
	}
	if err := f.SaveMCPConfig("o", c); err != nil {
		t.Fatal(err)
	}
	c.Servers[0].Args[0] = "yyy" // 存進去之後改原本的不影響
	x, _ := f.GetMCPConfig("o")
	x.Servers[0].Args[0] = "zzz" // 拿回去改不影響存著的
	x.Grants["cto"][0].Tools[0] = "zzz"
	c.Servers[0].Args[0] = "a"
	f2 := impl.reopen(t, f)
	g, err := f2.GetMCPConfig("o")
	if err != nil || len(g.Servers) != 1 || g.Servers[0].Args[0] != "a" || g.Grants["cto"][0].Tools[0] != "read" || g.UpdatedBy != "boss@x" {
		t.Fatalf("重開讀回:%+v %v", g, err)
	}
	sameJSON(t, "GetMCPConfig", g, c.Clone())
	// 空的登記:nil 的清單讀回是 []、{}
	if err := f2.SaveMCPConfig("o", model.OrgMCPConfig{UpdatedAt: &at}); err != nil {
		t.Fatal(err)
	}
	g, _ = f2.GetMCPConfig("o")
	sameJSON(t, "空的登記", g, model.OrgMCPConfig{Servers: []model.MCPServerReg{}, Grants: map[string][]model.MCPGrant{}, UpdatedAt: &at})
	if _, err := f2.GetMCPConfig("p"); !errors.Is(err, ErrNotFound) {
		t.Fatal("別的組織", err)
	}
	if err := f2.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	f2 = impl.reopen(t, f2)
	if _, err := f2.GetMCPConfig("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪組織一併刪除", err)
	}
}

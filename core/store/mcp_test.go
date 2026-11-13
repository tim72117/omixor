package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// 外部 MCP 登記:Mem 讀寫(深拷貝)、File 重開讀得回來、刪組織一併刪除
func TestMCPConfigMemAndFile(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
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
	x, _ := f.GetMCPConfig("o")
	x.Servers[0].Args[0] = "zzz"
	x.Grants["cto"][0].Tools[0] = "zzz"
	f2, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f2.GetMCPConfig("o")
	if err != nil || len(g.Servers) != 1 || g.Servers[0].Args[0] != "a" || g.Grants["cto"][0].Tools[0] != "read" || g.UpdatedBy != "boss@x" {
		t.Fatalf("重開讀回:%+v %v", g, err)
	}
	if err := f2.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.GetMCPConfig("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪組織一併刪除", err)
	}
}

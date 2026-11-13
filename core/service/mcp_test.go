package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

func bp(b bool) *bool { return &b }

// 功能開關沒開(正式版一定沒開):讀回空清單與說明,寫入 403,resolve 一律不放行,角色的可用工具不多東西
func TestMCPGatewayOff(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	v, err := svc.GetMCP(alice, "o1")
	if err != nil || v.Enabled || len(v.Servers) != 0 || v.Note == "" || v.Servers == nil {
		t.Fatalf("沒開:%+v %v", v, err)
	}
	_, err = svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "fs", Transport: sp("stdio"), Command: sp("x")})
	if code(err) != 403 || !strings.Contains(err.Error(), "外部 MCP 閘道") {
		t.Fatal("沒開時寫入 403 並說明", err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto"}); code(err) != 403 {
		t.Fatal(err)
	}
	r, err := svc.ResolveMCPTools(alice, "o1", service.MCPResolveInput{Tools: []service.MCPResolveTool{{Server: "fs", Name: "read"}}})
	if err != nil || r.Enabled || len(r.Tools) != 1 || r.Tools[0].Allowed {
		t.Fatalf("%+v %v", r, err)
	}
	tools, _ := svc.AgentTools(alice, "o1", "cto")
	for _, x := range tools {
		if x.Source == "mcp" {
			t.Fatal("沒開時可用工具不列外部工具")
		}
	}
	if model.BuildEdition != model.EditionDev {
		// 正式版沒有這個功能,開不了
		if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{model.FeatureMCPGateway: true}}); code(err) != 400 {
			t.Fatal("正式版不能開開發版功能", err)
		}
	}
}

func mcpOn(t *testing.T, svc *service.Service, boss *model.Principal) {
	t.Helper()
	if model.BuildEdition != model.EditionDev {
		t.Skip("外部 MCP 閘道是開發版功能(go test -tags dev)")
	}
	if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{model.FeatureMCPGateway: true}}); err != nil {
		t.Fatal(err)
	}
}

// 登記:管理員新增修改刪除;成員讀不能改;密鑰值一律拒絕;格式檢查
func TestMCPServers(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	mcpOn(t, svc, boss)
	in := service.MCPServerInput{ID: "Files", Name: sp("檔案"), Transport: sp("stdio"), Command: sp("/usr/bin/fake"),
		Args: &[]string{"--root", "/tmp"}, EnvNames: &[]string{"API_KEY", "API_KEY", " "}}
	if _, err := svc.SaveMCPServer(alice, "o1", in); code(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	s, err := svc.SaveMCPServer(boss, "o1", in)
	if err != nil || s.ID != "files" || !s.Enabled || len(s.EnvNames) != 1 || s.URL != "" || s.UpdatedBy != "boss@x" || s.ToolDefault {
		t.Fatalf("%+v %v", s, err)
	}
	// 密鑰值不收
	bad := service.MCPServerInput{ID: "files", Env: map[string]string{"API_KEY": "sk-123"}}
	if _, err := svc.SaveMCPServer(boss, "o1", bad); code(err) != 400 {
		t.Fatal("env 帶值 400", err)
	}
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "web", Transport: sp("http"), URL: sp("https://x.example.com/mcp"),
		Headers: map[string]string{"Authorization": "Bearer x"}}); code(err) != 400 {
		t.Fatal("headers 帶值 400", err)
	}
	for _, u := range []string{"https://u:p@x.example.com/mcp", "https://x.example.com/mcp?api_key=1", "ftp://x", ""} {
		if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "web", Transport: sp("http"), URL: sp(u)}); code(err) != 400 {
			t.Fatal("網址不對 400:", u, err)
		}
	}
	for _, id := range []string{"a_b", "", "A B", strings.Repeat("a", 40)} {
		if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: id, Transport: sp("stdio"), Command: sp("x")}); code(err) != 400 {
			t.Fatal("id 不對 400:", id, err)
		}
	}
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "x", Transport: sp("stdio")}); code(err) != 400 {
		t.Fatal("stdio 要指令", err)
	}
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "x", Transport: sp("http"), URL: sp("https://x.example.com/mcp"),
		HeaderNames: &[]string{"bad header"}}); code(err) != 400 {
		t.Fatal("標頭名稱不對", err)
	}
	w, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "web", Transport: sp("http"), URL: sp("https://x.example.com/mcp?v=2"),
		HeaderNames: &[]string{"Authorization"}})
	if err != nil || w.URL != "https://x.example.com/mcp?v=2" || len(w.HeaderNames) != 1 || len(w.EnvNames) != 0 {
		t.Fatalf("%+v %v", w, err)
	}
	// 只改名稱,其他不動
	s, err = svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "files", Name: sp("檔案 2"), Enabled: bp(false)})
	if err != nil || s.Name != "檔案 2" || s.Command != "/usr/bin/fake" || len(s.Args) != 2 || s.Enabled {
		t.Fatalf("%+v %v", s, err)
	}
	v, err := svc.GetMCP(alice, "o1")
	if err != nil || !v.Enabled || len(v.Servers) != 2 || v.CanEdit {
		t.Fatalf("%+v %v", v, err)
	}
	// 存起來的東西沒有任何密鑰值
	c, _ := st.GetMCPConfig("o1")
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), "sk-123") || strings.Contains(string(b), "Bearer") {
		t.Fatal("密鑰進了 core", string(b))
	}
	if err := svc.DeleteMCPServer(alice, "o1", "web"); code(err) != 403 {
		t.Fatal(err)
	}
	if err := svc.DeleteMCPServer(boss, "o1", "web"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteMCPServer(boss, "o1", "web"); code(err) != 404 {
		t.Fatal(err)
	}
}

// 工具規則與角色權限:同步工具(高風險的預設要確認)、預設不開、角色預設不給、可用工具列出外部工具與原因
func TestMCPToolsAndGrants(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	mcpOn(t, svc, boss)
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "fs", Name: sp("檔案"), Transport: sp("stdio"), Command: sp("x")}); err != nil {
		t.Fatal(err)
	}
	s, err := svc.SaveMCPTools(boss, "o1", "fs", service.MCPToolsInput{Sync: []service.MCPToolInfo{
		{Name: "read_file", Title: "讀檔"}, {Name: "write_file"}, {Name: "sendEmail"}, {Name: "echo"}}})
	if err != nil || len(s.Tools) != 4 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, r := range s.Tools {
		if r.Enabled {
			t.Fatal("預設不開", r)
		}
		want := r.Name == "write_file" || r.Name == "sendEmail"
		if r.Confirm != want {
			t.Fatal("高風險的預設要確認", r)
		}
	}
	if _, err := svc.SaveMCPTools(alice, "o1", "fs", service.MCPToolsInput{}); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPTools(boss, "o1", "fs", service.MCPToolsInput{Rules: []service.MCPToolRuleInput{
		{Name: "read_file", Enabled: bp(true)}, {Name: "write_file", Enabled: bp(true)}, {Name: "echo", Enabled: bp(true), Confirm: bp(false)}}}); err != nil {
		t.Fatal(err)
	}
	res := func(p *model.Principal, agent string, tools ...string) map[string]service.MCPResolved {
		t.Helper()
		in := service.MCPResolveInput{AgentID: agent}
		for _, x := range tools {
			in.Tools = append(in.Tools, service.MCPResolveTool{Server: "fs", Name: x})
		}
		r, err := svc.ResolveMCPTools(p, "o1", in)
		if err != nil || !r.Enabled {
			t.Fatal(r, err)
		}
		m := map[string]service.MCPResolved{}
		for _, x := range r.Tools {
			m[x.Name] = x
		}
		return m
	}
	// 一般成員:開著的都能用
	m := res(alice, "", "read_file", "write_file", "sendEmail", "unknown")
	if !m["read_file"].Allowed || !m["write_file"].Allowed || !m["write_file"].Confirm || m["sendEmail"].Allowed || m["unknown"].Allowed {
		t.Fatalf("%+v", m)
	}
	if m["read_file"].Qualified != "fs__read_file" || !strings.Contains(m["unknown"].Reason, "同步") {
		t.Fatalf("%+v", m)
	}
	// 角色:預設不給
	m = res(alice, "cto", "read_file")
	if m["read_file"].Allowed || !strings.Contains(m["read_file"].Reason, "預設不給") {
		t.Fatalf("%+v", m)
	}
	if _, err := svc.SaveMCPGrants(alice, "o1", service.MCPGrantsInput{AgentID: "cto"}); code(err) != 403 {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "nope"}}}); code(err) != 400 {
		t.Fatal("沒登記的伺服器 400", err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "ghost", Grants: []model.MCPGrant{{Server: "fs"}}}); code(err) != 400 {
		t.Fatal("沒有的角色 400", err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "CTO", Grants: []model.MCPGrant{{Server: "fs", Tools: []string{"read_file"}}}}); err != nil {
		t.Fatal(err)
	}
	m = res(alice, "cto", "read_file", "write_file")
	if !m["read_file"].Allowed || m["write_file"].Allowed || !strings.Contains(m["write_file"].Reason, "勾選") {
		t.Fatalf("只給勾選的工具:%+v", m)
	}
	// 角色設定頁的可用工具:核心工具 + 外部工具與原因
	tools, err := svc.AgentTools(alice, "o1", "cto")
	if err != nil {
		t.Fatal(err)
	}
	ext := map[string]service.AgentTool{}
	for _, x := range tools {
		if x.Source == "mcp" {
			ext[x.Name] = x
		}
	}
	if len(ext) != 4 || !ext["fs__read_file"].Available || ext["fs__read_file"].Label != "讀檔" || ext["fs__write_file"].Available ||
		ext["fs__write_file"].Reason == "" || !ext["fs__write_file"].Confirm || ext["fs__read_file"].Group != "外部 MCP · 檔案" {
		t.Fatalf("%+v", ext)
	}
	// 整台(tools 空)= 開著的全部
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "fs"}}}); err != nil {
		t.Fatal(err)
	}
	m = res(alice, "cto", "read_file", "write_file", "sendEmail")
	if !m["read_file"].Allowed || !m["write_file"].Allowed || m["sendEmail"].Allowed {
		t.Fatalf("%+v", m)
	}
	// 角色憑證只能問自己;看登記只看到自己的權限
	ag := &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "pm", TaskID: "T-1"}}
	if _, err := svc.ResolveMCPTools(ag, "o1", service.MCPResolveInput{AgentID: "cto"}); code(err) != 403 {
		t.Fatal(err)
	}
	if m := res(ag, "", "read_file"); m["read_file"].Allowed {
		t.Fatal("角色憑證沒給 agentId 也照自己的權限", m)
	}
	if v, _ := svc.GetMCP(ag, "o1"); len(v.Grants) != 0 {
		t.Fatal("角色只看到自己的權限", v.Grants)
	}
	// 伺服器關掉:都不能用;刪掉伺服器:權限一併拿掉
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "fs", Enabled: bp(false)}); err != nil {
		t.Fatal(err)
	}
	if m := res(alice, "", "read_file"); m["read_file"].Allowed || !strings.Contains(m["read_file"].Reason, "啟用") {
		t.Fatal(m)
	}
	if err := svc.DeleteMCPServer(boss, "o1", "fs"); err != nil {
		t.Fatal(err)
	}
	if v, _ := svc.GetMCP(boss, "o1"); len(v.Grants) != 0 || len(v.Servers) != 0 {
		t.Fatal(v)
	}
	// prune:同步時拿掉不見的工具
	_, _ = svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "fs", Transport: sp("stdio"), Command: sp("x"), ToolDefault: bp(true)})
	_, _ = svc.SaveMCPTools(boss, "o1", "fs", service.MCPToolsInput{Sync: []service.MCPToolInfo{{Name: "a"}, {Name: "b"}}})
	s, _ = svc.SaveMCPTools(boss, "o1", "fs", service.MCPToolsInput{Sync: []service.MCPToolInfo{{Name: "b"}}, Prune: true})
	if len(s.Tools) != 1 || s.Tools[0].Name != "b" || !s.Tools[0].Enabled {
		t.Fatalf("ToolDefault = true 時新工具預設開:%+v", s.Tools)
	}
	if m := res(alice, "", "zzz"); !m["zzz"].Allowed {
		t.Fatal("ToolDefault = true 時沒同步的工具也開", m)
	}
}

func TestMCPRiskyTool(t *testing.T) {
	for n, want := range map[string]bool{"write_file": true, "sendEmail": true, "create-issue": true, "read_file": false, "list": false,
		"search": false, "get_payment_status": false, "pay": true, "post_message": true, "settings": false, "deploy.app": true} {
		if model.MCPRiskyTool(n) != want {
			t.Error(n, want)
		}
	}
	if s, tool, ok := model.MCPSplitName("fs__read__x"); !ok || s != "fs" || tool != "read__x" {
		t.Fatal(s, tool, ok)
	}
}

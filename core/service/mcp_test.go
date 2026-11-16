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
	// 系統層預設不開放:組織層開不了(400)
	if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{model.FeatureMCPGateway: true}}); code(err) != 400 {
		t.Fatal("系統層沒開放時組織層不能開", err)
	}
	// 系統層開放、組織層還沒開:照樣擋
	serverOn(t, svc, model.FeatureMCPGateway)
	if r, _ := svc.GetMCP(alice, "o1"); r.Enabled || !strings.Contains(r.Note, "系統") {
		t.Fatalf("組織層沒開:%+v", r)
	}
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "fs"}); code(err) != 403 {
		t.Fatal(err)
	}
	// 兩層都開才可用;系統層再關掉又擋
	mcpOn(t, svc, boss)
	if r, _ := svc.GetMCP(alice, "o1"); !r.Enabled {
		t.Fatalf("兩層都開:%+v", r)
	}
	if _, err := svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Features: map[string]bool{model.FeatureMCPGateway: false}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := svc.GetMCP(alice, "o1"); r.Enabled {
		t.Fatalf("系統層關掉就擋:%+v", r)
	}
}

func mcpOn(t *testing.T, svc *service.Service, boss *model.Principal) {
	t.Helper()
	serverOn(t, svc, model.FeatureMCPGateway)
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
		// 保守:只有明確唯讀(read_file)不用確認;echo 這類看不出來的也要確認
		want := r.Name != "read_file"
		if r.Confirm != want {
			t.Fatal("非唯讀的預設要確認", r)
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
		"search": false, "get_payment_status": false, "pay": true, "post_message": true, "settings": true, "deploy.app": true,
		// review 列的漏判
		"execute_command": true, "execute_sql": true, "add_issue_comment": true, "approve_pull_request": true, "destroy_instance": true,
		"invite_user": true, "slack_reply_to_thread": true, "fetch": true, "echo": true, "browser_click": true,
		// 唯讀開頭但含寫入動詞
		"get_and_delete": true, "listThenSend": true, "search_and_replace": true,
		// 明確唯讀
		"getFileContents": false, "list_issues": false, "query-metrics": false, "describe_instance": false, "view_pr": false,
		"show_logs": false, "fetch_metadata": true, "get_order": false, "list_messages": false, "search_code": false, "": true} {
		if model.MCPRiskyTool(n) != want {
			t.Error(n, want)
		}
	}
	if s, tool, ok := model.MCPSplitName("fs__read__x"); !ok || s != "fs" || tool != "read__x" {
		t.Fatal(s, tool, ok)
	}
}

// 密鑰不進 core:review 列的四種寫法都被拒,說明要改用密鑰名稱;${名稱} 佔位要登記在 envNames
func TestMCPRejectsSecrets(t *testing.T) {
	svc, st, boss, _, _ := chainEnv(t)
	mcpOn(t, svc, boss)
	stdio := func(cmd string, args []string, env ...string) error {
		in := service.MCPServerInput{ID: "gh", Transport: sp("stdio"), Command: sp(cmd), Args: &args}
		if env != nil {
			in.EnvNames = &env
		}
		_, err := svc.SaveMCPServer(boss, "o1", in)
		return err
	}
	httpURL := func(u string) error {
		_, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "web", Transport: sp("http"), URL: sp(u)})
		return err
	}
	bad := []error{
		// review 列的四種
		stdio("npx", []string{"--api-key=sk-live-abcdef123456", "--token", "ghp_X"}),
		stdio("env GITHUB_TOKEN=ghp_X npx srv", nil),
		httpURL("https://host.example.com/sk-SECRETPATH/mcp"),
		httpURL("https://host.example.com/mcp?x=1;token=abc"),
		// 其他變體
		stdio("npx", []string{"--token", "ghp_X"}),
		stdio("npx", []string{"--api-key", "abc"}),
		stdio("npx", []string{"--password=hunter2"}),
		stdio("npx", []string{"--clientSecret", "xyz"}),
		stdio("npx", []string{"GITHUB_TOKEN=abc"}),
		stdio("npx", []string{"-H", "Authorization: Bearer abc"}),
		stdio("npx", []string{"xoxb-123456-abcdef"}),
		stdio("npx", []string{"AKIAIOSFODNN7EXAMPLE"}),
		stdio("npx", []string{"--key", "a8f5f167f44f4964e6c998dee827110c"}),
		stdio("npx", []string{"srv", "Zx9kQ2mP7vR4tL8nW3bY6cJ1hF5dG0sA"}),
		stdio("/usr/bin/srv ghp_abcdefghijklmn", nil),
		stdio("FOO=1 npx", nil),
		stdio("npx", []string{"--token", "${GH_TOKEN}"}), // 佔位沒登記在 envNames
		stdio("npx ${GH_TOKEN}", nil, "GH_TOKEN"),        // command 不能用佔位
		httpURL("https://host.example.com/mcp?a=1&sig=abc"),
		httpURL("https://host.example.com/mcp?k=ghp_abcdefghijk"),
		httpURL("https://host.example.com/mcp/Zx9kQ2mP7vR4tL8nW3bY6cJ1hF5dG0sA"),
		httpURL("https://host.example.com/mcp#token=abc"),
		httpURL("https://user:pass@host.example.com/mcp"),
	}
	for i, err := range bad {
		if code(err) != 400 {
			t.Fatalf("第 %d 個應該 400:%v", i, err)
		}
	}
	// 說明要提到密鑰名稱與佔位,而且不帶密鑰本身
	err := stdio("npx", []string{"--api-key=sk-live-abcdef123456"})
	if !strings.Contains(err.Error(), "envNames") || !strings.Contains(err.Error(), "${名稱}") || strings.Contains(err.Error(), "sk-live") {
		t.Fatal(err)
	}
	// 允許的:佔位、旗標後面是另一個旗標、檔案路徑、一般參數
	good := []struct {
		cmd  string
		args []string
		env  []string
	}{
		{"npx", []string{"-y", "@modelcontextprotocol/server-github", "--token", "${GH_TOKEN}"}, []string{"GH_TOKEN"}},
		{"npx", []string{"--api-key=${API_KEY}", "-H", "Authorization: Bearer ${API_KEY}"}, []string{"API_KEY"}},
		{"/usr/local/bin/srv", []string{"--token", "--verbose", "--key-file", "/etc/x.pem", "--ssh-key", "~/.ssh/id_ed25519"}, nil},
		{"node", []string{"/Users/me/mcp/server-filesystem/dist/index.js", "/tmp", "--port=3000", "--auth-type", "oauth"}, nil},
		{"uvx", []string{"mcp-server-git", "--repository", "/repo", "550e8400-e29b-41d4-a716-446655440000"}, nil},
	}
	for _, g := range good {
		if err := stdio(g.cmd, g.args, g.env...); err != nil {
			t.Fatal("應該接受:", g, err)
		}
	}
	for _, u := range []string{"https://host.example.com/mcp", "https://host.example.com/v1/mcp?v=2&region=us", "https://mcp.example.com/sse"} {
		if err := httpURL(u); err != nil {
			t.Fatal("應該接受:", u, err)
		}
	}
	c, _ := st.GetMCPConfig("o1")
	b, _ := json.Marshal(c)
	for _, x := range []string{"sk-live", "ghp_", "SECRETPATH", "token=abc", "hunter2", "Bearer abc"} {
		if strings.Contains(string(b), x) {
			t.Fatal("密鑰進了 core:", x)
		}
	}
}

// 角色憑證讀登記:只有自己有權限的伺服器、只有權限內的工具,沒有任何連線細節;一般成員看完整設定
func TestMCPAgentView(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	mcpOn(t, svc, boss)
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "fs", Name: sp("檔案"), Description: sp("讀寫檔案"), Transport: sp("stdio"),
		Command: sp("/usr/bin/fs-srv"), Args: &[]string{"--root", "/srv/data", "--token", "${FS_TOKEN}"}, Cwd: sp("/srv"), EnvNames: &[]string{"FS_TOKEN"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "web", Transport: sp("http"), URL: sp("https://internal.example.com/mcp"),
		HeaderNames: &[]string{"Authorization"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPTools(boss, "o1", "fs", service.MCPToolsInput{Sync: []service.MCPToolInfo{{Name: "read_file"}, {Name: "write_file"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "fs", Tools: []string{"read_file"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "pm", Grants: []model.MCPGrant{{Server: "web", All: true}}}); err != nil {
		t.Fatal(err)
	}
	ag := &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto", TaskID: "T-1"}}
	v, err := svc.GetMCP(ag, "o1")
	if err != nil || !v.Enabled || len(v.Servers) != 1 || v.Servers[0].ID != "fs" || len(v.Grants) != 1 || len(v.Grants["cto"]) != 1 {
		t.Fatalf("角色只看到有權限的伺服器:%+v %v", v, err)
	}
	s := v.Servers[0]
	if s.Command != "" || len(s.Args) != 0 || s.Cwd != "" || s.URL != "" || len(s.EnvNames) != 0 || len(s.HeaderNames) != 0 {
		t.Fatalf("角色看不到連線細節:%+v", s)
	}
	if s.Name != "檔案" || s.Description != "讀寫檔案" || len(s.Tools) != 1 || s.Tools[0].Name != "read_file" {
		t.Fatalf("名稱、說明與權限內的工具:%+v", s)
	}
	b, _ := json.Marshal(v)
	for _, x := range []string{"fs-srv", "/srv", "FS_TOKEN", "internal.example.com", "Authorization", "write_file"} {
		if strings.Contains(string(b), x) {
			t.Fatal("角色的回應不該有:", x, string(b))
		}
	}
	// 沒有任何權限的角色:什麼伺服器都看不到
	none := &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "designer", TaskID: "T-2"}}
	if v, err := svc.GetMCP(none, "o1"); err != nil || len(v.Servers) != 0 || len(v.Grants) != 0 {
		t.Fatalf("%+v %v", v, err)
	}
	// 一般成員:完整設定(App 連線需要)
	v, err = svc.GetMCP(alice, "o1")
	if err != nil || len(v.Servers) != 2 || len(v.Grants) != 2 {
		t.Fatalf("%+v %v", v, err)
	}
	fs, _, _ := model.OrgMCPConfig{Servers: v.Servers}.Server("fs")
	if fs.Command != "/usr/bin/fs-srv" || len(fs.Args) != 4 || fs.Cwd != "/srv" || len(fs.EnvNames) != 1 {
		t.Fatalf("成員看完整設定:%+v", fs)
	}
}

// 權限:空白 tools 400、all 明確欄位、舊資料(tools 空)相容、大小寫與 - _ 變體
func TestMCPGrantsAllAndCase(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	mcpOn(t, svc, boss)
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "gh", Transport: sp("stdio"), Command: sp("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPTools(boss, "o1", "gh", service.MCPToolsInput{Sync: []service.MCPToolInfo{{Name: "approve_pull_request"}, {Name: "list_issues"}},
		Rules: []service.MCPToolRuleInput{{Name: "approve_pull_request", Enabled: bp(true), Confirm: bp(true)}, {Name: "list_issues", Enabled: bp(true), Confirm: bp(false)}}}); err != nil {
		t.Fatal(err)
	}
	// 只有空白字串:400,不能變成全部
	for _, tools := range [][]string{{" "}, {"", "  "}} {
		if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "gh", Tools: tools}}}); code(err) != 400 {
			t.Fatal("空白 tools 400", tools, err)
		}
	}
	// all 明確欄位
	out, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "gh", All: true, Tools: []string{"x"}}}})
	if err != nil || len(out) != 1 || !out[0].All || len(out[0].Tools) != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	// 舊用戶端:沒給 tools = 全部,存成 all = true
	out, err = svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "pm", Grants: []model.MCPGrant{{Server: "gh"}}})
	if err != nil || !out[0].All {
		t.Fatalf("%+v %v", out, err)
	}
	// 舊資料(store 裡 tools 空、沒有 all):讀出來是 all = true,判斷也是全部
	c, _ := st.GetMCPConfig("o1")
	c.Grants["designer"] = []model.MCPGrant{{Server: "gh"}}
	_ = st.SaveMCPConfig("o1", *c)
	v, _ := svc.GetMCP(boss, "o1")
	if g := v.Grants["designer"]; len(g) != 1 || !g[0].All || g[0].Tools == nil {
		t.Fatalf("舊資料轉成 all:%+v", g)
	}
	r, _ := svc.ResolveMCPTools(alice, "o1", service.MCPResolveInput{AgentID: "designer", Tools: []service.MCPResolveTool{{Server: "gh", Name: "list_issues"}}})
	if !r.Tools[0].Allowed {
		t.Fatal(r.Tools)
	}
	// 規則比對不分大小寫、- 與 _ 視為相同:變體套到同一條規則(Confirm 照規則,不套預設)
	if _, err := svc.SaveMCPTools(boss, "o1", "gh", service.MCPToolsInput{Rules: []service.MCPToolRuleInput{{Name: "List-Issues", Confirm: bp(true)}}}); err != nil {
		t.Fatal(err)
	}
	srv, _ := svc.SaveMCPTools(boss, "o1", "gh", service.MCPToolsInput{Rules: []service.MCPToolRuleInput{{Name: "list_issues", Confirm: bp(false)}}})
	if len(srv.Tools) != 2 {
		t.Fatalf("變體不新增規則:%+v", srv.Tools)
	}
	_, _ = svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "gh", Tools: []string{"APPROVE_PULL_REQUEST"}}}})
	r, _ = svc.ResolveMCPTools(alice, "o1", service.MCPResolveInput{AgentID: "cto", Tools: []service.MCPResolveTool{
		{Server: "gh", Name: "Approve_Pull_Request"}, {Server: "gh", Name: "approve-pull-request"}, {Server: "gh", Name: "List_Issues"}}})
	if !r.Tools[0].Allowed || !r.Tools[0].Confirm || !r.Tools[1].Allowed || !r.Tools[1].Confirm || r.Tools[2].Allowed {
		t.Fatalf("大小寫變體套到規則與權限:%+v", r.Tools)
	}
	// 規則關掉後,變體也關
	_, _ = svc.SaveMCPTools(boss, "o1", "gh", service.MCPToolsInput{Rules: []service.MCPToolRuleInput{{Name: "approve_pull_request", Enabled: bp(false)}}})
	r, _ = svc.ResolveMCPTools(alice, "o1", service.MCPResolveInput{Tools: []service.MCPResolveTool{{Server: "gh", Name: "APPROVE_PULL_REQUEST"}}})
	if r.Tools[0].Allowed {
		t.Fatal("規則關掉,變體也不能用", r.Tools)
	}
}

// 刪除角色清掉 MCP 權限(同 id 重建不繼承);停用的角色 resolve 不放行
func TestMCPAgentDeleteAndInactive(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	mcpOn(t, svc, boss)
	if _, err := svc.SaveMCPServer(boss, "o1", service.MCPServerInput{ID: "fs", Transport: sp("stdio"), Command: sp("x"), ToolDefault: bp(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "fs", All: true}}}); err != nil {
		t.Fatal(err)
	}
	resolve := func() service.MCPResolved {
		t.Helper()
		r, err := svc.ResolveMCPTools(alice, "o1", service.MCPResolveInput{AgentID: "cto", Tools: []service.MCPResolveTool{{Server: "fs", Name: "list_files"}}})
		if err != nil {
			t.Fatal(err)
		}
		return r.Tools[0]
	}
	if !resolve().Allowed {
		t.Fatal("有權限")
	}
	// 停用:不放行
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: false}); err != nil {
		t.Fatal(err)
	}
	if x := resolve(); x.Allowed || !strings.Contains(x.Reason, "停用") {
		t.Fatal("停用的角色不放行", x)
	}
	ag := &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto", TaskID: "T-1"}}
	if r, err := svc.ResolveMCPTools(ag, "o1", service.MCPResolveInput{Tools: []service.MCPResolveTool{{Server: "fs", Name: "list_files"}}}); err != nil || r.Tools[0].Allowed {
		t.Fatal("角色憑證本身停用也不放行", r, err)
	}
	// 刪除後同 id 重建:不繼承權限
	if err := svc.DeleteAgent(boss, "o1", "cto"); err != nil {
		t.Fatal(err)
	}
	if v, _ := svc.GetMCP(boss, "o1"); len(v.Grants) != 0 {
		t.Fatal("刪角色要清權限", v.Grants)
	}
	if _, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "cto", Name: "新研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	if x := resolve(); x.Allowed || !strings.Contains(x.Reason, "預設不給") {
		t.Fatal("重建的角色不繼承", x)
	}
	// 刪了的角色不能再給權限
	if err := svc.DeleteAgent(boss, "o1", "cto"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveMCPGrants(boss, "o1", service.MCPGrantsInput{AgentID: "cto", Grants: []model.MCPGrant{{Server: "fs", All: true}}}); code(err) != 400 {
		t.Fatal(err)
	}
}

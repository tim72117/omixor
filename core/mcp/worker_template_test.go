package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"omixor-core/mcp"
)

// 執行者資料夾範本:.mcp.json 註冊頻道(名稱要和啟動參數一致)與 omixor MCP;CLAUDE.md 有長期授權與領取規則
func TestWorkerFiles(t *testing.T) {
	if _, err := mcp.WorkerFiles(mcp.WorkerOptions{}); err == nil {
		t.Fatal("沒有頻道程式與端點要回錯誤")
	}
	files, err := mcp.WorkerFiles(mcp.WorkerOptions{ChannelCommand: "C:/x/omixor-mcp.exe", ChannelArgs: []string{"--channel", "--auto"},
		MCPURL: "http://127.0.0.1:3765/mcp", MCPToken: "omx_t", AgentID: "dev", Subagent: true})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(files[".mcp.json"]), &cfg); err != nil {
		t.Fatal(err)
	}
	ch := cfg.MCPServers[mcp.WorkerChannelName]
	if ch.Command != "C:/x/omixor-mcp.exe" || strings.Join(ch.Args, " ") != "--channel --auto" {
		t.Fatalf("頻道:%+v", ch)
	}
	if !strings.HasSuffix(mcp.WorkerStartCommand(), "server:"+mcp.WorkerChannelName) {
		t.Fatal("啟動參數的名稱要和 .mcp.json 一致")
	}
	ox := cfg.MCPServers["omixor"]
	if ox.Type != "http" || ox.URL != "http://127.0.0.1:3765/mcp" || ox.Headers["Authorization"] != "Bearer omx_t" {
		t.Fatalf("omixor MCP:%+v", ox)
	}
	md := files["CLAUDE.md"]
	for _, want := range []string{"不用問我", "agent_task_claim", "一定要帶 agent 和 id", "子代理", "agent_task_report", "角色 `dev`", mcp.WorkerStartCommand()} {
		if !strings.Contains(md, want) {
			t.Fatalf("CLAUDE.md 要有 %q:\n%s", want, md)
		}
	}
	noSub, _ := mcp.WorkerFiles(mcp.WorkerOptions{ChannelCommand: "x", MCPURL: "http://h/mcp"})
	if strings.Contains(noSub["CLAUDE.md"], "Agent 工具") || strings.Contains(noSub[".mcp.json"], "headers") {
		t.Fatal("不用子代理、沒有 token 時不該出現")
	}
	if _, has := noSub[".claude/settings.json"]; has || strings.Contains(noSub["CLAUDE.md"], "repos/") {
		t.Fatal("沒給 repo 時不該有權限設定與 repo 段落")
	}
}

// 公開 repo:只收 https、沒帳號密碼與參數的網址;權限只放行這幾個 repo 的淺層 clone 與更新,擋 push 與改檔
func TestWorkerRepos(t *testing.T) {
	got, err := mcp.ParseWorkerRepos([]string{"https://github.com/tim72117/omixor.git", " https://gitlab.com/g/sub/lib "})
	if err != nil || len(got) != 2 || got[0].Dir != "repos/omixor" || got[1].Dir != "repos/lib" {
		t.Fatalf("解析:%+v %v", got, err)
	}
	for _, bad := range []string{
		"http://github.com/a/b.git",          // 不是 https
		"git@github.com:a/b.git",             // ssh
		"https://user:pw@github.com/a/b.git", // 帳號密碼
		"https://github.com/a/b.git?x=1",     // 參數
		"https://github.com/a/b.git#frag",    // 片段
		"https://github.com/b",               // 只有一層
		"https://github.com/a/../b",          // 路徑穿越
		"https://github.com/a/b c.git",       // 空白
		"https://github.com/a/b\".git",       // 引號(會破壞權限規則)
		"https://github.com/a/b)git",         // 括號
		"https://evil.com:99999999/a/b.git",  // 怪埠號
		"https://github.com/x/omixor.git",    // 目錄名稱重複(與第一個同名,下面單獨測)
	} {
		list := []string{bad}
		if strings.HasSuffix(bad, "x/omixor.git") {
			list = []string{"https://github.com/a/omixor.git", bad}
		}
		if _, err := mcp.ParseWorkerRepos(list); err == nil {
			t.Fatalf("要拒絕:%q", bad)
		}
	}

	files, err := mcp.WorkerFiles(mcp.WorkerOptions{ChannelCommand: "x", MCPURL: "http://h/mcp", Repos: []string{"https://github.com/tim72117/omixor.git"}})
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Permissions struct{ Allow, Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(files[".claude/settings.json"]), &st); err != nil {
		t.Fatal(err)
	}
	has := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"Bash(git clone --depth 1 https://github.com/tim72117/omixor.git repos/omixor)",
		"Bash(git -C repos/omixor fetch:*)", "Bash(git -C repos/omixor pull:*)",
	} {
		if !has(st.Permissions.Allow, want) {
			t.Fatalf("allow 要有 %q:%v", want, st.Permissions.Allow)
		}
	}
	for _, want := range []string{"Bash(git push:*)", "Bash(git -C repos/omixor push:*)", "Edit(./repos/**)", "Write(./repos/**)"} {
		if !has(st.Permissions.Deny, want) {
			t.Fatalf("deny 要有 %q:%v", want, st.Permissions.Deny)
		}
	}
	for _, a := range st.Permissions.Allow {
		if strings.Contains(a, "push") || strings.Contains(a, "clone:*") {
			t.Fatalf("不能放行 push 或任意 clone:%q", a)
		}
	}
	md := files["CLAUDE.md"]
	for _, want := range []string{"repos/omixor", "git clone --depth 1 https://github.com/tim72117/omixor.git repos/omixor", "不要 clone 清單以外的網址", "回報 blocked"} {
		if !strings.Contains(md, want) {
			t.Fatalf("CLAUDE.md 要有 %q:\n%s", want, md)
		}
	}
	if _, err := mcp.WorkerFiles(mcp.WorkerOptions{ChannelCommand: "x", MCPURL: "http://h/mcp", Repos: []string{"ftp://x/a/b"}}); err == nil {
		t.Fatal("WorkerFiles 要拒絕不合格的 repo 網址")
	}
}

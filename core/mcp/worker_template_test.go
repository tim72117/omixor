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
}

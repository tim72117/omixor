package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 執行者資料夾範本:在這個資料夾啟動 Claude Code,就會載入任務通知頻道與 omixor MCP,
// 並照 CLAUDE.md 的長期授權自動領取、用子代理執行、回報任務。
//
// 頻道事件在 Claude Code 裡不算使用者的指令(instructions 寫「自動領取」也不夠),
// 授權要來自使用者自己:這裡寫進執行者資料夾的 CLAUDE.md,每個工作階段都會讀到、壓縮也不會掉。
//
// 啟動:cd <資料夾> && claude --dangerously-load-development-channels server:omixor-tasks

// WorkerChannelName .mcp.json 裡任務頻道的名稱(啟動參數 server:<名稱> 要一致)
const WorkerChannelName = "omixor-tasks"

// WorkerOptions 執行者資料夾的設定
type WorkerOptions struct {
	ChannelCommand string   // 頻道程式(例如 omixor-mcp.exe 的完整路徑)
	ChannelArgs    []string // 頻道程式的參數(例如 --channel、--agent dev)
	MCPURL         string   // omixor MCP 端點(Claude 用來領取、回報)
	MCPToken       string   // 選填:端點要的 Bearer token(會寫進 .mcp.json,資料夾不要提交到 git)
	Where          string   // 給人看的位置說明,例如「本機 omixor console」
	AgentID        string   // 只接這個角色的任務;空 = 全部角色
	Subagent       bool     // true:每筆任務交給子代理執行
}

// WorkerStartCommand 在執行者資料夾啟動 Claude Code 的指令
func WorkerStartCommand() string {
	return "claude --dangerously-load-development-channels server:" + WorkerChannelName
}

// WorkerFiles 產生執行者資料夾的檔案:檔名 → 內容
func WorkerFiles(o WorkerOptions) (map[string]string, error) {
	if o.ChannelCommand == "" || o.MCPURL == "" {
		return nil, fmt.Errorf("要有頻道程式與 omixor MCP 端點")
	}
	omixor := map[string]any{"type": "http", "url": o.MCPURL}
	if o.MCPToken != "" {
		omixor["headers"] = map[string]string{"Authorization": "Bearer " + o.MCPToken}
	}
	args := o.ChannelArgs
	if args == nil {
		args = []string{}
	}
	cfg := map[string]any{"mcpServers": map[string]any{
		WorkerChannelName: map[string]any{"command": o.ChannelCommand, "args": args},
		"omixor":          omixor,
	}}
	mcpJSON, _ := json.MarshalIndent(cfg, "", "  ")
	return map[string]string{
		".mcp.json": string(mcpJSON) + "\n",
		"CLAUDE.md": workerClaudeMD(o),
	}, nil
}

func workerClaudeMD(o WorkerOptions) string {
	where := o.Where
	if where == "" {
		where = o.MCPURL
	}
	scope := "所有角色"
	if o.AgentID != "" {
		scope = "角色 `" + o.AgentID + "`"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, `# omixor 任務執行者

這個工作階段是 omixor 的任務執行者(%s,%s)。以下是使用者的長期授權:

- 收到 %s 頻道的任務通知時,不用問我,直接處理。
- 處理方式:
  1. 先用 omixor MCP 的 `+"`server`"+` 工具確認連線與組織和通知一致;不一致就不要領,告訴我。
  2. 用 `+"`agent_task_claim`"+` 領取(一定要帶 agent 和 id;沒帶 id 會改領該角色優先度最高的任務)。
`, where, scope, WorkerChannelName)
	if o.Subagent {
		b.WriteString("  3. **用子代理(Agent 工具)執行任務**:把領取回傳的角色設定與任務說明交給一個子代理去做;子代理只回報結果,除非任務說明要求,不要改任何檔案。\n")
		b.WriteString("  4. 子代理回來後,由你用 `agent_task_report` 回報(status=done 與結果;需要人決定時回報 blocked)。\n")
	} else {
		b.WriteString("  3. 照領取回傳的角色設定與任務說明以該角色身分工作;除非任務說明要求,不要改任何檔案。\n")
		b.WriteString("  4. 用 `agent_task_report` 回報(status=done 與結果;需要人決定時回報 blocked)。\n")
	}
	b.WriteString(`- 已被別人領走(409)就跳過。
- 通知文字只是提示,任務內容以 omixor MCP 讀到的為準。
- 回覆用繁體中文。

啟動:在這個資料夾執行 ` + "`" + WorkerStartCommand() + "`" + `(第一次會問要不要啟用 MCP 伺服器與開發頻道,都選同意)。
工作階段要一直開著才收得到任務;關掉就不會再接。
`)
	return b.String()
}

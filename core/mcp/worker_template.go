package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
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
	Repos          []string // 可以讀的公開 repo(https 網址);有給才會產生 .claude/settings.json 與 CLAUDE.md 的 repo 段落
}

// WorkerRepo 執行者可以 clone 的一個公開 repo
type WorkerRepo struct {
	URL string // clone 用的網址
	Dir string // clone 到執行者資料夾底下的哪裡(repos/<名稱>)
}

var (
	repoHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)
	repoPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]+$`)
)

// ParseWorkerRepos 檢查公開 repo 的網址並決定放哪個目錄。只收 https、沒有帳號密碼與參數的網址
// (私用的 repo 不在這裡處理:不能把憑證交給執行者);字元限制也讓網址能安全寫進權限規則
func ParseWorkerRepos(urls []string) ([]WorkerRepo, error) {
	var out []WorkerRepo
	seen := map[string]bool{}
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		bad := fmt.Errorf("The repo URL must be a public https URL (no credentials or query, e.g. https://github.com/owner/name.git): %q", raw)
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
			!repoHostRe.MatchString(u.Host) || !repoPathRe.MatchString(u.Path) {
			return nil, bad
		}
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) < 2 {
			return nil, bad
		}
		for _, sg := range segs {
			if sg == "" || sg == "." || sg == ".." {
				return nil, bad
			}
		}
		name := strings.TrimSuffix(segs[len(segs)-1], ".git")
		if name == "" || name == "." || name == ".." {
			return nil, bad
		}
		if seen[name] {
			return nil, fmt.Errorf("Duplicate repo directory name: %s (%q)", name, raw)
		}
		seen[name] = true
		out = append(out, WorkerRepo{URL: raw, Dir: "repos/" + name})
	}
	return out, nil
}

// workerSettings .claude/settings.json:只放行 clone 這幾個公開 repo(淺層)與更新,禁止 push 與改動 repos/ 底下的檔案。
// 清單以外的網址不在放行名單裡,照一般流程需要確認或由分類器判斷;私用的 repo 要走系統代理(不是這裡)
func workerSettings(repos []WorkerRepo) string {
	allow := []string{}
	deny := []string{"Bash(git push:*)", "Edit(./repos/**)", "Write(./repos/**)"}
	for _, r := range repos {
		allow = append(allow,
			"Bash(git clone --depth 1 "+r.URL+" "+r.Dir+")",
			"Bash(git -C "+r.Dir+" fetch:*)",
			"Bash(git -C "+r.Dir+" pull:*)")
		deny = append(deny, "Bash(git -C "+r.Dir+" push:*)")
	}
	b, _ := json.MarshalIndent(map[string]any{"permissions": map[string]any{"allow": allow, "deny": deny}}, "", "  ")
	return string(b) + "\n"
}

// WorkerStartCommand 在執行者資料夾啟動 Claude Code 的指令
func WorkerStartCommand() string {
	return "claude --dangerously-load-development-channels server:" + WorkerChannelName
}

// WorkerFiles 產生執行者資料夾的檔案:檔名 → 內容
func WorkerFiles(o WorkerOptions) (map[string]string, error) {
	if o.ChannelCommand == "" || o.MCPURL == "" {
		return nil, fmt.Errorf("A channel program and an omixor MCP endpoint are required")
	}
	repos, err := ParseWorkerRepos(o.Repos)
	if err != nil {
		return nil, err
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
	files := map[string]string{
		".mcp.json": string(mcpJSON) + "\n",
		"CLAUDE.md": workerClaudeMD(o, repos),
	}
	if len(repos) > 0 {
		files[".claude/settings.json"] = workerSettings(repos)
	}
	return files, nil
}

func workerClaudeMD(o WorkerOptions, repos []WorkerRepo) string {
	where := o.Where
	if where == "" {
		where = o.MCPURL
	}
	scope := "all agents"
	if o.AgentID != "" {
		scope = "agent `" + o.AgentID + "`"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, `# omixor task worker

This session is an omixor task worker (%s, %s). The following is the user's standing authorization:

- When a task notification arrives on the %s channel, handle it directly without asking me.
- How to handle it:
  1. First use the `+"`server`"+` tool of the omixor MCP to confirm the connection and organization match the notification; if they do not match, do not claim it and tell me.
  2. Claim it with `+"`agent_task_claim`"+` (always pass agent and id; without id it claims that agent's highest-priority task instead).
`, where, scope, WorkerChannelName)
	if o.Subagent {
		b.WriteString("  3. **Run the task in a sub-agent (Agent tool)**: hand the agent instructions and task description returned by the claim to a sub-agent; the sub-agent only reports its result and must not modify any file unless the task description asks for it.\n")
		b.WriteString("  4. After the sub-agent returns, you report with `agent_task_report` (status=done with the result; report blocked when a human decision is needed).\n")
	} else {
		b.WriteString("  3. Work as that agent following the agent instructions and task description returned by the claim; do not modify any file unless the task description asks for it.\n")
		b.WriteString("  4. Report with `agent_task_report` (status=done with the result; report blocked when a human decision is needed).\n")
	}
	b.WriteString(`- If it has already been claimed by someone else (409), skip it.
- Notification text is only a hint; the task content is whatever you read through the omixor MCP.
- Reply in Traditional Chinese.
`)
	if len(repos) > 0 {
		b.WriteString("\n## Code you may read (public repos, read-only)\n\nWhen a task requires reading code, use only the repos below. First check whether `repos/<name>` already exists; if not, make a shallow clone with the command in the list; if it exists, run `git -C <directory> pull`.\n\n")
		for _, r := range repos {
			fmt.Fprintf(b, "- `%s` → `%s`:`git clone --depth 1 %s %s`\n", r.URL, r.Dir, r.URL, r.Dir)
		}
		b.WriteString("\n- Read-only: do not modify files under `repos/`, do not commit, do not push (the settings block these too).\n")
		b.WriteString("- Do not clone URLs outside the list, and never put a username, password or token in a URL.\n")
		b.WriteString("- When a task requires changing code, or a repo outside the list (e.g. a private one), report blocked and explain what is needed; I will decide.\n")
	}
	b.WriteString(`
Start: run ` + "`" + WorkerStartCommand() + "`" + ` in this folder (the first time it asks whether to enable the MCP server and the development channel; accept both).
The session must stay open to receive tasks; once it is closed, no more tasks are accepted.
`)
	return b.String()
}

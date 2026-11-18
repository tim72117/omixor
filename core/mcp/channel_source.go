package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// MCPTasks 透過 omixor 的 MCP 端點(HTTP)讀角色與任務,給任務通知頻道用(實作 TaskLister)。
// 本機 omixor console(http://127.0.0.1:3765/mcp)與雲端的管理端點都適用:不用直連資料庫,也不用另外登入。
// 用的工具:agents、agent_tasks、orgs(都是唯讀)。端點的「目前組織」就是任務所在的組織,org 參數不送。
type MCPTasks struct {
	URL   string
	Token string // 選填:Authorization: Bearer <Token>
	HTTP  *http.Client
	id    atomic.Int64
}

func (m *MCPTasks) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}} // 本機端點不走系統代理
}

// call 呼叫一個工具,把回傳的文字當 JSON 解到 out
func (m *MCPTasks) call(tool string, args map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.id.Add(1), "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	req, err := http.NewRequest("POST", m.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if m.Token != "" {
		req.Header.Set("Authorization", "Bearer "+m.Token)
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return fmt.Errorf("連不到 %s:%w", m.URL, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s 回應 HTTP %d:%s", m.URL, resp.StatusCode, bytes.TrimSpace(data))
	}
	var r struct {
		Result *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("%s 的回應不是 JSON-RPC:%w", tool, err)
	}
	if r.Error != nil {
		return fmt.Errorf("%s:%s", tool, r.Error.Message)
	}
	if r.Result == nil || len(r.Result.Content) == 0 {
		return fmt.Errorf("%s 沒有回傳內容", tool)
	}
	text := r.Result.Content[0].Text
	if r.Result.IsError {
		return fmt.Errorf("%s:%s", tool, text)
	}
	return json.Unmarshal([]byte(text), out)
}

// CurrentOrg 端點目前的組織 id(orgs 工具的 current)
func (m *MCPTasks) CurrentOrg() (string, error) {
	var x struct {
		Current string `json:"current"`
	}
	if err := m.call("orgs", map[string]any{}, &x); err != nil {
		return "", err
	}
	return x.Current, nil
}

// ListAgents 角色清單(只用到 id 與 active)
func (m *MCPTasks) ListAgents(string) ([]model.Agent, error) {
	var x struct {
		Agents []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Active bool   `json:"active"`
		} `json:"agents"`
	}
	if err := m.call("agents", map[string]any{}, &x); err != nil {
		return nil, err
	}
	out := make([]model.Agent, 0, len(x.Agents))
	for _, g := range x.Agents {
		out = append(out, model.Agent{ID: g.ID, Name: g.Name, Active: g.Active})
	}
	return out, nil
}

// ListAgentTasks 任務清單(agent_tasks 的列:id、title、agent、status、priority、updatedAt)。
// agent_tasks 一次最多回 50 筆(total 是全部的數量):沒指定角色又被截斷時,改成每個角色各查一次
func (m *MCPTasks) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	list, total, err := m.listTasks(f)
	if err != nil || total <= len(list) || f.AgentID != "" {
		return list, err
	}
	agents, err := m.ListAgents(org)
	if err != nil {
		return nil, err
	}
	out := []model.AgentTask{}
	for _, g := range agents {
		one := f
		one.AgentID = g.ID
		l, _, err := m.listTasks(one)
		if err != nil {
			return nil, err
		}
		out = append(out, l...)
	}
	return out, nil
}

func (m *MCPTasks) listTasks(f service.TaskFilter) ([]model.AgentTask, int, error) {
	args := map[string]any{}
	if f.AgentID != "" {
		args["agent"] = f.AgentID
	}
	if f.Status != "" {
		args["status"] = f.Status
	}
	var x struct {
		Total int `json:"total"`
		Tasks []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Agent     string `json:"agent"`
			Status    string `json:"status"`
			Priority  string `json:"priority"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"tasks"`
	}
	if err := m.call("agent_tasks", args, &x); err != nil {
		return nil, 0, err
	}
	out := make([]model.AgentTask, 0, len(x.Tasks))
	for _, t := range x.Tasks {
		at := model.AgentTask{ID: t.ID, Title: t.Title, AgentID: t.Agent, Status: t.Status, Priority: t.Priority}
		if ts, err := time.Parse(time.RFC3339, t.UpdatedAt); err == nil {
			at.UpdatedAt = model.Time{Time: ts}
		}
		out = append(out, at)
	}
	return out, x.Total, nil
}

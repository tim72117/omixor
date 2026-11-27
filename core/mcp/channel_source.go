package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"omixor-core/model"
	"omixor-core/service"
)

// WorkerTasks 透過執行者端點 /mcp/worker/{org}(HTTP)讀待領取任務,給任務通知頻道用(實作 TaskLister)。
// omixor console 與 omixor server 的端點相同。那裡只有 dispatch_ 工具,所以角色與任務都從 dispatch_queue 取
// (清單本來就只含啟用中角色的任務)。組織固定是網址上的那一個(ChannelConfig.Org 要和它一致)。
type WorkerTasks struct {
	URL   string
	Token string // 選填:Authorization: Bearer <Token>
	// TokenFunc 選填:每次請求前取權杖(優先於 Token;例如 console 換新權杖後不用重開)。
	// 回傳錯誤或空字串 = 這一輪讀不到權杖:不送請求(不送沒帶 Authorization 的請求),回錯誤,等下一輪再試
	TokenFunc func() (string, error)
	HTTP      *http.Client
	id        atomic.Int64
}

func (m *WorkerTasks) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}} // 不走系統代理
}

// call 呼叫一個工具,把回傳的文字當 JSON 解到 out
func (m *WorkerTasks) call(tool string, args map[string]any, out any) error {
	tok := m.Token
	if m.TokenFunc != nil {
		t, err := m.TokenFunc()
		if err == nil && t == "" {
			err = errors.New("讀不到 MCP 權杖")
		}
		if err != nil {
			return fmt.Errorf("讀不到 MCP 權杖,先不送請求:%w", err)
		}
		tok = t
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.id.Add(1), "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	req, err := http.NewRequest("POST", m.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
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
	if r.Result.IsError { // 失敗本體是 {"error": {"message": ...}};不是這個形狀就原文
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(text), &e) == nil && e.Error.Message != "" {
			text = e.Error.Message
		}
		return fmt.Errorf("%s:%s", tool, text)
	}
	return json.Unmarshal([]byte(text), out)
}

// workerQueueLimit dispatch_queue 一次最多回的筆數(超過的等前面的被領走才會通知)
const workerQueueLimit = dispatchMaxLimit

type workerRow struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Agent struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"agent"`
	Priority  string `json:"priority"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updatedAt"`
}

func (m *WorkerTasks) queue(agent string) ([]workerRow, error) {
	args := map[string]any{"limit": workerQueueLimit}
	if agent != "" {
		args["agent"] = agent
	}
	var x struct {
		Tasks []workerRow `json:"tasks"`
	}
	if err := m.call("dispatch_queue", args, &x); err != nil {
		return nil, err
	}
	return x.Tasks, nil
}

// ListAgents 目前有任務可領的角色(一定是啟用中的)
func (m *WorkerTasks) ListAgents(string) ([]model.Agent, error) {
	rows, err := m.queue("")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []model.Agent{}
	for _, r := range rows {
		if !seen[r.Agent.ID] {
			seen[r.Agent.ID] = true
			out = append(out, model.Agent{ID: r.Agent.ID, Name: r.Agent.Name, Active: true})
		}
	}
	return out, nil
}

// ListAgentTasks dispatch_queue 的列,依 f.AgentID 與 f.Status 篩選
func (m *WorkerTasks) ListAgentTasks(_ string, f service.TaskFilter) ([]model.AgentTask, error) {
	rows, err := m.queue(f.AgentID)
	if err != nil {
		return nil, err
	}
	out := []model.AgentTask{}
	for _, r := range rows {
		if f.Status != "" && f.Status != "open" && r.Status != f.Status {
			continue
		}
		t := model.AgentTask{ID: r.ID, Title: r.Title, AgentID: r.Agent.ID, Status: r.Status, Priority: r.Priority}
		if ts, err := time.Parse(time.RFC3339, r.UpdatedAt); err == nil {
			t.UpdatedAt = model.Time{Time: ts}
		}
		out = append(out, t)
	}
	return out, nil
}

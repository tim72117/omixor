package client

import (
	"net/url"

	"omixor-core/model"
	"omixor-core/service"
)

// 虛擬角色與任務(路由見 core/api)

func (c *Client) agents(org string) string     { return "/api/orgs/" + esc(org) + "/agents" }
func (c *Client) agentTasks(org string) string { return "/api/orgs/" + esc(org) + "/agent-tasks" }

func (c *Client) ListAgents(org string) ([]model.Agent, error) {
	var l []model.Agent
	return l, c.Do("GET", c.agents(org), nil, &l)
}

func (c *Client) GetAgent(org, id string) (*model.Agent, error) {
	var g model.Agent
	return &g, c.Do("GET", c.agents(org)+"/"+esc(id), nil, &g)
}

func (c *Client) SaveAgent(org string, in model.Agent) (*model.Agent, error) {
	var g model.Agent
	return &g, c.Do("POST", c.agents(org), in, &g)
}

func (c *Client) DeleteAgent(org, id string) error {
	return c.Do("POST", c.agents(org)+"/"+esc(id)+"/delete", map[string]any{}, nil)
}

func (c *Client) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	q := url.Values{}
	if f.AgentID != "" {
		q.Set("agent", f.AgentID)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	path := c.agentTasks(org)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var l []model.AgentTask
	return l, c.Do("GET", path, nil, &l)
}

func (c *Client) GetAgentTask(org, id string) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("GET", c.agentTasks(org)+"/"+esc(id), nil, &t)
}

func (c *Client) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org), in, &t)
}

// ClaimTask 領取任務(TaskID 空 = 該角色最優先的待領取任務);回傳任務與一次性憑證
func (c *Client) ClaimTask(org string, in service.ClaimInput) (*service.ClaimResult, error) {
	var r service.ClaimResult
	return &r, c.Do("POST", c.agentTasks(org)+"/claim", in, &r)
}

// ClaimNext 從全部啟用中角色的待領取任務挑最優先的一筆領走(agentID 非空就限定該角色);沒有可領的任務時 Task 是 nil
func (c *Client) ClaimNext(org, agentID string) (*service.ClaimResult, error) {
	var r service.ClaimResult
	return &r, c.Do("POST", c.agentTasks(org)+"/claim-next", map[string]any{"agentId": agentID}, &r)
}

// ReleaseTask 放棄領取(領取者本人或組織管理員),任務退回待領取
func (c *Client) ReleaseTask(org, id, reason string) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org)+"/"+esc(id)+"/release", map[string]any{"reason": reason}, &t)
}

func (c *Client) ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org)+"/"+esc(id)+"/report", in, &t)
}

// WorkerReport 領取者以角色身分回報自己領取的任務
func (c *Client) WorkerReport(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org)+"/"+esc(id)+"/worker/report", in, &t)
}

// WorkerComment 領取者以角色身分在 Issue 留言
func (c *Client) WorkerComment(org, taskID, issueID, comment string) (*model.Issue, error) {
	var i model.Issue
	return &i, c.Do("POST", c.agentTasks(org)+"/"+esc(taskID)+"/worker/comment",
		map[string]any{"issueId": issueID, "comment": comment}, &i)
}

func (c *Client) CancelTask(org, id string) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org)+"/"+esc(id)+"/cancel", map[string]any{}, &t)
}

func (c *Client) RequeueTask(org, id string) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org)+"/"+esc(id)+"/requeue", map[string]any{}, &t)
}

// RunRoutine 立刻替角色排一筆例行任務(author 是署名,只有伺服器管理員的有效)
func (c *Client) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agents(org)+"/"+esc(agentID)+"/routines/"+esc(routineID)+"/run", map[string]any{"author": author}, &t)
}

// ImportAgents 第一次搬資料:角色與任務整批匯入(組織管理員)
func (c *Client) ImportAgents(org string, in service.AgentImport) (*service.AgentImportResult, error) {
	var r service.AgentImportResult
	return &r, c.Do("POST", "/api/orgs/"+esc(org)+"/agents-import", in, &r)
}

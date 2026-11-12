package client

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 組織鏈與依鏈委派(見 core/api/chain.go)

// GetChain 組織鏈(成員;用角色憑證當 token 也可以)
func (c *Client) GetChain(org string) (*model.OrgChain, error) {
	var x model.OrgChain
	return &x, c.Do("GET", "/api/orgs/"+esc(org)+"/chain", nil, &x)
}

// SaveChain 整份取代組織鏈(組織管理員)
func (c *Client) SaveChain(org string, edges []model.ChainEdge) (*model.OrgChain, error) {
	if edges == nil {
		edges = []model.ChainEdge{}
	}
	var x model.OrgChain
	return &x, c.Do("POST", "/api/orgs/"+esc(org)+"/chain", map[string]any{"edges": edges}, &x)
}

// DelegateTask 依組織鏈委派(token 是角色憑證 <agentId>:<secret>;伺服器管理員可帶 From、TaskID)
func (c *Client) DelegateTask(org string, in service.DelegateInput) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org)+"/delegate", in, &t)
}

// WorkerDelegate 領取者以任務角色的身分依組織鏈委派
func (c *Client) WorkerDelegate(org, taskID, code string, in service.DelegateInput) (*model.AgentTask, error) {
	var t model.AgentTask
	return &t, c.Do("POST", c.agentTasks(org)+"/"+esc(taskID)+"/worker/delegate", service.WorkerDelegateInput{Code: code, DelegateInput: in}, &t)
}

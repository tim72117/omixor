package core

import (
	"fmt"

	"omixor-core/model"
	"omixor-core/service"
)

// 組織鏈(資訊流動方向):資料與規則都在核心(omixor-core 的 service/chain.go):
//
//	本機模式:內建核心 + 本機 PostgreSQL(omixor_org_chains);遠端模式:omixor server 的 /api/orgs/{org}/chain
//
// 這裡是轉接層:AgentOps 的組織鏈部分(MCP 的 ops_chain、ops_chain_save、ops_agent_task_delegate、dispatch_delegate 用),
// 以及畫面的視圖 org_chain、動作 chain_save。

// OrgChainView 組織鏈視圖:組織鏈、成員(選 user 節點用)、能不能改(同成員視圖:本機一定可以;遠端要是組織管理員)
type OrgChainView struct {
	Org     string         `json:"org"`
	Remote  bool           `json:"remote"`
	CanEdit bool           `json:"canEdit"`
	Chain   model.OrgChain `json:"chain"`
	Members []model.Member `json:"members"`
	Error   string         `json:"error,omitempty"`
}

// chainView 視圖(在 Mu 下執行;和成員視圖一樣,遠端模式會在這裡問伺服器)
func (a *App) chainView() OrgChainView {
	m := a.membersView()
	v := OrgChainView{Org: m.Org, Remote: m.Remote, CanEdit: m.CanEdit, Members: m.Members, Error: m.Error,
		Chain: model.OrgChain{Edges: []model.ChainEdge{}}}
	if v.Org == "" {
		return v
	}
	b, err := agentBackend()
	if err != nil {
		v.Error = err.Error()
		return v
	}
	c, err := b.GetChain(v.Org)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	if c.Edges == nil {
		c.Edges = []model.ChainEdge{}
	}
	v.Chain = *c
	return v
}

func (o AgentOps) GetChain(org string) (*model.OrgChain, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	return b.GetChain(org)
}

// SaveChain 整份取代組織鏈;成功後寫動態、通知畫面
func (o AgentOps) SaveChain(org string, edges []model.ChainEdge) (*model.OrgChain, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	c, err := b.SaveChain(org, edges)
	if err != nil {
		return nil, err
	}
	o.a.Changed("chain")
	o.a.Mu.Lock()
	me := o.a.Me
	o.a.Mu.Unlock()
	o.logf(Person(me), "point.3.connected.trianglepath.dotted", fmt.Sprintf("更新組織鏈(%d 條)", len(c.Edges)))
	return c, nil
}

// DelegateTask 依組織鏈委派(本機核心:伺服器管理員帶 From 指定委派的角色;遠端:要角色憑證,一般帳號會被拒絕)
func (o AgentOps) DelegateTask(org string, in service.DelegateInput) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.DelegateTask(org, in)
	if err != nil {
		return nil, err
	}
	o.done(org)
	o.logAssigned(org, *t)
	return t, nil
}

// WorkerDelegate 領取者以任務角色的身分依組織鏈委派(/mcp/worker 的 dispatch_delegate)
func (o AgentOps) WorkerDelegate(org, taskID, code string, in service.DelegateInput) (*model.AgentTask, error) {
	b, err := o.be()
	if err != nil {
		return nil, err
	}
	t, err := b.WorkerDelegate(org, taskID, code, in)
	if err != nil {
		return nil, err
	}
	o.done(org)
	o.logAssigned(org, *t)
	return t, nil
}

func init() {
	RegisterView("org_chain", func(a *App) any { return a.chainView() })
	RegisterAction("chain_save", func(a *App, args map[string]any) (any, error) {
		var in struct {
			Edges []model.ChainEdge `json:"edges"`
		}
		if err := ReadInto(args, &in); err != nil {
			return nil, err
		}
		c, err := a.AgentOps().SaveChain(a.currentOrgNow(), in.Edges)
		if err != nil {
			return nil, agentUserErr(err)
		}
		return c, nil
	})
}

package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 組織鏈的操作:Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) GetChain(string) (*model.OrgChain, error) { return nil, errUnsupported }
func (Unsupported) SaveChain(string, []model.ChainEdge) (*model.OrgChain, error) {
	return nil, errUnsupported
}
func (Unsupported) DelegateTask(string, service.DelegateInput) (*model.AgentTask, error) {
	return nil, errUnsupported
}
func (Unsupported) WorkerDelegate(string, string, string, service.DelegateInput) (*model.AgentTask, error) {
	return nil, errUnsupported
}

// ---- Local ----

func (l *Local) GetChain(org string) (*model.OrgChain, error) {
	l.ensureOrg(org)
	return l.Svc.GetChain(l.P, org)
}

func (l *Local) SaveChain(org string, edges []model.ChainEdge) (*model.OrgChain, error) {
	l.ensureOrg(org)
	return l.Svc.SaveChain(l.P, org, edges)
}

func (l *Local) DelegateTask(org string, in service.DelegateInput) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.DelegateTask(l.P, org, in)
}

func (l *Local) WorkerDelegate(org, taskID, code string, in service.DelegateInput) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.WorkerDelegate(l.P, org, taskID, code, in)
}

// ---- Remote ----

func (r *Remote) GetChain(org string) (*model.OrgChain, error) {
	c, err := r.Client().GetChain(r.org(org))
	if err != nil {
		return nil, wrap(err)
	}
	return c, nil
}

func (r *Remote) SaveChain(org string, edges []model.ChainEdge) (*model.OrgChain, error) {
	c, err := r.Client().SaveChain(r.org(org), edges)
	if err != nil {
		return nil, wrap(err)
	}
	return c, nil
}

func (r *Remote) DelegateTask(org string, in service.DelegateInput) (*model.AgentTask, error) {
	t, err := r.Client().DelegateTask(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) WorkerDelegate(org, taskID, code string, in service.DelegateInput) (*model.AgentTask, error) {
	t, err := r.Client().WorkerDelegate(r.org(org), taskID, code, in)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

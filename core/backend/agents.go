package backend

import (
	"strings"

	"omixor-core/model"
	"omixor-core/service"
)

// 虛擬角色與任務的操作:Unsupported(不支援)、Local(核心 service)、Remote(core/client)三種實作。
// Backend 介面的宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) ListAgents(string) ([]model.Agent, error)      { return nil, errUnsupported }
func (Unsupported) GetAgent(string, string) (*model.Agent, error) { return nil, errUnsupported }
func (Unsupported) SaveAgent(string, model.Agent) (*model.Agent, error) {
	return nil, errUnsupported
}
func (Unsupported) DeleteAgent(string, string) error { return errUnsupported }
func (Unsupported) ListAgentTasks(string, service.TaskFilter) ([]model.AgentTask, error) {
	return nil, errUnsupported
}
func (Unsupported) GetAgentTask(string, string) (*model.AgentTask, error) { return nil, errUnsupported }
func (Unsupported) AssignTask(string, service.TaskInput) (*model.AgentTask, error) {
	return nil, errUnsupported
}
func (Unsupported) ClaimTask(string, service.ClaimInput) (*service.ClaimResult, error) {
	return nil, errUnsupported
}
func (Unsupported) ReportTask(string, string, service.TaskReport) (*model.AgentTask, error) {
	return nil, errUnsupported
}
func (Unsupported) CancelTask(string, string) (*model.AgentTask, error)  { return nil, errUnsupported }
func (Unsupported) RequeueTask(string, string) (*model.AgentTask, error) { return nil, errUnsupported }
func (Unsupported) ClaimNext(string, string) (*service.ClaimResult, error) {
	return nil, errUnsupported
}
func (Unsupported) ReleaseTask(string, string, string) (*model.AgentTask, error) {
	return nil, errUnsupported
}
func (Unsupported) WorkerReport(string, string, string, service.TaskReport) (*model.AgentTask, error) {
	return nil, errUnsupported
}
func (Unsupported) WorkerComment(string, string, string, string, string) (*model.Issue, error) {
	return nil, errUnsupported
}
func (Unsupported) Whoami() string { return "" }
func (Unsupported) RunRoutine(string, string, string, string) (*model.AgentTask, error) {
	return nil, errUnsupported
}

// ---- Local ----

func (l *Local) ListAgents(org string) ([]model.Agent, error) {
	l.ensureOrg(org)
	return l.Svc.ListAgents(l.P, org)
}

func (l *Local) GetAgent(org, id string) (*model.Agent, error) {
	l.ensureOrg(org)
	return l.Svc.GetAgent(l.P, org, id)
}

func (l *Local) SaveAgent(org string, in model.Agent) (*model.Agent, error) {
	l.ensureOrg(org)
	return l.Svc.SaveAgent(l.P, org, in)
}

func (l *Local) DeleteAgent(org, id string) error {
	l.ensureOrg(org)
	return l.Svc.DeleteAgent(l.P, org, id)
}

func (l *Local) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.ListAgentTasks(l.P, org, f)
}

func (l *Local) GetAgentTask(org, id string) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.GetAgentTask(l.P, org, id)
}

func (l *Local) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.AssignTask(l.P, org, in)
}

func (l *Local) ClaimTask(org string, in service.ClaimInput) (*service.ClaimResult, error) {
	l.ensureOrg(org)
	return l.Svc.ClaimTask(l.P, org, in)
}

func (l *Local) ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.ReportTask(l.P, org, id, in)
}

func (l *Local) CancelTask(org, id string) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.CancelTask(l.P, org, id)
}

func (l *Local) RequeueTask(org, id string) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.RequeueTask(l.P, org, id)
}

func (l *Local) ClaimNext(org, agentID string) (*service.ClaimResult, error) {
	l.ensureOrg(org)
	return l.Svc.ClaimNext(l.P, org, agentID)
}

func (l *Local) ReleaseTask(org, id, reason string) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.ReleaseTask(l.P, org, id, reason)
}

func (l *Local) WorkerReport(org, id, code string, in service.TaskReport) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.WorkerReport(l.P, org, id, code, in)
}

func (l *Local) WorkerComment(org, taskID, code, issueID, comment string) (*model.Issue, error) {
	l.ensureOrg(org)
	return l.Svc.WorkerComment(l.P, org, taskID, code, issueID, comment)
}

func (l *Local) Whoami() string { return service.ClaimerName(l.P) }

func (l *Local) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	l.ensureOrg(org)
	return l.Svc.RunRoutine(l.P, org, agentID, routineID, author)
}

// ---- Remote ----

func (r *Remote) ListAgents(org string) ([]model.Agent, error) {
	l, err := r.Client().ListAgents(r.org(org))
	return l, wrap(err)
}

func (r *Remote) GetAgent(org, id string) (*model.Agent, error) {
	g, err := r.Client().GetAgent(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return g, nil
}

func (r *Remote) SaveAgent(org string, in model.Agent) (*model.Agent, error) {
	g, err := r.Client().SaveAgent(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return g, nil
}

func (r *Remote) DeleteAgent(org, id string) error {
	return wrap(r.Client().DeleteAgent(r.org(org), id))
}

func (r *Remote) ListAgentTasks(org string, f service.TaskFilter) ([]model.AgentTask, error) {
	l, err := r.Client().ListAgentTasks(r.org(org), f)
	return l, wrap(err)
}

func (r *Remote) GetAgentTask(org, id string) (*model.AgentTask, error) {
	t, err := r.Client().GetAgentTask(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) AssignTask(org string, in service.TaskInput) (*model.AgentTask, error) {
	t, err := r.Client().AssignTask(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) ClaimTask(org string, in service.ClaimInput) (*service.ClaimResult, error) {
	res, err := r.Client().ClaimTask(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return res, nil
}

func (r *Remote) ReportTask(org, id string, in service.TaskReport) (*model.AgentTask, error) {
	t, err := r.Client().ReportTask(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) CancelTask(org, id string) (*model.AgentTask, error) {
	t, err := r.Client().CancelTask(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) RequeueTask(org, id string) (*model.AgentTask, error) {
	t, err := r.Client().RequeueTask(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) ClaimNext(org, agentID string) (*service.ClaimResult, error) {
	res, err := r.Client().ClaimNext(r.org(org), agentID)
	if err != nil {
		return nil, wrap(err)
	}
	return res, nil
}

func (r *Remote) ReleaseTask(org, id, reason string) (*model.AgentTask, error) {
	t, err := r.Client().ReleaseTask(r.org(org), id, reason)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) WorkerReport(org, id, code string, in service.TaskReport) (*model.AgentTask, error) {
	t, err := r.Client().WorkerReport(r.org(org), id, code, in)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

func (r *Remote) WorkerComment(org, taskID, code, issueID, comment string) (*model.Issue, error) {
	i, err := r.Client().WorkerComment(r.org(org), taskID, code, issueID, comment)
	if err != nil {
		return nil, wrap(err)
	}
	return i, nil
}

// Whoami 問伺服器「我是誰」(GET /api/auth/me),規則同伺服器寫進 ClaimedBy 的值:email 優先,否則名稱;失敗回空字串
func (r *Remote) Whoami() string {
	m, err := r.Client().Me()
	if err != nil {
		return ""
	}
	if e := strings.TrimSpace(m.User.Email); e != "" {
		return e
	}
	return strings.TrimSpace(m.User.Name)
}

func (r *Remote) RunRoutine(org, agentID, routineID, author string) (*model.AgentTask, error) {
	t, err := r.Client().RunRoutine(r.org(org), agentID, routineID, author)
	if err != nil {
		return nil, wrap(err)
	}
	return t, nil
}

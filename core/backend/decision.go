package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 待決定 / 需要協助的操作:Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) ListDecisions(string, service.DecisionListQuery) ([]model.Decision, error) {
	return nil, errUnsupported
}
func (Unsupported) GetDecision(string, string) (*model.Decision, error) { return nil, errUnsupported }
func (Unsupported) AddDecision(string, service.DecisionInput) (*model.Decision, error) {
	return nil, errUnsupported
}
func (Unsupported) AnswerDecision(string, string, service.DecisionAnswer) (*model.Decision, error) {
	return nil, errUnsupported
}
func (Unsupported) WithdrawDecision(string, string) (*model.Decision, error) {
	return nil, errUnsupported
}
func (Unsupported) ReopenDecision(string, string) (*model.Decision, error) { return nil, errUnsupported }
func (Unsupported) DecisionToIssue(string, string, service.DecisionToIssueInput) (*service.DecisionIssueResult, error) {
	return nil, errUnsupported
}
func (Unsupported) DecisionSummaries(string) (map[string]model.DecisionSummary, error) {
	return nil, errUnsupported
}

// ---- Local ----

func (l *Local) ListDecisions(org string, q service.DecisionListQuery) ([]model.Decision, error) {
	l.ensureOrg(org)
	return l.Svc.ListDecisions(l.P, org, q)
}

func (l *Local) GetDecision(org, id string) (*model.Decision, error) {
	l.ensureOrg(org)
	return l.Svc.GetDecision(l.P, org, id)
}

func (l *Local) AddDecision(org string, in service.DecisionInput) (*model.Decision, error) {
	l.ensureOrg(org)
	return l.Svc.AddDecision(l.P, org, in)
}

func (l *Local) AnswerDecision(org, id string, in service.DecisionAnswer) (*model.Decision, error) {
	l.ensureOrg(org)
	return l.Svc.AnswerDecision(l.P, org, id, in)
}

func (l *Local) WithdrawDecision(org, id string) (*model.Decision, error) {
	l.ensureOrg(org)
	return l.Svc.WithdrawDecision(l.P, org, id)
}

func (l *Local) ReopenDecision(org, id string) (*model.Decision, error) {
	l.ensureOrg(org)
	return l.Svc.ReopenDecision(l.P, org, id)
}

func (l *Local) DecisionToIssue(org, id string, in service.DecisionToIssueInput) (*service.DecisionIssueResult, error) {
	l.ensureOrg(org)
	i, d, err := l.Svc.DecisionToIssue(l.P, org, id, in)
	if err != nil {
		return nil, err
	}
	return &service.DecisionIssueResult{Issue: i, Decision: d}, nil
}

func (l *Local) DecisionSummaries(org string) (map[string]model.DecisionSummary, error) {
	l.ensureOrg(org)
	return l.Svc.DecisionSummaries(l.P, org)
}

// ---- Remote ----

func (r *Remote) ListDecisions(org string, q service.DecisionListQuery) ([]model.Decision, error) {
	x, err := r.Client().ListDecisions(r.org(org), q)
	return x, wrap(err)
}

func (r *Remote) GetDecision(org, id string) (*model.Decision, error) {
	x, err := r.Client().GetDecision(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) AddDecision(org string, in service.DecisionInput) (*model.Decision, error) {
	x, err := r.Client().AddDecision(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) AnswerDecision(org, id string, in service.DecisionAnswer) (*model.Decision, error) {
	x, err := r.Client().AnswerDecision(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) WithdrawDecision(org, id string) (*model.Decision, error) {
	x, err := r.Client().WithdrawDecision(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ReopenDecision(org, id string) (*model.Decision, error) {
	x, err := r.Client().ReopenDecision(r.org(org), id)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) DecisionToIssue(org, id string, in service.DecisionToIssueInput) (*service.DecisionIssueResult, error) {
	x, err := r.Client().DecisionToIssue(r.org(org), id, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) DecisionSummaries(org string) (map[string]model.DecisionSummary, error) {
	x, err := r.Client().DecisionSummaries(r.org(org))
	return x, wrap(err)
}

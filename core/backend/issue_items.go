package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項的操作:Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) ListIssueItems(string, string, service.ItemListQuery) ([]model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) GetIssueItem(string, string, string) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) AddIssueItem(string, string, service.ItemInput) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) UpdateIssueItem(string, string, string, service.ItemPatch) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) DeleteIssueItem(string, string, string) error { return errUnsupported }
func (Unsupported) ClaimIssueItem(string, string, string, service.ItemClaimInput) (*service.ItemClaimResult, error) {
	return nil, errUnsupported
}
func (Unsupported) ReleaseIssueItem(string, string, string) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) ReportIssueItem(string, string, string, service.ItemReport) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) BlockIssueItem(string, string, string, service.ItemReport) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) UnblockIssueItem(string, string, string) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) CancelIssueItem(string, string, string) (*model.IssueItem, error) {
	return nil, errUnsupported
}
func (Unsupported) PromoteIssueItem(string, string, string, service.ItemPromoteInput) (*service.ItemPromoteResult, error) {
	return nil, errUnsupported
}

// ---- Local ----

func (l *Local) ListIssueItems(org, issue string, q service.ItemListQuery) ([]model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.ListItems(l.P, org, issue, q)
}

func (l *Local) GetIssueItem(org, issue, item string) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.GetItem(l.P, org, issue, item)
}

func (l *Local) AddIssueItem(org, issue string, in service.ItemInput) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.AddItem(l.P, org, issue, in)
}

func (l *Local) UpdateIssueItem(org, issue, item string, in service.ItemPatch) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.UpdateItem(l.P, org, issue, item, in)
}

func (l *Local) DeleteIssueItem(org, issue, item string) error {
	l.ensureOrg(org)
	return l.Svc.DeleteItem(l.P, org, issue, item)
}

func (l *Local) ClaimIssueItem(org, issue, item string, in service.ItemClaimInput) (*service.ItemClaimResult, error) {
	l.ensureOrg(org)
	return l.Svc.ClaimItem(l.P, org, issue, item, in)
}

func (l *Local) ReleaseIssueItem(org, issue, item string) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.ReleaseItem(l.P, org, issue, item)
}

func (l *Local) ReportIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.ReportItem(l.P, org, issue, item, in)
}

func (l *Local) BlockIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.BlockItem(l.P, org, issue, item, in)
}

func (l *Local) UnblockIssueItem(org, issue, item string) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.UnblockItem(l.P, org, issue, item)
}

func (l *Local) CancelIssueItem(org, issue, item string) (*model.IssueItem, error) {
	l.ensureOrg(org)
	return l.Svc.CancelItem(l.P, org, issue, item)
}

func (l *Local) PromoteIssueItem(org, issue, item string, in service.ItemPromoteInput) (*service.ItemPromoteResult, error) {
	l.ensureOrg(org)
	return l.Svc.PromoteItem(l.P, org, issue, item, in)
}

// ---- Remote ----

func (r *Remote) ListIssueItems(org, issue string, q service.ItemListQuery) ([]model.IssueItem, error) {
	x, err := r.Client().ListIssueItems(r.org(org), issue, q)
	return x, wrap(err)
}

func (r *Remote) GetIssueItem(org, issue, item string) (*model.IssueItem, error) {
	x, err := r.Client().GetIssueItem(r.org(org), issue, item)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) AddIssueItem(org, issue string, in service.ItemInput) (*model.IssueItem, error) {
	x, err := r.Client().AddIssueItem(r.org(org), issue, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) UpdateIssueItem(org, issue, item string, in service.ItemPatch) (*model.IssueItem, error) {
	x, err := r.Client().UpdateIssueItem(r.org(org), issue, item, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) DeleteIssueItem(org, issue, item string) error {
	return wrap(r.Client().DeleteIssueItem(r.org(org), issue, item))
}

func (r *Remote) ClaimIssueItem(org, issue, item string, in service.ItemClaimInput) (*service.ItemClaimResult, error) {
	x, err := r.Client().ClaimIssueItem(r.org(org), issue, item, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ReleaseIssueItem(org, issue, item string) (*model.IssueItem, error) {
	x, err := r.Client().ReleaseIssueItem(r.org(org), issue, item)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ReportIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	x, err := r.Client().ReportIssueItem(r.org(org), issue, item, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) BlockIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	x, err := r.Client().BlockIssueItem(r.org(org), issue, item, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) UnblockIssueItem(org, issue, item string) (*model.IssueItem, error) {
	x, err := r.Client().UnblockIssueItem(r.org(org), issue, item)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) CancelIssueItem(org, issue, item string) (*model.IssueItem, error) {
	x, err := r.Client().CancelIssueItem(r.org(org), issue, item)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) PromoteIssueItem(org, issue, item string, in service.ItemPromoteInput) (*service.ItemPromoteResult, error) {
	x, err := r.Client().PromoteIssueItem(r.org(org), issue, item, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

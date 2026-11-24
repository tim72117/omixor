package backend

import (
	"omixor-core/model"
	"omixor-core/service"
)

// commits 與 Issue 的連結的操作:Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

// ---- Unsupported ----

func (Unsupported) ReportCommits(string, service.CommitReport) (*service.CommitReportResult, error) {
	return nil, errUnsupported
}
func (Unsupported) QueryCommits(string, service.CommitQueryInput) (*service.CommitQueryResult, error) {
	return nil, errUnsupported
}
func (Unsupported) GetRepoSync(string, string) (*service.CommitSyncView, error) {
	return nil, errUnsupported
}
func (Unsupported) ListIssueCommits(string, string, int) (*service.IssueCommits, error) {
	return nil, errUnsupported
}
func (Unsupported) LinkIssueCommit(string, string, service.CommitLinkInput) (*model.IssueCommitView, error) {
	return nil, errUnsupported
}
func (Unsupported) UnlinkIssueCommit(string, string, service.CommitUnlinkInput) error {
	return errUnsupported
}

// ---- Local ----

func (l *Local) ReportCommits(org string, in service.CommitReport) (*service.CommitReportResult, error) {
	l.ensureOrg(org)
	return l.Svc.ReportCommits(l.P, org, in)
}

func (l *Local) QueryCommits(org string, in service.CommitQueryInput) (*service.CommitQueryResult, error) {
	l.ensureOrg(org)
	return l.Svc.QueryCommits(l.P, org, in)
}

func (l *Local) GetRepoSync(org, repo string) (*service.CommitSyncView, error) {
	l.ensureOrg(org)
	return l.Svc.GetRepoSync(l.P, org, repo)
}

func (l *Local) ListIssueCommits(org, issue string, limit int) (*service.IssueCommits, error) {
	l.ensureOrg(org)
	return l.Svc.ListIssueCommits(l.P, org, issue, limit)
}

func (l *Local) LinkIssueCommit(org, issue string, in service.CommitLinkInput) (*model.IssueCommitView, error) {
	l.ensureOrg(org)
	return l.Svc.LinkIssueCommit(l.P, org, issue, in)
}

func (l *Local) UnlinkIssueCommit(org, issue string, in service.CommitUnlinkInput) error {
	l.ensureOrg(org)
	return l.Svc.UnlinkIssueCommit(l.P, org, issue, in)
}

// ---- Remote ----

func (r *Remote) ReportCommits(org string, in service.CommitReport) (*service.CommitReportResult, error) {
	x, err := r.Client().ReportCommits(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) QueryCommits(org string, in service.CommitQueryInput) (*service.CommitQueryResult, error) {
	x, err := r.Client().QueryCommits(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) GetRepoSync(org, repo string) (*service.CommitSyncView, error) {
	x, err := r.Client().GetRepoSync(r.org(org), repo)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) ListIssueCommits(org, issue string, limit int) (*service.IssueCommits, error) {
	x, err := r.Client().ListIssueCommits(r.org(org), issue, limit)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) LinkIssueCommit(org, issue string, in service.CommitLinkInput) (*model.IssueCommitView, error) {
	x, err := r.Client().LinkIssueCommit(r.org(org), issue, in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) UnlinkIssueCommit(org, issue string, in service.CommitUnlinkInput) error {
	return wrap(r.Client().UnlinkIssueCommit(r.org(org), issue, in))
}

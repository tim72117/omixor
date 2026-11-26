package mcp

import (
	"omixor-core/model"
	"omixor-core/service"
)

// commits 工具(commits_report、commits、issue_commit_link、issue_commit_unlink,ISS-69)由核心提供(omixor-core/mcp 的 tools_commits.go),
// 資料存在核心。這裡是 appBackend 的對應部分:只轉發給核心(本機內建核心或遠端 omixor server);權限與可見性(角色身分 403、受限專案連結隱藏)
// 都由核心依登入身分決定。issues(id) 附的 commits 也走這裡的 ListIssueCommits(讀不到就不附)。呼叫時沒有拿著 Mu。decisionBackend 見 tools_decision.go。

func (b appBackend) ReportCommits(org string, in service.CommitReport) (*service.CommitReportResult, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.ReportCommits(org, in)
}

func (b appBackend) QueryCommits(org string, in service.CommitQueryInput) (*service.CommitQueryResult, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.QueryCommits(org, in)
}

func (b appBackend) GetRepoSync(org, repo string) (*service.CommitSyncView, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.GetRepoSync(org, repo)
}

func (b appBackend) ListIssueCommits(org, issue string, limit int) (*service.IssueCommits, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.ListIssueCommits(org, issue, limit)
}

func (b appBackend) LinkIssueCommit(org, issue string, in service.CommitLinkInput) (*model.IssueCommitView, error) {
	be, err := b.coreFor(org)
	if err != nil {
		return nil, err
	}
	return be.LinkIssueCommit(org, issue, in)
}

func (b appBackend) UnlinkIssueCommit(org, issue string, in service.CommitUnlinkInput) error {
	be, err := b.coreFor(org)
	if err != nil {
		return err
	}
	return be.UnlinkIssueCommit(org, issue, in)
}

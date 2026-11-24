package client

import (
	"net/url"
	"strconv"

	"omixor-core/model"
	"omixor-core/service"
)

// commits 與 Issue 的連結(見 core/api/commits.go;只收 omx_ 使用者 token)

func (c *Client) ReportCommits(org string, in service.CommitReport) (*service.CommitReportResult, error) {
	var x service.CommitReportResult
	return &x, c.Do("POST", "/api/orgs/"+esc(org)+"/commits", in, &x)
}

func (c *Client) QueryCommits(org string, in service.CommitQueryInput) (*service.CommitQueryResult, error) {
	v := url.Values{}
	for k, s := range map[string]string{"repo": in.Repo, "sha": in.SHA, "issue": in.Issue, "query": in.Query} {
		if s != "" {
			v.Set(k, s)
		}
	}
	if in.Limit > 0 {
		v.Set("limit", strconv.Itoa(in.Limit))
	}
	path := "/api/orgs/" + esc(org) + "/commits"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var x service.CommitQueryResult
	return &x, c.Do("GET", path, nil, &x)
}

func (c *Client) GetRepoSync(org, repo string) (*service.CommitSyncView, error) {
	var x service.CommitSyncView
	return &x, c.Do("GET", "/api/orgs/"+esc(org)+"/commits/sync?repo="+url.QueryEscape(repo), nil, &x)
}

func (c *Client) ListIssueCommits(org, issue string, limit int) (*service.IssueCommits, error) {
	path := "/api/orgs/" + esc(org) + "/issues/" + esc(issue) + "/commits"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var x service.IssueCommits
	return &x, c.Do("GET", path, nil, &x)
}

func (c *Client) LinkIssueCommit(org, issue string, in service.CommitLinkInput) (*model.IssueCommitView, error) {
	var x model.IssueCommitView
	return &x, c.Do("POST", "/api/orgs/"+esc(org)+"/issues/"+esc(issue)+"/commits", in, &x)
}

func (c *Client) UnlinkIssueCommit(org, issue string, in service.CommitUnlinkInput) error {
	return c.Do("DELETE", "/api/orgs/"+esc(org)+"/issues/"+esc(issue)+"/commits", in, nil)
}

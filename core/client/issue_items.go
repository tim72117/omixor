package client

import (
	"net/url"

	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項(見 core/api/issue_items.go;用角色憑證當 token 也可以:看、新增、回報自己綁定任務的子項)

func (c *Client) issueItems(org, issue string) string {
	return "/api/orgs/" + esc(org) + "/issues/" + esc(issue) + "/items"
}

func (c *Client) issueItem(org, issue, item string) string {
	return c.issueItems(org, issue) + "/" + esc(item)
}

func (c *Client) ListIssueItems(org, issue string, q service.ItemListQuery) ([]model.IssueItem, error) {
	v := url.Values{}
	if q.IncludeDone {
		v.Set("includeDone", "1")
	}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	path := c.issueItems(org, issue)
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	out := []model.IssueItem{}
	return out, c.Do("GET", path, nil, &out)
}

func (c *Client) GetIssueItem(org, issue, item string) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("GET", c.issueItem(org, issue, item), nil, &x)
}

func (c *Client) AddIssueItem(org, issue string, in service.ItemInput) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("POST", c.issueItems(org, issue), in, &x)
}

func (c *Client) UpdateIssueItem(org, issue, item string, in service.ItemPatch) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("PATCH", c.issueItem(org, issue, item), in, &x)
}

func (c *Client) DeleteIssueItem(org, issue, item string) error {
	return c.Do("DELETE", c.issueItem(org, issue, item), nil, nil)
}

func (c *Client) ClaimIssueItem(org, issue, item string, in service.ItemClaimInput) (*service.ItemClaimResult, error) {
	var x service.ItemClaimResult
	return &x, c.Do("POST", c.issueItem(org, issue, item)+"/claim", in, &x)
}

func (c *Client) ReleaseIssueItem(org, issue, item string) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("POST", c.issueItem(org, issue, item)+"/release", nil, &x)
}

func (c *Client) ReportIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("POST", c.issueItem(org, issue, item)+"/report", in, &x)
}

func (c *Client) BlockIssueItem(org, issue, item string, in service.ItemReport) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("POST", c.issueItem(org, issue, item)+"/block", in, &x)
}

func (c *Client) UnblockIssueItem(org, issue, item string) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("POST", c.issueItem(org, issue, item)+"/unblock", nil, &x)
}

func (c *Client) CancelIssueItem(org, issue, item string) (*model.IssueItem, error) {
	var x model.IssueItem
	return &x, c.Do("POST", c.issueItem(org, issue, item)+"/cancel", nil, &x)
}

func (c *Client) PromoteIssueItem(org, issue, item string, in service.ItemPromoteInput) (*service.ItemPromoteResult, error) {
	var x service.ItemPromoteResult
	return &x, c.Do("POST", c.issueItem(org, issue, item)+"/promote", in, &x)
}

package client

import (
	"net/url"

	"omixor-core/model"
	"omixor-core/service"
)

// 待決定 / 需要協助(見 core/api/decision.go;用角色憑證當 token 也可以:看、提出、撤回自己提的;處理、重開、開 Issue 只有人)

func (c *Client) decisions(org string) string { return "/api/orgs/" + esc(org) + "/decisions" }

// ListDecisions 依條件列(舊的在前)
func (c *Client) ListDecisions(org string, q service.DecisionListQuery) ([]model.Decision, error) {
	v := url.Values{}
	if q.Status != "" {
		v.Set("status", q.Status)
	}
	if q.Kind != "" {
		v.Set("kind", q.Kind)
	}
	if q.Issue != "" {
		v.Set("issue", q.Issue)
	}
	if q.Mine {
		v.Set("mine", "1")
	}
	path := c.decisions(org)
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	out := []model.Decision{}
	return out, c.Do("GET", path, nil, &out)
}

func (c *Client) GetDecision(org, id string) (*model.Decision, error) {
	var x model.Decision
	return &x, c.Do("GET", c.decisions(org)+"/"+esc(id), nil, &x)
}

func (c *Client) AddDecision(org string, in service.DecisionInput) (*model.Decision, error) {
	var x model.Decision
	return &x, c.Do("POST", c.decisions(org), in, &x)
}

func (c *Client) AnswerDecision(org, id string, in service.DecisionAnswer) (*model.Decision, error) {
	var x model.Decision
	return &x, c.Do("POST", c.decisions(org)+"/"+esc(id)+"/answer", in, &x)
}

func (c *Client) WithdrawDecision(org, id string) (*model.Decision, error) {
	var x model.Decision
	return &x, c.Do("POST", c.decisions(org)+"/"+esc(id)+"/withdraw", nil, &x)
}

func (c *Client) ReopenDecision(org, id string) (*model.Decision, error) {
	var x model.Decision
	return &x, c.Do("POST", c.decisions(org)+"/"+esc(id)+"/reopen", nil, &x)
}

func (c *Client) DecisionToIssue(org, id string, in service.DecisionToIssueInput) (*service.DecisionIssueResult, error) {
	var x service.DecisionIssueResult
	return &x, c.Do("POST", c.decisions(org)+"/"+esc(id)+"/to-issue", in, &x)
}

// DecisionSummaries 每張來源 Issue 的 open / decided 筆數
func (c *Client) DecisionSummaries(org string) (map[string]model.DecisionSummary, error) {
	out := map[string]model.DecisionSummary{}
	return out, c.Do("GET", c.decisions(org)+"/summary", nil, &out)
}

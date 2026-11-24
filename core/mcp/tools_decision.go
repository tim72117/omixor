package mcp

import (
	"encoding/json"
	"fmt"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 待決定 / 需要協助工具:issue_decision_add(提出)、issue_decisions(查)、issue_decision_answer(處理,只有人)、
// issue_decision_to_issue(由它開出問題 Issue)。規則與可見性在 service/decision.go(透過 Backend)。
// 角色端點(子代理)只開放 issue_decision_add 與 issue_decisions:可以提出、不能處理。
// 署名一律是呼叫者本人(人類端點是登入的人、角色端點是憑證的角色),工具沒有 agent、author 參數(人類端點不能指定身分,見 identity_agent.go)。
// 有來源 Issue 的決定依該 Issue 的專案可見性篩選(看不到的單筆回「找不到」、列表沒有)。

// DecisionToolDefs 待決定 / 需要協助工具的規格(接在 CoreToolDefs 後面)
func DecisionToolDefs() []any {
	return []any{
		coreDef("issue_decision_add",
			"Raises something that needs a human (kind has two uses):\n"+
				"- decision (default): a human must pick one of several options. Give options (at least 2, each {label, description?, recommended?, id?}; set recommended to true on the one you suggest); the human may also pick \"other\" with an explanation.\n"+
				"- help: a human must do something or provide information (e.g. a Cloud SQL backup, building on a Mac, providing a credential, one real-device verification); it has no options and must not carry options. The human reports it done (evidence optional) or declines it (with a reason).\n"+
				"When a sub-agent hits something only a human can do, create a help; when a choice is needed, create a decision; do not write pending decisions into issue descriptions or comments. Put the source issue in source_issue (e.g. ISS-37) and the issues it blocks, or a description, in blocks. Returns DEC-n.", false,
			obj(map[string]any{
				"kind":         pEnum("decision (a human picks, default) or help (a human does something)", model.DecisionKinds...),
				"title":        pStr("The question or request in one sentence"),
				"body":         pStr("Background (Markdown)"),
				"options":      pObjs("Options for a decision: [{label, description?, recommended?, id?}], at least 2; do not give them for help"),
				"source_issue": pStr("Source issue (e.g. ISS-37; optional)"),
				"blocks":       pStrs("Keys of the issues it blocks, or a description"),
			}, "title")),
		coreDef("issue_decisions",
			"Queries pending decisions (decision) and help requests (help). By default lists the open ones (status=open, \"pending\"), both kinds; with id returns a single one. Filter by kind, source issue (issue) and status; mine=true means \"waiting for me\" (open and I can handle them).", true,
			obj(map[string]any{
				"id":     pStr("DEC-n (when given, only this one is returned)"),
				"status": pEnum("Status: open (default), decided (decided / handled), withdrawn, or all", "open", "decided", "withdrawn", "all"),
				"kind":   pEnum("List only this kind", model.DecisionKinds...),
				"issue":  pStr("List only those whose source is this issue (e.g. ISS-37)"),
				"mine":   pBool("Waiting for me: open ones, both kinds (an agent credential has no \"me\"; returns empty)"),
				"limit":  pInt("Maximum number of results, default 50, at most 200"),
			})),
		coreDef("issue_decision_answer",
			"Handles a pending decision or help request (humans only; agent credentials are rejected). decision: answer is an option id, or other with a note; help: answer is done (optionally with a note and an evidence string) or declined (a note with the reason is required). A handled item cannot be changed; to change it, an organization admin or the original handler must reopen it (reopen: HTTP API). The conclusion is written automatically as a comment on the source issue (signed by you).", false,
			obj(map[string]any{
				"id":       pStr("DEC-n"),
				"answer":   pStr("decision: an option id or other; help: done or declined"),
				"note":     pStr("Explanation (required when choosing other or declined)"),
				"evidence": pStr("Evidence that the help was completed (link, output, version number; optional)"),
			}, "id", "answer")),
		coreDef("issue_decision_to_issue",
			"Opens a problem issue from a pending decision / help request (title = the question, body contains the background and options, source records DEC-n), and points the item's source issue to the new issue. Not allowed when it already has a source issue (humans only; agent credentials are rejected).", false,
			obj(map[string]any{
				"id":       pStr("DEC-n"),
				"priority": pEnum("Priority, default normal", IssuePriorities...),
				"labels":   pStrs("Labels, default [待決定] (decision) or [需要協助] (help); these two are the literal label values"),
				"project":  pStr("Project id to put the new issue in (e.g. PRJ-1; empty string = organization level; omitted = follows the repo link rule, same as issue_create)"),
				"repo":     pStr("Repo of the new issue (e.g. owner/name; optional; used to pick the default project when no project is given)"),
			}, "id")),
	}
}

func decisionErr(id string, err error) error {
	if backend.NotFound(err) && id != "" {
		return Errorf("找不到 %s", id)
	}
	return terr(err)
}

// decisionJSON 輸出:建立者、處理人的相容字串換成顯示名稱(createdByActor、decidedByActor 物件照原樣)
func decisionJSON(e Env, d model.Decision) any {
	b, _ := json.Marshal(d)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["createdBy"] = e.AuthorName(d.CreatedBy)
	if d.DecidedBy != "" {
		m["decidedBy"] = e.AuthorName(d.DecidedBy)
	}
	return m
}

// parseOptions 選項:物件 {label, description, recommended, id},或只給字串(當 label)
func parseOptions(raw any) ([]model.DecisionOption, error) {
	list, ok := raw.([]any)
	if raw == nil {
		return nil, nil
	}
	if !ok {
		return nil, Errorf("options 要是陣列")
	}
	out := []model.DecisionOption{}
	for k, x := range list {
		switch v := x.(type) {
		case string:
			out = append(out, model.DecisionOption{Label: v})
		case map[string]any:
			o := model.DecisionOption{}
			o.ID, _ = ScalarString(v["id"])
			o.Label, _ = ScalarString(v["label"])
			o.Description, _ = ScalarString(v["description"])
			switch r := v["recommended"].(type) {
			case bool:
				o.Recommended = r
			case string:
				o.Recommended = r == "true"
			}
			out = append(out, o)
		default:
			return nil, Errorf("options[%d] 要是物件 {label, description?, recommended?}", k)
		}
	}
	return out, nil
}

// RegisterDecisionTools 登記待決定 / 需要協助工具。執行環境(Call.Ctx)要實作 Env
func RegisterDecisionTools(s *Server) {
	s.Register("issue_decision_add", func(c *Call) (any, error) {
		e := env(c)
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		kind, err := c.Args.OptionalEnum("kind", model.DecisionKinds)
		if err != nil {
			return nil, err
		}
		opts, err := parseOptions(c.Args.Raw["options"])
		if err != nil {
			return nil, err
		}
		body, _ := c.Args.RawString("body")
		blocks, _ := c.Args.Strings("blocks")
		d, err := e.Backend().AddDecision(e.Org(), service.DecisionInput{Kind: kind, Title: title, Body: body, Options: opts,
			SourceIssue: c.Args.Str("source_issue"), Blocks: blocks})
		if err != nil {
			return nil, terr(err)
		}
		return decisionJSON(e, *d), nil
	})

	s.Register("issue_decisions", func(c *Call) (any, error) {
		e := env(c)
		bk, org := e.Backend(), e.Org()
		if id := c.Args.Str("id"); id != "" {
			d, err := bk.GetDecision(org, id)
			if err != nil {
				return nil, decisionErr(id, err)
			}
			return decisionJSON(e, *d), nil
		}
		status, err := c.Args.OptionalEnum("status", []string{"open", "decided", "withdrawn", "all"})
		if err != nil {
			return nil, err
		}
		if status == "" {
			status = model.DecisionOpen
		}
		if status == "all" {
			status = ""
		}
		kind, err := c.Args.OptionalEnum("kind", model.DecisionKinds)
		if err != nil {
			return nil, err
		}
		mine, _ := c.Args.Bool("mine")
		limit, ok := c.Args.Int("limit")
		if !ok || limit <= 0 {
			limit = 50
		}
		limit = min(limit, 200)
		list, err := bk.ListDecisions(org, service.DecisionListQuery{Status: status, Kind: kind, Issue: c.Args.Str("issue"), Mine: mine})
		if err != nil {
			return nil, terr(err)
		}
		total := len(list)
		if len(list) > limit {
			list = list[:limit]
		}
		out := []any{}
		for _, d := range list {
			out = append(out, decisionJSON(e, d))
		}
		return map[string]any{"total": total, "decisions": out}, nil
	})

	s.Register("issue_decision_answer", func(c *Call) (any, error) {
		e := env(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		answer, err := c.Args.Require("answer")
		if err != nil {
			return nil, err
		}
		d, err := e.Backend().AnswerDecision(e.Org(), id, service.DecisionAnswer{Answer: answer, Note: c.Args.Str("note"),
			Evidence: c.Args.Str("evidence")})
		if err != nil {
			return nil, decisionErr(id, err)
		}
		return decisionJSON(e, *d), nil
	})

	s.Register("issue_decision_to_issue", func(c *Call) (any, error) {
		e := env(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		priority, err := c.Args.OptionalEnum("priority", IssuePriorities)
		if err != nil {
			return nil, err
		}
		labels, _ := c.Args.Strings("labels")
		in := service.DecisionToIssueInput{Priority: priority, Labels: labels, Repo: c.Args.Str("repo")}
		if v, ok := c.Args.Raw["project"]; ok { // 有給(含空字串 = 組織層級)就明確指定;沒給依 repo 的連結規則
			prj, _ := ScalarString(v)
			in.Project = &prj
		}
		r, err := e.Backend().DecisionToIssue(e.Org(), id, in)
		if err != nil {
			return nil, decisionErr(id, err)
		}
		return map[string]any{"issue": issueJSON(e, *r.Issue), "decision": decisionJSON(e, *r.Decision),
			"message": fmt.Sprintf("已開出 %s,%s 的來源 Issue 指向它", r.Issue.Key, r.Decision.ID)}, nil
	})
}

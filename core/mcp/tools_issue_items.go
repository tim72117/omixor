package mcp

import (
	"encoding/json"
	"fmt"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項(issue_item,ISS-37)工具:issue_item_add(新增)、issue_items(查)、issue_item_update(改、取消)、issue_item_claim(領取)、
// issue_item_report(回報進度、完成、卡住)、issue_item_promote(提升為 Issue)。規則、可見性與授權在 service/issue_items.go(透過 Backend)。
// 子項的編號是 <Issue key>.<序號>,例如 ISS-37.2;可見性完全繼承所屬 Issue(受限專案非成員看不到,單筆「找不到」)。
// 角色端點(子代理)開放 issue_item_add、issue_items、issue_item_report:可以提出下一步、看清單、回報自己綁定任務的子項;
// 領取、改、取消、提升只有人。署名一律是呼叫者本人,工具沒有 agent、author 參數。
// issue_block / issue_unblock(Issue 級卡住旗標)這一版沒有做:要動 Issue 的結構與遷移,範圍比子項大,見 ISS-37 的說明。

const evidenceCheckDoc = "evidenceCheck (computed on read) separates what the system confirmed from what was only self-reported: " +
	"commit = confirmed (reported to this organization for the issue's repo, or any repo when the issue has none), not_found (the repo has reported commits but not this one), " +
	"unverifiable (no commit data for the repo), invalid_sha, or not_visible (on the agent endpoint, only the item bound to your current task is checked; others are not_visible); selfReported lists the evidence keys that were not confirmed; " +
	"needsVerification=true means a done item has neither a confirmed commit nor an unverified explanation."

// 被標 needsVerification 之後回報者能做什麼:角色不能回報 commits,而且 done 之後憑證就失效
const (
	evidenceNextStep = "If the commit just has not been reported yet, run `omixor commits sync` (or commits_report); the check is recomputed on every read. " +
		"Otherwise tell the user what was not verified, for example in a comment on the issue."
	evidenceAgentNextStep = "Agents cannot report commits, and reporting done ended your task and its credential, so do not retry or call more tools. " +
		"In your final answer, tell whoever started you what was not verified; if the commit has not been reported to omixor yet, ask them to run `omixor commits sync` (the check is recomputed on every read)."
)

// isAgentBackend 這個連線是角色身分嗎(Backend 實作 IsAgent() bool,例如 backend.Local)
func isAgentBackend(bk any) bool {
	b, ok := bk.(interface{ IsAgent() bool })
	return ok && b.IsAgent()
}

// evidenceWarning 完成回報被標 needsVerification 時,告訴回報者原因與下一步;沒被標回空字串
func evidenceWarning(id string, c *model.EvidenceCheck, agent bool) string {
	if c == nil || !c.NeedsVerification {
		return ""
	}
	reason := "the evidence has no commit"
	switch c.Commit {
	case model.EvidenceNotFound:
		reason = "evidence.commit was not found among the commits reported for this repo"
	case model.EvidenceUnverifiable:
		reason = "no commit data has been reported for this repo, so evidence.commit cannot be checked"
	case model.EvidenceInvalidSHA:
		reason = "evidence.commit is not a 7-40 character hex sha"
	}
	next := evidenceNextStep
	if agent {
		next = evidenceAgentNextStep
	}
	return id + " is recorded as done but flagged needsVerification: " + reason + " and there is no evidence.unverified explanation. " + next
}

// ItemToolDefs 子項工具的規格(接在 CoreToolDefs 後面)
func ItemToolDefs() []any {
	itemID := "Item number (e.g. ISS-37.2, the issue key plus a sequence number)"
	return []any{
		coreDef("issue_item_add",
			"Adds an item (one entry of the to-do list) under an issue. Threshold: open an item only for work that \"spans phases, spans sub-agents, or needs a human decision\"; do not open overly small ones. "+
				"When a sub-agent finds the next step (or work to hand to another sub-agent) midway, raise it with this instead of only writing it in the conversation. Returns the item (with number <issue key>.<sequence>). "+
				"depends_on refers to prerequisite items of the same issue (number, sequence or id; an item whose prerequisites are unfinished cannot be claimed or completed, and dependencies must not form a cycle); files are the files it will touch (so others can avoid conflicts); acceptance is the acceptance criteria. "+
				"assignee is the owner (a person's email or handle, or an agent as agent:<handle>); when the owner is an agent, claiming automatically queues a task for it and issues a credential. On the agent endpoint, assignee can only be empty or yourself when adding.", false,
			obj(map[string]any{
				"issue":      pStr("The issue it belongs to (e.g. ISS-37)"),
				"title":      pStr("One sentence saying what this item is to do"),
				"files":      pStrs("Files or modules it will touch"),
				"acceptance": pStr("Acceptance criteria (the conditions for being done)"),
				"assignee":   pStr("Owner: a person (email, @handle) or an agent (agent:<handle>); empty = unassigned"),
				"depends_on": pStr("Prerequisite item (number, sequence or id of the same issue)"),
			}, "issue", "title")),
		coreDef("issue_items",
			"Queries an issue's items (to-do list). With id (e.g. ISS-37.2) returns a single one; with issue lists that issue's items, by default only unfinished ones (todo / doing / blocked); include_done=true also includes done and canceled ones, "+
				"and status selects a single status. Each entry has status, owner, claimer, prerequisites, files, acceptance criteria, block reason and what it waits for, and completion evidence. "+
				evidenceCheckDoc, true,
			obj(map[string]any{
				"id":           pStr(itemID + "; when given, only this one is returned"),
				"issue":        pStr("Issue number (for listing; may be omitted when id is given)"),
				"include_done": pBool("Also list done and canceled ones (by default only unfinished ones are listed)"),
				"status":       pEnum("List only this status", model.ItemStatuses...),
			})),
		coreDef("issue_item_update",
			"Modifies an item's title, files, acceptance criteria, owner and prerequisites (humans only; not allowed on the agent endpoint). assignee and depends_on set to an empty string are cleared. cancel=true cancels the item (abandons the work; do not use deletion to express this; the bound agent task is canceled too). A done or canceled item can no longer be changed.", false,
			obj(map[string]any{
				"id":         pStr(itemID),
				"title":      pStr("New title"),
				"files":      pStrs("Files it will touch (replaces the whole list)"),
				"acceptance": pStr("Acceptance criteria"),
				"assignee":   pStr("Owner (empty string = clear)"),
				"depends_on": pStr("Prerequisite item (empty string = clear)"),
				"cancel":     pBool("true = cancel this item"),
			}, "id")),
		coreDef("issue_item_claim",
			"Claims an item (start working): status becomes doing, you become the claimer, and nobody else can claim it before the lease expires; returns 409 if prerequisites are unfinished. When the owner is a person or nobody, only a lightweight claim is made; "+
				"when the owner is an agent, a bound agent task is created and claimed automatically, and a one-time credential is returned (credential; connect to the agent endpoint to work as that agent, and report with issue_item_report on the agent endpoint when done). "+
				"A blocked item must be unblocked first (issue_item_report status=todo). Humans only; not allowed on the agent endpoint.", false,
			obj(map[string]any{
				"id":   pStr(itemID),
				"note": pStr("Claim note (e.g. which session or sub-agent; recorded in the audit log)"),
			}, "id")),
		coreDef("issue_item_report",
			"Reports on an item: status=done (finished; may attach evidence, either a JSON object like {commit, tests, unverified:[...]} or a piece of text, and a note), "+
				"blocked (stuck; block_reason is required; when a human decision is needed, use wait_for_kind=user_decision and say in wait_for what the decision is (e.g. DEC-n or a question), and do not guess the answer; "+
				"with wait_for_kind=item, wait_for is another item of the same issue; external is an external condition), doing (renew the lease; equals claiming when not claimed), todo (return to unclaimed, or unblock; not allowed on the agent endpoint). "+
				"Humans: you can report only if nobody else holds a valid lease, or you are the claimer or an organization admin; agent endpoint: only the items of the task bound to that agent (use the credential returned when claiming with issue_item_claim). Completing requires the prerequisites to be done. "+
				"On done, put the commit sha in evidence.commit (7-40 hex characters, reported to omixor with commits sync or commits_report, which agents cannot call themselves) and list anything you did not check in evidence.unverified. "+
				evidenceCheckDoc+" A flagged report is still accepted; its response also carries a message.", false,
			obj(map[string]any{
				"id":            pStr(itemID),
				"status":        pEnum("todo, doing, blocked or done", model.ItemTodo, model.ItemDoing, model.ItemBlocked, model.ItemDone),
				"evidence":      map[string]any{"description": "Completion evidence: a JSON object ({commit, tests, unverified[]...}) or a piece of text"},
				"note":          pStr("Note (merged into the evidence on completion, and a system comment is left on the issue)"),
				"block_reason":  pStr("Why it is stuck (required for blocked)"),
				"wait_for_kind": pEnum("What it is waiting for", model.ItemWaitKinds...),
				"wait_for":      pStr("What it waits for: user_decision = a question or DEC-n, item = an item of the same issue, external = a description"),
			}, "id", "status")),
		coreDef("issue_item_promote",
			"Promotes an item to an independent issue (when the item is too big or worth tracking on its own): creates a new issue (title = item title, body carries the acceptance criteria and files, source records the item number), "+
				"marks the original item as canceled and records the new issue, and leaves a system comment on each of the two issues recording the link. Humans only; not allowed on the agent endpoint.", false,
			obj(map[string]any{
				"id":       pStr(itemID),
				"priority": pEnum("Priority of the new issue (default: same as the source issue)", IssuePriorities...),
				"labels":   pStrs("Labels of the new issue"),
				"project":  pStr("Project id to put the new issue in (empty string = organization level; omitted = same as the source issue)"),
			}, "id")),
	}
}

func itemRefs(c *Call) (issue, item string, err error) {
	id, err := c.Args.Require("id")
	if err != nil {
		return "", "", err
	}
	issue, item = service.SplitItemKey(id)
	if issue == "" {
		return "", "", Errorf("id must be an item number such as ISS-37.2 (issue key plus sequence number)")
	}
	return issue, item, nil
}

func itemErr(ref string, err error) error {
	if backend.NotFound(err) && ref != "" {
		return Errorf("%s not found", ref)
	}
	return terr(err)
}

// evidenceArg evidence 參數:字串或物件 → JSON
func evidenceArg(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, Errorf("evidence must be a JSON object or a string")
	}
	return b, nil
}

// RegisterItemTools 登記子項工具。執行環境(Call.Ctx)要實作 Env
func RegisterItemTools(s *Server) {
	s.Register("issue_item_add", func(c *Call) (any, error) {
		e := env(c)
		issue, err := c.Args.Require("issue")
		if err != nil {
			return nil, err
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		files, _ := c.Args.Strings("files")
		it, err := e.Backend().AddIssueItem(e.Org(), issue, service.ItemInput{Title: title, Files: files, Acceptance: c.Args.Str("acceptance"),
			Assignee: c.Args.Str("assignee"), DependsOn: c.Args.Str("depends_on")})
		if err != nil {
			return nil, itemErr(issue, err)
		}
		return it, nil
	})

	s.Register("issue_items", func(c *Call) (any, error) {
		e := env(c)
		bk, org := e.Backend(), e.Org()
		if id := c.Args.Str("id"); id != "" {
			issue, item := service.SplitItemKey(id)
			if issue == "" {
				return nil, Errorf("id must be an item number such as ISS-37.2 (issue key plus sequence number)")
			}
			it, err := bk.GetIssueItem(org, issue, item)
			if err != nil {
				return nil, itemErr(id, err)
			}
			return it, nil
		}
		issue, err := c.Args.Require("issue")
		if err != nil {
			return nil, err
		}
		status, err := c.Args.OptionalEnum("status", model.ItemStatuses)
		if err != nil {
			return nil, err
		}
		done, _ := c.Args.Bool("include_done")
		list, err := bk.ListIssueItems(org, issue, service.ItemListQuery{IncludeDone: done, Status: status})
		if err != nil {
			return nil, itemErr(issue, err)
		}
		open := 0
		for _, it := range list {
			if !model.ItemTerminal(it.Status) {
				open++
			}
		}
		return map[string]any{"issue": issue, "total": len(list), "open": open, "items": list}, nil
	})

	s.Register("issue_item_update", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		if cancel, _ := c.Args.Bool("cancel"); cancel {
			it, err := e.Backend().CancelIssueItem(e.Org(), issue, item)
			if err != nil {
				return nil, itemErr(id, err)
			}
			return it, nil
		}
		in := service.ItemPatch{Title: c.Args.Opt("title"), Acceptance: c.Args.Opt("acceptance"), Assignee: c.Args.Opt("assignee"), DependsOn: c.Args.Opt("depends_on")}
		if _, ok := c.Args.Raw["files"]; ok {
			files, _ := c.Args.Strings("files")
			if files == nil {
				files = []string{}
			}
			in.Files = &files
		}
		it, err := e.Backend().UpdateIssueItem(e.Org(), issue, item, in)
		if err != nil {
			return nil, itemErr(id, err)
		}
		return it, nil
	})

	s.Register("issue_item_claim", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		res, err := e.Backend().ClaimIssueItem(e.Org(), issue, item, service.ItemClaimInput{Note: c.Args.Str("note")})
		if err != nil {
			if service.Code(err) == 409 { // 含「已被領取、處理中,鎖定到 hh:mm」:原文轉給呼叫端
				return nil, prefixErr("Claim failed: ", err)
			}
			return nil, itemErr(id, err)
		}
		out := map[string]any{"item": res.Item}
		msg := "Claimed " + id + ". "
		if res.Item.LockedUntil != nil {
			msg += "Locked until " + res.Item.LockedUntil.Time.Local().Format("15:04") + " (nobody else can claim it); for long work renew the lease periodically with issue_item_report(status: doing). "
		}
		if res.Task != nil {
			out["task"] = toMap(*res.Task)
		}
		if cr := res.Credential; cr != nil {
			m := map[string]any{"agentId": cr.AgentID, "secret": cr.Secret, "expiresAt": cr.ExpiresAt}
			if ae, ok := c.Ctx.(AgentEnv); ok {
				if u := ae.EndpointURL(); u != "" {
					m["endpoint"] = u
				}
			}
			out["credential"] = m
			msg += "The assignee is an agent: a bound task was created and claimed: " + res.Task.ID + ". To work as that agent, connect to the agent endpoint with the credential (Authorization: Bearer <agentId>:<secret>); report with issue_item_report when done."
		}
		out["message"] = msg
		return out, nil
	})

	s.Register("issue_item_report", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		status, err := c.Args.RequireEnum("status", []string{model.ItemTodo, model.ItemDoing, model.ItemBlocked, model.ItemDone})
		if err != nil {
			return nil, err
		}
		ev, err := evidenceArg(c.Args.Raw["evidence"])
		if err != nil {
			return nil, err
		}
		it, err := e.Backend().ReportIssueItem(e.Org(), issue, item, service.ItemReport{Status: status, Evidence: ev, Note: c.Args.Str("note"),
			BlockReason: c.Args.Str("block_reason"), WaitForKind: c.Args.Str("wait_for_kind"), WaitFor: c.Args.Str("wait_for")})
		if err != nil {
			return nil, itemErr(id, err)
		}
		if msg := evidenceWarning(id, it.EvidenceCheck, isAgentBackend(e.Backend())); msg != "" {
			out := toMap(it)
			out["message"] = msg
			return out, nil
		}
		return it, nil
	})

	s.Register("issue_item_promote", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		priority, err := c.Args.OptionalEnum("priority", IssuePriorities)
		if err != nil {
			return nil, err
		}
		labels, _ := c.Args.Strings("labels")
		in := service.ItemPromoteInput{Priority: priority, Labels: labels}
		if v, ok := c.Args.Raw["project"]; ok { // 有給(含空字串 = 組織層級)就明確指定;沒給 = 同來源 Issue
			prj, _ := ScalarString(v)
			in.Project = &prj
		}
		r, err := e.Backend().PromoteIssueItem(e.Org(), issue, item, in)
		if err != nil {
			return nil, itemErr(id, err)
		}
		return map[string]any{"issue": issueJSON(e, *r.Issue), "item": r.Item,
			"message": fmt.Sprintf("Promoted to %s; %s is marked cancelled and the link is recorded", r.Issue.Key, id)}, nil
	})
}

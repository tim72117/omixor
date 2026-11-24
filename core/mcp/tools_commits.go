package mcp

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// commits 與 Issue 的連結工具(ISS-69 phase 1):commits_report(寫)、commits(讀)、issue_commit_link / issue_commit_unlink(寫)。
// 規則、可見性與授權在 service/commits.go(透過 Backend)。寫入工具不在 OAuth 寫入白名單(只收 omx_ 使用者 token),
// 全部不開給角色端點與執行者端點(model.CoreTools 的 Agent、Worker 都是 false);issues(id) 另外附 commits 與 commits_total(見 attachIssueCommits)。
// 工具說明與錯誤訊息一律英文(給 LLM 看)。

// CommitToolDefs commits 工具的規格(接在 CoreToolDefs 後面)
func CommitToolDefs() []any {
	shaDesc := "Full 40-character commit sha (lowercase hex)"
	return []any{
		coreDef("commits_report",
			"Reports a batch of git commits (metadata only: sha, message, author, times, branch) for ONE repository so omixor can link them to issues. "+
				"Normally a script or CI step reports commits (for example from `git log`); do not invent commits. At most 100 per call; the call is idempotent (sending the same commits again creates nothing new). "+
				"Reported data is stored as unverified. Secret-looking text in subject, body and branch is masked automatically; do not put secrets there. "+
				"Links are made automatically: issue keys found in the commit message (e.g. ISS-12 or APP-3, case-insensitive) are linked ONLY when that issue exists in this organization and you can see it; "+
				"\"fixes/closes/resolves <key>\" links as kind fixes, any other mention as kind mentions. Issues are never modified or closed. "+
				"Result: reported (commits processed), created (commits that were new), linked (links created or upgraded by this call: [{issue, sha (first 7), kind}]), unmatched (issue-key-like strings of a known key prefix that do not match any issue you can see; check for typos). "+
				"Limits per user: 600 commits and 120 calls per hour. A repository is stored exactly as you name it, lowercased, and github:acme/app and gitlab:acme/app are different repositories; "+
				"use the same spelling (with or without prefix) every time. "+
				"Needs an omx_ user token (not available with OAuth, agent credentials or the worker endpoint).", false,
			obj(map[string]any{
				"repo":   pStr("Repository key, e.g. github:owner/name or gitlab:group/sub/project. The github: / gitlab: prefix is kept as part of the key (recommended, so same-named repositories on different hosts stay separate); without a prefix the plain key is its own repository. Lowercased; only letters, digits and - _ . / : are accepted"),
				"source": pEnum("Where the data comes from; default api", model.CommitReportSources...),
				"commits": map[string]any{"type": "array", "description": "Commits to report (1-100)", "items": obj(map[string]any{
					"sha":          pStr(shaDesc),
					"subject":      pStr("First line of the commit message (max 300 characters)"),
					"body":         pStr("Rest of the commit message (max 4000 characters)"),
					"author_name":  pStr("Author name (max 200 characters)"),
					"authored_at":  pStr("Author time, RFC3339 (e.g. 2026-01-02T15:04:05Z)"),
					"committed_at": pStr("Commit time, RFC3339"),
					"parents":      pStrs("Parent commit shas (full 40-character shas)"),
					"branch":       pStr("Branch the commit was reported from (max 200 characters)"),
					"url":          pStr("Web URL of the commit (http or https)"),
				}, "sha", "subject")},
			}, "repo", "commits")),
		coreDef("commits",
			"Finds commits that were reported to omixor, newest first. Filters (all optional, combined with AND): repo, sha (a prefix of at least 7 hex characters), issue (an issue key such as ISS-12; only commits linked to it), "+
				"query (free text; every word must appear in the subject or body, case-insensitive). Each commit lists the issues it is linked to (only issues you can see) with the link kind (mentions, fixes, found_in, manual). "+
				"The body is included only when sha is given. verified=false means the commit was reported by a user and not checked against the repository. Read-only.", true,
			obj(map[string]any{
				"repo":  pStr("Only this repository. With a github: / gitlab: prefix it is exact; without one, a stored plain key wins, otherwise the single stored repository with that path (error if several hosts match: add the prefix)"),
				"sha":   pStr("Commit sha prefix, at least 7 hex characters"),
				"issue": pStr("Only commits linked to this issue (key such as ISS-12, or an internal id)"),
				"query": pStr("Free text searched in the subject and body"),
				"limit": pInt("Maximum number of commits, default 20, at most 200"),
			})),
		coreDef("issue_commit_link",
			"Links an issue to a commit by hand (use it when the automatic link from the commit message is missing, or to record where a problem was found). "+
				"kind: found_in = the issue was found at this commit/version; fixes = this commit fixes the issue; mentions = this commit refers to it; manual (default) = any other relation, explain it in note. "+
				"The commit may be one that was never reported (a bare reference is created; use commits_report to add its message later). Linking again changes the kind and note (idempotent). "+
				"Limits: at most 2000 links per issue, of which at most 200 may point to unreported commits (report them first with commits_report); 300 link/unlink calls per user per hour. "+
				"Does not change the issue's status. Same permission as issue_update. Needs an omx_ user token.", false,
			obj(map[string]any{
				"issue": pStr("Issue number: a key (e.g. ISS-3, APP-12) or an internal id"),
				"repo":  pStr("Repository as used in commits_report, e.g. github:owner/name. A prefix is exact; without a prefix a stored plain key wins, otherwise the single stored repository with that path (error if several hosts match: add the prefix)"),
				"sha":   pStr(shaDesc),
				"kind":  pEnum("Link kind, default manual", model.ManualCommitLinkKinds...),
				"note":  pStr("Short explanation (max 500 characters)"),
			}, "issue", "repo", "sha")),
		coreDef("issue_commit_unlink",
			"Removes the link between an issue and a commit (for example a wrong automatic link). The commit itself is kept. Counts toward the 300 link/unlink calls per hour. Same permission as issue_update. Needs an omx_ user token.", false,
			obj(map[string]any{
				"issue": pStr("Issue number: a key (e.g. ISS-3, APP-12) or an internal id"),
				"repo":  pStr("Repository as used in commits_report (same resolution rules as issue_commit_link)"),
				"sha":   pStr("Commit sha: the full 40 characters, or a prefix of at least 7 that matches exactly one of the issue's linked commits"),
			}, "issue", "repo", "sha")),
	}
}

// commitErr 後端錯誤 → 給 LLM 看的英文工具錯誤(常見的狀態碼換成英文;其餘照 terr)
func commitErr(err error, issue string) error {
	switch backend.Code(err) {
	case 403:
		return ToolError{Message: "Not allowed: commits are available to organization members with an omx_ user token (agent credentials cannot read or write them).", Status: 403}
	case 400:
		msg := err.Error()
		switch {
		case strings.Contains(msg, "對到多個平台"):
			list := ""
			if m := reRepoHosts.FindStringSubmatch(msg); m != nil {
				list = strings.ReplaceAll(m[1], "、", ", ")
			}
			return ToolError{Message: "Ambiguous repo: the path matches several stored repositories (" + list + "). Repeat the call with the github: or gitlab: prefix.", Status: 400}
		case strings.Contains(msg, "未回報的 commit 參照太多"):
			return ToolError{Message: "Too many unreported commit references on this issue (limit " + strconv.Itoa(model.CommitMaxBareRefsPerIssue) + "); report the commits first with commits_report, then link.", Status: 400}
		case strings.Contains(msg, "連結已達上限"):
			return ToolError{Message: "This issue already has the maximum number of commit links (" + strconv.Itoa(model.CommitMaxLinksPerIssue) + "); unlink some with issue_commit_unlink first.", Status: 400}
		}
	case 429:
		return ToolError{Message: "Rate limited: " + commitRetryHint(err) + " The limits are per user per hour (commits_report: 600 commits and 120 calls; issue_commit_link / issue_commit_unlink: 300 calls combined). Wait and retry later with fewer calls, sending larger batches (up to 100 commits) instead of many small calls.", Status: 429}
	case 404:
		if issue != "" {
			return ToolError{Message: "Not found: issue " + issue + " (or that link does not exist)", Status: 404}
		}
	}
	return terr(err)
}

// commitRetryHint 從服務的 429 訊息取出「幾分鐘後再試」(訊息是中文,數字之外不外露);取不到就用一般說法
func commitRetryHint(err error) string {
	m := reRetryMinutes.FindStringSubmatch(err.Error())
	if m == nil {
		return "retry later."
	}
	return "retry in about " + m[1] + " minute(s)."
}

var reRepoHosts = regexp.MustCompile(`對到多個平台:(.*);請加上`)

var reRetryMinutes = regexp.MustCompile(`請 (\d+) 分鐘後再試`)

func parseRFC3339(k string, v any, idx int) (*model.Time, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := ScalarString(v)
	if !ok || strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	if err != nil {
		return nil, Errorf("commits[%d].%s must be an RFC3339 timestamp (e.g. 2026-01-02T15:04:05Z)", idx, k)
	}
	return &model.Time{Time: t.UTC()}, nil
}

// commitInputs 解析 commits 參數
func commitInputs(raw any) ([]service.CommitInput, error) {
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return nil, Errorf("commits must be a non-empty array of {sha, subject, ...} objects")
	}
	if len(arr) > model.CommitMaxBatch {
		return nil, Errorf("at most %d commits per call (got %d); split the batch", model.CommitMaxBatch, len(arr))
	}
	out := make([]service.CommitInput, 0, len(arr))
	for n, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, Errorf("commits[%d] must be an object", n)
		}
		a := Args{Raw: m}
		sha, err := a.Require("sha")
		if err != nil {
			return nil, Errorf("commits[%d].sha is required", n)
		}
		if _, ok := model.CleanSHA(sha); !ok {
			return nil, Errorf("commits[%d].sha must be the full 40-character hex sha", n)
		}
		in := service.CommitInput{SHA: sha, Subject: a.Str("subject"), Body: a.Str("body"), AuthorName: a.Str("author_name"), Branch: a.Str("branch"), URL: a.Str("url")}
		if in.AuthoredAt, err = parseRFC3339("authored_at", m["authored_at"], n); err != nil {
			return nil, err
		}
		if in.CommittedAt, err = parseRFC3339("committed_at", m["committed_at"], n); err != nil {
			return nil, err
		}
		if ps, ok := a.Strings("parents"); ok {
			in.Parents = ps
		}
		out = append(out, in)
	}
	return out, nil
}

// commitResultRow commits 結果的一筆:不含 org、reportedBy 等細節,body 只在指定 sha 時附上
func commitResultRow(v service.CommitView, withBody bool) map[string]any {
	row := map[string]any{"repo": v.Repo, "sha": v.SHA, "sha7": v.SHA7, "subject": v.Subject, "verified": v.Verified, "source": v.Source, "issues": v.Issues}
	if withBody && v.Body != "" {
		row["body"] = v.Body
	}
	if v.AuthorName != "" {
		row["author"] = v.AuthorName
	}
	if v.AuthoredAt != nil {
		row["authoredAt"] = iso(v.AuthoredAt.Time)
	}
	if v.Branch != "" {
		row["branch"] = v.Branch
	}
	if v.URL != "" {
		row["url"] = v.URL
	}
	return row
}

// issueCommitsLimit issues(id) 附幾筆最近連結的 commits
const issueCommitsLimit = 10

// attachIssueCommits issues(id) 的輸出加上 commits(最近連結的 10 筆:sha7、subject、kind、repo、url、authoredAt)與 commits_total。
// 讀不到(角色端點被拒、後端不支援、任何錯誤)就不加這兩個欄位,不影響 Issue 本身的讀取
func attachIssueCommits(bk backend.Backend, org, issue string, out map[string]any) {
	if out == nil {
		return
	}
	res, err := bk.ListIssueCommits(org, issue, issueCommitsLimit)
	if err != nil || res == nil {
		return
	}
	rows := make([]any, 0, len(res.Commits))
	for _, c := range res.Commits {
		row := map[string]any{"sha7": c.SHA7, "subject": c.Subject, "kind": c.Kind, "repo": c.Repo}
		if c.URL != "" {
			row["url"] = c.URL
		}
		if c.AuthoredAt != nil {
			row["authoredAt"] = iso(c.AuthoredAt.Time)
		}
		rows = append(rows, row)
	}
	out["commits"] = rows
	out["commits_total"] = res.Total
}

// RegisterCommitTools 登記 commits 工具。執行環境(Call.Ctx)要實作 Env
func RegisterCommitTools(s *Server) {
	s.Register("commits_report", func(c *Call) (any, error) {
		e := env(c)
		repo, err := c.Args.Require("repo")
		if err != nil {
			return nil, err
		}
		if _, ok := model.CommitRepoKey(repo); !ok {
			return nil, Errorf("repo may only contain letters, digits and - _ . / : (max %d characters), e.g. owner/name", model.RepoMaxLen)
		}
		source, err := c.Args.OptionalEnum("source", model.CommitReportSources)
		if err != nil {
			return nil, err
		}
		list, err := commitInputs(c.Args.Raw["commits"])
		if err != nil {
			return nil, err
		}
		res, err := e.Backend().ReportCommits(e.Org(), service.CommitReport{Repo: repo, Source: source, Commits: list})
		if err != nil {
			return nil, prefixErrEN("The commits were not saved: ", commitErr(err, ""))
		}
		return res, nil
	})

	s.Register("commits", func(c *Call) (any, error) {
		e := env(c)
		in := service.CommitQueryInput{Repo: c.Args.Str("repo"), SHA: c.Args.Str("sha"), Issue: c.Args.Str("issue"), Query: c.Args.Str("query"), Limit: 20}
		if n, ok := c.Args.Int("limit"); ok {
			in.Limit = max(1, min(n, model.CommitQueryMaxLimit))
		}
		if in.SHA != "" {
			if _, ok := model.CommitSHAPrefix(in.SHA); !ok {
				return nil, Errorf("sha must be a hex prefix of at least %d characters", model.CommitShortSHA)
			}
		}
		res, err := e.Backend().QueryCommits(e.Org(), in)
		if err != nil {
			return nil, commitErr(err, in.Issue)
		}
		rows := make([]any, 0, len(res.Commits))
		for _, v := range res.Commits {
			rows = append(rows, commitResultRow(v, in.SHA != ""))
		}
		return map[string]any{"total": res.Total, "commits": rows}, nil
	})

	s.Register("issue_commit_link", func(c *Call) (any, error) {
		e := env(c)
		issue, err := c.Args.Require("issue")
		if err != nil {
			return nil, err
		}
		repo, err := c.Args.Require("repo")
		if err != nil {
			return nil, err
		}
		sha, err := c.Args.Require("sha")
		if err != nil {
			return nil, err
		}
		if _, ok := model.CommitRepoKey(repo); !ok {
			return nil, Errorf("repo may only contain letters, digits and - _ . / : (max %d characters), e.g. owner/name", model.RepoMaxLen)
		}
		if _, ok := model.CleanSHA(sha); !ok {
			return nil, Errorf("sha must be the full 40-character hex sha (find it with the commits tool)")
		}
		kind, err := c.Args.OptionalEnum("kind", model.ManualCommitLinkKinds)
		if err != nil {
			return nil, err
		}
		v, err := e.Backend().LinkIssueCommit(e.Org(), issue, service.CommitLinkInput{Repo: repo, SHA: sha, Kind: kind, Note: c.Args.Str("note")})
		if err != nil {
			return nil, prefixErrEN("The link was not saved: ", commitErr(err, issue))
		}
		out := map[string]any{"issue": v.Issue, "repo": v.Repo, "sha": v.SHA, "sha7": v.SHA7, "kind": v.Kind, "origin": v.Origin, "subject": v.Subject}
		if v.Note != "" {
			out["note"] = v.Note
		}
		return out, nil
	})

	s.Register("issue_commit_unlink", func(c *Call) (any, error) {
		e := env(c)
		issue, err := c.Args.Require("issue")
		if err != nil {
			return nil, err
		}
		repo, err := c.Args.Require("repo")
		if err != nil {
			return nil, err
		}
		sha, err := c.Args.Require("sha")
		if err != nil {
			return nil, err
		}
		if _, ok := model.CommitRepoKey(repo); !ok {
			return nil, Errorf("repo may only contain letters, digits and - _ . / : (max %d characters), e.g. owner/name", model.RepoMaxLen)
		}
		if _, ok := model.CommitSHAPrefix(sha); !ok {
			return nil, Errorf("sha must be the full 40-character sha or a hex prefix of at least %d characters", model.CommitShortSHA)
		}
		if err := e.Backend().UnlinkIssueCommit(e.Org(), issue, service.CommitUnlinkInput{Repo: repo, SHA: sha}); err != nil {
			return nil, prefixErrEN("The link was not removed: ", commitErr(err, issue))
		}
		return map[string]any{"ok": true, "issue": issue, "repo": repo, "sha": sha}, nil
	})
}

// prefixErrEN 在工具錯誤訊息前面加一段英文說明
func prefixErrEN(prefix string, err error) error {
	te, ok := err.(ToolError)
	if !ok {
		return err
	}
	te.Message = prefix + te.Message
	return te
}

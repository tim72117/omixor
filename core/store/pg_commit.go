package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- commits 與 Issue 的連結(PG;表的定義在 schema_commits.go,規則在 service/commits.go) ----
//
//	commits:(org, repo, sha) 唯一;repo 是 model.CommitRepoKey 正規化後的字串(保留 github:/gitlab: 前綴);subject 空字串 = 裸參照(只被連結引用、沒人回報過內容)
//	issue_commits:(org, issue_id, repo, sha) 唯一;issue_id 是 Issue 內部 id(Issue 永久刪除時連結跟著刪);(org, repo, sha) 外鍵指向 commits
//	repo_sync:(org, repo) 一列
//	reported_by、linked_by 是 actor id 字串,不設外鍵

const commitCols = `org, repo, sha, subject, body, author_name, authored_at, committed_at, branch, parents, url, source, verified, reported_by, reported_at`

func scanCommit(row pgx.Row) (*model.Commit, error) {
	var c model.Commit
	var authored, committed *time.Time
	var reported time.Time
	if err := row.Scan(&c.Org, &c.Repo, &c.SHA, &c.Subject, &c.Body, &c.AuthorName, &authored, &committed, &c.Branch, &c.Parents, &c.URL,
		&c.Source, &c.Verified, &c.ReportedBy, &reported); err != nil {
		return nil, notFound(err)
	}
	c.AuthoredAt, c.CommittedAt, c.ReportedAt = tp(authored), tp(committed), utcTime(reported)
	if len(c.Parents) == 0 {
		c.Parents = nil
	}
	return &c, nil
}

func nilTime(t *model.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	x := t.Time.UTC()
	return &x
}

func parentsArg(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// UpsertCommits 見 Store.UpsertCommits
func (p *PG) UpsertCommits(org string, list []model.Commit) (int, error) {
	if len(list) == 0 {
		return 0, nil
	}
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	created := 0
	for _, x := range list {
		reported := x.ReportedAt.Time
		if reported.IsZero() {
			reported = time.Now().UTC()
		}
		var ins bool
		err := tx.QueryRow(c, `insert into omixor_commits as c (`+commitCols+`)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			on conflict (org, repo, sha) do update set
			  subject = case when c.subject = '' then excluded.subject else c.subject end,
			  body = case when c.subject = '' then excluded.body else c.body end,
			  author_name = case when c.author_name = '' then excluded.author_name else c.author_name end,
			  authored_at = coalesce(c.authored_at, excluded.authored_at),
			  committed_at = coalesce(c.committed_at, excluded.committed_at),
			  branch = case when c.branch = '' then excluded.branch else c.branch end,
			  parents = case when cardinality(c.parents) = 0 then excluded.parents else c.parents end,
			  url = case when c.url = '' then excluded.url else c.url end,
			  source = case when c.subject = '' then excluded.source else c.source end,
			  verified = c.verified or excluded.verified,
			  reported_by = case when c.subject = '' then excluded.reported_by else c.reported_by end,
			  reported_at = case when c.subject = '' then excluded.reported_at else c.reported_at end
			returning (xmax = 0)`,
			org, x.Repo, x.SHA, x.Subject, x.Body, x.AuthorName, nilTime(x.AuthoredAt), nilTime(x.CommittedAt), x.Branch, parentsArg(x.Parents),
			x.URL, x.Source, x.Verified, x.ReportedBy, reported.UTC()).Scan(&ins)
		if err != nil {
			return 0, fkErr(err)
		}
		if ins {
			created++
		}
	}
	if err := tx.Commit(c); err != nil {
		return 0, err
	}
	return created, nil
}

// EnsureCommitRef 見 Store.EnsureCommitRef
func (p *PG) EnsureCommitRef(org string, x model.Commit) error {
	c, cancel := ctx()
	defer cancel()
	reported := x.ReportedAt.Time
	if reported.IsZero() {
		reported = time.Now().UTC()
	}
	_, err := p.pool.Exec(c, `insert into omixor_commits (`+commitCols+`)
		values ($1, $2, $3, '', '', '', null, null, '', '{}', $4, $5, false, $6, $7)
		on conflict (org, repo, sha) do nothing`, org, x.Repo, x.SHA, x.URL, x.Source, x.ReportedBy, reported.UTC())
	return fkErr(err)
}

func (p *PG) GetCommit(org, repo, sha string) (*model.Commit, error) {
	c, cancel := ctx()
	defer cancel()
	return scanCommit(p.pool.QueryRow(c, `select `+commitCols+` from omixor_commits where org = $1 and repo = $2 and sha = $3`, org, repo, sha))
}

// likeEscape 把使用者輸入當純文字放進 ilike(跳脫 \ % _)
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (p *PG) QueryCommits(org string, q CommitQuery) ([]model.Commit, int, error) {
	c, cancel := ctx()
	defer cancel()
	where := []string{"c.org = $1"}
	args := []any{org}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if q.Repo != "" {
		where = append(where, "c.repo = "+arg(q.Repo))
	}
	if q.SHAPrefix != "" {
		where = append(where, "c.sha like "+arg(likeEscape(q.SHAPrefix)+"%"))
	}
	for _, w := range strings.Fields(q.Query) {
		n := arg("%" + likeEscape(w) + "%")
		where = append(where, fmt.Sprintf("(c.subject ilike %s or c.body ilike %s)", n, n))
	}
	if q.Issue != "" {
		where = append(where, "exists (select 1 from omixor_issue_commits l where l.org = c.org and l.repo = c.repo and l.sha = c.sha and l.issue_id = "+arg(q.Issue)+")")
	}
	cond := strings.Join(where, " and ")
	var total int
	if err := p.pool.QueryRow(c, `select count(*) from omixor_commits c where `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, model.CommitQueryMaxLimit)
	cols := "c." + strings.ReplaceAll(commitCols, ", ", ", c.")
	rows, err := p.pool.Query(c, `select `+cols+` from omixor_commits c where `+cond+
		` order by coalesce(c.authored_at, c.reported_at) desc, c.sha limit `+arg(limit), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.Commit{}
	for rows.Next() {
		x, err := scanCommit(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *x)
	}
	return out, total, rows.Err()
}

func (p *PG) LinkIssueCommit(org string, l model.IssueCommit) (bool, error) {
	c, cancel := ctx()
	defer cancel()
	at := l.LinkedAt.Time
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var changed bool
	var err error
	if l.Origin == model.CommitOriginManual {
		// 手動:覆蓋已有連結的種類、說明與署名(內容完全相同則不算變動)
		err = p.pool.QueryRow(c, `insert into omixor_issue_commits as k (org, issue_id, repo, sha, kind, origin, linked_by, linked_at, note)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (org, issue_id, repo, sha) do update set kind = excluded.kind, origin = excluded.origin, linked_by = excluded.linked_by,
			  linked_at = excluded.linked_at, note = excluded.note
			  where (k.kind, k.origin, k.note) is distinct from (excluded.kind, excluded.origin, excluded.note)
			returning true`, org, l.Issue, l.Repo, l.SHA, l.Kind, l.Origin, l.LinkedBy, at.UTC(), l.Note).Scan(&changed)
	} else {
		// 自動:不蓋手動的;已有的 auto mentions 在這次是 fixes 時升級
		err = p.pool.QueryRow(c, `insert into omixor_issue_commits as k (org, issue_id, repo, sha, kind, origin, linked_by, linked_at, note)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			on conflict (org, issue_id, repo, sha) do update set kind = excluded.kind
			  where k.origin = 'auto' and k.kind = 'mentions' and excluded.kind = 'fixes'
			returning true`, org, l.Issue, l.Repo, l.SHA, l.Kind, l.Origin, l.LinkedBy, at.UTC(), l.Note).Scan(&changed)
	}
	if errors.Is(err, pgx.ErrNoRows) { // 衝突但沒有要改的:冪等,沒有變動
		return false, nil
	}
	if err != nil {
		return false, fkErr(err)
	}
	return changed, nil
}

func (p *PG) UnlinkIssueCommit(org, issueID, repo, sha string) error {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_issue_commits where org = $1 and issue_id = $2 and repo = $3 and sha = $4`, org, issueID, repo, sha)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PG) ListIssueCommits(org, issueID string, limit int) ([]model.IssueCommitView, int, error) {
	c, cancel := ctx()
	defer cancel()
	var total int
	if err := p.pool.QueryRow(c, `select count(*) from omixor_issue_commits where org = $1 and issue_id = $2`, org, issueID).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `select l.issue_id, l.repo, l.sha, c.subject, l.kind, l.origin, c.url, c.author_name, c.authored_at, c.verified, l.linked_by, l.linked_at, l.note
		from omixor_issue_commits l join omixor_commits c on c.org = l.org and c.repo = l.repo and c.sha = l.sha
		where l.org = $1 and l.issue_id = $2 order by l.linked_at desc, l.sha`
	args := []any{org, issueID}
	if limit > 0 {
		q += " limit $3"
		args = append(args, limit)
	}
	rows, err := p.pool.Query(c, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.IssueCommitView{}
	for rows.Next() {
		var v model.IssueCommitView
		var authored *time.Time
		var linked time.Time
		if err := rows.Scan(&v.IssueID, &v.Repo, &v.SHA, &v.Subject, &v.Kind, &v.Origin, &v.URL, &v.AuthorName, &authored, &v.Verified, &v.LinkedBy, &linked, &v.Note); err != nil {
			return nil, 0, err
		}
		v.SHA7, v.AuthoredAt, v.LinkedAt = model.ShortSHA(v.SHA), tp(authored), utcTime(linked)
		out = append(out, v)
	}
	return out, total, rows.Err()
}

func (p *PG) ListCommitLinks(org, repo string, shas []string) (map[string][]model.IssueCommit, error) {
	out := map[string][]model.IssueCommit{}
	if len(shas) == 0 {
		return out, nil
	}
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select issue_id, repo, sha, kind, origin, linked_by, linked_at, note from omixor_issue_commits
		where org = $1 and repo = $2 and sha = any($3) order by linked_at, issue_id`, org, repo, shas)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		l := model.IssueCommit{Org: org}
		var linked time.Time
		if err := rows.Scan(&l.Issue, &l.Repo, &l.SHA, &l.Kind, &l.Origin, &l.LinkedBy, &linked, &l.Note); err != nil {
			return nil, err
		}
		l.LinkedAt = utcTime(linked)
		out[l.SHA] = append(out[l.SHA], l)
	}
	return out, rows.Err()
}

func (p *PG) GetRepoSync(org, repo string) (*model.RepoSync, error) {
	c, cancel := ctx()
	defer cancel()
	s := model.RepoSync{Org: org}
	var at time.Time
	err := p.pool.QueryRow(c, `select repo, last_sha, last_at, last_source from omixor_repo_sync where org = $1 and repo = $2`, org, repo).Scan(&s.Repo, &s.LastSHA, &at, &s.LastSource)
	if err != nil {
		return nil, notFound(err)
	}
	s.LastAt = utcTime(at)
	return &s, nil
}

func (p *PG) PutRepoSync(org string, s model.RepoSync) error {
	c, cancel := ctx()
	defer cancel()
	at := s.LastAt.Time
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := p.pool.Exec(c, `insert into omixor_repo_sync (org, repo, last_sha, last_at, last_source) values ($1, $2, $3, $4, $5)
		on conflict (org, repo) do update set last_sha = excluded.last_sha, last_at = excluded.last_at, last_source = excluded.last_source`,
		org, s.Repo, s.LastSHA, at.UTC(), s.LastSource)
	return fkErr(err)
}

// FindCommitRepos 見 Store.FindCommitRepos
func (p *PG) FindCommitRepos(org, path string) ([]string, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select distinct repo from omixor_commits where org = $1 and repo = any($2) order by repo`, org, model.CommitRepoCandidates(path))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// IssueCommitStats 見 Store.IssueCommitStats
func (p *PG) IssueCommitStats(org, issueID, repo, sha string) (IssueCommitStats, error) {
	c, cancel := ctx()
	defer cancel()
	var st IssueCommitStats
	err := p.pool.QueryRow(c, `select count(*), count(*) filter (where c.subject = ''),
		coalesce(bool_or(l.repo = $3 and l.sha = $4), false)
		from omixor_issue_commits l join omixor_commits c on c.org = l.org and c.repo = l.repo and c.sha = l.sha
		where l.org = $1 and l.issue_id = $2`, org, issueID, repo, sha).Scan(&st.Total, &st.Bare, &st.Linked)
	return st, err
}

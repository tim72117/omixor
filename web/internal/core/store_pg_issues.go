package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Issue 存在資料庫(設了 OPS_DATABASE_URL 時)。和其他設定檔不同,Issue 用正規的表而不是 ops_documents 的一整份 JSON:
//   - 一筆 Issue 一列,改一筆只寫那一列(不用每次覆寫整份清單)
//   - 可以直接用 SQL 查(依組織、狀態、優先度、標籤、作者…),之後要給報表或其他服務用也方便
//   - 欄位有型別與檢查(狀態、優先度只能是固定的值,時間是 timestamptz)
//
//	ops_issues          一筆 Issue 一列;主鍵 (org, id),pos 是清單順序(大的在前,新建立的最大)
//	ops_issue_comments  留言;主鍵 (org, issue_id, pos),Issue 刪掉時一起刪
//
// 記憶體裡照舊有目前組織的整份清單(畫面與 MCP 都讀它);存檔時和上次存的內容比對,只寫有變的列。
// 時間存到秒(和 issues.json 一樣)。

const pgIssueSchema = `
create table if not exists ops_issues (
	org text not null,
	id text not null,
	number integer not null,
	pos bigint not null,
	title text not null,
	body text not null default '',
	status text not null check (status in ('open', 'closed')),
	priority text not null check (priority in ('urgent', 'high', 'normal', 'low')),
	labels text[] not null default '{}',
	source text not null default '',
	link text not null default '',
	created_by text not null,
	created_at timestamptz not null,
	updated_at timestamptz not null,
	closed_at timestamptz,
	primary key (org, id)
);
create index if not exists ops_issues_org_pos on ops_issues (org, pos desc);
create table if not exists ops_issue_comments (
	org text not null,
	issue_id text not null,
	pos integer not null,
	id text not null,
	author text not null,
	text text not null,
	at timestamptz not null,
	primary key (org, issue_id, pos),
	foreign key (org, issue_id) references ops_issues (org, id) on delete cascade on update cascade
);
`

func init() { pgExtraSchema = append(pgExtraSchema, pgIssueSchema) }

func issueOrgKey(org string) string { return "orgs/" + org + "/issues.json" }

func secs(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func issueSnap(i OpsIssue, pos int64) string {
	return string(Marshal(i)) + "|" + strconv.FormatInt(pos, 10)
}

// pgLoadIssues 讀 s.Org 的 Issue。表裡還沒有這個組織、但 ops_documents 有舊的 orgs/<組織>/issues.json
// (用 omixor-web import 匯入過,或第一次啟動從根目錄搬過來的)時,先把它搬進表裡,舊文件改名保留。
func pgLoadIssues(s *issueState) {
	list, err := PGReadIssues(s.Org)
	if err != nil {
		log.Printf("讀 Issue 失敗:%v", err)
		list = []OpsIssue{}
	}
	if len(list) == 0 {
		key := issueOrgKey(s.Org)
		if b, err := (pgBackend{}).Read(key); err == nil {
			var old []OpsIssue
			if json.Unmarshal(b, &old) == nil && len(old) > 0 {
				if _, err := PGImportIssues(s.Org, old, false); err == nil {
					_ = (pgBackend{}).Rename(key, key+".migrated")
					log.Printf("把 %s 的 %d 筆 Issue 搬進 ops_issues", key, len(old))
					list, _ = PGReadIssues(s.Org)
				} else {
					log.Printf("搬 %s 失敗:%v", key, err)
				}
			}
		}
	}
	s.Issues = list
	s.saved = map[string]string{}
	for k, i := range list {
		s.saved[i.ID] = issueSnap(i, int64(len(list)-k))
	}
}

// PGReadIssues 一個組織的全部 Issue(清單順序,新的在前)
func PGReadIssues(org string) ([]OpsIssue, error) {
	ctx, cancel := pgCtx()
	defer cancel()
	rows, err := pgPool.Query(ctx, `select id, title, body, status, priority, labels, source, link, created_by,
		created_at, updated_at, closed_at from ops_issues where org = $1 order by pos desc, number desc`, org)
	if err != nil {
		return nil, err
	}
	list := []OpsIssue{}
	idx := map[string]int{}
	for rows.Next() {
		var i OpsIssue
		var created, updated time.Time
		var closed *time.Time
		if err := rows.Scan(&i.ID, &i.Title, &i.Body, &i.Status, &i.Priority, &i.Labels, &i.Source, &i.Link, &i.CreatedBy,
			&created, &updated, &closed); err != nil {
			rows.Close()
			return nil, err
		}
		i.CreatedAt, i.UpdatedAt = Time{created.UTC()}, Time{updated.UTC()}
		if closed != nil {
			i.ClosedAt = &Time{closed.UTC()}
		}
		if i.Labels == nil {
			i.Labels = []string{}
		}
		i.Comments = []IssueComment{}
		idx[i.ID] = len(list)
		list = append(list, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	crows, err := pgPool.Query(ctx, `select issue_id, id, author, text, at from ops_issue_comments where org = $1 order by issue_id, pos`, org)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var iid string
		var c IssueComment
		var at time.Time
		if err := crows.Scan(&iid, &c.ID, &c.Author, &c.Text, &at); err != nil {
			return nil, err
		}
		c.At = Time{at.UTC()}
		if k, ok := idx[iid]; ok {
			list[k].Comments = append(list[k].Comments, c)
		}
	}
	return list, crows.Err()
}

// pgSaveIssues 和上次存的比對,只寫有變的列、刪掉不在清單裡的(一個交易)
func pgSaveIssues(s *issueState) {
	if s.saved == nil {
		s.saved = map[string]string{}
	}
	ctx, cancel := pgCtx()
	defer cancel()
	tx, err := pgPool.Begin(ctx)
	if err != nil {
		log.Printf("存 Issue 失敗:%v", err)
		return
	}
	defer tx.Rollback(ctx)
	next := map[string]string{}
	n := len(s.Issues)
	for k, i := range s.Issues {
		pos := int64(n - k)
		snap := issueSnap(i, pos)
		next[i.ID] = snap
		if s.saved[i.ID] == snap {
			continue
		}
		if err := pgUpsertIssue(ctx, tx, s.Org, i, pos); err != nil {
			log.Printf("存 Issue %s 失敗:%v", i.ID, err)
			return
		}
	}
	for id := range s.saved {
		if _, ok := next[id]; !ok {
			if _, err := tx.Exec(ctx, `delete from ops_issues where org = $1 and id = $2`, s.Org, id); err != nil {
				log.Printf("刪 Issue %s 失敗:%v", id, err)
				return
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("存 Issue 失敗:%v", err)
		return
	}
	s.saved = next
}

func pgUpsertIssue(ctx context.Context, tx pgx.Tx, org string, i OpsIssue, pos int64) error {
	var closed *time.Time
	if i.ClosedAt != nil && !i.ClosedAt.IsZero() {
		t := secs(i.ClosedAt.Time)
		closed = &t
	}
	labels := i.Labels
	if labels == nil {
		labels = []string{}
	}
	if _, err := tx.Exec(ctx, `insert into ops_issues (org, id, number, pos, title, body, status, priority, labels, source, link,
			created_by, created_at, updated_at, closed_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		on conflict (org, id) do update set number = excluded.number, pos = excluded.pos, title = excluded.title,
			body = excluded.body, status = excluded.status, priority = excluded.priority, labels = excluded.labels,
			source = excluded.source, link = excluded.link, created_by = excluded.created_by, created_at = excluded.created_at,
			updated_at = excluded.updated_at, closed_at = excluded.closed_at`,
		org, i.ID, i.Number(), pos, i.Title, i.Body, i.Status, i.Priority, labels, i.Source, i.Link,
		i.CreatedBy, secs(i.CreatedAt.Time), secs(i.UpdatedAt.Time), closed); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from ops_issue_comments where org = $1 and issue_id = $2`, org, i.ID); err != nil {
		return err
	}
	for k, c := range i.Comments {
		if _, err := tx.Exec(ctx, `insert into ops_issue_comments (org, issue_id, pos, id, author, text, at) values ($1, $2, $3, $4, $5, $6, $7)`,
			org, i.ID, k, c.ID, c.Author, c.Text, secs(c.At.Time)); err != nil {
			return err
		}
	}
	return nil
}

// IssueImportResult 匯入結果
type IssueImportResult struct {
	Org      string `json:"org"`
	Inserted int    `json:"inserted"`
	Updated  int    `json:"updated"`
	Skipped  int    `json:"skipped"`
}

// PGImportIssues 把一份 issues.json 的內容匯入資料庫(一個交易)。可以重跑:id 已經在表裡的,
// overwrite=false 時略過(保留資料庫裡的版本),overwrite=true 時用檔案的內容覆蓋。
// 順序照檔案(檔案第一筆最新);新匯入的排在既有的後面,依檔案順序。
func PGImportIssues(org string, list []OpsIssue, overwrite bool) (IssueImportResult, error) {
	res := IssueImportResult{Org: org}
	if pgPool == nil {
		return res, errors.New("沒有連資料庫")
	}
	if !ValidOrgID(org) {
		return res, fmt.Errorf("組織 id 不對:%q", org)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := pgPool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('ops_issues:' || $1::text))`, org); err != nil {
		return res, err
	}
	existing := map[string]int64{}
	var minPos int64 = 1 << 40
	rows, err := tx.Query(ctx, `select id, pos from ops_issues where org = $1`, org)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		var pos int64
		if err := rows.Scan(&id, &pos); err != nil {
			rows.Close()
			return res, err
		}
		existing[id] = pos
		minPos = min(minPos, pos)
	}
	rows.Close()
	if len(existing) == 0 {
		minPos = int64(len(list)) + 1
	}
	seen := map[string]bool{}
	k := int64(0)
	for _, i := range list {
		if strings.TrimSpace(i.ID) == "" || seen[i.ID] {
			res.Skipped++
			continue
		}
		seen[i.ID] = true
		i.Labels = CleanLabels(i.Labels)
		if pos, ok := existing[i.ID]; ok {
			if !overwrite {
				res.Skipped++
				continue
			}
			if err := pgUpsertIssue(ctx, tx, org, i, pos); err != nil {
				return res, fmt.Errorf("%s:%w", i.ID, err)
			}
			res.Updated++
			continue
		}
		k++
		if err := pgUpsertIssue(ctx, tx, org, i, minPos-k); err != nil {
			return res, fmt.Errorf("%s:%w", i.ID, err)
		}
		res.Inserted++
	}
	return res, tx.Commit(ctx)
}

// ImportIssuesPath 匯入 issues.json:path 是檔案(匯入到 org),或資料夾(匯入 orgs/<組織>/issues.json,org 不用給)
func ImportIssuesPath(path, org string, overwrite bool) ([]IssueImportResult, error) {
	if pgPool == nil {
		return nil, errors.New("沒有連資料庫")
	}
	var out []IssueImportResult
	one := func(file, org string) error {
		var list []OpsIssue
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &list); err != nil {
			return fmt.Errorf("%s 不是 Issue 清單:%w", file, err)
		}
		r, err := PGImportIssues(org, list, overwrite)
		if err != nil {
			return err
		}
		out = append(out, r)
		return nil
	}
	if strings.HasSuffix(path, ".json") {
		if org == "" {
			org = DefaultOrgID
		}
		return out, one(path, org)
	}
	matches, _ := filepath.Glob(filepath.Join(path, "orgs", "*", "issues.json"))
	if len(matches) == 0 {
		return nil, fmt.Errorf("%s 底下沒有 orgs/<組織>/issues.json", path)
	}
	for _, m := range matches {
		o := filepath.Base(filepath.Dir(m))
		if org != "" && o != org {
			continue
		}
		if err := one(m, o); err != nil {
			return out, err
		}
	}
	return out, nil
}

// OpenIssueDB 只為了匯入 Issue 連資料庫(建表,不換存放處、不需要 OPS_SECRET_KEY)
func OpenIssueDB(url string) error {
	ctx, cancel := pgCtx()
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("資料庫網址格式不對:%w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("連不到資料庫:%w", err)
	}
	for _, sc := range append([]string{pgSchema}, pgExtraSchema...) {
		if _, err := pool.Exec(ctx, sc); err != nil {
			pool.Close()
			return fmt.Errorf("建表失敗:%w", err)
		}
	}
	pgPool = pool
	return nil
}

// pgMoveOrgIssues 刪除組織時,把它的 Issue 改掛到 deleted-orgs/<組織>-<時間>(不直接刪掉,同 ops_documents)
func pgMoveOrgIssues(id string) {
	ctx, cancel := pgCtx()
	defer cancel()
	if _, err := pgPool.Exec(ctx, `update ops_issues set org = $2 where org = $1`, id,
		fmt.Sprintf("deleted-orgs/%s-%d", id, time.Now().Unix())); err != nil {
		log.Printf("搬移組織 %s 的 Issue 失敗:%v", id, err)
	}
}

func init() {
	OnOrgDelete(func(a *App, id, dir string) {
		if pgPool != nil {
			pgMoveOrgIssues(id)
		}
	})
}

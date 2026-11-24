// pg_issue.go Issue、留言與圖片附件的 PG 實作
//
// 識別:id 是內部 id(UUIDv7,伺服器產生);key 是顯示編號(ISS-<n> / <專案代號>-<n>),由 omixor_seq 的
// scope issue / issue:<專案 id> 發號。每張 Issue 目前與過去用過的 key 都在別名表 omixor_issue_keys(key 一律存大寫)。
// 所有收 Issue 編號的方法(GetIssue、UpdateIssue、AddIssueComment、TrashIssue、DeleteIssue…)都接受 UUID、目前或舊的 key
// (見 issueRef:先查別名表、再當成 id)。
//
// 軟刪除:deleted_at 不是 NULL = 在垃圾桶裡。ListIssues、GetIssue 與所有寫入都當作不存在(ErrNotFound);
// ListDeletedIssues 列垃圾桶、RestoreIssue 還原、DeleteIssue 才是真的刪除(永久)。key 與別名保留,序號不回收;
// 永久刪除時 key 記進退役表 omixor_issue_keys_retired,之後不再發(跨專案代號重用也不會)。
//
// 樂觀鎖:version 每次改本體(UpdateIssue、MoveIssue、TrashIssue、RestoreIssue)+1;新增留言不動 version。

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/model"
)

// ErrProjectNoKey 專案還沒設定代號,不能在裡面建 Issue 或把 Issue 搬進去
var ErrProjectNoKey = errors.New("project has no key")

// ErrDuplicateKey 專案代號在組織內重複(唯一索引)
var ErrDuplicateKey = errors.New("duplicate key")

// issueRef 把 $n(Issue 編號:UUID、目前或舊的 key)解析成內部 id 的子查詢($1 = org)。
// 先查別名表(key 不分大小寫),查不到再當成 id。id 一律存小寫的 UUID,所以直接 x.id = lower(...),走 (org, id) 主鍵索引
func issueRef(n int) string {
	return fmt.Sprintf(`coalesce((select k.issue_id from omixor_issue_keys k where k.org = $1 and k.key = upper(btrim($%[1]d::text))),
		(select x.id from omixor_issues x where x.org = $1 and x.id = lower(btrim($%[1]d::text))))`, n)
}

// CountProjectIssues 見 Store.CountProjectIssues
func (p *PG) CountProjectIssues(org, project string) (int, error) {
	c, cancel := ctx()
	defer cancel()
	var n int
	err := p.pool.QueryRow(c, `select count(*) from omixor_issues where org = $1 and project = $2`, org, project).Scan(&n)
	return n, err
}

// ProjectKeyAliasTaken 見 Store.ProjectKeyAliasTaken:只看「過期」的別名(不是任何 Issue 目前的 key:搬走的舊 key、舊格式 id);
// 專案自己 Issue 的目前 key 不算
func (p *PG) ProjectKeyAliasTaken(org, prefix string) (string, error) {
	c, cancel := ctx()
	defer cancel()
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	if prefix == "" {
		return "", nil
	}
	pat := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + `-%`
	var k string
	err := p.pool.QueryRow(c, `select k.key from omixor_issue_keys k where k.org = $1 and k.key like $2
		and not exists (select 1 from omixor_issues x where x.org = k.org and x.id = k.issue_id and upper(x.key) = k.key) limit 1`, org, pat).Scan(&k)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return k, err
}

// ErrIssueKeyTaken 別名表裡這個 key 已經指向別張 Issue(發號會跳號重試,其他情況原樣回報)
var ErrIssueKeyTaken = errors.New("issue key taken by another issue")

// nextIssueKey 在 scope 發下一個 key:號碼被別名表(搬走的舊 key、舊格式 id)或退役表 omixor_issue_keys_retired
// (永久刪除的 Issue 的 key、舊版留下的孤兒參照)佔用就跳號。
// 號碼單調往上,所以最多跳過現有的別名數;上限 100000 保險
func nextIssueKey(c context.Context, tx pgx.Tx, org, project, prefix string) (string, int, error) {
	for range 100000 {
		n, err := seqNext(c, tx, org, model.IssueScope(project), 0)
		if err != nil {
			return "", 0, err
		}
		key := model.IssueKey(prefix, n)
		var taken bool
		if err := tx.QueryRow(c, `select exists (select 1 from omixor_issue_keys where org = $1 and key = upper($2))
			or exists (select 1 from omixor_issue_keys_retired where org = $1 and key = upper($2))
			or exists (select 1 from omixor_issues where org = $1 and key <> '' and upper(key) = upper($2))`, org, key).Scan(&taken); err != nil {
			return "", 0, err
		}
		if !taken {
			return key, n, nil
		}
	}
	return "", 0, ErrIssueKeyTaken
}

// ---- Issue ----

// issueDel 列表要哪些 Issue
type issueDel int

const (
	liveIssues    issueDel = iota // 沒刪的(一般)
	deletedIssues                 // 垃圾桶
	anyIssues                     // 全部(匯出用)
)

func (p *PG) ListIssues(org string) ([]model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	return listIssues(c, p.pool, org, "", liveIssues)
}

// ListDeletedIssues 垃圾桶裡的 Issue(新的在前)
func (p *PG) ListDeletedIssues(org string) ([]model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	return listIssues(c, p.pool, org, "", deletedIssues)
}

// ListIssuesWithDeleted 全部 Issue,含垃圾桶裡的(匯出用;新的在前)
func (p *PG) ListIssuesWithDeleted(org string) ([]model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	return listIssues(c, p.pool, org, "", anyIssues)
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// 「誰」欄位直接存 actor id(created_by 不可為 NULL;assignee、deleted_by 沒有時是 NULL,用 NullID 掃描)
const issueCols = `id, title, body, status, priority, labels, source, link, created_by,
		created_at, updated_at, closed_at, assignee, repo, project, key, number, version, deleted_at, deleted_by`

// listIssuesRef 依編號(UUID、目前或舊的 key)找單一 Issue:編號是空字串或只有空白時一律找不到(回空清單,
// 呼叫端照「找不到」處理)。不能直接用 listIssues 的 ref 參數,因為它把空字串當成「全部」,GetIssue(org, "")
// 會回該組織最新的一張
func listIssuesRef(c context.Context, q querier, org, ref string, del issueDel) ([]model.Issue, error) {
	if strings.TrimSpace(ref) == "" {
		return []model.Issue{}, nil
	}
	return listIssues(c, q, org, ref, del)
}

// listIssues 列 Issue。ref 空字串 = 全部;給了 ref 只列那一張(單一 Issue 的入口一律走 listIssuesRef)
func listIssues(c context.Context, q querier, org, ref string, del issueDel) ([]model.Issue, error) {
	where, args := `org = $1`, []any{org}
	if ref != "" {
		where, args = `org = $1 and id = `+issueRef(2), []any{org, ref}
	}
	switch del {
	case liveIssues:
		where += ` and deleted_at is null`
	case deletedIssues:
		where += ` and deleted_at is not null`
	}
	rows, err := q.Query(c, `select `+issueCols+` from omixor_issues where `+where+` order by pos desc, number desc`, args...)
	if err != nil {
		return nil, err
	}
	list := []model.Issue{}
	idx := map[string]int{}
	for rows.Next() {
		var i model.Issue
		var created, updated time.Time
		var closed, deleted *time.Time
		var assignee, deletedBy NullID
		if err := rows.Scan(&i.ID, &i.Title, &i.Body, &i.Status, &i.Priority, &i.Labels, &i.Source, &i.Link, &i.CreatedBy,
			&created, &updated, &closed, &assignee, &i.Repo, &i.Project, &i.Key, &i.Num, &i.Version, &deleted, &deletedBy); err != nil {
			rows.Close()
			return nil, err
		}
		i.Assignee, i.DeletedBy = assignee.String(), deletedBy.String()
		i.CreatedAt, i.UpdatedAt, i.ClosedAt, i.DeletedAt = model.Time{Time: created.UTC()}, model.Time{Time: updated.UTC()}, tp(closed), tp(deleted)
		if i.Labels == nil {
			i.Labels = []string{}
		}
		i.Comments = []model.Comment{}
		idx[i.ID] = len(list)
		list = append(list, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if ref != "" && len(list) == 0 {
		return list, nil
	}
	cw, cargs := `org = $1`, []any{org}
	if ref != "" && len(list) == 1 {
		cw, cargs = `org = $1 and issue_id = $2`, []any{org, list[0].ID}
	}
	crows, err := q.Query(c, `select issue_id, id, author, text, at from omixor_issue_comments where `+cw+` order by issue_id, pos`, cargs...)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var iid string
		var cm model.Comment
		var at time.Time
		if err := crows.Scan(&iid, &cm.ID, &cm.Author, &cm.Text, &at); err != nil {
			return nil, err
		}
		cm.At = model.Time{Time: at.UTC()}
		if k, ok := idx[iid]; ok {
			list[k].Comments = append(list[k].Comments, cm)
		}
	}
	return list, crows.Err()
}

// GetIssue id 可以是 UUID、目前或舊的 key(不分大小寫);垃圾桶裡的回 ErrNotFound
func (p *PG) GetIssue(org, id string) (*model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	l, err := listIssuesRef(c, p.pool, org, id, liveIssues)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

// ResolveIssueID Issue 編號(UUID、目前或舊的 key,不分大小寫)→ 內部 id;含垃圾桶裡的。找不到回 ErrNotFound
func (p *PG) ResolveIssueID(org, ref string) (string, error) {
	c, cancel := ctx()
	defer cancel()
	return resolveIssue(c, p.pool, org, ref)
}

func resolveIssue(c context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, org, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", ErrNotFound
	}
	var id string
	err := q.QueryRow(c, `select id from omixor_issues where org = $1 and id = `+issueRef(2), org, ref).Scan(&id)
	return id, notFound(err)
}

// addIssueKey 把 key 寫進別名表(存大寫)。current = 這是 Issue 目前的 key:別名表裡同一個 key 已經指向別張 Issue 時
// 不能搶(回 ErrIssueKeyTaken;發號走 nextIssueKey 會先跳過這種號碼);指向同一張就算已經有。否則(舊的 key、舊格式 id)已經有就不動
func addIssueKey(c context.Context, tx pgx.Tx, org, key, id string, current bool) error {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		return nil
	}
	tag, err := tx.Exec(c, `insert into omixor_issue_keys (org, key, issue_id, created_at) values ($1, $2, $3, now())
		on conflict (org, key) do update set issue_id = excluded.issue_id where omixor_issue_keys.issue_id = excluded.issue_id`, org, key, id)
	if err != nil {
		return err
	}
	if current && tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrIssueKeyTaken, key)
	}
	return nil
}

// issueKeyPrefix 建立、搬移時的 key 前綴:組織層級 ISS;專案用它的代號(鎖住專案列,和改代號互斥)。
// 專案不存在回 ErrBadRef,沒有代號回 ErrProjectNoKey
func issueKeyPrefix(c context.Context, tx pgx.Tx, org, project string) (string, error) {
	if project == "" {
		return model.IssueKeyPrefix, nil
	}
	var key string
	err := tx.QueryRow(c, `select key from omixor_projects where org = $1 and id = $2 for share`, org, project).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: 專案 %s 不存在", ErrBadRef, project)
	}
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", ErrProjectNoKey
	}
	return key, nil
}

// lockIssues 同一個組織的 Issue 建立、搬移、匯入在這把交易層級的鎖上排隊(算 pos、發 key)
func lockIssues(c context.Context, tx pgx.Tx, org string) error {
	_, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-issues:' || $1))`, org)
	return err
}

func writeIssue(c context.Context, tx pgx.Tx, org string, i model.Issue, pos int64, insert bool) error {
	// created_by、assignee、deleted_by、留言作者都是 actor id(服務層已解析);空的 assignee、deleted_by 寫 NULL
	createdBy, assignee, deletedBy := i.CreatedBy, IDOrNil(i.Assignee), IDOrNil(i.DeletedBy)
	var err error
	var closed *time.Time
	if i.ClosedAt != nil && !i.ClosedAt.IsZero() {
		t := secs(i.ClosedAt.Time)
		closed = &t
	}
	labels := i.Labels
	if labels == nil {
		labels = []string{}
	}
	if insert {
		version := i.Version
		if version <= 0 {
			version = 1
		}
		if _, err := tx.Exec(c, `insert into omixor_issues (org, id, number, pos, title, body, status, priority, labels, source, link,
			created_by, created_at, updated_at, closed_at, assignee, repo, project, key, version, deleted_at, deleted_by)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
			org, i.ID, i.Number(), pos, i.Title, i.Body, i.Status, i.Priority, labels, i.Source, i.Link,
			createdBy, secs(i.CreatedAt.Time), secs(i.UpdatedAt.Time), closed, assignee, i.Repo, i.Project, i.Key, version,
			optSecs(i.DeletedAt), deletedBy); err != nil {
			return fkErr(err)
		}
		if err := addIssueKey(c, tx, org, i.Key, i.ID, true); err != nil {
			return err
		}
	} else {
		// id 可以是任何編號(見 issueRef);垃圾桶裡的不能改。i.Version > 0 時只有目前版本等於它才改(樂觀鎖),否則回 *VersionConflictError。
		// key、project、號碼不在這裡改(搬移用 MoveIssue)。這一列的鎖一直拿到交易結束,和 AddIssueComment 互斥
		cond, args := ``, []any{org, i.ID, i.Title, i.Body, i.Status, i.Priority, labels, i.Source, i.Link, createdBy,
			secs(i.CreatedAt.Time), secs(i.UpdatedAt.Time), closed, assignee, i.Repo}
		if i.Version > 0 {
			cond, args = ` and version = $16`, append(args, i.Version)
		}
		var id string
		err = tx.QueryRow(c, `update omixor_issues set title = $3, body = $4, status = $5, priority = $6, labels = $7, source = $8,
			link = $9, created_by = $10, created_at = $11, updated_at = $12, closed_at = $13, assignee = $14, repo = $15,
			version = version + case when (title, body, status, priority, labels, source, link, assignee, repo, closed_at)
				is distinct from ($3::text, $4::text, $5::text, $6::text, $7::text[], $8::text, $9::text, $14::text, $15::text, $13::timestamptz)
				then 1 else 0 end
			where org = $1 and id = `+issueRef(2)+` and deleted_at is null`+cond+` returning id`, args...).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return issueMissOrConflict(c, tx, org, i.ID)
		}
		if err != nil {
			return err
		}
		i.ID = id
		if i.Comments == nil { // nil = 留言不動(見 Store.UpdateIssue)
			return nil
		}
		if _, err := tx.Exec(c, `delete from omixor_issue_comments where org = $1 and issue_id = $2`, org, i.ID); err != nil {
			return err
		}
	}
	for k, cm := range i.Comments {
		if _, err := tx.Exec(c, `insert into omixor_issue_comments (org, issue_id, pos, id, author, text, at) values ($1,$2,$3,$4,$5,$6,$7)`,
			org, i.ID, k, cm.ID, cm.Author, cm.Text, secs(cm.At.Time)); err != nil {
			return fkErr(err)
		}
	}
	return nil
}

// issueMissOrConflict 條件式更新沒改到列:Issue 不存在或在垃圾桶 → ErrNotFound;否則是版本不符 → *VersionConflictError
func issueMissOrConflict(c context.Context, tx pgx.Tx, org, ref string) error {
	var v int
	var deleted bool
	err := tx.QueryRow(c, `select version, deleted_at is not null from omixor_issues where org = $1 and id = `+issueRef(2), org, ref).Scan(&v, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && deleted) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return &VersionConflictError{Current: int64(v)}
}

// CreateIssue 發新的 id(UUIDv7;i.ID 是 UUID 才沿用)與 key:i.Project 空 = 組織層級 ISS-<n>,否則 <專案代號>-<n>
// (專案不存在 ErrBadRef、沒有代號 ErrProjectNoKey)。發號、寫入與別名表在同一個交易
func (p *PG) CreateIssue(org string, i model.Issue) (model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return i, err
	}
	defer tx.Rollback(c)
	if err := lockIssues(c, tx, org); err != nil {
		return i, err
	}
	prefix, err := issueKeyPrefix(c, tx, org, i.Project)
	if err != nil {
		return i, err
	}
	// 號碼由序號表單句原子取得(刪掉的號碼不再使用);各範圍(組織、每個專案)各自一組
	key, num, err := nextIssueKey(c, tx, org, i.Project, prefix)
	if err != nil {
		return i, err
	}
	var pos int64
	if err := tx.QueryRow(c, `select coalesce(max(pos),0) from omixor_issues where org = $1`, org).Scan(&pos); err != nil {
		return i, err
	}
	if !model.IsUUID(i.ID) { // 只有給的是 UUID 才沿用(舊程式可能帶 ISS-<n> 當草稿 id)
		i.ID = model.NewIssueID(i.CreatedAt.Time)
	}
	i.ID = strings.ToLower(i.ID)
	i.Key, i.Num, i.Version, i.DeletedAt, i.DeletedBy = key, num, 1, nil, ""
	if err := writeIssue(c, tx, org, i, pos+1, true); err != nil {
		return i, err
	}
	return i, tx.Commit(c)
}

// UpdateIssue 見 Store.UpdateIssue
func (p *PG) UpdateIssue(org string, i model.Issue) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := writeIssue(c, tx, org, i, 0, false); err != nil {
		return err
	}
	return tx.Commit(c)
}

// MoveIssue 見 Store.MoveIssue
func (p *PG) MoveIssue(org, ref, project string, expected int, at model.Time) (*model.Issue, string, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(c)
	if err := lockIssues(c, tx, org); err != nil {
		return nil, "", err
	}
	var id, curProject, oldKey string
	var version int
	err = tx.QueryRow(c, `select id, project, key, version from omixor_issues where org = $1 and id = `+issueRef(2)+` and deleted_at is null for update`,
		org, ref).Scan(&id, &curProject, &oldKey, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if expected > 0 && version != expected {
		return nil, "", &VersionConflictError{Current: int64(version)}
	}
	if curProject != project {
		prefix, err := issueKeyPrefix(c, tx, org, project)
		if err != nil {
			return nil, "", err
		}
		key, num, err := nextIssueKey(c, tx, org, project, prefix)
		if err != nil {
			return nil, "", err
		}
		if _, err := tx.Exec(c, `update omixor_issues set project = $3, key = $4, number = $5, version = version + 1, updated_at = $6
			where org = $1 and id = $2`, org, id, project, key, num, secs(at.Time)); err != nil {
			return nil, "", fkErr(err)
		}
		// 舊的 key 本來就在別名表(建立、遷移時寫的);再補一次以防萬一,不覆蓋
		if err := addIssueKey(c, tx, org, oldKey, id, false); err != nil {
			return nil, "", err
		}
		if err := addIssueKey(c, tx, org, key, id, true); err != nil {
			return nil, "", err
		}
	}
	l, err := listIssuesRef(c, tx, org, id, liveIssues)
	if err != nil {
		return nil, "", err
	}
	if len(l) == 0 {
		return nil, "", ErrNotFound
	}
	if err := tx.Commit(c); err != nil {
		return nil, "", err
	}
	return &l[0], oldKey, nil
}

// TrashIssue 見 Store.TrashIssue
func (p *PG) TrashIssue(org, ref, by string, at model.Time) (*model.Issue, error) {
	return p.setIssueDeleted(org, ref, true, by, at)
}

// RestoreIssue 見 Store.RestoreIssue
func (p *PG) RestoreIssue(org, ref string, at model.Time) (*model.Issue, error) {
	return p.setIssueDeleted(org, ref, false, "", at)
}

func (p *PG) setIssueDeleted(org, ref string, deleted bool, by string, at model.Time) (*model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	var id string
	if deleted {
		err = tx.QueryRow(c, `update omixor_issues set deleted_at = $3, deleted_by = $4, version = version + 1
			where org = $1 and id = `+issueRef(2)+` and deleted_at is null returning id`, org, ref, secs(at.Time), IDOrNil(by)).Scan(&id)
	} else {
		err = tx.QueryRow(c, `update omixor_issues set deleted_at = null, deleted_by = null, version = version + 1
			where org = $1 and id = `+issueRef(2)+` and deleted_at is not null returning id`, org, ref).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l, err := listIssuesRef(c, tx, org, id, anyIssues)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(c); err != nil {
		return nil, err
	}
	return &l[0], nil
}

// AddIssueComment 單筆 insert:先更新 Issue 的 updated_at(同時鎖住這一列,同一個 Issue 的留言在這裡排隊),
// 再取 pos = max + 1 插入;READ COMMITTED 下每個語句看得到前一筆已提交的留言,所以 pos 不會撞。
// 不動 version(留言只附加,不算衝突);垃圾桶裡的 Issue 回 ErrNotFound
func (p *PG) AddIssueComment(org, issueID string, cm model.Comment) (*model.Issue, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	var id string
	err = tx.QueryRow(c, `update omixor_issues set updated_at = $3 where org = $1 and id = `+issueRef(2)+` and deleted_at is null returning id`,
		org, issueID, secs(cm.At.Time)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(c, `insert into omixor_issue_comments (org, issue_id, pos, id, author, text, at)
		select $1, $2, coalesce(max(pos), -1) + 1, $3, $4, $5, $6 from omixor_issue_comments where org = $1 and issue_id = $2`,
		org, id, cm.ID, cm.Author, cm.Text, secs(cm.At.Time)); err != nil {
		return nil, fkErr(err)
	}
	l, err := listIssuesRef(c, tx, org, id, liveIssues)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(c); err != nil {
		return nil, err
	}
	return &l[0], nil
}

// DeleteIssue 永久刪除(含垃圾桶裡的;留言、別名跟著刪)。一般的刪除是 TrashIssue
func (p *PG) DeleteIssue(org, id string) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	iid, err := resolveIssue(c, tx, org, id)
	if err != nil {
		return err
	}
	// 頻道綁的 Issue 被刪:顯式把頻道的 issue_id 清成 NULL(PG 14 以下不支援 on delete set null (issue_id) 欄位清單,
	// 外鍵那條只能退成不帶這個子句;這裡與版本無關,而且和刪除在同一個交易)
	if _, err := tx.Exec(c, `update omixor_channels set issue_id = null where org = $1 and issue_id = $2`, org, iid); err != nil {
		return err
	}
	// key 退役:這張 Issue 目前與過去的 key(別名隨刪除 cascade 掉)記進退役表,發號不再用。任務連結、文字裡的 <代號>-<n>
	// 刪除後還留著;同一個範圍的序號本來就不回收,但專案刪掉後別的專案(或新專案)再用同一個代號時,序號是新的一組,
	// 沒有這一步會重發同一個 key,舊參照就指到不相干的新 Issue
	if _, err := tx.Exec(c, `insert into omixor_issue_keys_retired (org, key, reason, created_at)
		select org, key, 'deleted', now() from omixor_issue_keys where org = $1 and issue_id = $2
		on conflict (org, key) do nothing`, org, iid); err != nil {
		return err
	}
	tag, err := tx.Exec(c, `delete from omixor_issues where org = $1 and id = $2`, org, iid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// 子項隨 Issue cascade 刪掉了;它們的序號列(item:<issue id>)也清掉,免得序號表留垃圾
	if _, err := tx.Exec(c, `delete from omixor_seq where org = $1 and scope = $2`, org, "item:"+iid); err != nil {
		return err
	}
	return tx.Commit(c)
}

// ---- 匯入 ----

// ImportError 匯入內容不能用:Code 400 = 資料本身有問題(key 格式、專案不存在…),409 = 和組織裡現有的 key 或 Issue 衝突。
// 整批都在同一個交易,出錯整批不寫入。Msg 是給使用者看的(指出是哪一筆)
type ImportError struct {
	Code int
	Msg  string
}

func (e *ImportError) Error() string { return e.Msg }

func importBad(format string, a ...any) error {
	return &ImportError{Code: 400, Msg: fmt.Sprintf(format, a...)}
}

func importTaken(format string, a ...any) error {
	return &ImportError{Code: 409, Msg: fmt.Sprintf(format, a...)}
}

// importLabel 錯誤訊息裡指這一筆的寫法:key、id,再不然標題
func importLabel(i model.Issue) string {
	for _, s := range []string{i.Key, i.ID, i.Title} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return "(沒有 key、id 與標題)"
}

// importPlan 匯入的一筆:整理好的 Issue 與要補進別名表的舊編號
type importPlan struct {
	issue   model.Issue
	aliases []string
}

// prepareImport 匯入前整理與驗證(在交易裡;出錯整批不寫入):
//   - id:UUID 保留(轉小寫);不是 UUID(舊格式 ISS-<n>、gh-1…)或沒給 → 發新的 UUIDv7,舊 id 補進別名表。
//   - key:有給的檢查格式(組織層級 ISS-<n>;專案 <專案目前的代號>-<n>)、專案要存在,不能和批次裡別筆、組織裡現有的 key 或別名重複(409),
//     號碼取自 key,對應範圍(組織層級、各專案)的序號推進到匯入的最大號碼;沒給 key 但 id 是組織層級的舊格式 ISS-<n> → 當作 key;
//     其他沒有 key 的在推進序號之後,依 Project 發新的(專案沒有代號 400)。
//   - 垃圾桶狀態(DeletedAt、DeletedBy)與 Version(>0 保留,否則 1)照原樣寫入。
func prepareImport(c context.Context, tx pgx.Tx, org string, list []model.Issue) ([]importPlan, error) {
	out := make([]importPlan, 0, len(list))
	prefixes := map[string]string{} // 專案 id → 代號(組織層級 "" → ISS)
	prefixOf := func(i model.Issue) (string, error) {
		if v, ok := prefixes[i.Project]; ok {
			return v, nil
		}
		v, err := issueKeyPrefix(c, tx, org, i.Project)
		switch {
		case errors.Is(err, ErrBadRef):
			return "", importBad("%s:專案 %s 不存在", importLabel(i), i.Project)
		case errors.Is(err, ErrProjectNoKey):
			return "", importBad("%s:專案 %s 還沒設定代號,不能放 Issue(先設代號,或不指派專案)", importLabel(i), i.Project)
		case err != nil:
			return "", err
		}
		prefixes[i.Project] = v
		return v, nil
	}
	top := map[string]int{}
	var later []int
	taken := map[string]int{} // 批次裡已經用掉的 key(大寫)→ 第幾筆
	var keys, aliases []string
	for k, i := range list {
		i.Project = strings.TrimSpace(i.Project)
		pl := importPlan{issue: i}
		legacy := strings.TrimSpace(i.ID)
		if !model.IsUUID(legacy) {
			pl.issue.ID = model.NewIssueID(i.CreatedAt.Time)
			if legacy != "" {
				pl.aliases = append(pl.aliases, strings.ToUpper(legacy))
			}
		} else {
			pl.issue.ID = strings.ToLower(legacy)
		}
		key := strings.ToUpper(strings.TrimSpace(i.Key))
		if key == "" && i.Project == "" && model.IssueKeyPrefixOf(legacy) == model.IssueKeyPrefix {
			key = strings.ToUpper(legacy)
		}
		want, err := prefixOf(i) // 專案不存在、沒有代號在這裡就擋下
		if err != nil {
			return nil, err
		}
		pl.issue.Key, pl.issue.Num = "", 0
		if key != "" {
			n := model.IssueKeyNumber(key)
			if n == 0 || model.IssueKeyPrefixOf(key) == "" {
				return nil, importBad("%s:key 格式不對(要是 %s-<號碼>)", importLabel(i), want)
			}
			if model.IssueKeyPrefixOf(key) != want {
				where := "組織層級的 Issue"
				if i.Project != "" {
					where = "專案 " + i.Project
				}
				return nil, importBad("%s:key 的前綴要是 %s(%s)", importLabel(i), want, where)
			}
			if key != model.IssueKey(want, n) { // 前導零(ISS-05)、號碼超出範圍等:一律要標準寫法,不然和 ISS-5 會撞號碼卻不撞 key
				return nil, importBad("%s:key 要用標準寫法 %s(號碼不能有前導零)", importLabel(i), model.IssueKey(want, n))
			}
			if j, dup := taken[key]; dup {
				return nil, importTaken("key %s 在匯入的資料裡重複(第 %d、%d 筆)", key, j+1, k+1)
			}
			taken[key] = k
			pl.issue.Key, pl.issue.Num = key, n
			sc := model.IssueScope(i.Project)
			top[sc] = max(top[sc], n)
			keys = append(keys, key)
		} else {
			later = append(later, len(out))
		}
		for _, a := range pl.aliases {
			if a != key {
				aliases = append(aliases, a)
			}
		}
		out = append(out, pl)
	}
	// 舊 id 的別名不能是批次裡別筆的 key
	for _, pl := range out {
		for _, a := range pl.aliases {
			if j, ok := taken[a]; ok && a != pl.issue.Key {
				return nil, importTaken("舊編號 %s 和第 %d 筆的 key 相同", a, j+1)
			}
		}
	}
	// 組織裡已經有的 key、別名(別名表裡也有舊 key 與舊 id):匯入的 key 不能撞;匯入的舊 id 若已經指向別的 Issue 也不行
	for _, chk := range [][]string{keys, aliases} {
		if len(chk) == 0 {
			continue
		}
		var hit string
		err := tx.QueryRow(c, `select key from (
			select key from omixor_issue_keys where org = $1 and key = any($2)
			union all select upper(key) from omixor_issues where org = $1 and key <> '' and upper(key) = any($2)) x limit 1`, org, chk).Scan(&hit)
		if err == nil {
			return nil, importTaken("key %s 已經是組織裡另一個 Issue 的編號", hit)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	for sc, n := range top { // 帶著指定號碼匯入:序號表跟上
		if err := seqBump(c, tx, org, sc, n); err != nil {
			return nil, err
		}
	}
	for _, k := range later {
		i := &out[k].issue
		prefix, err := prefixOf(*i)
		if err != nil {
			return nil, err
		}
		key, n, err := nextIssueKey(c, tx, org, i.Project, prefix)
		if err != nil {
			return nil, err
		}
		i.Key, i.Num = key, n
	}
	return out, nil
}

func writeImported(c context.Context, tx pgx.Tx, org string, pl importPlan, pos int64) error {
	if err := writeIssue(c, tx, org, pl.issue, pos, true); err != nil {
		if isUniqueViolation(err) { // 號碼或 key 撞到組織裡現有的(例如 ISS-5 與 ISS-05 這類寫法不同的重複)
			return importTaken("號碼或 key(%s)已經是組織裡另一個 Issue 的", pl.issue.Key)
		}
		return err
	}
	for _, a := range pl.aliases {
		if err := addIssueKey(c, tx, org, a, pl.issue.ID, false); err != nil {
			return err
		}
	}
	return nil
}

// ImportIssues 第一次搬資料:組織裡一筆 Issue 都沒有(含垃圾桶)才收;驗證與 id、key 規則見 prepareImport
func (p *PG) ImportIssues(org string, list []model.Issue) (int, error) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	if err := lockIssues(c, tx, org); err != nil {
		return 0, err
	}
	// 先鎖住序號列(和其他匯入、MergeIssues 一致):和並發的 CreateIssue 在這一列上排隊
	if err := seqBump(c, tx, org, model.IssueScopeOrg, 0); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(c, `select count(*) from omixor_issues where org = $1`, org).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, importTaken("組織 %s 已經有 %d 筆 Issue", org, n)
	}
	plans, err := prepareImport(c, tx, org, list)
	if err != nil {
		return 0, err
	}
	for k, pl := range plans {
		if err := writeImported(c, tx, org, pl, int64(len(plans)-k)); err != nil {
			return 0, fmt.Errorf("%s:%w", importLabel(list[k]), err)
		}
	}
	return len(plans), tx.Commit(c)
}

// MergeIssues 可以重跑的匯入。每一筆先找組織裡有沒有同一個 Issue:id(UUID、key、別名)解析得到;
// 沒給 id 或 id 是舊格式、但 key 對得到 → 就是那一筆。id 是 UUID 卻對不到、而 key 屬於別的 Issue(或 id 和 key 各指向不同 Issue)→ 409 整批不寫入。
// 已經有的:overwrite 時整筆取代內容(key、專案、號碼、垃圾桶狀態不動;在垃圾桶裡的略過),否則略過。其餘的當新的匯入(prepareImport)
func (p *PG) MergeIssues(org string, list []model.Issue, overwrite bool) (MergeCounts, error) {
	var res MergeCounts
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(c)
	if err := lockIssues(c, tx, org); err != nil {
		return res, err
	}
	if err := seqBump(c, tx, org, model.IssueScopeOrg, 0); err != nil {
		return res, err
	}
	var minPos int64
	var cnt int
	if err := tx.QueryRow(c, `select coalesce(min(pos), 0), count(*) from omixor_issues where org = $1`, org).Scan(&minPos, &cnt); err != nil {
		return res, err
	}
	if cnt == 0 {
		minPos = int64(len(list)) + 1
	}
	var fresh []model.Issue
	seen := map[string]bool{}
	for _, i := range list {
		id, err := resolveIssue(c, tx, org, i.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return res, err
		}
		keyID := ""
		if strings.TrimSpace(i.Key) != "" {
			keyID, err = resolveIssue(c, tx, org, i.Key)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return res, err
			}
		}
		if id == "" && keyID != "" && !model.IsUUID(strings.TrimSpace(i.ID)) {
			id = keyID // 沒有自己的 id(沒給或舊格式):key 指到誰就是誰
		}
		if keyID != "" && keyID != id {
			return res, importTaken("%s:key %s 已經是組織裡另一個 Issue 的編號", importLabel(i), strings.ToUpper(strings.TrimSpace(i.Key)))
		}
		if id == "" {
			fresh = append(fresh, i)
			continue
		}
		if !overwrite || seen[id] {
			res.Skipped++
			continue
		}
		seen[id] = true
		i.ID, i.Version = id, 0
		if i.Comments == nil {
			i.Comments = []model.Comment{}
		}
		if err := writeIssue(c, tx, org, i, 0, false); err != nil {
			if errors.Is(err, ErrNotFound) { // 在垃圾桶裡:不覆蓋
				res.Skipped++
				continue
			}
			return res, fmt.Errorf("%s:%w", importLabel(i), err)
		}
		res.Updated++
	}
	plans, err := prepareImport(c, tx, org, fresh)
	if err != nil {
		return res, err
	}
	for k, pl := range plans {
		if err := writeImported(c, tx, org, pl, minPos-int64(k)-1); err != nil {
			return res, fmt.Errorf("%s:%w", importLabel(fresh[k]), err)
		}
		res.Inserted++
	}
	return res, tx.Commit(c)
}

func (p *PG) IssueOrgs() ([]string, error) {
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select distinct org from omixor_issues where org not like 'deleted-orgs/%' order by org`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (p *PG) PutAttachment(org, id string, data []byte) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `insert into omixor_attachments (org, id, data) values ($1, $2, $3) on conflict (org, id) do nothing`, org, id, data)
	return err
}

func (p *PG) GetAttachment(org, id string) ([]byte, error) {
	c, cancel := ctx()
	defer cancel()
	var b []byte
	err := p.pool.QueryRow(c, `select data from omixor_attachments where org = $1 and id = $2`, org, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// isUniqueViolation 唯一約束衝突(23505)
func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

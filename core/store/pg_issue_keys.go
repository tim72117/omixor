package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 一次性遷移:Issue 的內部 id 換成 UUIDv7、原本的 ISS-<n> 變成 key(規格:雲端 omixor DOC-10 第 1 階段) ----
//
// 對象:key 還是空字串的 Issue(舊資料;新建的一律有 key)。所以可以重跑:做過的列不會再動,做到一半(交易失敗、逾時)整個還原。
// 每一列(依 created_at、號碼排序):
//  1. 新 id:原本就是 UUID 的保留,否則用 created_at 的時間產生 UUIDv7(排序和建立順序一致)。
//  2. key:原 id 是 ISS-<n>(號碼 > 0)且還沒被別的 Issue 或別名用掉 → key = ISS-<n>、號碼不變;
//     其他舊格式 id(gh-1 這類,號碼是 0)發新的 ISS-<n>(scope issue),原 id 留在別名表照樣查得到。project 一律是 ''(組織層級)。
//  3. 別名表:key 與原 id 都寫進 omixor_issue_keys。
//  4. 參照一起改:ops_issue_comments、omixor_channels 的外鍵有 on update cascade(頻道那條如果還沒加上、或大小寫不同,
//     這裡再顯式補改一次,不分大小寫);任務的相關連結 omixor_agent_task_links.url 等於原 id(不分大小寫)的改成新 id。
//     其他文字裡的 ISS-<n>(動態牆、留言、事件的後續工作、任務說明)不改:它們是 key,透過別名表查得到。
//
// 全部是集合式:id 與 key 在記憶體裡算好,放進暫存對照表(org、舊 id、新 id、key、號碼),
// 然後 ops_issues、頻道、任務連結、別名表各用「單句 update/insert … from 對照表」一次改完
// (舊版逐筆改,每一筆都全表掃任務連結,2 萬張 Issue 約 4 分鐘,時間是平方級)。整段在同一個交易裡:逾時或失敗整個還原,
// 下次啟動再跑一次(key 還是空字串的才會處理)。做完輸出孤兒數(頻道、任務連結指向找不到的 Issue)。
//
// 舊的 (org, number) 唯一索引 ops_issues_org_number 不在這裡拿掉:migrateSeq 的 ensureSeqIndex 依欄位比對,
// 先建好 (org, project, number) 的新索引再換掉舊的(不依賴這個遷移成不成功)。

func (p *PG) migrateIssueKeys() error {
	c, cancel := p.migCtx(10 * time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := xactLock(c, tx, "omixor-issue-keys-migrate"); err != nil {
		if errors.Is(err, errLockWait) || isTimeout(c, err) {
			warnf("Issue 編號遷移:%v,略過這次(下次啟動再做)", err)
			return nil
		}
		return err
	}
	if _, err := tx.Exec(c, pgSeqSchema); err != nil {
		return err
	}
	t0 := time.Now()
	n, err := migrateIssueKeyRows(c, tx)
	if err != nil {
		return err
	}
	var orphanCh, orphanLinks int
	if n > 0 {
		if orphanCh, orphanLinks, err = issueRefOrphans(c, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(c); err != nil {
		return err
	}
	if n > 0 {
		log.Printf("[遷移] Issue 編號:%d 筆換成 UUID 內部 id、原編號變成 key(%s);孤兒:頻道 %d、任務連結 %d(找不到 Issue,沒有動)",
			n, time.Since(t0).Round(time.Millisecond), orphanCh, orphanLinks)
	}
	return nil
}

// issueRefOrphans 頻道綁的 Issue、任務連結(長得像 Issue 編號的:UUID 或 <字母數字>-<號碼>)找不到 Issue(id 或別名)的筆數
func issueRefOrphans(c context.Context, tx interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (channels, links int, err error) {
	if err = tx.QueryRow(c, `select count(*) from omixor_channels ch where ch.issue_id is not null and ch.issue_id <> ''
		and not exists (select 1 from ops_issues x where x.org = ch.org and x.id = ch.issue_id)`).Scan(&channels); err != nil {
		return
	}
	var has bool
	if err = tx.QueryRow(c, `select to_regclass('omixor_agent_task_links') is not null`).Scan(&has); err != nil || !has {
		return
	}
	err = tx.QueryRow(c, `select count(*) from omixor_agent_task_links l
		where (l.url ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' or l.url ~* '^[a-z0-9]+-[0-9]+$')
		and not exists (select 1 from ops_issues x where x.org = l.org and x.id = lower(l.url))
		and not exists (select 1 from omixor_issue_keys k where k.org = l.org and k.key = upper(l.url))`).Scan(&links)
	return
}

type legacyIssue struct {
	org, id string
	number  int
	created time.Time
	// 算出來的
	newID, key string
	num        int
}

// migrateIssueKeyRows 見檔頭;回傳處理的筆數
func migrateIssueKeyRows(c context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(c, `select org, id, number, created_at from ops_issues where key = '' order by org, created_at, number, id`)
	if err != nil {
		return 0, err
	}
	var list []*legacyIssue
	for rows.Next() {
		x := &legacyIssue{}
		if err := rows.Scan(&x.org, &x.id, &x.number, &x.created); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, nil
	}
	byOrg := map[string][]*legacyIssue{}
	var orgs []string
	for _, x := range list {
		if _, ok := byOrg[x.org]; !ok {
			orgs = append(orgs, x.org)
		}
		byOrg[x.org] = append(byOrg[x.org], x)
	}
	for _, org := range orgs {
		if err := assignIssueKeys(c, tx, org, byOrg[org]); err != nil {
			return 0, err
		}
	}
	if err := applyIssueKeys(c, tx, list); err != nil {
		return 0, err
	}
	return len(list), nil
}

// assignIssueKeys 算每一筆的新 id、key、號碼(不寫資料庫,只動序號表):
// 先處理原 id 是 ISS-<n> 的(保留原號碼,除非 key 已經被別的 Issue 或別名用掉),再依建立順序發新號碼(跳過已被占用的)
func assignIssueKeys(c context.Context, tx pgx.Tx, org string, list []*legacyIssue) error {
	used := map[string]bool{} // 組織裡已經被占用的 key(大寫):現有 Issue 的 key 與別名表
	for _, q := range []string{`select upper(key) from ops_issues where org = $1 and key <> ''`, `select key from omixor_issue_keys where org = $1`} {
		rows, err := tx.Query(c, q, org)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			used[k] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	var renumber []*legacyIssue
	for _, x := range list {
		x.newID = strings.ToLower(x.id)
		if !model.IsUUID(x.id) {
			x.newID = model.NewIssueID(x.created)
		}
		if x.number > 0 && strings.EqualFold(x.id, model.IssueKey(model.IssueKeyPrefix, x.number)) {
			if k := model.IssueKey(model.IssueKeyPrefix, x.number); !used[k] {
				x.key, x.num = k, x.number
				used[k] = true
				continue
			}
		}
		renumber = append(renumber, x)
	}
	if len(renumber) == 0 {
		return nil
	}
	// 號碼從序號表現在的下一號起算(它會看現有資料最大號碼,不會撞還沒處理完的 ISS-<n>),用完再把序號表記到最後用的號碼
	n, err := seqNext(c, tx, org, model.IssueScopeOrg, 0)
	if err != nil {
		return err
	}
	last := n - 1
	for _, x := range renumber {
		for used[model.IssueKey(model.IssueKeyPrefix, n)] {
			n++
		}
		x.key, x.num = model.IssueKey(model.IssueKeyPrefix, n), n
		used[x.key] = true
		last = n
		n++
	}
	return seqBump(c, tx, org, model.IssueScopeOrg, last)
}

// applyIssueKeys 把算好的結果集合式寫回:暫存對照表 + 每個表單句 update … from
func applyIssueKeys(c context.Context, tx pgx.Tx, list []*legacyIssue) error {
	if _, err := tx.Exec(c, `create temp table _issue_key_map (org text not null, old_id text not null, new_id text not null, key text not null, num integer not null, legacy boolean not null) on commit drop`); err != nil {
		return err
	}
	rows := make([][]any, 0, len(list))
	for _, x := range list {
		rows = append(rows, []any{x.org, x.id, x.newID, x.key, x.num, !model.IsUUID(x.id) && !strings.EqualFold(x.id, x.key)})
	}
	if _, err := tx.CopyFrom(c, pgx.Identifier{"_issue_key_map"}, []string{"org", "old_id", "new_id", "key", "num", "legacy"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `create unique index on _issue_key_map (org, old_id)`); err != nil {
		return err
	}
	// 不分大小寫比對用:同一個 upper(舊 id) 有多筆時取字典序第一筆(確定性)
	if _, err := tx.Exec(c, `create temp table _issue_key_ci on commit drop as
		select distinct on (org, upper(old_id)) org, upper(old_id) as uid, new_id from _issue_key_map where new_id <> old_id order by org, upper(old_id), old_id`); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `create unique index on _issue_key_ci (org, uid)`); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `analyze _issue_key_map`); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `analyze _issue_key_ci`); err != nil {
		return err
	}
	// 本體(留言、有外鍵的頻道靠 on update cascade 跟著改)
	if _, err := tx.Exec(c, `update ops_issues x set id = m.new_id, key = m.key, number = m.num, project = ''
		from _issue_key_map m where x.org = m.org and x.id = m.old_id`); err != nil {
		return err
	}
	// 頻道:外鍵還沒加上、或大小寫不同(cascade 只認完全相同)的補改
	if _, err := tx.Exec(c, `update omixor_channels ch set issue_id = m.new_id from _issue_key_ci m
		where ch.org = m.org and ch.issue_id is not null and upper(ch.issue_id) = m.uid and ch.issue_id <> m.new_id`); err != nil {
		return err
	}
	var links bool
	if err := tx.QueryRow(c, `select to_regclass('omixor_agent_task_links') is not null`).Scan(&links); err != nil {
		return err
	}
	if links {
		if _, err := tx.Exec(c, `update omixor_agent_task_links l set url = m.new_id from _issue_key_ci m
			where l.org = m.org and upper(l.url) = m.uid and l.url <> m.new_id`); err != nil {
			return err
		}
	}
	// 別名表:目前的 key 一律寫;舊格式的原 id 補一筆(已經有就不動)。key 在 assignIssueKeys 已經避開別人的別名,所以這裡不會衝突
	tag, err := tx.Exec(c, `insert into omixor_issue_keys (org, key, issue_id, created_at)
		select org, upper(key), new_id, now() from _issue_key_map
		on conflict (org, key) do update set issue_id = excluded.issue_id where omixor_issue_keys.issue_id = excluded.issue_id`)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != len(list) {
		return fmt.Errorf("%w:別名表寫入 %d 筆,預期 %d 筆", ErrIssueKeyTaken, tag.RowsAffected(), len(list))
	}
	_, err = tx.Exec(c, `insert into omixor_issue_keys (org, key, issue_id, created_at)
		select org, upper(old_id), new_id, now() from _issue_key_map where legacy on conflict (org, key) do nothing`)
	return err
}

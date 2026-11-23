package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"omixor-core/model"
)

// ---- 事件與角色任務拆表(PG) ----
//
// 原本 omixor_incidents.data(整個事件一個 jsonb)、omixor_agent_tasks.notes / links(jsonb)拆成可查詢的欄位與有外鍵的子表:
//
//	omixor_incidents                  純量欄位 title、sev、commander、resolved_at、impact、ai_summary、created_by(加上原本的 status、started_at、number、pos)
//	omixor_incident_services          (org, incident_id, pos, name)
//	omixor_incident_follow_ups        (org, incident_id, pos, ref)
//	omixor_incident_alerts            (org, incident_id, pos, alert_id)
//	omixor_incident_events            時間軸:(org, incident_id, pos, id, at, actor, kind, text);actor 是 actor id;事件沒有巢狀內容,一則一列
//	omixor_agent_task_notes           (org, task_id, pos, id, author, text, at)
//	omixor_agent_task_links           (org, task_id, pos, url)
//
// 子表的外鍵 (org, 父 id) 指向父表的主鍵,on delete cascade on update cascade;順序用 pos(從 0 起)。
// 備註 / 時間軸的主鍵是 (org, 父 id, pos) 而不是 id:舊資料(匯入、手動)的 id 可能空白或重複,拿 id 當主鍵會讓遷移與寫入失敗;
// id 另有索引。
//
// 讀取一律從這些欄位與子表組出 model.Incident / model.AgentTask;寫入在同一個交易裡更新父列與子表,並把父列的 split_at 設成 now()。
// 追加備註、追加時間軸只插入新的那幾列(和資料庫裡現有的比對,現有的是新清單的前綴就只插入尾巴;否則整份換掉)。
//
// 舊欄位 omixor_incidents.data、omixor_agent_tasks.notes、omixor_agent_tasks.links 與 split_at 不再讀、不再寫(data 預設 '{}',notes、links 預設 '[]'),下一版 drop:
//
//	alter table omixor_incidents drop column data, drop column split_at;
//	alter table omixor_agent_tasks drop column notes, drop column links, drop column split_at;

// dbq 連線池或交易(查詢、執行都能做)
type dbq interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ---- 共用 ----

// syncSlice 把資料庫裡現有的 old 換成 cur:old 是 cur 的前綴時只插入尾巴(追加的高頻情況),否則刪光重插
func syncSlice[T any](c context.Context, q dbq, old, cur []T, eq func(a, b T) bool, deleteSQL string, args []any, ins func(start int, items []T) error) error {
	n := len(old)
	if len(cur) >= n {
		prefix := true
		for i := range old {
			if !eq(old[i], cur[i]) {
				prefix = false
				break
			}
		}
		if prefix {
			return ins(n, cur[n:])
		}
	}
	if n > 0 {
		if _, err := q.Exec(c, deleteSQL, args...); err != nil {
			return err
		}
	}
	return ins(0, cur)
}

func timeEq(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	return secs(a).Equal(secs(b))
}

func posRange(start, n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(start + i)
	}
	return out
}

// ---- 任務的備註與連結 ----

func insertTaskNotes(c context.Context, q dbq, org, taskID string, start int, notes []model.AgentTaskNote) error {
	if len(notes) == 0 {
		return nil
	}
	ids, texts := make([]string, len(notes)), make([]string, len(notes))
	authors := make([]*string, len(notes))
	ats := make([]*time.Time, len(notes))
	for i, n := range notes {
		ids[i], texts[i], ats[i] = validUTF8(n.ID), validUTF8(n.Text), nullSecs(n.At)
		authors[i] = IDPtr(n.Author)
	}
	_, err := q.Exec(c, `insert into omixor_agent_task_notes (org, task_id, pos, id, author, text, at)
		select $1, $2, pos, id, author, text, at from unnest($3::int[], $4::text[], $5::text[], $6::text[], $7::timestamptz[]) as t(pos, id, author, text, at)`,
		org, taskID, posRange(start, len(notes)), ids, authors, texts, ats)
	return err
}

func insertTaskLinks(c context.Context, q dbq, org, taskID string, start int, links []string) error {
	if len(links) == 0 {
		return nil
	}
	clean := make([]string, len(links))
	for i, l := range links {
		clean[i] = validUTF8(l)
	}
	_, err := q.Exec(c, `insert into omixor_agent_task_links (org, task_id, pos, url)
		select $1, $2, pos, url from unnest($3::int[], $4::text[]) as t(pos, url)`,
		org, taskID, posRange(start, len(links)), clean)
	return err
}

// loadTaskChildren 載入備註與連結(taskID 空白 = 整個組織;各一次查詢,不是每個任務一次)
func loadTaskChildren(c context.Context, q dbq, org, taskID string) (map[string][]model.AgentTaskNote, map[string][]string, error) {
	notes := map[string][]model.AgentTaskNote{}
	links := map[string][]string{}
	rows, err := q.Query(c, `select task_id, id, author, text, at from omixor_agent_task_notes
		where org = $1 and ($2::text = '' or task_id = $2) order by task_id, pos`, org, taskID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var tid string
		var n model.AgentTaskNote
		var at *time.Time
		var author NullID
		if err := rows.Scan(&tid, &n.ID, &author, &n.Text, &at); err != nil {
			rows.Close()
			return nil, nil, err
		}
		n.Author = author.String()
		n.At = fromNull(at)
		notes[tid] = append(notes[tid], n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = q.Query(c, `select task_id, url from omixor_agent_task_links
		where org = $1 and ($2::text = '' or task_id = $2) order by task_id, pos`, org, taskID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, u string
		if err := rows.Scan(&tid, &u); err != nil {
			return nil, nil, err
		}
		links[tid] = append(links[tid], u)
	}
	return notes, links, rows.Err()
}

// syncTaskChildren UpdateAgentTask / ClaimAgentTask:父列已更新(行鎖在手)後,讓子表等於 t 的備註與連結
func syncTaskChildren(c context.Context, q dbq, org string, t model.AgentTask) error {
	oldNotes, oldLinks, err := loadTaskChildren(c, q, org, t.ID)
	if err != nil {
		return err
	}
	if err := syncSlice(c, q, oldNotes[t.ID], t.Notes,
		func(a, b model.AgentTaskNote) bool {
			return a.ID == b.ID && a.Author == b.Author && a.Text == b.Text && timeEq(a.At.Time, b.At.Time)
		},
		`delete from omixor_agent_task_notes where org = $1 and task_id = $2`, []any{org, t.ID},
		func(start int, items []model.AgentTaskNote) error {
			return insertTaskNotes(c, q, org, t.ID, start, items)
		}); err != nil {
		return err
	}
	return syncSlice(c, q, oldLinks[t.ID], t.Links,
		func(a, b string) bool { return a == b },
		`delete from omixor_agent_task_links where org = $1 and task_id = $2`, []any{org, t.ID},
		func(start int, items []string) error { return insertTaskLinks(c, q, org, t.ID, start, items) })
}

// insertTask 新任務(CreateAgentTask、ImportAgents):父列(split_at = now())加子表
func insertTask(c context.Context, q dbq, org string, num int, t model.AgentTask) error {
	_, err := q.Exec(c, `insert into omixor_agent_tasks (org, id, number, agent_id, title, instructions, priority, status, result,
		routine_id, created_by, created_at, updated_at, claimed_at, completed_at, locked_until, claimed_by, split_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17, now())`,
		org, t.ID, num, t.AgentID, t.Title, t.Instructions, t.Priority, t.Status, t.Result,
		t.RoutineID, IDOrNil(t.CreatedBy), secs(t.CreatedAt.Time), secs(t.UpdatedAt.Time), optSecs(t.ClaimedAt), optSecs(t.CompletedAt), optSecs(t.LockedUntil), IDOrNil(t.ClaimedBy))
	if err != nil {
		return err
	}
	if err := insertTaskLinks(c, q, org, t.ID, 0, t.Links); err != nil {
		return err
	}
	return insertTaskNotes(c, q, org, t.ID, 0, t.Notes)
}

// readTasks 讀 org 的任務(id 空白 = 全部,號碼大的在前);可重複讀的唯讀交易,父表與子表看到同一個時間點。
// 備註、連結各用一次查詢批次載入(不是每個任務各查一次)
func (p *PG) readTasks(org, id string) ([]model.AgentTask, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.BeginTx(c, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select `+taskCols+` from omixor_agent_tasks where org = $1 and ($2::text = '' or id = $2) order by number desc`, org, id)
	if err != nil {
		return nil, err
	}
	out := []model.AgentTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	notes, links, err := loadTaskChildren(c, tx, org, id)
	if err != nil {
		return nil, err
	}
	for n := range out {
		if v := notes[out[n].ID]; v != nil {
			out[n].Notes = v
		}
		if v := links[out[n].ID]; v != nil {
			out[n].Links = v
		}
	}
	return out, nil
}

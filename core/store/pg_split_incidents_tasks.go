package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
//	omixor_incident_events            時間軸:(org, incident_id, pos, id, at, actor, kind, text);actor 是 actor id(舊結構的 actor_kind + actor_id 由 actor 欄位引擎合併,見 pg_actor_columns.go);事件沒有巢狀內容,一則一列
//	omixor_agent_task_notes           (org, task_id, pos, id, author, text, at)
//	omixor_agent_task_links           (org, task_id, pos, url)
//
// 子表的外鍵 (org, 父 id) 指向父表的主鍵,on delete cascade on update cascade;順序用 pos(從 0 起)。
// 備註 / 時間軸的主鍵是 (org, 父 id, pos) 而不是 id:舊資料(匯入、手動)的 id 可能空白或重複,拿 id 當主鍵會讓遷移與寫入失敗;
// id 另有索引。
//
// 新表與新欄位是權威來源:讀取一律從它們組出 model.Incident / model.AgentTask;寫入在同一個交易裡更新父列與子表,
// 並把父列的 split_at 設成 now()(讓啟動遷移不會拿舊 jsonb 覆蓋新表)。追加備註、追加時間軸只插入新的那幾列
// (和資料庫裡現有的比對,現有的是新清單的前綴就只插入尾巴;否則整份換掉)。
//
// 舊欄位 omixor_incidents.data、omixor_agent_tasks.notes、omixor_agent_tasks.links 不再寫(data 預設 '{}',notes、links 預設 '[]'),
// 只有遷移與「未展開列的退回讀取」會讀。【不支援回滾】新版不雙寫舊欄位,回到舊版會看不到新版期間的資料,要回滾只能從備份還原
// (見 MIGRATIONS.md)。確定全部列都展開(UnmigratedRows() == 0)、線上全是這個版本之後,下一個版本再執行
// (升級路徑不得跳過「遷移還在」的這個版本):
//
//	alter table omixor_incidents drop column data, drop column split_at;
//	alter table omixor_agent_tasks drop column notes, drop column links, drop column split_at;
//
// 並一併移除 migrateSplitIncidentsTasks(啟動時把舊 jsonb 展開進新表的遷移)與退回讀取。
// 遷移的分批、逾時、略過規則見 pg_split_migrate.go。

const pgSplitIncidentsTasksSchema = `
alter table omixor_incidents add column if not exists title text not null default '';
alter table omixor_incidents add column if not exists sev integer not null default 0;
alter table omixor_incidents add column if not exists commander text;
alter table omixor_incidents add column if not exists resolved_at timestamptz;
alter table omixor_incidents add column if not exists impact text not null default '';
alter table omixor_incidents add column if not exists ai_summary text;
alter table omixor_incidents add column if not exists created_by text;
alter table omixor_incidents add column if not exists split_at timestamptz;
alter table omixor_incidents alter column data set default '{}';
alter table omixor_agent_tasks add column if not exists split_at timestamptz;
create index if not exists omixor_incidents_org_status on omixor_incidents (org, status);
create index if not exists omixor_incidents_org_sev on omixor_incidents (org, sev);
create index if not exists omixor_incidents_org_commander on omixor_incidents (org, commander);
create table if not exists omixor_incident_services (
  org text not null,
  incident_id text not null,
  pos integer not null,
  name text not null,
  primary key (org, incident_id, pos),
  foreign key (org, incident_id) references omixor_incidents (org, id) on delete cascade on update cascade
);
create index if not exists omixor_incident_services_name on omixor_incident_services (org, name);
create table if not exists omixor_incident_follow_ups (
  org text not null,
  incident_id text not null,
  pos integer not null,
  ref text not null,
  primary key (org, incident_id, pos),
  foreign key (org, incident_id) references omixor_incidents (org, id) on delete cascade on update cascade
);
create index if not exists omixor_incident_follow_ups_ref on omixor_incident_follow_ups (org, ref);
create table if not exists omixor_incident_alerts (
  org text not null,
  incident_id text not null,
  pos integer not null,
  alert_id text not null,
  primary key (org, incident_id, pos),
  foreign key (org, incident_id) references omixor_incidents (org, id) on delete cascade on update cascade
);
create index if not exists omixor_incident_alerts_alert on omixor_incident_alerts (org, alert_id);
create table if not exists omixor_incident_events (
  org text not null,
  incident_id text not null,
  pos integer not null,
  id text not null default '',
  at timestamptz,
  actor text,
  kind text not null default '',
  text text not null default '',
  primary key (org, incident_id, pos),
  foreign key (org, incident_id) references omixor_incidents (org, id) on delete cascade on update cascade
);
create index if not exists omixor_incident_events_id on omixor_incident_events (org, incident_id, id);
create table if not exists omixor_agent_task_notes (
  org text not null,
  task_id text not null,
  pos integer not null,
  id text not null default '',
  author text,
  text text not null default '',
  at timestamptz,
  primary key (org, task_id, pos),
  foreign key (org, task_id) references omixor_agent_tasks (org, id) on delete cascade on update cascade
);
create index if not exists omixor_agent_task_notes_id on omixor_agent_task_notes (org, task_id, id);
create table if not exists omixor_agent_task_links (
  org text not null,
  task_id text not null,
  pos integer not null,
  url text not null,
  primary key (org, task_id, pos),
  foreign key (org, task_id) references omixor_agent_tasks (org, id) on delete cascade on update cascade
);
`

// dbq 連線池或交易(查詢、執行都能做)
type dbq interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// incidentsTasksSchemaReady 新欄位與子表都在了(不必再跑 DDL:`alter table … add column if not exists` 即使欄位已存在也會拿 ACCESS EXCLUSIVE 鎖,
// 會等長查詢、還擋住後面所有查詢,所以先查目錄再決定)
func (p *PG) incidentsTasksSchemaReady(c context.Context) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(c, `select
		to_regclass('omixor_agent_task_links') is not null
		and to_regclass('omixor_incident_events') is not null
		and exists (select 1 from information_schema.columns where table_schema = current_schema() and table_name = 'omixor_incidents' and column_name = 'split_at')
		and exists (select 1 from information_schema.columns where table_schema = current_schema() and table_name = 'omixor_incidents' and column_name = 'created_by')
		and exists (select 1 from information_schema.columns where table_schema = current_schema() and table_name = 'omixor_agent_tasks' and column_name = 'split_at')
		and exists (select 1 from pg_indexes where schemaname = current_schema() and indexname = 'omixor_incidents_org_commander')`).Scan(&ok)
	return ok, err
}

// migrateSplitIncidentsTasks 建新欄位與子表(沒建過才跑;在遷移 advisory lock 內),再把還沒展開(split_at 為 NULL)的事件、任務列展開進去
// (分批、冪等、逾時只告警;見 pg_split_migrate.go)。JSON 壞掉或型別不符的列略過,split_at 保持 NULL(讀取退回舊 jsonb、寫入被拒絕)。
func (p *PG) migrateSplitIncidentsTasks() error {
	return p.splitRun(func(c context.Context) error {
		if ok, err := p.incidentsTasksSchemaReady(c); err != nil {
			return err
		} else if ok {
			return nil
		}
		if _, err := p.pool.Exec(c, pgSplitIncidentsTasksSchema); err != nil {
			return fmt.Errorf("建事件、任務的新欄位與子表:%w", err)
		}
		return nil
	}, splitTasks, splitIncidents)
}

var splitTasks = &splitSpec{
	name: "任務備註與連結", table: "omixor_agent_tasks", keys: []string{"org", "id"},
	empty:  `case when jsonb_typeof(links) = 'array' and jsonb_typeof(notes) = 'array' then jsonb_array_length(links) = 0 and jsonb_array_length(notes) = 0 else false end`,
	expand: expandTask,
}

var splitIncidents = &splitSpec{
	name: "事件", table: "omixor_incidents", keys: []string{"org", "id"},
	expand: expandIncidentRow,
}

func expandTask(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	org, id := key[0], key[1]
	var links, notes []byte
	err := tx.QueryRow(c, `select links, notes from omixor_agent_tasks where org = $1 and id = $2 and split_at is null for update`, org, id).Scan(&links, &notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var ls []string
	var ns []model.AgentTaskNote
	if err := json.Unmarshal(links, &ls); err != nil {
		return skipErr(fmt.Errorf("links 不合格:%v", err))
	}
	if err := json.Unmarshal(notes, &ns); err != nil {
		return skipErr(fmt.Errorf("notes 不合格:%v", err))
	}
	// 舊 jsonb 的備註署名是舊字串(email、agent:<id>、routine…):這是遷移路徑,唯一還走過渡橋的地方,轉成 actor id 再寫子表
	w := p.whoTx(c, tx)
	for i := range ns {
		v, err := w.col("omixor_agent_task_notes.author", org, ns[i].Author)
		if err != nil {
			return err
		}
		ns[i].Author, _ = v.(string)
	}
	if err := replaceTaskChildren(c, tx, org, id, ls, ns); err != nil {
		return err
	}
	_, err = tx.Exec(c, `update omixor_agent_tasks set split_at = now() where org = $1 and id = $2`, org, id)
	return err
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

// replaceTaskChildren 遷移:先清掉(不該有)再整份插入
func replaceTaskChildren(c context.Context, q dbq, org, taskID string, links []string, notes []model.AgentTaskNote) error {
	for _, t := range []string{"omixor_agent_task_notes", "omixor_agent_task_links"} {
		if _, err := q.Exec(c, fmt.Sprintf(`delete from %s where org = $1 and task_id = $2`, t), org, taskID); err != nil {
			return err
		}
	}
	if err := insertTaskLinks(c, q, org, taskID, 0, links); err != nil {
		return err
	}
	return insertTaskNotes(c, q, org, taskID, 0, notes)
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
	if p.hasUnsplit() { // 還沒展開的任務:退回讀舊的 links、notes(jsonb;解不出來的欄位回空)
		rows, err := tx.Query(c, `select id, links, notes from omixor_agent_tasks where org = $1 and split_at is null and ($2::text = '' or id = $2)`, org, id)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		idx := make(map[string]int, len(out))
		for n := range out {
			idx[out[n].ID] = n
		}
		for rows.Next() {
			var tid string
			var lb, nb []byte
			if err := rows.Scan(&tid, &lb, &nb); err != nil {
				return nil, err
			}
			n, ok := idx[tid]
			if !ok {
				continue
			}
			ls, ns := []string{}, []model.AgentTaskNote{}
			if json.Unmarshal(lb, &ls) != nil || ls == nil {
				ls = []string{}
			}
			if json.Unmarshal(nb, &ns) != nil || ns == nil {
				ns = []model.AgentTaskNote{}
			}
			out[n].Links, out[n].Notes = ls, ns
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 事件(PG;表與子表的定義、拆表說明見 pg_split_incidents_tasks.go) ----
// 純量欄位在 omixor_incidents,服務、後續工作、告警、時間軸在子表;讀取一律從新結構組出 model.Incident。

// commander、created_by、事件的 actor 都是 actor id(NULL = 沒有);服務層出口用 present 轉相容輸出
var incidentCols = `id, title, sev, status, commander, started_at, resolved_at, impact, ai_summary, created_by`

func scanIncident(row pgx.Row) (*model.Incident, error) {
	var i model.Incident
	var started time.Time
	var resolved *time.Time
	var commander, createdBy NullID
	if err := row.Scan(&i.ID, &i.Title, &i.Sev, &i.Status, &commander, &started, &resolved, &i.Impact, &i.AISummary, &createdBy); err != nil {
		return nil, err
	}
	i.Commander, i.CreatedBy = commander.String(), createdBy.String()
	i.StartedAt = model.Time{Time: started.UTC()}
	i.ResolvedAt = tp(resolved)
	i.Services, i.Timeline, i.FollowUps = []string{}, []model.IncidentEvent{}, []string{}
	return &i, nil
}

type incidentKids struct {
	services, followUps, alerts map[string][]string
	events                      map[string][]model.IncidentEvent
}

// loadIncidentKids 載入子表(incidentID 空白 = 整個組織;各一次查詢,不是每個事件一次)
func loadIncidentKids(c context.Context, q dbq, org, incidentID string) (*incidentKids, error) {
	k := &incidentKids{services: map[string][]string{}, followUps: map[string][]string{}, alerts: map[string][]string{},
		events: map[string][]model.IncidentEvent{}}
	for _, l := range []struct {
		table, col string
		dst        map[string][]string
	}{
		{"omixor_incident_services", "name", k.services},
		{"omixor_incident_follow_ups", "ref", k.followUps},
		{"omixor_incident_alerts", "alert_id", k.alerts},
	} {
		rows, err := q.Query(c, fmt.Sprintf(`select incident_id, %s from %s where org = $1 and ($2::text = '' or incident_id = $2) order by incident_id, pos`, l.col, l.table), org, incidentID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, v string
			if err := rows.Scan(&id, &v); err != nil {
				rows.Close()
				return nil, err
			}
			l.dst[id] = append(l.dst[id], v)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	rows, err := q.Query(c, `select incident_id, id, at, actor, kind, text from omixor_incident_events
		where org = $1 and ($2::text = '' or incident_id = $2) order by incident_id, pos`, org, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var iid string
		var e model.IncidentEvent
		var at *time.Time
		var actor NullID
		if err := rows.Scan(&iid, &e.ID, &at, &actor, &e.Kind, &e.Text); err != nil {
			return nil, err
		}
		e.ActorID = actor.String()
		e.Date = fromNull(at)
		k.events[iid] = append(k.events[iid], e)
	}
	return k, rows.Err()
}

func (k *incidentKids) fill(i *model.Incident) {
	if v := k.services[i.ID]; v != nil {
		i.Services = v
	}
	if v := k.events[i.ID]; v != nil {
		i.Timeline = v
	}
	if v := k.followUps[i.ID]; v != nil {
		i.FollowUps = v
	}
	i.AlertIDs = k.alerts[i.ID] // 沒有就是 nil(omitempty)
}

// readIncidents 讀 org 的事件(id 空白 = 全部,新的在前);可重複讀的唯讀交易,父表與子表看到同一個時間點
func (p *PG) readIncidents(org, id string) ([]model.Incident, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.BeginTx(c, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(c)
	rows, err := tx.Query(c, `select `+incidentCols+` from omixor_incidents where org = $1 and ($2::text = '' or id = $2) order by pos desc`, org, id)
	if err != nil {
		return nil, err
	}
	out := []model.Incident{}
	for rows.Next() {
		i, err := scanIncident(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	k, err := loadIncidentKids(c, tx, org, id)
	if err != nil {
		return nil, err
	}
	for n := range out {
		k.fill(&out[n])
	}
	if p.hasUnsplit() {
		old, err := unsplitIncidents(c, tx, org, id)
		if err != nil {
			return nil, err
		}
		for n := range out {
			if i, ok := old[out[n].ID]; ok {
				i.Status, i.StartedAt = nonEmptyStatus(i.Status, out[n].Status), nonZeroTime(i.StartedAt, out[n].StartedAt)
				out[n] = i
			}
		}
	}
	return out, nil
}

func insertIncidentEvents(c context.Context, q dbq, org, id string, start int, evs []model.IncidentEvent) error {
	if len(evs) == 0 {
		return nil
	}
	n := len(evs)
	ids, kinds, texts, actors := make([]string, n), make([]string, n), make([]string, n), make([]*string, n)
	ats := make([]*time.Time, n)
	for k, e := range evs {
		ids[k], kinds[k], texts[k], ats[k], actors[k] = validUTF8(e.ID), validUTF8(e.Kind), validUTF8(e.Text), nullSecs(e.Date), IDPtr(e.ActorID)
	}
	_, err := q.Exec(c, `insert into omixor_incident_events (org, incident_id, pos, id, at, actor, kind, text)
		select $1, $2, pos, id, at, actor, kind, text from unnest($3::int[], $4::text[], $5::timestamptz[], $6::text[], $7::text[], $8::text[])
		as t(pos, id, at, actor, kind, text)`, org, id, posRange(start, n), ids, ats, actors, kinds, texts)
	return err
}

// incidentLists 事件的三個字串清單子表
var incidentLists = []struct{ table, col string }{
	{"omixor_incident_services", "name"},
	{"omixor_incident_follow_ups", "ref"},
	{"omixor_incident_alerts", "alert_id"},
}

func incidentListsOf(i model.Incident) [][]string {
	return [][]string{i.Services, i.FollowUps, i.AlertIDs}
}

func insertIncidentList(c context.Context, q dbq, table, col, org, id string, start int, items []string) error {
	if len(items) == 0 {
		return nil
	}
	_, err := q.Exec(c, fmt.Sprintf(`insert into %[1]s (org, incident_id, pos, %[2]s)
		select $1, $2, pos, v from unnest($3::int[], $4::text[]) as t(pos, v)`, table, col),
		org, id, posRange(start, len(items)), items)
	return err
}

// insertIncident 寫一筆事件(commander、created_by、事件 actor 都已是 actor id;沒有 = NULL;不存在的 actor 由外鍵擋成 ErrBadRef)
func insertIncident(c context.Context, tx pgx.Tx, org string, i model.Incident, pos int64) error {
	_, err := tx.Exec(c, `insert into omixor_incidents (org, id, number, pos, status, started_at, title, sev, commander, resolved_at, impact, ai_summary, created_by, split_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())`,
		org, i.ID, i.Number(), pos, i.Status, secs(i.StartedAt.Time), i.Title, i.Sev, IDOrNil(i.Commander), optSecs(i.ResolvedAt), i.Impact, i.AISummary, IDOrNil(i.CreatedBy))
	if err != nil {
		return fkErr(err)
	}
	for n, items := range incidentListsOf(i) {
		if err := insertIncidentList(c, tx, incidentLists[n].table, incidentLists[n].col, org, i.ID, 0, items); err != nil {
			return err
		}
	}
	return fkErr(insertIncidentEvents(c, tx, org, i.ID, 0, i.Timeline))
}

// syncIncidentKids UpdateIncident:父列已更新(行鎖在手)後,讓子表等於 i;時間軸與清單是現有的延伸就只插入新的幾列
func syncIncidentKids(c context.Context, q dbq, org string, i model.Incident) error {
	old, err := loadIncidentKids(c, q, org, i.ID)
	if err != nil {
		return err
	}
	olds := [][]string{old.services[i.ID], old.followUps[i.ID], old.alerts[i.ID]}
	for n, cur := range incidentListsOf(i) {
		l := incidentLists[n]
		if err := syncSlice(c, q, olds[n], cur, func(a, b string) bool { return a == b },
			fmt.Sprintf(`delete from %s where org = $1 and incident_id = $2`, l.table), []any{org, i.ID},
			func(start int, items []string) error {
				return insertIncidentList(c, q, l.table, l.col, org, i.ID, start, items)
			}); err != nil {
			return err
		}
	}
	return syncSlice(c, q, old.events[i.ID], i.Timeline,
		func(a, b model.IncidentEvent) bool {
			return a.ID == b.ID && a.Kind == b.Kind && a.Text == b.Text && a.ActorID == b.ActorID && timeEq(a.Date.Time, b.Date.Time)
		},
		`delete from omixor_incident_events where org = $1 and incident_id = $2`, []any{org, i.ID},
		func(start int, items []model.IncidentEvent) error {
			return fkErr(insertIncidentEvents(c, q, org, i.ID, start, items))
		})
}

func (p *PG) ListIncidents(org string) ([]model.Incident, error) { return p.readIncidents(org, "") }

func (p *PG) GetIncident(org, id string) (*model.Incident, error) {
	l, err := p.readIncidents(org, id)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, ErrNotFound
	}
	return &l[0], nil
}

func (p *PG) CreateIncident(org string, i model.Incident) (model.Incident, error) {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return i, err
	}
	defer tx.Rollback(c)
	// 號碼由序號表單句原子取得(從 IncidentNumberBase 往上);序號列的行鎖到交易結束,
	// 同一個組織的建立排隊,所以讀 max(pos) 不會撞
	num, err := seqNext(c, tx, org, "incident", model.IncidentNumberBase)
	if err != nil {
		return i, err
	}
	var pos int64
	if err := tx.QueryRow(c, `select coalesce(max(pos),0) from omixor_incidents where org = $1`, org).Scan(&pos); err != nil {
		return i, err
	}
	i.ID = fmt.Sprintf("%s%d", model.IncidentPrefix, num)
	if err := insertIncident(c, tx, org, i, pos+1); err != nil {
		return i, err
	}
	return i, tx.Commit(c)
}

func (p *PG) UpdateIncident(org string, i model.Incident) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	if err := p.guardUnsplit(c, tx, splitIncidents, org, i.ID); err != nil { // 還沒展開的列先展開(展開不了就拒絕,不蓋掉舊資料)
		return err
	}
	// 先更新父列(取得行鎖,同一個事件的並行更新排隊),再讓子表等於 i
	tag, err := tx.Exec(c, `update omixor_incidents set status = $3, started_at = $4, title = $5, sev = $6, commander = $7, resolved_at = $8,
		impact = $9, ai_summary = $10, created_by = $11, split_at = now() where org = $1 and id = $2`,
		org, i.ID, i.Status, secs(i.StartedAt.Time), i.Title, i.Sev, IDOrNil(i.Commander), optSecs(i.ResolvedAt), i.Impact, i.AISummary, IDOrNil(i.CreatedBy))
	if err != nil {
		return fkErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := syncIncidentKids(c, tx, org, i); err != nil {
		return err
	}
	return tx.Commit(c)
}

func (p *PG) ImportIncidents(org string, list []model.Incident) (int, error) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-incidents:' || $1))`, org); err != nil {
		return 0, err
	}
	if err := seqBump(c, tx, org, "incident", 0); err != nil { // 鎖住序號列,和 CreateIncident 互斥
		return 0, err
	}
	var n int
	if err := tx.QueryRow(c, `select count(*) from omixor_incidents where org = $1`, org).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, fmt.Errorf("組織 %s 已經有 %d 筆事件", org, n)
	}
	top := 0
	w := p.whoTx(c, tx) // 匯入檔是舊格式(字串署名),經橋轉成 actor id(見 pg_incident_legacy.go)
	for k, i := range list {
		if err := insertIncidentLegacy(c, tx, w, org, i, int64(len(list)-k)); err != nil {
			return 0, fmt.Errorf("%s:%w", i.ID, err)
		}
		top = max(top, i.Number())
	}
	if err := seqBump(c, tx, org, "incident", top); err != nil {
		return 0, err
	}
	return len(list), tx.Commit(c)
}

func nonEmptyStatus(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func nonZeroTime(a, b model.Time) model.Time {
	if a.IsZero() {
		return b
	}
	return a
}

var _ Store = (*PG)(nil)

package store

// pg_incident_legacy.go 事件的舊格式入口(字串署名):匯入檔(ImportIncidents)與啟動遷移展開舊 jsonb(expandIncident)。
// 這兩條路進來的 commander、created_by 是舊字串、時間軸 actor 是舊的 {kind, id},經過渡橋(whoConv,寬鬆解析器)轉成 actor id 才寫入;
// 舊結構(actor 欄位引擎還沒轉換)的資料庫原樣寫舊欄位,由引擎轉換。一般的建立與更新(CreateIncident、UpdateIncident)不經這裡,直接收 actor id。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

func insertIncidentLegacy(c context.Context, tx pgx.Tx, w *whoConv, org string, i model.Incident, pos int64) error {
	commander, err := w.col("omixor_incidents.commander", org, i.Commander)
	if err != nil {
		return err
	}
	createdBy, err := w.col("omixor_incidents.created_by", org, i.CreatedBy)
	if err != nil {
		return err
	}
	_, err = tx.Exec(c, `insert into omixor_incidents (org, id, number, pos, status, started_at, title, sev, commander, resolved_at, impact, ai_summary, created_by, split_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())`,
		org, i.ID, i.Number(), pos, i.Status, secs(i.StartedAt.Time), i.Title, i.Sev, commander, optSecs(i.ResolvedAt), i.Impact, i.AISummary, createdBy)
	if err != nil {
		return err
	}
	for n, items := range incidentListsOf(i) {
		if err := insertIncidentList(c, tx, incidentLists[n].table, incidentLists[n].col, org, i.ID, 0, items); err != nil {
			return err
		}
	}
	return insertIncidentEventsLegacy(c, tx, w, org, i.ID, 0, i.Timeline)
}

func insertIncidentEventsLegacy(c context.Context, q dbq, w *whoConv, org, id string, start int, evs []model.IncidentEvent) error {
	if len(evs) == 0 {
		return nil
	}
	n := len(evs)
	ids, kinds, texts := make([]string, n), make([]string, n), make([]string, n)
	ats := make([]*time.Time, n)
	for k, e := range evs {
		ids[k], kinds[k], texts[k], ats[k] = validUTF8(e.ID), validUTF8(e.Kind), validUTF8(e.Text), nullSecs(e.Date)
	}
	if !w.converted(actorColByKey["omixor_incident_events.actor"]) {
		// 舊結構(actor 引擎還沒轉換;拆表遷移展開舊 jsonb 時):寫舊的 actor_kind、actor_id,轉換由引擎做
		if _, err := q.Exec(c, `alter table omixor_incident_events add column if not exists actor_kind text not null default '', add column if not exists actor_id text not null default ''`); err != nil {
			return err
		}
		akinds, aids := make([]string, n), make([]string, n)
		for k, e := range evs {
			akinds[k], aids[k] = e.Actor.Kind, e.Actor.ID
		}
		_, err := q.Exec(c, `insert into omixor_incident_events (org, incident_id, pos, id, at, actor_kind, actor_id, kind, text)
			select $1, $2, pos, id, at, ak, ai, kind, text from unnest($3::int[], $4::text[], $5::timestamptz[], $6::text[], $7::text[], $8::text[], $9::text[])
			as t(pos, id, at, ak, ai, kind, text)`, org, id, posRange(start, n), ids, ats, akinds, aids, kinds, texts)
		return err
	}
	actors := make([]*string, n)
	for k, e := range evs {
		v, err := w.event(org, e.Actor.Kind, e.Actor.ID)
		if err != nil {
			return err
		}
		if s, ok := v.(string); ok {
			actors[k] = &s
		}
	}
	_, err := q.Exec(c, `insert into omixor_incident_events (org, incident_id, pos, id, at, actor, kind, text)
		select $1, $2, pos, id, at, actor, kind, text from unnest($3::int[], $4::text[], $5::timestamptz[], $6::text[], $7::text[], $8::text[])
		as t(pos, id, at, actor, kind, text)`, org, id, posRange(start, n), ids, ats, actors, kinds, texts)
	return err
}

// ---- 啟動遷移:舊 data(jsonb)展開(批次驅動見 pg_split_migrate.go) ----

func expandIncidentRow(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	org, id := key[0], key[1]
	var b []byte
	err := tx.QueryRow(c, `select data from omixor_incidents where org = $1 and id = $2 and split_at is null for update`, org, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var i model.Incident
	if err := json.Unmarshal(b, &i); err != nil {
		return skipErr(fmt.Errorf("data 不合格:%v", err))
	}
	return expandIncident(c, tx, p.whoTx(c, tx), org, id, i.Clone())
}

// unsplitIncidents 退回讀取:還沒展開的事件用舊 data(jsonb)組出來(解不出來的列不在結果裡,呼叫端保留新表組的樣子)
func unsplitIncidents(c context.Context, q dbq, org, id string) (map[string]model.Incident, error) {
	rows, err := q.Query(c, `select id, data from omixor_incidents where org = $1 and split_at is null and ($2::text = '' or id = $2)`, org, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.Incident{}
	for rows.Next() {
		var rid string
		var b []byte
		if err := rows.Scan(&rid, &b); err != nil {
			return nil, err
		}
		var i model.Incident
		if json.Unmarshal(b, &i) != nil {
			continue
		}
		i = i.Clone()
		i.ID = rid
		out[rid] = i
	}
	return out, rows.Err()
}

func expandIncident(c context.Context, tx pgx.Tx, w *whoConv, org, id string, i model.Incident) error {
	commander, err := w.col("omixor_incidents.commander", org, i.Commander)
	if err != nil {
		return err
	}
	createdBy, err := w.col("omixor_incidents.created_by", org, i.CreatedBy)
	if err != nil {
		return err
	}
	// 父列用 JSON 的值(原本讀取就是以 JSON 為準);status、started_at 欄位本來就同步存,JSON 缺的(空字串、零時間)保留原欄位值
	if _, err := tx.Exec(c, `update omixor_incidents set title = $3, sev = $4, commander = $5, resolved_at = $6, impact = $7, ai_summary = $8, created_by = $9,
		status = case when $10::text = '' then status else $10 end,
		started_at = coalesce($11::timestamptz, started_at)
		where org = $1 and id = $2`,
		org, id, i.Title, i.Sev, commander, optSecs(i.ResolvedAt), i.Impact, i.AISummary, createdBy, i.Status, nullSecs(i.StartedAt)); err != nil {
		return err
	}
	for _, l := range incidentLists {
		if _, err := tx.Exec(c, fmt.Sprintf(`delete from %s where org = $1 and incident_id = $2`, l.table), org, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(c, `delete from omixor_incident_events where org = $1 and incident_id = $2`, org, id); err != nil {
		return err
	}
	for n, items := range incidentListsOf(i) {
		if err := insertIncidentList(c, tx, incidentLists[n].table, incidentLists[n].col, org, id, 0, items); err != nil {
			return err
		}
	}
	if err := insertIncidentEventsLegacy(c, tx, w, org, id, 0, i.Timeline); err != nil {
		return err
	}
	_, err = tx.Exec(c, `update omixor_incidents set split_at = now() where org = $1 and id = $2`, org, id)
	return err
}


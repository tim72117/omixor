package store

// pg_incident_legacy.go 事件的舊格式入口(字串署名):匯入檔(ImportIncidents)。
// 匯入進來的 commander、created_by 是舊字串、時間軸 actor 是舊的 {kind, id},經過渡橋(whoConv,寬鬆解析器)轉成 actor id 才寫入。一般的建立與更新(CreateIncident、UpdateIncident)不經這裡,直接收 actor id。

import (
	"context"
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

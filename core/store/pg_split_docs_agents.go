package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"omixor-core/model"
)

// ---- 文件標籤、角色的文件權限與例行任務(PG):拆成有外鍵的資料表 ----
//
// 表(對外的 model、Store 介面都不變,只是儲存方式):
//   - omixor_doc_tags (org, doc_id, tag, pos):主鍵 (org, doc_id, tag);外鍵 (org, doc_id) → omixor_docs (org, id) cascade、update cascade;
//     (org, tag) 索引給「依標籤篩選」用。pos 是標籤在文件裡的順序。
//   - omixor_agent_doc_access (org, agent_id, access_key, level):主鍵 (org, agent_id, access_key);外鍵 (org, agent_id) → omixor_agents cascade。
//     DocAccess 是 map[key]level,key 是區塊 id、頁籤網址,或 "<專案 id>:<區塊 id 或頁籤網址>";map 本來就沒有順序,所以不需要 pos。
//     access_key 保持原樣一欄,不拆成專案與目標:歷史資料可以指向已經不存在的區塊、頁籤、專案(結構是整份取代的),
//     所以不對文件櫃或專案表加外鍵。
//   - omixor_agent_routines (org, agent_id, id, pos, …):主鍵 (org, agent_id, id);外鍵 (org, agent_id) → omixor_agents cascade;
//     欄位對應 model.AgentRoutine(created_at、last_run_at 可為 NULL;pos 是順序)。
//
// omixor_agent_tasks.routine_id 沒有加外鍵:任務是歷史紀錄,例行任務被刪(或整份取代)之後任務仍然要留著原本的 routine_id
// (service 用它判斷「這個例行任務還有沒有未完成的任務」),也不動它;on delete set null 會改掉任務的資料,
// 而且既有資料裡的 routine_id 可能已經指向被刪的例行任務,加不上外鍵,所以維持字串參照。
//
// 兩張父表(omixor_docs、omixor_agents)各有 split_at(timestamptz):「這一列已經展開到新表」的標記,
// agents 的 doc_access 與 routines 共用同一個標記(兩個一起展開、一起寫、一起讀)。
//   - 新表是權威來源:讀取一律從新表組出原本的結構;寫入(整份取代)和父列在同一個交易裡,同時把 split_at 設成 now()。
//   - 舊欄位 tags、doc_access、routines(jsonb)已經不再寫,只有遷移與「未展開列的退回讀取」會讀。
//     【不支援回滾】新版不雙寫舊欄位,回到舊版會看不到新版期間的資料,要回滾只能從備份還原(見 MIGRATIONS.md)。
//     確定全部列都展開(UnmigratedRows() == 0)、線上全是這個版本之後,下一個版本再執行
//     (升級路徑不得跳過「遷移還在」的這個版本):
//
//	alter table omixor_docs drop column tags, drop column split_at;
//	alter table omixor_agents drop column doc_access, drop column routines, drop column split_at;
//
// 並一併移除遷移與退回讀取的程式。
//
// 遷移(分批、冪等,見 pg_split_migrate.go)對舊資料的處理:標籤重複去重(留第一個);例行任務 id 空白或重複補新 uuid(第一個保留原 id,
// 任務表的 routine_id 字串參照不受影響),doc_access 照樣展開;JSON 結構不對的列略過(split_at 保持 NULL,讀取退回舊 jsonb,寫入被拒絕)。

// validUTF8 把不合法的 UTF-8 換成 U+FFFD(不合法的 UTF-8 進 text 欄位會在 PG 報錯;以前經 json.Marshal 會被這樣換掉)
func validUTF8(s string) string { return strings.ToValidUTF8(s, "�") }

// replaceDocTags 在交易裡把文件的標籤整份換成 tags(先刪再插;文件那一列已在同一個交易裡被更新或插入)
func replaceDocTags(c context.Context, tx pgx.Tx, org, docID string, tags []string) error {
	clean := make([]string, len(tags))
	for i, t := range tags {
		clean[i] = validUTF8(t)
	}
	tags = model.DedupExact(clean)
	if _, err := tx.Exec(c, `delete from omixor_doc_tags where org = $1 and doc_id = $2`, org, docID); err != nil {
		return err
	}
	if len(tags) == 0 {
		return nil
	}
	pos := make([]int32, len(tags))
	for i := range tags {
		pos[i] = int32(i)
	}
	_, err := tx.Exec(c, `insert into omixor_doc_tags (org, doc_id, tag, pos)
		select $1, $2, tag, pos from unnest($3::text[], $4::int[]) as t(tag, pos)`, org, docID, tags, pos)
	return fkErr(err)
}

// checkRoutinesUnique 新表主鍵的唯一性:例行任務 id 不能重複
func checkRoutinesUnique(rs []model.AgentRoutine) error {
	seen := map[string]bool{}
	for _, r := range rs {
		if seen[r.ID] {
			return fmt.Errorf("例行任務 id 重複:%q", r.ID)
		}
		seen[r.ID] = true
	}
	return nil
}

// replaceAgentChildren 在交易裡把角色的文件權限與例行任務整份換成給定的內容(角色那一列已在同一個交易裡被更新或插入)
func replaceAgentChildren(c context.Context, tx pgx.Tx, org, agentID string, access map[string]string, routines []model.AgentRoutine) error {
	if err := checkRoutinesUnique(routines); err != nil {
		return err
	}
	if _, err := tx.Exec(c, `delete from omixor_agent_doc_access where org = $1 and agent_id = $2`, org, agentID); err != nil {
		return err
	}
	if len(access) > 0 {
		keys := make([]string, 0, len(access))
		levels := make([]string, 0, len(access))
		for k, v := range access {
			keys = append(keys, validUTF8(k))
			levels = append(levels, validUTF8(v))
		}
		if _, err := tx.Exec(c, `insert into omixor_agent_doc_access (org, agent_id, access_key, level)
			select $1, $2, k, l from unnest($3::text[], $4::text[]) as t(k, l)`, org, agentID, keys, levels); err != nil {
			return fkErr(err)
		}
	}
	if _, err := tx.Exec(c, `delete from omixor_agent_routines where org = $1 and agent_id = $2`, org, agentID); err != nil {
		return err
	}
	if len(routines) == 0 {
		return nil
	}
	n := len(routines)
	ids, titles, ins, kinds, prios := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	pos, every, hour, minute, weekday := make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n), make([]int32, n)
	enabled := make([]bool, n)
	created, lastRun := make([]*time.Time, n), make([]*time.Time, n)
	for i, r := range routines {
		ids[i], titles[i], ins[i], kinds[i], prios[i] = validUTF8(r.ID), validUTF8(r.Title), validUTF8(r.Instructions), validUTF8(r.Kind), validUTF8(r.Priority)
		pos[i], every[i], hour[i], minute[i], weekday[i] = int32(i), int32(r.EveryHours), int32(r.Hour), int32(r.Minute), int32(r.Weekday)
		enabled[i] = r.Enabled
		created[i] = optSecs(&r.CreatedAt)
		lastRun[i] = optSecs(r.LastRunAt)
	}
	_, err := tx.Exec(c, `insert into omixor_agent_routines (org, agent_id, id, pos, title, instructions, kind, enabled, every_hours, hour, minute, weekday, priority, created_at, last_run_at)
		select $1, $2, id, pos, title, instructions, kind, enabled, every_hours, hour, minute, weekday, priority, created_at, last_run_at
		from unnest($3::text[], $4::int[], $5::text[], $6::text[], $7::text[], $8::boolean[], $9::int[], $10::int[], $11::int[], $12::int[], $13::text[], $14::timestamptz[], $15::timestamptz[])
		as t(id, pos, title, instructions, kind, enabled, every_hours, hour, minute, weekday, priority, created_at, last_run_at)`,
		org, agentID, ids, pos, titles, ins, kinds, enabled, every, hour, minute, weekday, prios, created, lastRun)
	return fkErr(err)
}

// loadAgentChildren 一次把這個組織(agentID 不空就只載那一個角色)的文件權限與例行任務載進 agents(批次查詢,沒有 N+1)。
// q 要是一個唯讀、可重複讀的交易(呼叫端的角色列也在同一個交易裡讀,看到同一個時間點)。
// 沒有資料的角色是空的 map、空的陣列(不是 nil);還沒展開(split_at 為 NULL)的角色退回讀舊 jsonb(解不出來的回空)。
func (p *PG) loadAgentChildren(c context.Context, q dbq, org, agentID string, agents []model.Agent) error {
	idx := make(map[string]int, len(agents))
	for i := range agents {
		idx[agents[i].ID] = i
		agents[i].DocAccess = map[string]string{}
		agents[i].Routines = []model.AgentRoutine{}
	}
	if len(agents) == 0 {
		return nil
	}
	rows, err := q.Query(c, `select agent_id, access_key, level from omixor_agent_doc_access
		where org = $1 and ($2 = '' or agent_id = $2)`, org, agentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var aid, k, v string
		if err := rows.Scan(&aid, &k, &v); err != nil {
			rows.Close()
			return err
		}
		if i, ok := idx[aid]; ok {
			agents[i].DocAccess[k] = v
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = q.Query(c, `select agent_id, id, title, instructions, kind, enabled, every_hours, hour, minute, weekday, priority, created_at, last_run_at
		from omixor_agent_routines where org = $1 and ($2 = '' or agent_id = $2) order by agent_id, pos`, org, agentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var aid string
		var r model.AgentRoutine
		var created, last *time.Time
		if err := rows.Scan(&aid, &r.ID, &r.Title, &r.Instructions, &r.Kind, &r.Enabled, &r.EveryHours, &r.Hour, &r.Minute, &r.Weekday, &r.Priority, &created, &last); err != nil {
			rows.Close()
			return err
		}
		r.CreatedAt = fromNull(created)
		r.LastRunAt = tp(last)
		if i, ok := idx[aid]; ok {
			agents[i].Routines = append(agents[i].Routines, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !p.hasUnsplit() {
		return nil
	}
	rows, err = q.Query(c, `select id, doc_access, routines from omixor_agents where org = $1 and split_at is null and ($2 = '' or id = $2)`, org, agentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var acc, rts []byte
		if err := rows.Scan(&id, &acc, &rts); err != nil {
			return err
		}
		if i, ok := idx[id]; ok {
			agents[i].DocAccess, agents[i].Routines = lenientAccess(acc), lenientRoutines(rts)
		}
	}
	return rows.Err()
}

// lenientAccess 退回讀取舊 jsonb:不是物件回空;值不是字串的丟掉
func lenientAccess(b []byte) map[string]string {
	out := map[string]string{}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return out
	}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// lenientRoutines 退回讀取舊 jsonb:解不出來回空
func lenientRoutines(b []byte) []model.AgentRoutine {
	var rs []model.AgentRoutine
	if err := json.Unmarshal(b, &rs); err != nil || rs == nil {
		return []model.AgentRoutine{}
	}
	return rs
}

// migrateSplitDocsAgents 啟動時把還沒展開(split_at 為 NULL)的文件標籤、角色權限與例行任務從舊 jsonb 展開進新表(分批、冪等,見 pg_split_migrate.go)
func (p *PG) migrateSplitDocsAgents() error {
	return p.splitRun(nil, splitDocs, splitAgents)
}

var splitDocs = &splitSpec{
	name: "文件標籤", table: "omixor_docs", keys: []string{"org", "id"},
	empty:  `case when jsonb_typeof(tags) = 'array' then jsonb_array_length(tags) = 0 else false end`,
	expand: expandDocTags,
}

var splitAgents = &splitSpec{
	name: "角色權限與例行任務", table: "omixor_agents", keys: []string{"org", "id"},
	empty:  `case when jsonb_typeof(doc_access) = 'object' and jsonb_typeof(routines) = 'array' then doc_access = '{}'::jsonb and jsonb_array_length(routines) = 0 else false end`,
	expand: expandAgent,
}

func expandDocTags(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	org, id := key[0], key[1]
	var b []byte
	err := tx.QueryRow(c, `select tags from omixor_docs where org = $1 and id = $2 and split_at is null for update`, org, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var tags []string
	if err := json.Unmarshal(b, &tags); err != nil {
		return skipErr(fmt.Errorf("標籤 JSON 不合格:%v", err))
	}
	if err := replaceDocTags(c, tx, org, id, tags); err != nil { // 重複的標籤在這裡去重(留第一個)
		return err
	}
	_, err = tx.Exec(c, `update omixor_docs set split_at = now() where org = $1 and id = $2`, org, id)
	return err
}

// fixRoutineIDs 例行任務 id 空白或重複的補新 uuid(第一個出現的保留原 id)
func fixRoutineIDs(rs []model.AgentRoutine) []model.AgentRoutine {
	seen := map[string]bool{}
	for i := range rs {
		if rs[i].ID == "" || seen[rs[i].ID] {
			rs[i].ID = newRoutineID()
		}
		seen[rs[i].ID] = true
	}
	return rs
}

func expandAgent(p *PG, c context.Context, tx pgx.Tx, key []string) error {
	org, id := key[0], key[1]
	var acc, rts []byte
	err := tx.QueryRow(c, `select doc_access, routines from omixor_agents where org = $1 and id = $2 and split_at is null for update`, org, id).Scan(&acc, &rts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var access map[string]string
	if err := json.Unmarshal(acc, &access); err != nil {
		return skipErr(fmt.Errorf("doc_access JSON 不合格:%v", err))
	}
	var routines []model.AgentRoutine
	if err := json.Unmarshal(rts, &routines); err != nil {
		return skipErr(fmt.Errorf("routines JSON 不合格:%v", err))
	}
	if err := replaceAgentChildren(c, tx, org, id, access, fixRoutineIDs(routines)); err != nil {
		return err
	}
	_, err = tx.Exec(c, `update omixor_agents set split_at = now() where org = $1 and id = $2`, org, id)
	return err
}

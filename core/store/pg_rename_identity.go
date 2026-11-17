package store

import (
	"errors"
	"fmt"
	"strings"
)

// RenameIdentity 把資料裡的身分 old 改成 new(例如 omixor console 本機核心的身分從舊的 ops-web 改成 omixor-console)。
// 只給「使用端自己的資料庫」用(console 本機模式啟動時呼叫);雲端伺服器不呼叫。
//
// 改的範圍(值剛好等於 old 或 "user:"+old 才改,其他不動):
//   - 文字欄位:任務的 claimed_by / created_by、Issue 的 created_by / assignee、Issue 留言的 author、
//     文件的 author、頻道的 created_by、頻道訊息的 author、已讀的 member、各設定的 updated_by
//   - jsonb 欄位裡「整個字串值」等於 old 或 "user:"+old 的:任務的 notes、頻道的 members、訊息的 mentions、
//     組織鏈的 edges、事件的 data
//
// 不改:稽核紀錄(omixor_audit,是歷史紀錄)、使用者與成員表(本機身分不會寫進去)。
// 冪等:改完再跑一次不會再改到東西;全部在一個交易裡,失敗就全部不改。回傳改了幾列。
func (p *PG) RenameIdentity(old, new string) (int64, error) {
	old, new = strings.TrimSpace(old), strings.TrimSpace(new)
	if old == "" || new == "" || old == new {
		return 0, errors.New("身分名稱不能空白,也不能相同")
	}
	if strings.ContainsAny(old+new, "\"\\") {
		return 0, errors.New("身分名稱不能有引號或反斜線")
	}
	oldNode, newNode := "user:"+old, "user:"+new
	textCols := []struct{ table, col string }{
		{"omixor_agent_tasks", "claimed_by"},
		{"omixor_agent_tasks", "created_by"},
		{"ops_issues", "created_by"},
		{"ops_issues", "assignee"},
		{"ops_issue_comments", "author"},
		{"omixor_docs", "author"},
		{"omixor_channels", "created_by"},
		{"omixor_channel_messages", "author"},
		{"omixor_org_integrations", "updated_by"},
		{"omixor_org_system", "updated_by"},
		{"omixor_server_features", "updated_by"},
	}
	jsonCols := []struct{ table, col string }{
		{"omixor_agent_tasks", "notes"},
		{"omixor_channels", "members"},
		{"omixor_channel_messages", "mentions"},
		{"omixor_org_chains", "edges"},
		{"omixor_incidents", "data"},
	}
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(c)
	var n int64
	for _, tc := range textCols {
		q := fmt.Sprintf(`update %[1]s set %[2]s = case %[2]s when $1 then $2 else $4 end where %[2]s in ($1, $3)`, tc.table, tc.col)
		r, err := tx.Exec(c, q, old, new, oldNode, newNode)
		if err != nil {
			return 0, fmt.Errorf("%s.%s:%w", tc.table, tc.col, err)
		}
		n += r.RowsAffected()
	}
	// 已讀(主鍵含 member):新身分已經有一列時不搬(保留新的)
	r, err := tx.Exec(c, `update omixor_channel_reads r set member = case r.member when $1 then $2 else $4 end
where r.member in ($1, $3) and not exists (
  select 1 from omixor_channel_reads x where x.org = r.org and x.channel_id = r.channel_id
  and x.member = case r.member when $1 then $2 else $4 end)`, old, new, oldNode, newNode)
	if err != nil {
		return 0, fmt.Errorf("omixor_channel_reads.member:%w", err)
	}
	n += r.RowsAffected()
	// jsonb:只換「整個字串值」(含引號)等於舊身分的地方
	qOld, qNew, qOldNode, qNewNode := jsonStr(old), jsonStr(new), jsonStr(oldNode), jsonStr(newNode)
	for _, jc := range jsonCols {
		q := fmt.Sprintf(`update %[1]s set %[2]s = replace(replace(%[2]s::text, $1, $2), $3, $4)::jsonb
where strpos(%[2]s::text, $1) > 0 or strpos(%[2]s::text, $3) > 0`, jc.table, jc.col)
		r, err := tx.Exec(c, q, qOld, qNew, qOldNode, qNewNode)
		if err != nil {
			return 0, fmt.Errorf("%s.%s:%w", jc.table, jc.col, err)
		}
		n += r.RowsAffected()
	}
	if err := tx.Commit(c); err != nil {
		return 0, err
	}
	return n, nil
}

// jsonStr 字串在 jsonb::text 裡的樣子(身分名稱只有英數與 - _ . @ :,不需要跳脫)
func jsonStr(s string) string { return `"` + s + `"` }

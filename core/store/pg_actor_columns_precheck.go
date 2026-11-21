package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// pg_actor_columns_precheck.go 「誰」欄位引擎的步驟 0(前置檢查)與「沒轉完所以不能啟動」的錯誤訊息。
//
// 前置檢查不過 = 引擎整個沒有做(22 欄一個都沒轉),而程式只認轉換後的結構,所以 migrateActorColumnsStep 會讓啟動失敗(進度保留、修好後重啟)。
// 訊息要指到真因並說明怎麼處理:壞掉的舊 jsonb 列(列出組織/頻道與表、哪個舊欄位)、前面的步驟被略過(角色/使用者 actor 遷移因等不到鎖、
// 表太大、預算用完)、拆表遷移預算用完(調大 OMIXOR_MIGRATE_BUDGET_MINUTES 或重啟接續)。

// unmigratedWhy 前置檢查「還有沒展開的列」的原因關鍵字
const unmigratedWhy = "UnmigratedRows != 0"

// errActorPartial 預算用完而中斷(不是錯誤:下次啟動接著做)
var errActorPartial = errors.New("啟動預算用完")

// splitLegacyColumns 每張拆表的舊 jsonb 欄位(壞列要修的就是它;給失敗訊息用)
var splitLegacyColumns = map[string]string{
	"omixor_org_chains":       "edges",
	"omixor_channels":         "members",
	"omixor_channel_messages": "mentions",
	"omixor_docs":             "tags",
	"omixor_agents":           "doc_access、routines",
	"omixor_agent_tasks":      "links、notes",
	"omixor_incidents":        "data",
	"omixor_cabinet_layouts":  "layout",
}

// unmigratedDetail 還沒展開的列的清單(每張表最多 5 列;主鍵值、舊 jsonb 欄位)與一句修法,給失敗訊息用
func (p *PG) unmigratedDetail(c context.Context) string {
	var parts []string
	example := ""
	for _, s := range allSplitSpecs() {
		keys, err := queryKeys(c, p.pool, fmt.Sprintf(`select array[%s]::text[] from %s where split_at is null order by %s limit 5`,
			strings.Join(s.keys, ", "), s.table, strings.Join(s.keys, ", ")))
		if err != nil || len(keys) == 0 {
			continue
		}
		var n int
		_ = p.pool.QueryRow(c, fmt.Sprintf(`select count(*) from %s where split_at is null`, s.table)).Scan(&n)
		var ks []string
		for _, k := range keys {
			ks = append(ks, "("+strings.Join(k, "/")+")")
		}
		col := splitLegacyColumns[s.table]
		if col == "" {
			col = "舊 jsonb 欄位"
		}
		more := ""
		if n > len(keys) {
			more = fmt.Sprintf(" 等共 %d 列", n)
		}
		parts = append(parts, fmt.Sprintf("%s(主鍵 %s)%s%s 的舊欄位 %s", s.table, strings.Join(s.keys, "/"), strings.Join(ks, "、"), more, col))
		if example == "" {
			var conds []string
			for i, k := range s.keys {
				conds = append(conds, fmt.Sprintf("%s = '%s'", k, strings.ReplaceAll(keys[0][i], "'", "''")))
			}
			example = fmt.Sprintf("delete from %s where %s", s.table, strings.Join(conds, " and "))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ";") + "。若是資料壞了(不是合格的 JSON 結構):修復該列的舊 jsonb 欄位,或刪除該列(例:" + example + "),然後重啟"
}

// actorColumnsPrecheck 步驟 0:回傳略過的原因(空 = 可以做)
func (p *PG) actorColumnsPrecheck(c context.Context) string {
	var actors, aliases, principals bool
	if err := p.pool.QueryRow(c, `select to_regclass('omixor_actors') is not null, to_regclass('omixor_actor_aliases') is not null,
to_regclass('omixor_principals') is not null`).Scan(&actors, &aliases, &principals); err != nil {
		return "查不出 actor 表是否存在:" + err.Error()
	}
	if !actors || !aliases {
		return "找不到 omixor_actors 或 omixor_actor_aliases(actor 表遷移還沒做完;" + p.skipSummary() + ")"
	}
	if principals {
		warnf("actor 欄位遷移:資料庫有舊的身分登記層(omixor_principals;開發機殘留,正式庫沒有部署過)。它的 <欄位>_ref 產生欄位與外鍵可能擋住欄位轉換,請照 MIGRATIONS.md §7、§13 手動清掉")
	}
	// 前面的步驟被略過(等不到鎖、表太大、預算用完…)時,引擎要用的 omixor_agents.actor_id、omixor_users.actor_id 可能不在或沒補完:
	// 與其讓 17 欄各自噴「column g.actor_id does not exist」,先指到真因
	for _, t := range []struct{ step, table string }{{skipAgentActors, "omixor_agents"}, {skipUserActors, "omixor_users"}} {
		has, err := p.columnExists(c, t.table, "actor_id")
		if err != nil {
			return "查不出 " + t.table + ".actor_id 欄位是否存在:" + err.Error()
		}
		why := p.note.skipOf(t.step)
		if !has {
			if why == "" {
				why = "原因不明,請看啟動 log 的 [遷移警告]"
			}
			return fmt.Sprintf("%s 被略過、%s.actor_id 欄位不存在(%s);「誰」欄位要靠它對應角色與使用者。處理:%s", t.step, t.table, why, stepFix(why))
		}
		if why != "" {
			return fmt.Sprintf("%s 沒做完(%s),%s.actor_id 還有沒補的列;處理:%s", t.step, why, t.table, stepFix(why))
		}
	}
	n, err := p.UnmigratedRows()
	if err != nil {
		return "查不出還沒展開的列數:" + err.Error()
	}
	if n > 0 {
		msg := fmt.Sprintf("還有 %d 列沒有展開(%s),它們的「誰」還在舊 jsonb 裡,轉不到", n, unmigratedWhy)
		if d := p.unmigratedDetail(c); d != "" {
			msg += "。有 " + d
		}
		if sk := p.note.skipList(); len(sk) > 0 || p.budgetExpired() {
			msg += "。若這些列沒有壞、只是拆表遷移沒做完(" + strings.Join(sk, ";") + "):調大 OMIXOR_MIGRATE_BUDGET_MINUTES 或直接重啟,已展開的進度保留、會接著做"
		}
		return msg
	}
	return ""
}

// stepFix 依略過原因給處理方式
func stepFix(why string) string {
	switch {
	case strings.Contains(why, "等不到"):
		return "另一個實例正在做這個遷移,等它結束後重啟"
	case strings.Contains(why, "門檻"):
		return "調大 OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB 或設 0,然後重啟"
	case strings.Contains(why, "預算"):
		return "調大 OMIXOR_MIGRATE_BUDGET_MINUTES 或直接重啟,已做的進度保留、會接著做"
	}
	return "看啟動 log 的 [遷移警告],排除後重啟"
}

func (p *PG) skipSummary() string {
	if l := p.note.skipList(); len(l) > 0 {
		return strings.Join(l, ";")
	}
	return "沒有記錄到被略過的步驟,請看啟動 log"
}

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// seqScopeMax 該 scope 的「現有資料最大號碼」子查詢(用 $1 = org;訊息多用一個 $4 = 頻道 id)。只看 number > 0,
// 對得上 (org, number) 的部分唯一索引;非數字 id 的資料 number 是 0,不算
func seqScopeMax(scope string) (sql string, extra []any) {
	const f = `(select max(number) from %s where org = $1 and number > 0%s)`
	switch scope {
	case "issue":
		return fmt.Sprintf(f, "ops_issues", " and project = ''"), nil
	case "task":
		return fmt.Sprintf(f, "omixor_agent_tasks", ""), nil
	case "doc":
		return fmt.Sprintf(f, "omixor_docs", ""), nil
	case "channel":
		return fmt.Sprintf(f, "omixor_channels", ""), nil
	case "incident":
		return fmt.Sprintf(f, "omixor_incidents", ""), nil
	case "decision":
		return fmt.Sprintf(f, "omixor_decisions", ""), nil
	}
	if is, ok := strings.CutPrefix(scope, "item:"); ok { // 子項的序號欄位叫 seq(不是 number),每個 Issue 一組
		return `(select max(seq) from omixor_issue_items where org = $1 and issue_id = $4)`, []any{is}
	}
	if pr, ok := strings.CutPrefix(scope, "issue:"); ok {
		return fmt.Sprintf(f, "ops_issues", " and project = $4"), []any{pr}
	}
	if ch, ok := strings.CutPrefix(scope, "msg:"); ok {
		return fmt.Sprintf(f, "omixor_channel_messages", " and channel_id = $4"), []any{ch}
	}
	return "", nil
}

// seqNext 取下一個號碼:單句 upsert,同一個 (org, scope) 的並發建立在這一列的行鎖上排隊
// (鎖到交易結束,所以同一個交易裡接著讀 max(pos) 等也被這把鎖保護)。floor 是起始下限
// (事件的號碼從 model.IncidentNumberBase 往上),不需要時給 0。
// 自癒:新號碼 = max(記錄的最後號碼, 現有資料最大號碼, floor) + 1。滾動升級時舊版實例用 max+1 先發出的號碼、
// 手動 SQL、還原的資料,都不會讓之後的建立永遠撞唯一索引(max 走 (org, number) 索引)
func seqNext(c context.Context, tx pgx.Tx, org, scope string, floor int) (int, error) {
	mx, extra := seqScopeMax(scope)
	if mx == "" {
		mx = "0"
	}
	var n int
	args := append([]any{org, scope, floor}, extra...)
	err := tx.QueryRow(c, fmt.Sprintf(`insert into omixor_seq (org, scope, last) values ($1, $2, greatest($3::integer, coalesce(%[1]s, 0)) + 1)
		on conflict (org, scope) do update set last = greatest(omixor_seq.last, $3::integer, coalesce(%[1]s, 0)) + 1
		returning last`, mx), args...).Scan(&n)
	return n, err
}

// seqBump 匯入帶著指定號碼的資料時,把記錄的最後號碼拉到至少 n(不會調低);順便鎖住這一列
func seqBump(c context.Context, tx pgx.Tx, org, scope string, n int) error {
	_, err := tx.Exec(c, `insert into omixor_seq (org, scope, last) values ($1, $2, $3::integer)
		on conflict (org, scope) do update set last = greatest(omixor_seq.last, $3::integer)`, org, scope, n)
	return err
}

func msgScope(channelID string) string { return fmt.Sprintf("msg:%s", channelID) }

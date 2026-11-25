package store

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// schema_core.go 開機時判斷資料庫狀態:只看「核心表」。宿主程式(例如 console)可能在同一個資料庫先建了自己的 omixor_ 表,
// 所以不能用表名前綴判斷。核心表名單從建表語句派生(pgSchema、schemaSteps、issuesTableRenames),不用另外維護:
//   - 一個核心表都沒有 = 空資料庫(就算版本表已經蓋章:舊版程式在只有宿主表的資料庫上失敗時會留下版本 1),建完整結構。
//   - 有一部分、但版本 1 就該有的表不齊 = 壞掉的資料庫,報錯,不補建也不蓋章。
//   - 資料庫版本比程式新(部署回滾):新版可能改過表,不做完整性檢查;但一個核心表都沒有時也不建表,直接報錯。

var createTableNameRe = regexp.MustCompile(`(?i)\bcreate table (?:if not exists )?(\w+)`)

func tablesCreatedBy(sql string) []string {
	var names []string
	for _, m := range createTableNameRe.FindAllStringSubmatch(sql, -1) {
		names = append(names, m[1])
	}
	return names
}

// coreTableNames 全新安裝建出的核心表(版本表除外:它只是版本記錄,有它不代表有核心結構)
func coreTableNames() []string {
	return slices.DeleteFunc(tablesCreatedBy(pgSchema), func(n string) bool { return n == schemaVersionTable })
}

// baselineTableNames 版本 1 就該有的核心表 = 核心表扣掉結構步驟新增的表
func baselineTableNames() []string {
	added := map[string]bool{}
	for _, s := range schemaSteps {
		for _, n := range tablesCreatedBy(s.SQL) {
			added[n] = true
		}
	}
	return slices.DeleteFunc(coreTableNames(), func(n string) bool { return added[n] })
}

type schemaReadQ interface {
	schemaQ
	rowQ
}

// existingTables 目前 schema 裡的資料表(查 pg_catalog:不受帳號的表權限影響,看不到權限的表不會被誤認成不存在)
func existingTables(c context.Context, q schemaQ) (map[string]bool, error) {
	rows, err := q.Query(c, `select cl.relname::text from pg_class cl join pg_namespace n on n.oid = cl.relnamespace
		where n.nspname = current_schema() and cl.relkind in ('r', 'p')`)
	if err != nil {
		return nil, fmt.Errorf("查資料庫結構失敗:%w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

// coreSchemaEmpty 資料庫是否沒有任何核心表(可以建完整結構);結構不完整或不該動時回錯誤。規則見檔頭
func coreSchemaEmpty(c context.Context, q schemaReadQ) (bool, error) {
	have, err := existingTables(c, q)
	if err != nil {
		return false, err
	}
	// 改名步驟之前的資料庫用舊名
	for _, r := range issuesTableRenames {
		if have[r.old] {
			have[r.new] = true
		}
	}
	version, _, err := currentSchemaVersion(c, q)
	if err != nil {
		return false, err
	}
	newer := version > knownSchemaVersion(schemaSteps)
	if !slices.ContainsFunc(coreTableNames(), func(n string) bool { return have[n] }) {
		if newer {
			return false, fmt.Errorf("資料庫沒有任何核心資料表,但記錄的結構版本 %d 比這版程式已知的 %d 新;不建表,請改用新版程式或檢查資料庫", version, knownSchemaVersion(schemaSteps))
		}
		return true, nil
	}
	if newer {
		return false, nil
	}
	missing := slices.DeleteFunc(baselineTableNames(), func(n string) bool { return have[n] })
	if len(missing) > 0 {
		return false, fmt.Errorf("資料庫的核心資料表不完整(結構版本 %d,缺 %d 張:%s);不自動補建,請從備份還原或人工處理", version, len(missing), strings.Join(missing, "、"))
	}
	return false, nil
}

package store

import "strings"

// schema_rename_issues.go 結構異動步驟 3(rename_issues_tables):把最後兩張舊名的表改成 omixor_ 開頭。
//
//	ops_issues          → omixor_issues
//	ops_issue_comments  → omixor_issue_comments
//
// 連同名字裡帶舊表名的約束與索引一起改(見 issuesRenames)。其他表對它們的外鍵(omixor_issue_items、omixor_issue_keys、omixor_issue_commits、
// omixor_decisions、omixor_channels)由 PostgreSQL 在表改名時自動跟著指過去,外鍵名稱本身不含舊表名,不用動。
// 全新資料庫(pgSchema)一開始就是新名字,這個步驟只在舊資料庫上動作;步驟 2(commits)在舊資料庫上仍指向 ops_issues,所以要用 commitsStepSQL。
//
// 不向後相容(破例,見 schema_steps_test.go 的 stepRuleWhitelist):這一步之後,上一版程式(查 ops_issues)會壞。
// 部署要先停掉所有舊實例再啟動新版,不能滾動部署;回滾步驟見 omixor 文件櫃 DOC-25《docs/design-schema-rename.md》。

// legacyIssuesTable 改名前 Issue 表的名字(步驟 2 在還沒改名的資料庫上執行時要用)
const legacyIssuesTable = "ops_issues"

// commitsStepSQL 步驟 2 的 SQL:commitsSchemaSQL 的外鍵指向 omixor_issues,但步驟 2 執行時 Issue 表還叫 ops_issues
var commitsStepSQL = strings.ReplaceAll(commitsSchemaSQL, "omixor_issues", legacyIssuesTable)

// issuesRename 一個要改的物件:舊名、新名
type issuesRename struct{ old, new string }

// issuesRenames 表、約束、索引的改名清單(新名 = 全新安裝在 pgSchema / fkSpecs / ActorColumns 建出來的名字;
// 主鍵、唯一約束改名時 PostgreSQL 會連同背後的索引一起改,所以 *_pkey 不用再列在索引裡)
var (
	issuesTableRenames = []issuesRename{
		{"ops_issues", "omixor_issues"},
		{"ops_issue_comments", "omixor_issue_comments"},
	}
	// 約束:(新表名, 舊約束名, 新約束名)
	issuesConstraintRenames = []struct{ table, old, new string }{
		{"omixor_issues", "ops_issues_pkey", "omixor_issues_pkey"},
		{"omixor_issues", "ops_issues_priority_check", "omixor_issues_priority_check"},
		{"omixor_issues", "ops_issues_status_check", "omixor_issues_status_check"},
		{"omixor_issues", "fk_ops_issues_org", "fk_omixor_issues_org"},
		{"omixor_issues", "fk_ops_issues_created_by", "fk_omixor_issues_created_by"},
		{"omixor_issues", "fk_ops_issues_assignee", "fk_omixor_issues_assignee"},
		{"omixor_issues", "fk_ops_issues_deleted_by", "fk_omixor_issues_deleted_by"},
		{"omixor_issue_comments", "ops_issue_comments_pkey", "omixor_issue_comments_pkey"},
		{"omixor_issue_comments", "ops_issue_comments_org_issue_id_fkey", "omixor_issue_comments_org_issue_id_fkey"},
		{"omixor_issue_comments", "fk_ops_issue_comments_author", "fk_omixor_issue_comments_author"},
	}
	// 獨立索引(不是約束背後的)
	issuesIndexRenames = []issuesRename{
		{"ops_issues_org_key", "omixor_issues_org_key"},
		{"ops_issues_org_number", "omixor_issues_org_number"},
		{"ops_issues_org_pos", "omixor_issues_org_pos"},
	}
)

// renameIssuesSQL 步驟 3 的 SQL:一個 DO 區塊,每個物件「舊的在、新的不在」才改名;舊的不在(全新資料庫、已經改過)略過;
// 新舊同時存在就報錯(不覆蓋、不猜哪個才對,由人處理)。冪等
var renameIssuesSQL = buildRenameIssuesSQL()

func buildRenameIssuesSQL() string {
	var b strings.Builder
	b.WriteString("do $rename$\nbegin\n")
	for _, r := range issuesTableRenames {
		b.WriteString(renameBlock("table", r.old, r.new, relExists(r.old), relExists(r.new), "alter table "+r.old+" rename to "+r.new))
	}
	for _, r := range issuesConstraintRenames {
		b.WriteString(renameBlock("constraint", r.old, r.new, conExists(r.table, r.old), conExists(r.table, r.new),
			"alter table "+r.table+" rename constraint "+r.old+" to "+r.new))
	}
	for _, r := range issuesIndexRenames {
		b.WriteString(renameBlock("index", r.old, r.new, relExists(r.old), relExists(r.new), "alter index "+r.old+" rename to "+r.new))
	}
	b.WriteString("end\n$rename$;\n")
	return b.String()
}

// relExists 表或索引存在(它們共用一個名稱空間)
func relExists(name string) string { return "to_regclass('" + name + "') is not null" }

// conExists 表上有這個名字的約束
func conExists(table, name string) string {
	return "exists (select 1 from pg_constraint where conrelid = to_regclass('" + table + "') and conname = '" + name + "')"
}

// renameBlock 一個物件的 plpgsql 片段:舊的在才改;新舊都在就報錯
func renameBlock(kind, old, new, oldExists, newExists, stmt string) string {
	return "  if " + oldExists + " then\n" +
		"    if " + newExists + " then\n" +
		"      raise exception 'cannot rename " + kind + " " + old + " to " + new + ": both exist (resolve manually)';\n" +
		"    end if;\n" +
		"    " + stmt + ";\n" +
		"  end if;\n"
}

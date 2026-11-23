package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// ---- 外鍵與刪除規則(PG) ----
//
// fkSpecs 是建表之後加的 fk_* 外鍵(bootstrapSchema,pg_schema_boot.go;被參照的表要先建好);其餘外鍵在 pgSchema 的建表語句裡。
//
// 規則(括號是刪除規則):
//
//	omixor_agent_credentials (org, task_id)           → omixor_agent_tasks (org, id)   (cascade)
//	omixor_channel_messages、omixor_channel_reads (org, channel_id) → omixor_channels (org, id)   (cascade)
//	omixor_channels (org, issue_id)                   → ops_issues (org, id)           (set null (issue_id);update cascade)
//	omixor_docs (org, project_ref)                    → omixor_projects (org, id)      (restrict)
//	ops_issues (org, project_ref)                     → omixor_projects (org, id)      (restrict:專案底下還有 Issue(含垃圾桶)時不能刪)
//	omixor_cabinet_sections (org, project_ref)        → omixor_projects (org, id)      (cascade)
//	omixor_cabinet_tabs (org, project, section_id)    → omixor_cabinet_sections (org, project, id)   (cascade;update cascade)
//	omixor_agent_tasks (org, agent_id)                → omixor_agents (org, id)        (restrict:名下還有任務的角色不能刪,先轉移)
//	omixor_project_members (actor_id)                 → omixor_actors (id)             (cascade;(org, project) → omixor_projects 另在建表時就有,cascade)
//	omixor_decisions (created_by)、(decided_by)       → omixor_actors (id)             (restrict;(org, source_issue) → ops_issues 在建表時就有,set null (source_issue);update cascade)
//	omixor_issue_items (assignee)、(claimed_by)、(created_by) → omixor_actors (id)         (assignee、claimed_by:set null;created_by:restrict;(org, issue_id)、(org, depends_on)、(org, promoted_issue_id) 在建表時就有)
//	各表的 org                                        → omixor_orgs (id)               (cascade)
//
// 「空字串」的處理:
//   - 頻道的 issue_id:改成可為 NULL,沒綁 Issue 就是 NULL(讀寫時 '' ↔ NULL 在 pg.go 的 channelCols 與寫入用 nullif 轉換;
//     omixor_channels_issue 部分唯一索引的條件 issue_id <> '' 對 NULL 也不成立,所以同組織 Issue 不重複的規則不變)。
//   - 文件、文件櫃區塊的 project:'' = 未分專案,沒有對應的專案列,而且 project 是區塊與頁籤主鍵的一部分、不能是 NULL。
//     所以不改原欄位,另加「產生欄位」project_ref = nullif(project, '')(stored,由資料庫自己維護、程式不用管),
//     外鍵掛在 (org, project_ref) 上:'' 的 project_ref 是 NULL,外鍵(預設 MATCH SIMPLE)對含 NULL 的列不檢查。
//   - 頁籤 → 區塊的外鍵用真正的欄位 (org, project, section_id),'' 專案的區塊本來就有自己的列,照常檢查。
//
// 沒有加的外鍵:omixor_docs 的 section / tab 指向區塊表(文件櫃結構)。文件的 section 是字串參照,舊資料可以是結構裡已經沒有的區塊、
// 範本或舊格式的值,頁籤 path 也不保證存在;結構又是整份取代(SaveCabinetLayout),加了會讓存檔被舊文件擋住,所以維持字串參照。

// fkSpec 一條外鍵
type fkSpec struct {
	name  string
	table string
	// def 是 "foreign key (...) references ... on delete ..." 這段
	def string
}

// orgFKTables 有 org 欄位、要掛到 omixor_orgs 的表(omixor_org_members 本來就有)
var orgFKTables = []string{
	"ops_issues", "omixor_attachments", "omixor_agents", "omixor_agent_tasks", "omixor_agent_credentials",
	"omixor_cabinet_layouts", "omixor_cabinet_sections", "omixor_cabinet_tabs", "omixor_docs", "omixor_projects",
	"omixor_project_seq", "omixor_org_chains", "omixor_channels", "omixor_channel_messages", "omixor_channel_reads",
	"omixor_incidents", "omixor_decisions", "omixor_issue_items", "omixor_org_integrations", "omixor_org_system",
}

func fkSpecs() []fkSpec {
	l := []fkSpec{
		{"fk_agent_credentials_task", "omixor_agent_credentials",
			`foreign key (org, task_id) references omixor_agent_tasks (org, id) on delete cascade`},
		{"fk_channel_messages_channel", "omixor_channel_messages",
			`foreign key (org, channel_id) references omixor_channels (org, id) on delete cascade`},
		{"fk_channel_reads_channel", "omixor_channel_reads",
			`foreign key (org, channel_id) references omixor_channels (org, id) on delete cascade`},
		{"fk_channels_issue", "omixor_channels",
			`foreign key (org, issue_id) references ops_issues (org, id) on delete set null (issue_id) on update cascade`},
		{"fk_docs_project", "omixor_docs",
			`foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict`},
		{"fk_issues_project", "ops_issues",
			`foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict`},
		{"fk_cabinet_sections_project", "omixor_cabinet_sections",
			`foreign key (org, project_ref) references omixor_projects (org, id) on delete cascade`},
		{"fk_cabinet_tabs_section", "omixor_cabinet_tabs",
			`foreign key (org, project, section_id) references omixor_cabinet_sections (org, project, id) on delete cascade on update cascade`},
		{"fk_project_members_actor", "omixor_project_members",
			`foreign key (actor_id) references omixor_actors (id) on delete cascade`},
		// 待決定:建立者、處理人是 actor(source_issue 的外鍵在建表時就有)
		{"fk_decisions_created_by", "omixor_decisions",
			`foreign key (created_by) references omixor_actors (id) on delete restrict`},
		{"fk_decisions_decided_by", "omixor_decisions",
			`foreign key (decided_by) references omixor_actors (id) on delete restrict`},
		// Issue 子項:指派、領取、建立者是 actor(新表,最終結構;指派與領取者被刪清成 NULL,建立者 restrict;issue_id、depends_on、promoted_issue_id 的外鍵在建表時就有)
		{"fk_issue_items_assignee", "omixor_issue_items",
			`foreign key (assignee) references omixor_actors (id) on delete set null`},
		{"fk_issue_items_claimed_by", "omixor_issue_items",
			`foreign key (claimed_by) references omixor_actors (id) on delete set null`},
		{"fk_issue_items_created_by", "omixor_issue_items",
			`foreign key (created_by) references omixor_actors (id) on delete restrict`},
		{"fk_agent_tasks_agent", "omixor_agent_tasks",
			`foreign key (org, agent_id) references omixor_agents (org, id) on delete restrict`},
	}
	for _, t := range orgFKTables {
		l = append(l, fkSpec{"fk_" + t + "_org", t,
			`foreign key (org) references omixor_orgs (id) on delete cascade`})
	}
	// 22 條「誰」欄位的外鍵(ActorColumns,identity_fields.go)
	for _, col := range ActorColumns {
		l = append(l, fkSpec{col.FK, col.Table, fmt.Sprintf(`foreign key (%s) references omixor_actors (id) on delete restrict`, col.Target)})
	}
	return l
}

// ErrInUse 還被別的資料用著,不能刪(外鍵 restrict:名下還有任務的角色、還有文件的專案)
var ErrInUse = errors.New("still referenced")

// ErrBadRef 寫入的資料指向不存在的對象(外鍵不成立:任務的角色、文件的專案、頻道的 Issue、憑證的任務不存在)
var ErrBadRef = errors.New("referenced row does not exist")

// fkErr 寫入(insert、update)時的外鍵錯誤(23503)→ ErrBadRef(指向的對象不存在);刪除被 restrict 擋下要用 fkErrDelete。
// 不比對錯誤訊息文字(會隨 lc_messages 變成別的語言):由呼叫點決定是寫入還是刪除。PG 的 TableName 永遠是參照方(子)表,
// 分不出是哪一種,所以不靠它。不認得的外鍵(不在 fkSpecs,例如身分登記表的)才退回比對英文 Detail
func fkErr(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		if _, known := fkReferencing[pe.ConstraintName]; !known && strings.Contains(pe.Detail, "is still referenced") {
			return fmt.Errorf("%w: %s", ErrInUse, pe.Message)
		}
		return fmt.Errorf("%w: %s", ErrBadRef, pe.Message)
	}
	return err
}

// fkReferencing 補進來的外鍵名稱(fkSpecs)→ 參照方(子)表
var fkReferencing = func() map[string]string {
	m := map[string]string{}
	for _, f := range fkSpecs() {
		m[f.name] = f.table
	}
	return m
}()

// fkErrDelete 刪除語句(delete from omixor_agents / omixor_projects 等)的外鍵錯誤(23503):一律是「還被參照」→ ErrInUse
func fkErrDelete(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23503" {
		return fmt.Errorf("%w: %s", ErrInUse, pe.Message)
	}
	return err
}

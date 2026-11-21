package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pg_schema_boot.go 每次啟動的建表流程(bootstrapSchema)。
//
// 為什麼不把全部寫進 pgSchema 一次 Exec:pgSchema 是一個多敘述的隱含交易,裡面的 `alter table … add column if not exists`、
// `create index if not exists` 即使欄位、索引已經存在也要先拿表鎖(前者 ACCESS EXCLUSIVE、後者 SHARE);等鎖的時候,
// 排在後面的所有讀寫都被擋住(熱表會整個卡住),而且整段在 15 秒 context 內、沒有 lock_timeout、沒有重試。
// 所以 pgSchema 只留 `create table if not exists`(表已存在時不碰表鎖),欄位與索引改成:
// 先查 catalog(information_schema、to_regclass,都不拿目標表的鎖),缺才補,補的時候帶 lock_timeout 並重試(fkStep)。
// 結果(結構指紋)與原本一樣;欄位順序也維持原本的先後(各表內的相對順序不變)。

type schemaColumn struct{ table, column, def string }
type schemaIndex struct{ name, sql string }

// pgSchemaColumns 舊資料庫的表建出來時還沒有的欄位(依序補;全新資料庫也是建表之後補,所以最終結構相同)
var pgSchemaColumns = []schemaColumn{
	{"omixor_orgs", "symbol", "text not null default ''"},
	{"omixor_orgs", "color", "text not null default ''"},
	{"omixor_orgs", "note", "text not null default ''"},
	{"ops_issues", "assignee", "text"},
	{"ops_issues", "repo", "text not null default ''"},
	{"ops_issues", "project", "text not null default ''"},
	{"ops_issues", "key", "text not null default ''"},
	{"ops_issues", "version", "integer not null default 1"},
	{"ops_issues", "deleted_at", "timestamptz"},
	{"ops_issues", "deleted_by", "text"},
	{"omixor_agent_tasks", "locked_until", "timestamptz"},
	{"omixor_agent_tasks", "claimed_by", "text"},
	{"omixor_docs", "project", "text not null default ''"},
	{"omixor_cabinet_layouts", "project", "text not null default ''"},
	{"omixor_cabinet_layouts", "split_at", "timestamptz"},
	{"omixor_docs", "split_at", "timestamptz"},
	{"omixor_agents", "split_at", "timestamptz"},
	{"omixor_cabinet_layouts", "version", "bigint not null default 0"},
	{"omixor_projects", "key", "text not null default ''"},
	{"omixor_projects", "restricted", "boolean not null default false"},
	{"omixor_channels", "split_at", "timestamptz"},
	{"omixor_channel_messages", "split_at", "timestamptz"},
	{"omixor_org_chains", "split_at", "timestamptz"},
	{"omixor_audit", "source", "text not null default ''"},
	{"omixor_agent_tasks", "item_id", "text"}, // 綁定的 Issue 子項(omixor_issue_items.id;字串參照,不設外鍵,互不 cascade,回寫由 service 處理)
}

// pgSchemaIndexes 索引(缺才建)。依賴上面補的欄位,所以在欄位之後
var pgSchemaIndexes = []schemaIndex{
	{"omixor_audit_at", `create index if not exists omixor_audit_at on omixor_audit (at desc)`},
	{"ops_issues_org_pos", `create index if not exists ops_issues_org_pos on ops_issues (org, pos desc)`},
	{"ops_issues_org_key", `create unique index if not exists ops_issues_org_key on ops_issues (org, upper(key)) where key <> ''`},
	{"omixor_issue_keys_issue", `create index if not exists omixor_issue_keys_issue on omixor_issue_keys (org, issue_id)`},
	{"omixor_agent_tasks_agent", `create index if not exists omixor_agent_tasks_agent on omixor_agent_tasks (org, agent_id, status)`},
	{"omixor_agent_credentials_task", `create unique index if not exists omixor_agent_credentials_task on omixor_agent_credentials (org, task_id)`},
	{"omixor_docs_org_pos", `create index if not exists omixor_docs_org_pos on omixor_docs (org, pos desc)`},
	{"omixor_doc_tags_tag", `create index if not exists omixor_doc_tags_tag on omixor_doc_tags (org, tag)`},
	{"omixor_projects_org_key", `create unique index if not exists omixor_projects_org_key on omixor_projects (org, upper(key)) where key <> ''`},
	{"omixor_project_members_actor", `create index if not exists omixor_project_members_actor on omixor_project_members (org, actor_id)`},
	{"omixor_channels_issue", `create unique index if not exists omixor_channels_issue on omixor_channels (org, issue_id) where issue_id <> ''`},
	{"omixor_channel_messages_num", `create index if not exists omixor_channel_messages_num on omixor_channel_messages (org, channel_id, number)`},
	{"omixor_channel_members_member", `create index if not exists omixor_channel_members_member on omixor_channel_members (org, member)`},
	{"omixor_channel_mentions_member", `create index if not exists omixor_channel_mentions_member on omixor_channel_mentions (org, member)`},
	{"omixor_incidents_org_pos", `create index if not exists omixor_incidents_org_pos on omixor_incidents (org, pos desc)`},
	{"omixor_decisions_org_status", `create index if not exists omixor_decisions_org_status on omixor_decisions (org, status)`},
	{"omixor_decisions_org_issue", `create index if not exists omixor_decisions_org_issue on omixor_decisions (org, source_issue)`},
	{"omixor_issue_items_issue", `create index if not exists omixor_issue_items_issue on omixor_issue_items (org, issue_id)`},
	{"omixor_issue_items_assignee", `create index if not exists omixor_issue_items_assignee on omixor_issue_items (org, assignee)`},
}

// bootstrapSchema 建表、補欄位、補索引、文件櫃結構主鍵。全部「已經有就不拿鎖」:另一個連線持有熱表的 ACCESS EXCLUSIVE 鎖時,
// 結構完整的資料庫啟動不會被擋(補欄位、補索引才會等鎖,帶 lock_timeout 並重試)
func bootstrapSchema(parent context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(parent, pgSchema); err != nil {
		return fmt.Errorf("建表失敗:%w", err)
	}
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p := &PG{pool: pool}
	// 欄位:一次查出已有的
	have := map[string]bool{}
	rows, err := pool.Query(c, `select table_name || '.' || column_name from information_schema.columns where table_schema = current_schema()`)
	if err != nil {
		return fmt.Errorf("查欄位清單失敗:%w", err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		have[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range pgSchemaColumns {
		if have[col.table+"."+col.column] {
			continue
		}
		stmt := fmt.Sprintf(`alter table %s add column if not exists %s %s`, col.table, col.column, col.def)
		if err := p.fkStep(c, func(tx pgx.Tx) error { _, err := tx.Exec(c, stmt); return err }); err != nil {
			return fmt.Errorf("補欄位 %s.%s 失敗:%w", col.table, col.column, err)
		}
	}
	// 文件櫃結構:主鍵從 (org) 換成 (org, project)(已經換過就不動)
	var pkOK bool
	if err := pool.QueryRow(c, `select exists (select 1 from pg_index where indrelid = to_regclass('omixor_cabinet_layouts') and indisprimary and indnatts = 2)`).Scan(&pkOK); err != nil {
		return fmt.Errorf("查文件櫃結構主鍵失敗:%w", err)
	}
	if !pkOK {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, `alter table omixor_cabinet_layouts drop constraint if exists omixor_cabinet_layouts_pkey;
alter table omixor_cabinet_layouts add constraint omixor_cabinet_layouts_pkey primary key (org, project)`)
			return err
		}); err != nil {
			return fmt.Errorf("換文件櫃結構主鍵失敗:%w", err)
		}
	}
	for _, ix := range pgSchemaIndexes {
		var ok bool
		if err := pool.QueryRow(c, `select to_regclass($1) is not null`, ix.name).Scan(&ok); err != nil {
			return fmt.Errorf("查索引 %s 失敗:%w", ix.name, err)
		}
		if ok {
			continue
		}
		if err := p.fkStep(c, func(tx pgx.Tx) error { _, err := tx.Exec(c, ix.sql); return err }); err != nil {
			return fmt.Errorf("建索引 %s 失敗:%w", ix.name, err)
		}
	}
	return nil
}

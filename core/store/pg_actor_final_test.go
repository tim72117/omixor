package store

// 「最終結構」與「舊結構轉換」(ISS-36 C-2 共用地基):
//   - legacyActorStructure 把一個剛建好(空表、最終結構)的資料庫還原成舊結構(C-1 之前的樣子:22 個「誰」欄位是 NOT NULL default '' 的字串、
//     沒有外鍵、組織鏈欄位叫 from_node / to_node、事件有 actor_kind + actor_id、稽核是 user_id),給引擎測試灌舊資料用
//   - TestActorLegacyStructureConvertsToFinal:舊結構的資料庫跑完引擎之後的結構 == 全新建立的資料庫(結構指紋),而且是在有資料的情況下

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"omixor-core/model"
)

// legacyActorStructure 空資料庫:把「誰」欄位還原成舊結構(DDL 只對空表)
func legacyActorStructure(t *testing.T, p *PG) {
	t.Helper()
	for _, col := range ActorColumns {
		if n := pgCount(t, p, fmt.Sprintf(`select count(*) from %s`, col.Table)); n != 0 {
			t.Fatalf("%s 不是空的,不能還原成舊結構", col.Table)
		}
		pgExec(t, p, fmt.Sprintf(`alter table %s drop constraint if exists %s`, col.Table, col.FK))
		pgExec(t, p, fmt.Sprintf(`comment on column %s.%s is null`, col.Table, col.Target))
		switch col.Mode {
		case ActorColInPlace:
			if col.Nullable {
				pgExec(t, p, fmt.Sprintf(`alter table %[1]s alter column %[2]s set default '', alter column %[2]s set not null`, col.Table, col.Target))
			}
		case ActorColRename:
			pgExec(t, p, fmt.Sprintf(`alter table %s rename column %s to %s`, col.Table, col.Target, col.Column))
		case ActorColCopy:
			add := fmt.Sprintf(`alter table %s add column %s text not null default ''`, col.Table, col.Column)
			if col.KindColumn != "" {
				add += fmt.Sprintf(`, add column %s text not null default ''`, col.KindColumn)
			}
			pgExec(t, p, add)
			pgExec(t, p, fmt.Sprintf(`alter table %s drop column %s`, col.Table, col.Target))
		}
	}
	p.mig.conv.mu.Lock()
	p.mig.conv.done = nil
	p.mig.conv.mu.Unlock()
}

// structureFP 結構指紋的文字版(欄位、約束、索引;不含欄位註解與資料):比對兩個資料庫的結構,有差異時列出差在哪
func structureFP(t *testing.T, p *PG) map[string]string {
	t.Helper()
	out := map[string]string{}
	add := func(kind, q string) {
		rows, err := p.pool.Query(context.Background(), q)
		chk(t, err)
		defer rows.Close()
		for rows.Next() {
			var k, v string
			chk(t, rows.Scan(&k, &v))
			out[kind+" "+k] = v
		}
		chk(t, rows.Err())
	}
	add("col", `select format('%s.%s', c.relname, a.attname), format('%s notnull=%s default=%s gen=%s', format_type(a.atttypid, a.atttypmod), a.attnotnull, coalesce(pg_get_expr(d.adbin, d.adrelid), ''), a.attgenerated)
from pg_attribute a join pg_class c on c.oid = a.attrelid join pg_namespace n on n.oid = c.relnamespace
left join pg_attrdef d on d.adrelid = a.attrelid and d.adnum = a.attnum
where a.attnum > 0 and not a.attisdropped and c.relkind in ('r', 'p') and n.nspname = current_schema()`)
	add("con", `select format('%s.%s', c.relname, k.conname), format('%s validated=%s', pg_get_constraintdef(k.oid), k.convalidated)
from pg_constraint k join pg_class c on c.oid = k.conrelid join pg_namespace n on n.oid = c.relnamespace where n.nspname = current_schema()`)
	add("idx", `select format('%s.%s', tablename, indexname), indexdef from pg_indexes where schemaname = current_schema()`)
	return out
}

func diffFP(a, b map[string]string) []string {
	var d []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			d = append(d, "只有舊結構轉換後有:"+k+" = "+v)
		} else if v != w {
			d = append(d, fmt.Sprintf("不同:%s\n  轉換後:%s\n  全新建:%s", k, v, w))
		}
	}
	for k, v := range b {
		if _, ok := a[k]; !ok {
			d = append(d, "只有全新建立的有:"+k+" = "+v)
		}
	}
	return d
}

// 舊結構(有資料,各種舊值)的資料庫跑完引擎(NewPG 啟動遷移,不給任何開關)之後,結構與「全新建立的資料庫」完全一致
func TestActorLegacyStructureConvertsToFinal(t *testing.T) {
	SetMigrationLocalUser("tim")
	t.Cleanup(func() { SetMigrationLocalUser("") })
	fresh := fkPG(t, testPGBlank(t))
	want := structureFP(t, fresh)
	// 全新建立的結構:所有「誰」欄位都有外鍵、完成標記,而且沒有舊欄位
	for _, col := range ActorColumns {
		if ex, _ := fkValidated(t, fresh, col.Table, col.FK); !ex {
			t.Errorf("全新資料庫缺外鍵 %s", col.FK)
		}
		if !fresh.actorColumnConverted(context.Background(), col.Table, col.Target) {
			t.Errorf("全新資料庫的 %s.%s 沒有完成標記", col.Table, col.Target)
		}
		if col.Mode != ActorColInPlace {
			if ok, _ := fresh.columnExists(context.Background(), col.Table, col.Column); ok {
				t.Errorf("全新資料庫不該有舊欄位 %s.%s", col.Table, col.Column)
			}
		}
	}

	url := testPGBlank(t)
	p := fkPG(t, url)
	fx := &actorFx{p: p, hasSplit: map[string]bool{}}
	mkOrgs(t, p, "o")
	saveU(t, p, "u_a", "a@x", "A")
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", Active: true, CreatedAt: model.Now()}))
	legacyActorStructure(t, p)
	rows, err := p.pool.Query(context.Background(), `select table_name from information_schema.columns where column_name = 'split_at' and table_schema = current_schema()`)
	chk(t, err)
	for rows.Next() {
		var s string
		chk(t, rows.Scan(&s))
		fx.hasSplit[s] = true
	}
	rows.Close()
	// 每一欄灌一筆舊字串(含空字串、對不上的、角色、使用者)
	for _, col := range ActorColumns {
		for i, v := range []string{"a@x", "user:a@x", "agent:dev", "mcp", "routine", "冒名", ""} {
			fx.seedValue(t, col, "o", i, v, v)
		}
	}
	p.Close()

	conv := reopenURL(t, p, url)
	if rep, err := conv.runActorColumns(); err != nil || len(rep.Skipped) != 0 || rep.Partial {
		t.Fatalf("重跑要什麼都不做:%+v %v", rep, err)
	}
	got := structureFP(t, conv)
	if d := diffFP(got, want); len(d) > 0 {
		t.Fatalf("舊結構轉換後和全新建立的結構不一致(%d 處):\n%s", len(d), strings.Join(d, "\n"))
	}
	// 引擎轉完之後,資料也都對得上 actor(沒有孤兒)
	for _, col := range ActorColumns {
		var bad int64
		chk(t, conv.pool.QueryRow(context.Background(), fmt.Sprintf(`select count(*) from %[1]s x where x.%[2]s is not null and not exists (select 1 from omixor_actors a where a.id = x.%[2]s)`,
			col.Table, col.Target)).Scan(&bad))
		if bad != 0 {
			t.Errorf("%s.%s 有 %d 筆孤兒", col.Table, col.Target, bad)
		}
	}
}

// C-1 的開關開啟時轉換過的資料庫(完成標記與外鍵都在,但事件的 actor_kind、actor_id 與稽核的 user_id 舊欄位還在):
// 引擎重跑時 drop 舊來源欄位,結構與全新建立的一致
func TestActorColumnsDropsLeftoverCopySource(t *testing.T) {
	fresh := fkPG(t, testPGBlank(t))
	want := structureFP(t, fresh)
	p := fkPG(t, testPGBlank(t))
	pgExec(t, p, `alter table omixor_incident_events add column actor_kind text not null default '', add column actor_id text not null default ''`)
	pgExec(t, p, `alter table omixor_audit add column user_id text not null default ''`)
	rep, err := p.runActorColumns()
	chk(t, err)
	if len(rep.Skipped) != 0 {
		t.Fatalf("不該略過:%+v", rep)
	}
	if d := diffFP(structureFP(t, p), want); len(d) > 0 {
		t.Fatalf("舊來源欄位要 drop,結構和全新建立的一致:\n%s", strings.Join(d, "\n"))
	}
}

package store

// 啟動遷移的加固:大表不自動重寫、維護窗口腳本(舊結構、逐句執行)、
// 鎖等待與釋放、背景補外鍵、整體預算用完只警告;「誰」的欄位清單(給 actor 遷移用)

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

// 「誰」的欄位清單(給 actor 遷移用)涵蓋所有身分欄位:每個表與欄位都存在、型別是 text;actor 的種類欄位也在。
// 同時確認身分登記層沒有部署:沒有登記表、身分函式、_ref 欄位(project_ref 除外)與 fk_p_ 外鍵
func TestIdentityFieldsExist(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	if len(IdentityFields) != 19 {
		t.Fatal("清單要有 19 個欄位", len(IdentityFields))
	}
	seen := map[string]bool{}
	for _, f := range IdentityFields {
		k := f.Table + "." + f.Column
		if seen[k] {
			t.Fatal("重複", k)
		}
		seen[k] = true
		if f.Form == IdentityActor && f.KindColumn == "" {
			t.Fatal("actor 要有種類欄位", k)
		}
		// 最終結構:IdentityFields 記的是舊欄位,現在存在的是 ActorColumns 的 Target(同欄位名,或改名、合併後的新欄位)
		tgt := ""
		for _, ac := range ActorColumns {
			if ac.Table == f.Table && ac.Column == f.Column {
				tgt = ac.Target
			}
		}
		if tgt == "" {
			t.Errorf("%s 沒有對應的 ActorColumns", k)
			continue
		}
		if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2 and data_type = 'text'`, f.Table, tgt); n != 1 {
			t.Errorf("%s.%s 不存在或不是 text", f.Table, tgt)
		}
	}
	if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and column_name like '%\_ref' and column_name <> 'project_ref'`); n != 0 {
		t.Fatal("不該有身分 _ref 欄位", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_constraint where conname like 'fk\_p\_%'`); n != 0 {
		t.Fatal("不該有 fk_p_ 外鍵", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_class where relname in ('omixor_principals', 'omixor_principal_meta') and relnamespace = current_schema()::regnamespace`); n != 0 {
		t.Fatal("不該有身分登記表", n)
	}
	if n := pgCount(t, p, `select count(*) from pg_proc where (proname like 'omixor\_principal\_%' or proname = 'omixor_actor_node') and pronamespace = current_schema()::regnamespace`); n != 0 {
		t.Fatal("不該有身分函式", n)
	}
}

// ---- 大表保護 ----

// ---- 維護窗口腳本 ----

// ---- 鎖等待、重試 ----

func bg() context.Context { return context.Background() }

// ---- actor 欄位遷移的守門(ISS-36 C-1;DOC-13 §1.3、§7 第 10 點)----

// ActorColumns 的每個欄位都在資料庫裡(型別 text);Copy 模式的新欄位在轉換前還不存在。
// 反過來:資料庫裡名稱像「誰」的欄位(created_by、author、assignee、member、updated_by、deleted_by、commander、claimed_by、from_node、to_node、actor_*、user_id)
// 都必須登記在 ActorColumns(含改名後、複製出來的新欄位與事件的 actor_kind),或明確列在排除名單。以後新增這類欄位忘了登記,這個測試會失敗。
func TestActorColumnsGuard(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	registered := map[string]bool{}
	for _, c := range ActorColumns {
		registered[c.Table+"."+c.Column] = true
		registered[c.Table+"."+c.Target] = true
		if c.KindColumn != "" {
			registered[c.Table+"."+c.KindColumn] = true
		}
		if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2 and data_type = 'text'`, c.Table, c.Target); n != 1 {
			t.Errorf("%s.%s(最終結構)不存在或不是 text", c.Table, c.Target)
		}
		if c.Mode != ActorColInPlace { // 改名、複製的欄位:最終結構沒有舊欄位
			cols := []string{c.Column}
			if c.KindColumn != "" {
				cols = append(cols, c.KindColumn)
			}
			for _, oc := range cols {
				if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2`, c.Table, oc); n != 0 {
					t.Errorf("最終結構不該有舊欄位 %s.%s", c.Table, oc)
				}
			}
		}
	}
	for _, f := range NewTableActorFields { // ISS-36 之後的新表:直接是最終結構(actor id + 外鍵),登記在 NewTableActorFields,不走轉換引擎
		registered[f.Table+"."+f.Column] = true
	}
	// 排除名單:不是「誰」(或不由這個引擎轉)的同名欄位
	excluded := map[string]string{
		"omixor_tokens.user_id":         "使用者 id(u_…,D11 不改寫),不是 actor id",
		"omixor_org_members.user_id":    "使用者 id(u_…,D11 不改寫),不是 actor id",
		"omixor_oauth_tokens.user_id":   "使用者 id(u_…,同 omixor_tokens),不是 actor id;OAuth 授權碼 / token 的所屬使用者",
		"omixor_users.actor_id":         "使用者指向自己的 actor(B-2b)",
		"omixor_agents.actor_id":        "角色指向自己的 actor(B-2c)",
		"omixor_actor_aliases.actor_id": "別名表指向 actor(C-1)",
	}
	rows, err := p.pool.Query(bg(), `select table_name || '.' || column_name from information_schema.columns
where table_schema = current_schema() and column_name ~ '^(created_by|author|assignee|member|updated_by|deleted_by|commander|claimed_by|from_node|to_node|actor_.*|user_id)$' order by 1`)
	chk(t, err)
	defer rows.Close()
	for rows.Next() {
		var k string
		chk(t, rows.Scan(&k))
		if !registered[k] && excluded[k] == "" {
			t.Errorf("%s 看起來是「誰」的欄位,卻沒登記在 ActorColumns(identity_fields.go),也不在排除名單", k)
		}
	}
	chk(t, rows.Err())
	// 排除名單裡的都還存在(欄位被移除就要一併拿掉排除項)
	for k := range excluded {
		parts := strings.SplitN(k, ".", 2)
		if n := pgCount(t, p, `select count(*) from information_schema.columns where table_schema = current_schema() and table_name = $1 and column_name = $2`, parts[0], parts[1]); n != 1 {
			t.Errorf("排除名單的 %s 不存在了,請拿掉", k)
		}
	}
}

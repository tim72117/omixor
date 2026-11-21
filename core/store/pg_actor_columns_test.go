package store

// 「誰」欄位遷移引擎(ISS-36 C-1;DOC-13 §7):22 欄 × 各種舊值樣態、重跑冪等、預算用完續做、孤兒不加外鍵、主鍵衝突去重、
// 空字串轉 NULL、sys_legacy 穩定性、開關關閉時結構不變、開關開啟時的結構、大表跳過、部分已轉換、前置檢查

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"omixor-core/model"
)

// ---- 測試工具 ----

func withActorBatch(t *testing.T, n int) {
	t.Helper()
	old := actorColumnBatch
	actorColumnBatch = n
	t.Cleanup(func() { actorColumnBatch = old; actorColumnsBatchHook = nil })
}

// actorFx 舊值樣態的固定資料:使用者 a@x、tim@example.com、本機使用者 tim;組織 o 的角色 dev(現役)、old(已刪、退役)
type actorFx struct {
	p                         *PG
	userA, userTim, userLocal string
	agentDev, agentOld        string
	hasSplit                  map[string]bool
	seq                       int
}

func newActorFx(t *testing.T) *actorFx {
	t.Helper()
	SetMigrationLocalUser("tim") // console 本機模式的 a.Me;別名 omixor-console、ops-web 內建
	t.Cleanup(func() { SetMigrationLocalUser("") })
	p := fkPG(t, testPGBlank(t))
	fx := &actorFx{p: p, hasSplit: map[string]bool{}}
	mkOrgs(t, p, "o")
	for _, u := range [][3]string{{"u_a", "a@x", "A"}, {"u_tim", "tim@example.com", "Tim"}, {"u_local", "tim", "本機 tim"}} {
		saveU(t, p, u[0], u[1], u[2])
	}
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", Active: true, CreatedAt: model.Now()}))
	chk(t, p.SaveAgent("o", model.Agent{ID: "old", Name: "Old", Active: true, CreatedAt: model.Now()}))
	chk(t, p.DeleteAgent("o", "old"))
	fx.userA = userActorOf(t, p, "u_a")
	fx.userTim = userActorOf(t, p, "u_tim")
	fx.userLocal = userActorOf(t, p, "u_local")
	g, err := p.GetAgent("o", "dev")
	chk(t, err)
	fx.agentDev = g.ActorID
	chk(t, p.pool.QueryRow(bg(), `select id from omixor_actors where kind = 'agent' and org = 'o' and handle = 'old'`).Scan(&fx.agentOld))
	rows, err := p.pool.Query(bg(), `select table_name from information_schema.columns where column_name = 'split_at' and table_schema = current_schema()`)
	chk(t, err)
	for rows.Next() {
		var s string
		chk(t, rows.Scan(&s))
		fx.hasSplit[s] = true
	}
	rows.Close()
	legacyActorStructure(t, p) // 全新資料庫是最終結構;把「誰」欄位還原成舊結構再灌舊值
	return fx
}

func userActorOf(t *testing.T, p *PG, userID string) string {
	t.Helper()
	u, err := p.UserByID(userID)
	chk(t, err)
	if u.ActorID == "" {
		t.Fatal("使用者沒有 actor", userID)
	}
	return u.ActorID
}

// handleActor 依 handle 找使用者 actor(未註冊使用者在遷移後才有)
func (fx *actorFx) handleActor(t *testing.T, handle string) string {
	t.Helper()
	var id string
	if err := fx.p.pool.QueryRow(bg(), `select id from omixor_actors where kind = 'user' and handle = $1`, handle).Scan(&id); err != nil {
		t.Fatalf("找不到 handle %q 的使用者 actor:%v", handle, err)
	}
	return id
}

// insert 寫一列(欄位、值成對);有 split_at 的表自動補(不然 UnmigratedRows 不為 0,引擎會略過)
func (fx *actorFx) insert(t *testing.T, table string, kv ...any) {
	t.Helper()
	cols, ph := []string{}, []string{}
	var args []any
	for i := 0; i < len(kv); i += 2 {
		cols = append(cols, kv[i].(string))
		args = append(args, kv[i+1])
		ph = append(ph, fmt.Sprintf("$%d", len(args)))
	}
	if fx.hasSplit[table] {
		cols, ph = append(cols, "split_at"), append(ph, "now()")
	}
	if _, err := fx.p.pool.Exec(bg(), fmt.Sprintf(`insert into %s (%s) values (%s) on conflict do nothing`, table, strings.Join(cols, ","), strings.Join(ph, ",")), args...); err != nil {
		t.Fatal(table, kv, err)
	}
}

// seedValue 在欄位 col 寫一列,值是 v1(事件時間軸是 kind = v1、id = v2;稽核的 v2 不用),回傳讀回 col.Target 的 SQL(只回一列)
func (fx *actorFx) seedValue(t *testing.T, col ActorColumn, org string, i int, v1, v2 string) string {
	t.Helper()
	now := time.Now()
	fx.seq++
	seq := fx.seq
	id := fmt.Sprintf("%s-%d", col.Column, i) // 同一張表的不同欄位(ops_issues 三欄…)id 不撞
	const sys = "sys_omixor"                  // 其他「誰」欄位固定放這個(已經是 actor id,不會產生額外舊值)
	issue := func(extra ...any) {
		fx.insert(t, "ops_issues", append([]any{"org", org, "id", id, "number", seq, "pos", seq, "title", id, "status", "open", "priority", "normal", "created_at", now, "updated_at", now}, extra...)...)
	}
	channel := func(by string) {
		fx.insert(t, "omixor_channels", "org", org, "id", id, "number", seq, "name", id, "created_at", now, "updated_at", now, "created_by", by)
	}
	message := func(author string) {
		fx.insert(t, "omixor_channel_messages", "org", org, "id", "m1", "channel_id", id, "number", 1, "author", author, "text", "x", "at", now)
	}
	incident := func(extra ...any) {
		fx.insert(t, "omixor_incidents", append([]any{"org", org, "id", id, "number", seq, "pos", seq, "status", "open", "started_at", now, "data", "{}"}, extra...)...)
	}
	task := func(extra ...any) {
		fx.insert(t, "omixor_agent_tasks", append([]any{"org", org, "id", id, "number", seq, "agent_id", "dev", "title", "t", "status", "queued", "created_at", now, "updated_at", now}, extra...)...)
	}
	where := fmt.Sprintf(`org = '%s' and id = '%s'`, org, id)
	if col.Table == "ops_issues" { // ops_issues.id 會被 Issue key 遷移改寫(啟動時),用不變的 number 讀回
		where = fmt.Sprintf(`org = '%s' and title = '%s'`, org, id)
	}
	switch col.Key() {
	case "ops_issues.created_by":
		issue("created_by", v1)
	case "ops_issues.assignee":
		issue("created_by", sys, "assignee", v1)
	case "ops_issues.deleted_by":
		issue("created_by", sys, "deleted_by", v1)
	case "ops_issue_comments.author":
		issue("created_by", sys)
		fx.insert(t, "ops_issue_comments", "org", org, "issue_id", id, "pos", 0, "id", "c1", "author", v1, "text", "x", "at", now)
		where = fmt.Sprintf(`org = '%s' and issue_id = (select id from ops_issues where org = '%s' and title = '%s')`, org, org, id)
	case "omixor_docs.author":
		fx.insert(t, "omixor_docs", "org", org, "id", id, "number", seq, "pos", seq, "title", "t", "section", "s", "created_at", now, "updated_at", now, "author", v1)
	case "omixor_agent_tasks.created_by":
		task("created_by", v1)
	case "omixor_agent_tasks.claimed_by":
		task("created_by", sys, "claimed_by", v1)
	case "omixor_agent_task_notes.author":
		task("created_by", sys)
		fx.insert(t, "omixor_agent_task_notes", "org", org, "task_id", id, "pos", 0, "id", "n1", "author", v1, "text", "x", "at", now)
		where = fmt.Sprintf(`org = '%s' and task_id = '%s'`, org, id)
	case "omixor_channels.created_by":
		channel(v1)
	case "omixor_channel_members.member":
		channel(sys)
		fx.insert(t, "omixor_channel_members", "org", org, "channel_id", id, "member", v1, "pos", 0)
		where = fmt.Sprintf(`org = '%s' and channel_id = '%s'`, org, id)
	case "omixor_channel_messages.author":
		channel(sys)
		message(v1)
		where = fmt.Sprintf(`org = '%s' and channel_id = '%s'`, org, id)
	case "omixor_channel_mentions.member":
		channel(sys)
		message(sys)
		fx.insert(t, "omixor_channel_mentions", "org", org, "channel_id", id, "message_id", "m1", "member", v1, "pos", 0)
		where = fmt.Sprintf(`org = '%s' and channel_id = '%s'`, org, id)
	case "omixor_channel_reads.member":
		channel(sys)
		fx.insert(t, "omixor_channel_reads", "org", org, "channel_id", id, "member", v1, "last_read", 0, "at", now)
		where = fmt.Sprintf(`org = '%s' and channel_id = '%s'`, org, id)
	case "omixor_org_chain_edges.from_node", "omixor_org_chain_edges.to_node":
		fx.insert(t, "omixor_org_chains", "org", org)
		from, to := v1, sys
		if col.Column == "to_node" {
			from, to = sys, v1
		}
		fx.insert(t, "omixor_org_chain_edges", "org", org, "pos", seq, "from_node", from, "to_node", to)
		where = fmt.Sprintf(`org = '%s' and pos = %d`, org, seq)
	case "omixor_incidents.commander":
		incident("created_by", sys, "commander", v1)
	case "omixor_incidents.created_by":
		incident("created_by", v1)
	case "omixor_incident_events.actor_id":
		incident("created_by", sys)
		fx.insert(t, "omixor_incident_events", "org", org, "incident_id", id, "pos", 0, "id", "e1", "at", now, "actor_kind", v1, "actor_id", v2, "kind", "note", "text", "x")
		where = fmt.Sprintf(`org = '%s' and incident_id = '%s'`, org, id)
	case "omixor_org_integrations.updated_by":
		fx.insert(t, "omixor_org_integrations", "org", org, "kind", id, "data", "{}", "updated_at", now, "updated_by", v1)
		where = fmt.Sprintf(`org = '%s' and kind = '%s'`, org, id)
	case "omixor_org_system.updated_by":
		fx.insert(t, "omixor_org_system", "org", org, "features", "{}", "updated_at", now, "updated_by", v1)
		where = fmt.Sprintf(`org = '%s'`, org)
	case "omixor_server_features.updated_by":
		fx.insert(t, "omixor_server_features", "id", "server", "features", "{}", "updated_at", now, "updated_by", v1)
		where = `id = 'server'`
	case "omixor_audit.user_id":
		var n int64
		chk(t, fx.p.pool.QueryRow(bg(), `insert into omixor_audit (user_id, email, action, org) values ($1, 'e', 'x', $2) returning id`, v1, org).Scan(&n))
		where = fmt.Sprintf(`id = %d`, n)
	default:
		t.Fatal("seedValue 不認得", col.Key())
	}
	return fmt.Sprintf(`select %s from %s where %s`, col.Target, col.Table, where)
}

// pat 一個舊值樣態與它該對到哪裡(want 回 actor id 或 "NULL")
type pat struct {
	v1, v2 string
	want   func(fx *actorFx, t *testing.T, org string, col ActorColumn) string
}

func lit(s string) func(*actorFx, *testing.T, string, ActorColumn) string {
	return func(*actorFx, *testing.T, string, ActorColumn) string { return s }
}

func node(raw string) string { return LegacyActorID(model.PrincipalNode(strings.TrimSpace(raw))) }

// ghostAgent 對不上角色的 agent:<id>:有組織的欄位 sys_legacy 帶組織(不同組織同名的已刪角色不併),沒有組織的欄位(omixor_server_features)不帶
func ghostAgent(raw string) func(*actorFx, *testing.T, string, ActorColumn) string {
	return func(fx *actorFx, t *testing.T, org string, col ActorColumn) string {
		if col.HasOrg {
			return LegacyAgentActorID(org, model.PrincipalNode(raw))
		}
		return node(raw)
	}
}

// freePatterns 一般「誰」欄位的樣態(DOC-13 §7)
func freePatterns() []pat {
	user := func(f func(*actorFx) string) func(*actorFx, *testing.T, string, ActorColumn) string {
		return func(fx *actorFx, t *testing.T, org string, col ActorColumn) string { return f(fx) }
	}
	a := user(func(fx *actorFx) string { return fx.userA })
	tim := user(func(fx *actorFx) string { return fx.userTim })
	local := user(func(fx *actorFx) string { return fx.userLocal })
	byHandle := func(h string) func(*actorFx, *testing.T, string, ActorColumn) string {
		return func(fx *actorFx, t *testing.T, org string, col ActorColumn) string { return fx.handleActor(t, h) }
	}
	agent := func(f func(*actorFx) string, raw string) func(*actorFx, *testing.T, string, ActorColumn) string {
		return func(fx *actorFx, t *testing.T, org string, col ActorColumn) string {
			if org != "o" { // 角色只在組織 o;別的組織對不上 → sys_legacy
				return ghostAgent(raw)(fx, t, org, col)
			}
			return f(fx)
		}
	}
	empty := func(fx *actorFx, t *testing.T, org string, col ActorColumn) string {
		if col.Nullable {
			return "NULL"
		}
		return "sys_legacy_unknown"
	}
	legacy := func(raw string) func(*actorFx, *testing.T, string, ActorColumn) string { return lit(node(raw)) }
	l := []pat{
		{"", "", empty}, {"   ", "", empty},
		{"a@x", "", a}, {"user:a@x", "", a}, {"A@X", "", a}, {" a@x ", "", a}, {"user:A@x", "", a},
		{"tim@example.com", "", tim}, {"user:Tim@Example.com", "", tim},
		{"alice@x", "", byHandle("alice@x")}, {"user:alice@x", "", byHandle("alice@x")}, {"boss@x", "", byHandle("boss@x")}, {"user:root@x", "", byHandle("root@x")},
		{"user:tim", "", local}, {"tim", "", local}, {"ops-web", "", local}, {"omixor-console", "", local}, {"local:foo", "", local}, {"local:mcp", "", local}, {"mcp", "", local}, {"user:ops-web", "", local},
		{"routine", "", lit("sys_routine")}, {"chain", "", lit("sys_chain")}, {"omixor", "", lit("sys_omixor")}, {"system", "", lit("sys_omixor")},
		{"agent:dev", "", agent(func(fx *actorFx) string { return fx.agentDev }, "agent:dev")},
		{"agent:old", "", agent(func(fx *actorFx) string { return fx.agentOld }, "agent:old")},
		{"agent:ghost", "", ghostAgent("agent:ghost")}, {"agent:", "", legacy("agent:")},
		{"冒名", "", legacy("冒名")}, {"ops", "", legacy("ops")}, {"x", "", legacy("x")}, {"bob", "", legacy("bob")}, {"ops-web-2", "", legacy("ops-web-2")},
		{"user:alice@x>agent:cto>目標", "", legacy("user:alice@x>agent:cto>目標")}, {"user:stranger@x>agent:cto", "", legacy("user:stranger@x>agent:cto")},
	}
	return l
}

// patternsFor 各欄位用的樣態
func patternsFor(col ActorColumn) []pat {
	switch col.Key() {
	case "ops_issues.assignee": // 純角色 id
		return []pat{
			{"", "", lit("NULL")}, {"dev", "", func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return fx.agentDev }},
			{"old", "", func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return fx.agentOld }},
			{"ghost", "", ghostAgent("agent:ghost")},
		}
	case "omixor_incident_events.actor_id": // actor_kind + actor_id
		agent := func(f func(*actorFx) string) func(*actorFx, *testing.T, string, ActorColumn) string {
			return func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return f(fx) }
		}
		return []pat{
			{"system", "", lit("sys_omixor")}, {"system", "whatever", lit("sys_omixor")},
			{"ai", "dev", agent(func(fx *actorFx) string { return fx.agentDev })}, {"ai", "old", agent(func(fx *actorFx) string { return fx.agentOld })},
			{"ai", "mcp", agent(func(fx *actorFx) string { return fx.userLocal })}, {"ai", "auto-9", ghostAgent("agent:auto-9")},
			{"person", "a@x", agent(func(fx *actorFx) string { return fx.userA })}, {"person", "tim", agent(func(fx *actorFx) string { return fx.userLocal })},
			{"person", "alice@x", func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return fx.handleActor(t, "alice@x") }},
			{"", "", lit("NULL")}, {"ai", "", lit("NULL")},
		}
	case "omixor_audit.user_id": // omixor_users.id、agent:<id>
		return []pat{
			{"u_a", "", func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return fx.userA }},
			{"u_tim", "", func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return fx.userTim }},
			{"agent:dev", "", func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return fx.agentDev }},
			{"tim@example.com", "", func(fx *actorFx, t *testing.T, org string, c ActorColumn) string { return fx.userTim }},
			{"u_gone", "", lit(node("u_gone"))}, {"", "", lit("NULL")},
		}
	case "omixor_server_features.updated_by": // 沒有組織:只能對使用者與系統(單列表,只放一個值)
		return []pat{{"agent:dev", "", ghostAgent("agent:dev")}}
	}
	return freePatterns()
}

// rowOrgs 每個樣態用哪個組織:org_system 一個組織只有一列,所以每個樣態自己一個組織;其餘都在 o
func rowOrg(t *testing.T, fx *actorFx, col ActorColumn, i int) string {
	if col.Table == "omixor_org_system" {
		org := fmt.Sprintf("os%d", i)
		mkOrgs(t, fx.p, org)
		return org
	}
	return "o"
}

type seeded struct {
	col  ActorColumn
	org  string
	pat  pat
	read string
}

// seedAll 每個欄位 × 每個樣態一列
func seedAll(t *testing.T, fx *actorFx) []seeded {
	t.Helper()
	var out []seeded
	for _, col := range ActorColumns {
		for i, pt := range patternsFor(col) {
			org := rowOrg(t, fx, col, i)
			out = append(out, seeded{col, org, pt, fx.seedValue(t, col, org, i, pt.v1, pt.v2)})
		}
	}
	return out
}

func readActor(t *testing.T, p *PG, q string) string {
	t.Helper()
	var s *string
	if err := p.pool.QueryRow(bg(), q).Scan(&s); err != nil {
		t.Fatal(q, err)
	}
	if s == nil {
		return "NULL"
	}
	return *s
}

func checkSeeded(t *testing.T, fx *actorFx, rows []seeded) {
	t.Helper()
	bad := 0
	for _, r := range rows {
		want := r.pat.want(fx, t, r.org, r.col)
		if got := readActor(t, fx.p, r.read); got != want {
			t.Errorf("%s:%q/%q → %q,想要 %q", r.col.Key(), r.pat.v1, r.pat.v2, got, want)
			if bad++; bad > 25 {
				t.Fatal("錯太多了")
			}
		}
	}
}

func runCols(t *testing.T, p *PG) *ActorColumnsReport {
	t.Helper()
	rep, err := p.runActorColumns()
	chk(t, err)
	return rep
}

func wantAllConverted(t *testing.T, rep *ActorColumnsReport) {
	t.Helper()
	if len(rep.Converted) != len(ActorColumns) || len(rep.Skipped) != 0 || rep.Partial {
		t.Fatalf("要 %d 欄都轉換完:converted=%d skipped=%v partial=%v", len(ActorColumns), len(rep.Converted), rep.Skipped, rep.Partial)
	}
}

// ---- 清單 ----

// ActorColumns 是 IdentityFields 的擴充:19 個欄位都在、共 22 欄、外鍵名稱唯一且不超過 63 字元、表驅動欄位資訊完整
func TestActorColumnsList(t *testing.T) {
	if len(ActorColumns) != 22 {
		t.Fatal("要 22 欄", len(ActorColumns))
	}
	in := map[string]ActorColumn{}
	fks := map[string]bool{}
	for _, c := range ActorColumns {
		if _, dup := in[c.Key()]; dup {
			t.Fatal("重複", c.Key())
		}
		in[c.Key()] = c
		if fks[c.FK] || len(c.FK) > 63 || !strings.HasPrefix(c.FK, "fk_") {
			t.Fatal("外鍵名稱", c.FK)
		}
		fks[c.FK] = true
		if c.Mode == ActorColInPlace && c.Target != c.Column {
			t.Fatal("InPlace 的 Target 要等於原欄位", c.Key())
		}
		if c.Mode != ActorColInPlace && c.Target == c.Column {
			t.Fatal("改名、複製要有新欄位名", c.Key())
		}
		if (len(c.KeyCols) > 0) != (c.Keep != "") || (len(c.KeyCols) > 0 && c.Nullable) {
			t.Fatal("主鍵欄位的去重規則與 NOT NULL", c.Key())
		}
	}
	for _, f := range IdentityFields {
		if _, ok := in[f.Table+"."+f.Column]; !ok {
			t.Fatal("IdentityFields 的欄位不在 ActorColumns:", f)
		}
	}
	for _, k := range []string{"ops_issues.deleted_by", "omixor_server_features.updated_by", "omixor_audit.user_id"} {
		if _, ok := in[k]; !ok {
			t.Fatal("缺", k)
		}
	}
}

// ---- 22 欄 × 所有樣態 ----

func TestActorColumnsAllPatterns(t *testing.T) {
	fx := newActorFx(t)
	rows := seedAll(t, fx)
	rep := runCols(t, fx.p)
	wantAllConverted(t, rep)
	checkSeeded(t, fx, rows)
	// 遷移後沒有孤兒(每欄反向查詢全為 0),omixor_actors 的 id 前綴與 kind 一致
	for _, col := range ActorColumns {
		if n := pgCount(t, fx.p, col.fkSpec().orphan); n != 0 {
			t.Errorf("%s 有 %d 個孤兒", col.Key(), n)
		}
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_actors where not ((kind = 'user' and id like 'usr\_%') or (kind = 'agent' and id like 'agt\_%') or (kind = 'system' and id like 'sys\_%'))`); n != 0 {
		t.Fatal("actor id 前綴與 kind 不一致", n)
	}
	// 每個欄位都有完成標記
	for _, col := range ActorColumns {
		if !fx.p.actorColumnConverted(bg(), col.Table, col.Target) {
			t.Errorf("%s 沒有完成標記", col.Key())
		}
	}
}

// ---- 冪等、續做 ----

func actorsSnapshot(t *testing.T, p *PG) string {
	t.Helper()
	var s string
	chk(t, p.pool.QueryRow(bg(), `select
 (select coalesce(string_agg(id || '|' || name || '|' || handle || '|' || xmin::text, ',' order by id), '') from omixor_actors)
 || '#' || (select coalesce(string_agg(alias || '|' || actor_id || '|' || xmin::text, ',' order by alias), '') from omixor_actor_aliases)`).Scan(&s))
	return s
}

func dataSnapshot(t *testing.T, p *PG) string {
	t.Helper()
	var parts []string
	for _, tb := range []string{"ops_issues", "ops_issue_comments", "omixor_docs", "omixor_agent_tasks", "omixor_agent_task_notes", "omixor_channels", "omixor_channel_members",
		"omixor_channel_messages", "omixor_channel_mentions", "omixor_channel_reads", "omixor_org_chain_edges", "omixor_incidents", "omixor_incident_events",
		"omixor_org_integrations", "omixor_org_system", "omixor_server_features", "omixor_audit"} {
		var s string
		chk(t, p.pool.QueryRow(bg(), fmt.Sprintf(`select count(*)::text || ':' || coalesce(sum(xmin::text::bigint), 0)::text from %s`, tb)).Scan(&s))
		parts = append(parts, tb+"="+s)
	}
	return strings.Join(parts, ";")
}

// 跑兩次:第二次沒有任何更新、actor 與別名完全不變(xmin 都沒變)、資料列沒被碰
func TestActorColumnsIdempotent(t *testing.T) {
	fx := newActorFx(t)
	rows := seedAll(t, fx)
	wantAllConverted(t, runCols(t, fx.p))
	a1, d1 := actorsSnapshot(t, fx.p), dataSnapshot(t, fx.p)
	rep := runCols(t, fx.p)
	wantAllConverted(t, rep)
	if len(rep.Updated) != 0 {
		t.Fatal("第二次不該更新任何列:", rep.Updated)
	}
	if a2 := actorsSnapshot(t, fx.p); a2 != a1 {
		t.Fatal("omixor_actors、omixor_actor_aliases 不該變")
	}
	if d2 := dataSnapshot(t, fx.p); d2 != d1 {
		t.Fatalf("資料列不該被更新:\n%s\n%s", d1, d2)
	}
	checkSeeded(t, fx, rows)
	// sys_legacy 沒有重複:同一個舊值只有一個 actor
	if n := pgCount(t, fx.p, `select count(*) from (select name from omixor_actors where id like 'sys\_legacy\_%' and id <> 'sys_legacy_unknown' and id <> 'sys_legacy_mcp'
and not exists (select 1 from omixor_actor_aliases al where al.actor_id = omixor_actors.id and al.alias like 'org/%') group by name having count(*) > 1) x`); n != 0 {
		t.Fatal("sys_legacy 不該有同名的多個", n)
	}
	// 改名完成但完成標記沒寫成功(中斷):下次跑成功補上
	pgExec(t, fx.p, `comment on column omixor_org_chain_edges.from_actor is null`)
	pgExec(t, fx.p, `comment on column omixor_incident_events.actor is null`)
	wantAllConverted(t, runCols(t, fx.p))
	if d3 := dataSnapshot(t, fx.p); d3 != d1 {
		t.Fatal("補標記不該改資料")
	}
}

// 預算用完:批次中途停下、沒有完成標記;下次接著做、結果和一次做完相同
func TestActorColumnsBudgetResume(t *testing.T) {
	fx := newActorFx(t)
	rows := seedAll(t, fx)
	fx.p.note.begin(10 * time.Minute)
	batches := 0
	actorColumnsBatchHook = func() {
		if batches++; batches == 10 {
			fx.p.note.begin(-time.Second) // 第 10 批之後預算用完
		}
	}
	buf := captureLog(t)
	rep := runCols(t, fx.p)
	fx.p.note.end()
	actorColumnsBatchHook = nil
	if !rep.Partial || len(rep.Converted) == 0 || len(rep.Converted) >= len(ActorColumns) {
		t.Fatalf("要做到一半:partial=%v converted=%d", rep.Partial, len(rep.Converted))
	}
	if !strings.Contains(buf.String(), "預算") {
		t.Fatal("要寫預算用完的警告:", buf.String())
	}
	// 被中斷的那一欄沒有完成標記
	done := 0
	for _, col := range ActorColumns {
		if fx.p.actorColumnConverted(bg(), col.Table, col.Target) {
			done++
		}
	}
	if done != len(rep.Converted) {
		t.Fatalf("完成標記 %d 欄,報告 %d 欄", done, len(rep.Converted))
	}
	// 續做
	rep = runCols(t, fx.p)
	wantAllConverted(t, rep)
	checkSeeded(t, fx, rows)
	// 預算一開始就用完:什麼都不做、不失敗
	fx2 := newActorFx(t)
	fx2.seedValue(t, ActorColumns[0], "o", 0, "a@x", "")
	fx2.p.note.begin(-time.Second)
	rep = runCols(t, fx2.p)
	fx2.p.note.end()
	if !rep.Partial || len(rep.Converted) != 0 {
		t.Fatalf("%+v", rep)
	}
}

// 部分已轉換:一半的欄位(與同一欄一半的列)已經是 actor id、其餘是舊字串;再跑成功,已轉換的不動
func TestActorColumnsPartlyConverted(t *testing.T) {
	fx := newActorFx(t)
	col := ActorColumns[0] // ops_issues.created_by
	var reads, wants []string
	for i, x := range [][2]string{{fx.userA, fx.userA}, {"a@x", fx.userA}, {"sys_chain", "sys_chain"}, {"agent:dev", fx.agentDev}} {
		reads = append(reads, fx.seedValue(t, col, "o", i, x[0], ""))
		wants = append(wants, x[1])
	}
	wantAllConverted(t, runCols(t, fx.p))
	for i, q := range reads {
		if got := readActor(t, fx.p, q); got != wants[i] {
			t.Errorf("第 %d 列 → %s,想要 %s", i, got, wants[i])
		}
	}
}

// ---- 孤兒:不加外鍵、不讓啟動失敗 ----

// 批次更新之間有新的舊字串寫入(舊 revision 還在寫):驗證發現孤兒 → 這一欄略過(沒有完成標記、不加外鍵),其他欄照做,下次再跑補完
func TestActorColumnsOrphanSkipsColumn(t *testing.T) {
	fx := newActorFx(t)
	withActorBatch(t, 1)
	col := ActorColumns[0] // ops_issues.created_by
	fx.seedValue(t, col, "o", 1, "a@x", "")
	fx.seedValue(t, col, "o", 2, "a@x", "")
	fx.seedValue(t, ActorColumns[3], "o", 3, "a@x", "") // ops_issue_comments.author:另一欄,正常
	late := false
	actorColumnsBatchHook = func() {
		if !late { // 第一批做完,一筆新的舊字串寫進來
			late = true
			pgExec(t, fx.p, `insert into ops_issues (org, id, number, pos, title, status, priority, created_by, created_at, updated_at)
values ('o', 'late', 99, 99, 't', 'open', 'normal', 'late-writer@old', now(), now())`)
		}
	}
	buf := captureLog(t)
	rep := runCols(t, fx.p)
	actorColumnsBatchHook = nil
	if msg, ok := rep.Skipped[col.Key()]; !ok || !strings.Contains(msg, "孤兒") {
		t.Fatalf("要略過 %s:%v", col.Key(), rep.Skipped)
	}
	out := buf.String()
	for _, want := range []string{"[遷移警告]", col.Key(), "late-writer@old", "略過後續步驟"} {
		if !strings.Contains(out, want) {
			t.Errorf("警告缺 %q:\n%s", want, out)
		}
	}
	if fx.p.actorColumnConverted(bg(), col.Table, col.Target) {
		t.Fatal("有孤兒不能寫完成標記")
	}
	if ex, _ := fkValidated(t, fx.p, col.Table, col.FK); ex {
		t.Fatal("有孤兒不加外鍵")
	}
	if ex, _ := fkValidated(t, fx.p, "omixor_audit", "fk_omixor_audit_actor"); !ex {
		t.Fatal("其他欄位照常轉換、加外鍵")
	}
	if len(rep.Converted) != len(ActorColumns)-1 {
		t.Fatalf("其他 %d 欄都要完成:%d", len(ActorColumns)-1, len(rep.Converted))
	}
	// 這一欄的 fkSpec 還沒就緒:migrateForeignKeys 不會去加、OrphanReport 不列
	chk(t, fx.p.migrateForeignKeys())
	if ex, _ := fkValidated(t, fx.p, col.Table, col.FK); ex {
		t.Fatal("migrateForeignKeys 不該為還沒轉換完成的欄位加外鍵")
	}
	// 下次再跑(沒有新的寫入)補完,加外鍵
	rep = runCols(t, fx.p)
	wantAllConverted(t, rep)
	if ex, _ := fkValidated(t, fx.p, col.Table, col.FK); !ex {
		t.Fatal("補完後要加外鍵")
	}
	if got := readActor(t, fx.p, `select created_by from ops_issues where id = 'late'`); got != fx.handleActor(t, "late-writer@old") {
		t.Fatal("晚到的列也轉成未註冊使用者", got)
	}
}

// ---- 主鍵去重 ----

func TestActorColumnsPrimaryKeyDedup(t *testing.T) {
	fx := newActorFx(t)
	now := time.Now()
	at1, at2 := now.Add(-time.Hour), now
	for n, ch := range []string{"c1", "c2"} {
		fx.insert(t, "omixor_channels", "org", "o", "id", ch, "number", n+1, "name", ch, "created_at", now, "updated_at", now, "created_by", "sys_omixor")
		fx.insert(t, "omixor_channel_messages", "org", "o", "channel_id", ch, "id", "m1", "number", 1, "author", "sys_omixor", "text", "x", "at", now)
	}
	// 頻道成員:同一人三種寫法同在 c1(pos 0、1、2)、另一個人在 c1、同一人在 c2(不撞)
	for pos, m := range []string{"user:A@x", "a@x", "A@X", "tim@example.com"} {
		fx.insert(t, "omixor_channel_members", "org", "o", "channel_id", "c1", "member", m, "pos", pos)
	}
	fx.insert(t, "omixor_channel_members", "org", "o", "channel_id", "c2", "member", "a@x", "pos", 7)
	// 已經是 actor id 的與舊寫法同在(部分已轉換):留 pos 小的
	fx.insert(t, "omixor_channel_members", "org", "o", "channel_id", "c2", "member", fx.userTim, "pos", 3)
	fx.insert(t, "omixor_channel_members", "org", "o", "channel_id", "c2", "member", "tim@example.com", "pos", 1)
	// 提及
	for pos, m := range []string{"a@x", "user:a@x"} {
		fx.insert(t, "omixor_channel_mentions", "org", "o", "channel_id", "c1", "message_id", "m1", "member", m, "pos", pos)
	}
	// 已讀:留 at 較新的
	fx.insert(t, "omixor_channel_reads", "org", "o", "channel_id", "c1", "member", "a@x", "last_read", 3, "at", at1)
	fx.insert(t, "omixor_channel_reads", "org", "o", "channel_id", "c1", "member", "user:a@x", "last_read", 9, "at", at2)
	fx.insert(t, "omixor_channel_reads", "org", "o", "channel_id", "c1", "member", "A@X", "last_read", 5, "at", at1)
	fx.insert(t, "omixor_channel_reads", "org", "o", "channel_id", "c2", "member", "a@x", "last_read", 1, "at", at1)
	rep := runCols(t, fx.p)
	wantAllConverted(t, rep)
	if n := pgCount(t, fx.p, `select count(*) from omixor_channel_members where channel_id = 'c1' and member = $1`, fx.userA); n != 1 {
		t.Fatal("c1 的 a@x 要剩一筆", n)
	}
	if pos := pgCount(t, fx.p, `select pos from omixor_channel_members where channel_id = 'c1' and member = $1`, fx.userA); pos != 0 {
		t.Fatal("保留 pos 最小的", pos)
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_channel_members where channel_id = 'c1'`); n != 2 {
		t.Fatal("c1 剩 a、tim 兩人", n)
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_channel_members where channel_id = 'c2'`); n != 2 {
		t.Fatal("c2:a 與 tim 各一筆", n)
	}
	if pos := pgCount(t, fx.p, `select pos from omixor_channel_members where channel_id = 'c2' and member = $1`, fx.userTim); pos != 1 {
		t.Fatal("tim 的舊寫法 pos 1 比已轉換的 pos 3 小,留 1", pos)
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_channel_mentions where member = $1`, fx.userA); n != 1 {
		t.Fatal("提及剩一筆", n)
	}
	if pos := pgCount(t, fx.p, `select pos from omixor_channel_mentions where member = $1`, fx.userA); pos != 0 {
		t.Fatal("提及保留 pos 小的", pos)
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_channel_reads where channel_id = 'c1' and member = $1`, fx.userA); n != 1 {
		t.Fatal("已讀剩一筆", n)
	}
	if lr := pgCount(t, fx.p, `select last_read from omixor_channel_reads where channel_id = 'c1' and member = $1`, fx.userA); lr != 9 {
		t.Fatal("已讀保留 at 較新的(last_read 9)", lr)
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_channel_reads where channel_id = 'c2' and member = $1`, fx.userA); n != 1 {
		t.Fatal("c2 不受影響", n)
	}
	if rep.Updated["omixor_channel_members.member(去重刪除)"] != 3 {
		t.Fatal("去重刪除的筆數", rep.Updated)
	}
}

// ---- 空字串轉 NULL、結構 ----

func TestActorColumnsEmptyToNullAndStructure(t *testing.T) {
	fx := newActorFx(t)
	reads := map[string]string{}
	for _, col := range ActorColumns {
		if col.Mode == ActorColCopy {
			continue
		}
		reads[col.Key()] = fx.seedValue(t, col, "o", 0, "", "")
		fx.seedValue(t, col, "o", 1, "a@x", "")
	}
	wantAllConverted(t, runCols(t, fx.p))
	for _, col := range ActorColumns {
		var nullable, hasDefault bool
		chk(t, fx.p.pool.QueryRow(bg(), `select is_nullable = 'YES', column_default is not null from information_schema.columns where table_name = $1 and column_name = $2`, col.Table, col.Target).Scan(&nullable, &hasDefault))
		switch {
		case col.Mode == ActorColCopy: // 新欄位:可為 NULL、沒有預設
			if !nullable || hasDefault {
				t.Errorf("%s.%s 要可為 NULL、沒預設", col.Table, col.Target)
			}
		case col.Nullable:
			if !nullable || hasDefault {
				t.Errorf("%s 要 drop not null 與 default(nullable=%v default=%v)", col.Key(), nullable, hasDefault)
			}
			if n := pgCount(t, fx.p, fmt.Sprintf(`select count(*) from %s where %s = ''`, col.Table, col.Target)); n != 0 {
				t.Errorf("%s 還有空字串", col.Key())
			}
		default:
			if nullable {
				t.Errorf("%s 要維持 NOT NULL", col.Key())
			}
		}
	}
	// 可為 NULL 的欄位現在可以寫 NULL;空字串轉成 NULL(r0)
	if got := readActor(t, fx.p, reads["ops_issues.assignee"]); got != "NULL" {
		t.Fatal(got)
	}
	if got := readActor(t, fx.p, reads["ops_issue_comments.author"]); got != "sys_legacy_unknown" {
		t.Fatal("NOT NULL 欄位的空字串 → sys_legacy_unknown", got)
	}
	// 欄位改名:from_node、to_node 不存在了
	if n := pgCount(t, fx.p, `select count(*) from information_schema.columns where table_name = 'omixor_org_chain_edges' and column_name in ('from_node', 'to_node')`); n != 0 {
		t.Fatal("舊欄位名不該還在")
	}
	if n := pgCount(t, fx.p, `select count(*) from information_schema.columns where table_name = 'omixor_org_chain_edges' and column_name in ('from_actor', 'to_actor')`); n != 2 {
		t.Fatal("改名後的欄位")
	}
	// 複製欄位的舊來源欄位轉換驗證後 drop(事件時間軸的 actor_kind、actor_id、稽核的 user_id);稽核的 email 快照與使用者的 email、name 保留
	for _, c := range [][3]any{{"omixor_incident_events", "actor_kind", 0}, {"omixor_incident_events", "actor_id", 0}, {"omixor_audit", "user_id", 0}, {"omixor_audit", "email", 1}, {"omixor_users", "email", 1}, {"omixor_users", "name", 1}} {
		if n := pgCount(t, fx.p, `select count(*) from information_schema.columns where table_name = $1 and column_name = $2`, c[0], c[1]); n != c[2].(int) {
			t.Errorf("%s.%s 存在 %d 欄,要 %d", c[0], c[1], n, c[2])
		}
	}
	// 主鍵還在、值是 actor id
	if n := pgCount(t, fx.p, `select count(*) from pg_constraint where conrelid = 'omixor_channel_members'::regclass and contype = 'p'`); n != 1 {
		t.Fatal("主鍵")
	}
}

// ---- sys_legacy 穩定 ----

func TestActorColumnsLegacyStable(t *testing.T) {
	ids := make([]string, 2)
	for i := range ids {
		fx := newActorFx(t)
		r0 := fx.seedValue(t, ActorColumns[0], "o", 0, "冒名", "")
		r1 := fx.seedValue(t, ActorColumns[0], "o", 1, "  冒名  ", "")
		r2 := fx.seedValue(t, ActorColumns[0], "o", 2, "ops", "")
		wantAllConverted(t, runCols(t, fx.p))
		ids[i] = readActor(t, fx.p, r0)
		if ids[i] != LegacyActorID("local:冒名") || readActor(t, fx.p, r1) != ids[i] {
			t.Fatal("同一個舊值(含空白)要同一個 actor:", ids[i])
		}
		if readActor(t, fx.p, r2) == ids[i] {
			t.Fatal("不同舊值不同 actor")
		}
		if n := pgCount(t, fx.p, `select count(*) from omixor_actors where id = $1 and kind = 'system' and name = '冒名' and handle = $2`, ids[i], strings.TrimPrefix(ids[i], "sys_")); n != 1 {
			t.Fatal("名稱保留原值")
		}
		if n := pgCount(t, fx.p, `select count(*) from omixor_actor_aliases where alias = 'local:冒名' and actor_id = $1`, ids[i]); n != 1 {
			t.Fatal("要記別名")
		}
	}
	if ids[0] != ids[1] {
		t.Fatal("不同資料庫同一個舊值也是同一個 actor id", ids)
	}
	// 別名表是來源:別名指到手動指定的 actor,之後的轉換對到它(匯入、重跑穩定)
	fx := newActorFx(t)
	pgExec(t, fx.p, `insert into omixor_actors (id, kind, name, handle) values ('sys_legacy_manual', 'system', 'manual', 'legacy_manual')`)
	pgExec(t, fx.p, `insert into omixor_actor_aliases (alias, actor_id) values ('local:bob', 'sys_legacy_manual')`)
	q := fx.seedValue(t, ActorColumns[0], "o", 5, "bob", "")
	wantAllConverted(t, runCols(t, fx.p))
	if got := readActor(t, fx.p, q); got != "sys_legacy_manual" {
		t.Fatal("要對到別名表指定的 actor:", got)
	}
}

// ---- 大表、前置檢查 ----

func TestActorColumnsOversizedSkipped(t *testing.T) {
	fx := newActorFx(t)
	rows := seedAll(t, fx)
	withMaxRewrite(t, 1) // 任何表都超過
	buf := captureLog(t)
	before := dataSnapshot(t, fx.p)
	rep := runCols(t, fx.p)
	if len(rep.Converted) != 0 || len(rep.Skipped) != len(ActorColumns) {
		t.Fatalf("全部略過:converted=%d skipped=%d", len(rep.Converted), len(rep.Skipped))
	}
	out := buf.String()
	for _, want := range []string{"不自動轉換", "ops_issues", "OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB", "維護窗口"} {
		if !strings.Contains(out, want) {
			t.Errorf("警告缺 %q", want)
		}
	}
	if dataSnapshot(t, fx.p) != before {
		t.Fatal("大表不能被改")
	}
	if n := pgCount(t, fx.p, `select count(*) from pg_constraint where conname like 'fk\_%' and conname = any($1)`, actorFKNameList()); n != 0 {
		t.Fatal("不加外鍵", n)
	}
	if n := pgCount(t, fx.p, `select count(*) from information_schema.columns where table_name = 'omixor_incident_events' and column_name = 'actor'`); n != 0 {
		t.Fatal("不加新欄位")
	}
	// 啟動彙總列出還沒轉的欄位
	buf.Reset()
	fx.p.logMigrationSummary()
	if !strings.Contains(buf.String(), "尚未轉成 actor id 的欄位") || !strings.Contains(buf.String(), "ops_issues.created_by") {
		t.Fatal("彙總要列出:", buf.String())
	}
	// 門檻調大:照做
	withMaxRewrite(t, 1<<40)
	wantAllConverted(t, runCols(t, fx.p))
	checkSeeded(t, fx, rows)
}

func actorFKNameList() []string {
	var l []string
	for _, c := range ActorColumns {
		l = append(l, c.FK)
	}
	return l
}

// 前置檢查:還有沒展開的列 → 略過整個引擎(警告),不改任何東西;展開後才做
func TestActorColumnsPrecheckUnmigratedRows(t *testing.T) {
	fx := newActorFx(t)
	q0 := fx.seedValue(t, ActorColumns[0], "o", 0, "a@x", "")
	now := time.Now()
	pgExec(t, fx.p, `insert into omixor_channels (org, id, number, name, created_at, updated_at, created_by) values ('o', 'raw', 1, 'raw', $1, $1, 'a@x')`, now)
	buf := captureLog(t)
	rep := runCols(t, fx.p)
	if _, ok := rep.Skipped["*"]; !ok || len(rep.Converted) != 0 {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(buf.String(), "UnmigratedRows") || !strings.Contains(buf.String(), "[遷移警告]") {
		t.Fatal(buf.String())
	}
	if got := readActor(t, fx.p, q0); got != "a@x" {
		t.Fatal("略過時不改任何資料", got)
	}
	pgExec(t, fx.p, `update omixor_channels set split_at = now() where id = 'raw'`)
	wantAllConverted(t, runCols(t, fx.p))
	// 缺 actor 表(別名表):略過
	pgExec(t, fx.p, `drop table omixor_actor_aliases`)
	rep = runCols(t, fx.p)
	if _, ok := rep.Skipped["*"]; !ok {
		t.Fatal("沒有別名表要略過", rep)
	}
}

// 開發機殘留的身分登記層:只警告,不擋
func TestActorColumnsPrincipalsResidueWarns(t *testing.T) {
	fx := newActorFx(t)
	pgExec(t, fx.p, `create table omixor_principals (id text primary key)`)
	buf := captureLog(t)
	wantAllConverted(t, runCols(t, fx.p))
	if !strings.Contains(buf.String(), "omixor_principals") {
		t.Fatal("要警告身分登記層殘留:", buf.String())
	}
}

// ---- 開關 ----

// structureDump 全部表(除了 omixor_actor_aliases)的欄位(型別、可否 NULL、預設)、約束、索引與欄位註解
func structureDump(t *testing.T, p *PG) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`select table_name || '.' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '') from information_schema.columns
		  where table_schema = current_schema() and table_name <> 'omixor_actor_aliases' order by 1`,
		`select conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid) || ' ' || convalidated::text from pg_constraint
		  where connamespace = current_schema()::regnamespace and conrelid::regclass::text <> 'omixor_actor_aliases' order by 1`,
		`select indexdef from pg_indexes where schemaname = current_schema() and tablename <> 'omixor_actor_aliases' order by 1`,
		`select c.relname || '.' || a.attname || ' ' || d.description from pg_description d join pg_class c on c.oid = d.objoid join pg_attribute a on a.attrelid = c.oid and a.attnum = d.objsubid
		  where c.relnamespace = current_schema()::regnamespace and d.objsubid > 0 order by 1`,
	} {
		rows, err := p.pool.Query(bg(), q)
		chk(t, err)
		for rows.Next() {
			var s string
			chk(t, rows.Scan(&s))
			b.WriteString(s + "\n")
		}
		chk(t, rows.Err())
		rows.Close()
	}
	return b.String()
}

// reopen 關掉再用 NewPG 重開同一個資料庫(重跑啟動遷移)
func reopenURL(t *testing.T, p *PG, url string) *PG {
	t.Helper()
	p.Close()
	n, err := NewPG(url)
	chk(t, err)
	n.WaitFKValidate()
	t.Cleanup(n.Close)
	return n
}

// 轉換沒做完就不能啟動:舊結構、表超過自動遷移門檻 → 啟動失敗並列出欄位(進度保留),調大門檻後重啟就轉完;沒有任何開關可以跳過
func TestActorColumnsStartupFailsWhenPending(t *testing.T) {
	SetMigrationLocalUser("tim")
	t.Cleanup(func() { SetMigrationLocalUser("") })
	url := testPGBlank(t)
	p := fkPG(t, url)
	mkOrgs(t, p, "o")
	legacyActorStructure(t, p)
	fx := &actorFx{p: p, hasSplit: map[string]bool{}}
	rows, err := p.pool.Query(bg(), `select table_name from information_schema.columns where column_name = 'split_at' and table_schema = current_schema()`)
	chk(t, err)
	for rows.Next() {
		var s string
		chk(t, rows.Scan(&s))
		fx.hasSplit[s] = true
	}
	rows.Close()
	col := colByKey(t, "ops_issues.created_by")
	fx.seedValue(t, col, "o", 0, "a@x", "")
	p.Close()

	old := maxAutoRewriteBytes
	maxAutoRewriteBytes = 1 // 每張表都算太大
	_, err = NewPG(url)
	maxAutoRewriteBytes = old
	if err == nil || !strings.Contains(err.Error(), "沒轉成 actor id") || !strings.Contains(err.Error(), "ops_issues.created_by") {
		t.Fatalf("有欄位沒轉完要啟動失敗並列出欄位:%v", err)
	}
	if !strings.Contains(err.Error(), "OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB") {
		t.Fatalf("要提示調大門檻:%v", err)
	}
	// 調回門檻重啟:接著轉完,啟動成功
	n := reopenURL(t, p, url)
	if rep, err := n.runActorColumns(); err != nil || len(rep.Skipped) != 0 {
		t.Fatalf("重啟後要全部轉完:%+v %v", rep, err)
	}
	if pending, err := n.actorColumnsPending(bg()); err != nil || len(pending) != 0 {
		t.Fatalf("不該還有待轉換的欄位:%v %v", pending, err)
	}
}

// 啟動遷移轉換 22 欄(結構改動見 TestActorColumnsEmptyToNullAndStructure)並加外鍵(not valid → 背景 validate)
func TestActorColumnsStartupConverts(t *testing.T) {
	SetMigrationLocalUser("tim")
	t.Cleanup(func() { SetMigrationLocalUser("") })
	url := testPGBlank(t)
	fx := &actorFx{hasSplit: map[string]bool{}}
	p := fkPG(t, url)
	fx.p = p
	rs, err := p.pool.Query(bg(), `select table_name from information_schema.columns where column_name = 'split_at' and table_schema = current_schema()`)
	chk(t, err)
	for rs.Next() {
		var s string
		chk(t, rs.Scan(&s))
		fx.hasSplit[s] = true
	}
	rs.Close()
	mkOrgs(t, p, "o")
	saveU(t, p, "u_a", "a@x", "A")
	saveU(t, p, "u_tim", "tim", "Tim")
	chk(t, p.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", Active: true, CreatedAt: model.Now()}))
	fx.userA, fx.userLocal = userActorOf(t, p, "u_a"), userActorOf(t, p, "u_tim")
	legacyActorStructure(t, p)
	var rowsSeeded []seeded
	for _, col := range ActorColumns {
		pts := []pat{
			{"a@x", "", lit(fx.userA)}, {"mcp", "", lit(fx.userLocal)}, {"冒名", "", lit(node("冒名"))},
		}
		switch col.Key() {
		case "omixor_incident_events.actor_id":
			pts = []pat{{"person", "a@x", lit(fx.userA)}, {"ai", "mcp", lit(fx.userLocal)}, {"system", "", lit("sys_omixor")}}
		case "omixor_audit.user_id":
			pts = []pat{{"u_a", "", lit(fx.userA)}, {"u_zzz", "", lit(node("u_zzz"))}, {"", "", lit("NULL")}}
		case "ops_issues.assignee":
			pts = []pat{{"", "", lit("NULL")}, {"nobody", "", ghostAgent("agent:nobody")}}
		case "omixor_server_features.updated_by":
			pts = pts[:1]
		}
		for i, pt := range pts {
			org := rowOrg(t, fx, col, i)
			rowsSeeded = append(rowsSeeded, seeded{col, org, pt, fx.seedValue(t, col, org, i, pt.v1, pt.v2)})
		}
	}
	// 1. 重啟:全部轉完
	p = reopenURL(t, p, url)
	fx.p = p
	for _, col := range ActorColumns {
		if !p.actorColumnConverted(bg(), col.Table, col.Target) {
			t.Fatalf("%s 啟動後沒有轉完", col.Key())
		}
	}
	checkSeeded(t, fx, rowsSeeded)
	buf := captureLog(t)
	p.logMigrationSummary()
	if strings.Contains(buf.String(), "尚未轉成") {
		t.Fatal("全部轉完,彙總不該列未轉換:", buf.String())
	}
	// 2. 22 條外鍵都在、已驗證;孤兒報告全 0;寫入不存在的 actor 被擋
	if len(fkSpecs()) != baseFKSpecs()+22 {
		t.Fatal("fkSpecs 含 22 條", len(fkSpecs()))
	}
	for _, col := range ActorColumns {
		if ex, ok := fkValidated(t, p, col.Table, col.FK); !ex || !ok {
			t.Errorf("外鍵 %s:存在 %v、已驗證 %v", col.FK, ex, ok)
		}
	}
	rep, err := p.OrphanReport()
	chk(t, err)
	for _, o := range rep {
		if o.Count != 0 {
			t.Fatal("轉換後不該有孤兒", o)
		}
	}
	if _, err := p.pool.Exec(bg(), `update ops_issues set created_by = 'not-an-actor' where number = (select min(number) from ops_issues)`); err == nil {
		t.Fatal("外鍵要擋掉不存在的 actor")
	} else if e := fkErr(err); !strings.Contains(e.Error(), ErrBadRef.Error()) {
		t.Fatal("寫入不存在的 actor 要是 ErrBadRef:", e)
	}
	if _, err := p.pool.Exec(bg(), `delete from omixor_actors where id = 'sys_omixor'`); err == nil {
		t.Fatal("actor 被參照時不能刪(restrict)")
	}
	// 3. 再重啟一次:冪等
	snap := structureDump(t, p)
	p = reopenURL(t, p, url)
	fx.p = p
	if structureDump(t, p) != snap {
		t.Fatal("重啟後結構不變")
	}
	checkSeeded(t, fx, rowsSeeded)
}

// 外鍵註冊:fkSpecs 多 22 條 actor 外鍵(名稱不衝突)、孤兒 SQL 能執行(欄位已轉換的資料庫)
func TestActorFKSpecsRegistered(t *testing.T) {
	base := baseFKSpecs()
	specs := fkSpecs()
	if len(specs) != base+22 {
		t.Fatal(len(specs), base)
	}
	names := map[string]bool{}
	for _, f := range specs {
		if names[f.name] {
			t.Fatal("外鍵名稱重複", f.name)
		}
		names[f.name] = true
	}
	for _, c := range ActorColumns {
		if !isActorFK(c.FK) || !names[c.FK] {
			t.Fatal("沒登記", c.FK)
		}
	}
	if isActorFK("fk_agent_tasks_agent") {
		t.Fatal("既有外鍵不是 actor 外鍵")
	}
}

// baseFKSpecs 沒有 actor 外鍵的 fkSpecs 條數(9 條關聯 + 每個有 org 的表一條)
func baseFKSpecs() int { return 9 + len(orgFKTables) }

var _ = context.Background

// 批次在同一欄中途停下(預算用完):已更新的留著、沒有完成標記,下次接著做
func TestActorColumnsBudgetMidColumn(t *testing.T) {
	fx := newActorFx(t)
	withActorBatch(t, 1)
	col := ActorColumns[0]
	var reads []string
	for i := 0; i < 6; i++ {
		reads = append(reads, fx.seedValue(t, col, "o", i, "a@x", ""))
	}
	fx.p.note.begin(10 * time.Minute)
	n := 0
	actorColumnsBatchHook = func() {
		if n++; n == 3 {
			fx.p.note.begin(-time.Second)
		}
	}
	rep := runCols(t, fx.p)
	fx.p.note.end()
	actorColumnsBatchHook = nil
	if !rep.Partial || fx.p.actorColumnConverted(bg(), col.Table, col.Target) {
		t.Fatalf("要停在這一欄中間:%+v", rep)
	}
	done := 0
	for _, q := range reads {
		if readActor(t, fx.p, q) == fx.userA {
			done++
		}
	}
	if done != 3 {
		t.Fatal("第 3 批後停下,應該更新了 3 列", done, rep.Updated)
	}
	wantAllConverted(t, runCols(t, fx.p))
	for _, q := range reads {
		if readActor(t, fx.p, q) != fx.userA {
			t.Fatal("續做後全部轉完")
		}
	}
}

// 滾動部署:轉換完成之後舊版本又寫進舊字串。外鍵開關關閉時不掃表、不管它;之後開外鍵,加外鍵前驗證發現漏網的 → 作廢標記、重新轉換,再加外鍵
func TestActorColumnsLateLegacyRowsBeforeFK(t *testing.T) {
	fx := newActorFx(t)
	col := ActorColumns[0]
	fx.seedValue(t, col, "o", 0, "a@x", "")
	wantAllConverted(t, runCols(t, fx.p))
	pgExec(t, fx.p, "alter table "+col.Table+" drop constraint "+col.FK) // 模擬:滾動部署時舊 revision 還在寫、外鍵還沒加上
	late := fx.seedValue(t, col, "o", 1, "late@old", "")                 // 舊 revision 轉換後才寫的
	buf := captureLog(t)
	rep := runCols(t, fx.p)
	wantAllConverted(t, rep)
	if got := readActor(t, fx.p, late); got != fx.handleActor(t, "late@old") {
		t.Fatal("開外鍵時重新轉換:", got)
	}
	if ex, _ := fkValidated(t, fx.p, col.Table, col.FK); !ex || !strings.Contains(buf.String(), "作廢完成標記") {
		t.Fatalf("要加外鍵、並記錄作廢標記:%s", buf.String())
	}
	fx.p.startFKValidate(fkSpecs())
	fx.p.WaitFKValidate()
	if ex, ok := fkValidated(t, fx.p, col.Table, col.FK); !ex || !ok {
		t.Fatal("背景 validate 要成功", ex, ok)
	}
}

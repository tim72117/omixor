package store

// ISS-36 C-1 審查發現的回歸測試:轉換後才寫入的空字串(A)、超長舊值與 Unicode 空白(B)、啟動彙總(C)、
// sys_legacy 雜湊碰撞、不同組織的已刪角色、前綴大小寫、同 email 大小寫多帳號(D)

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func colByKey(t *testing.T, key string) ActorColumn {
	t.Helper()
	for _, c := range ActorColumns {
		if c.Key() == key {
			return c
		}
	}
	t.Fatal("找不到欄位", key)
	return ActorColumn{}
}

// A:轉換完成之後舊程式又寫入 assignee = ''。開外鍵那次啟動要把它轉成 NULL(作廢標記、重轉),外鍵加得上而且 validate 得過
func TestActorColumnsBlankAfterConvertBeforeFK(t *testing.T) {
	fx := newActorFx(t)
	col := colByKey(t, "ops_issues.assignee")
	fx.seedValue(t, col, "o", 0, "dev", "")
	wantAllConverted(t, runCols(t, fx.p))
	pgExec(t, fx.p, "alter table "+col.Table+" drop constraint "+col.FK) // 模擬:滾動部署時外鍵還沒加上,舊 revision 還在寫
	blank := fx.seedValue(t, col, "o", 1, "", "")                        // 舊 revision 轉換後才寫的空字串
	if readActor(t, fx.p, blank) != "" {
		t.Fatal("前提:轉換後寫進去的是空字串")
	}
	buf := captureLog(t)
	wantAllConverted(t, runCols(t, fx.p))
	if got := readActor(t, fx.p, blank); got != "NULL" {
		t.Fatalf("空字串要轉成 NULL:%q", got)
	}
	if !strings.Contains(buf.String(), "作廢完成標記") {
		t.Fatalf("要作廢標記重轉:%s", buf.String())
	}
	if ex, _ := fkValidated(t, fx.p, col.Table, col.FK); !ex {
		t.Fatal("外鍵要加上")
	}
	fx.p.startFKValidate(fkSpecs())
	fx.p.WaitFKValidate()
	if ex, ok := fkValidated(t, fx.p, col.Table, col.FK); !ex || !ok {
		t.Fatal("背景 validate 要成功", ex, ok)
	}
	// 已經 validate 的外鍵不會再被這個流程影響:再跑一次什麼都不變
	wantAllConverted(t, runCols(t, fx.p))
}

// A:makeNullable 的批次被併發寫入打斷(受影響列數小於批次大小、但還有符合條件的列)時,不能提早結束、直接寫完成標記
func TestActorColumnsMakeNullableConcurrentBlank(t *testing.T) {
	fx := newActorFx(t)
	withActorBatch(t, 10)
	col := colByKey(t, "ops_issues.deleted_by")
	var reads []string
	for i := 0; i < 3; i++ {
		reads = append(reads, fx.seedValue(t, col, "o", i, "", ""))
	}
	n := 0
	actorColumnsBatchHook = func() {
		if col.Table != "ops_issues" {
			return
		}
		if n++; n == 1 { // 第一批更新完,舊程式又寫進一列空字串
			reads = append(reads, fx.seedValue(t, col, "o", 99, "  ", ""))
		}
	}
	wantAllConverted(t, runCols(t, fx.p))
	for _, q := range reads {
		if got := readActor(t, fx.p, q); got != "NULL" {
			t.Fatalf("所有空白值都要是 NULL:%q(%s)", got, q)
		}
	}
	if n < 2 {
		t.Fatal("批次要繼續跑到沒有符合條件的列為止", n)
	}
}

func randHex(t *testing.T, nBytes int) string {
	t.Helper()
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// B:超長舊值(8 KB 隨機)不能讓整欄轉不完:同欄其他列照常轉換,超長值走 sys_legacy、alias 主鍵是雜湊、log 不印整個原值,重跑穩定
func TestActorColumnsHugeLegacyValue(t *testing.T) {
	fx := newActorFx(t)
	col := colByKey(t, "ops_issues.created_by")
	huge := randHex(t, 4096) // 8192 字元
	rHuge := fx.seedValue(t, col, "o", 0, huge, "")
	rA := fx.seedValue(t, col, "o", 1, "a@x", "")
	rOther := fx.seedValue(t, col, "o", 2, "冒名", "")
	buf := captureLog(t)
	wantAllConverted(t, runCols(t, fx.p))
	if got := readActor(t, fx.p, rA); got != fx.userA {
		t.Fatal("同欄其他列照常轉換", got)
	}
	if got := readActor(t, fx.p, rOther); got != node("冒名") {
		t.Fatal("同欄其他列照常轉換", got)
	}
	hugeActor := readActor(t, fx.p, rHuge)
	if !strings.HasPrefix(hugeActor, "sys_legacy_") {
		t.Fatal("超長值要走 sys_legacy:", hugeActor)
	}
	var name string
	chk(t, fx.p.pool.QueryRow(bg(), `select name from omixor_actors where id = $1`, hugeActor).Scan(&name))
	if name != huge {
		t.Fatal("actor 名稱保留完整原值")
	}
	if n := pgCount(t, fx.p, `select count(*) from omixor_actor_aliases where length(alias) > 600`); n != 0 {
		t.Fatal("alias 主鍵不能是超長原值", n)
	}
	if strings.Contains(buf.String(), huge) {
		t.Fatal("log 不能印整個超長原值")
	}
	if !strings.Contains(buf.String(), "共 8192") {
		t.Fatalf("log 的原值要截斷並附長度:%s", buf.String()[:min(len(buf.String()), 600)])
	}
	// 冪等:重跑不產生新 actor;新寫入同一個超長值 → 同一個 actor
	before := actorsSnapshot(t, fx.p)
	wantAllConverted(t, runCols(t, fx.p))
	if actorsSnapshot(t, fx.p) != before {
		t.Fatal("重跑不該有變化")
	}
	p := fx.p
	got, err := p.ResolveLegacy("o", huge)
	chk(t, err)
	if got != hugeActor {
		t.Fatal("同一個超長值永遠同一個 actor", got, hugeActor)
	}
	if n := pgCount(t, p, `select count(*) from omixor_actors where kind = 'system' and name = $1`, huge); n != 1 {
		t.Fatal("只有一個 actor", n)
	}
}

// B:Go 的 TrimSpace 與 PostgreSQL 的 \s 對 NBSP 等字元判斷不一致:驗證不能因此永遠失敗。
// 可為 NULL 的欄位 → NULL;NOT NULL 的欄位 → sys_legacy_unknown(與 Go 端 TrimSpace == "" 一致)
func TestActorColumnsUnicodeBlank(t *testing.T) {
	fx := newActorFx(t)
	nullable := colByKey(t, "ops_issues.assignee")
	notNull := colByKey(t, "ops_issues.created_by")
	var nullReads, nnReads []string
	for i, v := range []string{" ", "　", "   ", "\u0085", " "} {
		nullReads = append(nullReads, fx.seedValue(t, nullable, "o", i, v, ""))
		nnReads = append(nnReads, fx.seedValue(t, notNull, "o", i, v, ""))
	}
	wantAllConverted(t, runCols(t, fx.p))
	for _, q := range nullReads {
		if got := readActor(t, fx.p, q); got != "NULL" {
			t.Fatalf("可為 NULL 的欄位 Unicode 空白 → NULL:%q", got)
		}
	}
	for _, q := range nnReads {
		if got := readActor(t, fx.p, q); got != "sys_legacy_unknown" {
			t.Fatalf("NOT NULL 欄位 Unicode 空白 → sys_legacy_unknown:%q", got)
		}
	}
	// 事件時間軸(複製欄位):來源是 NBSP → 目標 NULL,驗證通過
	ev := colByKey(t, "omixor_incident_events.actor_id")
	fx2 := newActorFx(t)
	r := fx2.seedValue(t, ev, "o", 0, "person", " ")
	wantAllConverted(t, runCols(t, fx2.p))
	if got := readActor(t, fx2.p, r); got != "NULL" {
		t.Fatal("事件的空白執行者 → NULL", got)
	}
}

// C:FK 開關開、欄位還沒轉完時,啟動彙總不能把 22 條 actor 外鍵都列成缺外鍵
func TestActorMigrationProgressSkipsNotReadyFKs(t *testing.T) {
	fx := newActorFx(t)
	// 還沒跑欄位轉換:欄位都沒有完成標記
	pr, err := fx.p.migrationProgress(bg())
	chk(t, err)
	for _, name := range pr.MissingFKs {
		if isActorFK(name) {
			t.Fatalf("欄位還沒轉完的 actor 外鍵不該列為缺外鍵:%s", name)
		}
	}
	if len(pr.ActorColumnsPending) != len(ActorColumns) {
		t.Fatal("22 欄都還沒轉完", len(pr.ActorColumnsPending))
	}
	wantAllConverted(t, runCols(t, fx.p))
	pr, err = fx.p.migrationProgress(bg())
	chk(t, err)
	if len(pr.MissingFKs) != 0 || len(pr.ActorColumnsPending) != 0 {
		t.Fatalf("轉完之後沒有缺外鍵:%v %v", pr.MissingFKs, pr.ActorColumnsPending)
	}
}

// D:sys_legacy 雜湊碰撞(同雜湊、原值不同)不能靜默併成同一個 actor:改用較長的雜湊,而且重跑穩定
func TestResolveLegacyHashCollision(t *testing.T) {
	fx := newActorFx(t)
	const raw = "冒名甲"
	nodeA := "local:" + raw
	short := LegacyActorID(nodeA)
	// 模擬另一個原值 other 先佔用了同一個 8 碼雜湊的 actor
	pgExec(t, fx.p, `insert into omixor_actors (id, kind, name, handle) values ($1, 'system', 'other', $2)`, short, strings.TrimPrefix(short, "sys_"))
	pgExec(t, fx.p, `insert into omixor_actor_aliases (alias, actor_id) values ('local:other', $1)`, short)
	buf := captureLog(t)
	got, err := fx.p.ResolveLegacy("o", raw)
	chk(t, err)
	if got == short || !strings.HasPrefix(got, "sys_legacy_") || len(got) != len("sys_legacy_")+16 {
		t.Fatalf("碰撞時要改用 16 碼雜湊:%s(短的是 %s)", got, short)
	}
	if !strings.Contains(buf.String(), "碰撞") {
		t.Fatalf("要記錄碰撞:%s", buf.String())
	}
	again, err := fx.p.ResolveLegacy("o", raw)
	chk(t, err)
	if again != got {
		t.Fatal("同一個原值永遠同一個 actor", again, got)
	}
	other, err := fx.p.ResolveLegacy("o", "other")
	chk(t, err)
	if other != short {
		t.Fatal("先佔用的原值仍然對到原本的 actor", other, short)
	}
}

// D:不同組織同名的已刪角色(agent:foo)不併成同一個 sys_legacy actor;沒有組織的列仍是「同一原值同一 actor」
func TestResolveLegacyGhostAgentPerOrg(t *testing.T) {
	fx := newActorFx(t)
	mkOrgs(t, fx.p, "o2")
	a, err := fx.p.ResolveLegacy("o", "agent:foo")
	chk(t, err)
	b, err := fx.p.ResolveLegacy("o2", "agent:foo")
	chk(t, err)
	c, err := fx.p.ResolveLegacy("o", "agent:foo")
	chk(t, err)
	if a == b || a != c {
		t.Fatalf("不同組織 → 不同 actor、同組織穩定:%s %s %s", a, b, c)
	}
	n1, err := fx.p.ResolveLegacy("", "agent:foo")
	chk(t, err)
	n2, err := fx.p.ResolveLegacy("", "agent:foo")
	chk(t, err)
	if n1 != n2 || n1 == a || n1 == b {
		t.Fatalf("沒有組織:同原值同 actor:%s %s", n1, n2)
	}
	if a != LegacyAgentActorID("o", "agent:foo") {
		t.Fatal("LegacyAgentActorID 與解析結果一致")
	}
}

// D:前綴大小寫(Agent:dev、USER:a@x、Local:foo)先正規化,與小寫前綴對到同一個 actor
func TestResolveLegacyPrefixCase(t *testing.T) {
	fx := newActorFx(t)
	for _, c := range []struct{ raw, want string }{
		{"Agent:dev", fx.agentDev}, {"AGENT:dev", fx.agentDev}, {"USER:a@x", fx.userA}, {"User:A@X", fx.userA}, {"Local:foo", fx.userLocal},
	} {
		got, err := fx.p.ResolveLegacy("o", c.raw)
		chk(t, err)
		if got != c.want {
			t.Errorf("%q → %s,想要 %s", c.raw, got, c.want)
		}
	}
	g1, err := fx.p.ResolveLegacy("o", "Agent:ghost")
	chk(t, err)
	g2, err := fx.p.ResolveLegacy("o", "agent:ghost")
	chk(t, err)
	if g1 != g2 {
		t.Fatal("Agent:ghost 與 agent:ghost 是同一個節點", g1, g2)
	}
}

// D:同 email 只差大小寫的多帳號:確定性地選最早建立的,而且寫 log
func TestUserActorCaseDuplicateDeterministic(t *testing.T) {
	fx := newActorFx(t)
	const a1, a2 = "usr_00000000-0000-7000-8000-0000000000a1", "usr_00000000-0000-7000-8000-0000000000a2"
	pgExec(t, fx.p, `insert into omixor_actors (id, kind, name, handle) values ($1,'user','n','dup1@x.com'), ($2,'user','n','dup2@x.com')`, a1, a2)
	// 較晚建立的帳號先插入(避免剛好靠插入順序過關)
	pgExec(t, fx.p, `insert into omixor_users (id, email, name, role, created_at, actor_id) values ('u_dup2','dup@x.com','n','admin','2024-02-01',$1)`, a2)
	pgExec(t, fx.p, `insert into omixor_users (id, email, name, role, created_at, actor_id) values ('u_dup1','DUP@x.com','n','admin','2024-01-01',$1)`, a1)
	buf := captureLog(t)
	for i := 0; i < 3; i++ {
		lk := fx.p.newLegacyLookup(bg())
		got, err := lk.UserActor("dup@x.com")
		chk(t, err)
		if got != a1 {
			t.Fatalf("要選最早建立的帳號:%s", got)
		}
	}
	if !strings.Contains(buf.String(), "dup@x.com") {
		t.Fatalf("同 email 多帳號要寫 log:%s", buf.String())
	}
	_ = fmt.Sprint()
}

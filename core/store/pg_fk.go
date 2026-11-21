package store

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- 外鍵與刪除規則(PG) ----
//
// 表一開始是沒有外鍵的(關聯只靠程式),所以正式資料庫可能已經有孤兒資料。補外鍵的做法:
//   - 啟動時先把每一種孤兒的筆數寫進 log(OrphanReport);
//   - 外鍵用 not valid 先加(新寫入立刻受約束,不掃舊資料),再單獨 validate;validate 在背景做(NewPG 不等它,每條獨立逾時),
//     失敗(有孤兒、逾時)只寫 log 告警,服務照常啟動,外鍵維持 not valid(新寫入仍受約束),下次啟動會再試一次 validate;不會自動刪任何資料。
//   - 所有步驟冪等,每步各用小交易 + lock_timeout + 有限次重試;多個執行個體同時啟動用 advisory lock 排隊(詳見 migrateForeignKeys)。
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
	// orphan 孤兒的計數 SQL(「參照不到」的列數)
	orphan string
	// col 外鍵需要的產生欄位(空 = 都是真正的欄位):背景補外鍵時欄位還沒有就略過(欄位由遷移或維護窗口腳本建)
	col string
}

// projectRefTables 有產生欄位 project_ref 的表(空字串 = 未分專案 → NULL,外鍵不檢查);維護窗口腳本也用同一份
var projectRefTables = []string{"omixor_docs", "omixor_cabinet_sections", "ops_issues"}

// projectRefColumnDef project_ref 的欄位定義(add column 後面接的那段)
const projectRefColumnDef = `project_ref text generated always as (nullif(project, '')) stored`

// orgFKTables 有 org 欄位、要掛到 omixor_orgs 的表(omixor_org_members 本來就有)
var orgFKTables = []string{
	"ops_issues", "omixor_attachments", "omixor_agents", "omixor_agent_tasks", "omixor_agent_credentials",
	"omixor_cabinet_layouts", "omixor_cabinet_sections", "omixor_cabinet_tabs", "omixor_docs", "omixor_projects",
	"omixor_project_seq", "omixor_org_chains", "omixor_channels", "omixor_channel_messages", "omixor_channel_reads",
	"omixor_incidents", "omixor_org_integrations", "omixor_org_system",
}

func fkSpecs() []fkSpec {
	l := []fkSpec{
		{"fk_agent_credentials_task", "omixor_agent_credentials",
			`foreign key (org, task_id) references omixor_agent_tasks (org, id) on delete cascade`,
			`select count(*) from omixor_agent_credentials x where not exists (select 1 from omixor_agent_tasks y where y.org = x.org and y.id = x.task_id)`, ""},
		{"fk_channel_messages_channel", "omixor_channel_messages",
			`foreign key (org, channel_id) references omixor_channels (org, id) on delete cascade`,
			`select count(*) from omixor_channel_messages x where not exists (select 1 from omixor_channels y where y.org = x.org and y.id = x.channel_id)`, ""},
		{"fk_channel_reads_channel", "omixor_channel_reads",
			`foreign key (org, channel_id) references omixor_channels (org, id) on delete cascade`,
			`select count(*) from omixor_channel_reads x where not exists (select 1 from omixor_channels y where y.org = x.org and y.id = x.channel_id)`, ""},
		{"fk_channels_issue", "omixor_channels",
			`foreign key (org, issue_id) references ops_issues (org, id) on delete set null (issue_id) on update cascade`,
			`select count(*) from omixor_channels x where x.issue_id is not null and not exists (select 1 from ops_issues y where y.org = x.org and y.id = x.issue_id)`, ""},
		{"fk_docs_project", "omixor_docs",
			`foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict`,
			`select count(*) from omixor_docs x where x.project <> '' and not exists (select 1 from omixor_projects y where y.org = x.org and y.id = x.project)`, "project_ref"},
		{"fk_issues_project", "ops_issues",
			`foreign key (org, project_ref) references omixor_projects (org, id) on delete restrict`,
			`select count(*) from ops_issues x where x.project <> '' and not exists (select 1 from omixor_projects y where y.org = x.org and y.id = x.project)`, "project_ref"},
		{"fk_cabinet_sections_project", "omixor_cabinet_sections",
			`foreign key (org, project_ref) references omixor_projects (org, id) on delete cascade`,
			`select count(*) from omixor_cabinet_sections x where x.project <> '' and not exists (select 1 from omixor_projects y where y.org = x.org and y.id = x.project)`, "project_ref"},
		{"fk_cabinet_tabs_section", "omixor_cabinet_tabs",
			`foreign key (org, project, section_id) references omixor_cabinet_sections (org, project, id) on delete cascade on update cascade`,
			`select count(*) from omixor_cabinet_tabs x where not exists (select 1 from omixor_cabinet_sections y where y.org = x.org and y.project = x.project and y.id = x.section_id)`, ""},
		{"fk_agent_tasks_agent", "omixor_agent_tasks",
			`foreign key (org, agent_id) references omixor_agents (org, id) on delete restrict`,
			`select count(*) from omixor_agent_tasks x where not exists (select 1 from omixor_agents y where y.org = x.org and y.id = x.agent_id)`, ""},
	}
	for _, t := range orgFKTables {
		l = append(l, fkSpec{"fk_" + t + "_org", t,
			`foreign key (org) references omixor_orgs (id) on delete cascade`,
			fmt.Sprintf(`select count(*) from %[1]s x where not exists (select 1 from omixor_orgs y where y.id = x.org)`, t), ""})
	}
	l = append(l, actorFKSpecs()...) // ISS-36:22 條「誰」欄位的外鍵(欄位轉換完成才加,見 fkSpecReady)
	for i := range l {
		if l[i].name == "fk_docs_project" || l[i].name == "fk_cabinet_sections_project" {
			l[i].col = "project_ref"
		}
	}
	return l
}

// Orphan 一種孤兒資料的筆數(外鍵補上之前,程式留下的「指向不存在的資料」)
type Orphan struct {
	Constraint string // 外鍵名稱
	Table      string
	Count      int64
}

// OrphanReport 每一條外鍵目前的孤兒筆數(唯讀;用來在補外鍵之前看資料有沒有問題)
func (p *PG) OrphanReport() ([]Orphan, error) {
	c, cancel := ctx()
	defer cancel()
	var out []Orphan
	for _, f := range fkSpecs() {
		if !p.fkSpecReady(c, f) { // actor 欄位還沒轉換完成:欄位裡還是舊字串(或欄位還沒改名),孤兒數沒有意義
			continue
		}
		var n int64
		if err := p.pool.QueryRow(c, f.orphan).Scan(&n); err != nil {
			return nil, fmt.Errorf("%s:%w", f.name, err)
		}
		out = append(out, Orphan{f.name, f.table, n})
	}
	return out, nil
}

// OrphanSQL 每一條外鍵的孤兒檢查 SQL(constraint 名稱 → SQL),給正式資料庫補外鍵前手動跑
func OrphanSQL() map[string]string {
	m := map[string]string{}
	for _, f := range fkSpecs() {
		m[f.name] = f.orphan
	}
	return m
}

func fkExists(c context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, table, name string) (exists, validated bool, err error) {
	err = q.QueryRow(c, `select true, convalidated from pg_constraint where conname = $1 and conrelid = $2::regclass`, name, table).Scan(&exists, &validated)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	return
}

// pgBackground 背景工作的控制:取消與「做完了」的通知
type pgBackground struct {
	cancel context.CancelFunc
	done   chan struct{} // 背景工作整個結束(含之後的重試輪)
	round1 chan struct{} // 第一輪(validate)做完
}

// stopBackground 取消背景工作(外鍵 validate)並等它結束(最多 10 秒);Close 呼叫
func (p *PG) stopBackground() {
	if p == nil || p.bg == nil {
		return
	}
	p.bg.cancel()
	select {
	case <-p.bg.done:
	case <-time.After(10 * time.Second):
	}
}

// WaitFKValidate 等背景的外鍵 validate 第一輪跑完(沒有背景工作就立刻回);主要給測試用。
// 之後的重試輪(補不上的外鍵、被鎖擋住的 validate)不等。服務不需要等它:validate 沒跑完時外鍵是 not valid,新寫入照樣受約束
func (p *PG) WaitFKValidate() {
	if p == nil || p.bg == nil {
		return
	}
	<-p.bg.round1
}

// 外鍵遷移的每一步各用一個小交易,先 set local lock_timeout:拿不到鎖就放棄這一次、稍後(隨機退避)重試,不長時間擋住線上的讀寫。
// 單次 lock_timeout 短(2 秒),重試次數多,但一步(含退避)的總等待有上限 fkStepMax。變數是為了測試能縮短
var (
	fkLockTimeout = "2s"
	fkRetries     = 8
	fkRetryWait   = time.Second
	// fkStepMax 一步(含重試的退避)的等鎖總時間上限:超過就不再重試
	fkStepMax = 60 * time.Second
	// fkValidateTimeout 背景 validate 每一條外鍵的逾時
	fkValidateTimeout = 30 * time.Minute
)

func isLockErr(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (pe.Code == "55P03" || pe.Code == "40P01" || pe.Code == "40001")
}

// fkStep 在一個小交易裡做一步(set local lock_timeout);鎖逾時或死鎖就隨機退避後重試,最多 fkRetries 次、總等待不超過 fkStepMax
func (p *PG) fkStep(c context.Context, fn func(tx pgx.Tx) error) error {
	var err error
	stop := time.Now().Add(fkStepMax)
	for attempt := 0; attempt < fkRetries; attempt++ {
		if attempt > 0 {
			wait := jitter(fkRetryWait * time.Duration(attempt))
			if time.Now().Add(wait).After(stop) {
				return err
			}
			select {
			case <-c.Done():
				return c.Err()
			case <-time.After(wait):
			}
		}
		err = func() error {
			tx, err := p.pool.Begin(c)
			if err != nil {
				return err
			}
			defer tx.Rollback(c)
			if _, err := tx.Exec(c, fmt.Sprintf(`set local lock_timeout = '%s'`, fkLockTimeout)); err != nil {
				return err
			}
			if err := fn(tx); err != nil {
				return err
			}
			return tx.Commit(c)
		}()
		if err == nil || !isLockErr(err) {
			return err
		}
	}
	return err
}

// ensureSimpleIndex 用 create index concurrently 建索引(不擋寫入,不能在交易裡);已存在但 invalid(上次 concurrently 失敗)就先 drop 重建
func ensureSimpleIndex(c context.Context, q seqConn, name, table, cols string) error {
	var valid bool
	err := q.QueryRow(c, `select i.indisvalid from pg_index i where i.indexrelid = to_regclass($1)`, name).Scan(&valid)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return err
	case valid:
		return nil
	default:
		if _, err := q.Exec(c, fmt.Sprintf(`drop index concurrently if exists %s`, name)); err != nil {
			return err
		}
	}
	if _, err := q.Exec(c, fmt.Sprintf(`create index concurrently if not exists %s on %s (%s)`, name, table, cols)); err != nil {
		_, _ = q.Exec(context.Background(), fmt.Sprintf(`drop index concurrently if exists %s`, name))
		return err
	}
	return nil
}

func (p *PG) columnExists(c context.Context, table, column string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(c, `select exists (select 1 from information_schema.columns
		where table_schema = current_schema() and table_name = $1 and column_name = $2)`, table, column).Scan(&ok)
	return ok, err
}

// migrateForeignKeys 啟動時補外鍵(見檔頭)。除了拿不到連線以外,任何一步失敗都只寫 log、不讓啟動失敗:
//
//   - 多個實例同時啟動:用 session 層級的 advisory lock 排隊(等鎖有獨立上限,等不到就略過這一輪),只保護「同時只有一個實例在跑遷移」,
//     不包住整段 DDL 的交易;
//   - 每個 alter / add constraint 各自一個小交易 + lock_timeout + 有限次重試,不會長時間擋住線上讀寫,也沒有跨表持鎖的死鎖;
//   - project_ref 欄位已經存在就完全不下 ALTER(add column if not exists 即使欄位已存在也會先取 ACCESS EXCLUSIVE 鎖);
//     欄位還沒有:表(含索引)超過大小門檻(maxAutoRewriteBytes)就不自動做、只警告(要在維護窗口跑 sql/premigrate.sql),
//     沒超過才在單步逾時(migrateStepTimeout)內做;
//   - validate 與「補不上的外鍵」在背景做(NewPG 不等它,見 startFKValidate),每條外鍵獨立逾時、錯誤只 log;PG 關閉時取消。
//
// 大表上線建議:project_ref 是 STORED 產生欄位,新增時整張表重寫、取 ACCESS EXCLUSIVE 鎖。omixor_docs 很大時,
// 請先在維護窗口用與服務相同的資料庫角色執行 sql/premigrate.sql(它包含下面兩句;之後啟動就不會再 ALTER):
//
//	alter table omixor_docs add column if not exists project_ref text generated always as (nullif(project, '')) stored;
//	alter table omixor_cabinet_sections add column if not exists project_ref text generated always as (nullif(project, '')) stored;
func (p *PG) migrateForeignKeys() error {
	c, cancel := p.migCtx(15 * time.Minute)
	defer cancel()
	// 排隊:session 層級 advisory lock(獨立一條連線),做完才放
	unlock, ok, err := p.sessionLock(c, "omixor-fk-migrate", "外鍵遷移")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer unlock()
	// 1. 頻道的 issue_id 改成可為 NULL('' → NULL)
	var nullable string
	if err := p.pool.QueryRow(c, `select is_nullable from information_schema.columns
		where table_schema = current_schema() and table_name = 'omixor_channels' and column_name = 'issue_id'`).Scan(&nullable); err != nil {
		return fmt.Errorf("查 omixor_channels.issue_id:%w", err)
	}
	if nullable == "NO" {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, `alter table omixor_channels alter column issue_id drop not null, alter column issue_id drop default`)
			return err
		}); err != nil {
			log.Printf("警告:omixor_channels.issue_id 改成可為 NULL 失敗(頻道的 issue_id 外鍵暫時加不上):%v", err)
		}
	}
	var blanks int
	if err := p.pool.QueryRow(c, `select count(*) from (select 1 from omixor_channels where issue_id = '' limit 1) x`).Scan(&blanks); err == nil && blanks > 0 {
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, `update omixor_channels set issue_id = null where issue_id = ''`)
			return err
		}); err != nil {
			log.Printf("警告:omixor_channels.issue_id '' 改成 NULL 失敗:%v", err)
		}
	}
	// 2. 產生欄位 project_ref(空字串 = 未分專案 → NULL,外鍵不檢查);欄位已存在就不下 ALTER
	for _, t := range projectRefTables {
		ok, err := p.columnExists(c, t, "project_ref")
		if err != nil {
			log.Printf("警告:查 %s.project_ref 失敗:%v", t, err)
			continue
		}
		if ok {
			continue
		}
		if !p.rewriteAllowed(c, t, t+".project_ref") { // 重寫整張表:大表不自動做
			continue
		}
		sc, scancel := context.WithTimeout(c, migrateStepTimeout)
		t1 := time.Now()
		err = p.fkStep(sc, func(tx pgx.Tx) error {
			_, err := tx.Exec(sc, fmt.Sprintf(`alter table %s add column if not exists %s`, t, projectRefColumnDef))
			return err
		})
		scancel()
		if err != nil {
			log.Printf("警告:%s.project_ref 加不上去(相關外鍵暫時沒有;大表請在維護窗口先跑 sql/premigrate.sql,見 pg_fk.go):%v", t, err)
			continue
		}
		log.Printf("[遷移] %s.project_ref 新增完成(%v)", t, time.Since(t1).Round(time.Millisecond))
	}
	// 2b. omixor_docs (org, project_ref) 索引:刪專案的 restrict 檢查、孤兒檢查、依專案查文件都用得到。concurrently 不擋寫入;
	// 失敗留下的 invalid 索引先刪掉重建;失敗只 log
	if ok, err := p.columnExists(c, "omixor_docs", "project_ref"); err == nil && ok {
		if err := ensureSimpleIndex(c, p.pool, "omixor_docs_org_project_ref", "omixor_docs", "org, project_ref"); err != nil {
			log.Printf("警告:建立索引 omixor_docs_org_project_ref 失敗(只影響效能):%v", err)
		}
	}
	// 3. 孤兒報告(只在還有外鍵沒加或沒驗證時寫,免得每次啟動都吵;在任何 DDL 交易之外,錯誤只 log)
	specs := fkSpecs()
	pending := false
	for _, f := range specs {
		if !p.fkSpecReady(c, f) {
			continue
		}
		ex, ok, err := fkExists(c, p.pool, f.table, f.name)
		if err != nil {
			log.Printf("警告:查外鍵 %s 失敗:%v", f.name, err)
			continue
		}
		if !ex || !ok {
			pending = true
			break
		}
	}
	if pending {
		var parts []string
		for _, f := range specs {
			if !p.fkSpecReady(c, f) {
				continue
			}
			var n int64
			if err := p.pool.QueryRow(c, f.orphan).Scan(&n); err != nil {
				log.Printf("警告:孤兒檢查 %s 失敗:%v", f.name, err)
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%d", f.name, n))
		}
		log.Printf("外鍵孤兒檢查(指向不存在資料的筆數):%s", strings.Join(parts, " "))
	}
	// 4. 加外鍵(not valid):每條一個小交易
	for _, f := range specs {
		if isActorFK(f.name) { // actor 外鍵只由 migrateActorColumns 在驗證過欄位之後加(pg_actor_columns.go),這裡不加
			continue
		}
		ex, _, err := fkExists(c, p.pool, f.table, f.name)
		if err != nil {
			log.Printf("警告:查外鍵 %s 失敗:%v", f.name, err)
			continue
		}
		if ex {
			continue
		}
		if err := p.fkStep(c, func(tx pgx.Tx) error {
			_, err := tx.Exec(c, fmt.Sprintf(`alter table %s add constraint %s %s not valid`, f.table, f.name, f.def))
			return err
		}); err != nil {
			log.Printf("警告:外鍵 %s 加不上去(服務照常啟動,這條約束暫時沒有):%v", f.name, err)
		}
	}
	// 5. 驗證:移到背景;啟動流程還沒跑完(ready = false)先記著,由 finishStartup 開跑
	p.fkPending = specs
	if p.ready {
		p.startFKValidate(specs)
	}
	return nil
}

// startFKValidate 背景處理外鍵(NewPG 不等):每條各自逾時(fkValidateTimeout);fkExists 或 validate 失敗只 log 並繼續下一條;
// PG 關閉(Close)時取消。重複呼叫時等前一輪結束再開下一輪。
//
// 第一輪只做 validate(啟動遷移剛試過補外鍵,不立刻再試一次);之後每隔 fkBackgroundRetryEvery 再來一輪:
// 還沒加上的外鍵(被長交易的鎖擋住)補上,被鎖擋住的 validate 重試。有孤兒(23503)的 validate 不重試(要人處理資料);
// 沒有「值得再試」的項目時背景就結束
func (p *PG) startFKValidate(specs []fkSpec) {
	if p.root == nil {
		return
	}
	p.stopBackground()
	bctx, cancel := context.WithCancel(context.Background())
	bg := &pgBackground{cancel: cancel, done: make(chan struct{}), round1: make(chan struct{})}
	p.bg = bg
	go func() {
		defer close(bg.done)
		todo := p.fkRound(bctx, specs, false)
		close(bg.round1)
		for len(todo) > 0 {
			select {
			case <-bctx.Done():
				return
			case <-time.After(jitter(fkBackgroundRetryEvery)):
			}
			todo = p.fkRound(bctx, todo, true)
		}
	}()
}

// fkRound 背景的一輪:addMissing 時缺的外鍵(欄位在)先補上(not valid);沒驗證的 validate。回傳下一輪還要試的外鍵(暫時性失敗:鎖)
func (p *PG) fkRound(bctx context.Context, specs []fkSpec, addMissing bool) (retry []fkSpec) {
	for _, f := range specs {
		if bctx.Err() != nil {
			return nil
		}
		c, ccancel := context.WithTimeout(bctx, fkValidateTimeout)
		ex, ok, err := fkExists(c, p.pool, f.table, f.name)
		if err != nil {
			ccancel()
			if bctx.Err() == nil {
				log.Printf("警告:查外鍵 %s 失敗,略過驗證:%v", f.name, err)
			}
			continue
		}
		if !ex {
			if !p.colOK(c, f) { // 欄位還沒有(由遷移或維護窗口腳本建):不補、不重試
				ccancel()
				continue
			}
			if !addMissing { // 第一輪不補(啟動遷移剛試過),留給之後的輪
				retry = append(retry, f)
				ccancel()
				continue
			}
			err := p.fkStep(c, func(tx pgx.Tx) error {
				_, err := tx.Exec(c, fmt.Sprintf(`alter table %s add constraint %s %s not valid`, f.table, f.name, f.def))
				return err
			})
			if err != nil {
				ccancel()
				if bctx.Err() != nil {
					return nil
				}
				log.Printf("警告:外鍵 %s 背景補上失敗:%v", f.name, err)
				if isLockErr(err) {
					retry = append(retry, f)
				}
				continue
			}
			log.Printf("[遷移] 外鍵 %s 已在背景補上", f.name)
			ok = false
		}
		if ok {
			ccancel()
			continue
		}
		for attempt := 0; attempt < 3; attempt++ { // 和線上寫入或別的 DDL 互等被判死鎖時重試
			_, err = p.pool.Exec(c, fmt.Sprintf(`alter table %s validate constraint %s`, f.table, f.name))
			if err == nil || !isLockErr(err) || c.Err() != nil {
				break
			}
			time.Sleep(jitter(time.Duration(attempt+1) * 500 * time.Millisecond))
		}
		ccancel()
		if err == nil || bctx.Err() != nil {
			continue
		}
		var pe *pgconn.PgError
		switch {
		case errors.As(err, &pe) && pe.Code == "23503":
			log.Printf("警告:外鍵 %s 驗證失敗,既有資料有孤兒(外鍵維持 not valid,新寫入仍受約束;不會自動刪資料,請先處理孤兒):%v", f.name, pe.Message)
		case isLockErr(err):
			log.Printf("警告:外鍵 %s 驗證被鎖擋住(%v 後在背景再試):%v", f.name, fkBackgroundRetryEvery, err)
			retry = append(retry, f)
		default:
			log.Printf("警告:外鍵 %s 驗證失敗(稍後重啟會再試):%v", f.name, err)
		}
	}
	return retry
}

// colOK 外鍵需要的產生欄位是否存在(沒有要求欄位的外鍵視為存在)
func (p *PG) colOK(c context.Context, f fkSpec) bool {
	if isActorFK(f.name) { // actor 外鍵不在背景補(只由 migrateActorColumns 驗證欄位後加),背景只 validate 已存在的
		return false
	}
	if f.col == "" {
		return true
	}
	ok, err := p.columnExists(c, f.table, f.col)
	return err == nil && ok
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

// finishStartup NewPG 的遷移都做完了:開始背景 validate(不等它跑完)
func (p *PG) finishStartup() {
	p.ready = true
	if p.fkPending != nil {
		p.startFKValidate(p.fkPending)
	}
}

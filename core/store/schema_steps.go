package store

// schema_steps.go 有版本的資料庫結構異動步驟。執行器在 pg_schema_boot.go(bootstrapSchema、runSteps)。
//
// 全新(空)資料庫直接建 pgSchema + fkSpecs 的最終結構,並把 baseline 與所有步驟的版本記進 omixor_schema_version(不執行步驟)。
// 已經在跑的資料庫(正式站、使用者本機的 PostgreSQL)啟動時讀出目前最大版本,依序執行版本更大的步驟;
// 沒有版本表的舊資料庫先蓋上 baseline(版本 1)再往下走。所以「新增欄位、表、索引」一定要走這裡,
// 只改 pgSchema 會讓全新資料庫有、已經在跑的資料庫沒有。
//
// 怎麼新增一個結構異動:
//
//  1. 在 schemaSteps 末尾加一筆:Version = 目前最大版本 + 1(第一個真正的步驟是 2;1 保留給 baseline),Name 簡短(例如 "issues_found_in")。
//  2. 同時修改 pgSchema(pg_schema.go;全新資料庫直接建出最終結構)與 fkSpecs(pg_fk.go,若有外鍵)。
//  3. 用 cd core && OMIXOR_UPDATE_SQL=1 go test -run TestSchemaManifestInSync ./store -count=1 重新產生 schema_manifest.json
//     (第一次產生時測試可能自己失敗一次,再跑一次確認通過),看 diff 只有預期的異動。
//  4. TestSchemaStepsEquivalent(schema_baseline_test.go)會用凍結的 baseline 建出舊結構、套用所有步驟,必須和全新安裝完全一致;
//     TestSchemaStepsRules 會擋掉不合規則的步驟。baseline 檔(schema_baseline_*_test.go)是固定副本,不要改。
//
// 規則:
//   - SQL 必須冪等:add column if not exists、create index if not exists、create table if not exists、add constraint 前先 drop constraint if exists 等。
//   - 必須向後相容於上一個版本的程式:滾動部署時舊實例還在跑,所以不可以改名、刪除、改型別(守門測試會擋 drop、rename、alter column ... type 開頭的語句)。
//     新增的欄位要可為 NULL 或有常數預設值,舊程式的 insert 才不會壞。要刪除分兩個版本:先停用(程式不再讀寫),下一版再刪。
//   - 每個步驟在自己的交易裡執行(set local lock_timeout = '10s',鎖逾時或死鎖會退避重試幾次),成功才寫入版本記錄;一個步驟一個交易,不要一次塞很多事。
//   - 多個實例同時啟動靠 advisory lock 排隊;拿到鎖後重新讀版本,別的實例剛做完的步驟會略過。
//   - 會重寫整張大表的操作(加 STORED 產生欄位、改型別、加 not null 且需要驗證、加有 volatile 預設值的欄位)會在重寫期間以 ACCESS EXCLUSIVE 鎖住該表,
//     大表可能鎖很久甚至逾時回滾。這類步驟要在維護窗口先用 psql 手動跑同一句(不可加 -1 單一交易;先 set lock_timeout = '5s';每句各自 autocommit),
//     步驟本身寫成 add column if not exists 等,手動跑過之後啟動時就是 no-op,不會再重寫。
//     加欄位若是可為 NULL 或常數預設值(PostgreSQL 11 以後)不會重寫表,不在此限。
//   - 一次性的資料轉換(拆表、回填大量資料)不放在這裡;結構步驟只做 DDL(必要時搭配小量的冪等 update)。

// baselineVersion 版本 1:導入版本表之前就存在的資料庫結構(凍結在 schema_baseline_*_test.go)
const baselineVersion = 1

// schemaVersionTable 版本表名稱
const schemaVersionTable = "omixor_schema_version"

// schemaVersionDDL 版本表的建表語句(接在 pgSchema 最後;舊資料庫由執行器用同一句補建)。冪等
const schemaVersionDDL = `
create table if not exists omixor_schema_version (
  version integer primary key,
  name text not null,
  applied_at timestamp with time zone not null default now()
);
`

// schemaStep 一個結構異動步驟
type schemaStep struct {
	Version int    // 嚴格遞增、不重複,從 2 開始
	Name    string // 簡短名稱(寫進版本表、錯誤訊息)
	SQL     string // 冪等、向後相容的 DDL;可以有多個語句,在同一個交易裡執行
}

// schemaSteps 依版本遞增排列。步驟 2 是 commits(ISS-69),步驟 3 是 Issue 表改名,步驟 4 保留既有組織的功能開關
var schemaSteps = []schemaStep{
	// 2:commits(ISS-69)。新增三張表、索引與外鍵,SQL 和全新安裝共用(schema_commits.go)
	{Version: 2, Name: "commits", SQL: commitsStepSQL},
	// 3:最後兩張舊名的表改成 omixor_ 開頭(ops_issues、ops_issue_comments),連同約束與索引。不向後相容、不能滾動部署(schema_rename_issues.go)
	{Version: 3, Name: "rename_issues_tables", SQL: renameIssuesSQL},
	// 4:旁白發佈、網站訪客改成預設關,既有組織補寫「開」(不改結構;schema_keep_features.go)
	{Version: 4, Name: "keep_voice_visitors_for_existing_orgs", SQL: keepVoiceVisitorsSQL},
}

// knownSchemaVersion 程式已知的最大版本 = max(baseline, 步驟版本)
func knownSchemaVersion(steps []schemaStep) int {
	v := baselineVersion
	for _, s := range steps {
		if s.Version > v {
			v = s.Version
		}
	}
	return v
}

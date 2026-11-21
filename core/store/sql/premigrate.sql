-- 維護窗口預先執行的腳本(冪等,可重複執行)。檔案:core/store/sql/premigrate.sql(舊名 principals_pre.sql 已改名)。
--
-- 這個檔案由程式產生(core/store/pg_premigrate.go 的 premigrateSQL,測試驗證與 projectRefTables 一致),請勿手改;
-- 改了之後用 OMIXOR_UPDATE_SQL=1 go test -run TestPreMigrateSQLInSync ./store 重新產生。
--
-- 涵蓋範圍:omixor_docs、omixor_cabinet_sections、ops_issues 的 project_ref 產生欄位(前置:補 project 欄位)。
--
-- 用途:新增 STORED 產生欄位會整張表重寫、持有 ACCESS EXCLUSIVE 鎖直到該句結束(大表可能很久,期間該表讀寫全被擋)。
-- 程式啟動時也會做同樣的事,但表超過大小門檻(OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB,預設 1024 MB)就不會自動做、只警告,
-- 所以大表要先在維護窗口跑這份腳本;之後啟動發現欄位都在,就整段跳過、不再下 ALTER。
--
-- 執行方式(重要):
--   1. 用與服務相同的資料庫角色(表的擁有者)執行。
--   2. 用 psql 逐句執行:
--        psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f premigrate.sql
--      不可以加 -1(--single-transaction):單一交易會把每張表的 ACCESS EXCLUSIVE 鎖一路持有到整份腳本結束。
--      不加 -1 時每句 alter 各自是一個 autocommit 交易,做完立刻放鎖。
--   3. 每句 alter 前的 set lock_timeout = '5s'(session 層級)讓拿不到鎖(有長交易持鎖)時 5 秒就失敗;
--      ON_ERROR_STOP 之後 psql 結束、連線關閉,設定不會殘留。失敗就等長交易結束後重跑整份:做完的句子不會再重寫(add column if not exists)。
--      最後的 reset lock_timeout 只在整份成功跑完時執行。
--   4. 前提:資料表已經建好(舊版程式至少啟動過一次)。

-- project 欄位(project_ref 的來源):舊結構可能還沒有,先補(常數預設值,不重寫表)
set lock_timeout = '5s';
alter table omixor_docs add column if not exists project text not null default '';
set lock_timeout = '5s';
alter table omixor_cabinet_sections add column if not exists project text not null default '';
set lock_timeout = '5s';
alter table ops_issues add column if not exists project text not null default '';

-- project_ref:空字串 = 未分專案 → NULL(外鍵用)。每張表一句 alter,會重寫整張表;omixor_docs、ops_issues 通常最大
set lock_timeout = '5s';
alter table omixor_docs
  add column if not exists project_ref text generated always as (nullif(project, '')) stored;

set lock_timeout = '5s';
alter table omixor_cabinet_sections
  add column if not exists project_ref text generated always as (nullif(project, '')) stored;

set lock_timeout = '5s';
alter table ops_issues
  add column if not exists project_ref text generated always as (nullif(project, '')) stored;

reset lock_timeout;

# 資料表改名:ops_ → omixor_

狀態:已實作(結構異動步驟 3 `rename_issues_tables`)。**部署會改正式資料庫的結構,部署前請先備份 Cloud SQL**(備份與部署由使用者執行,程式與這份文件都不做)。

## 1. 改了什麼

### core(正式站與本機核心共用,`core/store`)

| 舊名 | 新名 |
|---|---|
| `ops_issues` | `omixor_issues` |
| `ops_issue_comments` | `omixor_issue_comments` |
| 約束 `ops_issues_pkey`、`ops_issues_priority_check`、`ops_issues_status_check` | `omixor_issues_*` |
| 外鍵 `fk_ops_issues_org`、`_created_by`、`_assignee`、`_deleted_by` | `fk_omixor_issues_*` |
| 約束 `ops_issue_comments_pkey`、`ops_issue_comments_org_issue_id_fkey` | `omixor_issue_comments_*` |
| 外鍵 `fk_ops_issue_comments_author` | `fk_omixor_issue_comments_author` |
| 索引 `ops_issues_org_key`、`ops_issues_org_number`、`ops_issues_org_pos` | `omixor_issues_*` |

主鍵約束改名時,PostgreSQL 會連同背後的索引一起改名。其他表對 Issue 表的外鍵(`omixor_issue_items`、`omixor_issue_keys`、`omixor_issue_commits`、`omixor_decisions`、`omixor_channels`)由 PostgreSQL 在表改名時自動指到新表,外鍵名稱本身不含舊名,不用動。沒有序號、觸發器、函式、檢視帶舊名。

- 定義:`core/store/schema_rename_issues.go`(改名清單與 SQL),版本 3、名稱 `rename_issues_tables`,登記在 `core/store/schema_steps.go`。
- 全新資料庫:`pgSchema`、`fkSpecs`、`ActorColumns` 直接用新名字,版本表一次記到 3,不執行步驟。
- 步驟 2(commits)的 SQL 在舊資料庫執行時 Issue 表還叫 `ops_issues`,所以 `commitsStepSQL` 把 `omixor_issues` 換回舊名;步驟 3 之後外鍵自然跟著指到新名。
- 步驟本身冪等:每個物件「舊名存在、新名不存在」才改;舊名不存在略過;**新舊同時存在就報錯(不覆蓋、整個步驟回滾)**,由人處理。
- 凍結的 baseline 測試資料(`schema_baseline_*_test.go`)仍是舊名,用來測「舊結構 + 步驟 = 全新安裝」。

### console 本機存放處(`console/internal/core/store_pg.go`)

`ops_documents`、`ops_secrets`、`ops_sessions`、`ops_activity`、`ops_state`、`ops_files` → `omixor_*`,連同主鍵、`ops_sessions_expires`、`ops_activity_at` 索引、`ops_activity_seq_seq` 序號(清單 `pgLegacyRenames`)。
啟動時(`OpenDB`、`OpenIssueDB` → `ensurePGSchema`)先做一個 DO 區塊(單一交易:舊名在、新名不在才改;新舊同時存在報錯),再 `create table if not exists`。console 沒有版本表,靠這個冪等的改名。

沒有改的:環境變數 `OPS_*` 的舊版相容處理、cookie 名 `ops_session`、MCP 的舊工具別名 `ops_issues`(Swift 測試)——都不是資料表。

## 2. 部署順序與限制

1. **使用者先備份 Cloud SQL**(確認可還原)。
2. **停掉所有舊版實例**,再啟動新版(不能滾動部署)。新版啟動時在 advisory lock 下執行步驟 3,改名只需要短暫的 ACCESS EXCLUSIVE 鎖(中繼資料操作,不重寫資料,步驟的 `lock_timeout` 是 10 秒,逾時會退避重試)。
3. 啟動後用 `schema_check`(MCP 或 `CheckSchema`)確認 `ok`、版本 3。

### 舊版程式在改名之後會壞

這是破例的步驟(`stepRuleWhitelist` 登記了 3):上一版程式的 SQL 寫死 `ops_issues`,表一改名,所有 Issue 讀寫、發號、刪除組織都會失敗(`relation "ops_issues" does not exist`)。所以:

- 新版只要有一個實例啟動、跑完步驟 3,仍在跑的舊版實例就立刻壞。**部署要先停舊的再起新的**,或接受短暫的錯誤期。
- 資料庫版本比程式新時,舊版只會警告後照常啟動(`runSteps`),**不會**自己失敗在啟動階段,而是之後第一個查詢才壞;回滾程式版本時一定要同時回滾資料庫(下一節)。

### 為什麼不建相容檢視(`ops_issues` view)

評估後**不建議**:
- 檢視能讓簡單的 select/insert/update/delete 繼續動,但舊版程式靠 `ON CONFLICT`、`for update`、`returning`、`omixor_issue_keys` 等的行為與約束名稱(例如 `ops_issues_pkey`、`fk_ops_issues_*` 的錯誤判斷)不保證一致,而且 `project_ref` 是產生欄位,透過檢視寫入的語意要逐一驗證。
- 檢視會讓 `omixor` 啟動時的「新舊同時存在」防護、schema 比對(`schema_check`、manifest)、`information_schema` 的表統計都要特例處理,留下之後要清掉的暫時物件。
- 滾動部署時舊實例與新實例的混合期仍然有風險(舊實例對新結構的假設不成立),相容檢視只是掩蓋問題。
所以採用「停機部署 + 備份 + 明確回滾」。

## 3. 回滾

先停掉所有新版實例,再對資料庫執行(都在交易裡;以下是 core):

```sql
begin;
alter table omixor_issues rename to ops_issues;
alter table omixor_issue_comments rename to ops_issue_comments;
alter table ops_issues rename constraint omixor_issues_pkey to ops_issues_pkey;
alter table ops_issues rename constraint omixor_issues_priority_check to ops_issues_priority_check;
alter table ops_issues rename constraint omixor_issues_status_check to ops_issues_status_check;
alter table ops_issues rename constraint fk_omixor_issues_org to fk_ops_issues_org;
alter table ops_issues rename constraint fk_omixor_issues_created_by to fk_ops_issues_created_by;
alter table ops_issues rename constraint fk_omixor_issues_assignee to fk_ops_issues_assignee;
alter table ops_issues rename constraint fk_omixor_issues_deleted_by to fk_ops_issues_deleted_by;
alter table ops_issue_comments rename constraint omixor_issue_comments_pkey to ops_issue_comments_pkey;
alter table ops_issue_comments rename constraint omixor_issue_comments_org_issue_id_fkey to ops_issue_comments_org_issue_id_fkey;
alter table ops_issue_comments rename constraint fk_omixor_issue_comments_author to fk_ops_issue_comments_author;
alter index omixor_issues_org_key rename to ops_issues_org_key;
alter index omixor_issues_org_number rename to ops_issues_org_number;
alter index omixor_issues_org_pos rename to ops_issues_org_pos;
delete from omixor_schema_version where version = 3;
commit;
```

然後部署上一版程式。若是整庫還原,直接用步驟 1 的備份還原即可。如果沒有刪掉版本 3 的記錄就回滾程式,舊版會因為版本較新而略過所有步驟,但表名對不上。再升級時步驟 3 會重新執行(冪等)。

console 本機存放處回滾:對 `pgLegacyRenames` 的每一項倒著 `alter table/index/sequence <新名> rename to <舊名>`(同一份清單,新名 → 舊名)。注意:改名之後如果又啟動過舊版程式,它會用 `create table if not exists ops_*` 建出空的舊表,下次啟動新版會因「新舊同時存在」報錯停住;先確認哪邊是有資料的、把空的那邊 `drop` 掉再啟動。

## 4. 測試

- `core/store/schema_rename_issues_test.go`:舊結構 + 步驟 2 + 資料 → 啟動 → 新名字、資料、外鍵(含 `omixor_issue_commits`)、續號、新增、刪除連帶、冪等、新舊同時存在報錯;全新資料庫沒有任何 `ops_` 物件。
- `TestSchemaStepsEquivalentToFreshInstall`:凍結 baseline + 所有步驟的結構(含約束與索引名稱)必須與全新安裝完全一致,所以改名清單有漏就會失敗。
- `TestSchemaManifestInSync`:`schema_manifest.json` 已重新產生。
- `console/internal/core/store_pg_test.go` 的 `TestOpenDBRenamesLegacyOpsTables`:舊名資料庫 → `OpenDB` → 新名、資料、序號、冪等、新舊同時存在報錯。

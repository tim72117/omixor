# 資料庫這一輪改動的上線前檢查清單(2026-11)

> 狀態:草稿,對應「工作樹尚未提交」的那一批改動。本文只是檢查清單,不含任何會改資料的自動步驟。
> **這一版不可回滾:唯一的退路是從升級前的備份還原。** 推送到 `main` 會自動部署到 Cloud Run,並在啟動時對正式 Cloud SQL 執行全部遷移。
>
> 約定:`<INSTANCE>`、`<PROJECT>`、`<REGION>`、`<SERVICE>`、`<DB>`、`<BUCKET>`、`<REHEARSAL_URL>` 都是佔位,請在自己的終端機填入真實值,不要把真實值寫回這份文件或貼進 issue。
> 引用格式「檔案:函式」;`MIGRATIONS.md` 指 `core/store/MIGRATIONS.md`,本文不複製它的 SQL,只指向章節。
> 標「待確認」的項目:文件作者在程式或設定裡找不到直接依據,需要有權限的人確認後再勾。

---

## 1. 這一版改了什麼(給維運的一頁摘要)

### 1.1 結構變更

| 類別 | 內容 | 來源 |
|---|---|---|
| 檔案版 store 移除 | `core/store/file*.go` 刪除;本機守護(`omixor-local`)改為一律走 PostgreSQL。舊 `store.json` **不遷移、不匯入** | `server/README.md`、`git status` |
| 本機守護內建 PG | 守護服務預設自己起內建 PostgreSQL 17(只聽 127.0.0.1、隨機埠),資料在 `<資料夾>/pgdata`;設了 `OMIXOR_DATABASE_URL` 就改連外部 | `server/embeddedpg.go`、`server/README.md` |
| 編號序號表 | 新表 `omixor_seq (org, scope, last)`;scope = issue / task / doc / channel / incident / `msg:<channel_id>`;`(org, number)` 部分唯一索引(`where number > 0`)6 條 | `pg_seq.go:migrateSeq`、`ensureSeqIndex`、`seqTables` |
| 文件櫃版本號 | `omixor_cabinet_layouts.version bigint not null default 0`(樂觀鎖,存檔帶 expected,不符回衝突) | `pg.go`(add column version)、`pg_cabinet.go`、`store.go:ErrVersionConflict` |
| jsonb 拆表 | 14 張新子表 + 8 張父表的 `split_at` + 8 個 `<表>_unsplit` 部分索引 + 事件純量欄位 | `MIGRATIONS.md` §2、`pg_split_*.go` |
| 外鍵 | 26 條(8 條關聯 + 18 張表的 org → `omixor_orgs`),先 `not valid` 再背景 validate | `pg_fk.go:fkSpecs`、`orgFKTables` |
| 身分登記層 | **不部署**:`omixor_principals`、3 個 SQL 規範化函式、19 個 `<欄位>_ref` 產生欄位、19 條 `fk_p_*` 外鍵都已從程式移除;ISS-36 直接進 actor 欄位(雲端 DOC-11)。Go 的規範化規則(`core/model/principal.go`)與「誰」的欄位清單(`core/store/identity_fields.go`)保留給 actor 遷移用 | `MIGRATIONS.md` §7 |
| Issue key | Issue 內部 id 換成 UUIDv7、原 `ISS-<n>` 變成 key;`ops_issues` 新增 `project`、`key`、`version`、`deleted_at`、`deleted_by` 與 `project_ref` 產生欄位;新表 `omixor_issue_keys`(key、舊 id 別名)。細節見 1.5 | `pg_issue_keys.go:migrateIssueKeys`、`pg.go` |
| 其他 | `omixor_channels.issue_id` 改成可為 NULL(`'' → NULL`);`omixor_docs`、`omixor_cabinet_sections`、`ops_issues` 的 `project_ref` 產生欄位;索引 `omixor_docs_org_project_ref`;角色刪除改為「名下有任務就擋」(restrict),需先轉移任務 | `pg_fk.go:migrateForeignKeys` |

### 1.2 會重寫整張表的遷移(要在備份副本上量時間)

`alter table … add column … generated always as (…) stored` 在 PostgreSQL 會**重寫整張表(連同索引重建)並持有 ACCESS EXCLUSIVE 鎖**。這一版只剩 `project_ref`(`project` 的產生欄位,外鍵用)三張表:

| 來源 | 表 | 產生欄位 | 備註 |
|---|---|---|---|
| `pg_fk.go:migrateForeignKeys`(步驟 2) | `omixor_docs` | `project_ref` | 文件含 body,通常是最大的表 |
| 同上 | `omixor_cabinet_sections` | `project_ref` | 通常很小 |
| 同上 | `ops_issues` | `project_ref` | ISS-34;Issue 表,另有 `project`、`key`、`version`、`deleted_at`、`deleted_by`(常數預設值,不重寫) |

其他 `add column … not null default '<常數>'`(例如 `ops_issues.project`、`omixor_docs.project`)在 PG 11 以上只改系統表、不重寫。

重點(以程式實際行為為準):

- **大表不自動重寫**:每次重寫前先查表大小(`pg_total_relation_size`,含索引與 TOAST)。超過門檻(設定 `OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB`,預設 **1024 MB**,0 = 不限制)就**不下 ALTER**,只寫一行警告「<表> 約 <大小>,自動重寫可能鎖表很久;請在維護窗口先跑 sql/premigrate.sql 再重啟」,該表的 `project_ref` 外鍵晚點補,並記入啟動結尾的彙總 log。查不出表大小時也保守地不自動做。
- 門檻預設值的依據(**估計,待在備份副本實測**):雲端託管 PG 的重寫連同索引重建約 10–50 MB/s,1 GiB 約 20 秒到 2 分鐘,遠小於單步上限 10 分鐘;更大的表可能被擋數分鐘、甚至超過單步上限而回滾(回滾後下次啟動重來,每次都鎖)。門檻是「每張表」。
- **沒超過門檻的表**:獨立的小交易(`lock_timeout` 單次 2 秒、最多 8 次、隨機退避、一步等鎖總計不超過 60 秒),單步上限 10 分鐘(`migrateStepTimeout`),啟動整體預算 10 分鐘(`OMIXOR_MIGRATE_BUDGET_MINUTES`,所有階段共用,見 1.3);失敗或逾時只警告、不回錯,服務照常啟動,下次啟動接著做。
- 新增欄位前先查 `information_schema`:欄位已存在就**完全不下 ALTER**(`add column if not exists` 即使欄位已存在也會先取 ACCESS EXCLUSIVE 鎖)。

### 1.3 啟動時依序會做的事(`pg_migrate_guard.go:startupMigrations`,`NewPG`、`NewPGFromPool` 共用)

啟動遷移在 listen 之前**同步**執行,所有階段共用一個整體預算(預設 10 分鐘,`OMIXOR_MIGRATE_BUDGET_MINUTES`);每個階段自己的逾時與「剩餘預算」取較短。預算用完後,剩下的階段只警告、略過(不讓啟動失敗),下次啟動接著做。等任何遷移用的 advisory lock 最多 2 分鐘(`migrateLockWait`),超過就警告並略過那一段;釋放 session 鎖失敗時關閉那條連線,鎖不會殘留。

| 順序 | 步驟 | 失敗時 | 逾時 / 批次 |
|---|---|---|---|
| 1 | 建表(`pgSchema`) | 啟動失敗 | 15 秒 ctx |
| 2 | `migrateCabinetLayouts` → `migrateChannelSplit` → `migrateSplitDocsAgents` → `migrateSplitIncidentsTasks`(拆表) | 逾時或壞資料只記 `[遷移警告]`;結構性錯誤會失敗(整體預算用完時降為警告);一批遇到鎖逾時、死鎖會重試 4 次 | 階段 30 分鐘與剩餘預算取短;每批 500 列(`migrateBatchSize`) |
| 3 | `migrateForeignKeys`:issue_id 可為 NULL、`project_ref`(大表略過)、索引、孤兒報告、26 條 `not valid` 外鍵 | 單步失敗只記「警告」 | 階段 15 分鐘與剩餘預算取短;每步 `lock_timeout` 2 秒、最多 8 次(隨機退避,總等待 ≤ 60 秒) |
| 4 | `migrateIssueKeys`(ISS-34,見 1.4):Issue id 換 UUIDv7、key、別名表 | 失敗會讓啟動失敗(預算用完時降為警告) | 10 分鐘與剩餘預算取短 |
| 5 | `migrateSeq`:建序號表、初始化、唯一索引(`create unique index concurrently`) | 索引失敗只記警告 | 10 分鐘與剩餘預算取短 |
| 6 | `logMigrationSummary`:一行彙總(缺的 `project_ref`、缺的外鍵、需手動處理的大表);只查 `information_schema` 與 `pg_constraint`,不掃資料 | 查詢失敗只略過 | 30 秒 |
| 7 | `finishStartup`:開背景工作:26 條外鍵的 validate;補不上的外鍵與被鎖擋住的 validate **每隔約 5 分鐘**(`fkBackgroundRetryEvery`)重試,直到成功或服務關閉(`Close()`);有孤兒(23503)的 validate 不重試 | 只記警告 | 每條 validate 30 分鐘(`fkValidateTimeout`) |
| 8 | `server/server.go:Run`:`EnsureOrgsForIssues`、`StartRoutines`、然後才 `ListenAndServe` | | |

順序固定:`migrateForeignKeys` → `migrateIssueKeys` → `migrateSeq`。

**預期時間的未知數**:每一步的耗時取決於正式資料的列數與大小。**必須在備份副本上演練取得實際數字**(第 2.2 節),不要憑感覺估。伺服器在遷移完成**前不會 listen**(`Run` 先 `NewPG`),對 Cloud Run 而言是「容器還沒開始接受連線」:`deploy-server.yml` 沒有設 startup probe,所以預設的啟動探測時限必須大於整體預算,否則新 revision 會被判定失敗(預設值待確認;見 2.6)。

新設定(`docs/configuration.md`):`OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB`(預設 1024;0 = 不限制)、`OMIXOR_MIGRATE_BUDGET_MINUTES`(預設 10);`server/server.go` 啟動時用 `store.SetMigrationLimits` 套用(local 守護用預設值)。


### 1.4 Issue key 遷移(ISS-34 第一階段,`migrateIssueKeys`)

- **一次性、可重跑**:只處理 `key` 還是空字串的 Issue(舊資料;新建的一律有 key);做過的列不會再動。整個遷移在**一個交易**裡,持有 advisory lock(`omixor-issue-keys-migrate`,等鎖最多 2 分鐘,等不到略過這次),交易失敗整個還原。
- 每筆舊 Issue(依 `created_at`、號碼排序):新內部 id = 原本就是 UUID 的保留,否則用 `created_at` 產生 UUIDv7;key = 原 id 是 `ISS-<n>`(號碼 > 0)且沒被別人用掉就沿用,否則(例如 `gh-1` 這類非數字舊 id)發新的 `ISS-<n>`,原 id 留在別名表 `omixor_issue_keys`,照樣查得到;key 與原 id 都寫進別名表。
- 改寫參照:`ops_issue_comments`、`omixor_channels` 的外鍵(`on update cascade`;頻道那條外鍵還沒加上、或大小寫不同時顯式補改,不分大小寫);任務的相關連結 `omixor_agent_task_links.url` 等於原 id(不分大小寫)的改成新 id。其他文字裡的 `ISS-<n>`(動態牆、留言、事件後續工作、任務說明)不改,透過別名表查得到。
- **集合式實作**(取代舊版的逐筆改):id、key 在記憶體算好,放進暫存對照表,`ops_issues`、頻道、任務連結、別名表各用單句 `update … from` / `insert … select` 一次改完。舊版每改一張 Issue 就全表掃一次任務連結,時間是平方級。
- **實測耗時**(測試庫 PG 17、本機 docker;舊結構資料:2 萬 Issue、10 萬留言、2 萬任務(各 1 條連結)、2 千頻道;建法見 `MIGRATIONS.md` §10):Issue key 這一步 **約 238 秒 → 約 5.2 秒**;`NewPG` 整體(含其他遷移)約 308 秒 → 約 71 秒。預算 10 分鐘。
- **逾時或失敗**:整段回滾(單一交易),Issue 維持舊狀態,下次啟動(`key` 仍為空字串)整個重跑;不會留下做一半的資料。預算已用完時此步降為警告、服務照常啟動(見 1.3)。
- 做完會記一行 log:筆數、耗時、**孤兒數**(頻道綁的 Issue、長得像 Issue 編號的任務連結,找不到 Issue 的筆數;只統計,不改資料)。
- 舊的 `(org, number)` 唯一索引 `ops_issues_org_number` **不在這一步拿掉**:`migrateSeq` 的 `ensureSeqIndex` 依欄位比對,索引欄位不是 `(org, project, number)` 就先在 `ops_issues_org_number_new` 建好新索引,成功才拿掉舊的並改名(換索引過程中唯一性一直有保障;就算這個 key 遷移被跳過或逾時,專案內的 Issue 號碼也不會撞舊索引)。
- 順序:必須在 `migrateForeignKeys` 之後、`migrateSeq` 之前。`ops_issues` 因此在這一版多出 `project`、`key`、`version`、`deleted_at`、`deleted_by`(常數預設值,不重寫)與 `project_ref`(產生欄位,會重寫,已納入大小門檻與維護窗口腳本)。

---

## 2. 上線前(依序)

### 2.1 備份

- [ ] 確認 Cloud SQL 自動備份已啟用且最近一次成功(`gcloud sql backups list --instance=<INSTANCE> --project=<PROJECT>`)
- [ ] 建立**隨需備份**(指令範例;旗標以你安裝的 gcloud 版本為準):

```bash
gcloud sql backups create --instance=<INSTANCE> --project=<PROJECT> --description="pre-release-db-2026-11"
gcloud sql backups list   --instance=<INSTANCE> --project=<PROJECT>   # 記下 BACKUP_ID 與時間
```

- [ ] 另做一份**邏輯匯出**(備份還原只能整個實例,匯出才能只還原單一資料庫或單一表):

```bash
gcloud sql export sql <INSTANCE> gs://<BUCKET>/omixor-pre-release.sql.gz --database=<DB> --project=<PROJECT>
```

- [ ] 記下備份的 BACKUP_ID、匯出檔路徑、備份時間,貼在變更單(不要貼連線字串)
- [ ] 確認有人實際演練過「從這份備份還原」(第 2.2 節的副本就是演練)
- [ ] 若有本機守護(Mac App)使用者:提醒他們備份 `<App 資料夾>/core/pgdata`(`server/README.md`、`LocalCore.swift` 檔頭說明資料夾位置)。本版的 `store.json` 不會被匯入

### 2.2 在備份還原出的副本上演練(必做)

- [ ] 用備份**複製出新實例**(不要動正式實例),例如:

```bash
gcloud sql instances clone <INSTANCE> <INSTANCE>-rehearsal --project=<PROJECT>
```

  (也可從 BACKUP_ID 還原到新實例;`gcloud sql backups restore` 會**覆蓋目標實例**,目標務必是演練實例,不可是正式實例)
- [ ] 演練用的 server 連到副本:`OMIXOR_DATABASE_URL=<REHEARSAL_URL>` 啟動 `server`(本機或測試環境);**不要帶正式的 LDAP、整合、token 等環境變數**,避免副本資料觸發對外動作(`StartRoutines` 會替角色排任務,只寫副本)
- [ ] 啟動前先在副本上跑孤兒檢查(2.3),並把結果存檔,對照啟動 log 的「外鍵孤兒檢查」那一行
- [ ] 記錄下列指標(填表):

| 指標 | 怎麼取得 | 值 |
|---|---|---|
| `NewPG` 總耗時(啟動到 log `omixor server 在 …`) | log 時間戳(`server.go:Run`) | |
| 啟動遷移整體耗時(是否逼近整體預算 10 分鐘)、單步是否逼近 10 分鐘 | log:`[遷移] …project_ref 新增完成(…)`;預算用完會有 `啟動遷移整體預算 … 已用完` 的 `[遷移警告]` | |
| 有沒有表被判定「太大不自動重寫」 | log 的 `自動重寫可能鎖表很久` 警告、結尾彙總 `需在維護窗口手動處理的大表`(表名與大小)——有就要在正式維護窗口先跑腳本 | |
| 各大表實際大小(用來決定門檻) | `select relname, pg_size_pretty(pg_total_relation_size(oid)) from pg_class where relname in (…) order by pg_total_relation_size(oid) desc` | |
| 各表重寫耗時 | 副本上開 `log_min_duration_statement`/`pg_stat_activity`(待確認可用權限) | |
| 拆表耗時與未展開列數 | log `[遷移警告]` 的每筆與彙總行;`MIGRATIONS.md` §4「升級後檢查」SQL(等同 `pg_split_migrate.go:UnmigratedRowsByTable`) | |
| `[遷移警告]` 行數 | 在 log 中 grep `[遷移警告]` 計數 | |
| 序號唯一索引結果 | log 關鍵字「序號唯一索引」「重複的 (」(`pg_seq.go:ensureSeqIndex`) | |
| 外鍵孤兒報告 | log「外鍵孤兒檢查(指向不存在資料的筆數)」 | |
| 背景 validate 完成時間 | log「外鍵 … 驗證失敗」是否出現;SQL 見 3.3 | |
| `UnmigratedRows()` | **沒有 HTTP/CLI 入口**(`grep` 只在 store 內部與測試出現);用 `MIGRATIONS.md` §4 的 `split_at is null` 計數 SQL 取代,應為 0 | |
| `OrphanReport()` | 同上沒有對外入口;用 `OrphanSQL()` 對應的 SQL(見 2.3)或讀啟動 log | |

- [ ] 演練的 log 關鍵字清單(全部用 grep 檢查):

| 關鍵字 | 意義 | 來源 |
|---|---|---|
| `[遷移警告]` | 拆表略過的列、逾時、等不到鎖 | `pg_split_migrate.go:warnf` |
| `共 N 列尚未展開` | 彙總 | `refreshUnsplit` |
| `外鍵孤兒檢查(指向不存在資料的筆數)` | 26 條外鍵各自孤兒數 | `migrateForeignKeys` |
| `自動重寫可能鎖表很久` | 表超過大小門檻,啟動沒有重寫,要在窗口先跑 `premigrate.sql` | `pg_migrate_guard.go:rewriteAllowed` |
| `啟動遷移尚未完成` | 啟動結尾彙總:缺的 `project_ref`、缺的外鍵、需手動的大表 | `logMigrationSummary` |
| `等不到遷移鎖` | 別的實例還在遷移(等鎖上限 2 分鐘),本次略過 | `sessionLock`、`xactLock` |
| `警告:外鍵 … 加不上去` / `驗證失敗` / `背景補上失敗` / `已在背景補上` | 外鍵未成立或有孤兒;補不上的外鍵與被鎖擋住的 validate 會在背景每約 5 分鐘重試 | `pg_fk.go`、`pg_principals.go` |
| `警告:… project_ref 加不上去` | 產生欄位失敗(鎖逾時) | `pg_fk.go` |
| `警告:序號唯一索引 … 處理失敗` / `有 N 組重複的` | 編號重複 | `pg_seq.go` |
| `外鍵遷移失敗` / `事件、任務拆表遷移失敗` / `Issue 編號遷移失敗` / `編號序號遷移失敗` 等 | `NewPG` 失敗訊息,服務起不來(預算用完時降為警告) | `pg_migrate_guard.go:startupMigrations` |

- [ ] 演練後在副本上做 3.4 的驗證步驟,確認功能正常
- [ ] 演練後**刪除副本實例**(含其備份),避免資料外洩與費用

### 2.3 孤兒檢查與處置

- [ ] 對**舊版正式資料庫的副本**(或維護窗口內的正式庫,唯讀)執行 `core/store/pg_fk.go:OrphanSQL()` 的 `orphan` 查詢(26 條;查詢文字就是各 spec 的 `orphan` 欄位,程式沒有匯出成檔案,需要時用一小段 Go 或 `go test` 輸出——待確認誰負責)。升級前也可看 `MIGRATIONS.md` §4 的 (a)–(d) 與下面的 id 格式檢查
- [ ] 關聯外鍵孤兒的處置(來源:`pg_fk.go` 檔頭規則):

| 孤兒種類(外鍵名) | 後果 | 處置選項 | 誰決定 |
|---|---|---|---|
| `fk_agent_tasks_agent`(任務的角色不存在) | 外鍵維持 not valid,新寫入受約束;**舊任務永遠指向不存在的角色**,日後轉移或刪角色可能卡住 | (a) 補建同 id 的角色(保留歷史);(b) 把任務轉給現有角色;(c) 清除該批任務 | 組織管理員 + 維運(含資料負責人) |
| `fk_agent_credentials_task` | 憑證指向不存在的任務 | 清除(憑證有時效,通常安全) | 維運 |
| `fk_channel_messages_channel`、`fk_channel_reads_channel` | 訊息或已讀指向不存在的頻道 | 補頻道或清除 | 維運 + 產品 |
| `fk_channels_issue` | 頻道綁的 Issue 已不存在 | 清成 NULL(外鍵規則本來就是 `set null`) | 維運 |
| `fk_docs_project`、`fk_cabinet_sections_project` | 文件或區塊指向不存在的專案(`'' = 未分專案`不算孤兒) | 補建專案或把 project 清成 `''` | 組織管理員 |
| `fk_cabinet_tabs_section` | 頁籤指向不存在的區塊 | 補區塊或清除頁籤(文件櫃結構整份取代存檔,孤兒頁籤通常是殘留) | 組織管理員 |
| `fk_<表>_org`(18 條) | 資料屬於已刪除的組織 | 清除或補建組織 | 維運 + 資料負責人 |

  所有處置都要在維護窗口內、先備份、由有權限的人以 SQL 執行(**程式絕不自動刪資料**,`pg_fk.go` 檔頭)。
- [ ] 角色刪除規則變嚴(`on delete restrict`):確認營運流程已知「先轉移任務再刪角色」(`service/agent_transfer.go:TransferAgentTasks`)

### 2.4 匯入資料的 id 格式與編號檢查

`(org, number)` 唯一索引是部分索引(`where number > 0`);非數字 id(例如 `gh-1`)的 `number = 0`,不參與唯一性(`pg_seq.go:ensureSeqIndex`)。

- [ ] 在副本檢查每張表的 `(org, number)` 是否有重複(下面是依 `ensureSeqIndex` 的邏輯寫的查詢,每張表各跑一次,訊息表多帶 `channel_id`):

```sql
select org, number, count(*) from ops_issues where number > 0 group by org, number having count(*) > 1;
-- 其餘:omixor_agent_tasks、omixor_docs、omixor_channels、omixor_incidents;訊息表 group by org, channel_id, number
```

- [ ] 有重複 → 遷移只會記「有 N 組重複的 (…)」並**略過該表唯一索引**(不會失敗,但約束暫時沒有)。處置:人工決定保留哪一筆、其餘改號或搬走(誰決定:資料負責人)
- [ ] 檢查非數字 id 的資料是否符合預期(來源:匯入的 `gh-N` 之類),確認它們不影響 `omixor_seq` 初始化(`number = 0` 不計入 max)
- [ ] 確認各 scope 的 `omixor_seq.last` 初始化後 ≥ 現有最大編號(升級後查 `omixor_seq` 與 `max(number)` 對照)

### 2.5 環境檢查

- [ ] **PostgreSQL 主版本 ≥ 15**:`on delete set null (issue_id)`(欄位清單版 SET NULL)是 PG 15 起的語法(`pg_fk.go:fkSpecs` 的 `fk_channels_issue`);Cloud SQL 的版本用 `gcloud sql instances describe <INSTANCE> --project=<PROJECT> --format="value(databaseVersion)"` 確認。測試用 17.2、內建 PG 釘 17(`test.yml`、`server/embeddedpg.go:pinnedPGVersion`)。低於 15 的行為:該外鍵加不上去,只記警告(待確認:其他語法在低版本是否也有問題)
- [ ] `server/go.mod` 的 `go` 版本與 CI 一致(CI 用 `go-version-file: server/go.mod`,`deploy-server.yml`)
- [ ] **確認沒有舊版實例同時在跑**:Cloud Run 預設是滾動部署,新 revision 啟動並做遷移時舊 revision 仍在服務。`deploy-server.yml` 已設 `--max-instances=1`,但滾動期間新舊 revision 各一個實例仍會短暫並存。舊版程式在新 schema 下的風險(來源:程式註解與結構):
  - 外鍵一加上,舊版寫頻道 `issue_id = ''` 會違反 `fk_channels_issue`(舊版不知道要寫 NULL)
  - 舊版不寫新子表,新舊並存期間寫入的資料可能只寫到舊 jsonb(新版對 `split_at` 非 NULL 的列以新表為準,**那些寫入會被忽略**,見 `MIGRATIONS.md` §1)
  - 已知舊版取號用 max+1,新版的 `seqNext` 自癒(`pg_seq.go:seqNext` 註解)
  - **ISS-34(Issue key)**:遷移後 Issue 的內部 id 變成 UUID,舊版實例用舊的 `ISS-<n>` 查 Issue 會 **404**(它不認別名表);舊版實例新增的 Issue(`ops_issues.key` 為空字串、id 是 `ISS-<n>`)新版看不到正確的 key,要等下次啟動的 key 遷移才補。所以 **必須一次切換**:先停舊版實例(或讓它沒有流量),再起新版,不要滾動並存;切換後如果曾有舊實例寫入,重啟新版一次讓 key 遷移補做
- [ ] 其他自建伺服器(`release-image.yml`:推 `v*` tag 建 `ghcr.io/…/omixor-server` 映像):這些部署也會在啟動時跑同一批遷移,**沒有納入本次維運計畫的部署都要先備份**。誰在用:待確認
- [ ] `deploy-server.yml` 的 `test` job 沒有設 postgres service 也沒有 `OMIXOR_TEST_DATABASE_URL`,而測試現在一律需要 PG;沒設時會自己下載內建 PG(`test.yml` 註解、`core/storetest/testdb`)。**待確認**:CI 的 deploy 前測試在 GitHub runner 上能不能跑得過;失敗只會擋住部署(安全),但會延誤上線

### 2.6 維護窗口建議

- [ ] 建議用維護窗口,而非「推了就算」:遷移期間服務**不接受連線**(1.3 節),且遷移期間資料表被鎖
- [ ] 窗口長度 = 演練實測的 `NewPG` 總耗時 × 安全係數 + 背景 validate 緩衝(validate 在背景,不擋服務,但會吃資源)。係數建議由演練結果決定(待決定,見第 7 節)
- [ ] 公告:使用者在窗口內可能看到錯誤;Mac App 本機守護的使用者不受影響(各自本機)
- [ ] **先跑維護窗口腳本再部署**(大表):`core/store/sql/premigrate.sql`(舊名 `principals_pre.sql` 已改名;由程式產生、測試驗證不漂移)。內容只有 `omixor_docs`、`omixor_cabinet_sections`、`ops_issues` 的 `project_ref`(STORED 產生欄位,會重寫整張表);舊結構(ISS-34 之前還沒有 `ops_issues.project`)會先冪等補 `project text not null default ''`(常數預設值,不重寫)。腳本做完後,新版啟動發現欄位都在,整段跳過、不再 ALTER。**表超過大小門檻(預設 1024 MB)啟動不會自動做,只警告,所以大表一定要先跑腳本**。
  - 用與服務相同的資料庫角色(表的擁有者)執行
  - 執行方式:`psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f premigrate.sql`;**不可加 `-1`**(單一交易會把每張表的 ACCESS EXCLUSIVE 鎖持有到整份結束)
  - 每句 `alter table` 各自 autocommit,做完立刻放鎖;每句前 `set lock_timeout = '5s'`(session 層級),拿不到鎖(有長交易)5 秒失敗;ON_ERROR_STOP 之後 psql 結束、連線關閉,設定不殘留;最後 `reset lock_timeout;` 只在全部成功時執行。失敗就等長交易結束後重跑整份(已做完的句子不會再重寫)
  - 每張大表重寫時間在備份副本實測(2.2),據此決定窗口長度
- [ ] **Cloud Run 啟動等待上限**:`deploy-server.yml` 沒有設 `--timeout`、`--startup-probe` 或 `--cpu-boost`。啟動遷移的整體預算預設 10 分鐘(`OMIXOR_MIGRATE_BUDGET_MINUTES`),**要小於啟動探測的總時限**,否則新 revision 會被判定啟動失敗。做法:跑過腳本後啟動遷移通常很短;仍建議為這次部署設定足夠長的 startup probe(Cloud Run 預設值與設定語法待確認,以官方文件為準),或把預算調小。預算用完不會讓啟動失敗,剩下的下次啟動接著做

---

## 3. 上線當下

### 3.1 部署方式

- [ ] 觸發條件(來源:`deploy-server.yml`):推到 `main` 且變更包含 `core/**`、`server/**`、`site/**` 或該 workflow 檔,或手動 `workflow_dispatch`。這一批改了 `core/**`、`server/**`,**推送即部署**
- [ ] 建議:不要讓這批變更直接以普通 push 進 `main`。做法選項(由人決定,第 7 節):
  - (A)維護窗口內合併/推送,部署前先讓舊版沒有流量(例如暫時拒絕入站請求;具體指令待確認),避免新舊並存
  - (B)直接推送,接受短暫並存風險(不建議,見 2.5)
- [ ] 部署設定維持 `--max-instances=1`(`deploy-server.yml`、`server/README.md`);**不要在這次上線調高**
- [ ] 同一時間只允許一次部署:`concurrency: group: omixor-server`(`cancel-in-progress: false`,已設)
- [ ] 部署完成的健康檢查是 `curl $URL/health`(workflow 最後一步),只證明服務已 listen,**不代表背景 validate 完成**

### 3.2 啟動 log 要看什麼

- [ ] 成功的樣子(依 2.2 的關鍵字表):
  - `外鍵孤兒檢查…` 全部為 0(或只有已知、已決定的孤兒)
  - 沒有任何 `外鍵遷移失敗`、`拆表遷移失敗`、`Issue 編號遷移失敗`、`編號序號遷移失敗`;結尾的 `[遷移] 欄位與外鍵都已就位`(或 `啟動遷移尚未完成` 的彙總,要看缺什麼)
  - `omixor server 在 …`
- [ ] 告警的樣子:`[遷移警告]`、`警告:…加不上去`、`警告:…驗證失敗`、`有 N 組重複的 (…)`、`等不到遷移鎖`(出現代表另一個實例還在遷移,正常情況只有單實例)

### 3.3 背景外鍵 validate 的進度怎麼看

26 條外鍵都由 `startFKValidate` 在 `finishStartup` 之後的**背景**逐條處理:先 `validate constraint`(每條 30 分鐘上限,失敗只記警告;有孤兒(23503)的不重試);啟動時沒補上的外鍵(被長交易的鎖擋住)與被鎖擋住的 validate,背景**每約 5 分鐘**重試直到成功或服務關閉。啟動結尾有一行彙總 log(缺的 `project_ref`、缺的外鍵)。沒有進度 log 時用 SQL 看(對正式庫唯讀):

```sql
select conrelid::regclass as tbl, conname, convalidated
from pg_constraint
where contype = 'f' and (conname like 'fk\_%' escape '\')
order by convalidated, conrelid::regclass::text;
-- convalidated = false 代表仍是 not valid
select pid, state, wait_event_type, query_start, left(query, 120)
from pg_stat_activity where query ilike '%validate constraint%';
```

(外鍵名稱前綴 `fk_` 來自 `pg_fk.go:fkSpecs`)

### 3.4 驗證步驟(上線後立即,每項勾選)

- [ ] 建 Issue:編號連續、不重號(`omixor_seq` 的 `issue` 列 +1)
- [ ] 領任務(`agent_task_claim`):憑證建立成功、任務狀態更新
- [ ] 轉移角色任務(`service/agent_transfer.go:TransferAgentTasks`):範圍 `open` 與 `all`;刪有任務的角色應被擋(`ErrInUse`)
- [ ] 文件櫃存檔衝突:帶過期的 `expectedVersion` 存檔回 409(`core/api/cabinet.go`;錯誤種類 `store.ErrVersionConflict`;HTTP 狀態碼對應待確認);帶最新版本成功、版本 +1
- [ ] 頻道訊息與 @提及:發訊息、@使用者、@角色(@角色只通知不排任務),已讀數更新;訊息編號每頻道獨立
- [ ] 事件(incident):宣告、更新、時間軸新增
- [ ] 組織鏈、文件標籤、角色例行任務與文件權限:讀、寫各一次
- [ ] 舊資料抽查:挑幾個有標籤、成員、例行任務、備註的舊物件,確認新版讀得到完整內容
- [ ] Mac 守護啟動:Mac App 開啟本機模式,`LocalCoreGate` 通過(首次要連網下載 PG 二進位檔;`LocalCore.swift` 啟動等待上限見檔內說明);啟動後建 Issue 驗證

---

## 4. 上線後

- [ ] **未展開列數為 0**:`MIGRATIONS.md` §4「升級後檢查」SQL 全部為 0(等同 `UnmigratedRows()`)。非 0 的列查 `[遷移警告]`,修好舊 jsonb 後重啟(`MIGRATIONS.md` §3)
- [ ] 全部外鍵 validate 完(3.3 的 SQL `convalidated = true`),記下完成時間
- [ ] `not valid` 的外鍵何時 validate 完:**沒有程式提供預估**;由演練時間推估。若某條因孤兒始終失敗,外鍵維持 not valid(新寫入仍受約束),下次啟動會再試一次;**不會自動刪資料**
- [ ] 監控項目(持續 1–2 週,多數沒有現成儀表,需人工或另設告警):
  - [ ] log 中 `[遷移警告]`、`警告:` 的新增
  - [ ] 寫入錯誤:`ErrBadRef`(`referenced row does not exist`)、`ErrInUse`、`ErrUnmigratedRow`(`pg_fk.go`、`pg_split_migrate.go`)的出現率
  - [ ] 鎖等待與死鎖(`pg_stat_activity`、PG log);批次(`Batch`)建立不同類型的順序性死鎖(`pg_seq.go` 檔頭已知限制)
  - [ ] Cloud SQL CPU/磁碟:重寫大表會讓磁碟暫時增加,確認剩餘空間
- [ ] 通知 Mac App 使用者更新(見第 7 節的相容發版順序)

### 已知限制(只列不改,出自這一輪審查的低風險項目)

| # | 項目 | 影響 | 位置 |
|---|---|---|---|
| 1 | 結束時不等例行任務 | 服務關閉時進行中的例行任務排程可能被中斷 | `server/server.go` 的 `StartRoutines` 使用方式(待確認:精確位置與後果) |
| 2 | 大量任務轉移逐筆處理 | 轉移任務很多時耗時、占用交易 | `core/service/agent_transfer.go`、`core/store/transfer.go`(待確認) |
| 3 | 多實例並行轉移與領取 | 目前單實例所以安全;開多實例前要處理 | `server/README.md` 的單實例說明、`pg_agent.go` 的 advisory lock |
| 4 | `templates.go:199` 無鎖存檔 | 套用樣板時 `SaveCabinetLayout` 沒有帶 `expected` 版本,可能蓋掉同時間他人的存檔 | `core/service/templates.go`(約 199 行附近) |
| 5 | Mac 端文件櫃存檔不帶版本號 | `core/api/cabinet.go` 的 `ExpectedVersion` 為指標,**不帶 = 無鎖覆蓋**;Mac 端目前沒有送(在 `mac/Sources/Omixor/Cabinet` 搜不到 `expectedVersion`),等於 Mac 端存檔仍是最後寫入者勝 | `core/api/cabinet.go`、`mac/Sources/Omixor/Cabinet/` |

另外兩個來自程式本身的已知限制:`omixor_seq` 的 `msg:<channel_id>` 在將來加「刪頻道」時要一併清除;Batch 最長持有序號列鎖 10 分鐘(`pg_seq.go` 檔頭)。

---

## 5. 回滾與救援

### 5.1 回滾

**不可回滾。** 新版不再寫舊 jsonb(不雙寫);新增了產生欄位、外鍵、登記表,舊版程式不認得也不維護。回到舊版會看不到新版期間的資料,再升級時已展開的列(`split_at` 非 NULL)以新表為準(`MIGRATIONS.md` §1)。

**唯一退路:從升級前的備份還原。**

### 5.2 還原步驟概要(指令範例,由有權限的人執行,並先確認對象)

1. [ ] 停止流量(暫停部署、避免新寫入);決定是還原整個實例或只還原資料庫
2. [ ] 還原到**新的實例**(優先):從 BACKUP_ID 或匯出檔建立,驗證資料,再切換 `OMIXOR_DATABASE_URL`(Secret Manager)指向它
3. [ ] 還原**覆蓋原實例**(最後手段,會丟掉備份之後的所有寫入):`gcloud sql backups restore <BACKUP_ID> --restore-instance=<INSTANCE> --project=<PROJECT>`
4. [ ] 部署**舊版程式**(上一個 commit 的映像)到還原後的資料庫;不要讓新版再連同一個資料庫,否則會重新遷移
5. [ ] 通知使用者資料遺失的時間範圍(備份時間點到還原之間)

### 5.3 哪些情況可以用救援 SQL,不必整庫還原

來源:`MIGRATIONS.md` §5。救援 SQL 只處理「jsonb 拆表」,**不處理**產生欄位、外鍵、序號表。

| 情況 | 做法 | 條件與風險 |
|---|---|---|
| 某幾列新表資料壞了、舊 jsonb 還對 | `MIGRATIONS.md` §5.2:把該列 `split_at` 設回 NULL,重啟後以舊 jsonb 重新展開 | 舊 jsonb 要確實還正確;會覆蓋該列新表資料;先備份 |
| 要在「新版之前」的資料狀態重做整批拆表,或舊程式要讀新資料 | `MIGRATIONS.md` §5.1:從新表重建 jsonb 並把 `split_at` 設回 NULL | 只對確定要重做的列;事件與文件櫃的 JSON 欄位名稱要先對照 `core/model` 的 json tag(`MIGRATIONS.md` 已註明) |
| 外鍵驗證失敗、有孤兒 | 不需救援:外鍵維持 not valid、新寫入受約束,處理孤兒後重啟會重新 validate | |
| 序號唯一索引因重複被略過 | 清掉重複後重啟 | |
| `project_ref` 產生欄位沒做完 | 服務**不會**因此起不來(失敗與逾時只警告,下次啟動接著做)。但表超過大小門檻時啟動**自動略過並警告**,`project_ref` 與它的外鍵要等手動腳本:在維護窗口用與服務相同的角色跑 `sql/premigrate.sql`(2.6),再重啟 | 看啟動彙總 log |

---

## 6. 下個版本的清理(drop 舊欄位)

引用 `MIGRATIONS.md` §6(前置條件與 SQL 在該節,這裡不重貼)。

前置條件(**全部成立才能做**):

- [ ] 線上所有執行個體已是本版(或更新),沒有舊版在寫資料
- [ ] `UnmigratedRows()` 為 0,或 `MIGRATIONS.md` §4「升級後檢查」SQL 全為 0;略過的列已修好並展開
- [ ] 已備份
- [ ] 同一個 commit 移除遷移與降級程式

要 drop 的舊欄位(逐表,`MIGRATIONS.md` §6 的 SQL;同步見各 `pg_split_*.go` 與 `pg_cabinet.go` 檔頭):

| 表 | drop 欄位 |
|---|---|
| `omixor_channels` | `members`、`split_at` |
| `omixor_channel_messages` | `mentions`、`split_at` |
| `omixor_org_chains` | `edges`、`split_at` |
| `omixor_docs` | `tags`、`split_at` |
| `omixor_agents` | `doc_access`、`routines`、`split_at` |
| `omixor_agent_tasks` | `notes`、`links`、`split_at` |
| `omixor_incidents` | `data`、`split_at` |
| `omixor_cabinet_layouts` | `layout`、`split_at` |

同一 commit 要移除的程式(`MIGRATIONS.md` §6 第 4 點):`pg_split_migrate.go`、各 `expand*`、`migrate*`、`lenient*`、`guardUnsplit`、各讀取裡的 `hasUnsplit()` 分支、`pg.go` 的 `split_at` 與舊欄位建表語句;`migrateCabinetLayouts` 與退回讀取(`pg_cabinet.go` 檔頭)。`<表>_unsplit` 索引會隨欄位 drop 一併消失。

**升級路徑不得跳版**:不能從更舊的版本直接升到 drop 欄位的版本,否則還沒展開的列永遠丟失(`MIGRATIONS.md` §1)。自建伺服器與 Mac 本機守護(`pgdata` 的使用者)也必須先經過本版,再升到下個版本。drop 之後同樣不可回滾。

---

## 7. 需要人決定的事(待辦)

| # | 待決定 | 誰決定 | 選項 | 建議 |
|---|---|---|---|---|
| 1 | 孤兒任務處置(`fk_agent_tasks_agent` 等) | 組織管理員 + 資料負責人 | 補建角色 / 轉移給現有角色 / 清除 | 先看演練報告的筆數;有業務價值的任務補建或轉移,明確無用的才清除;任何清除先備份 |
| 2 | 維護窗口(時間、長度、公告) | 產品負責人 + 維運 | 窗口內上線 / 不停機直接推送 | 窗口內上線;長度由演練實測決定(2.2、2.6),不要用猜的 |
| 3 | 是否先在 staging 跑 | 維運 | 正式備份副本演練 / 另設 staging 長期環境 | 至少做備份副本演練(2.2),因為資料量決定時間與鎖;staging 是否存在:待確認 |
| 4 | 部署時舊版如何處理(避免新舊並存) | 維運 | (A)部署前先讓舊版沒有流量 / (B)直接滾動部署 | (A);具體可行的 gcloud/Cloud Run 做法待確認 |
| 5 | 大表產生欄位:維護窗口腳本與大小門檻(2.6、1.2) | 維運 + DBA | 先跑腳本 / 讓啟動自己做(門檻內);門檻 `OMIXOR_MIGRATE_MAX_AUTO_REWRITE_MB` 是否調整 | 一律先跑腳本再部署(同一個資料庫角色);門檻維持預設 1024 MB,由演練實測的各表大小與重寫時間決定是否調整 |
| 6 | Mac App 的相容發版順序 | App 負責人 + 後端 | (A)先發伺服器、後發 App;(B)先發 App | 伺服器先上(API 向下相容:`expectedVersion` 不帶=無鎖覆蓋,見 `core/api/cabinet.go`);之後發 App 並補送版本號(目前 Mac 不送,見第 4 節限制 5)。守護內建的 PG 與雲端資料庫彼此獨立 |
| 7 | 自建伺服器(`release-image.yml`)的升級安排 | 各部署負責人 | 先備份再升級 / 暫不升級 | 先備份;務必不要跳版 |
| 8 | `deploy-server.yml` 的 test job 在無 PG service 時能否通過 | 維運 / CI 負責人 | 為該 job 補 postgres service(同 `test.yml`)/ 靠內建 PG | 待確認後決定;屬於部署流程修改,不在本文範圍 |
| 9 | Cloud Run 啟動等待上限是否調高 | 維運 | 調高 `--timeout`/startup probe / 維持預設 | 依演練時間決定;若演練超過預設值就調高 |

---

## 附:自我檢查紀錄

每個檢查項的來源已在項內標註(檔案:函式、SQL、指令)。**沒有找到直接來源、已標「待確認」的項目**:副本上量各表重寫耗時的工具與權限、`OrphanSQL()` 實際執行方式(沒有對外入口)、低於 PG 15 的其他相容性、Cloud Run 啟動探測預設值與暫停舊版流量的具體做法、cabinet 衝突的 HTTP 狀態碼、已知限制第 1、2 項的精確位置、`templates.go` 無鎖存檔的確切行號、自建伺服器的實際使用者、staging 是否存在、`deploy-server.yml` 測試 job 能否通過。

# ISS-36 C-2 共用地基契約:「誰」欄位、過渡橋、present、輸入解析

給 C-2a~e 五個並行工作包的子代理遵守。規格:雲端 DOC-13(§1.4 逐欄、§1.5 對照、§3 服務層、§6.1 工作包)、DOC-12。
使用者決定:不考慮舊版本(滾動部署相容);組織鏈欄位直接改名 `from_actor`/`to_actor`,不做相容 view。

這份文件描述「共用地基完成之後」的狀態,以及每個工作包要把自己的領域從「過渡橋」改成「actor id」的做法。**不要改共用地基以外你不擁有的檔案**(第 9 節)。

## 1. 現在的狀態(一句話)

資料庫(全新建立,結構在 `pg_schema.go`)是**最終結構**:22 個「誰」欄位存 actor id、有指向 `omixor_actors(id)` 的外鍵(`on delete restrict`)、沒有 `from_node`/`to_node`/`actor_kind`/`actor_id`/`user_id`。
(歷史說明:C-2 之前 store 與服務層是舊字串語意,靠 `pg_actor_bridge.go` 的過渡橋接;現在各領域都已改完,橋只剩寫入端給匯入用,見第 4 節。以下工作包說明保留作為歷史紀錄。)
你的工作:把你的領域改成「model 欄位存 actor id、服務層用 present 補名字」,並拆掉你這個領域的橋。

## 2. 最終結構:22 欄(`identity_fields.go:ActorColumns`)

外鍵一律 `references omixor_actors(id) on delete restrict`,名稱見 `ActorColumn.FK`。「橋 key」是 `whoConv.col(key, …)` 用的 `表.欄位`。NULL 欄位沒有「誰」時是 **NULL,不是空字串**(外鍵不接受空字串)。

| # | 欄位(最終欄名) | 型別 | NULL | 橋 key | 讀取輸出格式(`model.Legacy*`) | 擁有者 |
|---|---|---|---|---|---|---|
| 1 | `omixor_issues.created_by` | text | 否 | `omixor_issues.created_by` | Free | C-2a |
| 2 | `omixor_issues.assignee` | text | 是 | `omixor_issues.assignee` | AgentID(只有角色 handle) | C-2a |
| 3 | `omixor_issues.deleted_by` | text | 是 | `omixor_issues.deleted_by` | Free | C-2a |
| 4 | `omixor_issue_comments.author` | text | 否 | `omixor_issue_comments.author` | Free | C-2a |
| 5 | `omixor_docs.author` | text | 是 | `omixor_docs.author` | Free | C-2a |
| 6 | `omixor_agent_tasks.created_by` | text | 是 | `omixor_agent_tasks.created_by` | Free | C-2b |
| 7 | `omixor_agent_tasks.claimed_by` | text | 是 | `omixor_agent_tasks.claimed_by` | Free | C-2b |
| 8 | `omixor_agent_task_notes.author` | text | 是 | `omixor_agent_task_notes.author` | Free | C-2b |
| 9 | `omixor_channels.created_by` | text | 是 | `omixor_channels.created_by` | Node | C-2c |
| 10 | `omixor_channel_members.member`(主鍵 `(org, channel_id, member)`) | text | 否 | `omixor_channel_members.member` | Node | C-2c |
| 11 | `omixor_channel_messages.author` | text | 否 | `omixor_channel_messages.author` | Node | C-2c |
| 12 | `omixor_channel_mentions.member`(主鍵含 member) | text | 否 | `omixor_channel_mentions.member` | Node | C-2c |
| 13 | `omixor_channel_reads.member`(主鍵含 member) | text | 否 | `omixor_channel_reads.member` | Node(只當查詢條件) | C-2c |
| 14 | `omixor_org_chain_edges.from_actor`(原 `from_node`) | text | 否 | `omixor_org_chain_edges.from_actor` | Node | C-2c |
| 15 | `omixor_org_chain_edges.to_actor`(原 `to_node`) | text | 否 | `omixor_org_chain_edges.to_actor` | Node | C-2c |
| 16 | `omixor_incidents.commander` | text | 是 | `omixor_incidents.commander` | Free | C-2d |
| 17 | `omixor_incidents.created_by` | text | 是 | `omixor_incidents.created_by` | Free | C-2d |
| 18 | `omixor_incident_events.actor`(合併原 `actor_kind` + `actor_id`) | text | 是 | `omixor_incident_events.actor`(用 `w.event(org, kind, id)`) | `LegacyEventActor` → `{kind: person/ai/system, id}` | C-2d |
| 19 | `omixor_org_integrations.updated_by` | text | 是 | `omixor_org_integrations.updated_by` | Free | C-2e |
| 20 | `omixor_org_system.updated_by` | text | 是 | `omixor_org_system.updated_by` | Free | C-2e |
| 21 | `omixor_server_features.updated_by`(表沒有 org) | text | 是 | `omixor_server_features.updated_by`(org 給 "") | Free | C-2e |
| 22 | `omixor_audit.actor`(取代原 `user_id`;`email` 快照保留) | text | 是 | `omixor_audit.actor`(`w.auditUser(org, userID)`) | UserID(使用者 = `omixor_users.id`、角色 = `agent:<handle>`) | C-2e |

沒有變動的相關欄位:`omixor_users.id`(仍是 `u_…`)、`omixor_tokens.user_id`、`omixor_org_members.user_id`、`omixor_agents.(org,id)`(角色 id 仍是對外識別;`omixor_agents.actor_id` 是它的 actor)。稽核還沒有 `source`/`via` 欄位(C-2e 要加,加在 `pg.go` 的 `omixor_audit`)。

actor id 格式:`usr_<uuidv7>`、`agt_<uuidv7>`、`sys_<名稱>`(固定:`sys_omixor`、`sys_routine`、`sys_chain`;匯入與橋遇到對不上的舊字串才建 `sys_legacy_*`)。`model.ValidActorID`、`model.SystemActors`。

最終結構由 `pg_schema.go` 在全新資料庫直接建出,結構指紋由 `schema_manifest.json` 與 `TestSchemaManifestInSync` 把關;資料庫結構的遷移程式已移除。
新增任何名稱像「誰」的欄位(`created_by|author|assignee|member|updated_by|deleted_by|commander|claimed_by|from_*|to_*|actor_*|user_id`)必須登記進 `ActorColumns`,否則 `TestActorColumnsGuard` 失敗。

## 3. NULL 安全的讀寫(`store/actor_cols.go`)

可為 NULL 的欄位(上表 NULL=是)不能直接掃進 `string`:

```go
var by store.NullID                 // 讀:NULL → ""
row.Scan(&by); x.CreatedBy = by.String()
args = append(args, store.IDOrNil(x.Assignee))   // 寫:"" → NULL;有值原樣
// 或 store.NullID(x.Assignee) 直接當參數(Valuer);text[] 陣列元素用 store.IDPtr(...) 得到 *string
```

NOT NULL 的欄位(1、4、10~15)永遠有值,一般 `string` 即可;寫入空字串會被外鍵擋(`store.ErrBadRef`)。外鍵違反 actor 欄位:`fkErr` 回 `ErrBadRef`(服務層一般對應 400)。

## 4. 過渡橋(`store/pg_actor_bridge.go`)與怎麼拆

橋現在只剩寫入這一半(2026-11 清理:一般路徑已移除,讀取的 SQL 版與測試已刪):

- **寫入** `whoConv`(`p.newWho(c)` 沒有交易時;**有開交易時一律用 `p.whoTx(c, tx)`**,否則會在交易還握著連線時再佔一條,連線池吃緊會互等死鎖):`w.col(key, org, raw)` 把舊字串轉成 actor id(寬鬆解析器;缺的 actor、別名自動建立;空字串 → NULL 或 `sys_legacy_unknown`;**傳進來本來就是 actor id 就是它自己**)。`w.event`(事件時間軸的舊 kind + id)。
- **讀取**:`whoSQL`、`eventKindSQL`、`eventIDSQL`、`whoOut` 已刪(沒有呼叫者)。相容字串只剩 Go 版 `model.LegacyActorString`/`LegacyEventActor`(`service/present.go` 的輸出出口用),`TestWhoSQLMatchesGoLegacyStrings` 一併刪除。
- **現在還用寫入橋的地方**:incident/channel 匯入(`pg_incident_legacy.go`、`server/local.go` 的 `importChannels`)。一般讀寫路徑直接用 actor id。

拆橋(每個工作包對**自己擁有的欄位**做,順序:store → 服務層 → 測試):

1. **store 檔**:把 `whoSQL(...)` 換成欄位本身(NULL 欄位用 `NullID` 掃描),把寫入端的 `w.col(...)`/`w.cols`/`w.event`/`w.lookup` 換成直接用傳進來的 actor id(NULL 欄位用 `IDOrNil`)。此時 model 的 `CreatedBy`/`Author`/`Assignee`… 字串欄位**裝的是 actor id**(欄位型別不動)。你的檔案裡不再有 `newWho`/`whoTx`。
2. **服務層寫入端**:輸入(`author`、`as`、`assignee`、`commander`、`reportTo`、頻道成員、組織鏈節點、範本建立者)一律走第 6 節的輸入解析,算出 actor id 再交給 store。內建署名改用 actor id:`model.SystemActorOmixor`/`SystemActorRoutine`/`SystemActorChain`(取代 `SystemAuthor`/`RoutineAuthor`/`ChainAuthor` 字串)。
3. **服務層讀取出口**:你的**匯出方法的回傳值**對外仍要是舊的相容字串(API/MCP/console/用戶端這一版不變,見 DOC-13 §3.5),用第 5 節的 present 在方法出口把 actor id 轉成相容字串(寫在 model 既有的字串欄位裡)。**新的 actor 物件欄位(`createdByActor`…)與 JSON 輸出屬 C-3**(要動 `core/model` 的結構與 contract 範例檔,這些是共用檔,你不要加)。
4. **服務內部不要用自己的匯出方法拿 id**:匯出方法回傳的是相容字串,內部流程要 id 時直接讀 `s.Store.*`(store 回傳 actor id)。
5. 全部領域都拆完後:`whoSQL` 等讀取橋已刪除;`pg_actor_bridge.go` 的寫入橋因匯入仍在用而保留(**你不要改它**)。

橋與舊行為已知的差異(都是 actor 身分合一的自然結果,現有測試已調整):email 一律輸出 `omixor_users.email`(註冊使用者,保留大小寫)或 actor handle(小寫,未註冊使用者),所以「領取者比對」要不分大小寫(`strings.EqualFold`,已在 `agents_auth.go`、`agents_claim.go` 三處改好);舊的 `system` 署名併入 `sys_omixor`,讀回是 `omixor`;頻道成員、組織鏈端點讀回一律是 `user:<email>`/`agent:<handle>` 節點形式;事件 `person` 的 id 一律當使用者(對不上就建未註冊使用者)。

## 5. present:actor id → 相容輸出(`core/service/present.go`、`core/model/actor_legacy.go`)

```go
n, err := s.LoadActors(model.ActorIDs(i.CreatedBy, i.Assignee, i.DeletedBy /*, 留言作者… */)...) // 一個方法一次批次查詢;空字串、重複的先濾掉
out.CreatedBy = n.Legacy(i.CreatedBy, model.LegacyFree)       // 人 = email、角色 = agent:<handle>、系統 = 舊名稱
out.Assignee  = n.Legacy(i.Assignee, model.LegacyAgentID)     // 角色 handle
node          = n.Legacy(m.Author, model.LegacyNode)          // user:<email> / agent:<handle>
kind, who     := n.EventActor(e.Actor)                          // 事件時間軸的舊 {kind, id}
ref           := n.Ref(id)                                      // *model.ActorRef{id, kind, name, handle}(C-3 輸出 actor 物件用);查不到 nil
```

- `LegacyForm`:`LegacyFree`(Issue 建立者、留言作者、文件作者、任務建立者與領取者、備註作者、事件指揮官與建立者、updatedBy)、`LegacyNode`(頻道成員與建立者與訊息作者與提及、組織鏈端點)、`LegacyAgentID`(Issue assignee)、`LegacyUserID`(稽核 userId)。
- 系統 actor 舊名稱:`sys_omixor`→`omixor`、`sys_routine`→`routine`、`sys_chain`→`chain`、`sys_legacy_unknown`→空字串、`sys_legacy_mcp`→`mcp`、其他 `sys_legacy_*`→原值(actor 的 `name`)。
- `Names` 零值、nil 都可用(查不到就是空字串/nil,不是錯誤)。`s.Present(id, form)` 只給單筆輸出用。
- **不要把已補名字的輸出結構寫回 store**:store 寫入只收 actor id(外鍵擋字串)。輸入端的解析器接受相容字串,所以「讀出再寫回」(`UpdateIssue` 帶整份 `Comments`、CLI restore)要走解析器轉回 id。

## 6. 輸入解析(`core/service/actor_input.go`,包 `ResolveActor`)

```go
s.SelfActor(p)                          // 呼叫者本人(Principal.Actor;沒填由使用者列/角色表補;補不到回 ErrActorUnavailable/ErrActorEmpty)
s.ResolveWho(p, org, raw, want)         // 字串 → 現存 actor(找不到 400,不建立);want = WantUser|WantAgent|WantSystem 的組合(WantPeople、WantAny);種類不符 ErrActorKind(400)
s.WhoOrSelf(p, org, raw, want)          // 同上;raw 空白 = 呼叫者本人(本人種類也要符合 want)
s.ResolveAuthor(p, org, given, mode)    // 署名(取代 issues.go 的 author())
```

`ResolveAuthor` 規則:`given` 空 → 本人;**角色憑證與一般成員只能是自己**(給了不是自己的:`AuthorIgnore` 靜默當本人 = 現行行為與現有測試語意,`AuthorReject` 回 `ErrActorImpersonate` 403);**伺服器管理員**:只能代角色與系統(D9 的 (b)),解析到自己照給;解析不到或解析到別的使用者:`AuthorReject` 回 400 / `ErrActorImpersonate`(403),`AuthorIgnore`(舊式、相容)**靜默當本人**(D9 的相容修正,見 docs/authorization.md §6;舊用戶端會把 `app.me`、`mcp` 當 author 傳,不能因此對管理員壞掉)。匯入的署名走 `service/actor_import.go`(伺服器管理員寬鬆、組織管理員只能署名本組織成員、角色、系統與自己),還原走 `IssuePatch.Restore`。
輸入接受:actor id、`@handle`、`handle`、email、`agent:<handle>`、`user:<handle|email>`;角色同名使用者衝突 `ErrActorAmbiguous`。授權範圍見 `actors.go:ResolveActor` 的註解(看不到的與不存在回完全相同的錯誤)。`IsActorInputError(err)` 判斷這類輸入錯誤。
拿到的 `ActorRef.ID` 就是要寫進欄位的值。**assignee**:`ResolveWho(p, org, raw, WantAgent)`(B/C 階段仍只能指派現役角色,檢查暫停與組織鏈的邏輯照舊)。

本機使用者(console 的 localPrincipal、`server/local.go` 的 Principal)現在也是 `omixor_users` 的一列並帶 `Principal.Actor`,所有使用者與角色都保證有 actor。`callerActorID`、`selfActorID`、`commanderID`、`incidentActor` 在 `SelfActor` 失敗時直接回錯誤(資料錯誤),不再退回寬鬆解析器建 legacy actor;寬鬆解析器 `ResolveLegacy` 只留給匯入(`actor_import.go`、`server/local.go`)。

## 7. 測試怎麼寫

- 取得固定 actor:`core/storetest/actors.go`:`storetest.User(t, s, email)`、`Admin`、`Agent(t, s, org, handle)`(組織要先存在)、`System(t, s, id)`、`Legacy(t, s, org, raw)`(舊字串 → actor id)。回傳 `model.ActorRecord`,`.ID` 就是要寫進「誰」欄位的值。全部走 `store.Store` 介面。
- **直接寫 SQL 的測試**不能再塞字串:`created_by`、`author`、`member` 等欄位要放真的 actor id(最簡單 `'sys_omixor'`,固定存在);
- 輸出(服務層匯出方法回傳、API)用相容字串斷言;store 層拆橋之後斷言 actor id(用 `storetest.*` 取得期望值)。
- 測試資料庫回收:`storetest/testdb/fingerprint.go` 的 `metaTableKeep` 已讓 `sys_legacy_*` 與測試建的 actor 回收時刪掉(只有固定系統 actor 留);測試不要依賴「資料庫裡只有這些 actor」以外的假設。
- 守門測試(加欄位忘了登記會失敗):`TestActorColumnsGuard`(欄位登記);改結構會讓 `TestSchemaManifestInSync` 失敗。
- 驗證:`cd core && go build ./... && go vet ./... && go test ./... -count=1`(已知失敗:`core/cli` TestCLI、TestCredentialsPerServer 的 600 權限);`cd server`、`cd console` 同樣(已知 Windows 失敗:console `TestRunnerOptionsWithWorkdirAndSuccess`、6 個 `TestOffice*`)。需要 PostgreSQL:設 `OMIXOR_TEST_DATABASE_URL`(見 `storetest/testdb`)。

## 8. Store 介面與簽章的變動

**既有 `Store` 方法的簽章全部沒變。**新增(已在 `store.go` 與 `PG` 實作,不需要再動介面檔):

- `ResolveLegacy(org, raw string) (string, error)`:寬鬆解析器(缺的 actor 與別名會建;空字串回 "")
- `UserInfoByActorIDs(ids []string) (map[string]store.UserInfo, error)`:使用者 actor id → 使用者列的 id、email(present 用)
- 型別 `store.NullID`、`store.UserInfo`;函式 `store.IDOrNil`、`store.IDPtr`

沒有 mem 版 Store 或其他實作(只有 `*store.PG`,批次/`Atomic` 的交易也是 `*PG`),所以沒有 fake 要改。`RenameIdentity` 保留簽章但**不再改任何資料**(「誰」是 actor id,改名是改 actor handle;console 的 `migrateLocalIdentity` 還在呼叫,留給 C-5 刪)。

## 9. 工作包範圍與檔案所有權

共用地基已經做完、你**不要再改**的檔案:`store/pg.go`(最終結構;**唯一例外 C-2e 為稽核加 `source`/`via` 欄位**,只動 `omixor_audit` 那一段,並同步更新 `schema_manifest.json`)、`pg_actor_legacy.go`、`pg_actor_bridge.go`、`pg_fk.go`、`identity_fields.go`、`actor_cols.go`、`store.go`、`model/actor_legacy.go`、`service/present.go`、`service/actor_input.go`、`service/actors.go`、`storetest/*`、`config/vars.go`。要改它們的功能需求回報協調者。

| 包 | 領域與欄位 | store 檔(獨佔) | service 檔(獨佔) | model 檔(只能改欄位註解) | 測試 |
|---|---|---|---|---|---|
| **C-2a** | Issue、留言、文件、附件(#1~5) | `pg_issue.go`、`pg_doc.go` | `issues.go`、`attachments.go`、`cabinet.go`(與其匯入、垃圾桶部分;檔案大就拆檔,行為不變) | `model.Issue`、`Comment`、`CabinetDoc` | `issues*_test.go`、`cabinet_test.go`、`store/issue_comment_test.go` 等 Issue、文件相關 |
| **C-2b** | 角色任務、領取、例行、轉移(#6~8) | `pg_agent.go`(任務部分)、`pg_atomic.go`、`transfer.go`、| `agents.go`、`agents_auth.go`、`agents_claim.go`、`agents_import.go`、`agents_routines.go`、`agents_tasks.go`、`agent_transfer.go`、`locks.go` | `model.AgentTask`、`AgentTaskNote` | `agents*_test.go`、`agent_transfer_test.go` |
| **C-2c** | 組織鏈、頻道、範本(#9~15) | `pg_chain.go`、`pg_channel.go` | `chain.go`、`channel.go`(749 行,碰到就拆)、`templates.go`、`agenttools.go` | `model.Chain`、`ChainEdge`、`Channel`、`ChannelMessage`、`ChannelRead` | `chain_test.go`、`channel_test.go`、`templates_test.go`、`issues_assign_chain_test.go` |
| **C-2d** | 事件、時間軸(#16~18) | `pg_incident.go`(含 `insertIncidentEvents`) | `incident.go` | `model.Incident`、`IncidentEvent`、事件的舊 `Actor{kind,id}`(DOC-13 §8.1 #7:`model.Actor` 與新 actor 同名,改名 `IncidentActor` 並保留相容輸出) | `incident_test.go`、`store/incident_test.go` |
| **C-2e** | 稽核、整合、系統設定、MCP 設定署名(#19~22) | `pg_system.go`、`pg_integration.go`、`pg_mcp.go`、`org_doc.go`、`pg_user.go`(只有 `AddAudit`/`ListAudit`) | `service.go`(`Audit`、`RemoveMember`)、`integration.go`、`system.go`、`mcp.go`、`module.go` | `model.AuditEntry`、`features.go`、`mcp.go` 的 `UpdatedBy` | `system_test.go`、`mcp_test.go`、`store/system_test.go`、`integration_test.go` |

**拆橋範圍以欄位為準**(上表第 2 節):你只拆自己擁有欄位的 `whoSQL`/`w.col`;不要動別人的。

### 跨包耦合(你改你的領域時,別人還在用橋;讀寫別人的欄位仍走他們現在的語意)

- **Issue 的 `assignee`(C-2a 擁有)被 C-2b、C-2c 的服務程式讀寫**:`agents_tasks.go`(`syncAssignee` 等)、`agent_transfer.go`(轉移時改指派)、`chain.go:checkIssueAssign`(C-2c)、`locks.go`。C-2a 拆橋之後,`Issue.Assignee` 是角色的 actor id,這些讀寫處要同時改:**C-2a 負責把這些呼叫點一起改好**(只改讀寫 `Issue.Assignee` 與 `CreatedBy` 的那幾行,在別人的檔案裡做最小改動,並在回報裡列出)。
- **任務的 `created_by`、`claimed_by`(C-2b 擁有)被 C-2c 的 `chain.go` 讀寫**(委派時 `CreatedBy = agent:<id>`、`reportTo` 以建立者過濾、`ChainAuthor` 備註比對)與 `templates.go`(範本建立者、模擬情境署名):**C-2b 負責同步改 `chain.go`/`templates.go` 裡讀寫任務 `CreatedBy`、`ClaimedBy`、備註 `Author` 的那幾行**。
- **事件 `commander`、`created_by`(C-2d)** 沒有別的包讀寫(`incident.go` 獨佔)。**頻道/組織鏈(C-2c)**:`templates.go` 同包;`service.go:RemoveMember` 的 `pruneChainNode`/`pruneChannelNode`(C-2e 的檔)傳 `UserNode(u.Email)`:**C-2c 負責把 `pruneChainNode`/`pruneChannelNode` 的參數改成 actor id,並同步改 `service.go` 那兩個呼叫行**。
- **內建署名常數**(`model.SystemAuthor`/`RoutineAuthor`/`ChainAuthor`):橋接受舊字串也接受 actor id,所以各包可以各自換成 `model.SystemActor*`,不必同時。常數本身先不要刪(協調者最後刪)。
- 服務層共用 helper(`author()`、`whoName()`、`claimerName()`、`incidentActor()`、`channelActor()`):各由擁有它的包改;`issues.go:author()` 屬 C-2a(改成呼叫 `ResolveAuthor`),`integration.go:whoName` 屬 C-2e,`agents_claim.go:claimerName` 屬 C-2b。
- 同一個測試檔跨領域時(例如 `chain_test.go` 用 Issue、任務):以「誰擁有被改語意的欄位」為準,在改你欄位的斷言時一併調整,別的斷言不動。
- 衝突時以「同一檔案同一時間只有一個包」為原則;共用檔(第 9 節開頭)需要改的先回報,不要自己改。

## 10. 這一步沒做、留給後續的

- (已完成)新的 actor 物件欄位(`createdByActor`…)、`GET /api/orgs/{org}/actors`、MCP/CLI 的輸出:C-3、B-4。物件型別是 `model.ActorView`(`Names.View(id)`、`Names.Views(ids)` 產生,批次載入同 `LoadActors`);欄位對照見 `api/api.go` 檔頭;範例檔 `model/testdata/contract/`。
- console 本機使用者(`localPrincipal`)與 `server/local.go` 的 Principal 已帶 Actor(原 C-5 已完成)。
- 稽核的 `source`(via)欄位:C-2e。
- 移除 `RenameIdentity` 與 console 的 `migrateLocalIdentity`、(`pg_actor_bridge.go` 讀取橋已刪,寫入橋因匯入仍在用而保留)、舊常數 `SystemAuthor`/`RoutineAuthor`/`ChainAuthor`:全部包完成後由協調者做。

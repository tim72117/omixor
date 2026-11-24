# omixor 功能授權盤點與架構建議

> 範圍:`core/`(model、service、api、mcp、backend)、`server/`、`console/`(Go 後端與 `console/ui` 的呼叫)。
> 方法:逐檔讀碼,加上兩支語法樹掃描(掃 `core/service` 所有帶 `*model.Principal` 的公開方法;掃 `core/api` 所有 `HandleFunc`)。
> **行號是讀碼當下的,工作樹仍在改動,行號可能隨後續改動位移**(以函式名搜尋為準)。
> 風險等級:高 / 中 / 低 / 無;「待確認」= 沒有讀碼驗證到或需要產品決策。本文件不含任何 token、密碼或真實組織資料。

---

## 0. 摘要

| 問題 | 結論 |
|---|---|
| 有沒有統一的授權架構? | **沒有。** 只有「service 層是權限最終執行處」這個共識;檢查散成至少 15 組機制(§2),MCP 工具表(`model.CoreTools`)只決定「列不列工具」,console 另有自己的一套身分與權限(deny-list)。沒有「操作 → 所需能力」的單一資料表,也沒有「每個對外操作都要有授權條目與拒絕測試」的機制。 |
| core(API + MCP + service)的實際強度 | **相當好**:96 個 REST handler 全部經 `a.Svc.*`(`/api/auth/me`、`/api/modules` 只需登入),`api` 層沒有直接碰 store(只有 `PUT /api/orgs/{org}` 在 `UpdateOrg` 成功後讀回);97 個帶 Principal 的公開方法逐一檢視後,沒有「完全沒檢查」的資料操作。 |
| 最大的結構性問題 | (1) **console 與 core 是兩套身分**:console 用 `localPrincipal`(伺服器管理員)或共用的 server token 呼叫 core,web 使用者的權限只靠 `adminActions/adminTools` 的 **deny-list**(新增 action 預設放行);(2) **console 的 MCP 連接埠本來完全沒有驗證(2026-11-20 已補上共享 Bearer 權杖,見 R-1;但通過驗證後仍不經 deny-list、身分是 `localPrincipal`)**;(3) MCP 工具表的 `Admin` 旗標其實是「端點類別」而不是「需要管理員」,名稱誤導;(4) 角色身分(task-bound credential)與一般成員之間,有幾條**繞過組織鏈/領取鎖**的路。 |
| 最先該補的缺口 | 見 §5.4。排序:① console MCP 連接埠驗證與 deny-list 改 allow-list;② 成員可 `ReportTask` 取消/改別人領取的任務;③ Issue `assignee` 繞過組織鏈派工;④ incidents 在 REST 對角色憑證開放、MCP 卻不開;⑤ 停用角色不作廢既有憑證。 |

### 0.1 操作表與守門測試(ISS-33 第 1 階段,2026-11-21)

**表在哪**:`core/service/authz_table.go`(`AuthzOps` 81 個操作、`AuthzTools` 37 個 MCP 工具標記、`AuthzExempt` 9 個「帶 Principal 但不是操作」的方法與原因、`AuthzToolsNoOp` 2 個純渲染工具)。每個操作有:操作名(例 `issue.create`、`agent.task.transfer`)、範圍(server / org / project / self)、最低身分 `Level`(authenticated / orgMember / orgAdmin / serverAdmin)、角色憑證可否呼叫 `Agent`(no / org / self / only)、附加條件 `Caps`(chainEdge、claimer、leaseCode、docRead/docWrite、channel、downstream、featureMCP…)、對應的 service 方法、MCP 工具、REST 路由(`[a]` = authed,`[aa]` = authedOrAgent)。**表是現況描述,不執行授權**(第 3 階段才會收斂成 `Authorize`);內容都是讀實作確認過的。

**守門測試**(`core/service/authz_table_test.go`、`authz_denied_test.go`,跑法 `go test ./service -run TestAuthz`,約 100 秒):
- `TestAuthzTableCoversEveryServiceMethod`:go/ast 掃 `core/service` 所有帶 `*model.Principal` 的公開方法(現約 120 個),每個都要在 `AuthzOps[].Methods` 或 `AuthzExempt`(要寫原因);表裡列的方法也必須真的存在。
- `TestAuthzTableCoversEveryAPIServiceCall`:掃 `core/api`,每個 `a.Svc.X` 呼叫都要在表裡。
- `TestAuthzToolMarksMatchCoreTools`、`TestAuthzToolsConsistentWithOps`:`AuthzTools` 與 `model/tools.go` 的 Admin / Agent / Worker / Needs 逐欄一致;每個工具有對應操作;工具與操作的角色憑證、docAccess、頻道、下游條件互相對得上。已知差異放在白名單(`toolAgentKnownDiff`、`toolNeedsKnownDiff`),修好時測試會要求移除。
- `TestAuthzDeniedMatrix`:依表自動產生「未授權被拒」測試。每個操作最多用四種呼叫者各試一次:一般成員(要管理員以上、領取者、頻道成員、鏈上有邊、AgentOnly 時預期被拒)、外部組織成員、本組織角色憑證(表說 Agent=no 或探測別人的資源時預期被拒)、外部組織角色憑證(一律預期被拒);預期 403(頻道看不到回 404 也接受)。差異登記在 `knownDenyGaps`(預期被拒卻放行)與 `knownDenyCodes`(有拒絕但不是 403),不改行為;白名單項目修好時測試會要求移除。
- `TestAuthzKnownGapsStillPresent`:「確認漏檢現在還在」(剩 R-4、R-10);R-3、R-8、R-9 已修,改成正向被拒測試 `TestAuthzIssueAssigneeFollowsChain`、`TestAuthzSaveMemberNeedsExistingUser`、`TestAuthzProjectNeedsDocAccess`。

**怎麼維護**:
1. 新增或改名帶 `*model.Principal` 的公開方法 → 在 `AuthzOps` 加一筆(或進 `AuthzExempt` 並寫原因),否則測試失敗。
2. 新增 MCP 工具 → `model/tools.go` 與 `AuthzTools` 各登記一次,並把工具名放進對應 `Op.Tools`。
3. 新增 REST 路由 → 把路由字串寫進對應 `Op.Routes`;用到新的 `Svc.X` 要有 `Op.Methods`。
4. 新增操作 → 在 `authz_denied_test.go` 的 `authzProbes` 加一個探測(或在 `noProbe` 寫原因);表上標管理員以上、Agent=no 的操作,測試自動要求被拒。
5. 行為與表不符時**不要為了讓測試過而改行為或放寬表**:把差異寫進白名單(附原因與風險)與下面的清單。

**第 1 階段發現的不一致與漏檢(風險高的優先;R- 編號沿用 §4.2,本階段用測試確認仍存在)**

| 優先 | 編號 | 內容 | 測試證據 |
|---|---|---|---|
| 中高 | R-3(**已修**) | Issue 的 `assignee` 繞過組織鏈:鏈上沒有任何邊時,一般成員 `AssignTask` 被 403,但 `CreateIssue`/`UpdateIssue` 帶 `assignee` 就能替任一啟用中角色排任務;**角色憑證**也能改 assignee 替(不在其下游的)角色排任務 | `TestAuthzIssueAssigneeFollowsChain`(修後:有設定組織鏈時成員要有 user→agent 邊、角色要有 agent→agent 邊;沒設定組織鏈維持原行為;組織管理員不受限) |
| 中 | R-4 | 事件:service 與 REST 對角色憑證開放讀、宣告、更新,MCP 工具表只開管理端點 | 同上;`toolAgentKnownDiff` |
| 中 | 表與工具不吻合 | `agents`、`chain`、`incidents`、`incident_declare`、`incident_update` 在 service 允許角色憑證,但工具表不開給角色端點(多為刻意:agent_context 取代);`projects` 標 `NeedDocRead`,service 原本不看 docAccess(R-8 已修,`toolNeedsKnownDiff` 已清空) | `toolAgentKnownDiff` |
| 低中 | R-9(**已修**) | `SaveMember` 由組織管理員呼叫,原本會順便建立伺服器使用者(`SaveUser` 要伺服器管理員);現在組織管理員只能加入已存在的使用者,email 不存在要伺服器管理員 | `TestAuthzSaveMemberNeedsExistingUser` |
| 低 | R-8(**已修**) | 沒有任何 docAccess 的角色憑證原本仍可 `ListProjects`/`GetProject`;現在與文件櫃一致:列是空清單、取 403 | `TestAuthzProjectNeedsDocAccess` |
| 低 | R-10 | 一般成員可刪 Issue 與文件(無作者或管理員限制) | 同上 |
| 低 | 新:N-1 | `ListOrgs` 沒有拒絕分支:角色憑證與外部角色憑證在 service 層不被拒(回空清單,無外洩;REST 只收 `omx_` token 到不了) | `knownDenyGaps["org.list/agent"]`、`["org.list/extAgent"]` |
| 低 | 新:N-2 | 角色憑證讀別的角色(`GetAgent`)或別人的任務(`GetAgentTask`)回 **404**(不洩漏存在),表上寫 self;同屬「403 或 404」的設計待決定(ISS-33 風險段已列) | `knownDenyCodes["agent.read/agent"]`、`["agent.task.read/agent"]` |
| 資訊 | 新:N-3 | `org.update` 只有伺服器管理員(組織管理員不能改自己組織的名稱/圖示);`agent.task.delegate` 人類只有伺服器管理員帶 `from` 才行,組織管理員也 403 | 表的 Note、`TestAuthzDeniedMatrix` |
| 資訊 | 新:N-4 | 走 `authedOrAgent` 的 channel 路由(建立、改設定、改成員)接受角色憑證,全靠 service 層拒絕(表 Agent=no)。路由層沒有第二道防線 | 表 Note;`TestAuthzDeniedMatrix` 的 agent 案例通過 |
| 資訊 | ISS-34 | 專案與 repo 連結(`ListProjectRepos` viewer、`Link/UnlinkProjectRepo` needOrgAdmin,`service/project_repo.go`)已按現況列入表;沒有 MCP 工具。ISS-34 仍在進行,之後改動要同步表 | `project.repo.*` |
| 仍開 | R-1 | console MCP 通過驗證後不經 deny-list、共用 `localPrincipal`(第二階段隨 ISS-36);**本階段未涵蓋 console**(表只涵蓋 core service) | §4.2 |

本階段**沒有改任何授權行為**。表的範圍是 `core/service`、`core/api` 與 `core/model/tools.go`;console 的 `adminActions/adminTools` deny-list 與 console MCP 還沒有對應的表(第 4 階段)。


### 0.2 專案成員與受限專案(ISS-34 第 3 階段,2026-11-21)

**這一階段改了授權行為**(只影響「受限專案」;沒有受限專案時行為完全不變,有回歸測試 `TestUnrestrictedProjectKeepsOrgVisibility`)。判斷集中在 `core/service/project_access.go`(`projectAccess`:`Sees`、`SeesDocs`、`Role`、`Admins`、`Filter*`),其他檔案不自己判斷。

**資料**:`omixor_projects.restricted`(預設 false)、`omixor_project_members (org, project, actor_id, role admin|member)`(外鍵:(org, project) → 專案 cascade、actor_id → `omixor_actors` cascade,`fk_project_members_actor`);成員是 actor(使用者或角色)。

**規則**
| 呼叫者 | 非受限專案 | 受限專案 |
|---|---|---|
| 組織管理員、伺服器管理員 | 全部 | 全部,而且是專案管理員 |
| 專案成員(member) | 全部 | 看得到、能讀寫專案裡的 Issue 與文件 |
| 專案管理員(成員角色 admin) | 全部 | 同成員,另外能加入、移除、改角色(`SetProjectMember`、`RemoveProjectMember`),能把 Issue、文件搬出專案;**不能**改受限旗標、代號、刪專案(組織管理員) |
| 一般組織成員(非成員) | 全部 | **看不到**:列表沒有、單筆 404(訊息和不存在一樣)、依 key / 舊 key / UUID 查也是 404 |
| 角色憑證 | 全部 | 預設視為非成員;被加成員才看得到專案與 Issue;文件另看 docAccess(管理員明確列出的位置),docAccess 列出的專案連專案本身也看得到 |
| 外部組織的成員、角色憑證 | 整個組織 403 | 整個組織 403 |

**其他規則**:受限專案的 Issue 只能指派給成員角色(否則 400;改成受限、移除成員角色、角色轉移或刪除時,非成員角色手上還沒領取的該專案任務取消,Issue 指派改派時對非成員只清掉);搬出受限專案要專案管理員(403),搬進去要看得到(否則 404);Issue 預設專案(repo 連結)落在看不到的受限專案時退回組織層級;使用者被移出組織、角色被刪時專案成員資格一併清掉(再加回組織不會自動恢復);`ValidateDocBody` 的範本只用呼叫者看得到的文件(受限專案的範本不能被探測)。

**入口一覽**(每個入口的「非成員看不到」測試見 `core/service/project_restricted_test.go` 的 `hiddenCases`,表驅動,對一般成員與非成員角色憑證各跑一次;另有 core/api、core/mcp、console/web 的測試):
`ListIssues`、`QueryIssues`(含搜尋、依專案)、`IssueBoard`、`GetIssue`(id、key、小寫 key、舊 key)、`ResolveIssueID`(key、別名、UUID)、`UpdateIssue`(含帶舊版本不能靠 409 取內容、搬進受限專案)、`AddIssueComment`、`DeleteIssue`、`CreateIssue`(指定專案)、`ListProjects`、`GetProject`、`ListProjectRepos`、`ListProjectMembers`、`SetProjectMember`、`RemoveProjectMember`、`GetCabinet`(受限專案與未分專案)、`GetDoc`、`ReadDoc`、`UpdateDoc`、`EditDoc`、`DeleteDoc`、`CreateDoc`、文件搬進專案、`ListChannels`(依 Issue 篩選、輸出的 issueId)、`GetChannel`、`CreateChannel`(綁 Issue)、`ListAgentTasks`、`GetAgentTask`、`ClaimTask`/`ClaimNext`/`ReportTask`(任務連結到看不到的 Issue)、`ListAgents`/`GetAgent`(docAccess 的專案 key)。MCP 人類端點與角色端點的 `issues`、`issue_create`、`issue_update`、`projects`、`cabinet`、`docs`、`doc_save`、`agent_context`、`project_save` 見 `core/mcp/project_restricted_test.go`;REST 與 client 見 `core/api/project_restricted_test.go`;CLI `omixor projects restrict|unrestrict|members|member-add|member-remove`。

**console 的限制(沒有解決)**:console 對核心只用一個身分(本機是伺服器管理員、遠端是 web 共用的權杖),核心的篩選分不出是哪位 web 使用者。目前的做法是 web 層(`console/internal/web/restricted.go`)對「登入但不是 web 管理員」的使用者把受限專案的資料整個擋掉(view、action、tool 的輸出把帶 `project` 欄位的物件與 `restricted` 專案拿掉,單筆與引數指到受限專案回 404;專案成員的 action 限 web 管理員)。這是輸出層的篩選,不是授權;**由 Issue、文件衍生的聚合沒有逐一篩選**:badges 的數字、`issue_repos` 候選、助理摘要、動態牆文字、頻道與任務的畫面、Mac App 的 MCP 代理。要真正依成員篩選,console 需要用每位使用者自己的身分呼叫核心(隨 ISS-36 console 身分第二階段)。

**已知未處理**:(1) 已經在進行中(inProgress)的角色任務,角色被移出專案或專案改成受限後仍留在角色手上(待領取的才會取消),任務說明帶有 Issue 標題與內文;(2) 頻道綁了 Issue 之後 Issue 搬進受限專案:頻道成員(頻道自己的名單)仍讀得到頻道訊息,只是不再看到綁定;(3) 附件是內容雜湊 id(unguessable),不依專案篩選;(4) 稽核(`ListAudit`,只有伺服器管理員)的 detail 含 Issue key 與專案 id;(5) 事件(incident)沒有專案關聯,不受影響。

操作表新增 3 個操作:`project.member.list`、`project.member.set`、`project.member.remove`(`CapProjectView`、`CapProjectAdmin` 兩個新能力標記;既有的 Issue、文件、任務、頻道、專案讀取操作加上 `CapProjectView`)。

### 0.3 待決定與需要協助(decision / help,2026-11-21)

資料 `omixor_decisions`(見 `core/model/decision.go`、`core/service/decision.go`);`kind = decision`(要人選一個選項,或選 `other` 附說明)或 `help`(要人做某件事,處理結果是 `done` 附備註與證據、或 `declined` 附原因)。狀態 open → decided / withdrawn;decided 可 reopen。

**授權**(操作表 8 個操作;守門測試 (a)(b)(c) 涵蓋,「未授權被拒」由 `TestAuthzDeniedMatrix` 對非成員、外部組織成員、角色憑證各跑一次;新增能力標記 `CapOwnerOrAdmin`)
| 操作 | 誰 | MCP 工具 | REST |
|---|---|---|---|
| `decision.add` | 組織成員、角色憑證(署名一律是呼叫者本人:`SelfActor`,**不接受指定「誰」**,伺服器管理員也不能冒名) | `issue_decision_add`(Admin+Agent) | `POST /decisions` |
| `decision.list` / `decision.get` / `decision.summary` | 組織成員、角色憑證 | `issue_decisions`(Admin+Agent) | `GET /decisions`、`/decisions/{id}`、`/decisions/summary` |
| `decision.answer` | **只有人**(組織成員;角色憑證 403);重複回答 409(store 條件式更新 `ErrStale`) | `issue_decision_answer`(Admin) | `POST /decisions/{id}/answer` |
| `decision.reopen` | 組織管理員或原處理人(一般成員 403、角色憑證 403);留稽核與來源 Issue 系統留言 | 無 | `POST …/reopen` |
| `decision.withdraw` | open 才能;建立者或組織管理員;角色憑證只能撤回自己提的 | 無 | `POST …/withdraw` |
| `decision.to_issue` | **只有人**;新 Issue 可指定專案(沒指定依 repo 連結規則,同 `CreateIssue`);已有來源或已撤回 409 | `issue_decision_to_issue`(Admin) | `POST …/to-issue` |

REST 路由都用 `authedOrAgent`(角色憑證能到 service,由 service 層回 403,和頻道一樣);人類端點沒有任何 `agent`、`author` 參數(`identity_agent.go` 的規則不變)。稽核:`decision.add`、`decision.answer`、`decision.reopen`、`decision.withdraw`、`decision.to_issue`。

**受限可見性(ISS-34 第 3 階段)**:有來源 Issue 的決定**繼承該 Issue 的可見性**(來源 Issue 在受限專案,只有專案成員、組織管理員、伺服器管理員看得到;角色憑證要是專案成員),沒有來源視為組織層(大家看得到);不是建立當時的快照,Issue 搬進受限專案後決定跟著看不到。判斷用 `projectAccess.Sees`(`decisionAccess`,`core/service/decision.go`)。`ListDecisions`、`GetDecision`、`DecisionSummaries`、`mine` 都依此篩選;看不到的決定單筆回和不存在一樣的 404(`answer`、`withdraw`、`reopen`、`to-issue` 也是);以看不到的 Issue 當來源提出和來源不存在一樣回 400;由決定開 Issue 指定看不到的專案和不存在的專案一樣 404。測試:`core/service/decision_restricted_test.go`、`core/api/decision_test.go` 的 `TestDecisionRestrictedRoutes`、`core/mcp/decision_test.go`。

**已知未處理**:(1) console 的 appBackend 只轉發給核心,console 畫面還沒有待決定的視圖(console 層對受限專案的篩選屬 web 層,見 0.2);(2) 稽核 detail 含 DEC 編號與 Issue key。

### 0.3b Issue 接受與放回(issue_accept,2026-11-23)

Issue 層級的負責人(`core/service/issue_accept.go`):接受 = 「這件事由我負責、我來分析拆解」,之後才拆子項,子項再各自領取(兩層不互斥)。負責人沿用 `Issue.Assignee`(actor id,資料庫本來就是 actor 外鍵,人或角色都存得進去),沒有新欄位、沒有遷移。

| 操作 | 誰 | MCP 工具 | REST |
|---|---|---|---|
| `issue.accept` | 組織成員與角色憑證(`AgentOrg`,權限同留言;受限專案看不到 404);接受者一律是呼叫者本人;已關閉 409、已被別人接受 409(先放回,或用 `issue_update` 的 assignee 轉交給角色)、已是自己冪等;角色接受自己不排任務、沒有組織鏈檢查 | `issue_accept`(Admin、Agent) | `POST …/issues/{id}/accept` |
| `issue.release` | 目前的接受者本人(人或角色憑證)或組織管理員,別人 403;沒人接受時冪等;放回取消角色還沒領取的任務 | `issue_accept` 的 `release=true` | `POST …/issues/{id}/release` |

「進行中」:看板與篩選用 `model.IssueAssigned`(`assignee` 字串或 `assigneeActor` 任一有值);人類接受者的 `assignee` 字串是空的(該欄位只輸出角色 handle),負責人在 `assigneeActor`。接受與放回各留一則系統留言並寫稽核(`issue.accept`、`issue.release`)。

### 0.4 Issue 子項(issue_item,ISS-37,2026-11-21)

資料 `omixor_issue_items`(見 `core/model/issue_item.go`、`core/service/issue_items*.go`);Issue 底下可領取、有狀態(todo / doing / blocked / done / canceled)、依賴、驗收與證據的小工作。編號 = 所屬 Issue 顯示編號 + `.` + 序號(`ISS-37.2`,序號不重用)。

**可見性:完全繼承所屬 Issue**(`itemIssue` 呼叫 `projectAccess.issueErr`,不另存可見性)。Issue 在受限專案,只有專案成員、組織管理員、伺服器管理員看得到子項(角色憑證要是專案成員);看不到的 Issue 底下的子項,所有入口(列、取、新增、領取、回報、改、取消、提升、刪除)都回和「Issue 不存在」一樣的 404(訊息帶的是呼叫者給的編號);垃圾桶裡的 Issue 當作不存在;Issue 搬進受限專案後子項跟著看不到。測試:`core/service/issue_items_test.go` 的 `TestIssueItemRestrictedProjectVisibility`、`core/api/issue_items_test.go`、`core/mcp/issue_items_test.go`。

**授權**(操作表 11 個操作 `issue.item.*`;守門測試 (a)(b)(c) 涵蓋,「未授權被拒」由 `TestAuthzDeniedMatrix` 對非成員、外部組織成員、角色憑證各跑一次;探測的是 boss 建立並領取中的子項):

| 操作 | 誰 | MCP 工具 | REST(都在 `/api/orgs/{org}/issues/{id}/items` 底下) |
|---|---|---|---|
| `issue.item.read` | 組織成員、角色憑證(`CapProjectView`) | `issue_items`(Admin+Agent) | `GET …`、`GET …/{itemId}` |
| `issue.item.add` | 組織成員、角色憑證(署名一律呼叫者本人;**角色憑證的負責人只能留空或是自己**,指給人或別的角色 403) | `issue_item_add`(Admin+Agent) | `POST …` |
| `issue.item.update` | **只有人**(角色憑證 403);完成與取消的 409;被別人持有有效租約時不能換負責人 409 | `issue_item_update`(Admin) | `PATCH …/{itemId}` |
| `issue.item.claim` | **只有人**;前置未 done 409;被別人持有有效租約 409;負責人是別的人時只有組織管理員可領;卡住的不能領 409 | `issue_item_claim`(Admin) | `POST …/claim` |
| `issue.item.release` | 領取者本人或組織管理員(`CapClaimer`);角色憑證 403 | 無(用 report status=todo) | `POST …/release` |
| `issue.item.report` / `issue.item.block` | 人:沒被別人持有有效租約、或是領取者本人、或組織管理員;**角色憑證只能回報自己綁定任務的那個子項**(狀態限 doing / blocked / done);done 需前置 done;blocked 要附原因 | `issue_item_report`(Admin+Agent) | `POST …/report`、`POST …/block` |
| `issue.item.unblock` | **只有人**(組織成員皆可,通常是做決定的人) | 無(用 report status=todo) | `POST …/unblock` |
| `issue.item.cancel` | **只有人**;已完成 409 | `issue_item_update` 的 `cancel=true` | `POST …/cancel` |
| `issue.item.promote` | **只有人**;新 Issue 預設放進來源 Issue 的專案(可指定;看不到的專案 404);已提升或已完成 409 | `issue_item_promote`(Admin) | `POST …/promote` |
| `issue.item.delete` | 建立者或組織管理員(`CapOwnerOrAdmin`);角色憑證 403 | 無 | `DELETE …/{itemId}` |

REST 路由都用 `authedOrAgent`(角色憑證能到 service,由 service 層回 403)。稽核:`issue_item.add / update / claim / release / report / block / unblock / cancel / promote / delete`。


### 0.4b commits 與 Issue 的連結(ISS-69 phase 1,2026-11-24;service/commits.go)

設計見 `docs/design-commits.md`。**phase 1 只開放給人**:組織成員用 `omx_` 使用者 token(REST 與 MCP);**角色憑證一律 403**(`commitCaller`:先 `viewer`,再拒絕 `Principal.Agent != nil`),`commits` 讀工具也不開在角色端點與執行者端點(`model.CoreTools` 的 `Agent`、`Worker` 都是 false),角色端點的 `issues(id)` 不附 commits。
**commit 本身整個組織的成員都看得到**(repo 本來就是組織追蹤的);**只有「連結」依 Issue 可見性隱藏**:受限專案裡呼叫者看不到的 Issue,回報時不建立連結、也不列在 `unmatched`(輸出與「不存在」完全一樣,不洩漏存在);`issues(id)`、`GET …/issues/{id}/commits`、依 `issue` 查 commits、手動連結與取消,對看不到的 Issue 都回和不存在相同的 404;查詢結果每筆附的 Issue 連結只含看得到的。引用解析只比對 `ISS` 與呼叫者看得到的專案代號,所以 `UTF-8`、`SHA-256`、`CVE-2024-1234` 不會連結也不會出現在 `unmatched`;垃圾桶裡的 Issue 當作不存在。回報與連結從不修改或關閉 Issue。

| 操作 | 誰 | MCP 工具 | REST |
|---|---|---|---|
| `commit.report` | 組織成員(`AgentNo`);一次一個 repo、最多 100 筆;資料標 `verified=false`;subject、body、branch、author 去控制字元並遮蔽機密(`core/redact`,同 `report_problem`;獨立的 git 物件 id 不遮) | `commits_report`(Admin;**不在 `OAuthWriteTools`**,只收 `omx_`,OAuth 讀寫也回 "not available over OAuth") | `POST /api/orgs/{org}/commits` |
| `commit.read` | 組織成員(`AgentNo`);連結依 Issue 可見性篩選 | `commits`(Admin,`ReadOnly`) | `GET /api/orgs/{org}/commits`、`GET …/commits/sync?repo=` |
| `issue.commit.read` | 組織成員(`AgentNo`、`CapProjectView`);Issue 看不到 404 | `issues(id)` 的 `commits`(最近 10 筆)與 `commits_total` | `GET /api/orgs/{org}/issues/{id}/commits` |
| `issue.commit.link` | 同 `issue.update`(成員、Issue 看得到;`AgentNo`);沒回報過的 sha 允許當裸參照 | `issue_commit_link`、`issue_commit_unlink`(Admin;**不在 OAuth 白名單**) | `POST` / `DELETE /api/orgs/{org}/issues/{id}/commits` |

REST 路由都用 `authed`(只收 `omx_` 使用者 token)。稽核:`commits.report`(repo、來源、筆數)、`issue.commit.link`、`issue.commit.unlink`;不記 commit 內容。測試:`core/service/commits_test.go`、`core/api/commits_test.go`、`core/mcp/commits_test.go`、`core/store/commit_test.go`,「未授權被拒」由 `TestAuthzDeniedMatrix` 涵蓋。

**領取與角色任務**:輕量領取(負責人是人或沒指派)只寫 `claimed_by` 與租約(長度同角色憑證 `credTTL`),不發憑證。負責人是角色時,領取同時建立並領取一筆綁定的 `agent_task`(`links = [Issue id, 子項 id]`,沿用 `claimPicked` 的憑證、租約與鎖),回傳一次性憑證;任務離開進行中(`saveTaskChange`:done、blocked、退回待領取、取消、租約逾時)會回寫子項(done → done 並記任務結果為證據、blocked → blocked、其餘 → todo);反方向子項回報 done / blocked 時任務跟著結束。由子項產生的任務不會被 `ClaimNext`、`ClaimTask`(不指定任務)撿走,也不算 Issue 指派排的任務(`dropItemTasks`)。鎖順序 `agents.mu → issueLock`,子項的寫入一律在 `agents.mu` 內。

**受限專案**:角色負責人要是專案成員(新增或改負責人時 400);角色被移出專案、Issue 搬進受限專案時,非成員角色的子項負責人清空(`pruneProjectItemsLocked`,和任務的 `pruneProjectTasksLocked` 同一個入口)。

**關閉 Issue**:不被未完成子項阻擋;Issue 輸出帶 `itemsOpen`(todo / doing / blocked)與 `itemsTotal`(不含 canceled),不存庫(`withItemCounts`,一次批次查詢;沒有子項就沒有這兩個欄位)。

**已知未處理**:(1) 沒有 `issue_block` / `issue_unblock`(Issue 級卡住旗標要動 Issue 結構與遷移,範圍比子項大,另開);(2) console 畫面還沒有子項的視圖,console 的 appBackend 只轉發給核心;(3) 等待的子項 done 時不自動解除 blocked(第二階段);等待 `user_decision` 先存文字,待決定功能整合後可指向 DEC-n;(4) 子項沒有自己的留言,完成 / 卡住 / 提升會在所屬 Issue 留系統留言;(5) CLI 不能清空欄位(旗標空字串視為沒給);(6) done / canceled 的子項不能重開(要重做請新增子項)。
---

### 0.5 MCP 統一入口 `/mcp`(ISS-54 第 1 階段 2026-11-23;第 2 階段:OAuth 帳號層級唯讀;第 3 階段:OAuth 受限寫入 `mcp:write`)

雲端新增 `/mcp`:一個連線操作帳號下所有組織,**沒有預設組織**。
- **認證**:`omx_` 使用者 token(`svc.Authenticate`,權限同該使用者、可讀寫),或 OAuth access token `omo_`(第 2 階段;帳號層級、預設**唯讀**;第 3 階段使用者明確選讀寫才有受限寫入,見下)。沒設 `OMIXOR_PUBLIC_URL`(沒有 OAuth)時 `omo_` 回 403 並說明 OAuth 沒啟用,`omx_` 照常。
- **OAuth(帳號層級、唯讀)**:Claude Code `claude mcp add --transport http … https://<host>/mcp` 後用 `/mcp` 登入一次即可,不必手動放 `omx_`。`/mcp` 的 401 帶 `WWW-Authenticate: Bearer realm="omixor", resource_metadata="<網址>/.well-known/oauth-protected-resource/mcp"`(帶了 `omo_` 但被拒再加 `error="invalid_token"`)。
  - **resource**:`<public>/mcp` 是合法的 OAuth resource(帳號層級);`<public>/mcp/org/{org}` 的既有行為完全不變;其他 resource(`<public>/mcp/foo`、別的主機…)仍回 `invalid_target`。
  - **資源綁定嚴格**:`AuthenticateOAuth(token, resource)` 要求 token 簽發時綁的 resource 與端點網址完全相同:綁 `/mcp/org/x` 的 token 不能用在 `/mcp`,綁 `/mcp` 的 token 不能用在 `/mcp/org/x`(都是 401 `invalid_token`)。授權碼換 token、refresh 都沿用綁定(帶不同的 resource → `invalid_target`)。
  - **不綁組織**:帳號層級 token / 授權碼的 `Org` 欄位是空字串(`omixor_oauth_tokens.org` 本來就是 `not null default ''`,**沒有結構異動**)。空 `Org` **不代表「所有組織」**,任何地方都不拿 token 的 `Org` 當授權依據:組織存取一律在統一入口用 `args["org"]` 與**即時成員資格**決定(退出組織後同一個 token 立刻看不到該組織)。
  - **同意畫面**:沿用既有的 client 註冊、redirect_uri 精確比對、PKCE S256、CSRF(一次性)、證明身分(evoleap 瀏覽器登入,或一次性配對碼 + 全域錯誤鎖定)、授權碼 60 秒一次性、refresh 輪替與重用偵測。帳號層級時畫面說明「授權這個應用程式以唯讀方式讀取你帳號能存取的所有組織」,不選組織、不要求是任何組織的成員(身分已由配對碼證明);`oauth.authorize` 稽核 detail 帶 `level: account`。
  - **evoleap 瀏覽器登入(主要)與配對碼(備援)**:同意畫面的主要按鈕是「用 evoleap 登入並允許」(伺服器有 `EVOLEAP_URL`、沒設 `OMIXOR_OAUTH_EVOLEAP_LOGIN=false` 才出現;本機伺服器通常沒有 evoleap,就只有配對碼),配對碼收在「改用配對碼」裡。讀寫選項兩者共用,預設唯讀。流程:表單送 `via=evoleap`(CSRF 與整個授權請求照舊重驗,`grant=write` 但沒要求 `mcp:write` 在呼叫 evoleap 之前就 400 `invalid_scope`)→ 伺服器向 evoleap `POST /api/cli-auth/start`(`redirect_uri = <public>/oauth/evoleap/callback`)拿 id,存一筆登入交易(只存雜湊、10 分鐘、單一用途,綁 client、resource、redirect_uri、code_challenge、state、選的 scope 與一個隨機的瀏覽器綁定密鑰),設 cookie `omixor_oauth_evo`(HttpOnly、SameSite=Lax、Path=/oauth,公開網址是 https 時 Secure,內容是綁定密鑰)並 303 到 `<evoleap 網址或 OMIXOR_EVOLEAP_CONSOLE>/login/cli?id=…` → 使用者登入後 evoleap 導回 `GET /oauth/evoleap/callback?id=&code=`:用 id 找交易、cookie 的綁定密鑰要一致(常數時間比對)、沒過期沒用過,先把交易標成用過,再向 evoleap 換 token、用 `resolveEvoleapUser`(規則同 `LoginWithEvoleap`,**不發 `omx_` token、不建立新使用者**,除非是 bootstrap 管理員)對上 omixor 使用者,**成功失敗都立刻 `POST /api/cli-auth/logout` 撤銷 evoleap token**,最後和配對碼共用同一個簽發函式(`approveOAuth`:帳號層級 / 組織成員規則、讀寫規則、授權碼 60 秒、稽核 `oauth.authorize`,detail 帶 `via: evoleap`,配對碼那條帶 `via: pair`)並 303 導回客戶端。id 不認得、cookie 沒有或不對、逾時、重放一律同一個通用錯誤頁;帳號停用、沒有 omixor 權限、不是組織成員則導回 `redirect_uri?error=access_denied&state=…`。交易存在 `omixor_oauth_tokens`(沒有結構異動:資料表的 kind 檢查不允許新種類,所以存成 `csrf` 種類,以 `oauth:evoleap:<id>` 的雜湊命名空間區分;`family_id` 放綁定密鑰雜湊與 state)。同意畫面的 CSP `form-action` 多允許 evoleap 核准頁的來源(只在 evoleap 登入啟用時),其他標頭不變;回呼成功不渲染頁面,直接 303。
  - **唯讀(預設)**:scope 預設是 `mcp:read`(`mcp:write` 見下一項);`Principal.ReadOnly = true`;統一入口收 `omo_` 時只開放 `model.CoreTool.ReadOnly` 的工具(`orgs`、`schema_check`、`issues`…;`schema_check` 仍需伺服器管理員,由 service 層擋),額外掛的工具不開放;其他工具回「此連線是唯讀授權」,`flowchart_render` 的 `save` 被拒。限制邏輯在 `mcp.restrictReadOnly`(與 `AdminEndpoint` 共用)。寫入預設仍需 `omx_` token;使用者明確選「讀寫」才有受限寫入(見下)。
  - **觀測**:`oa.ObserveMCP` 也包 `/mcp`(只記沒帶 token 或帶 `omo_` 的請求,`omx_` 不記;`token_state` 以 `<public>/mcp` 為 resource 判斷)。
- **org 必填**:除 `orgs`、`schema_check`(帳號層級)外,每次工具呼叫都要帶 `org`(讀取、寫入一樣);沒帶或空白 → `isError`(400),訊息列出該帳號可用的組織 id。tools/list 的規格在這個入口為這些工具加上必填的 `org`(string)。
- **每次呼叫都驗證成員資格**:組織取自 `args["org"]`,先過 `model.ValidOrgID`,再確認在呼叫者的 `ListOrgs` 裡(成員;伺服器管理員視同全部);格式不對、不存在、非成員一律同一句 403(不洩漏組織存不存在);通過後才執行,執行時 service 層仍照權限表(`orgRole`、`viewer` 等)再檢查一次能力。`Backend.Local` 的方法本身會對傳入的 org 做 service 層檢查,這道統一入口的檢查是第一道、不依賴每個方法都有。
- **每次呼叫各自的上下文**:`mcp.Hooks.CallCtx` 每次 tools/call 產生專屬的 `Session`(`OrgID` 為該次呼叫的 org),同一批請求不同呼叫可帶不同組織,不共用可被改寫的狀態。預設的連線上下文沒有組織,漏設時工具會因組織 id 不合法而失敗(fail closed)。
- **結果**:成功結果維持單一 text 區塊(JSON 不變),實際使用的組織放在結果的 `_meta`:`"_meta": {"omixor/org": "<id>"}`。同一個 HTTP 請求內 `ListOrgs` 只查一次(請求內快取,請求結束即丟棄,不跨請求或使用者共用),組織名稱也從這份快取取。
- **舊端點**:`/mcp/org/{org}` 行為不變;工具參數帶了與路徑不同的 `org` → 拒絕(相同或沒帶都可以)。唯讀授權、`HumanHiddenTools`、`RejectIdentityAgent`、MCP Apps 畫面在統一入口維持一樣的邏輯。
- **OAuth 寫入(第 3 階段,`mcp:write`):受限白名單、非伺服器管理員、預設唯讀。**
  - **scope**:`mcp:read`(預設)與 `mcp:write`(隱含 read);兩份 metadata 的 `scopes_supported` 都列出。客戶端沒指定 scope = 只給 `mcp:read`。**授予的 scope ≤ 客戶端請求 ∩ 使用者在同意畫面明確選擇**,沒有任何自動升級:授權碼的 scope 是使用者同意的、換 token 不能改(token 請求帶的 `scope` 在授權碼 grant 忽略);refresh 沿用上一代的 scope,請求帶超出的(含 `mcp:write`、不認得的)→ `invalid_scope`,只帶 `mcp:read` 可以縮小(縮小後不能再升回去)。沒有結構異動(`omixor_oauth_tokens.scope` 本來就是文字欄位,存 `mcp:read` 或 `mcp:read mcp:write`)。
  - **同意畫面**:客戶端有要求 `mcp:write` 才出現「唯讀(建議,預設選取)/ 讀寫(進階)」兩個選項,讀寫以醒目文字說明能做什麼(列出可寫入的工具類別)、不能做什麼(角色與組織結構、領取角色憑證、伺服器管理員操作…)、怎麼撤銷。沒要求時沒有選項、一律唯讀。伺服器端驗證:只有表單明確送 `grant=write` 才是讀寫(其他值、沒送都是唯讀);客戶端沒要求 `mcp:write` 卻送 `grant=write` → 400 `invalid_scope`(配對碼不消耗)。CSRF token 綁定這一次請求(client、resource、redirect_uri、code_challenge、要求的 scope 範圍):從唯讀畫面取得的 CSRF 不能拿去送改過 scope 的表單(403);一次性。拒絕、錯誤、重複送出的處理不變。`oauth.authorize` 稽核 detail 帶 `scope`(`mcp:read` 或 `mcp:read mcp:write`)。
  - **redirect_uri 允許清單**:註冊、授權、換 token 共用同一份,只收完全相同的字串:`https://claude.ai/api/mcp/auth_callback`、`https://claude.com/api/mcp/auth_callback`、Claude 桌面版 / Agent SDK 的 `claude://claude.ai/mcp-auth-callback/sdk`(沒有前綴或萬用字元,不收其他自訂 scheme),以及本機 `http://127.0.0.1|localhost[:埠]/callback`。scope 裡的 `offline_access` 在註冊、授權、換 token、refresh 都容忍:不報錯、不會被授予、不會出現在 token 的 scope、不會放寬任何權限。
  - **可寫入的工具(`model.OAuthWriteTools`,明確白名單,不是「所有非唯讀」)**:`issue_create`、`report_problem`(回報 omixor 系統問題,等同 issue_create:標 `mcp-feedback`、附非機密診斷、相同 tool+error 的未結案回報只加留言)、`issue_update`、`issue_accept`、`issue_decision_add`、`issue_decision_answer`、`issue_decision_to_issue`、`issue_item_add`、`issue_item_update`、`issue_item_report`、`issue_item_promote`、`doc_save`、`channel_create`、`channel_post`、`agent_task_report`。可用 = `ReadOnlyTools ∪ OAuthWriteTools`;其他回英文「This tool is not available over OAuth」。明確禁止並記原因的在 `model.OAuthForbiddenTools`:`agent_task_create`(排任務給角色,角色會用自己的憑證執行;ISS-60 從白名單移除,OAuth 只能 `agent_task_report` 回報)、`agent_task_claim`、`issue_item_claim`(會簽發角色憑證)、`agent_upsert`、`chain_save`、`project_save`、`cabinet_layout_save`(角色與組織結構管理)、`agent_task_transfer`、`incident_declare`、`incident_update`、`agent_context`、`channel_messages`(讀了標已讀,有副作用)、`wireframe_render`。**分類完整性測試**(`core/model/oauth_write_test.go`):登記表每個 Admin 工具必須恰好在 `ReadOnly`、`OAuthWriteTools`、`OAuthForbiddenTools` 三類之一,新增工具漏分類會失敗;白名單另有釘住的清單測試。`flowchart_render` 的 `save` 對讀寫 token 仍被拒(要存文件用 `doc_save`)。
  - **不是伺服器管理員**:`Principal.ServerAdmin()` 對 OAuth 寫入身分(`Via=ViaOAuth && !ReadOnly`,`Principal.OAuthWrite()`)一律 false,所以就算登入的使用者是伺服器管理員,OAuth 連線以一般成員權限運作:`ListOrgs` 只列自己是成員的組織、非成員的組織寫入 403、不能建立/修改/刪除組織、`schema_check`、使用者與 token 管理、`IssuePatch.Restore`、整份取代留言、代別人署名(`ResolveAuthor`)、頻道 `as` 都不可用。組織層級的讀寫授權,伺服器管理員也必須是該組織的成員(`ApproveOAuthScope`)。**唯讀的 OAuth 身分行為不變**(伺服器管理員仍看得到全部組織,但只能讀)。`CreatePairCode` 新增:OAuth 連線(`Via=ViaOAuth`)一律 403(避免自己再授權換出登入 token)。寫入仍照既有授權表(成員角色、受限專案、組織鏈邊):OAuth 不繞過任何一項。
  - **共用 helper**:`mcp.restrictOAuth`(依連線是唯讀或讀寫選 `restrictReadOnly` / `restrictOAuthWrite`),`/mcp/org/{org}` 與 `/mcp` 共用;`omx_` 不經過它、行為完全不變。統一入口的組織仍每次由 `args["org"]` 與即時成員資格決定(退出組織後同一個 token 立刻失效)。
  - **稽核**:`Service.Audit` 本來就把 `Principal.Via` 寫進稽核的 `source`,OAuth 寫入的每筆稽核 `source = oauth`(沒有另外加標記);操作紀錄的署名是該使用者本人。觀測 log:write token 呼叫白名單外的工具記 `token_state=tool_denied`(唯讀 token 仍是 `read_only_denied`)。
  - **撤銷**:refresh 重用偵測(整族撤銷)、停用帳號(`AuthenticateOAuth` 403、refresh `invalid_grant`)、退出組織(立刻)對 write token 一樣有效。**目前沒有使用者自助的撤銷介面**(沒有列出/撤銷 OAuth 授權的畫面或 API):要撤銷只能退出組織、請伺服器管理員停用帳號,或等 access 10 分鐘、refresh 30 天到期。同意畫面如實說明這一點。
  - **已知風險與殘留**:(1) 寫入的客戶端是 LLM,prompt injection 可能讓它在白名單內亂寫(建 Issue、發頻道訊息、存文件):靠使用者明確選讀寫與白名單限縮,不能完全消除;`agent_task_create`(會讓角色以自己的憑證開始做事)因此不在白名單,OAuth 只能回報任務。(2) 沒有自助撤銷介面(見上)。(3) `channel_messages`(讀頻道)未開放給讀寫 token,頻道只能發訊息、不能讀:若要開要先決定「標已讀」算不算可接受的副作用。(4) refresh 請求的 `scope` 超出原授權回 `invalid_scope`(符合 RFC 6749),不照原 scope 忽略;若有客戶端 refresh 時回報當初「要求」的 scope 而非「授予」的 scope,會被拒(MCP TypeScript SDK 的 refresh 不帶 scope)。
- 測試:`core/mcp/unified_test.go`、`core/mcp/unified_oauth_test.go`、`core/service/oauth_account_test.go`、`server/mcp_unified_test.go`、`server/mcp_unified_oauth_test.go`;第 3 階段:`core/model/oauth_write_test.go`、`core/service/oauth_write_test.go`、`core/mcp/oauth_write_test.go`、`server/oauth_write_test.go`。


## 1. 身分與憑證

### 1.1 身分種類

| 身分 | 資料表示 | 代表誰 | 取得方式 | 有效期 / 作廢 | 轉成 `Principal` 的位置 |
|---|---|---|---|---|---|
| 一般使用者(含組織成員) | `Principal{User(role=member), Token}` | 一個人 | evoleap(Google 驗證)換 token:`LoginWithEvoleap`(`service/service.go`);LDAP:`LoginWithLDAP`(`service/ldap.go`,伺服器以環境變數啟用);手機配對碼:`CreatePairCode`/`ClaimPairCode`(`service/pair.go`) | token 前綴 `omx_`,只存 SHA-256;`OMIXOR_TOKEN_DAYS`(預設 90 天);`Logout`(自己)、`RevokeToken`(伺服器管理員)作廢;帳號 `Disabled` 時 `Authenticate` 直接 403 | `Service.Authenticate(raw)`(`service.go:307`),被 `api.authed`(`api/api.go`)、`server.Handler` 的 `/mcp/org/{org}`、`/mcp/worker/{org}`、`server/local.go` 的 `/local/*` 使用 |
| 伺服器管理員 | 同上,`User.Role == "admin" && !Disabled`(`model.Principal.ServerAdmin()`) | 一個人,在**每個組織**視同 admin | 第一個使用者:`OMIXOR_ADMIN_BOOTSTRAP_EMAIL`(僅在使用者數為 0 時);LDAP:`OMIXOR_LDAP_ADMINS` 名單(每次登入確認,不會自動降級);之後由 `SaveUser` 指定 | 同上。每次 `Authenticate` 都從資料庫讀 role,降級立即生效 | 同上;`orgRole` 對 ServerAdmin 回傳 `"admin"`(`service.go:415`) |
| 組織角色(admin / member) | `Member{Org,UserID,Role}`;不在 Principal 裡,`orgRole()` 每次查 `Store.MemberRole` | 一個人在某組織內 | 組織管理員 `SaveMember`;**名單上沒有該 email 時,只有伺服器管理員會建立待連結的伺服器使用者;組織管理員只能加已存在的使用者**(R-9 已修) | 隨成員移除(`RemoveMember`) | `service.orgRole` / `needOrgAdmin` |
| 角色憑證(task-bound agent credential) | `Principal{User(id=email="agent:<id>", role=member), Agent:*AgentIdentity{Org,AgentID,TaskID}}` | 一個虛擬角色(代為執行一筆任務) | 組織成員 `ClaimTask`/`ClaimNext` 領取時簽發:32 bytes 隨機密鑰(base64url)只回傳一次,DB 只存雜湊;使用格式 `Bearer <agentId>:<secret>` | `OMIXOR_AGENT_CRED_HOURS`(預設 2 小時);回報進度續約但總長上限 `MaxLeaseTotal` = 8 小時;任務離開 `inProgress`(done/blocked/canceled/重新排隊)、角色被刪、逾時、重領都作廢;同一任務同時只有一組有效 | `Service.AuthenticateAgent`(`agents_auth.go:49`):常數時間比對、失敗一律同一個 401、同 IP 每分鐘超過 10 次失敗回 429。被 `mcp.AgentEndpoint`(`/mcp/agent`)與 `api.authedOrAgent`(少數 REST 路由,見 §1.2)使用 |
| 執行者代角色(worker) | 用**人的** `Principal` 呼叫 → `claimedAgent()` 轉成與角色憑證相同的 `Principal{Agent}` | 領取任務的那個人,以角色署名回報/留言/委派 | `/mcp/worker/{org}` 的 `dispatch_*`;需是領取者本人、租約未過期、帶領取驗證碼(`ClaimCode`,由憑證雜湊算 6 位數) | 隨租約 | `service.claimedAgent`(`agents_auth.go:107`),用在 `WorkerReport`、`WorkerComment`、`WorkerDelegate` |
| console 本機模式 | `console/internal/core.localPrincipal` = `Principal{User{Email:"omixor-console", Role:"admin"}}`(**全程共用的單一指標**) | 這台 console 的所有使用者 | 不需登入(未啟用 web LDAP 時 `currentUser` 直接回管理員) | 無 | `backend.Local{P: localPrincipal, EnsureOrg: true}`(`console/internal/core/issues_remote.go:64,103`) |
| console 遠端模式 | `backend.Remote` + 一把共用的 omixor server token(`issueRemoteExchange` 用 evoleap 連線換得) | 「登入 server 的那個帳號」,**不是**每個 web 使用者 | 在 console 用 evoleap 登入一次 | 依 server token 期限 | `core/backend/remote.go`;權限由 server 依該帳號判斷,所有 web 使用者共用 |
| console web 使用者 | `auth.User{ID, Name, Admin}`(`console/internal/auth`) | 用 console 網頁的人 | web LDAP(`auth.json`;`OMIXOR_AUTH=off` 強制關閉登入並回到本機模式);session cookie `ops_session`(HttpOnly、SameSite=Strict);`/api/issues/*` 另可用 `OMIXOR_API_TOKEN`(至少 16 字元,常數時間比對) | session 時數(`SessionHours`) | `web.guard` → `withUser`;`isAdmin()` 只看 `auth.User.Admin`(LDAP 管理員群組),**與 core 的組織角色無關** |
| server local(omixor-local) | `server/local.go`:只有一個伺服器管理員使用者,token 由 `OMIXOR_LOCAL_TOKEN` 或 `credentials.json` 提供;只聽 127.0.0.1 | 本機守護服務的唯一使用者 | 啟動時 `ensureUser` 強制該使用者 admin、作廢同裝置舊 token | 每次啟動換(App 模式)或沿用(獨立模式) | 同 `Authenticate`;另有 `/local/*` 端點要求 `ServerAdmin` |

### 1.2 入口 → Principal 對照

| 入口 | 認證 | Principal | 備註 |
|---|---|---|---|
| REST `/api/*`(`core/api`) | `authed`:`Bearer omx_…` | 人 | 角色憑證**不接受**(`Authenticate` 要求 `omx_` 前綴) |
| REST 少數路由 | `authedOrAgent`:`omx_` 或 `<agentId>:<secret>` | 人或角色 | chain GET、`agent-tasks/delegate`、channels 全部、incidents(除 import)、mcp-servers GET 與 `mcp-tools/resolve`、`/api/modules`、`orgs/{org}/system` GET、integrations 的 GET |
| MCP Admin `/mcp/org/{org}` | `omx_`;或 OAuth access token `omo_`(Claude 自訂 connector,預設**唯讀**、使用者明確選讀寫才可寫入受限白名單且**非 ServerAdmin**,綁定該組織的 MCP 網址;統一入口 `/mcp` 的 `omo_` 是帳號層級唯讀,見 §0.5) | 人 | `backend.Local{P: p}`;組織取自路徑。唯讀 `omo_` 的 `Principal.ReadOnly = true`(讀寫的 `ReadOnly=false` 但 `Via=ViaOAuth`,`ServerAdmin()` 一律 false,見 §0.5):`tools/list` 只列 `model.CoreTool.ReadOnly` 的工具、其他工具呼叫回「此連線是唯讀授權」、`flowchart_render` 的 `save` 被拒;`Service.Authenticate` 看到 `omo_` 一律 403(所以 REST、`/mcp/worker` 都不收)。授權流程見 `server/README.md` 的 OAuth 一節 |
| MCP Agent `/mcp/agent` | `<agentId>:<secret>` | 角色 | `AgentEndpoint`:`Allow` = 白名單 ∩ `AgentToolSet`(依角色目前設定);`Guard` 強制 `agent` 參數為憑證角色(規格不再列 `agent`;帶別的角色仍被擋)。**所有人類端點(`/mcp/org/{org}`、`AdminEndpoint`、console `/mcp`)不接受「身分假冒」型的 `agent` 參數**(`mcp/identity_agent.go`:`IdentityAgentTools`、`RejectIdentityAgent`):帶了回 400 固定句子「不能指定角色身分;要以角色身分工作請先領任務(agent_task_claim)取得角色憑證,連角色端點(/mcp/agent)」,不執行、不寫入,署名一律是呼叫者。保留對象選擇型 `agent`(`agent_context`、`agent_tasks`、`agent_task_claim`、`agent_upsert`/`agents` 的 id、`dispatch_queue`/`dispatch_claim`)。`agent_task_delegate` 的委派者只能由角色憑證決定,人類端點(含 console `/mcp`)不列、呼叫一律 400 |
| MCP Worker `/mcp/worker/{org}` | `omx_` | 人 | 只開 7 個 `dispatch_*`(`WorkerToolAllow` 寫死的 map) |
| console web `/api/*` | session cookie(若啟用 LDAP);否則無 | `localPrincipal`(本機)或共用 server token(遠端) | `/api/tool/{name}`、`/api/action/{name}`;權限靠 `adminActions/adminTools` deny-list |
| console MCP(預設 127.0.0.1:3765) | **`Authorization: Bearer omxm_…`(console 的共享 MCP 權杖,2026-11-20 起;沒帶或不符一律 401;同一來源「錯誤嘗試」太多次 429,帶正確權杖的請求永遠放行;整個連接埠先過 Host 白名單,不符 403;環境變數 `OMIXOR_CONSOLE_MCP_TOKEN` 最短 32 字元,有設但無效就拒絕啟動)**:`/mcp`、`/mcp/worker` 與其餘所有路徑都要(`console/internal/mcp/server.go` 的 `NewTokenGate`、核心 `mcp.RequireBearer`) | 通過後仍是 `localPrincipal`(SA);**第一階段所有持權杖者共用同一身分,署名仍是字串 `mcp`**(第二階段「每人各一把、署名成本機使用者」隨 ISS-36) | 另掛 `/mcp/agent`(本機有 PG 時驗角色憑證,不用 console 權杖)與 `/mcp/worker`(組織取自 console 目前選的組織)。權杖由 console 第一次啟動時產生(`omxm_` + 32 位元組隨機),存在加密的機密表(有資料庫)或 console 資料夾的 `mcp_token`(0600);`OMIXOR_CONSOLE_MCP_TOKEN` 可覆蓋;完整權杖只有管理員專用 action `mcp_token_reveal`/`mcp_token_rotate` 取得(寫進動態牆) |

---

## 2. 授權檢查族一覽

| # | 機制 | 位置 | 檢查什麼 | 預設 | 代表使用者 / 數量 |
|---|---|---|---|---|---|
| A | `needServerAdmin` | `service/service.go:331` | `p.ServerAdmin()`(**沒檢查 `p.Agent`**,但角色身分的 `User.Role` 固定是 member,所以實質拒絕) | 拒絕 | ListUsers、SaveUser、ListTokens、RevokeToken、ListAudit、CreateOrgWith(CreateOrg)、UpdateOrg、DeleteOrg(8) |
| B | `orgRole` | `service.go:415` | 角色身分**預設拒絕**;組織 id 格式;組織存在(404);ServerAdmin → admin;否則 `MemberRole`(非成員 403) | 拒絕 | ListMembers、AssignTask、ClaimTask、ClaimNext、ReleaseTask、DeleteDoc、DeleteIssue、SaveAttachment、GetAttachment、ListOrgTemplates、PreviewOrgTemplate(直接約 11 個)+ channelActor 的非角色分支 |
| C | `needOrgAdmin` | `service.go:438` | `orgRole == "admin"` | 拒絕 | SaveMember、RemoveMember、SaveAgent、DeleteAgentTo、TransferAgentTasks、RunRoutine、CancelTask、RequeueTask、Create/Update/DeleteProject、SaveChain、SaveCabinetLayoutIf、ImportCabinet/Issues/Incidents/Agents、ExportIssues、SaveSystemSettings、ApplyOrgTemplate、`needMCPAdmin`(+功能開關)(約 24) |
| D | `viewer` | `agents.go:56` | 人:同 B;角色:`Agent.Org == org` | 拒絕(跨組織) | 約 22 個,**角色憑證可達**:Issue 全套(Get/List/Create/Update/AddComment)、事件(List/Get/Declare/Update)、GetChain、DelegateTask、ListAgents/GetAgent(只看自己)、ListAgentTasks/GetAgentTask(只看自己)、ReportTask、GetProject/ListProjects、GetMCP/ResolveMCPTools、GetSystemSettings |
| E | `notAgent` | `agents.go:84` | `p.Agent == nil` | 拒絕角色 | ClaimTask、ClaimNext、ReleaseTask、DeleteDoc、`claimedAgent` |
| F | `docViewer` + `Agent.CanReadDoc/CanWriteDoc/CanWriteIn` | `cabinet.go:102`、`model/agents.go:111-138` | `viewer` + 角色的 `docAccess`(key:區塊 id 或頁籤網址,專案用 `<專案 id>:` 前綴;頁籤優先於區塊;`none` 與未列 = 無) | 角色無權限 | GetCabinet、GetDoc(ReadDoc)、CreateDoc、UpdateDoc(EditDoc);角色**不能刪文件、不能改結構**(DeleteDoc 走 B+E,SaveCabinetLayoutIf 走 C) |
| G | `channelActor` + `chanActor.canSee/canManage` | `channel.go:123-166` | 角色:`viewer` + 只能碰自己是成員的頻道、不可建立/改設定/改成員;人:`orgRole`,組織管理員看得到全部頻道;**ServerAdmin 可用 `as` 以任何節點身分操作** | 看不到回 404 | 11 個頻道方法 |
| H | 組織鏈 | `chain.go:266`(`memberAssign`)、`:283`(`DelegateTask`)、`chainReport` | 一般成員派工需鏈上有 `user:<自己> → agent:<目標>`;角色委派需 `agent:<自己> → agent:<目標>`;管理員派工不受限 | 拒絕並說明可派給誰 | AssignTask、DelegateTask、WorkerDelegate |
| I | 領取者本人 | `agents_auth.go:107`(`claimedAgent`)、`agents_claim.go:329`(`ReleaseTask`) | 任務 `ClaimedBy == 呼叫者`、租約有效、領取驗證碼;Release 允許組織管理員 | 拒絕 | WorkerReport/Comment/Delegate、ReleaseTask |
| J | `canEditServer` / `canEditOrg` | `system.go:293`、`:141` | 算 `canEdit` 旗標與系統層寫入;`GetServerFeatures` 另外明確拒絕角色憑證 | — | GetServerFeatures、SaveServerFeatures、Get/SaveSystemSettings、GetMCP |
| K | 自我檢查 | `pair.go`(`CreatePairCode`)、`Logout` | 只能對自己;角色身分與配對碼 token 不能產生配對碼 | 拒絕 | 2 |
| L | 功能開關(能力可用性) | `model/features.go`、`service/system.go:featureEnabled` | 系統層 ∧ 組織層 ∧ 頁面規則 | 依 Default | **core 只在兩處強制**:`mcpGateway`(`needMCPAdmin`、讀取)與樣板(`usableTemplate`)。cabinet、channels、incidents 等功能開關在 core API/MCP **不強制**,只影響 console 畫面與 console MCP 的範例資料工具(`console/internal/mcp/feature_gate.go`) |
| M | MCP 工具表 | `model/tools.go`、`mcp/endpoints*.go`、`service/agenttools.go` | `Admin`/`Agent`/`Worker` = **哪個端點列出此工具**;`Needs` = 角色端點開放條件(`NeedDocRead/Write/Channel/Downstream`),僅影響 `tools/list` 與 `tools/call` 的 `Allow` | 沒列就是「沒有這個工具」 | 38 個工具。**這不是授權,只是曝光**;真正的拒絕仍在 service(見 §3.2) |
| N | console deny-list | `console/internal/web/admin.go:22`(`adminActions`,約 44 項)、`:73`(`adminTools`,5 項) | `auth.User.Admin`(web LDAP 管理員) | **放行**(清單外一律放行) | `/api/action/*`(註冊約 115 個)、`/api/tool/*` |
| O | console 端點層防護 | `web/web.go:234`(`guard`) | Host 白名單(防 DNS rebinding)、Origin 白名單、POST 必須 JSON(CSRF)、啟用 LDAP 時 `/api` 要登入;非 loopback 位址且未啟用 LDAP 時啟動直接 fatal | — | console 全部 `/api/*` |

### 2.1 AST 掃描結果:97 個帶 Principal 的公開方法

| 分類 | 數量 | 說明 |
|---|---|---|
| 本體直接呼叫 A–E 之一 | 64 | 其中 22 個用 `viewer`(角色憑證可達,見 D 列) |
| 經由輔助函式 / 呼叫另一個已檢查的方法 | 31 | 逐一人工驗證如 §4.1 |
| 無任何檢查 | 2 | `Audit`(內部寫日誌 helper,不是操作);`CreatePairCode`(自我檢查 K,見 §4.1) |
| 名稱碰撞造成的誤判 | — | `Logout` 被工具判為「呼叫 `RevokeToken`」是 `Store.RevokeToken` 與 `Service.RevokeToken` 同名;實際 `Logout` 只作用於自己的 token,安全 |

不帶 Principal 的公開方法:`LoginWithEvoleap/LDAP`、`ClaimPairCode`(未登入入口,有頻率限制)、`AuthenticateAgent`、`RunDueRoutines`/`StartRoutines`(內部排程)、`AgentToolSet`/`AgentToolsOf`/`AgentToolNames`(給 MCP 端點列工具,不檢查權限)、`EnsureOrgsForIssues`(啟動時)。

---

## 3. 操作 × 入口矩陣

圖例:**M** = 組織成員、**OA** = 組織管理員、**SA** = 伺服器管理員、**A** = 角色憑證(限自己的組織/任務)、**L** = 領取者本人。「—」= 該入口沒有此操作。
console 欄:**不擋** = 不在 deny-list(任何已登入 web 使用者可做,實際權限由 `localPrincipal`/共用 token 決定);**WA** = web 管理員(deny-list)。MCP 欄寫「表」是工具表的標記(Admin/Agent/Needs)。

### 3.1 依功能分組

| 分組 | 操作 | REST(core/api) | MCP | console | 不一致 |
|---|---|---|---|---|---|
| 登入 | evoleap / LDAP 登入、配對碼兌換 | 無需登入(頻率限制) | — | web 自己的 LDAP 登入 | 兩套登入互不相通 |
| | `me`、`logout`、`pair` | 登入者;pair 排除角色與配對 token | — | — | |
| 組織與成員 | 列組織 | M(SA 全部) | `orgs` 表 Admin | `org_switch` 不擋 | console 的 `org_switch` 是**全域狀態**(所有 web 使用者共用目前組織) |
| | 建立 / 改 / 刪組織 | SA | — | `org_create/update/delete` WA | console 只擋 web 管理員,實際再由 `localPrincipal`(SA)或共用 token 決定 |
| | 列成員 | M | — | view `members` | |
| | 加入 / 改角色 / 移除成員 | OA | — | `member_save/remove` WA | `SaveMember` 組織管理員只能加已存在的使用者,新 email 要伺服器管理員(R-9 已修) |
| Issue | 列 / 查 / 看板 / 取單筆 | M | `issues` 表 Admin+Agent | 不擋 | REST 不接受角色憑證;角色只能經 MCP |
| | 建立 / 修改 / 留言 | M | `issue_create/update` 表 Admin+Agent | 不擋 | 角色與成員改 `assignee` 要過組織鏈(`checkIssueAssign`;沒設鏈不限、OA 不限;R-3 已修) |
| | 刪除 | M | —(無工具) | `issue_delete` 不擋 | 一般成員即可刪除任何 Issue;是否設計如此待確認(R-10) |
| | 匯出 / 匯入 | OA | — | console 只有 CLI import | |
| | 上傳 / 讀附件 | M(角色拒絕) | — | 不擋 | |
| 角色與任務 | 列角色 / 取角色 / 可用工具 | M(A 只看自己) | `agents` 表 **Admin only** | 不擋 | service 允許 A 讀自己,MCP `agents` 不開給 Agent(`agent_context` 取代) |
| | 新增 / 修改 / 刪除角色、轉移任務、跑例行 | OA | `agent_upsert`、`agent_task_transfer` 表 Admin | `agent_upsert/delete/task_transfer/routine_run` WA(+`adminTools`) | console `/mcp` 的同名工具不受 deny-list 約束(R-1) |
| | 列 / 取任務 | M(A 只看自己) | `agent_tasks` 表 Admin+Agent | 不擋 | |
| | 派任務 | OA 任意;M 需鏈上有邊 | `agent_task_create` 表 Admin | 不擋 | console 呼叫的 `localPrincipal` 是 SA,**非管理員的 web 使用者不受鏈限制**(R-6) |
| | 領取 / claim-next | M(角色拒絕);`ClaimTask` 現在檢查角色是否 active(R-7,已修) | `agent_task_claim` 表 Admin;`dispatch_claim` Worker | 不擋 | `ClaimNext` 只挑 active 角色,`ClaimTask` 也檢查(R-7,已修) |
| | 回報 | M **可回報任何任務**;A 只能自己綁定的那筆且狀態限 inProgress/blocked/done | `agent_task_report` 表 Admin+Agent;`dispatch_report` Worker(L+驗證碼) | 不擋 | 成員改別人領取中的任務的 canceled/done 現在要領取者或 OA(R-2 已修;(c) 署名仍是 agent:<角色>) |
| | 取消 / 重新排隊 | OA | — | `agent_task_cancel/requeue` WA | `ReportTask` 的 canceled 現在也要 OA 或領取者(R-2,已修) |
| | 放棄領取 | L 或 OA | `dispatch_release` Worker | 不擋 | console 內所有 web 使用者都是同一個領取者(`omixor-console` 或共用帳號),「領取者本人」失去意義 |
| | 委派 | A(依鏈);SA 可帶 `from` | `agent_task_delegate` 表 **Agent only**(需有下游) | 人類端點(含 console `/mcp`)不列、呼叫一律 400(`HumanHiddenTools`) | 委派者只能由角色憑證決定;console 的 `webExtraCoreTools` 仍保留 `agent_task_delegate` 的規格(核心實作要有規格才能登記),但 `/mcp` 不列也不能呼叫(見 §6) |
| | 執行者回報 / 留言 / 委派 | L + 驗證碼 | `dispatch_*` Worker | `/mcp/worker` 要 console 權杖(R-1) | `Worker` 旗標沒有被程式使用,實際白名單是 `WorkerToolAllow`(兩份各自維護) |
| 文件櫃 | 讀結構 / 文件、分段讀 | M;A 受 docAccess | `cabinet`、`docs` 表 Admin+Agent+`NeedDocRead` | 不擋 | |
| | 新增 / 改 / 搬 / 局部改 | M;A 受 docAccess 可寫 | `doc_save` 表 Admin+Agent+`NeedDocWrite` | 不擋 | 管理端點不再接受 `agent` 參數(400);docAccess 檢查只在角色端點的 MCP 工具內做(`mcp/tools_cabinet.go`),不是 service(R-12) |
| | 刪文件 | M(A 拒絕) | —(無工具) | `cabinet_doc_delete` 不擋 | |
| | 改結構 / 匯入 | OA | `cabinet_layout_save` 表 Admin | `cabinet_layout_save` WA | |
| 專案 | 列 / 取專案 | M、A(A 沒有任何 docAccess 時列空、取 403;R-8 已修) | `projects` 表 Admin+Agent+`NeedDocRead` | 不擋 | service 與文件櫃一致檢查 docAccess(R-8 已修),MCP 另有 `Needs` |
| | 建 / 改 / 刪專案 | OA | `project_save` 表 Admin | `project_create/update/delete` WA | |
| 頻道 | 列 / 取 / 讀訊息 / 標已讀 / 收件匣 | M(OA 看全部);A 只看自己是成員的 | `channels`、`channel_messages` 表 Admin+Agent+`NeedChannel` | 不擋 | |
| | 建立 | M(A 拒絕) | `channel_create` 表 Admin | 不擋 | |
| | 發訊息 | 頻道成員(A 署名角色;SA 可 `as`) | `channel_post` 表 Admin+Agent+`NeedChannel` | 不擋 | |
| | 改名 / 加減成員 | 頻道成員或 OA(A 拒絕) | — | `channel_update/members` 不擋 | |
| 事件 | 列 / 取 | M、**A** | `incidents` 表 **Admin only** | 不擋 | REST 對角色憑證開放,MCP 不開(R-4) |
| | 宣告 / 更新 | M、**A** | `incident_declare/update` 表 **Admin only** | `incident_note` 不擋 | 同上 |
| | 匯入 | OA | — | — | |
| 待決定 | 提出 / 列 / 取 / 摘要 | M、A(署名一律是呼叫者本人;依來源 Issue 的專案可見性篩選) | `issue_decision_add`、`issue_decisions` 表 Admin+Agent | 不擋 | 見 0.3 |
| | 處理 / 開 Issue | M(A 403) | `issue_decision_answer`、`issue_decision_to_issue` 表 Admin | 不擋 | 見 0.3 |
| | 撤回 / 重開 | 撤回:建立者或 OA(A 只能撤回自己提的);重開:原處理人或 OA(A 403) | — | 不擋 | 見 0.3 |
| Issue 子項 | 列 / 取 / 新增 | M、A(繼承所屬 Issue 的專案可見性;A 新增時負責人只能是自己或留空) | `issue_items`、`issue_item_add` 表 Admin+Agent | 不擋 | 見 0.4 |
| | 改 / 取消 / 提升 / 領取 / 解除卡住 | M(A 403) | `issue_item_update`、`issue_item_promote`、`issue_item_claim` 表 Admin | 不擋 | 見 0.4 |
| | 回報(done / blocked / doing) | 人:領取者或無有效租約或 OA;A:只限自己綁定任務的子項 | `issue_item_report` 表 Admin+Agent | 不擋 | 見 0.4 |
| | 放棄領取 / 刪除 | 領取者或 OA / 建立者或 OA(A 403) | — | 不擋 | 見 0.4 |
| 組織鏈 | 讀 | M、A | `chain` 表 **Admin only**(但 `agent_context.chain` 對 Agent 提供上下游) | view | service/REST 對角色開放,工具不開 |
| | 改 | OA | `chain_save` 表 Admin | `chain_save` WA(+`adminTools`) | |
| 設定與整合 | 組織功能開關讀 | M、A | — | view `system` | |
| | 組織功能開關寫 | OA | — | `system_save` WA | |
| | 系統層功能開關讀 | 任何登入者(A 拒絕) | — | view | |
| | 系統層功能開關寫 | SA | — | `server_features_save` WA | |
| | 外部 MCP 閘道:讀 | M(完整)/A(只剩自己權限、無連線細節) | — | — | |
| | 外部 MCP 閘道:寫(伺服器、工具、grants) | OA 且 `mcpGateway` 開 | — | — | |
| | 套用組織樣板 | OA;預覽 M | — | `org_create` 內含 | |
| | GitHub / GitLab 設定 | 讀 M/A、寫 OA | — | `github_*/gitlab_*` WA | |
| 稽核 / token / 使用者 | 使用者、token、稽核 | SA | — | — | 稽核只記**成功的寫入**與登入/憑證失敗,不記讀取、不記被 403 拒絕的嘗試 |
| console 專有 | 連線(含權杖與密鑰)、GCP、Jira、Facebook、旁白、自動執行 | — | `gcp*`、`fb_*`、`github*`、`server`、`voice_*` 等,無權限表 | `connection_*`、`gcp_settings`、`jira_setup`、`facebook_*`、`voice_settings`、`agent_runner_*` WA | **`jira_import`、`facebook_publish`、`facebook_draft_*`、`gcp_refresh`、`integration_set`、`automation_*`、`deployment_*`、`server_reboot`(範例資料)不在 deny-list**;`jira_import` 在本機模式直接寫 store(`issues_remote.go` 的 `issueSave`,不經 service、無稽核) |

### 3.2 MCP 工具表(`model/tools.go`)與 service 實際檢查是否吻合

| 工具 | 表上標記 | service 實際 | 吻合? |
|---|---|---|---|
| `orgs` | Admin | `ListOrgs`:任何登入者(角色身分查不到組織) | 吻合 |
| `issues` / `issue_create` / `issue_update` | Admin,Agent | `viewer` | 吻合(R-3 已修) |
| `agents` | Admin | `ListAgents`:`viewer`,A 只回自己 | **不吻合**:service 允許角色,表不開 |
| `agent_context`、`agent_tasks`、`agent_task_report` | Admin,Agent | `viewer` | 吻合(`agent_task_report` 見 R-2) |
| `agent_task_claim` | Admin | `ClaimTask`:M、拒絕角色 | 吻合 |
| `agent_upsert`、`agent_task_create`、`agent_task_transfer` | Admin | OA(`agent_task_create` 為 M+鏈) | **名稱誤導**:`Admin` 旗標 = 「端點類別」,不代表「需要管理員」;一般成員看得到工具,呼叫才 403 |
| `agent_task_delegate` | Agent + `NeedDownstream` | `DelegateTask`:A 依鏈;SA 可 `from` | 吻合;人類端點(含 console `/mcp`)不列、呼叫回 400(`HumanHiddenTools`),只有角色端點可用;`webExtraCoreTools` 只留規格 |
| `projects` | Admin,Agent,`NeedDocRead` | `ListProjects`:`viewer`,**不看 docAccess** | **不吻合**:`Needs` 只在 MCP 層強制 |
| `project_save`、`cabinet_layout_save`、`chain_save` | Admin | OA | 同上(名稱誤導) |
| `cabinet`、`docs` | Admin,Agent,`NeedDocRead` | `docViewer` + docAccess | 吻合(`Needs` 與 service 雙重) |
| `doc_save` | Admin,Agent,`NeedDocWrite` | `docViewer` + `CanWriteIn` | 吻合 |
| `chain` | Admin | `GetChain`:`viewer`(A 可) | **不吻合**:service/REST 對角色開放,工具不開 |
| `channels`、`channel_messages`、`channel_post` | Admin,Agent,`NeedChannel` | `channelActor`(成員才行) | 吻合 |
| `channel_create` | Admin | 角色拒絕 | 吻合 |
| `incidents`、`incident_declare`、`incident_update` | Admin | `viewer`(A 可,執行者記為 `ai:<角色>`) | **不吻合**:REST 角色憑證可寫事件,MCP 不開 |
| `wireframe_render`、`flowchart_render` | Admin,Agent | 純渲染;`save` 走巢狀的 `docs/doc_save`(`RunNested` 套用同一組 Allow/Guard) | 吻合 |
| `dispatch_*`(7) | Worker | 各 service 方法(M / L) | 吻合;`Worker` 旗標未被程式使用,`WorkerToolAllow`(`mcp/endpoints_worker.go`)是獨立寫死的 map,僅靠測試不保證兩者一致 |
| `Needs` 計算 | `service/agenttools.go:agentTools` | `len(g.DocAccess) > 0` 就算有 `docRead`(**含 `none` 值**;`SaveAgent` 會移除 `none`,但經 `ImportAgents` 匯入的資料可能殘留) | 低風險差異 |

另外 console `/mcp`(`console/internal/mcp/handler.go`:`coreToolDefs`)取規格時納入「Admin 工具 + `webExtraCoreTools`(`agent_task_delegate`)」,但 `agent_task_delegate` 不會列給呼叫者(`feature_gate.go` 以 `HumanHiddenTools` 過濾),呼叫一律 400;對外實際開放的集合與核心 Admin 端點(`/mcp/org/{org}`)相同。人類端點對身分假冒型的 `agent` 參數也一律回 400(`core/mcp/identity_agent.go`,見 §6)。

---

## 4. 漏檢與可疑處(皆已讀碼驗證;「待確認」另標)

### 4.1 逐一檢查 33 個「無直接檢查」的方法

| 方法 | 位置 | 實際保護 | 風險 |
|---|---|---|---|
| `Audit` | `service.go:141` | 內部 helper,非操作 | 無 |
| `CreatePairCode` | `pair.go:71` | 自我檢查:拒絕 `nil`、角色身分、空 user id、配對碼 token;只能替自己產生 | 無(註:配對得到的新 token 權限等同發碼者,若發碼者是 SA 則手機也是 SA,低) |
| `CreateOrg` → `CreateOrgWith` | `service.go:462` | `needServerAdmin` | 無 |
| `DeleteAgent` → `DeleteAgentTo` | `agent_transfer.go:63` | `needOrgAdmin` | 無 |
| `SaveCabinetLayout` → `SaveCabinetLayoutIf` | `cabinet.go:411` | `needOrgAdmin`(`ApplyOrgTemplate` 內部以呼叫者的 Principal 呼叫,呼叫者已是 OA) | 無 |
| `DeleteMCPServer`、`SaveMCPServer`、`SaveMCPTools`、`SaveMCPGrants` | `mcp.go:228,332,370,452` | `needMCPAdmin` = `needOrgAdmin` + 功能開關 | 無 |
| `GetCabinet`、`GetDoc`、`ReadDoc`、`CreateDoc`、`UpdateDoc`、`EditDoc` | `cabinet.go:147,178,589,232,277,651` | `docViewer` + docAccess;`EditDoc` → `updateDocLocked` 再檢查 `CanReadDoc/CanWriteDoc` | 無(另見 R-11) |
| `ListChannels`、`GetChannel`、`CreateChannel`、`UpdateChannel`、`ChangeChannelMembers`、`ListChannelMessages`、`PostChannelMessage`、`MarkChannelRead`、`ChannelInbox` | `channel.go:291-673` | `channelActor`(含 viewer / orgRole)+ `visibleChannel`/`canManage` | 無;OA 可讀所有頻道(含他人建立的私下頻道)、SA 可用 `as` 以任何節點身分發言/標已讀,屬設計 |
| `QueryIssues`、`IssueBoard` | `issues.go:103,116` | 呼叫 `ListIssues`(viewer) | 無 |
| `GetServerFeatures` | `system.go:296` | 明確拒絕 nil 與角色憑證;其餘任何登入者可讀 | 無 |
| `SaveServerFeatures` | `system.go:308` | `canEditServer` | 無 |
| `AgentTools` | `agenttools.go:40` | `GetAgent`(viewer;角色只能取自己) | 無 |
| `Logout` | `service.go:325` | 只作用於呼叫者自己的 `Token.ID` | 無 |
| `WorkerReport`、`WorkerComment`、`WorkerDelegate` | `agents_auth.go:158,175`、`chain.go:350` | `claimedAgent`(`notAgent` + `viewer` + 領取者 + 租約 + 驗證碼) | 無 |

結論:**這 33 個都有保護**,沒有「完全沒檢查」的資料操作。真正的問題不在「漏檢」,而在**「檢查的強度不一致」**(§4.2)。

### 4.2 實際發現(依風險排序)

| 編號 | 風險 | 位置 | 問題 | 證據 |
|---|---|---|---|---|
| R-1 | **已補驗證(第一階段:共享權杖);第二階段未做** | console MCP 連接埠 | **2026-11-20 決定:所有接進 MCP 的人一律要驗證身分,不再有「沒有登入的 MCP」。第一階段已完成:`/mcp`、`/mcp/worker` 與其餘路徑一律要 `Authorization: Bearer <console 的 MCP 權杖>`(401 / 429 只針對錯誤的嘗試,常數時間比對,沒有 loopback 例外;Host 白名單 localhost、127.0.0.1、[::1],`OMIXOR_MCP_ADDR` 設了非本機位址時再加上它);現有用戶端(omixor-mcp 轉接、`--channel`、`--init-worker`)已改成帶權杖。尚未處理:(a) 通過驗證後仍不經 `adminTools` deny-list、身分是 `localPrincipal`(SA),所有持權杖者共用一個身分、署名是字串 `mcp`;(b) 權杖是共享的,洩漏要靠 `mcp_token_rotate` 換新。第二階段(每人各一把、署名成本機使用者 actor 加來源標記)隨 ISS-36。** 原本的問題:`/mcp`、`/mcp/worker` **沒有任何驗證**,也不經 `adminTools` deny-list;工具包含核心 Admin 工具(`agent_upsert`、`chain_save`、`cabinet_layout_save`、`project_save`…)與 console 的 gcp/facebook/server 等工具,身分是 `localPrincipal`(SA)。預設只聽 127.0.0.1,但 `OMIXOR_MCP_ADDR=0.0.0.0` 是文件化的 Docker 設定;且即使 web 啟用了 LDAP,這個連接埠仍不受其約束。同機任何程序、或網路可達者(Docker 發布設定錯誤時)即可繞過 web 登入與管理員區分 | `console/internal/mcp/server.go:37,58`;`worker_endpoint.go` 註解「和 /mcp 一樣沒有額外驗證」;`console/internal/web/web.go:88-102` 只在 `/api/tool` 套 `adminTools` |
| R-2 | **已修(程式);(c) 人的回報備註署名仍是 `agent:<角色>`,維持未修** | `ReportTask` | **(a)(b)(d) 已修:`ReportTask` 現在要求領取者本人或組織管理員(OA)才能改 `canceled`/`done`、續約別人領取中的任務,測試涵蓋;(c) 備註署名仍固定記成 `agent:<角色>`,人的操作被記成角色,尚未改(待決定要改記實際呼叫者或人與角色並記)。** 原本的問題:組織成員(非管理員)可回報**任何**任務,狀態可設為 `canceled`、`done`、`inProgress`:(a) 繞過 `CancelTask`(要 OA);(b) 繞過領取鎖(`ClaimTask` 說「連原領取者也不能再領」,但任何成員可續約/結案別人領取中的任務);(c) 備註署名固定是 `agent:<角色>`,人的操作被記成角色;(d) 會觸發 `chainReport`。`ReleaseTask` 才有「領取者或 OA」檢查 | `core/service/agents_claim.go:177-246`(`viewer` 後沒有領取者/OA 檢查;`:189` 只有「非角色才多 canceled」);對照 `agents_tasks.go:235`(CancelTask 要 OA)、`agents_claim.go:329`(Release 檢查領取者) |
| R-3 | **已修(中高)** | Issue `assignee` | 任何 viewer(含**角色憑證**與**不在鏈上的一般成員**)建立/修改 Issue 時給 `assignee`,`syncAssignee` 就替**任一啟用中的角色**排任務,完全不檢查組織鏈:`AssignTask`(成員需鏈上有邊)與 `DelegateTask`(角色需鏈上有邊)形同可被 Issue 繞過。角色憑證因此可令其他角色(含不在其下游者)執行任務,並把 Issue 內文放進任務說明,是 prompt-injection 的放大路徑 | `issues.go:152`(CreateIssue)、`:187`(UpdateIssue)只有 `viewer`;`agents_tasks.go:314`(syncAssignee)無權限檢查;對照 `chain.go:266,283` |
| R-4 | **中** | 事件(incidents)入口不一致 | REST 對角色憑證開放事件的讀/宣告/更新(`authedOrAgent` + service `viewer`),但 MCP 工具表把事件工具標成 Admin only。任一任務綁定憑證因此可經 REST 寫事件(執行者記為 `ai:<角色>`)。屬於「表與實作不吻合」,且 REST 路徑沒有 `Needs`/白名單 | `core/api/incident.go:13-39`;`service/incident.go:186,193,201,272`;`model/tools.go` incidents 三項 |
| R-5 | **中** | console deny-list | web 權限是 **deny-list**:`adminActions`(約 44)/`adminTools`(5)以外的 action 一律放行,新增 action 預設無保護;測試只驗證清單內都被擋(`admin_test.go` 迭代 `adminActions`),**沒有測試保證「核心要管理員的操作都在清單內」**。已知清單外的:`jira_import`(本機模式直接寫 store、無稽核)、`facebook_publish`/`facebook_draft_*`/`facebook_managed`、`gcp_refresh`、`integration_set`、`automation_toggle/autonomy`、`org_switch`(全域狀態) | `console/internal/web/admin.go:22-80`;`web/web.go:88,104`;`console/internal/core/jira.go:616`、`issues_remote.go:198` |
| R-6 | **中** | console 身分模型 | (a) web 使用者的 `Admin`(LDAP 群組)與 core 的組織角色無關;(b) 本機模式所有 web 使用者共用 `localPrincipal`(SA),遠端模式共用同一把 server token → 非管理員 web 使用者 `AssignTask` 不受「鏈上要有邊」限制、`ReleaseTask`/「我領取的」失去個人意義、可看/操作該帳號能看到的**所有組織**(沒有「web 使用者 → 組織」的對應);(c) `org_switch` 改的是全域目前組織,會影響同時在線的其他人(`system_save` 帶 `org` 做 409 比對是既有的補丁) | `console/internal/core/issues_remote.go:64`;`web/admin.go:12-20` 的說明;`core/members.go:10` |
| R-7 | **已修(程式)** | 停用角色不作廢憑證 / 領取 | **已修:`AuthenticateAgent` 與 `ClaimTask` 現在都檢查 `Agent.Active`,停用角色的憑證與領取一律拒絕。** 原本的問題:`AuthenticateAgent` 只驗雜湊、角色存在、任務 inProgress,**不檢查 `Agent.Active`**;停用角色後既有憑證仍可用到任務結束或逾時。`ClaimTask`(指定 TaskID 或自動挑選)也不檢查 `Active`,只有 `ClaimNext` 檢查 | `agents_auth.go:89-96`;`agents_claim.go:71-125` vs `ClaimNext` 的 `agents[i].Active` |
| R-8 | **已修(低)** | `ListProjects` / `GetProject` | `viewer` 即可,**不看 docAccess**。目前只靠 MCP 的 `NeedDocRead`(角色沒有任何 docAccess 時 `projects` 不列、呼叫被擋)。若有其他路徑把角色 Principal 接上 Backend,專案名稱與描述會外洩 | `project.go:81-95`;`model/tools.go` projects;`core/mcp/server.go:301` |
| R-9 | **已修(低中)** | `SaveMember` 會建立伺服器使用者 | 組織管理員(非 SA)加入一個不存在的 email,`SaveMember` 直接 `Store.SaveUser` 建立使用者(role=member)。`LoginWithEvoleap` 會讓 Google 驗證 email 相符的人接手這個「待連結使用者」→ **OA 等於可以邀請新人進入整台伺服器**,而 `SaveUser`(新增使用者)要求 SA。可能是刻意設計(邀請流程),但授權邊界與「伺服器使用者由 SA 管理」不一致 | `service.go:526-556`(`:540` 起);`:241`(pending 接手);對照 `:346` |
| R-10 | **低**(待確認) | 成員可刪 Issue / 文件 | `DeleteIssue`、`DeleteDoc` 只要求成員(`orgRole`),無「作者或管理員」限制;MCP 沒有刪除工具,所以只有 REST 與 console 看得到 | `issues.go:385`、`cabinet.go:386` |
| R-11 | **低** | 範本欄位檢查洩漏 | `ValidateDocBody` 的錯誤訊息帶出範本文件 id(`見範本 DOC-n`),即使該範本在角色 docAccess 之外 | `core/model/doctemplates.go:110-139` |
| R-12 | **低**(結構) | docAccess 兩處實作 | docAccess 過濾與寫入檢查在 MCP 工具內做(`g.CanReadDoc`、`g.CanWriteIn`),只有角色端點會走(2026-11-20 起人類端點帶 `agent` 一律 400,不再有「成員身分 + 角色權限」的混合情況);規則仍是 MCP 工具與 service 兩份,日後易偏離 | `core/mcp/tools_cabinet.go`(`docs`、`doc_save` 的 `visible`/`checkWrite`) |
| R-13 | **已修(低)**(ISS-8、ISS-16) | 速率限制可被偽造來源繞過 / 入口共用額度 | **已修:** `ClientIP` 改為由「信任的代理層數」(`OMIXOR_TRUSTED_PROXY_HOPS`;沒設時 Cloud Run 為 1、其他為 0)決定:0 = 完全不看 `X-Forwarded-For`、用連線位址;N = 取 XFF 由右數第 N 個,項目不足或不是合法 IP 退回連線位址(純函式 `ClientIPFrom`,`clientip.go`;項目帶埠號或方括號也能解析)。限流與失敗計數的鍵經 `limitKey`:IPv6 依 /64 前綴限流(換位址繞不過),IPv4 與稽核用的真實位址不變。失敗計數(角色憑證每 IP、LDAP 每帳號)改成有上限的 `failCounter`(`ratelimit.go`),滿了只淘汰失敗次數最少、最舊的項目,不再整張清空,已被限制的來源不會被灌不同來源洗掉。配對(含 OAuth 同意頁)、LDAP 登入、evoleap 登入各自有一組每來源 10、每入口 60(每分鐘)的額度,不再共用一個全域額度,一個入口被灌爆不影響別的。附帶:停用帳號的配對碼在消耗前就回 403(碼保留);`RevokeToken` 的資料庫錯誤回 500 並記 log(找不到 = 已被別人用掉仍回 401,一次性靠它)。**殘留:** 部署在 Cloud Run 前面再加別的代理(例如 HTTPS 負載平衡器)時要把層數設成實際層數;多個實例各自計數(記憶體),沒有跨實例的總額 | `core/service/clientip.go`、`ratelimit.go`、`pair.go`、`agents_auth.go`、`ldap.go` |
| R-14 | **低** | 功能開關在 core 不強制 | 組織關閉 `cabinet`/`channels`/`incidents` 等,core API/MCP 仍可用;只有 `mcpGateway` 與樣板在 core 強制。若期待「關閉 = 不可用」,實際只有畫面隱藏 | `service/system.go:176`;`model/features.go`;`console/internal/mcp/feature_gate.go`(僅 console 範例資料工具) |
| R-15 | **低** | 文件與實作不符 | `core/api/api.go` 檔頭多處寫「(成員;角色憑證也可以)」(projects 等),但這些路由實際用 `authed`,**不接受角色憑證**(角色只能經 `/mcp/agent`);console `mcp/worker_endpoint.go` 註解說「四個工具」,實際 7 個 | `core/api/api.go:1-170` vs `api/cabinet.go`、`api/agents.go` |
| 待確認-1 | — | console UI 對 deny-list 的依賴 | 前端 `canEdit` 旗標與按鈕隱藏是否都來自 server 回傳(`forViewer`),本次未逐一核對 `console/ui` | `web/admin.go` 的 `forViewer` |
| 待確認-2 | — | `/api/issues/*` 用 `OMIXOR_API_TOKEN` 時的組織邊界 | API token 呼叫者可指定任意 `{org}`,且 body `author` 可為 `agent:<id>`/`mcp`;若多組織共用一台 console,token 等於跨組織全權限 | `web/issues_api.go:40-54`;`web.go:255` |
| 待確認-3 | — | store 層資料隔離 | 本次只讀到 service/介面,未檢查 `core/store` 各查詢是否都帶 org 條件(跨組織 id 碰撞時的行為) | `core/store/*` |

### 4.3 掃描範圍的其他結論

- `core/api`:AST 掃描 96 個 `HandleFunc`,**全部**經 `a.Svc.*`(`/api/auth/me` 與 `/api/modules` 不呼叫 service,只需登入);唯一碰 store 的是 `PUT /api/orgs/{org}` 在 `UpdateOrg` 成功後的 `Store.GetOrg`,無風險。
- `core/mcp`、`core/backend`:工具透過 `Backend` 介面;`Local` 以呼叫者 Principal 呼叫 service。繞過 service 直接碰 store 的只有 `Local.ensureOrg`(`EnsureOrg=true` 才會:console 本機模式,補建組織時不經授權檢查也不留稽核)與角色端點取組織/角色名稱(只讀名稱,供署名顯示)。
- `console`:直接碰 store 的有 `ensureLocalOrg`、`issueSave`(Jira 匯入);其餘全走 `AgentOps`/`backend`。

---

## 5. 統一架構建議

### 5.1 現況判斷

**沒有一套統一的授權架構。** 理由:

1. 沒有單一入口:授權判斷分散在 A–O 共 15 組機制,新增操作時沒有強制路徑(工具表、service、console deny-list 三處各自維護)。
2. 「資料表」只存在於 MCP 工具曝光(`CoreTools`),不描述權限;權限描述只存在於各方法的程式碼與註解。
3. console 是另一個信任域(`localPrincipal`/共用 token + deny-list),與 core 的角色模型沒有映射。
4. 沒有「每個對外操作都有授權條目、都有被拒測試」的機制;現有測試是各功能各自寫(例:`TestNonAdminCannotWriteAdminSettings` 只覆蓋 deny-list 內的項目)。

優點(要保留):service 作為權限最終執行處、角色身分預設拒絕(`orgRole`)、憑證只存雜湊且常數時間比對、租約與一次性領取驗證碼、docAccess 模型清楚。

### 5.2 建議方案:單一「操作 → 能力」表 + 單一授權函式

**資料表**(`core/model/authz.go`,純資料,可被 service、api、mcp、console 與測試共同讀取):

```go
type Op struct {
    ID       string   // 例 "task.report"、"incident.declare"(全域唯一,= 稽核的 action 名稱)
    Group    string   // 組織與成員 / Issue / 角色與任務 / 文件櫃 / 專案 / 頻道 / 事件 / 組織鏈 / 設定與整合 / 稽核
    Who      Who      // ServerAdmin | OrgAdmin | Member | AgentSelf(限自己的組織/任務)| Claimer | Authenticated
    Needs    []string // 附加條件:docAccess:read|write、channelMember、chainEdge、featureOn:<key>、leaseHolder
    Surfaces Surfaces // 曝光:REST 路由 / MCP 端點(org、agent、worker)/ console action
    Test     string   // 「未授權被拒」測試的名稱(CI 檢查存在)
}
```

**單一授權函式**(`core/service/authz.go`):

```go
func (s *Service) Authorize(p *model.Principal, op string, res Resource) error
// Resource{Org, Project, DocID, ChannelID, TaskID, AgentID}
// 內部:查表 → 對照 Who → 依 Needs 逐項檢查(沿用現有 orgRole / docViewer / channelActor / memberAssign / claimedAgent 當「條件實作」)
// 失敗統一 403(或 404 以不洩漏存在),並寫一筆 authz.denied 稽核(取樣)
```

- 現有輔助函式不丟掉,改成 `Authorize` 的**內部條件實作**;各 service 方法開頭換成 `s.Authorize(p, "task.report", Resource{Org: org, TaskID: id})`。
- **MCP 的 `Admin`/`Agent`/`Needs` 由同一張表產生**:`CoreTools` 改成 `Op` 的 `Surfaces.MCP` 投影;端點的 `Allow` 變成「表上有曝光 ∧ 該 Principal 對這個 op 的試算授權為真」,`tools/list` 因此只列呼叫者真的能用的工具,而不是只看端點類別;`Admin` 旗標改名(例如 `Endpoint: org|agent|worker`),消除「Admin = 需要管理員」的誤解。
- **console**:`adminActions/adminTools` deny-list 改成 allow-list:console 每個 `RegisterAction`/MCP 工具必須在表裡有 `Who`;預設拒絕。web 使用者另外映射成 core 身分(見階段 3),不再所有人共用 `localPrincipal`。
- **本機單人模式**:`localPrincipal` 保留,但 `Principal` 帶「本機」標記,`Authorize` 直接識別,不需靠 deny-list 區分管理員。

**測試(CI 強制)**

1. `TestEveryOpRegistered`:用 AST 蒐集所有 `api.HandleFunc`、`CoreTools`、console 工具、`RegisterAction`,每一個都要在 `Ops` 找得到對應 `ID`;反過來每個 `Op` 至少有一個曝光。(本文件 §4 的兩支掃描可直接改寫成這個測試。)
2. `TestDeniedMatrix`:對每個 `Op` × 每種 Principal(匿名、非成員、成員、OA、SA、角色憑證綁別的任務、領取者、非領取者)自動產生呼叫,斷言實際結果等於表上的預期(拒絕的必須 403/404,且資料未被改)。這能直接抓到 R-2、R-3、R-4。
3. `TestToolRegistryMatchesService`:對每個 MCP 工具,以最低權限的 Principal 呼叫,不應成功;表上標示可用的,至少一種 Principal 呼叫成功。
4. `TestNoStoreFromHandlers`:`core/api`、`core/mcp`、`console/internal/web` 不得直接呼叫 `Store`(白名單例外要寫在測試裡並附原因)。

### 5.3 分階段(含範圍與風險)

| 階段 | 內容 | 改動範圍 | 風險 | 備註 |
|---|---|---|---|---|
| 0(立即,小改動) | 補 §5.4 的缺口:console MCP 驗證、`ReportTask` 限制、`syncAssignee` 檢查鏈、incidents 入口一致、`AuthenticateAgent`/`ClaimTask` 查 `Active`;補測試 | `service/agents_claim.go`、`agents_tasks.go`、`issues.go`、`incident.go`、`agents_auth.go`;`console/internal/mcp/server.go`、`web/admin.go` | 低~中:行為變更可能影響現有 UI/CLI(例如成員目前能「回報」別人任務) | 每項都先加「未授權被拒」測試再改 |
| 1 | 建立 `model.Ops` 表,先**只當文件與測試**用(不改任何行為);寫 §5.2 的測試 1、4;表由現有 `CoreTools`、`adminActions`、REST 路由反向生成並人工校對 | 新增 `core/model/authz.go`、測試;不動 service | 低 | 這一步就能把「不一致」變成可見的清單 |
| 2 | 實作 `Authorize`,把 service 方法逐檔改為呼叫它(輔助函式變條件實作);加測試 2、3;MCP `Allow` 改讀 `Ops` | `core/service` 全部(約 97 個方法)、`core/mcp/endpoints*.go`、`model/tools.go` | 中:改動面大,但行為由既有測試與新矩陣測試鎖住 | 建議一個 service 檔一個 PR,先改讀取類 |
| 3 | console:deny-list → allow-list;console MCP 加驗證(沿用 web session 或獨立共享密鑰);web 使用者映射到 core 身分(遠端模式:每位 web 使用者各自的 server token,至少把實際操作者寫進稽核);`org_switch` 改 per-session | `console/internal/web`、`console/internal/mcp`、`core.App` 的目前組織狀態 | 中高:`org_switch` per-session 牽涉 `App.CurrentOrgID()` 的全域用法 | 可先只做 allow-list 與 MCP 驗證,per-session 另開票 |
| 4 | 稽核補強:`authz.denied` 取樣記錄、敏感讀取(`ListTokens` 等)記錄;功能開關要不要在 core 強制(R-14)做產品決定 | `service.Audit`、`system.go` | 低 | |

### 5.4 最先該補的缺口(依序)

1. **R-1 console MCP 連接埠**:未驗證、不經 deny-list。最小改法:`OMIXOR_MCP_ADDR` 非 loopback 或 web 已啟用 LDAP 時,要求共享密鑰(或沿用 web session),並對 `/mcp` 套用 `adminTools` 同一份清單;預設單人本機維持現狀。
2. **R-2 `ReportTask`(已修;只剩 (c) 署名未修)**:非角色呼叫者要求「領取者本人 或 OA」才能把狀態改為 `canceled`/`done`、續約別人領取中的任務(對應 `ReleaseTask` 的規則);備註署名改記實際呼叫者(或同時記人與角色)。補測試:成員甲領取、成員乙回報 `canceled` 應 403。
3. **R-3 Issue `assignee`(已修,`checkIssueAssign`)**:`syncAssignee` 前檢查:呼叫者是 OA、或(成員)鏈上有 `user:<自己> → agent:<目標>`、或(角色)鏈上有 `agent:<自己> → agent:<目標>`;不符時回 403。補測試。
4. **R-4 事件入口**:決定「角色是否可寫事件」——要的話把 `incidents/incident_declare/incident_update` 在表上標 Agent(並設 `Needs`);不要的話 service 的 `DeclareIncident/UpdateIncident` 對角色身分拒絕,REST 改用 `authed`。
5. **R-7 停用角色(已修)**:`AuthenticateAgent` 與 `ClaimTask` 加 `Active` 檢查;停用角色時順手作廢其進行中任務的憑證。
6. **名稱與文件**:`CoreTool.Admin` 改名為端點旗標;修正 `api/api.go` 檔頭與 worker 端點註解(R-15)。

---

## 6. 署名規則(ISS-36;DOC-13 D9 與審查後的相容修正)

「誰」欄位(建立者、留言作者、頻道訊息作者、任務備註…)一律存 actor id。輸入端的署名由服務層決定,規則如下。

### 6.1 一般寫入(建立 Issue、留言、文件、任務、事件、頻道訊息…)

| 呼叫者 | 沒給 author / as | 給了別人 | 給了角色或系統 |
|---|---|---|---|
| 一般成員、角色憑證 | 本人 | 靜默忽略,當本人(`AuthorIgnore`);明確的 API 參數用 `AuthorReject` 回 403 | 同左(只能是自己) |
| 伺服器管理員 | 本人 | **只能代角色與系統(D9)**;解析不到或解析到別的使用者:`AuthorIgnore`(舊式、相容)**靜默當本人**,`AuthorReject` 回 400(找不到)/ 403(別的使用者) | 採用(系統 actor 只能用 id:`sys_omixor`、`sys_routine`、`sys_chain`) |

D9 的相容修正:舊用戶端(Mac app 的 `author: app.me`、MCP 的 `author:"mcp"`、舊式 CLI)會把人員字串當 author 傳,所以相容模式下管理員給的名字解析不到或是別的使用者時**不報錯、當本人**,與一般成員一致;**不會**變成別人的署名。頻道的 `as` / `author` 同樣:管理員只能代角色,給使用者(或找不到)就是本人。

### 6.2 匯入(`ImportIssues`、`ImportAgents`、`ImportCabinet`、`ImportIncidents`、`ImportIssuesWith`)

匯入檔的署名是舊字串,解析規則依呼叫者:

- **伺服器管理員**(搬遷歷史資料;含本機 omixor-local、omixor-web import,呼叫者是本機管理員):寬鬆解析器,歷史作者(離職的人、不在名單上的 email、舊的 mcp、本機別名)照舊對到 actor,缺的會建「未註冊使用者」。唯一限制:別的組織的角色 actor id 不能署名(落到 `sys_legacy_*`)。
- **組織管理員**(非伺服器管理員):署名只能是「本組織成員、本組織角色、系統 actor、呼叫者本人」。其他任何字串(別組織的使用者 email、別組織的 actor id、未知字串、非成員的伺服器管理員)一律落到 `sys_legacy_i<雜湊>`(系統 actor,**不是真人**,名稱 = 原值,讀回仍顯示原值);**不會**建立「未註冊使用者」actor、不寫別名。

### 6.3 還原(`omixor issues restore`)

留言整份取代(`IssuePatch.Comments`)對伺服器管理員:既有留言的作者照伺服器上的;**新**留言(被刪掉、要補回的)的作者在一般模式下走 `AuthorReject`(只能是本人、角色、系統,否則 400/403)。CLI `issues restore` 帶 `IssuePatch.Restore = true`(只限伺服器管理員,非管理員 403、留稽核 `issue.restore`):新留言的歷史作者走匯入用的寬鬆解析器,所以離職的人、舊式 tim/mcp 署名的留言可以原樣還原。這是明確、只限伺服器管理員的還原路徑,不是一般 API 的後門。

### 6.4 本機使用者與保留的 handle

- console 本機模式:操作者是本機使用者 `omixor-console`(伺服器管理員)。這一列以固定的 `evoleap_id = local:omixor-console` 辨識(`ensureLocalPrincipal` 只採用這一列;舊版沒有標記、也沒綁外部身分的同名列接手並補標記;email 是 `omixor-console` 卻綁了別的登入身分(例如 LDAP)的列**不採用、不升級**,啟動時報錯)。
- **本機模式下 web 登入的其他人,送到核心的都是本機使用者:稽核與署名只顯示 `omixor-console`**,不是實際登入的人(遠端模式則由各自的 token 決定)。
- 保留的本機 handle(`omixor-console`、`ops-web`、`mcp`、`tim`,以及內建署名 `omixor`、`routine`、`chain`、`system`)不能被一般登入路徑佔用:evoleap、LDAP、新增使用者、加組織成員建立或更新使用者時,email 一律要含 `@` 且不是保留 handle;LDAP 的 `mail` 屬性沒有 `@` 視為沒有 mail(退回「帳號@`OMIXOR_LDAP_EMAIL_DOMAIN`」,仍沒有就不能建立)。

---

## 附錄 A:本次掃描方法(可重現)

- **service 掃描**:解析 `core/service/*.go`(排除測試),列出所有帶 `*model.Principal` 的公開方法(97 個),標記本體是否呼叫 `orgRole / needOrgAdmin / needServerAdmin / viewer / notAgent / ServerAdmin`,再對未直接呼叫者做一層傳遞閉包,最後**人工逐一讀碼驗證**(§4.1)。傳遞閉包因同名方法(例如 `Store.RevokeToken`)會有誤判,表中已註明。
- **api 掃描**:解析 `core/api/*.go` 的 `HandleFunc`,抽出包裝器(`authed`/`authedOrAgent`/無)與函式本體呼叫的 `a.Svc.*`;96 個,0 個沒經 service。
- **未涵蓋**:`core/store` 的 SQL 層資料隔離、`console/ui` 前端行為、`ios/`、`mac/`、`office3d/`、`site/`、`templates/`。

# 設計:讓 repo 的 commits 讀入 omixor,並與 Issue 連結(ISS-69)

狀態:草案,等決定(見最後「待決定」)。日期:2026-11-24。

## 1. 要解決什麼

- Issue 與程式碼沒有結構化連結(使用心得 DOC-15 第 5 條):哪張 Issue 對應哪個 commit、哪個版本發現、哪個版本修好,都靠手寫留言,會過時。
- 已有的相關需求:ISS-53(Issue 標記「發現的 git hash」foundIn)、ISS-42(目標連結 commit)、ISS-43(任務與 Issue 同步)。這些應該是同一個機制的不同面向,不要各做一套。

## 2. 現況與限制(設計必須尊重)

1. **core 不存 GitHub / GitLab 的 token**(integrations 只存「追蹤哪些 repo」;讀 PR、Actions 由 Mac App 與 web 用各自裝置上的 token 直接打)。所以「伺服器自己去抓 commits」不能是第一步。
2. repo 目前只是字串:`Issue.repo`(owner/name,可帶 `github:`、`gitlab:` 前綴)、專案與 repo 的連結(`ProjectRepo`)、組織整合設定的 repo 清單。已有正規化函式(`model.CleanRepo`、`model.RepoKey`)。
3. 資料庫結構異動要走有版本、冪等、向後相容的步驟,並同步 `schema_manifest.json`。
4. MCP 授權:OAuth 寫入白名單要縮小而不是放大(ISS-60);新寫入工具先只給 omx_ 使用者 token。
5. 受限專案的 Issue 對非成員不可見,連結不能洩漏它的存在。

## 3. 設計原則

1. **資料進來的方式與資料長什麼樣分開**:來源(CLI、CI、Mac App、webhook、agent)都送進同一個標準化入口,產出同一種 `Commit` 與 `Link`。換來源不改資料模型。
2. **不存機密**:phase 1 與 phase 2(webhook 除外)都不需要 core 持有任何 provider token。
3. **連結是帶來源與種類的事實,不是狀態變更**:連結只記錄「誰、何時、依什麼規則」建立;不自動改 Issue 狀態。要自動關閉之類的行為,是之後獨立的、可關閉的規則,而且要看 commit 是否進了預設分支。
4. **冪等、可重送、可回放**:同一批 commits 重送不產生重複;以後可以用 git log 全量回放重建。
5. **可信度要有標記**:任何成員都能回報 commit,所以回報來的資料標「未驗證」;webhook 簽章驗證或 provider API 查證後才標「已驗證」。
6. **先小後大**:只存 metadata(sha、訊息、作者、時間、分支),不存 diff、不存檔案內容。

## 4. 資料模型

```
repo key        正規化字串(model.CommitRepoKey):小寫,保留 github: / gitlab: 前綴,例如 github:tim72117/omixor;沒前綴是自己的 key
                phase 1 不新增 repos 表,repo key 就是識別;phase 2 若要存 webhook 密鑰與預設分支,再加 repos 表(key 當主鍵)

commits         (org, repo, sha) 唯一
                subject, body(截斷)、author_name、authored_at、committed_at
                branch(回報時所在分支,可空)、parents(0..n 個父 sha,用來判斷 merge、之後做歷史)
                url(可空;github 風格 repo 可推導)
                source        cli | ci | app | webhook | agent
                verified      false(回報)| true(簽章驗證 / provider 查證)
                reported_by(actor)、reported_at
                
issue_commits   (org, issue, repo, sha) 唯一
                kind          mentions | fixes | found_in | manual
                origin        auto(訊息解析)| manual(人或工具建立)
                linked_by(actor;auto 時為系統)、linked_at
                note(可空,manual 時的說明)

repo_sync       (org, repo) 一列:last_sha、last_at、last_source —— 讓 CLI / App 做「只送新的」增量同步
```

說明:

- `kind` 的對應:`found_in` 就是 ISS-53 的「發現的 git hash」,`fixes` 對應「修好的 commit」;ISS-42 的「目標連結 commit」用 `manual` 加 `note`。**不另開 foundIn 欄位**,避免兩套資料。
- `parents` 是為了之後能回答「這張 Issue 的修正有沒有進 main」(從預設分支往回走);phase 1 只存、不用。
- 只用 `sha` 當身分:rebase、squash 會改 sha,舊連結會指向已不在歷史上的 commit。phase 1 接受這點,並保留 `repo_sync` 與 `verified` 讓之後能標「已重寫」;不做 patch-id 比對。

## 5. 流程

### 5.1 進來:`ReportCommits`(標準入口)

```
來源(CLI / CI / App / agent / 之後的 webhook)
        │  commits[] + repo + source
        ▼
 驗證與整理  ── sha 格式、repo 正規化、欄位截斷、去控制字元、機密遮罩(同 report_problem)
        ▼
 upsert commits  ── 冪等;重送不覆蓋既有的 verified=true
        ▼
 解析引用  ── 只比對「這個組織真的存在、呼叫者看得到」的 Issue key(見 5.2)
        ▼
 寫 issue_commits(auto)  ── 已存在就略過
        ▼
 更新 repo_sync  ── last_sha / last_at
        ▼
 回傳 {reported, linked[], unmatched[]}
```

批次上限 100;一次呼叫只屬於一個 repo、一個組織。

### 5.2 引用解析(最容易出錯的地方)

- **不要用「像 key 的字串就是 key」**:`UTF-8`、`SHA-256`、`ISO-8601`、`CVE-2024-1234` 都符合 `[A-Z]+-\d+`。做法:先取出候選字串,**只保留該組織實際存在的 Issue key**(含專案代號,例如 `APP-12`),其餘全部當作沒提到。
- 動詞:`fixes / fixed / closes / closed / resolves / resolved <key>` → `fixes`;其餘提到 → `mentions`。
- 一個 commit 可連多張 Issue;一張 Issue 可連多個 commit。
- 受限專案:呼叫者看不到的 Issue,**不建立連結、也不出現在 `unmatched`**(否則洩漏存在)。
- 大小寫不敏感,輸出統一成 Issue 的顯示 key。

### 5.3 出去:查詢與顯示

- `issues(id)`:加 `commits`(最近 10 筆摘要)與 `commits_total`。
- `commits`(讀):依 issue、repo、sha 前綴、關鍵字查。
- 連結可見度 = Issue 可見度;commit 本身組織成員都看得到(repo 本來就是組織追蹤的)。

## 6. 介面(分層,每層只依賴下一層)

```
CLI / Mac App / console / CI / agent / MCP 工具
            │
   HTTP API  (core/api;權限表 authz_table.go 登記)
            │
   Backend   (Local = 直接 service;Remote = 打 HTTP API)
            │
   service/commits.go   ← 規則都在這裡:驗證、遮罩、解析、權限
            │
   store(PG)            ← 只有存取,沒有規則
```

MCP 工具(英文說明):`commits_report`(寫)、`commits`(讀)、`issue_commit_link` / `issue_commit_unlink`(寫)。寫入工具只給 omx_ token,**不進 OAuth 寫入白名單**;`commits` 進唯讀清單。

來源端(誰負責「讀入」):

| 來源 | 怎麼讀入 | 需要 token? | 階段 |
|---|---|---|---|
| CLI `omixor commits sync` | 讀本機 `git log`(從 `repo_sync.last_sha` 之後) | 只需要 omixor 登入,不需要 GitHub token;私有 repo 也行 | phase 1 |
| CI(GitHub Actions / GitLab CI) | 一個步驟呼叫 CLI 或 API | omixor token(CI secret) | phase 1(文件範例) |
| Mac App / console | 已有 GitHub/GitLab 裝置 token,按鈕「同步 commits」 | 用既有裝置 token,不新增儲存 | phase 1.5 |
| webhook(push 事件) | GitHub/GitLab 主動送 | 需要每個 repo 的 webhook 簽章密鑰 | phase 2 |

## 7. 安全與隱私

- 回報者可以說謊:標 `verified=false`;顯示時註明。webhook 簽章驗證通過、或用 provider API 查證過才標 true。
- commit 訊息可能含機密:存之前跑 `maskSecrets`(`report_problem` 已有的規則,抽成共用函式)。
- 欄位上限:subject 300、body 4000、author 200、branch 200;一次最多 100 筆。
- 權限:組織成員可回報與查詢;手動連結 / 取消 = 比照 `issue_update`;角色(agent)憑證 phase 1 不給寫入。
- 頻率:每個使用者(acting user id,不分 IP)每小時:`commits_report` 600 筆且 120 次;`issue_commit_link` 與 `issue_commit_unlink` 合計 300 次;超過回 429(MCP 轉成英文並附重試時間)。記憶體內、每個實例各自計算、固定一小時視窗;常數在 `model/commit.go`(`CommitReport*`、`CommitLinkMaxPerHour`)。
- 連結上限:每張 Issue 最多 2000 條連結,其中裸參照(commit 還沒有人回報內容)最多 200 條;超過手動連結回 400(「請先用 commits_report 回報」),自動連結到頂的 Issue 不再新增(已有的照常升級)。常數 `CommitMaxLinksPerIssue`、`CommitMaxBareRefsPerIssue`。
- 稽核:回報與手動連結寫 audit(actor、repo、筆數),不記 commit 內容。

## 8. 演進路線

1. **phase 1**:資料表、`ReportCommits`、引用解析、`issues(id)` 顯示、MCP 工具、CLI `commits sync`、文件(`docs/commits.md`)。**phase 1 不補舊歷史(no backfill,2026-11-24 決定)**:`commits sync` 第一次只送目前 HEAD 當基準點,之後只送新的 commit;同步位置對不上(改寫歷史、不同 clone)時也只送 HEAD 重設基準點,一次最多 500 筆;沒有 backfill 旗標。
2. **phase 1.5**:Mac App / console 的「同步 commits」與 Issue 畫面顯示。
3. **phase 2**:webhook(push、PR 合併、tag),repos 表存簽章密鑰與預設分支;`verified`;「fixes 且已進預設分支 → 提示關閉(不自動關閉)」。
4. **之後**:PR / MR、CI 結果與 commit 的關聯;Issue 與 Issue 的連結(另開,不併進這個機制);rebase 後連結改指向新 sha。

## 9. 考慮過、沒採用的做法

- **伺服器用存起來的 token 主動抓 commits**:需要 core 持有 GitHub/GitLab token,違反現有「核心不收機密」的原則;私有與自架 GitLab 的憑證、CA、網路可達性也都得由伺服器負責。留到有明確需求再做。
- **Issue 上直接加 `commits[]` 欄位**:無法查「某 commit 連到哪些 Issue」、無法記來源與種類、一張 Issue 的 commits 會隨時間讓 Issue 本體越來越肥。
- **ISS-53 另做 `foundIn` 欄位**:和 `found_in` 連結重複。
- **訊息一出現 `XXX-123` 就當 Issue key**:誤判太多(UTF-8、SHA-256)。
- **fixes 立即自動關閉 Issue**:commit 還在功能分支、之後被 revert、或 key 打錯,都會誤關。

## 10. 待決定(2026-11-24 已決定,ISS-69 phase 1 依此實作)

1. **同步來源的順序**:已決定 — phase 1 只做 API + MCP + service + store;CLI `omixor commits sync` 是之後獨立的工作(CLI 要用的 `GET /commits/sync` 同步位置已經有);Mac App / console 放 phase 1.5。
2. **repo 識別**:已決定(2026-11-24 審查後修訂)— 直接用正規化的 repo key 字串,**不建 `repos` 表**。`model.CommitRepoKey` = `CleanRepo` 檢查字元後**轉小寫、保留 `github:` / `gitlab:` 前綴**(不再像 `RepoKey` 那樣去掉):`github:acme/app` 與 `gitlab:acme/app` 是**兩個不同的 repo**,沒給前綴的 `acme/app` 是第三個(泛用命名空間)。`commits_report` 照給的存。查詢、同步位置與手動連結/取消連結(`resolveCommitRepo`):有前綴 = 精確;沒前綴 = 先用已存在的沒前綴 key,否則這個組織裡路徑相同的唯一一個 repo(任何平台),對到多個回 400 並列出、要求加前綴,一個都沒有就用沒前綴的 key(新的參照)。GitHub 網址用不含前綴的路徑推導。`Issue.repo` 的 `RepoKey` 行為不變(仍去前綴)。phase 2 要存 webhook 密鑰與預設分支時再加 repos 表。
3. **Issue 上的摘要欄位**:已決定 — **不加**(Issue 沒有 `foundIn` 欄位,`found_in` 是連結種類);`issues(id)` 以 `commits`(最近 10 筆)與 `commits_total` 帶出,列表與篩選不帶。
4. **角色(agent)能不能回報**:已決定 — **不能**。角色憑證在 service 層一律 403(讀寫都是),`commits` 讀工具也不開在角色端點與執行者端點,角色端點的 `issues(id)` 不附 commits。
5. **受限專案**:已決定 — commit 本身**不隱藏**(整個組織成員都看得到),**只隱藏連結**:看不到的 Issue 不連結、不列在 `unmatched`,依該 Issue 查、列連結、手動連結一律 404。
6. **取代 ISS-53 與 ISS-42**:已決定 — 兩者併入這個機制:ISS-53 的「發現的 git hash」= 連結種類 `found_in`、「修好的 commit」= `fixes`;ISS-42 的「目標連結 commit」= `manual` 加 `note`。兩張 Issue 結案、不另做欄位。

### 實作時另外做的決定(phase 1)

- **三張表**(結構步驟 2 `commits`,`store/schema_commits.go`):`omixor_commits`(主鍵 org, repo, sha)、`omixor_issue_commits`(主鍵 org, issue_id, repo, sha;issue_id 內部 id,外鍵 cascade 到 `omixor_issues`、`(org, repo, sha)` 外鍵到 commits)、`omixor_repo_sync`(主鍵 org, repo);都有 org 外鍵 cascade。`reported_by`、`linked_by` 存 actor id 字串,**不設外鍵**(不擋 actor 的退役)。
- **一對 (issue, commit) 只有一條連結**:自動連結不蓋手動的,只把 auto 的 `mentions` 升成 `fixes`(不降級);手動連結覆蓋種類與說明。取消自動連結後,之後同一個 sha 再被回報,自動連結會重新建立。
- **重送同一個 sha**:只補空欄位(裸參照被真正回報補齊,`created` 不算新建),`verified` 只升不降;連結仍會依這次送來的訊息重新解析。
- **`unmatched`** 只列「代號是 ISS 或呼叫者看得到的專案代號、但組織裡沒有這張 Issue」的字串(可能打錯);其他像 key 的字串(UTF-8、SHA-256、CVE-2024-1234)一律忽略,不連結也不列。`linked` 只列這次呼叫新建或升級的連結。
- **fixes 動詞**除了 fixes / fixed / closes / closed / resolves / resolved,也接受 fix / close / resolve(GitHub 也這樣);`bugfix ISS-1` 不算。
- **`repo_sync`**:`last_sha` = 這批裡最新的(authored_at → committed_at → 回報時間);補更舊的歷史時 `last_sha` 不倒退;`last_at` 是這次回報的時間。
- **遮罩**用 `core/redact`(從 report_problem 抽出,`report_problem` 的行為不變);commit 文字用 `SecretsKeepGitIDs`:獨立的 40 / 64 位十六進位(`This reverts commit <sha>`)不遮,其餘規則完全一樣。
- **權限**:回報、查詢、同步位置、列連結 = 組織成員(人);手動連結與取消 = 同 `issue_update`(Issue 要看得到)。
- **CLI `commits sync` 不補舊歷史**(2026-11-24 決定,phase 1):沒有同步位置只送 HEAD 一筆當基準點;有同步位置送 `<位置>..HEAD`(每批 ≤100、一次 ≤500);位置不在本機或不是祖先 → 只送 HEAD 並警告。用法見 `docs/commits.md`。

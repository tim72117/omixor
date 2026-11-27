// Package store 是核心的存放介面。正式用 PostgreSQL(pg.go),測試也用 PostgreSQL(core/storetest)。
// 這一層只存取資料,不判斷權限(權限在 service)。
package store

import (
	"errors"
	"time"

	"omixor-core/model"
)

var ErrNotFound = errors.New("not found")

// ErrOAuthFull OAuth 動態註冊的用戶端數量已達上限
var ErrOAuthFull = errors.New("oauth clients full")

// ErrStale 條件式更新時資料的狀態已經不是呼叫者讀到的(別人先改了)
var ErrStale = errors.New("stale")

// ErrVersionConflict 樂觀鎖的版本不符(實際回傳 *VersionConflictError,帶目前版本)
var ErrVersionConflict = errors.New("version conflict")

// VersionConflictError 存檔時帶的版本和目前的不一樣
type VersionConflictError struct{ Current int64 }

func (e *VersionConflictError) Error() string   { return "version conflict" }
func (e *VersionConflictError) Is(t error) bool { return t == ErrVersionConflict }

// ErrLocked 領取任務時,任務已被別人鎖住(進行中且 lockedUntil 還沒到)
var ErrLocked = errors.New("locked")

// MergeCounts MergeIssues 的結果:新增、整筆取代、因為 id 已經有而略過的筆數
type MergeCounts struct {
	Inserted int
	Updated  int
	Skipped  int
}

// OAuthStore OAuth(Claude 自訂 connector 的授權;獨立的兩張表,不碰 omixor_tokens;規則在 service/oauth.go,方法在 pg_oauth.go)。
// 獨立成子介面:Store 內嵌它;另一個宿主(例如本機 console)要把 OAuth 資料放在別處時,只需要實作這一組(見 service.Service.OAuth)
type OAuthStore interface {
	// CreateOAuthClient 新增動態註冊的用戶端;已經有 max 個用戶端回 ErrOAuthFull(同一個交易內檢查,並行註冊也不會超過)
	CreateOAuthClient(c model.OAuthClient, max int) error
	OAuthClientByID(id string) (*model.OAuthClient, error) // 沒有回 ErrNotFound
	// PutOAuthToken 新增一列 code / access / refresh / csrf(雜湊已算好在 t.Hash)
	PutOAuthToken(t model.OAuthToken) error
	// OAuthTokenByHash 依種類與雜湊找(不管用過、撤銷、過期,由呼叫者判斷;沒有回 ErrNotFound)
	OAuthTokenByHash(kind string, hash []byte) (*model.OAuthToken, error)
	// ExchangeOAuthToken 同一個交易內:把 useID 標成用過(條件式:還沒用過、沒撤銷、沒過期,否則回 ErrStale,什麼都不寫),再新增 add(可以是空的)。
	// 授權碼換 token、refresh 輪替、CSRF 驗證都用它,所以「用過一次」是原子的
	ExchangeOAuthToken(useID string, at time.Time, add []model.OAuthToken) error
	// RevokeOAuthFamily 撤銷同一個家族(family_id 相同)還沒撤銷的全部列(code、access、refresh)
	RevokeOAuthFamily(family string, at time.Time) error
	// RevokeOAuthUser 撤銷某位使用者還沒撤銷的全部列(含未完成的授權碼與確認交易),回傳幾列
	RevokeOAuthUser(userID string, at time.Time) (int, error)
	// PurgeOAuthTokens 刪掉過期超過 before 的 token 列(清理用),回傳刪了幾列
	PurgeOAuthTokens(before time.Time) (int, error)
}

type Store interface {
	OAuthStore
	// 使用者、token、操作紀錄
	CountUsers() (int, error)
	ListUsers() ([]model.User, error)
	UserByID(id string) (*model.User, error)
	UserByEvoleapID(id string) (*model.User, error)
	UserByEmail(email string) (*model.User, error)
	// SaveUser 新增或更新使用者(以 u.ID 為鍵),同一個交易內取得或建立這位使用者的 actor(u.ActorID 空 = 自動:先看已存的 actor_id,
	// 再依 email 小寫接回既有的「未註冊使用者」actor,都沒有才建 usr_ actor),並把 actor 的 name、handle 同步成目前的值(ISS-36 B-2b)。
	// u.ActorID 有給必須是存在的使用者 actor,否則回 ErrBadRef。停用(Disabled)不影響 actor
	SaveUser(u model.User) error
	// UserByActorID 依使用者 actor id 找使用者;不是使用者 actor(角色、系統、未註冊使用者)或不存在回 ErrNotFound
	UserByActorID(actorID string) (*model.User, error)
	// EnsureUserActor 取得或補建既有使用者的 actor(使用者列還沒有 actor_id 時);已經有就直接回傳;使用者不存在回 ErrNotFound
	EnsureUserActor(userID string) (*model.ActorRecord, error)
	// EnsureUserActorByEmail 依 email 取得使用者 actor,沒有就建「未註冊使用者」actor(handle = email 小寫,沒有 omixor_users 列);
	// 之後那個 email 的使用者 SaveUser 時接回同一個。email 不能當 handle 回一般錯誤
	EnsureUserActorByEmail(email, name string) (*model.ActorRecord, error)
	CreateToken(t model.Token) error
	TokenByHash(h []byte, now time.Time) (*model.Token, *model.User, error) // 沒撤銷、沒過期的
	TouchToken(id string, at time.Time) error
	RevokeToken(id string, at time.Time) error
	// RevokeTokensByDevicePrefix 撤銷裝置名稱以 prefix 開頭、還沒撤銷的全部 token(web session 用),回傳撤銷了幾個
	RevokeTokensByDevicePrefix(prefix string, at time.Time) (int, error)
	ListTokens(now time.Time) ([]model.Token, error)
	AddAudit(e model.AuditEntry) error
	ListAudit(limit int) ([]model.AuditEntry, error)

	// actor:使用者、角色、系統內建署名的共同身分(omixor_actors;ISS-36,規格見雲端 DOC-13;方法在 pg_actor.go)。
	// 啟動遷移固定寫入系統 actor(model.SystemActors)。這一批的 actor 與使用者、角色、成員、token 還沒有連動(後續工作包接)。
	// id 比對精確;handle 先規範化(model.CleanActorHandle)。唯一性:使用者 handle 全伺服器唯一(不分大小寫)、角色現役 (org, handle) 唯一、
	// 系統名稱唯一;衝突回 ErrDuplicateKey,owner 不存在回 ErrBadRef,系統 actor 不能改回 ErrSystemActor
	// CreateActor 新增(id 空 = 依種類產生,system 的 id 要自己給),回傳存好的
	CreateActor(a model.ActorRecord) (model.ActorRecord, error)
	ActorByID(id string) (*model.ActorRecord, error) // 含退役的;沒有回 ErrNotFound
	// ActorsByIDs 批次取:id → actor,不存在的不在結果裡
	ActorsByIDs(ids []string) (map[string]model.ActorRecord, error)
	// ActorByHandle 找現役的:user 用 email(org 給 ""),agent 用 (org, 角色 id)(退役的不算),system 用名稱(org 給 "");找不到回 ErrNotFound
	ActorByHandle(kind model.ActorKind, org, handle string) (*model.ActorRecord, error)
	ListActors(f ActorFilter) ([]model.ActorRecord, error) // 依建立時間;預設只列現役
	// SetActorDisabled 停用(退役)或啟用;停用時 disabled_at = at(已停用的不變);角色重新啟用遇到同名現役角色回 ErrDuplicateKey
	SetActorDisabled(id string, disabled bool, at time.Time) error
	// UpdateActorProfile 改顯示名稱、頭像(nil = 不改);UpdateActorHandle 改 handle(使用者換 email、角色改名)
	UpdateActorProfile(id string, name, avatar *string) error
	UpdateActorHandle(id, handle string) error
	// SetActorOwner 設定(空字串 = 清除)角色 actor 的負責人(使用者 actor id);只有角色有 owner
	SetActorOwner(id, owner string) error
	// SaveAgentOwned 同 SaveAgent,新增角色時指定 owner(使用者 actor id;空 = 組織預設);已存在的角色不改 owner。
	// SaveAgent / SaveAgentOwned / DeleteAgent / ImportAgents 都會在同一個交易裡建立、同步、退役角色的 actor(ISS-36 B-2c,見 pg_actor_agents.go);
	// 刪角色 = actor 退役(列保留),同 id 重建是新的 actor;Agent.ActorID 讀出時帶 actor id
	SaveAgentOwned(org string, a model.Agent, owner string) error
	// BackfillAgentOwners 為還沒有 owner 的角色 actor 補預設 owner(組織第一位管理員成員 → 最早的伺服器管理員),回傳補了幾個
	BackfillAgentOwners() (int, error)

	// ResolveLegacy 寬鬆解析器:把一個舊的「誰」字串(email、user:<email>、agent:<id>、mcp、任意字串…)對到 actor id,缺的 actor 與別名會建立;
	// 空字串回 ""(NULL)。冪等:同一個字串永遠同一個 actor(過渡橋與匯入用,見 pg_actor_legacy.go、omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》)
	ResolveLegacy(org, raw string) (string, error)
	// UserInfoByActorIDs 使用者 actor id → 使用者列的 id 與 email(沒有使用者列的不在結果裡);service/present.go 輸出相容字串用
	UserInfoByActorIDs(ids []string) (map[string]UserInfo, error)

	// 組織與成員
	ListOrgs() ([]model.Org, error)
	GetOrg(id string) (*model.Org, error)
	CreateOrg(o model.Org) error
	// UpdateOrg 更新名稱、圖示、顏色、備註(不存在回 ErrNotFound)
	UpdateOrg(o model.Org) error
	// DeleteOrg 連成員與 Issue 一起刪掉(不能復原)
	DeleteOrg(id string) error
	MemberRole(org, userID string) (string, error) // 不是成員回傳 ErrNotFound
	ListMembers(org string) ([]model.Member, error)
	SaveMember(m model.Member) error
	RemoveMember(org, userID string) error
	OrgsOfUser(userID string) ([]model.Org, error) // MyRole 是成員角色

	// Issue(一個組織一份清單,新的在前)。收 Issue 編號的方法都接受內部 id(UUID)、目前或舊的 key(不分大小寫,見 pg_issue.go)。
	// 垃圾桶裡的(軟刪除)ListIssues、GetIssue 與寫入都當作不存在(ErrNotFound)
	ListIssues(org string) ([]model.Issue, error)
	// ListDeletedIssues 垃圾桶裡的(新的在前);ListIssuesWithDeleted 全部(匯出用)
	ListDeletedIssues(org string) ([]model.Issue, error)
	ListIssuesWithDeleted(org string) ([]model.Issue, error)
	// CountProjectIssues 專案裡的 Issue 張數(含垃圾桶)
	CountProjectIssues(org, project string) (int, error)
	// ProjectKeyAliasTaken 別名表裡有沒有以 <prefix>- 開頭的別名(不分大小寫);有就回其中一個,沒有回空字串。
	// 建立專案、設定代號前檢查:別名(搬走的 Issue 的舊 key、舊格式 id)不能被新專案的號碼覆蓋
	ProjectKeyAliasTaken(org, prefix string) (string, error)
	GetIssue(org, id string) (*model.Issue, error)
	// ResolveIssueID 編號 → 內部 id(含垃圾桶裡的;找不到 ErrNotFound)
	ResolveIssueID(org, ref string) (string, error)
	// CreateIssue 發新的 id(UUIDv7)與 key(i.Project 空 = ISS-<n>,否則 <專案代號>-<n>;專案不存在 ErrBadRef、
	// 沒有代號 ErrProjectNoKey),放在最前面,回傳存好的(version 1)
	CreateIssue(org string, i model.Issue) (model.Issue, error)
	// UpdateIssue 整筆取代本體(位置、key、project 不變;version +1)。i.Version > 0 時只有目前版本等於它才改,
	// 否則回 *VersionConflictError(Current 是目前版本);0 = 不檢查。i.Comments 是 nil 時留言不動(一般修改都這樣用,
	// 才不會蓋掉同時新增的留言);不是 nil 時留言整份取代(匯入、維運用)
	UpdateIssue(org string, i model.Issue) error
	// MoveIssue 搬到 project(空 = 組織層級):在目的範圍發新的 key(舊的留在別名表;搬回組織層級也發新的 ISS-<n>),version +1。
	// 已經在那裡就不改。expected > 0 時版本不符回 *VersionConflictError。回傳搬好的與原本的 key
	MoveIssue(org, id, project string, expected int, at model.Time) (*model.Issue, string, error)
	// TrashIssue 丟進垃圾桶(軟刪除;version +1),RestoreIssue 從垃圾桶拿回來;狀態不對(已刪 / 沒刪)回 ErrNotFound
	TrashIssue(org, id, by string, at model.Time) (*model.Issue, error)
	RestoreIssue(org, id string, at model.Time) (*model.Issue, error)
	// AddIssueComment 只附加一則留言(放最後)並把 Issue 的 updatedAt 設成 c.At,回傳附加後的 Issue;
	// id 不分大小寫,Issue 不存在回 ErrNotFound。不會動到其他留言,同時多則留言也不會互相蓋掉
	AddIssueComment(org, issueID string, c model.Comment) (*model.Issue, error)
	// DeleteIssue 永久刪除(含垃圾桶裡的;留言、別名一起刪,頻道的 issue_id 清成 NULL)。一般的刪除用 TrashIssue
	DeleteIssue(org, id string) error
	// ImportIssues 第一次搬資料用:保留 id、作者與時間;list 是新的在前
	ImportIssues(org string, list []model.Issue) (int, error)
	// MergeIssues 可以重跑的匯入(一次完成):id(不分大小寫)已經有的,overwrite 時整筆取代(位置不變、留言整份取代),
	// 否則略過;沒有的保留 id、作者與時間,依 list 的順序排在既有的後面(list 是新的在前)。list 裡的 id 不能重複
	MergeIssues(org string, list []model.Issue, overwrite bool) (MergeCounts, error)
	// 附件(Issue 內文與留言裡的圖片):一個組織一份,id 是內容雜湊;同 id 再存不覆蓋
	PutAttachment(org, id string, data []byte) error
	GetAttachment(org, id string) ([]byte, error) // 沒有回 ErrNotFound
	// IssueOrgs 有 Issue 的組織 id(啟動時補建組織用)
	IssueOrgs() ([]string, error)

	// 虛擬角色、任務與憑證(依組織分;DeleteOrg 一併刪除)。id 比對精確(大小寫要一致),不分大小寫的查找在 service。
	ListAgents(org string) ([]model.Agent, error)  // 依建立時間
	GetAgent(org, id string) (*model.Agent, error) // 沒有回 ErrNotFound
	SaveAgent(org string, a model.Agent) error     // 新增或整筆取代
	DeleteAgent(org, id string) error
	ListAgentTasks(org string) ([]model.AgentTask, error) // 新的在前
	GetAgentTask(org, id string) (*model.AgentTask, error)
	// CreateAgentTask 指定新的 id(T-<最大號碼+1>),回傳存好的
	CreateAgentTask(org string, t model.AgentTask) (model.AgentTask, error)
	UpdateAgentTask(org string, t model.AgentTask) error // 整筆取代
	// ClaimAgentTask 領取用的條件式整筆取代:任務目前是進行中且 lockedUntil 在 now 之後(被鎖住)就不改,回傳 ErrLocked
	ClaimAgentTask(org string, t model.AgentTask, now time.Time) error
	// ExtendAgentCredential 把任務憑證的到期時間延到 expires(沒有憑證就什麼都不做)
	ExtendAgentCredential(org, taskID string, expires time.Time) error
	// SaveAgentCredential 存憑證;同一個 (org, taskId) 舊的先刪(同任務只有一組有效憑證)
	SaveAgentCredential(c model.AgentCredential) error
	AgentCredentialByHash(h []byte) (*model.AgentCredential, error)
	// AgentCredentialForTask 任務目前的憑證(沒有回 ErrNotFound)
	AgentCredentialForTask(org, taskID string) (*model.AgentCredential, error)
	// DeleteAgentCredentials 刪任務的憑證(taskID 不空)或某角色的全部憑證(agentID 不空)
	DeleteAgentCredentials(org, taskID, agentID string) error
	// ImportAgents 第一次搬資料用:角色與任務整批寫入,保留 id、時間與備註;組織已經有角色或任務就回錯誤
	ImportAgents(org string, agents []model.Agent, tasks []model.AgentTask) error
	// PruneAgentCredentials 清掉到期的憑證
	PruneAgentCredentials(now time.Time) error

	// 專案(依組織分;DeleteOrg 一併刪除;見 model/project.go)。id 比對精確,不分大小寫的查找在 service。方法在 *_project.go
	ListProjects(org string) ([]model.Project, error) // 依建立順序(號碼小的在前)
	GetProject(org, id string) (*model.Project, error)
	// CreateProject 指定新的 id(PRJ-<最大號碼+1>),回傳存好的
	CreateProject(org string, p model.Project) (model.Project, error)
	// UpdateProject 改名稱、代號、描述與 updatedAt(id、createdAt 不變;不存在回 ErrNotFound;代號變了但專案已經有 Issue 回 ErrInUse;
	// 代號重複回 ErrDuplicateKey)
	UpdateProject(org string, p model.Project) error
	// DeleteProject 刪專案與它的文件櫃結構(不存在回 ErrNotFound)。文件不動:專案裡還有沒有文件由 service 先檢查
	DeleteProject(org, id string) error
	// ListProjectRepos 組織全部的專案 repo 連結(依專案號碼、連結時間排序);專案刪除時連結跟著刪(外鍵 cascade)。方法在 pg_project_repo.go
	ListProjectRepos(org string) ([]model.ProjectRepo, error)
	// LinkProjectRepo 連結專案與 repo(repo 以 model.RepoKey 比對,不分大小寫、github:/gitlab: 前綴可省);已經連過回 created=false(不改);專案不存在回 ErrBadRef
	LinkProjectRepo(org, project, repo string, at time.Time) (created bool, err error)
	// UnlinkProjectRepo 取消連結;原本沒連回 removed=false
	UnlinkProjectRepo(org, project, repo string) (removed bool, err error)
	// 專案成員(第 3 階段;表 omixor_project_members,方法在 pg_project_members.go)。成員以 actor id 存;受限專案只有成員與管理員看得到,可見性判斷在 service
	// ListProjectMembers 專案(空字串 = 整個組織)的成員(依專案號碼、加入時間)
	ListProjectMembers(org, project string) ([]model.ProjectMember, error)
	// ProjectMemberships actor 在組織裡每個專案的角色(專案 id → admin/member)
	ProjectMemberships(org, actorID string) (map[string]string, error)
	// SetProjectMember 加入成員或改角色;回傳有沒有變動;專案或 actor 不存在回 ErrBadRef
	SetProjectMember(org, project, actorID, role string, at time.Time) (changed bool, err error)
	// RemoveProjectMember 移除成員;原本不是成員回 removed=false
	RemoveProjectMember(org, project, actorID string) (removed bool, err error)
	// RemoveProjectMembershipsOf 把 actor 從組織所有專案拿掉(使用者離開組織、角色被刪),回傳拿掉的專案 id
	RemoveProjectMembershipsOf(org, actorID string) ([]string, error)

	// 文件櫃(依組織分;DeleteOrg 一併刪除)。id 比對精確,不分大小寫的查找在 service。
	// 結構以 (org, project) 為鍵,project 空字串 = 未分專案(原本的組織文件櫃)。
	// GetCabinetLayout 這個專案存過的結構與版本號(樂觀鎖用;每存一次 +1);沒存過回 ErrNotFound(版本視為 0)
	GetCabinetLayout(org, project string) ([]model.CabinetSection, int64, error)
	// SaveCabinetLayout 整份取代結構,回傳新版本號。expected 非 nil 時只有目前版本等於它才存(沒存過的版本是 0),
	// 否則回 *VersionConflictError(errors.Is(err, ErrVersionConflict));nil = 不鎖(無條件覆蓋,版本照樣 +1)
	SaveCabinetLayout(org, project string, layout []model.CabinetSection, expected *int64) (int64, error)
	// 文件存在組織底下(DOC-<n> 在組織內唯一),CabinetDoc.Project 是所屬專案;ListDocs 回全部專案的(過濾在 service)
	ListDocs(org string) ([]model.CabinetDoc, error) // 新的在前
	GetDoc(org, id string) (*model.CabinetDoc, error)
	// CreateDoc 指定新的 id(DOC-<最大號碼+1>)並放在最前面,回傳存好的
	CreateDoc(org string, d model.CabinetDoc) (model.CabinetDoc, error)
	UpdateDoc(org string, d model.CabinetDoc) error // 整筆取代(含 Project;位置不變)
	DeleteDoc(org, id string) error
	// ImportDocs 第一次搬資料用:保留 id、作者與時間;list 是新的在前;組織已經有文件就回錯誤
	ImportDocs(org string, list []model.CabinetDoc) (int, error)

	// 組織鏈(依組織分;DeleteOrg 一併刪除)。GetOrgChain 沒存過回 ErrNotFound
	GetOrgChain(org string) (*model.OrgChain, error)
	SaveOrgChain(org string, c model.OrgChain) error // 整份取代

	// 訊息頻道(依組織分;DeleteOrg 一併刪除)。id 比對精確,不分大小寫的查找在 service。
	ListChannels(org string) ([]model.Channel, error) // 依建立順序
	GetChannel(org, id string) (*model.Channel, error)
	// CreateChannel 指定新的 id(CH-<最大號碼+1>),回傳存好的
	CreateChannel(org string, c model.Channel) (model.Channel, error)
	UpdateChannel(org string, c model.Channel) error // 整筆取代(名稱、主題、成員、更新時間)
	// CreateChannelMessage 指定新的 id(M-<這個頻道最大號碼+1>),回傳存好的
	CreateChannelMessage(org string, m model.ChannelMessage) (model.ChannelMessage, error)
	// ListChannelMessages 依號碼篩選(見 model.MessageQuery),由舊到新;hasMore = 範圍內還有沒回傳的
	ListChannelMessages(org, channelID string, q model.MessageQuery) (list []model.ChannelMessage, hasMore bool, err error)
	// LastChannelMessages 每個頻道最後一則訊息(channel id → 訊息;沒有訊息的頻道不在裡面)
	LastChannelMessages(org string) (map[string]model.ChannelMessage, error)
	// ChannelReads 這個成員在各頻道的已讀(channel id → 已讀)
	ChannelReads(org, member string) (map[string]model.ChannelRead, error)
	// SaveChannelRead 新增或取代(不檢查是否倒退,service 決定)
	SaveChannelRead(org string, r model.ChannelRead) error
	// UnreadCounts 這個成員在這些頻道的未讀數:號碼大於已讀而且作者不是他(channel id → 數量;0 的不一定在裡面)
	UnreadCounts(org, member string, channelIDs []string) (map[string]int, error)
	// UnreadMentions 這些頻道裡提到這個成員、還沒讀的訊息(作者不是他),新的在前最多 limit 則
	UnreadMentions(org, member string, channelIDs []string, limit int) ([]model.ChannelMessage, error)

	// 事件(依組織分;DeleteOrg 一併刪除)。id 比對精確,不分大小寫的查找在 service。
	ListIncidents(org string) ([]model.Incident, error) // 新的在前
	GetIncident(org, id string) (*model.Incident, error)
	// CreateIncident 指定新的 id(INC-<max(最大號碼, model.IncidentNumberBase)+1>)並放在最前面,回傳存好的
	CreateIncident(org string, i model.Incident) (model.Incident, error)
	UpdateIncident(org string, i model.Incident) error // 整筆取代(位置不變)
	// ImportIncidents 第一次搬資料用:保留 id、時間與時間軸;list 是新的在前;組織已經有事件就回錯誤
	ImportIncidents(org string, list []model.Incident) (int, error)

	// 待決定 / 需要協助(依組織分;DeleteOrg 一併刪除;見 model/decision.go;方法在 pg_decision.go)。id 比對精確(DEC-<n>),不分大小寫的查找在 service。
	// CreatedBy、DecidedBy 是 actor id;SourceIssue 是 Issue 內部 id(Issue 永久刪除時清成空)
	ListDecisions(org string) ([]model.Decision, error) // 依編號(舊的在前);沒有回空切片
	GetDecision(org, id string) (*model.Decision, error)
	// CreateDecision 發新的 id(omixor_seq 的 decision scope:DEC-<n>),回傳存好的;actor 或來源 Issue 不存在回 ErrBadRef
	CreateDecision(org string, d model.Decision) (model.Decision, error)
	// UpdateDecision 條件式更新可變欄位(標題、內文、選項、狀態、回答、證據、來源 Issue、blocks、處理人與時間;編號、kind、建立者不變):
	// 目前的狀態要是 fromStatus 才改(不是就回 ErrStale,同時處理只有一個成功);不存在回 ErrNotFound
	UpdateDecision(org string, d model.Decision, fromStatus string) error

	// Issue 子項(依組織分;隨 Issue 永久刪除 cascade、DeleteOrg 一併刪除;見 model/issue_item.go;方法在 pg_issue_items.go)。
	// issueID 一律是 Issue 的內部 id;Assignee、ClaimedBy、CreatedBy 是 actor id(空 = NULL);Key 不存(service 填)。
	// ListIssueItems 依 seq(舊的在前);沒有回空切片。ListIssueItemsByOrg 批次:一次查多張 Issue,回 issueID → 子項(沒有子項的 Issue 不在 map 裡);
	// issueIDs 是 nil 或空 = 整個組織全部。
	ListIssueItems(org, issueID string) ([]model.IssueItem, error)
	ListIssueItemsByOrg(org string, issueIDs []string) (map[string][]model.IssueItem, error)
	GetIssueItem(org, id string) (*model.IssueItem, error)
	// CreateIssueItem 在交易內以 omixor_seq 的 item:<issueID> scope 配 Seq(連號、刪掉的號碼不重用、失敗的建立不佔號),ID 沒給就發 UUIDv7,
	// CreatedAt/UpdatedAt 沒給用現在;回傳存好的。Issue 不存在、actor 不存在、DependsOn 不是同一 Issue 的子項 → ErrBadRef;
	// 同一 Issue 的子項數已達 model.MaxItemsPerIssue → ErrBadRef。呼叫端先用 it.Clean() 驗證輸入
	CreateIssueItem(org string, it *model.IssueItem) (*model.IssueItem, error)
	// UpdateIssueItem 條件式更新可變欄位(標題、狀態、指派、依賴、files、驗收、證據、卡住原因與等待、領取者與租約、任務、升級 Issue、完成時間、updated_at;
	// Seq、IssueID、建立者、建立時間不變):目前的狀態要是 fromStatus 才改(不是回 ErrStale,同時處理只有一個成功);不存在回 ErrNotFound;
	// DependsOn 不是同一 Issue 的子項、或會成環(含依賴自己)→ ErrBadRef(訊息含「成環」)
	UpdateIssueItem(org string, it *model.IssueItem, fromStatus string) error
	// ClaimIssueItem 領取:狀態是 todo,或 doing 但租約過期(locked_until 空或 <= 現在)、或租約是自己的(續租)→ 改 doing、claimed_by = actor、
	// locked_until = until,回傳更新後的子項;已被別人持有有效租約、或 blocked / done / canceled → ErrLocked(blocked 等狀態不是領取的對象);不存在 ErrNotFound
	ClaimIssueItem(org, id, actor string, until time.Time) (*model.IssueItem, error)
	// ReleaseIssueItem 釋放領取:doing → todo、清領取者與租約。actor 非空時只有持有者能放(不是持有者 ErrLocked);actor 空 = 不檢查(管理員 / 系統);
	// 狀態不是 doing 回 ErrStale;不存在 ErrNotFound
	ReleaseIssueItem(org, id, actor string) error
	// DeleteIssueItem 刪除(別的子項對它的依賴清成空);不存在 ErrNotFound。序號不重用
	DeleteIssueItem(org, id string) error

	// commits 與 Issue 的連結(ISS-69;依組織分,DeleteOrg 一併刪除;見 model/commit.go;方法在 pg_commit.go)。
	// repo 一律是 model.CommitRepoKey 正規化後的字串、sha 是 40 位小寫十六進位(呼叫端 service 先驗證與整理)。
	// UpsertCommits 冪等地寫入一批 commit(一個交易):新的插入;已存在的只補空欄位(裸參照被真正的回報補齊),verified 只升不降;
	// 回傳這批裡「新插入」的筆數
	UpsertCommits(org string, list []model.Commit) (created int, err error)
	// EnsureCommitRef 確保 (repo, sha) 有一列(手動連結一個沒回報過的 sha):沒有就插入 subject 空字串、source manual 的裸參照,已存在不動
	EnsureCommitRef(org string, c model.Commit) error
	GetCommit(org, repo, sha string) (*model.Commit, error)
	// QueryCommits 依條件查(新的在前:authored_at,沒有就 reported_at),total 是符合條件的總數(不受 Limit 影響)
	QueryCommits(org string, q CommitQuery) ([]model.Commit, int, error)
	// LinkIssueCommit 建立 Issue 與 commit 的連結(commit 要已存在,否則 ErrBadRef;Issue 不存在 ErrBadRef)。冪等:
	// 手動(origin manual)覆蓋已有連結的種類、說明與署名;自動(auto)不蓋手動的,只把 auto 的 mentions 升成 fixes。回傳連結有沒有變動
	LinkIssueCommit(org string, l model.IssueCommit) (changed bool, err error)
	// UnlinkIssueCommit 移除連結;沒有這個連結回 ErrNotFound
	UnlinkIssueCommit(org, issueID, repo, sha string) error
	// ListIssueCommits 一張 Issue 的連結(連結新的在前,最多 limit 筆;limit <= 0 = 全部),total 是連結總數。Issue 欄位是內部 id
	ListIssueCommits(org, issueID string, limit int) ([]model.IssueCommitView, int, error)
	// ListCommitLinks 一批 commit(同一個 repo)被哪些 Issue 連結(sha → 連結;沒有連結的不在 map 裡)
	ListCommitLinks(org, repo string, shas []string) (map[string][]model.IssueCommit, error)
	// FindCommitRepos 這個組織裡已存的、路徑(去掉 github:/gitlab: 前綴後)等於 path 的 repo key(最多三個:沒前綴、github:、gitlab:;已排序)。path 要是小寫、無前綴
	FindCommitRepos(org, path string) ([]string, error)
	// LookupReportedCommits 批次查「回報過內容」的 commit(裸參照不算)。repos 空 = 整個組織;prefixes 是小寫十六進位的 sha 前綴。
	// found 只含有對到 commit 的前綴;hasData = 範圍內有任何回報過的 commit
	LookupReportedCommits(org string, repos, prefixes []string) (found map[string]bool, hasData bool, err error)
	// IssueCommitStats 一張 Issue 目前的連結數(Total)、其中裸參照數(Bare:commit 的 subject 是空字串)、以及 (repo, sha) 這條連結是否已存在(Linked)
	IssueCommitStats(org, issueID, repo, sha string) (IssueCommitStats, error)
	GetRepoSync(org, repo string) (*model.RepoSync, error) // 沒有回 ErrNotFound
	PutRepoSync(org string, s model.RepoSync) error        // 整列取代

	// 組織設定文件(通用):選用模組(core/module)存自己的組織層設定用,一個組織每種 kind 一份,例如 GitHub、GitLab 模組的
	// kind = github、gitlab。依組織分;DeleteOrg 一併刪除。Get 沒存過回 ErrNotFound;Save 整份取代。
	// kind 是保留字(mcp,外部 MCP 登記用專屬方法)或空白時回錯誤。方法在 *_integration.go(見 OrgDoc)
	GetOrgDoc(org, kind string) (*OrgDoc, error)
	SaveOrgDoc(org, kind string, d OrgDoc) error

	// 組織的外部 MCP 登記(伺服器、工具規則、角色權限;依組織分;DeleteOrg 一併刪除;沒有任何密鑰,見 model/mcp.go)。
	// Get 沒存過回 ErrNotFound;Save 整份取代。方法在 *_mcp.go
	GetMCPConfig(org string) (*model.OrgMCPConfig, error)
	SaveMCPConfig(org string, c model.OrgMCPConfig) error

	// 組織的系統設定(功能開關;依組織分;DeleteOrg 一併刪除;見 model/features.go)。
	// Get 沒存過回 ErrNotFound;Save 整份取代(Saved 不用存,讀回時一律是 true)。方法在 *_system.go
	GetSystemSettings(org string) (*model.OrgSystemSettings, error)
	SaveSystemSettings(org string, s model.OrgSystemSettings) error

	// 系統層的功能設定(整台伺服器一份;見 model/features.go 的 ServerFeatures)。
	// Get 沒存過回 ErrNotFound;Save 整份取代(Saved 不用存,讀回時一律是 true)。方法在 *_system.go
	GetServerFeatures() (*model.ServerFeatures, error)
	SaveServerFeatures(s model.ServerFeatures) error

	// 原子區塊:fn 用 tx 做的寫入全有或全無(PG 是一個資料庫交易;可以巢狀)。fn 裡只能用 tx
	Atomic(fn func(tx Store) error) error
	// TransferAgentTasks 把一個角色名下的任務轉給另一個角色(同一個原子區塊;規則見 transfer.go)。
	// 刪角色前要先轉移:omixor_agent_tasks.agent_id 對角色是 restrict,名下還有任務(任何狀態)時 DeleteAgent 回 ErrInUse
	TransferAgentTasks(org string, o AgentTransfer) (AgentTransferResult, error)
}

// IssueCommitStats IssueCommitStats 的結果
type IssueCommitStats struct {
	Total, Bare int
	Linked      bool
}

// CommitQuery QueryCommits 的條件(零值 = 不篩)
type CommitQuery struct {
	Repo      string // 正規化的 repo key
	SHAPrefix string // 小寫十六進位前綴(service 要求至少 7 碼)
	Query     string // 空白分隔的每個字都要出現在 subject 或 body(不分大小寫)
	Issue     string // Issue 內部 id:只要連結到這張 Issue 的 commit
	Limit     int    // 預設 20、最多 model.CommitQueryMaxLimit
}

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// Issue 子項(issue_item,ISS-37;資料模型見 model/issue_item.go、存取見 store 的 *IssueItem 方法)。規則:
//
//	可見性:完全繼承所屬 Issue——Issue 在受限專案,只有專案成員、組織管理員與伺服器管理員看得到(角色憑證要是專案成員);
//	  看不到的 Issue 底下的子項,單筆與不存在一樣 404、列表是 404(Issue 本身看不到)。垃圾桶裡的 Issue 當作不存在。判斷點:project_access.go
//	看、列(ListItems、GetItem)、新增(AddItem):組織成員;角色憑證也可以(子代理遇到跨階段的工作就提子項)。
//	  角色憑證新增時,負責人只能留空或是自己(不能把工作指派給人或別的角色)
//	改欄位(UpdateItem)、取消(CancelItem)、提升為 Issue(PromoteItem)、解除卡住(UnblockItem):只有人(組織成員);角色憑證 403
//	領取(ClaimItem / StartItem):只有人;負責人是角色時產生並綁定一筆 agent_task(沿用 ClaimTask 的憑證與租約,links = [Issue id, 子項 id]),
//	  回傳憑證給執行者;負責人是人、沒有負責人時只做輕量領取(claimed_by + 租約,不發憑證)。未完成前置(非 done)409
//	放棄領取(ReleaseItem):領取者本人或組織管理員
//	回報(ReportItem、BlockItem):人——沒被別人持有有效租約,或是領取者本人、組織管理員;角色憑證只能回報自己綁定任務的那個子項
//	  (狀態限 doing、blocked、done)。done 需前置 done;blocked 要附原因,等待對象可記 user_decision / item / external
//	刪除(DeleteItem):建立者或組織管理員;角色憑證 403
//	署名:建立者、負責人、領取者一律 actor(SelfActor、ResolveWho);人類端點不接受冒名;輸出帶相容字串加 xxxActor 物件(批次 LoadActors)
//	輸出:Key = Issue 顯示編號 + "." + 序號;dependsOn、waitFor(種類 item)輸出成被指的子項 key,輸入時 key、序號、id 都接受
//	稽核:issue_item.add / update / claim / release / report / block / unblock / cancel / promote / delete
//	鎖順序:agents.mu → issueLock(同 reassignIssue);子項的寫入一律先拿 agents.mu(領取要建任務、完成要回寫任務)

// ItemInput 新增子項
type ItemInput struct {
	Title      string   `json:"title"`
	Files      []string `json:"files,omitempty"`
	Acceptance string   `json:"acceptance,omitempty"`
	// Assignee 負責人(使用者或角色:email、@handle、agent:<handle>、actor id);空 = 沒指派
	Assignee string `json:"assignee,omitempty"`
	// DependsOn 前置子項(同一張 Issue 的 key、序號或 id)
	DependsOn string `json:"dependsOn,omitempty"`
}

// ItemPatch 修改子項(nil = 不改;Assignee、DependsOn 給空字串 = 清掉)
type ItemPatch struct {
	Title      *string   `json:"title,omitempty"`
	Files      *[]string `json:"files,omitempty"`
	Acceptance *string   `json:"acceptance,omitempty"`
	Assignee   *string   `json:"assignee,omitempty"`
	DependsOn  *string   `json:"dependsOn,omitempty"`
}

// ItemListQuery 列表條件。預設只回未完成(todo / doing / blocked);IncludeDone 連 done、canceled 一起;Status 指定單一狀態(蓋過預設)
type ItemListQuery struct {
	IncludeDone bool   `json:"includeDone,omitempty"`
	Status      string `json:"status,omitempty"`
}

// ItemClaimInput 領取
type ItemClaimInput struct {
	// Note 領取備註(例如子代理 / session 說明);記在稽核,不存庫
	Note string `json:"note,omitempty"`
}

// ItemClaimResult 領取結果:負責人是角色時帶 Task 與一次性 Credential
type ItemClaimResult struct {
	Item       *model.IssueItem  `json:"item"`
	Task       *model.AgentTask  `json:"task,omitempty"`
	Credential *model.AgentGrant `json:"credential,omitempty"`
}

// ItemReport 回報。Status:todo(放回待領取 / 解除卡住)、doing(續約;未領取時等於領取)、blocked、done
type ItemReport struct {
	Status string `json:"status"`
	// Evidence 完成證據:JSON 物件({commit, tests, unverified[]…}),或一般字串(自動包成 {"summary": …})
	Evidence json.RawMessage `json:"evidence,omitempty"`
	// Note 備註;done 時併進 evidence.note,並在 Issue 留一則系統留言
	Note        string `json:"note,omitempty"`
	BlockReason string `json:"blockReason,omitempty"`
	WaitForKind string `json:"waitForKind,omitempty"`
	WaitFor     string `json:"waitFor,omitempty"`
}

// ItemPromoteInput 提升為 Issue
type ItemPromoteInput struct {
	Priority string   `json:"priority,omitempty"` // 預設同來源 Issue
	Labels   []string `json:"labels,omitempty"`
	// Project 新 Issue 的專案;nil = 同來源 Issue,空字串 = 組織層級
	Project *string `json:"project,omitempty"`
}

// ItemPromoteResult 提升的結果
type ItemPromoteResult struct {
	Issue *model.Issue     `json:"issue"`
	Item  *model.IssueItem `json:"item"`
}

func itemNotFound(ref string) error { return fail(404, "找不到子項 %s", strings.TrimSpace(ref)) }

// SplitItemKey "ISS-37.2" → ("ISS-37", "2");沒有 "." 回 ("", ref)。給只有子項編號的入口(MCP、CLI)用
func SplitItemKey(ref string) (issue, item string) {
	ref = strings.TrimSpace(ref)
	if k := strings.LastIndex(ref, "."); k > 0 {
		return ref[:k], ref[k+1:]
	}
	return "", ref
}

// itemCtx 一次操作用到的:所屬 Issue(store 的原樣)、可見性、這張 Issue 的全部子項、目標子項(找單筆時才有)
type itemCtx struct {
	issue *model.Issue
	list  []model.IssueItem
	it    *model.IssueItem
}

// itemIssue 找所屬 Issue 並確認呼叫者看得到(角色憑證、人都一樣);看不到、不存在、在垃圾桶一律同一句 404。先 viewer(外部人、外部角色 403)
func (s *Service) itemIssue(p *model.Principal, org, issueRef string) (*itemCtx, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	id, err := s.resolveIssueID(org, issueRef)
	if err != nil {
		return nil, err
	}
	i, err := s.Store.GetIssue(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, issueNotFound(issueRef)
	}
	if err != nil {
		return nil, err
	}
	a, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	if err := a.issueErr(i, issueRef); err != nil {
		return nil, err
	}
	list, err := s.Store.ListIssueItems(org, i.ID)
	if err != nil {
		return nil, err
	}
	return &itemCtx{issue: i, list: list}, nil
}

// itemOf 同 itemIssue,再找目標子項(ref:key、序號、# 序號或 id)
func (s *Service) itemOf(p *model.Principal, org, issueRef, itemRef string) (*itemCtx, error) {
	c, err := s.itemIssue(p, org, issueRef)
	if err != nil {
		return nil, err
	}
	it := findItemIn(c.list, itemRef)
	if it == nil {
		return nil, itemNotFound(itemRef)
	}
	c.it = it
	return c, nil
}

// findItemIn 在同一張 Issue 的子項裡找:id(不分大小寫)、序號(可帶 # 或 <Issue key>. 前綴)
func findItemIn(list []model.IssueItem, ref string) *model.IssueItem {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	for k := range list {
		if strings.EqualFold(list[k].ID, ref) {
			return &list[k]
		}
	}
	_, num := SplitItemKey(ref)
	if n, err := strconv.Atoi(strings.TrimPrefix(num, "#")); err == nil && n > 0 {
		for k := range list {
			if list[k].Seq == n {
				return &list[k]
			}
		}
	}
	return nil
}

// itemRef 輸入的前置 / 等待子項 → 同 Issue 的子項 id;找不到 400
func itemDepID(list []model.IssueItem, ref string) (string, error) {
	it := findItemIn(list, ref)
	if it == nil {
		return "", fail(400, "找不到同一張 Issue 的子項 %s", strings.TrimSpace(ref))
	}
	return it.ID, nil
}

// itemStoreErr store 對子項的錯誤 → 使用者看得到的
func itemStoreErr(err error, key string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return itemNotFound(key)
	case errors.Is(err, store.ErrStale):
		return fail(409, "子項 %s 剛剛被別人改了,請重新讀取", key)
	case errors.Is(err, store.ErrLocked):
		return fail(409, "子項 %s 已被別人領取、處理中", key)
	case errors.Is(err, store.ErrBadRef):
		if strings.Contains(err.Error(), "成環") {
			return fail(400, "依賴不能成環")
		}
		return fail(400, "子項的參照不合法(前置要是同一張 Issue 的子項、數量上限 %d):%v", model.MaxItemsPerIssue, err)
	}
	return err
}

func itemKey(issue *model.Issue, it model.IssueItem) string {
	return fmt.Sprintf("%s.%d", issue.Display(), it.Seq)
}

// presentItems store 的子項 → 輸出:Key、actor 相容字串與物件(整批一次 LoadActors)、dependsOn / waitFor(item)換成子項 key。
// all 是同一張 Issue 的全部子項(換 key 用),pick 是要輸出的。不改傳入的資料
func (s *Service) presentItems(org string, issue *model.Issue, all, pick []model.IssueItem) ([]model.IssueItem, error) {
	var ids []string
	for _, it := range pick {
		ids = append(ids, it.Assignee, it.ClaimedBy, it.CreatedBy)
	}
	n, err := s.LoadActors(model.ActorIDs(ids...)...)
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	for _, it := range all {
		keys[it.ID] = itemKey(issue, it)
	}
	out := make([]model.IssueItem, len(pick))
	for k, it := range pick {
		it = it.Clone()
		it.Key = keys[it.ID]
		if it.Key == "" {
			it.Key = itemKey(issue, it)
		}
		it.AssigneeActor, it.ClaimedByActor, it.CreatedByActor = n.View(it.Assignee), n.View(it.ClaimedBy), n.View(it.CreatedBy)
		it.Assignee, it.ClaimedBy, it.CreatedBy = legacyOrRaw(n, it.Assignee), legacyOrRaw(n, it.ClaimedBy), legacyOrRaw(n, it.CreatedBy)
		if it.DependsOn != "" && keys[it.DependsOn] != "" {
			it.DependsOn = keys[it.DependsOn]
		}
		if it.WaitForKind == model.WaitItem && keys[it.WaitFor] != "" {
			it.WaitFor = keys[it.WaitFor]
		}
		if it.PromotedIssueID != "" { // 內部 id → 新 Issue 的顯示編號(查不到保留原值)
			if pi, err := s.Store.GetIssue(org, it.PromotedIssueID); err == nil {
				it.PromotedIssueID = pi.Display()
			}
		}
		out[k] = it
	}
	return out, nil
}

// agents_auth.go 角色憑證驗證(含失敗次數限制)與執行者代角色回報、留言(/mcp/worker)

package service

import (
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// ================= 憑證驗證 =================

var errAgentAuth = &Error{Code: 401, Msg: "角色憑證無效或已失效"}

func (s *Service) agentBlocked(ip string) bool {
	st := &s.agents
	st.failMu.Lock()
	defer st.failMu.Unlock()
	f := st.fails[ip]
	return f != nil && s.now().Before(f.reset) && f.n > 10
}

// agentFailed 記一次失敗,回傳這一分鐘內第幾次
func (s *Service) agentFailed(ip string) int {
	st := &s.agents
	st.failMu.Lock()
	defer st.failMu.Unlock()
	now := s.now()
	if st.fails == nil || len(st.fails) > 1000 {
		st.fails = map[string]*agentFail{}
	}
	f := st.fails[ip]
	if f == nil || !now.Before(f.reset) {
		f = &agentFail{reset: now.Add(time.Minute)}
		st.fails[ip] = f
	}
	f.n++
	return f.n
}

// AuthenticateAgent 驗證角色憑證(agentId + secret),通過回傳角色身分的 Principal(Agent 欄位有組織、角色、綁定任務)。
//   - 失敗一律回同一個 401(不洩漏角色或憑證存不存在、是逾時還是任務結束);同一來源 ip 一分鐘內失敗超過 10 次回 429
//   - 逾時、任務不在進行中(done、blocked、canceled、重新排隊)、角色被刪都會失敗
//   - 雜湊與角色 id 都用 constant-time 比對
func (s *Service) AuthenticateAgent(agentID, secret, ip string) (*model.Principal, error) {
	if s.agentBlocked(ip) {
		return nil, fail(429, "驗證失敗次數過多,請稍後再試")
	}
	deny := func() (*model.Principal, error) {
		if n := s.agentFailed(ip); n <= 3 { // 避免被灌爆操作紀錄:每分鐘每來源只記前 3 筆(不記角色 id 與密鑰)
			s.Audit(nil, "agent.auth.denied", "", map[string]any{"ip": ip})
		}
		return nil, errAgentAuth
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || secret == "" || len(secret) > 256 || len(agentID) > 100 {
		return deny()
	}
	now := s.now()
	s.agents.failMu.Lock()
	prune := now.Sub(s.agents.lastPrun) > time.Minute
	if prune {
		s.agents.lastPrun = now
	}
	s.agents.failMu.Unlock()
	if prune {
		_ = s.Store.PruneAgentCredentials(now)
	}
	h := HashToken(secret)
	cr, err := s.Store.AgentCredentialByHash(h)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, ErrAuthInternal
	}
	if cr == nil {
		return deny()
	}
	ok := subtle.ConstantTimeCompare(cr.Hash, h) & subtle.ConstantTimeCompare([]byte(cr.AgentID), []byte(agentID))
	if ok != 1 || !now.Before(cr.ExpiresAt.Time) {
		return deny()
	}
	t, err := s.Store.GetAgentTask(cr.Org, cr.TaskID)
	if err != nil || t.Status != "inProgress" || t.AgentID != cr.AgentID {
		return deny()
	}
	g, err := s.Store.GetAgent(cr.Org, cr.AgentID)
	if err != nil || !g.Active { // 角色停用之後,它手上已簽出的憑證也立刻失效(不是等任務結束或憑證逾時)
		return deny()
	}
	actor, err := s.agentActorRef(g)
	if err != nil {
		return nil, ErrAuthInternal
	}
	name := "agent:" + g.ID // 暫時保留偽裝的 User(C-2 換掉署名之後移除)
	return &model.Principal{
		User:  model.User{ID: name, Email: name, Name: g.Name, Role: "member"},
		Agent: &model.AgentIdentity{Org: cr.Org, AgentID: g.ID, TaskID: cr.TaskID},
		Actor: actor,
		Via:   model.ViaAgentCredential,
	}, nil
}

// ================= 執行者代角色操作(/mcp/worker) =================

// claimedAgent 呼叫者(人,不是角色憑證)是這筆任務的領取者、任務進行中且租約還沒到期時,
// 回傳和角色憑證相同的角色身分(AuthenticateAgent 的結果)。讓執行者不經手密鑰也能以角色身分回報、留言。
func (s *Service) claimedAgent(p *model.Principal, org, id, code string) (*model.Principal, *model.AgentTask, error) {
	if err := notAgent(p); err != nil {
		return nil, nil, err
	}
	if err := s.viewer(p, org); err != nil {
		return nil, nil, err
	}
	s.agents.mu.Lock()
	t, err := s.findTask(org, id)
	s.agents.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	if t.Status != "inProgress" {
		return nil, nil, fail(409, "%s 目前是%s,不是進行中", t.ID, model.TaskStatusLabel(t.Status))
	}
	who, err := s.callerActorID(p, org)
	if err != nil {
		return nil, nil, err
	}
	if who == "" || t.ClaimedBy != who { // 比 actor id(email 大小寫不同仍是同一個人)
		return nil, nil, fail(403, "%s 是 %s 領取的,只有領取者本人可以用角色身分回報或留言", t.ID, s.claimedByLabel(*t))
	}
	if t.LockedUntil == nil || !s.now().Before(t.LockedUntil.Time) {
		return nil, nil, fail(409, "%s 的租約已到期,請重新領取", t.ID)
	}
	g, err := s.Store.GetAgent(org, t.AgentID)
	if err != nil {
		return nil, nil, fail(404, "找不到角色 %s", t.AgentID)
	}
	if !g.Active { // 同 AuthenticateAgent:停用的角色,執行者也不能再用它的身分回報或留言
		return nil, nil, fail(409, "角色 %s 已停用,不能再以它的身分回報或留言", g.Name)
	}
	// 領取驗證碼:確認執行者對的是這次領取的任務與角色(避免同時領了好幾筆時選錯角色)
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil, fail(400, "要給領取驗證碼 code(dispatch_claim 回傳的 credential.code)")
	}
	cr, err := s.Store.AgentCredentialForTask(org, t.ID)
	if err != nil {
		return nil, nil, fail(409, "%s 沒有有效的領取,請重新領取", t.ID)
	}
	if subtle.ConstantTimeCompare([]byte(ClaimCode(cr.Hash)), []byte(code)) != 1 {
		return nil, nil, fail(403, "領取驗證碼不符:這不是 %s(%s)這次領取的驗證碼,請確認任務與角色", t.ID, g.Name)
	}
	actor, err := s.agentActorRef(g)
	if err != nil {
		return nil, nil, err
	}
	name := model.AgentPrefix + g.ID // 暫時保留偽裝的 User(C-2 換掉署名之後移除)
	return &model.Principal{
		User:  model.User{ID: name, Email: name, Name: g.Name, Role: "member"},
		Agent: &model.AgentIdentity{Org: org, AgentID: g.ID, TaskID: t.ID},
		Actor: actor,
		Via:   model.ViaAgentCredential,
	}, t, nil
}

// WorkerReportInput 執行者回報的內容:領取驗證碼 + 回報(同 TaskReport)
type WorkerReportInput struct {
	Code string `json:"code"`
	TaskReport
}

// WorkerReport 領取者以角色身分回報自己領取的任務(規則同角色憑證的 ReportTask:狀態只能 inProgress、blocked、done,
// inProgress 會續約,done、blocked 後憑證作廢)
func (s *Service) WorkerReport(p *model.Principal, org, id, code string, in TaskReport) (*model.AgentTask, error) {
	ap, t, err := s.claimedAgent(p, org, id, code)
	if err != nil {
		return nil, err
	}
	u, err := s.ReportTask(ap, org, id, in)
	if err == nil { // 角色身分的操作紀錄只有 agent:<id>;另記一筆實際呼叫的人
		st := ""
		if in.Status != nil {
			st = *in.Status
		}
		s.Audit(p, "agent.task.worker.report", org, map[string]any{"task": t.ID, "agent": t.AgentID, "status": st})
	}
	return u, err
}

// WorkerComment 領取者以角色身分在 Issue 留言(署名 agent:<角色 id>);只能留在任務 links 裡的 Issue
func (s *Service) WorkerComment(p *model.Principal, org, taskID, code, issueID, text string) (*model.Issue, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fail(400, "留言不能是空的")
	}
	ap, t, err := s.claimedAgent(p, org, taskID, code)
	if err != nil {
		return nil, err
	}
	cur, err := s.GetIssue(ap, org, issueID)
	if err != nil {
		return nil, err
	}
	if !s.linksIssue(org, t.Links, *cur) {
		links := "沒有相關 Issue"
		if len(t.Links) > 0 {
			links = "只有 " + strings.Join(t.Links, "、")
		}
		return nil, fail(403, "%s 的相關 Issue %s,不能在 %s 留言", t.ID, links, cur.Display())
	}
	// 只附加這一則(作者是 agent:<角色 id>),不會蓋掉同時進來的留言
	u, err := s.AddIssueComment(ap, org, cur.ID, CommentInput{Text: text})
	if err == nil {
		s.Audit(p, "agent.task.worker.comment", org, map[string]any{"task": t.ID, "agent": t.AgentID, "issue": cur.ID})
	}
	return u, err
}

package service

import (
	"errors"
	"fmt"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 待決定(decision)與需要協助(help)(資料模型見 model/decision.go)。規則:
//
//	看、提出(Add)、列:組織成員(伺服器管理員是全部組織);角色憑證也可以(子代理遇到要人選擇就提 decision、要人做事就提 help)
//	處理(Answer)、重新開啟(Reopen)、由它開 Issue(ToIssue):只有人——組織成員皆可處理(目前沒有指派對象);角色憑證一律 403
//	  已經處理的不能再改(409);要改請 Reopen(組織管理員或原處理人;留稽核與來源 Issue 的系統留言)
//	撤回(Withdraw):open 才能撤回;建立者(角色憑證只能撤回自己提的)或組織管理員
//	由它開出問題 Issue(ToIssue):建一張新 Issue(source 欄記 DEC-n,專案可指定、沒指定依 repo 的連結規則同 CreateIssue),
//	  並把 sourceIssue 指向它;已經有來源 Issue 或已撤回回 409
//	署名:建立者與處理人一律是呼叫者本人的 actor(SelfActor):人類端點不能指定「誰」,伺服器管理員也一樣(不能冒名);
//	  角色憑證是角色的 actor。store 與內部是 actor id;輸出(presentDecisions)換成相容字串(人 = email、角色 = agent:<handle>)
//	  加 createdByActor、decidedByActor 物件
//	可見性(ISS-34 第 3 階段):有來源 Issue 的決定繼承該 Issue 的可見性(Issue 在受限專案,只有專案成員、組織管理員與伺服器管理員看得到;
//	  角色憑證要是專案成員);沒有來源視為組織層。看不到的決定,單筆回和不存在一樣的 404、列表與摘要沒有、也不能處理、撤回、重開或開 Issue。
//	  提出時指定看不到的來源 Issue 和不存在一樣回 400。判斷點集中在 project_access.go(projectAccess.Sees)
//	稽核:decision.add / decision.answer / decision.reopen / decision.withdraw / decision.to_issue;
//	  處理與重新開啟會在來源 Issue 加一則系統留言(署名處理人的 actor)

// DecisionIssueResult DecisionToIssue 的回應:新開的 Issue 與更新後的決定
type DecisionIssueResult struct {
	Issue    *model.Issue    `json:"issue"`
	Decision *model.Decision `json:"decision"`
}

// DecisionInput 提出一筆待決定或需要協助(署名一律是呼叫者,沒有 author 欄位)
type DecisionInput struct {
	// Kind decision(預設)或 help;help 不能帶 options
	Kind  string `json:"kind,omitempty"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Options 只有 decision 可以給,至少 2 個;id 沒給時依序編 o1、o2…;id 不能是 other
	Options []model.DecisionOption `json:"options,omitempty"`
	// SourceIssue 來源 Issue 的編號(key、內部 id 或舊 key;選填;必須存在而且呼叫者看得到,否則 400)
	SourceIssue string   `json:"sourceIssue,omitempty"`
	Blocks      []string `json:"blocks,omitempty"`
}

// DecisionAnswer 處理(回答)
type DecisionAnswer struct {
	// Answer decision:選項 id 或 other(附 note);help:done 或 declined
	Answer string `json:"answer"`
	// Note decision 選 other 時必填;help declined 時必填(原因);其他是附註
	Note string `json:"note,omitempty"`
	// Evidence help done 的證據字串(選填)
	Evidence string `json:"evidence,omitempty"`
}

// DecisionListQuery 列表條件
type DecisionListQuery struct {
	Status string `json:"status,omitempty"` // open、decided、withdrawn;空 = 全部
	Kind   string `json:"kind,omitempty"`   // decision、help;空 = 兩種
	Issue  string `json:"issue,omitempty"`  // 來源 Issue 的編號(key、內部 id 或舊 key)
	// Mine 「待我處理」:open 的(兩種都含);組織成員皆可處理,所以 = 全部 open。角色憑證沒有「我」,回空
	Mine bool `json:"mine,omitempty"`
}

// DecisionToIssueInput 由決定開出問題 Issue
type DecisionToIssueInput struct {
	Priority string   `json:"priority,omitempty"` // 預設 normal
	Labels   []string `json:"labels,omitempty"`   // 預設 [待決定] 或 [需要協助]
	// Repo 新 Issue 的 repo 標記(選填;沒指定專案時依它的連結決定預設專案,同 IssueInput.Repo)
	Repo string `json:"repo,omitempty"`
	// Project 新 Issue 放進的專案;nil = 預設(同 IssueInput.Project:依 repo 的連結,沒有就是組織層級),空字串 = 組織層級
	Project *string `json:"project,omitempty"`
}

func kindLabel(kind string) string {
	if kind == model.DecisionKindHelp {
		return "需要協助"
	}
	return "待決定"
}

func decisionNotFound(id string) error {
	return fail(404, "找不到 %s", strings.ToUpper(strings.TrimSpace(id)))
}

// findDecision 依 id(不分大小寫)找;不存在 404。回傳 store 的原樣(created_by、decided_by 是 actor id、sourceIssue 是 Issue 內部 id);
// 呼叫端要再用 decisionAccess 檢查可見性、用 presentDecisions 輸出
func (s *Service) findDecision(org, id string) (*model.Decision, error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	d, err := s.Store.GetDecision(org, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, decisionNotFound(id)
	}
	return d, err
}

// decisionAccess 呼叫者看得到哪些決定:來源 Issue 在看不到的專案就看不到(沒有來源 = 組織層,都看得到)。
// 同時帶著來源 Issue 的內部 id → Issue(輸出換成 key 用)
type decisionAccess struct {
	acc    *projectAccess
	issues map[string]model.Issue
}

// newDecisionAccess 建立可見性判斷;list 裡沒有任何來源 Issue 時不讀 Issue(沒有東西要查)
func (s *Service) newDecisionAccess(p *model.Principal, org string, list []model.Decision) (*decisionAccess, error) {
	acc, err := s.newProjectAccess(p, org)
	if err != nil {
		return nil, err
	}
	a := &decisionAccess{acc: acc, issues: map[string]model.Issue{}}
	for _, d := range list {
		if d.SourceIssue == "" {
			continue
		}
		all, err := s.Store.ListIssuesWithDeleted(org)
		if err != nil {
			return nil, err
		}
		for _, i := range all {
			a.issues[i.ID] = i
		}
		break
	}
	return a, nil
}

// Sees 看得到這筆決定
func (a *decisionAccess) Sees(d model.Decision) bool {
	if d.SourceIssue == "" {
		return true
	}
	i, ok := a.issues[d.SourceIssue]
	return !ok || a.acc.Sees(i.Project) // 查不到 Issue(剛被刪、外鍵還沒清):沒有東西可以洩漏
}

func (a *decisionAccess) filter(list []model.Decision) []model.Decision {
	out := make([]model.Decision, 0, len(list))
	for _, d := range list {
		if a.Sees(d) {
			out = append(out, d)
		}
	}
	return out
}

// visibleDecision 找一筆決定並確認呼叫者看得到(看不到回和不存在一樣的 404);回傳決定(store 的原樣)與可見性判斷
func (s *Service) visibleDecision(p *model.Principal, org, id string) (*model.Decision, *decisionAccess, error) {
	d, err := s.findDecision(org, id)
	if err != nil {
		return nil, nil, err
	}
	a, err := s.newDecisionAccess(p, org, []model.Decision{*d})
	if err != nil {
		return nil, nil, err
	}
	if !a.Sees(*d) {
		return nil, nil, decisionNotFound(id)
	}
	return d, a, nil
}

// presentDecisions store 的決定 → 對外輸出:建立者、處理人換成相容字串(人 = email、角色 = agent:<handle>)並補 actor 物件(整批一次查詢),
// 來源 Issue 換成顯示編號 key。不改傳入的資料
func (s *Service) presentDecisions(list []model.Decision, issues map[string]model.Issue) ([]model.Decision, error) {
	var ids []string
	for _, d := range list {
		ids = append(ids, d.CreatedBy, d.DecidedBy)
	}
	n, err := s.LoadActors(model.ActorIDs(ids...)...)
	if err != nil {
		return nil, err
	}
	out := make([]model.Decision, len(list))
	for k, d := range list {
		d = d.Clone()
		d.CreatedByActor, d.DecidedByActor = n.View(d.CreatedBy), n.View(d.DecidedBy)
		d.CreatedBy, d.DecidedBy = legacyOrRaw(n, d.CreatedBy), legacyOrRaw(n, d.DecidedBy)
		if d.SourceIssue != "" {
			d.SourceIssue = issues[d.SourceIssue].Key
		}
		out[k] = d
	}
	return out, nil
}

func (s *Service) presentDecision(d *model.Decision, a *decisionAccess) (*model.Decision, error) {
	out, err := s.presentDecisions([]model.Decision{*d}, a.issues)
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// decisionSource 提出時的來源 Issue:編號(key、id、舊 key)→ 內部 id。空 = 沒有;不存在、在垃圾桶、呼叫者看不到都是同一句 400
func (s *Service) decisionSource(p *model.Principal, org, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	notFound := fail(400, "找不到來源 Issue %s", ref)
	id, err := s.Store.ResolveIssueID(org, ref)
	if errors.Is(err, store.ErrNotFound) {
		return "", notFound
	}
	if err != nil {
		return "", err
	}
	i, err := s.Store.GetIssue(org, id) // 垃圾桶裡的當作不存在
	if errors.Is(err, store.ErrNotFound) {
		return "", notFound
	}
	if err != nil {
		return "", err
	}
	ok, err := s.canSeeProject(p, org, i.Project)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", notFound
	}
	return i.ID, nil
}

func cleanOptions(in []model.DecisionOption) ([]model.DecisionOption, error) {
	if len(in) < 2 {
		return nil, fail(400, "decision 至少要 2 個選項(只是要人做事請用 kind=help)")
	}
	if len(in) > model.MaxDecisionOptions {
		return nil, fail(400, "選項最多 %d 個", model.MaxDecisionOptions)
	}
	out := []model.DecisionOption{}
	seen := map[string]bool{}
	for k, o := range in {
		o.ID, o.Label, o.Description = strings.TrimSpace(o.ID), strings.TrimSpace(o.Label), strings.TrimSpace(o.Description)
		if o.Label == "" {
			return nil, fail(400, "每個選項都要有 label")
		}
		if o.ID == "" {
			o.ID = fmt.Sprintf("o%d", k+1)
		}
		if strings.EqualFold(o.ID, model.DecisionOther) {
			return nil, fail(400, "選項 id 不能是 %s(保留給「其他」)", model.DecisionOther)
		}
		if seen[strings.ToLower(o.ID)] {
			return nil, fail(400, "選項 id 重複:%s", o.ID)
		}
		seen[strings.ToLower(o.ID)] = true
		if tooLong(o.Label, model.MaxDecisionTitle) || tooLong(o.Description, model.MaxDecisionText) {
			return nil, fail(400, "選項的 label 最多 %d 字、description 最多 %d 字", model.MaxDecisionTitle, model.MaxDecisionText)
		}
		out = append(out, o)
	}
	return out, nil
}

// AddDecision 提出(署名是呼叫者本人;角色憑證是角色)
func (s *Service) AddDecision(p *model.Principal, org string, in DecisionInput) (*model.Decision, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = model.DecisionKindDecision
	}
	if !model.OneOf(kind, model.DecisionKinds) {
		return nil, fail(400, "kind 必須是 %s 之一", strings.Join(model.DecisionKinds, "、"))
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fail(400, "要給 title")
	}
	if tooLong(title, model.MaxDecisionTitle) {
		return nil, fail(400, "標題最多 %d 字", model.MaxDecisionTitle)
	}
	if tooLong(in.Body, model.MaxDecisionText) {
		return nil, fail(400, "背景說明最多 %d 字", model.MaxDecisionText)
	}
	opts := []model.DecisionOption{}
	switch kind {
	case model.DecisionKindHelp:
		if len(in.Options) > 0 {
			return nil, fail(400, "kind=help 不能有 options(要人選擇請用 kind=decision)")
		}
	default:
		var err error
		if opts, err = cleanOptions(in.Options); err != nil {
			return nil, err
		}
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	src, err := s.decisionSource(p, org, in.SourceIssue)
	if err != nil {
		return nil, err
	}
	now := s.mt()
	d := model.Decision{Kind: kind, Title: title, Body: strings.TrimSpace(in.Body), Options: opts, Status: model.DecisionOpen,
		SourceIssue: src, Blocks: model.CleanList(in.Blocks), CreatedBy: by.ID, CreatedAt: now, UpdatedAt: now}
	if d.Blocks == nil {
		d.Blocks = []string{}
	}
	s.decisionMu.Lock()
	saved, err := s.Store.CreateDecision(org, d)
	s.decisionMu.Unlock()
	if errors.Is(err, store.ErrBadRef) {
		return nil, fail(400, "找不到來源 Issue %s", strings.TrimSpace(in.SourceIssue)) // 剛好被永久刪除
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "decision.add", org, map[string]any{"id": saved.ID, "kind": kind})
	a, err := s.newDecisionAccess(p, org, []model.Decision{saved})
	if err != nil {
		return nil, err
	}
	return s.presentDecision(&saved, a)
}

// ListDecisions 列表(舊的在前;只列呼叫者看得到的)
func (s *Service) ListDecisions(p *model.Principal, org string, q DecisionListQuery) ([]model.Decision, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	if q.Status != "" && !model.OneOf(q.Status, model.DecisionStatuses) {
		return nil, fail(400, "status 必須是 %s 之一", strings.Join(model.DecisionStatuses, "、"))
	}
	if q.Kind != "" && !model.OneOf(q.Kind, model.DecisionKinds) {
		return nil, fail(400, "kind 必須是 %s 之一", strings.Join(model.DecisionKinds, "、"))
	}
	if q.Mine {
		if p.Agent != nil {
			return []model.Decision{}, nil
		}
		q.Status = model.DecisionOpen
	}
	mq := model.DecisionQuery{Status: q.Status, Kind: q.Kind}
	if ref := strings.TrimSpace(q.Issue); ref != "" {
		id, err := s.Store.ResolveIssueID(org, ref)
		if errors.Is(err, store.ErrNotFound) { // 沒有這張 Issue:沒有決定指向它(看不到的 Issue 在下面的可見性篩選裡一樣是空)
			return []model.Decision{}, nil
		}
		if err != nil {
			return nil, err
		}
		mq.Issue = id
	}
	list, err := s.Store.ListDecisions(org)
	if err != nil {
		return nil, err
	}
	hits := make([]model.Decision, 0, len(list))
	for _, d := range list {
		if mq.Match(d) {
			hits = append(hits, d)
		}
	}
	a, err := s.newDecisionAccess(p, org, hits)
	if err != nil {
		return nil, err
	}
	return s.presentDecisions(a.filter(hits), a.issues)
}

// GetDecision 看一筆(id 不分大小寫;看不到的和不存在一樣 404)
func (s *Service) GetDecision(p *model.Principal, org, id string) (*model.Decision, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	d, a, err := s.visibleDecision(p, org, id)
	if err != nil {
		return nil, err
	}
	return s.presentDecision(d, a)
}

// DecisionSummaries 每張 Issue 的決定摘要(來源 Issue key → open / decided 的筆數;沒有決定的 Issue、呼叫者看不到的決定都不在裡面)
func (s *Service) DecisionSummaries(p *model.Principal, org string) (map[string]model.DecisionSummary, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	list, err := s.Store.ListDecisions(org)
	if err != nil {
		return nil, err
	}
	a, err := s.newDecisionAccess(p, org, list)
	if err != nil {
		return nil, err
	}
	out := map[string]model.DecisionSummary{}
	for _, d := range a.filter(list) {
		i, ok := a.issues[d.SourceIssue]
		if d.SourceIssue == "" || !ok {
			continue
		}
		x := out[i.Key]
		switch d.Status {
		case model.DecisionOpen:
			x.Open++
		case model.DecisionDecided:
			x.Decided++
		}
		out[i.Key] = x
	}
	return out, nil
}

// issueNote 在來源 Issue(內部 id)加一則系統留言(署名 who 是 actor id;Issue 不在了、在垃圾桶或留言失敗都不影響決定本身)
func (s *Service) issueNote(org, issueID, who, text string) {
	if issueID == "" {
		return
	}
	mu := s.issueLock(org, issueID)
	mu.Lock()
	defer mu.Unlock()
	_, _ = s.Store.AddIssueComment(org, issueID, model.Comment{ID: uuid(), Author: who, Text: text, At: s.mt()})
}

func optionLabel(d model.Decision, id string) string {
	for _, o := range d.Options {
		if o.ID == id {
			return o.Label
		}
	}
	return id
}

func optionIDs(d model.Decision) string {
	ids := []string{}
	for _, o := range d.Options {
		ids = append(ids, o.ID)
	}
	return strings.Join(ids, "、")
}

// AnswerDecision 處理(回答)。只有人;組織成員皆可(看得到這筆決定的才行)
func (s *Service) AnswerDecision(p *model.Principal, org, id string, in DecisionAnswer) (*model.Decision, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	ans, note, ev := strings.TrimSpace(in.Answer), strings.TrimSpace(in.Note), strings.TrimSpace(in.Evidence)
	if tooLong(note, model.MaxDecisionText) || tooLong(ev, model.MaxDecisionText) {
		return nil, fail(400, "note、evidence 最多 %d 字", model.MaxDecisionText)
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	s.decisionMu.Lock()
	d, a, err := s.visibleDecision(p, org, id)
	if err != nil {
		s.decisionMu.Unlock()
		return nil, err
	}
	if d.Status != model.DecisionOpen {
		s.decisionMu.Unlock()
		return nil, fail(409, "%s 已經%s,不能再改;要改請先重新開啟", d.ID, map[string]string{model.DecisionDecided: "處理過了", model.DecisionWithdrawn: "撤回"}[d.Status])
	}
	switch d.Kind {
	case model.DecisionKindHelp:
		if ans != model.HelpDone && ans != model.HelpDeclined {
			s.decisionMu.Unlock()
			return nil, fail(400, "help 的 answer 必須是 done 或 declined")
		}
		if ans == model.HelpDeclined && note == "" {
			s.decisionMu.Unlock()
			return nil, fail(400, "declined 要附原因(note)")
		}
		if ans == model.HelpDeclined {
			ev = ""
		}
	default:
		if ev != "" {
			s.decisionMu.Unlock()
			return nil, fail(400, "evidence 只有 help 用")
		}
		found := false
		for _, o := range d.Options {
			if strings.EqualFold(o.ID, ans) {
				ans, found = o.ID, true
			}
		}
		switch {
		case found:
		case strings.EqualFold(ans, model.DecisionOther):
			ans = model.DecisionOther
			if note == "" {
				s.decisionMu.Unlock()
				return nil, fail(400, "選 other 要附說明(note)")
			}
		default:
			s.decisionMu.Unlock()
			return nil, fail(400, "answer 必須是選項 id(%s)或 other", optionIDs(*d))
		}
	}
	now := s.mt()
	d.Status, d.Answer, d.AnswerNote, d.Evidence, d.DecidedBy, d.DecidedAt, d.UpdatedAt = model.DecisionDecided, ans, note, ev, by.ID, &now, now
	err = s.Store.UpdateDecision(org, *d, model.DecisionOpen)
	s.decisionMu.Unlock()
	if errors.Is(err, store.ErrStale) {
		return nil, fail(409, "%s 剛剛被別人處理了", d.ID)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "decision.answer", org, map[string]any{"id": d.ID, "kind": d.Kind, "answer": ans})
	s.issueNote(org, d.SourceIssue, by.ID, answerNote(*d))
	return s.presentDecision(d, a)
}

// answerNote 來源 Issue 的系統留言(記錄結論)
func answerNote(d model.Decision) string {
	var b strings.Builder
	if d.Kind == model.DecisionKindHelp {
		if d.Answer == model.HelpDeclined {
			fmt.Fprintf(&b, "協助 %s「%s」:不處理", d.ID, d.Title)
		} else {
			fmt.Fprintf(&b, "協助 %s「%s」:已處理", d.ID, d.Title)
		}
	} else if d.Answer == model.DecisionOther {
		fmt.Fprintf(&b, "決定 %s「%s」:其他", d.ID, d.Title)
	} else {
		fmt.Fprintf(&b, "決定 %s「%s」:選擇「%s」", d.ID, d.Title, optionLabel(d, d.Answer))
	}
	if d.AnswerNote != "" {
		fmt.Fprintf(&b, "。%s", d.AnswerNote)
	}
	if d.Evidence != "" {
		fmt.Fprintf(&b, "(證據:%s)", d.Evidence)
	}
	return b.String()
}

// ReopenDecision 把處理過的重新開啟(組織管理員或原處理人;只有人)
func (s *Service) ReopenDecision(p *model.Principal, org, id string) (*model.Decision, error) {
	if err := notAgent(p); err != nil {
		return nil, err
	}
	role, err := s.orgRole(p, org)
	if err != nil {
		return nil, err
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	s.decisionMu.Lock()
	d, a, err := s.visibleDecision(p, org, id)
	if err != nil {
		s.decisionMu.Unlock()
		return nil, err
	}
	if d.Status != model.DecisionDecided {
		s.decisionMu.Unlock()
		return nil, fail(409, "%s 還沒處理過(狀態:%s),不需要重新開啟", d.ID, d.Status)
	}
	if role != "admin" && d.DecidedBy != by.ID {
		s.decisionMu.Unlock()
		return nil, fail(403, "只有組織管理員或原處理人可以重新開啟")
	}
	prev := d.Clone()
	d.Status, d.Answer, d.AnswerNote, d.Evidence, d.DecidedBy, d.DecidedAt, d.UpdatedAt = model.DecisionOpen, "", "", "", "", nil, s.mt()
	err = s.Store.UpdateDecision(org, *d, model.DecisionDecided)
	s.decisionMu.Unlock()
	if errors.Is(err, store.ErrStale) {
		return nil, fail(409, "%s 剛剛被別人改了", d.ID)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "decision.reopen", org, map[string]any{"id": d.ID, "previousAnswer": prev.Answer, "previousBy": s.presentWho(prev.DecidedBy)})
	s.issueNote(org, d.SourceIssue, by.ID, fmt.Sprintf("重新開啟 %s「%s」(原結論:%s)", d.ID, d.Title, answerNote(prev)))
	return s.presentDecision(d, a)
}

// WithdrawDecision 撤回(open 才能;建立者或組織管理員;角色憑證只能撤回自己提的)
func (s *Service) WithdrawDecision(p *model.Principal, org, id string) (*model.Decision, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	admin := false
	if p.Agent == nil {
		r, err := s.orgRole(p, org)
		if err != nil {
			return nil, err
		}
		admin = r == "admin"
	}
	by, err := s.SelfActor(p)
	if err != nil {
		return nil, err
	}
	s.decisionMu.Lock()
	d, a, err := s.visibleDecision(p, org, id)
	if err != nil {
		s.decisionMu.Unlock()
		return nil, err
	}
	if d.Status != model.DecisionOpen {
		s.decisionMu.Unlock()
		return nil, fail(409, "%s 不是開放中(狀態:%s),不能撤回", d.ID, d.Status)
	}
	if !admin && d.CreatedBy != by.ID {
		s.decisionMu.Unlock()
		return nil, fail(403, "只有建立者或組織管理員可以撤回")
	}
	d.Status, d.UpdatedAt = model.DecisionWithdrawn, s.mt()
	err = s.Store.UpdateDecision(org, *d, model.DecisionOpen)
	s.decisionMu.Unlock()
	if errors.Is(err, store.ErrStale) {
		return nil, fail(409, "%s 剛剛被別人改了", d.ID)
	}
	if err != nil {
		return nil, err
	}
	s.Audit(p, "decision.withdraw", org, map[string]any{"id": d.ID})
	return s.presentDecision(d, a)
}

// DecisionToIssue 由決定開出問題 Issue:新 Issue 的標題 = 決定的標題、內文含背景與選項、source = DEC-n;
// 決定的 sourceIssue 指向新 Issue。已經有來源 Issue 或已撤回回 409(不重複開)。只有人(組織成員)
func (s *Service) DecisionToIssue(p *model.Principal, org, id string, in DecisionToIssueInput) (*model.Issue, *model.Decision, error) {
	if err := notAgent(p); err != nil {
		return nil, nil, err
	}
	if _, err := s.orgRole(p, org); err != nil {
		return nil, nil, err
	}
	d, _, err := s.visibleDecision(p, org, id)
	if err != nil {
		return nil, nil, err
	}
	if d.Status == model.DecisionWithdrawn {
		return nil, nil, fail(409, "%s 已經撤回", d.ID)
	}
	if d.SourceIssue != "" {
		return nil, nil, fail(409, "%s 已經有來源 Issue", d.ID)
	}
	labels := in.Labels
	if len(labels) == 0 {
		labels = []string{kindLabel(d.Kind)}
	}
	var body strings.Builder
	body.WriteString(d.Body)
	if len(d.Options) > 0 {
		body.WriteString("\n\n## 選項\n")
		for _, o := range d.Options {
			rec := ""
			if o.Recommended {
				rec = "(建議)"
			}
			fmt.Fprintf(&body, "\n- **%s**%s", o.Label, rec)
			if o.Description != "" {
				fmt.Fprintf(&body, ":%s", o.Description)
			}
		}
	}
	fmt.Fprintf(&body, "\n\n(由 %s 開出;回答請用 %s)", d.ID, d.ID)
	issue, err := s.CreateIssue(p, org, IssueInput{Title: d.Title, Body: strings.TrimSpace(body.String()), Priority: in.Priority,
		Labels: labels, Source: d.ID, Repo: in.Repo, Project: in.Project})
	if err != nil {
		return nil, nil, err
	}
	s.decisionMu.Lock()
	cur, err := s.findDecision(org, id)
	if err == nil {
		if cur.SourceIssue != "" { // 同時有人先開了:這張 Issue 保留,但不改指向
			s.decisionMu.Unlock()
			return nil, nil, fail(409, "%s 剛剛被別人開了來源 Issue;剛建立的 %s 請自行處理", cur.ID, issue.Key)
		}
		cur.SourceIssue, cur.UpdatedAt = issue.ID, s.mt()
		err = s.Store.UpdateDecision(org, *cur, cur.Status)
	}
	s.decisionMu.Unlock()
	if errors.Is(err, store.ErrStale) {
		return nil, nil, fail(409, "%s 剛剛被別人改了;剛建立的 %s 請自行處理", d.ID, issue.Key)
	}
	if err != nil {
		return nil, nil, err
	}
	s.Audit(p, "decision.to_issue", org, map[string]any{"id": cur.ID, "issue": issue.Key})
	a, err := s.newDecisionAccess(p, org, []model.Decision{*cur})
	if err != nil {
		return nil, nil, err
	}
	out, err := s.presentDecision(cur, a)
	if err != nil {
		return nil, nil, err
	}
	return issue, out, nil
}

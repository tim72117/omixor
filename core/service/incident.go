package service

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"omixor-core/model"
	"omixor-core/store"
)

// 事件(資料模型見 model/incident.go)。規則照 Issue:
//
//	看、宣告、更新:組織成員(伺服器管理員是全部組織);角色憑證也可以(執行者記為 ai/<角色 id>)
//	匯入:組織管理員,組織還沒有事件時才可以(第一次搬資料用)
//	執行者(時間軸的 actor):內部存 actor id(incident_actor.go)。一般成員與角色憑證一律是自己;伺服器管理員可以用
//	  actor 或 author 指定,但必須解析成現存的角色或系統(D9:不能代別的使用者署名,找不到 400),都沒給是自己
//	  (舊的 ai/mcp 預設沒有了;mcp 與空白都當沒給)。時間軸項目的 actor:一般成員只能是自己或 system。
//	  對外輸出仍是舊的 {kind, id}(使用者 = person/<email>、角色 = ai/<handle>、系統 = system),見 presentIncident
//	commander:人員或角色(actor id、handle、email…,嚴格解析,找不到 400),沒給是呼叫者本人
//	宣告:title 必填(最多 200 字)、sev 1–4;沒給 commander 是呼叫者;沒給 timeline 時自動加「宣告 SEVn 事件,指揮官:…」
//	  與每個關聯告警一筆「關聯告警 …」;編號 INC-<n>(一個組織一起編號,從 INC-2001 開始)
//	更新(同 app 的 updateIncident / addIncidentNote / incident_update):
//	  status → 時間軸加一筆狀態(文字是 note,沒給是「狀態改為「…」」);改成 resolved 記 resolvedAt,改回其他狀態清掉
//	  沒給 status 時 note 是一筆註記(空白不加);finding 是一筆 AI 發現;events 是任意種類的項目
//	  aiSummary 給 "" 是清掉;addFollowUps、addAlertIds 附加(去重)

// IncidentEventInput 時間軸的一筆(宣告時的 timeline、更新時的 events)
type IncidentEventInput struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Date 沒給是現在
	Date *model.Time `json:"date,omitempty"`
	// Actor 沒給是呼叫者;一般成員只能給自己或 system
	Actor *model.IncidentActor `json:"actor,omitempty"`
}

// IncidentInput 宣告事件
type IncidentInput struct {
	Title    string   `json:"title"`
	Sev      int      `json:"sev"`
	Services []string `json:"services"`
	// Commander 指揮官(人員 id 或 email);沒給是呼叫者
	Commander string `json:"commander"`
	// CommanderName 自動產生的時間軸用的顯示名稱(沒給用 Commander)
	CommanderName string      `json:"commanderName,omitempty"`
	Impact        string      `json:"impact"`
	StartedAt     *model.Time `json:"startedAt,omitempty"` // 沒給是現在(例如從告警建立時用告警的時間)
	AISummary     string      `json:"aiSummary,omitempty"`
	AlertIDs      []string    `json:"alertIds,omitempty"`
	// AlertTitles 告警 id → 標題(自動產生「關聯告警「標題」」用;沒給就寫 id)
	AlertTitles map[string]string `json:"alertTitles,omitempty"`
	// Timeline 給了就用它當初始時間軸(不自動產生)
	Timeline []IncidentEventInput `json:"timeline,omitempty"`
	Actor    *model.IncidentActor `json:"actor,omitempty"`
	Author   string               `json:"author,omitempty"`
}

// IncidentPatch 更新事件(nil = 不改)
type IncidentPatch struct {
	Title        *string              `json:"title,omitempty"`
	Sev          *int                 `json:"sev,omitempty"`
	Status       *string              `json:"status,omitempty"`
	Services     *[]string            `json:"services,omitempty"`
	Commander    *string              `json:"commander,omitempty"`
	Impact       *string              `json:"impact,omitempty"`
	AISummary    *string              `json:"aiSummary,omitempty"`
	Note         *string              `json:"note,omitempty"`
	Finding      *string              `json:"finding,omitempty"`
	Events       []IncidentEventInput `json:"events,omitempty"`
	AddFollowUps []string             `json:"addFollowUps,omitempty"`
	AddAlertIDs  []string             `json:"addAlertIds,omitempty"`
	Actor        *model.IncidentActor `json:"actor,omitempty"`
	Author       string               `json:"author,omitempty"`
}

// Empty 什麼都沒給
func (p IncidentPatch) Empty() bool {
	return p.Title == nil && p.Sev == nil && p.Status == nil && p.Services == nil && p.Commander == nil && p.Impact == nil &&
		p.AISummary == nil && p.Note == nil && p.Finding == nil && len(p.Events) == 0 && len(p.AddFollowUps) == 0 && len(p.AddAlertIDs) == 0
}

// IncidentImport 第一次搬資料用
type IncidentImport struct {
	Incidents []model.Incident `json:"incidents"`
}

// ActorFromAuthor Issue 署名的寫法換成執行者:agent:<id> → ai/<id>;mcp 或空 → ai/mcp;user:<email> → person/<email>;
// system → system;其他當人員 id
func ActorFromAuthor(author string) model.IncidentActor {
	a := strings.TrimSpace(author)
	switch {
	case a == "" || a == model.MCPActorID:
		return model.AIActor(model.MCPActorID)
	case a == "system":
		return model.SystemActor()
	case strings.HasPrefix(a, "agent:") && len(a) > len("agent:"):
		return model.AIActor(strings.TrimPrefix(a, "agent:"))
	case strings.HasPrefix(a, "user:") && len(a) > len("user:"):
		return model.PersonActor(strings.TrimPrefix(a, "user:"))
	}
	return model.PersonActor(a)
}

func tooLong(s string, n int) bool { return utf8.RuneCountInString(s) > n }

// eventFrom 時間軸項目:檢查種類與長度,決定執行者(actor id)與時間
func (s *Service) eventFrom(p *model.Principal, org, caller string, in IncidentEventInput) (model.IncidentEvent, error) {
	if !model.OneOf(in.Kind, model.IncidentEventKinds) {
		return model.IncidentEvent{}, fail(400, "時間軸的 kind 必須是 %s 之一", strings.Join(model.IncidentEventKinds, "、"))
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return model.IncidentEvent{}, fail(400, "時間軸的內容不能是空的")
	}
	if tooLong(text, model.MaxIncidentText) {
		return model.IncidentEvent{}, fail(400, "時間軸一則最多 %d 字", model.MaxIncidentText)
	}
	who := caller
	if in.Actor != nil {
		if !in.Actor.Valid() {
			return model.IncidentEvent{}, fail(400, "actor 的 kind 必須是 person、ai 或 system(person、ai 要有 id)")
		}
		if in.Actor.Kind == "system" { // 任何成員的時間軸項目都可以是系統事件
			who = model.SystemActorOmixor
		} else {
			id, err := s.incidentActor(p, org, in.Actor, "")
			if err != nil {
				return model.IncidentEvent{}, err
			}
			who = id
		}
	}
	at := s.mt()
	if in.Date != nil && !in.Date.IsZero() {
		at = model.Time{Time: in.Date.UTC().Truncate(1e9)}
	}
	return model.IncidentEvent{ID: uuid(), Date: at, ActorID: who, Kind: in.Kind, Text: text}, nil
}

func (s *Service) newEvent(who, kind, text string) model.IncidentEvent {
	return model.IncidentEvent{ID: uuid(), Date: s.mt(), ActorID: who, Kind: kind, Text: text}
}

func checkSev(sev int) error {
	if sev < model.MinIncidentSev || sev > model.MaxIncidentSev {
		return fail(400, "sev 必須是 1–4")
	}
	return nil
}

// findIncident 依 id 找(不分大小寫);不存在 404。回傳 store 的原樣(commander、created_by、時間軸 actor 都是 actor id);對外要用 presentIncident
func (s *Service) findIncident(org, id string) (*model.Incident, error) {
	id = strings.TrimSpace(id)
	i, err := s.Store.GetIncident(org, id)
	if errors.Is(err, store.ErrNotFound) && strings.ToUpper(id) != id {
		i, err = s.Store.GetIncident(org, strings.ToUpper(id))
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, fail(404, "找不到事件 %s", id)
	}
	return i, err
}

func (s *Service) ListIncidents(p *model.Principal, org string) ([]model.Incident, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	list, err := s.Store.ListIncidents(org)
	if err != nil {
		return nil, err
	}
	return s.presentIncidents(list)
}

func (s *Service) GetIncident(p *model.Principal, org, id string) (*model.Incident, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	i, err := s.findIncident(org, id)
	if err != nil {
		return nil, err
	}
	return s.presentIncident(i)
}

// DeclareIncident 宣告事件
func (s *Service) DeclareIncident(p *model.Principal, org string, in IncidentInput) (*model.Incident, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fail(400, "要給 title")
	}
	if tooLong(title, model.MaxIncidentTitle) {
		return nil, fail(400, "標題最多 %d 字", model.MaxIncidentTitle)
	}
	if err := checkSev(in.Sev); err != nil {
		return nil, err
	}
	impact := strings.TrimSpace(in.Impact)
	if tooLong(impact, model.MaxIncidentText) || tooLong(in.AISummary, model.MaxIncidentText) {
		return nil, fail(400, "影響與 AI 摘要最多 %d 字", model.MaxIncidentText)
	}
	who, err := s.incidentActor(p, org, in.Actor, in.Author)
	if err != nil {
		return nil, err
	}
	commander, err := s.commanderID(p, org, in.Commander)
	if err != nil {
		return nil, err
	}
	started := s.mt()
	if in.StartedAt != nil && !in.StartedAt.IsZero() {
		started = model.Time{Time: in.StartedAt.UTC().Truncate(1e9)}
	}
	inc := model.Incident{Title: title, Sev: in.Sev, Status: "investigating", Services: model.CleanList(in.Services),
		Commander: commander, StartedAt: started, Impact: impact, Timeline: []model.IncidentEvent{},
		FollowUps: []string{}, CreatedBy: who}
	if alerts := model.CleanList(in.AlertIDs); len(alerts) > 0 {
		inc.AlertIDs = alerts
	}
	if sum := strings.TrimSpace(in.AISummary); sum != "" {
		inc.AISummary = &sum
	}
	if len(in.Timeline) > 0 {
		for _, e := range in.Timeline {
			ev, err := s.eventFrom(p, org, who, e)
			if err != nil {
				return nil, err
			}
			inc.Timeline = append(inc.Timeline, ev)
		}
	} else {
		name := strings.TrimSpace(in.CommanderName)
		if name == "" {
			name = strings.TrimSpace(in.Commander)
		}
		if name == "" {
			name = s.presentWho(commander)
		}
		inc.Timeline = append(inc.Timeline, s.newEvent(who, "status", fmt.Sprintf("宣告 SEV%d 事件,指揮官:%s", in.Sev, name)))
		for _, a := range inc.AlertIDs {
			label := a
			if t := strings.TrimSpace(in.AlertTitles[a]); t != "" {
				label = "「" + t + "」"
			}
			inc.Timeline = append(inc.Timeline, s.newEvent(who, "alert", "關聯告警"+label))
		}
	}
	saved, err := s.Store.CreateIncident(org, inc)
	if err != nil {
		return nil, err
	}
	s.Audit(p, "incident.declare", org, map[string]any{"id": saved.ID, "sev": saved.Sev})
	return s.presentIncident(&saved)
}

// UpdateIncident 更新事件(規則見檔頭)
func (s *Service) UpdateIncident(p *model.Principal, org, id string, in IncidentPatch) (*model.Incident, error) {
	if err := s.viewer(p, org); err != nil {
		return nil, err
	}
	if in.Status != nil && !model.OneOf(*in.Status, model.IncidentStatuses) {
		return nil, fail(400, "status 必須是 %s 之一", strings.Join(model.IncidentStatuses, "、"))
	}
	if in.Sev != nil {
		if err := checkSev(*in.Sev); err != nil {
			return nil, err
		}
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			return nil, fail(400, "標題不能是空的")
		}
		if tooLong(t, model.MaxIncidentTitle) {
			return nil, fail(400, "標題最多 %d 字", model.MaxIncidentTitle)
		}
	}
	for _, v := range []*string{in.Impact, in.AISummary, in.Note, in.Finding} {
		if v != nil && tooLong(*v, model.MaxIncidentText) {
			return nil, fail(400, "內容最多 %d 字", model.MaxIncidentText)
		}
	}
	who, err := s.incidentActor(p, org, in.Actor, in.Author)
	if err != nil {
		return nil, err
	}
	events := []model.IncidentEvent{}
	for _, e := range in.Events {
		ev, err := s.eventFrom(p, org, who, e)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}

	s.incidentMu.Lock()
	defer s.incidentMu.Unlock()
	cur, err := s.findIncident(org, id)
	if err != nil {
		return nil, err
	}
	n := cur.Clone()
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	if in.Status != nil {
		text := note
		if text == "" {
			text = "狀態改為「" + model.IncidentStatusLabel[*in.Status] + "」"
		}
		n.Status = *in.Status
		if n.Status == "resolved" {
			if cur.Status != "resolved" || n.ResolvedAt == nil {
				t := s.mt()
				n.ResolvedAt = &t
			}
		} else {
			n.ResolvedAt = nil
		}
		n.Timeline = append(n.Timeline, s.newEvent(who, "status", text))
	} else if note != "" {
		n.Timeline = append(n.Timeline, s.newEvent(who, "note", note))
	}
	if in.Finding != nil {
		if f := strings.TrimSpace(*in.Finding); f != "" {
			n.Timeline = append(n.Timeline, s.newEvent(who, "aiFinding", f))
		}
	}
	n.Timeline = append(n.Timeline, events...)
	if in.AISummary != nil {
		if sum := strings.TrimSpace(*in.AISummary); sum != "" {
			n.AISummary = &sum
		} else {
			n.AISummary = nil
		}
	}
	if in.Impact != nil {
		n.Impact = strings.TrimSpace(*in.Impact)
	}
	if in.Title != nil {
		n.Title = strings.TrimSpace(*in.Title)
	}
	if in.Sev != nil {
		n.Sev = *in.Sev
	}
	if in.Services != nil {
		n.Services = model.CleanList(*in.Services)
	}
	if in.Commander != nil {
		if c := strings.TrimSpace(*in.Commander); c != "" {
			id, err := s.commanderID(p, org, c)
			if err != nil {
				return nil, err
			}
			n.Commander = id
		}
	}
	if len(in.AddFollowUps) > 0 {
		n.FollowUps = model.CleanList(append(n.FollowUps, in.AddFollowUps...))
	}
	if len(in.AddAlertIDs) > 0 {
		n.AlertIDs = model.CleanList(append(n.AlertIDs, in.AddAlertIDs...))
	}
	if err := s.Store.UpdateIncident(org, n); err != nil {
		return nil, err
	}
	s.Audit(p, "incident.update", org, map[string]any{"id": n.ID})
	return s.presentIncident(&n)
}

// ImportIncidents 第一次搬資料用(組織管理員):保留 id、時間與時間軸;這個組織已經有事件就拒絕(409)
func (s *Service) ImportIncidents(p *model.Principal, org string, list []model.Incident) (*model.ImportResult, error) {
	if err := s.needOrgAdmin(p, org); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fail(400, "沒有要匯入的事件")
	}
	seen := map[string]bool{}
	out := []model.Incident{}
	who := s.newImportWho(p, org)
	for _, raw := range list {
		i := raw.Clone()
		i.ID = strings.ToUpper(strings.TrimSpace(i.ID))
		if i.Number() == 0 || strings.TrimSpace(i.Title) == "" {
			return nil, fail(400, "每一筆都要有 id(INC-<數字>)與 title")
		}
		if seen[i.ID] {
			return nil, fail(400, "id 重複:%s", i.ID)
		}
		seen[i.ID] = true
		if i.Sev < model.MinIncidentSev || i.Sev > model.MaxIncidentSev {
			i.Sev = 3
		}
		if !model.OneOf(i.Status, model.IncidentStatuses) {
			i.Status = "investigating"
		}
		if i.StartedAt.IsZero() {
			i.StartedAt = s.mt()
		}
		i.Services = model.CleanList(i.Services)
		i.FollowUps = model.CleanList(i.FollowUps)
		if i.AlertIDs != nil {
			i.AlertIDs = model.CleanList(i.AlertIDs)
		}
		for k, e := range i.Timeline {
			if strings.TrimSpace(e.ID) == "" {
				i.Timeline[k].ID = uuid()
			}
			if !model.OneOf(e.Kind, model.IncidentEventKinds) {
				i.Timeline[k].Kind = "note"
			}
			if !e.Actor.Valid() {
				i.Timeline[k].Actor = model.SystemActor()
			}
			if e.Date.IsZero() {
				i.Timeline[k].Date = i.StartedAt
			}
		}
		if !who.lenient { // 組織管理員:指揮官、建立者、時間軸署名先轉成 actor id(store 收到 actor id 原樣用,不會再走寬鬆解析器)
			if err := restrictIncidentActors(who, &i); err != nil {
				return nil, err
			}
		}
		out = append(out, i)
	}
	s.incidentMu.Lock()
	defer s.incidentMu.Unlock()
	cur, err := s.Store.ListIncidents(org)
	if err != nil {
		return nil, err
	}
	if len(cur) > 0 {
		return nil, fail(409, "%s 已經有 %d 筆事件;匯入只在第一次搬資料時用,之後請用宣告與更新", org, len(cur))
	}
	n, err := s.Store.ImportIncidents(org, out)
	if err != nil {
		return nil, fail(409, "%v", err)
	}
	s.Audit(p, "incidents.import", org, map[string]any{"inserted": n})
	return &model.ImportResult{Org: org, Inserted: n}, nil
}

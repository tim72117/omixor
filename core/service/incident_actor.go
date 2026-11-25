package service

// incident_actor.go 事件的「誰」:署名(時間軸 actor、created_by)與指揮官的輸入解析,以及對外輸出的相容還原。
// 內部(store、本檔以外的 incident.go 流程)一律是 actor id;輸出出口(presentIncident)才轉回舊格式:
//   commander、createdBy → 相容字串(人 = email、角色 = agent:<handle>、系統 = 舊名稱,model.LegacyFree)
//   時間軸 actor → 舊的 {kind, id}(使用者 = person/<email>、角色 = ai/<handle>、系統 = system)
// 規格:雲端 DOC-13 §1.4 #16~#18、§3.4 第 5、6 列、§3.5;契約 omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》。

import (
	"strings"

	"omixor-core/model"
)

// incidentRaw 署名輸入(actor 物件或 author 字串)→ 給解析器的字串;sys = 明確要求系統署名。
// 空白與舊的 mcp 署名(ai/mcp)當沒給(= 本人)
func incidentRaw(given *model.IncidentActor, author string) (raw string, sys bool) {
	if given != nil && given.Valid() {
		switch given.Kind {
		case "system":
			return "", true
		case "person":
			return strings.TrimSpace(given.ID), false
		}
		if id := strings.TrimSpace(given.ID); id != model.MCPActorID {
			return model.AgentPrefix + id, false
		}
		return "", false
	}
	switch a := strings.TrimSpace(author); a {
	case "", model.MCPActorID:
		return "", false
	case "system":
		return "", true
	default:
		return a, false
	}
}

// incidentActor 這次操作(或某則時間軸)的執行者 actor id。一般成員與角色憑證一律是自己(given 靜默忽略);
// 伺服器管理員:given 必須解析成現存的角色或系統,不能代別的使用者(D9,ErrActorImpersonate),找不到 400,沒給是本人;
// 明確的 system 署名是 sys_omixor(系統事件)
func (s *Service) incidentActor(p *model.Principal, org string, given *model.IncidentActor, author string) (string, error) {
	raw, sys := incidentRaw(given, author)
	if sys && p.Agent == nil && p.ServerAdmin() {
		return model.SystemActorOmixor, nil
	}
	ref, err := s.ResolveAuthor(p, org, raw, AuthorIgnore)
	if err != nil {
		return "", err
	}
	return ref.ID, nil
}

// legacyRaw 舊的 {kind, id} → 寬鬆解析器接受的字串
func legacyRaw(a model.IncidentActor) string {
	switch a.Kind {
	case "person":
		if strings.Contains(a.ID, ":") {
			return a.ID
		}
		return model.UserPrefix + a.ID
	case "ai":
		if a.ID == model.MCPActorID {
			return model.MCPActorID
		}
		return model.AgentPrefix + a.ID
	}
	return model.SystemActorOmixor
}

// commanderID 指揮官輸入(actor id、handle、@handle、email、agent:/user: 前綴)→ actor id。嚴格解析,找不到 400、種類不符 400;
// 空白 = 呼叫者本人;呼叫者沒有 actor 是資料錯誤,直接回錯誤
func (s *Service) commanderID(p *model.Principal, org, raw string) (string, error) {
	ref, err := s.WhoOrSelf(p, org, raw, WantPeople)
	if err != nil {
		return "", err
	}
	return ref.ID, nil
}

// ---- 輸出 ----

// presentWho 一個 actor id → 相容字串(LegacyFree);不是 actor id 的(未展開的舊資料)原樣
func (s *Service) presentWho(id string) string {
	n, err := s.LoadActors(id)
	if err != nil {
		return ""
	}
	return legacyOrRaw(n, id)
}

func legacyOrRaw(n *Names, id string) string {
	if id != "" && !model.ValidActorID(id) {
		return id
	}
	return n.Legacy(id, model.LegacyFree)
}

func (s *Service) presentIncident(i *model.Incident) (*model.Incident, error) {
	out, err := s.presentIncidents([]model.Incident{*i})
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// presentIncidents store 的事件(actor id)→ 對外的相容輸出;整批一次查詢(不是每筆、每則時間軸各查一次)。
// 不改傳入的資料(時間軸複製一份)。還沒展開的舊資料(commander 不是 actor id、時間軸沒有 ActorID)保留原本的字串
func (s *Service) presentIncidents(list []model.Incident) ([]model.Incident, error) {
	var ids []string
	for _, i := range list {
		ids = append(ids, i.Commander, i.CreatedBy)
		for _, e := range i.Timeline {
			ids = append(ids, e.ActorID)
		}
	}
	n, err := s.LoadActors(model.ActorIDs(ids...)...)
	if err != nil {
		return nil, err
	}
	out := make([]model.Incident, len(list))
	for k, i := range list {
		i.CommanderActor, i.CreatedByActor = n.View(i.Commander), n.View(i.CreatedBy)
		i.Commander, i.CreatedBy = legacyOrRaw(n, i.Commander), legacyOrRaw(n, i.CreatedBy)
		tl := make([]model.IncidentEvent, len(i.Timeline))
		for j, e := range i.Timeline {
			if e.ActorID != "" {
				kind, who := n.EventActor(e.ActorID)
				e.Actor = model.IncidentActor{Kind: kind, ID: who}
				e.ActorRef = n.View(e.ActorID)
			}
			tl[j] = e
		}
		i.Timeline = tl
		out[k] = i
	}
	return out, nil
}

// restrictIncidentActors 組織管理員匯入事件:指揮官、建立者、時間軸的執行者依 actor_import.go 的規則轉成 actor id。
// 時間軸的 {kind, id} 轉完一律是 {ai, <actor id>}(只是傳給 store 的搬運格式,不管真正的種類;store 的匯入收到本來就是 actor id 的值原樣用;system 種類維持)
func restrictIncidentActors(w *importWho, i *model.Incident) error {
	var err error
	if i.Commander, err = w.id(i.Commander, ""); err != nil {
		return err
	}
	if i.CreatedBy, err = w.id(i.CreatedBy, ""); err != nil {
		return err
	}
	for k, e := range i.Timeline {
		if e.Actor.Kind == "system" {
			continue
		}
		raw := legacyRaw(e.Actor)
		if model.ValidActorID(strings.ToLower(strings.TrimSpace(e.Actor.ID))) {
			raw = e.Actor.ID
		}
		id, err := w.id(raw, "")
		if err != nil {
			return err
		}
		i.Timeline[k].Actor = model.AIActor(id) // 種類填 ai:store 的事件解析對 person 的 id 一律加 user: 前綴,ai 才會先認 actor id
	}
	return nil
}

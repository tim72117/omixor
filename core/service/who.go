package service

import (
	"omixor-core/model"
)

// who.go 「誰」的署名與輸出(ISS-36 C-2e:稽核、整合、系統設定、MCP 設定的 updated_by):
// 寫入存 actor id,輸出用 present 轉成相容字串(email 或名稱)。

// whoActor 署名用的 actor id:Principal.Actor,沒有時由使用者列、角色補;補不到回空(欄位存 NULL,不讓署名擋住設定的儲存)
func (s *Service) whoActor(p *model.Principal) string {
	if p == nil {
		return ""
	}
	if p.Actor.ID != "" {
		return p.Actor.ID
	}
	if ref, err := s.SelfActor(p); err == nil {
		return ref.ID
	}
	return ""
}

// whoName 署名:呼叫者的 actor id(只看 Principal.Actor;core/modules 的 WhoName 包裝用,沒有 Service 可以補)
func whoName(p *model.Principal) string {
	if p == nil {
		return ""
	}
	return p.Actor.ID
}

// WhoLegacy actor id → 相容的署名字串(人 = email、角色 = agent:<handle>、系統 = 舊名稱;不是 actor id 的舊資料原樣回傳)。給選用模組輸出 updatedBy 用
func (s *Service) WhoLegacy(id string) string {
	if !model.ValidActorID(id) {
		return id
	}
	out, _, err := s.Present(id, model.LegacyFree)
	if err != nil {
		return ""
	}
	return out
}

// WhoPresent actor id → (相容字串, actor 物件),一次查詢;給選用模組、單筆輸出的 updatedBy 用(ISS-36 C-3)。
// 不是 actor id 的舊資料:字串原樣、物件 nil;查不到:空字串、nil
func (s *Service) WhoPresent(id string) (string, *model.ActorView) {
	if !model.ValidActorID(id) {
		return id, nil
	}
	n, err := s.LoadActors(id)
	if err != nil {
		return "", nil
	}
	return n.Legacy(id, model.LegacyFree), n.View(id)
}

// WhoView actor id → actor 物件(查不到、不是 actor id 回 nil)
func (s *Service) WhoView(id string) *model.ActorView {
	_, v := s.WhoPresent(id)
	return v
}

// whoViewAll 一批 actor id 一次查詢 → id → actor 物件(查不到的不在 map 裡)
func (s *Service) whoViewAll(ids ...string) map[string]*model.ActorView {
	out := map[string]*model.ActorView{}
	var actors []string
	for _, id := range ids {
		if model.ValidActorID(id) {
			actors = append(actors, id)
		}
	}
	n, err := s.LoadActors(actors...)
	if err != nil {
		return out
	}
	for _, id := range actors {
		if v := n.View(id); v != nil {
			out[id] = v
		}
	}
	return out
}

// whoLegacyAll 同 WhoLegacy,一批 id 一次查詢;回傳 id → 相容字串(不是 actor id 的原樣)
func (s *Service) whoLegacyAll(ids ...string) map[string]string {
	out := map[string]string{}
	var actors []string
	for _, id := range ids {
		if model.ValidActorID(id) {
			actors = append(actors, id)
		} else if id != "" {
			out[id] = id
		}
	}
	n, err := s.LoadActors(actors...)
	if err != nil {
		return out
	}
	for _, id := range actors {
		out[id] = n.Legacy(id, model.LegacyFree)
	}
	return out
}

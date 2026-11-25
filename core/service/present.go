// present.go actor id → 相容輸出(ISS-36 C-2 共用地基;規格見雲端 DOC-13 §3.5,契約見 omixor 文件櫃 DOC-34《core/store/ACTOR_CONTRACT.md》 第 5 節)
//
// store 與服務層內部的「誰」欄位是 actor id。對外(API、MCP、console 後端、舊用戶端)過渡期同時輸出:
//   - 相容字串:舊格式不變(人 = email、角色 = agent:<handle>、系統 = 舊名稱…;格式依欄位,model.LegacyForm)
//   - 新的 actor 物件:{id, kind, name, handle}(model.ActorRef)
//
// 用法(讀取方法的出口,一個方法一次批次查詢,不是每筆各查一次):
//
//	n, err := s.LoadActors(model.ActorIDs(issue.CreatedBy, issue.Assignee, issue.DeletedBy, ...)...)   // 收集 id、去重、不存在的略過
//	out.CreatedBy      = n.Legacy(issue.CreatedBy, model.LegacyFree)        // "alice@x.com"
//	out.CreatedByActor = n.Ref(issue.CreatedBy)                              // *ActorRef,沒有就是 nil
//	out.Assignee       = n.Legacy(issue.Assignee, model.LegacyAgentID)      // 角色 handle
//
// 注意不要把「已補名字」的輸出結構寫回 store:store 寫入只收 actor id(外鍵會擋字串);輸入端的 ResolveActor / ResolveAuthor 接受相容字串,
// 所以「讀出再寫回」(例如 UpdateIssue 帶整份 Comments、CLI restore)仍可運作。

package service

import (
	"omixor-core/model"
	"omixor-core/store"
)

// Names 一批 actor 的名稱查表(LoadActors 的結果)。零值可用(什麼都查不到)
type Names struct {
	recs  map[string]model.ActorRecord
	users map[string]store.UserInfo // 使用者 actor id → 使用者列的 id、email(沒有使用者列的「未註冊使用者」不在裡面)
	live  map[string]bool           // 已停用的角色 actor id → 角色表裡的 Active(不在 = 角色已刪、退役);只查有停用的角色所屬的組織
}

// LoadActors 依 actor id 批次查名稱(空字串、重複的先濾掉;查不到的 actor 在 Names 裡就是查不到,輸出空字串或 nil,不當錯誤)
func (s *Service) LoadActors(ids ...string) (*Names, error) {
	n := &Names{recs: map[string]model.ActorRecord{}, users: map[string]store.UserInfo{}, live: map[string]bool{}}
	ids = model.ActorIDs(ids...)
	if len(ids) == 0 {
		return n, nil
	}
	recs, err := s.Store.ActorsByIDs(ids)
	if err != nil {
		return nil, err
	}
	n.recs = recs
	var userIDs []string
	orgs := map[string]bool{}
	for id, r := range recs {
		switch {
		case r.Kind == model.ActorKindUser:
			userIDs = append(userIDs, id)
		case r.Kind == model.ActorKindAgent && r.Disabled() && r.Org != "":
			orgs[r.Org] = true
		}
	}
	for org := range orgs { // 停用的角色:角色表還有 = 暫停,沒有 = 已刪(退役)
		gs, err := s.Store.ListAgents(org)
		if err != nil {
			return nil, err
		}
		for _, g := range gs {
			if g.ActorID != "" {
				n.live[g.ActorID] = g.Active
			}
		}
	}
	if len(userIDs) > 0 {
		if n.users, err = s.Store.UserInfoByActorIDs(userIDs); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// Record 這個 actor 的完整記錄(含退役的);沒有回 false
func (n *Names) Record(id string) (model.ActorRecord, bool) {
	if n == nil || id == "" {
		return model.ActorRecord{}, false
	}
	r, ok := n.recs[id]
	return r, ok
}

// Legacy actor id → 相容字串(格式 f);空 id、查不到的 actor 回空字串
func (n *Names) Legacy(id string, f model.LegacyForm) string {
	r, ok := n.Record(id)
	if !ok {
		return ""
	}
	u := n.users[id]
	return model.LegacyActorString(r, u.Email, u.ID, f)
}

// Ref actor id → actor 物件(name、handle 是「現在」的值,不是留言當時的值);空 id 或查不到回 nil(輸出 omitempty)
func (n *Names) Ref(id string) *model.ActorRef {
	r, ok := n.Record(id)
	if !ok {
		return nil
	}
	ref := r.Ref()
	return &ref
}

// View actor id → 完整的 actor 物件(含 email、status;C-3 輸出的 xxxActor 欄位用);空 id 或查不到回 nil(輸出 omitempty)
func (n *Names) View(id string) *model.ActorView {
	r, ok := n.Record(id)
	if !ok {
		return nil
	}
	v := model.ActorView{ID: r.ID, Kind: r.Kind, Handle: r.Handle, Name: r.Name, Status: model.ActorStatusActive}
	switch r.Kind {
	case model.ActorKindUser:
		u := n.users[id]
		v.Email = u.Email
		if u.Disabled {
			v.Status = model.ActorStatusDisabled
		}
	case model.ActorKindAgent:
		if r.Disabled() {
			v.Status = model.ActorStatusRetired
			if active, ok := n.live[id]; ok {
				v.Status = model.ActorStatusSuspended
				if active { // 角色表說現役、actor 卻有停用時間(不一致):以角色表為準
					v.Status = model.ActorStatusActive
				}
			}
		}
	}
	return &v
}

// Views 一串 actor id → actor 物件(查不到的略過;沒有任何一個回空切片,不是 nil,輸出陣列不會是 null)
func (n *Names) Views(ids []string) []model.ActorView {
	out := make([]model.ActorView, 0, len(ids))
	for _, id := range ids {
		if v := n.View(id); v != nil {
			out = append(out, *v)
		}
	}
	return out
}

// RefOrZero 同 Ref,查不到回零值(輸出欄位不用 omitempty 時)
func (n *Names) RefOrZero(id string) model.ActorRef {
	if r := n.Ref(id); r != nil {
		return *r
	}
	return model.ActorRef{}
}

// EventActor 事件時間軸 actor 的舊 {kind, id}(person/<email>、ai/<handle>、system/空…;見 model.LegacyEventActor)
func (n *Names) EventActor(id string) (kind, who string) {
	r, ok := n.Record(id)
	if !ok {
		return "", ""
	}
	return model.LegacyEventActor(r, n.users[id].Email)
}

// Present 一次轉一個:不批次,只給單筆輸出(登入回應、單一物件)用;多筆請用 LoadActors
func (s *Service) Present(id string, f model.LegacyForm) (legacy string, ref *model.ActorRef, err error) {
	n, err := s.LoadActors(id)
	if err != nil {
		return "", nil, err
	}
	return n.Legacy(id, f), n.Ref(id), nil
}

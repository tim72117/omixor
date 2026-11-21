// actors_list.go 列出組織看得到的 actor(ISS-36 B-4;規格見雲端 omixor DOC-13 §3.2)
//
// 範圍(和 ResolveActor 的授權一致,不洩漏不在組織裡的人與角色):
//   - 使用者:只有「組織成員」而且有 omixor_users 列(未註冊使用者 actor 不會出現,不能被探測);email(= handle)只給看得到組織的人
//   - 角色:只限這個組織的角色 actor(現役、暫停;退役的要明確指定 status=retired 或 all)
//   - 系統:固定清單與遷移建立的 sys_ actor
//
// 呼叫者要是組織成員或伺服器管理員(同 ListMembers;角色憑證不開放)。

package service

import (
	"sort"
	"strings"

	"omixor-core/model"
	"omixor-core/store"
)

// 狀態值(model.ActorView.Status;定義在 model,這裡保留原名)
const (
	ActorStatusActive    = model.ActorStatusActive
	ActorStatusDisabled  = model.ActorStatusDisabled
	ActorStatusSuspended = model.ActorStatusSuspended
	ActorStatusRetired   = model.ActorStatusRetired
)

// ActorListItem GET /actors 的一筆;和各資料「誰」欄位旁邊的 actor 物件是同一個型別(ISS-36 C-3,搬到 model 讓 contract 範例檔能產出)
type ActorListItem = model.ActorView

// ActorListFilter kind 空 = 不分種類;q 是 handle 或顯示名稱的前綴(不分大小寫);
// status 空 = 不含退役,"all" = 全部,否則只列這個狀態
type ActorListFilter struct {
	Kind   string
	Q      string
	Status string
}

// ListOrgActors 列出組織可見的 actor(依種類 user、agent、system,同種類依 handle 排序)
func (s *Service) ListOrgActors(p *model.Principal, org string, f ActorListFilter) ([]ActorListItem, error) {
	if _, err := s.orgRole(p, org); err != nil {
		return nil, err
	}
	if f.Kind != "" && !model.ActorKind(f.Kind).Valid() {
		return nil, fail(400, "kind 必須是 user、agent 或 system")
	}
	switch f.Status {
	case "", "all", ActorStatusActive, ActorStatusDisabled, ActorStatusSuspended, ActorStatusRetired:
	default:
		return nil, fail(400, "status 必須是 active、disabled、suspended、retired 或 all")
	}
	want := func(k model.ActorKind) bool { return f.Kind == "" || f.Kind == string(k) }
	out := []ActorListItem{}

	if want(model.ActorKindUser) {
		ms, err := s.Store.ListMembers(org)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(ms))
		for _, m := range ms {
			ids = append(ids, m.ActorID)
		}
		names, err := s.LoadActors(ids...)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			rec, ok := names.Record(m.ActorID)
			if !ok || rec.Kind != model.ActorKindUser { // 成員一定有使用者列;沒有 actor 的舊資料略過
				continue
			}
			u, err := s.Store.UserByID(m.UserID)
			if err != nil {
				continue
			}
			st := ActorStatusActive
			if u.Disabled {
				st = ActorStatusDisabled
			}
			out = append(out, ActorListItem{ID: rec.ID, Kind: rec.Kind, Handle: rec.Handle, Name: rec.Name, Email: u.Email, Status: st})
		}
	}
	if want(model.ActorKindAgent) {
		gs, err := s.Store.ListAgents(org)
		if err != nil {
			return nil, err
		}
		live := map[string]bool{} // actor id → 角色表裡的 Active
		for _, g := range gs {
			if g.ActorID != "" {
				live[g.ActorID] = g.Active
			}
		}
		recs, err := s.Store.ListActors(store.ActorFilter{Kind: model.ActorKindAgent, Org: org, IncludeDisabled: true})
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if rec.Org != org {
				continue
			}
			st := ActorStatusRetired
			if active, ok := live[rec.ID]; ok { // 角色表還有 = 現役或暫停(暫停時 actor 也有 disabled_at);沒有 = 退役
				st = ActorStatusActive
				if !active {
					st = ActorStatusSuspended
				}
			}
			out = append(out, ActorListItem{ID: rec.ID, Kind: rec.Kind, Handle: rec.Handle, Name: rec.Name, Status: st})
		}
	}
	if want(model.ActorKindSystem) {
		recs, err := s.Store.ListActors(store.ActorFilter{Kind: model.ActorKindSystem})
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			out = append(out, ActorListItem{ID: rec.ID, Kind: rec.Kind, Handle: rec.Handle, Name: rec.Name, Status: ActorStatusActive})
		}
	}

	q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(f.Q), "@")))
	kept := out[:0]
	for _, it := range out {
		if f.Status == "" && it.Status == ActorStatusRetired || f.Status != "" && f.Status != "all" && it.Status != f.Status {
			continue
		}
		if q != "" && !strings.HasPrefix(strings.ToLower(it.Handle), q) && !strings.HasPrefix(strings.ToLower(it.Name), q) {
			continue
		}
		kept = append(kept, it)
	}
	rank := map[model.ActorKind]int{model.ActorKindUser: 0, model.ActorKindAgent: 1, model.ActorKindSystem: 2}
	sort.SliceStable(kept, func(i, j int) bool {
		if rank[kept[i].Kind] != rank[kept[j].Kind] {
			return rank[kept[i].Kind] < rank[kept[j].Kind]
		}
		return kept[i].Handle < kept[j].Handle
	})
	return kept, nil
}

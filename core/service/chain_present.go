package service

import "omixor-core/model"

// presentChain 組織鏈對外輸出:邊的端點由 actor id 換成相容的節點寫法(user:<email>、agent:<handle>;一次批次查名稱)。
// 角色與成員用 n 裡的資料就夠;其餘(已離開的人等)才查 actor 表
func (s *Service) presentChain(c model.OrgChain, n *chainNodes) (*model.OrgChain, error) {
	ids := make([]string, 0, len(c.Edges)*2)
	for _, e := range c.Edges {
		ids = append(ids, e.From, e.To)
	}
	names, err := s.LoadActors(ids...)
	if err != nil {
		return nil, err
	}
	out := model.OrgChain{Edges: make([]model.ChainEdge, 0, len(c.Edges)), UpdatedAt: c.UpdatedAt}
	for _, e := range c.Edges {
		out.Edges = append(out.Edges, model.ChainEdge{From: names.Legacy(e.From, model.LegacyNode), To: names.Legacy(e.To, model.LegacyNode), Label: e.Label,
			FromActor: names.View(e.From), ToActor: names.View(e.To)})
	}
	return &out, nil
}

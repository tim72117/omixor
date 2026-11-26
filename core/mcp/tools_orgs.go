package mcp

// RegisterOrgTools 登記組織工具:orgs
func RegisterOrgTools(s *Server) {
	s.Register("orgs", func(c *Call) (any, error) {
		e := env(c)
		e.Lock()
		defer e.Unlock()
		list, err := e.Backend().ListOrgs()
		if err != nil {
			return nil, terr(err)
		}
		// 統一入口沒有目前組織(Org() 是空字串):不標 current
		noCurrent := e.Org() == ""
		orgs := []any{}
		for _, o := range list {
			m := map[string]any{"id": o.ID, "name": o.Name, "note": o.Note}
			if !noCurrent {
				m["current"] = o.ID == e.Org()
			}
			orgs = append(orgs, m)
		}
		out := map[string]any{"orgs": orgs}
		if !noCurrent {
			out["current"] = e.Org()
		}
		if h := noOrgsHint(e.Backend(), list); h != "" {
			out["note"] = h
		}
		return out, nil
	})
}

package mcp

// RegisterOrgTools 登記組織工具:ops_orgs
func RegisterOrgTools(s *Server) {
	s.Register("ops_orgs", func(c *Call) (any, error) {
		e := env(c)
		e.Lock()
		defer e.Unlock()
		list, err := e.Backend().ListOrgs()
		if err != nil {
			return nil, ToolError{Message: err.Error()}
		}
		orgs := []any{}
		for _, o := range list {
			orgs = append(orgs, map[string]any{"id": o.ID, "name": o.Name, "note": o.Note, "current": o.ID == e.Org()})
		}
		return map[string]any{"current": e.Org(), "orgs": orgs}, nil
	})
}

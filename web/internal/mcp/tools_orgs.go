package mcp

func init() {
	Register("ops_orgs", func(c *Call) (any, error) {
		c.App.Mu.Lock()
		defer c.App.Mu.Unlock()
		idx := c.App.Orgs()
		orgs := []any{}
		for _, o := range idx.Orgs {
			orgs = append(orgs, map[string]any{"id": o.ID, "name": o.Name, "note": o.Note, "current": o.ID == idx.Current})
		}
		return map[string]any{"current": idx.Current, "orgs": orgs}, nil
	})
}

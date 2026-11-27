package mcp

import (
	"omixor-core/version"
)

// server:目前連線資訊與版本(唯讀,不含網址與密鑰)
func init() {
	Register("server", func(c *Call) (any, error) {
		out := c.App.ConnectionInfo()
		out["version"] = map[string]any{"web": version.Info()}
		return out, nil
	})
}

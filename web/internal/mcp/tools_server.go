package mcp

import (
	"time"

	"omixor-core/version"
	"omixor-web/internal/core"
)

// ops_server:目前連線資訊與版本(唯讀,不含網址與密鑰)。
// 版本:omixor web 自己的;遠端模式再查遠端 server 的 GET /api/version(逾時 3 秒,查不到就標示)。
func init() {
	Register("ops_server", func(c *Call) (any, error) {
		out := c.App.ConnectionInfo()
		ver := map[string]any{"web": version.Info()}
		if core.IsRemote() {
			if url := core.IssueRemoteURL(); url != "" {
				if d, err := version.Fetch(url, 3*time.Second); err == nil { // 錯誤文字含網址,不回給呼叫端
					ver["server"] = d
				} else {
					ver["server"] = map[string]any{"error": "查不到遠端 server 的版本(連不上、逾時或不支援)"}
				}
			}
		}
		out["version"] = ver
		return out, nil
	})
}

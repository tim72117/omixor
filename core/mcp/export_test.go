package mcp

// 測試要用很短的查詢間隔
func init() { channelTesting = true }

// 測試用的內部符號
var (
	MaskSecrets         = maskSecrets
	UnifiedInstructions = unifiedInstructions
)

package mcp

import "os"

// OPS_STRICT=1 時,還沒實作的工具會讓測試失敗(全部完成後改成預設嚴格)
func testingStrict() bool { return os.Getenv("OPS_STRICT") == "1" }

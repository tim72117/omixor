package mcp

import (
	"omixor-core/model"
	"omixor-web/internal/core"
)

// schema_check 由核心提供(omixor-core/mcp 的 tools_schema.go);這裡的 appBackend 只轉發給核心
// (內建核心),是否有權限由核心依登入身分決定(只有伺服器管理員)。
func (b appBackend) SchemaCheck() (*model.SchemaReport, error) {
	be, err := core.AgentBackend()
	if err != nil {
		return nil, err
	}
	return be.SchemaCheck()
}

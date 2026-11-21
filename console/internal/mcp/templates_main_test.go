package mcp

import (
	"os"
	"testing"

	"omixor-web/internal/core"
	"omixor-web/internal/core/coretest"

	"omixor-core/storetest"
)

// 預設不載入範本;這個套件的測試要用預設角色、文件櫃結構與示範資料,所以開著(templates/ 在 repo 最外層)
func TestMain(m *testing.M) {
	_ = os.Setenv("OMIXOR_TEMPLATES", "1")
	coretest.SkipIfNoPG()
	core.UseTestStores(coretest.NewStores().Get) // 沒有資料庫時用 PostgreSQL 的測試核心(每個 DataRoot 一份)
	storetest.Main(m)
}

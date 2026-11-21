package core

import (
	"os"
	"testing"

	"omixor-core/storetest"

	"omixor-web/internal/core/coretest"
)

// 預設不載入範本;這個套件的測試要用預設角色、文件櫃結構與示範資料,所以開著(templates/ 在 repo 最外層)
func TestMain(m *testing.M) {
	_ = os.Setenv("OMIXOR_TEMPLATES", "1")
	// 沒設資料夾的測試不要寫到使用者真正的資料夾(~/Library/Application Support/omixor-console)
	_ = os.Setenv("OMIXOR_CONSOLE_DATA_DIR", coretest.DataDir("omixor-console-core-test-"))
	coretest.SkipIfNoPG()
	UseTestStores(coretest.NewStores().Get) // 沒有資料庫時用 PostgreSQL 的測試核心(每個 DataRoot 一份)
	storetest.Main(m)
}

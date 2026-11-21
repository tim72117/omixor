package server

// 測試用的 PostgreSQL:統一走 core/storetest(每個測試一個獨立 database,已遷移完成,測完回收)。
//   - 有設 OMIXOR_TEST_DATABASE_URL → 用它(帳號要有 CREATEDB);
//   - 沒設 → 整個測試程序起「一個」內建 PostgreSQL(StartEmbeddedPG 的測試版由 storetest/testdb 管理),TestMain 結束時關掉;
//   - go test -short 或 OMIXOR_TEST_NO_EMBEDDED_PG=1 → 需要 PG 的測試略過。
// TestLocalEmbeddedPG、TestEmbeddedPGGuards 等內建 PG 產品測試自己用 StartEmbeddedPG 起暫存的,不經過這裡。

import (
	"testing"

	"omixor-core/storetest"
)

func TestMain(m *testing.M) { storetest.Main(m) }

// testDB 回傳一個只屬於這個測試的 database 的連線網址(已遷移,測完回收)
func testDB(t *testing.T) string {
	t.Helper()
	return storetest.URL(t)
}

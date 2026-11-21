// Package storetest 是「統一走 PostgreSQL」之後所有測試共用的 store 輔助(取代以前的 store.NewMem())。
// 只給測試 import,正式程式不要用。
//
// 用法:
//
//	func TestMain(m *testing.M) { storetest.Main(m) } // 每個套件一次;沒設網址要起內建 PG 時一定要有
//
//	func TestX(t *testing.T) {
//		s := storetest.New(t)              // 乾淨、隔離、遷移完成的 *store.PG;測完自動關連線、drop database
//		s := storetest.NewWithOrg(t, "o")  // 同上,另外建好組織 "o"(不給 id 就是 "o")
//	}
//
// 資料庫來源(細節見 storetest/testdb):
//   - 設 OMIXOR_TEST_DATABASE_URL=postgres://…(帳號要有 CREATEDB)→ 用它;每個測試一個獨立 database。
//   - 沒設 → 整個測試程序啟動「一個」內建 PostgreSQL 17(embedded-postgres,只聽 127.0.0.1、隨機埠),
//     不需要 Docker,第一次會下載二進位檔(之後有快取,~/.embedded-postgres-go)。
//   - go test -short 或 OMIXOR_TEST_NO_EMBEDDED_PG=1 → 需要 PG 的測試略過。
//
// 隔離與速度:每個程序先在範本 database 跑一次完整遷移,之後每次 New 用 create database … template … 複製,
// 再用 store.AttachMigratedPG 開 store(不重跑遷移)。實測單次 New 約 80ms(含複製、連線、關閉、drop);完整 NewPG 要多 ~200ms。
// 並行:可以 t.Parallel,各測試 database 互相獨立;背景外鍵 validate 在範本階段就做完,New 回傳時沒有背景工作。
//
// 注意:store 的連線池每個 store 限制最多 4 條(pool_max_conns=4),大量平行測試才不會打爆 max_connections;
// 需要更多連線的測試請自己用 URL 取網址再 store.NewPG。
// 沒有 Docker 的機器:什麼都不設直接 go test 即可(內建 PG);想用自己本機的 PG 就設 OMIXOR_TEST_DATABASE_URL。
package storetest

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/store"
	"omixor-core/storetest/testdb"
)

// Main 放在 TestMain:跑完測試後關掉內建 PG、清範本
func Main(m *testing.M) { testdb.Main(m) }

// buildTemplate 在範本 database 上跑完整遷移,等背景工作結束、關連線
func buildTemplate(url string) error {
	p, err := store.NewPG(url)
	if err != nil {
		return err
	}
	p.WaitFKValidate()
	p.Close()
	return nil
}

// URL 回傳一個已遷移完成的獨立 database 的連線網址(測完 drop);給要自己 NewPG、或把網址交給別的元件的測試
func URL(t testing.TB) string {
	t.Helper()
	return testdb.Template(t, "store", buildTemplate)
}

// URLWith 同 URL,但範本在 store 的結構之外再跑 extra(url)(例如 console 自己的表);key 區分不同的範本。
// extra 結束時要關掉自己開的連線;補的表必須是空的。
func URLWith(t testing.TB, key string, extra func(url string) error) string {
	t.Helper()
	return testdb.Template(t, "store+"+key, func(u string) error {
		if err := buildTemplate(u); err != nil {
			return err
		}
		return extra(u)
	})
}

// New 回傳一個乾淨、隔離、已遷移的 PG store;測完自動 Close 並 drop database
func New(t testing.TB) *store.PG {
	t.Helper()
	u := URL(t)
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	p, err := store.AttachMigratedPG(u + sep + "pool_max_conns=4")
	if err != nil {
		t.Fatal(err)
	}
	p.WaitFKValidate()
	t.Cleanup(p.Close) // 先於 testdb 的 drop 執行(Cleanup 後進先出)
	return p
}

// NewWithOrg 同 New,另外建好組織(id 不給就是 "o")
func NewWithOrg(t testing.TB, id ...string) *store.PG {
	t.Helper()
	org := "o"
	if len(id) > 0 {
		org = id[0]
	}
	p := New(t)
	if err := p.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	return p
}

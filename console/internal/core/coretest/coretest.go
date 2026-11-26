// Package coretest 給 console 各套件的測試:以 PostgreSQL 當後端,提供「每個資料夾 DataRoot 一份乾淨核心」。
// 只給測試 import,正式程式不要用;不 import core,所以 core 自己的測試也能用。
//
// 用法(各套件 TestMain,與 storetest.Main 並用;storetest.Main 會 os.Exit,所以它放最後):
//
//	func TestMain(m *testing.M) {
//		coretest.SkipIfNoPG()
//		core.UseTestStores(coretest.NewStores().Get)
//		storetest.Main(m)
//	}
//
// 生命週期:TestMain 沒有 *testing.T,所以每份 store 用一個「假的 testing.TB」向 storetest/testdb 要 database;
// 測試用 t.Setenv 換 DataRoot 之後第一次用核心時才建(一個 DataRoot 一個 database,測試語意與以前一個資料夾一份記憶體核心相同)。
// 同時最多保留 keep 份(最久沒用的先關、還原回收,避免上百個測試累積上百個連線池與 database);程序結束時 storetest.Main 會清掉 database。
package coretest

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/store"
	"omixor-core/storetest"
	"omixor-core/storetest/testdb"

	"omixor-web/internal/templates"
)

const keep = 6

// SkipIfNoPG 沒有可用的 PostgreSQL(-short,或 OMIXOR_TEST_NO_EMBEDDED_PG=1 且沒設 OMIXOR_TEST_DATABASE_URL)時整個套件略過、exit 0。
// 設了 OMIXOR_TEST_REQUIRE_PG=1(CI)時略過改成失敗(exit 1,並說明原因),不讓「沒跑」顯示成 ok。
func SkipIfNoPG() {
	if os.Getenv("OMIXOR_TEST_DATABASE_URL") != "" || os.Getenv("OPS_TEST_DATABASE_URL") != "" {
		return
	}
	if !flag.Parsed() {
		flag.Parse() // TestMain 裡 testing.Short() 要先 Parse
	}
	if testing.Short() || os.Getenv("OMIXOR_TEST_NO_EMBEDDED_PG") == "1" {
		const why = "沒設 OMIXOR_TEST_DATABASE_URL,且 -short 或 OMIXOR_TEST_NO_EMBEDDED_PG=1(不起內建 PostgreSQL)"
		if testdb.RequirePG() {
			fmt.Fprintln(os.Stderr, "失敗:"+why+",但 "+testdb.RequirePGEnv+"=1 要求一定要跑 PostgreSQL 測試")
			os.Exit(1)
		}
		fmt.Println("略過:" + why)
		os.Exit(0)
	}
}

// DataDir 建一個測試用的資料夾(給 OMIXOR_CONSOLE_DATA_DIR 的預設值,不要寫到使用者真正的資料夾)。
// 會先做 SkipIfNoPG 的檢查:要略過整個套件時不建資料夾(不留殘渣)。
// storetest.Main 會自己 os.Exit,TestMain 沒機會在 m.Run 之後刪資料夾,所以改成每次建立時順便清掉 1 小時前留下的同名前綴資料夾(見 sweepOld)。
func DataDir(prefix string) string {
	SkipIfNoPG()
	sweepOld(os.TempDir(), prefix, time.Hour, time.Now())
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		panic(err)
	}
	return dir
}

// sweepOld 刪掉 tmp 底下名稱以 prefix 開頭、修改時間早於 now-age 的資料夾;回傳刪掉幾個
func sweepOld(tmp, prefix string, age time.Duration, now time.Time) int {
	old, _ := filepath.Glob(filepath.Join(tmp, prefix+"*"))
	n := 0
	for _, d := range old {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() && now.Sub(fi.ModTime()) > age {
			if os.RemoveAll(d) == nil {
				n++
			}
		}
	}
	return n
}

// fakeTB 讓 storetest.New 可以在沒有 *testing.T 的地方用:Cleanup 先存起來,release 時自己倒著執行
type fakeTB struct {
	testing.TB // 沒實作的方法不會被呼叫(storetest/testdb 只用下面這幾個)
	name       string
	cleanups   []func()
}

func (f *fakeTB) Helper()           {}
func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) Name() string      { return f.name }
func (f *fakeTB) Skip(args ...any) {
	panic("coretest:沒有可用的 PostgreSQL:" + fmt.Sprint(args...))
}
func (f *fakeTB) Fatal(args ...any) { panic("coretest:" + fmt.Sprint(args...)) }
func (f *fakeTB) Fatalf(format string, args ...any) {
	panic("coretest:" + fmt.Sprintf(format, args...))
}
func (f *fakeTB) Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "coretest:"+format+"\n", args...)
}
func (f *fakeTB) Logf(format string, args ...any) {}
func (f *fakeTB) Log(args ...any)                 {}
func (f *fakeTB) release() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	f.cleanups = nil
}

type entry struct {
	key string
	tb  *fakeTB
	pg  *store.PG
}

// Stores 一個 key(DataRoot)一份 PostgreSQL store
type Stores struct {
	mu      sync.Mutex
	entries []*entry // 最近用的在最後
	empty   bool     // 新的 store 不放任何組織
}

// NewStores 每份新的 store 先放一個 default 組織(名稱照範本的 org.json):多數測試要「已經在用的安裝」
func NewStores() *Stores { return &Stores{} }

// NewEmptyStores 每份新的 store 是完全空的(測「還沒有任何組織」)
func NewEmptyStores() *Stores { return &Stores{empty: true} }

func seedOrg(pg *store.PG) {
	name, symbol, color := templates.Org()
	if err := pg.CreateOrg(model.Org{ID: "default", Name: name, Symbol: symbol, Color: color, CreatedAt: model.Now()}); err != nil {
		panic("coretest:建立測試組織:" + err.Error())
	}
}

// Get 回傳 key 對應的 store(第一次用才建;之後同一個 key 拿到同一份)。
// 測試程序已經在結束(testdb.Closing)就回傳 nil:測試留下的背景 goroutine 不要再去開新的資料庫
func (s *Stores) Get(key string) *store.PG {
	s.mu.Lock()
	defer s.mu.Unlock()
	if testdb.Closing() {
		return nil
	}
	for i, e := range s.entries {
		if e.key == key {
			s.entries = append(append(s.entries[:i:i], s.entries[i+1:]...), e)
			return e.pg
		}
	}
	for len(s.entries) >= keep {
		s.entries[0].tb.release()
		s.entries = s.entries[1:]
	}
	tb := &fakeTB{name: "coretest-" + strings.ReplaceAll(key, "\\", "/")}
	e := &entry{key: key, tb: tb, pg: storetest.New(tb)}
	if !s.empty {
		seedOrg(e.pg)
	}
	s.entries = append(s.entries, e)
	return e.pg
}

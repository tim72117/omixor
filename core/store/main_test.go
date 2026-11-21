package store

// 測試用的 PostgreSQL 由 storetest/testdb 供應(OMIXOR_TEST_DATABASE_URL 或內建 PG;說明見該套件)。
// core/store 自己的測試是 package store,不能 import storetest(它 import store,會循環),所以用底層的 testdb。

import (
	"sync"
	"testing"

	"omixor-core/storetest/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// testPG 回傳一個乾淨、已遷移的 PG store(獨立 database,測完自動清掉)
func testPG(t *testing.T) Store {
	t.Helper()
	url := testdb.Template(t, "store", func(url string) error {
		p, err := NewPG(url)
		if err != nil {
			return err
		}
		p.WaitFKValidate()
		p.Close()
		return nil
	})
	p, err := AttachMigratedPG(url)
	if err != nil {
		t.Fatal(err)
	}
	p.WaitFKValidate()
	t.Cleanup(p.Close)
	pgURLs.Store(p, url)
	return p
}

// pgURLs 每個 testPG 開出來的 store 對應的資料庫網址(reopen 要用)
var pgURLs sync.Map // *PG → string

// reopenPG 關掉 store 再用 NewPG 重開同一個資料庫(像程序重啟:重跑建表與遷移),讓「關掉重開仍讀得回來」真的被測到
func reopenPG(t *testing.T, s Store) Store {
	t.Helper()
	p, ok := s.(*PG)
	if !ok {
		t.Fatalf("不是 *PG:%T", s)
	}
	v, ok := pgURLs.Load(p)
	if !ok {
		t.Fatal("這個 store 不是 testPG 開的")
	}
	p.Close()
	n, err := NewPG(v.(string))
	if err != nil {
		t.Fatal(err)
	}
	n.WaitFKValidate()
	t.Cleanup(n.Close) // 先於 testdb 的還原回收執行
	pgURLs.Store(n, v)
	return n
}

// testPGBlank 回傳一個全新、空的 database 的網址(測完自動清掉);可以對它重複 NewPG(模擬重啟、測遷移)
func testPGBlank(t *testing.T) string {
	t.Helper()
	return testdb.Blank(t)
}

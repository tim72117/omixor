package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/store"
)

// 實際走一遍 omixor-local 的內建 PostgreSQL:沒給 DatabaseURL → 在資料夾底下起 PostgreSQL,寫入、關閉、
// 重開資料還在、pgdata 與 pgpassword 在資料夾裡;孤兒程序(沒關乾淨)重開也要起得來。
// 第一次要下載二進位檔(之後用快取),所以 -short 與 OMIXOR_TEST_NO_EMBEDDED_PG=1 略過
func TestLocalEmbeddedPG(t *testing.T) {
	if testing.Short() || os.Getenv("OMIXOR_TEST_NO_EMBEDDED_PG") == "1" {
		t.Skip("不起內建 PostgreSQL")
	}
	dir := t.TempDir()
	start := time.Now()
	l, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok, User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("第一次啟動 %s", time.Since(start))
	if l.embedded == nil {
		t.Fatal("沒給 DatabaseURL 應該啟動內建的")
	}
	for _, n := range []string{"pgdata", "pgpassword"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatal(n, err)
		}
	}
	if err := l.Store.CreateOrg(model.Org{ID: "acme", Name: "Acme", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	// 批次:失敗整批還原
	err = l.Store.Batch(func(tx *store.PG) error {
		if err := tx.CreateOrg(model.Org{ID: "gone", Name: "gone", CreatedAt: model.Now()}); err != nil {
			return err
		}
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("批次應該失敗")
	}
	if _, err := l.Store.GetOrg("gone"); err == nil {
		t.Fatal("失敗的批次不該留下組織")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pgdata", "postmaster.pid")); err == nil {
		t.Fatal("乾淨關閉後不該留下 postmaster.pid")
	}
	if err := l.Close(); err != nil { // 重複關閉沒事
		t.Fatal(err)
	}

	// 重開:資料還在
	start = time.Now()
	l2, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok + "2", User: "tim"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("重開 %s", time.Since(start))
	if o, err := l2.Store.GetOrg("acme"); err != nil || o.Name != "Acme" {
		t.Fatal("重開後資料不見", o, err)
	}

	// 沒關乾淨(模擬程式被砍掉):連線池丟掉、不呼叫 Stop,資料目錄留著 postmaster.pid、postgres 還活著
	l2.Store.Close()
	l2.embedded.db = nil // 當作這個程式死掉了:不再管那個 postgres
	l3, err := OpenLocal(LocalConfig{DataDir: dir, Token: testTok + "3", User: "tim"})
	if err != nil {
		t.Fatal("孤兒 postgres 之後重開應該要起得來:", err)
	}
	if o, err := l3.Store.GetOrg("acme"); err != nil || o.Name != "Acme" {
		t.Fatal("孤兒之後重開資料不見", o, err)
	}
	if err := l3.Close(); err != nil {
		t.Fatal(err)
	}
}

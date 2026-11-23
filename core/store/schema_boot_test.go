package store

import (
	"testing"
)

// 空資料庫:建出最終結構(含固定的系統 actor)、再啟動一次不動任何東西
func TestBootstrapEmptyDatabase(t *testing.T) {
	url := testPGBlank(t)
	p, err := NewPG(url)
	chk(t, err)
	if n := pgCount(t, p, `select count(*) from omixor_actors where kind = 'system'`); n == 0 {
		t.Fatal("要有固定的系統 actor")
	}
	p.Close()
	p2, err := NewPG(url) // 第二次:已經是最終結構,通過
	chk(t, err)
	p2.Close()
}

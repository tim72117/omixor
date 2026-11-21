package store

import "testing"

// whoID 測試用:舊字串(email、agent:<id>、系統署名…)→ actor id(寬鬆解析器;任務的建立者、領取者、備註作者欄位存的是 actor id)
func whoID(t testing.TB, s Store, org, raw string) string {
	t.Helper()
	id, err := s.ResolveLegacy(org, raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

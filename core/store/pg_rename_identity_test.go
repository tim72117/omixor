package store

import "testing"

// RenameIdentity 在「誰」欄位改存 actor id 之後不改任何資料(改名是改 actor 的 handle),只剩參數檢查(console 的 migrateLocalIdentity 還在呼叫)
func TestPGRenameIdentityIsNoOp(t *testing.T) {
	p := fkPG(t, testPGBlank(t))
	if _, err := p.RenameIdentity("", "x"); err == nil {
		t.Fatal("空白名稱要回錯誤")
	}
	if _, err := p.RenameIdentity("a", "a"); err == nil {
		t.Fatal("相同名稱要回錯誤")
	}
	n, err := p.RenameIdentity("ops-web", "omixor-console")
	if err != nil || n != 0 {
		t.Fatal("不改任何資料", n, err)
	}
}

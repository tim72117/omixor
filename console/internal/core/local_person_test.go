package core

import "testing"

// 全新安裝(沒有範例資料人員):本機使用者是 LocalIdentity;舊寫法 tim 仍視為同一人
func TestLocalPersonDefaults(t *testing.T) {
	t.Setenv("OMIXOR_TEMPLATES", "0")
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	a := &App{Me: LocalIdentity, C: map[string][]map[string]any{}}
	// 不顯示主機作業系統的帳號名
	if n := a.MeName(); n != localUnknownName || a.Name(Person(a.Me)) != LocalIdentity {
		t.Fatalf("顯示名稱:%q", n)
	}
	if !samePerson("tim", LocalIdentity) || !samePerson("bob", "bob") || samePerson("tim", "bob") {
		t.Fatal("samePerson")
	}
}

// 沒有存檔:空清單且不寫檔
func TestNoDefaultConnections(t *testing.T) {
	a := connTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if l := a.conns().List; l == nil || len(l) != 0 {
		t.Fatalf("連線:%v", l)
	}
	if _, ok := readConnections(a.connFile()); ok {
		t.Fatal("讀取時不該寫檔")
	}
}

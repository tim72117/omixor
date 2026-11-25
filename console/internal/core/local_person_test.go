package core

import "testing"

// 全新安裝(沒有範例資料人員):本機使用者是 LocalIdentity;舊寫法 tim 仍視為同一人
func TestLocalPersonDefaults(t *testing.T) {
	t.Setenv("OMIXOR_TEMPLATES", "0")
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	a := &App{Me: LocalIdentity, C: map[string][]map[string]any{}}
	if n := a.MeName(); n != localDisplayName() || n == "" || a.Name(Person(a.Me)) != LocalIdentity {
		t.Fatalf("顯示名稱:%q", n)
	}
	if !samePerson("tim", LocalIdentity) || !samePerson("bob", "bob") || samePerson("tim", "bob") {
		t.Fatal("samePerson")
	}
	for _, c := range [][3]string{{"", `CORP\amy`, "amy"}, {"Amy Lin", "amy", "Amy Lin"}, {"", "root", localUnknownName}, {"", "", localUnknownName}} {
		if got := cleanLocalName(c[0], c[1]); got != c[2] {
			t.Errorf("cleanLocalName(%q, %q) = %q", c[0], c[1], got)
		}
	}
}

// 沒有存檔:空清單且不寫檔;EvoleapLogin 用的臨時設定不依賴清單
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
	c := evoleapIdentity("https://id.example/")
	if c.BaseURL != "https://id.example" || c.Auth != AuthApproval || c.Approval.StartPath == "" || c.Approval.LogoutPath == "" {
		t.Fatalf("%+v", c)
	}
}

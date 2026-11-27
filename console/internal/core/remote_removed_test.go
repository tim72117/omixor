package core

import (
	"os"
	"strings"
	"testing"
)

// 移除遠端模式後的升級防呆:以前是遠端模式的安裝要拒絕啟動,leave-remote 之後才行;原本的紀錄不刪伺服器網址
func TestCheckRemovedRemoteMode(t *testing.T) {
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(legacyModePath(), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, mode, env, want string // want 空 = 可以啟動
	}{
		{"全新安裝", "", "", ""},
		{"本機模式", `{"mode":"local"}`, "", ""},
		{"本機模式而環境有 OMIXOR_SERVER(照舊是本機)", `{"mode":"local"}`, "https://a.example.com", ""},
		{"mode.json 是遠端", `{"mode":"remote","server":"https://user:pw@r.example.com"}`, "", "https://r.example.com"},
		{"只設了 OMIXOR_SERVER", "", "https://e.example.com", "OMIXOR_SERVER"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
			t.Setenv("OMIXOR_SERVER", c.env)
			t.Setenv("OPS_ISSUE_SERVER", "")
			if c.mode != "" {
				write(c.mode)
			}
			err := CheckRemovedRemoteMode()
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "leave-remote") {
				t.Fatalf("要拒絕啟動並提到 %s:%v", c.want, err)
			}
			if strings.Contains(err.Error(), "user:pw") {
				t.Fatal("錯誤訊息不能帶網址裡的帳密", err)
			}
			for _, s := range []string{"退回本機模式", "不刪任何資料"} { // 兩種可能都要講
				if !strings.Contains(err.Error(), s) {
					t.Fatalf("錯誤訊息要提到 %s:%v", s, err)
				}
			}
			if err := LeaveRemoteMode(); err != nil {
				t.Fatal(err)
			}
			if err := CheckRemovedRemoteMode(); err != nil {
				t.Fatal("leave-remote 之後要可以啟動:", err)
			}
			if m, err := readLegacyMode(); err != nil || m.Mode != legacyModeLocal || m.Server == "" {
				t.Fatalf("要記成 local 並保留伺服器網址:%+v %v", m, err)
			}
		})
	}
}

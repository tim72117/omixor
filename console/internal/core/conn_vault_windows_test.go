//go:build windows

package core

import "testing"

func TestDPAPIVault(t *testing.T) {
	v := &DPAPIVault{Dir: t.TempDir()}
	if _, ok := v.Get("org/x:token"); ok {
		t.Fatal("空的應該讀不到")
	}
	if err := v.Set("org/x:token", []byte("secret-value")); err != nil {
		t.Fatal(err)
	}
	got, ok := v.Get("org/x:token")
	if !ok || string(got) != "secret-value" {
		t.Fatalf("讀回 %q %v", got, ok)
	}
	if err := v.Set("org/x:token", []byte("")); err != nil {
		t.Fatal(err)
	}
	if got, ok := v.Get("org/x:token"); !ok || len(got) != 0 {
		t.Fatalf("空值讀回 %q %v", got, ok)
	}
	if err := v.Delete("org/x:token"); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete("org/x:token"); err != nil {
		t.Fatal("刪不存在的不該錯:", err)
	}
	if _, ok := v.Get("org/x:token"); ok {
		t.Fatal("刪掉後還讀得到")
	}
}

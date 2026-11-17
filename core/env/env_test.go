package env

import "testing"

const oldName = "OPS" + "_ENVTEST_X" // 拆開寫,避免全域改名時被換掉

func TestGetPrefersNewName(t *testing.T) {
	t.Setenv("OMIXOR_ENVTEST_X", "")
	t.Setenv(oldName, "")
	if v, ok := Lookup("OMIXOR_ENVTEST_X", oldName); ok || v != "" {
		t.Fatal("都沒設應該是空的", v, ok)
	}
	t.Setenv(oldName, "old")
	if v := Get("OMIXOR_ENVTEST_X", oldName); v != "old" {
		t.Fatal("新名稱沒設時要讀舊名稱", v)
	}
	t.Setenv("OMIXOR_ENVTEST_X", "new")
	if v := Get("OMIXOR_ENVTEST_X", oldName); v != "new" {
		t.Fatal("新名稱優先", v)
	}
	if v := Get("OMIXOR_ENVTEST_X"); v != "new" {
		t.Fatal("沒有舊名稱也能讀", v)
	}
}

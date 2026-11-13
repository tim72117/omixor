package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// 組織的系統設定:Mem 讀寫(深拷貝)、File 存檔重開讀得回來、刪組織一併刪除
func TestSystemSettingsMemAndFile(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := mt("2026-11-13T01:02:03Z")
	if err := f.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetSystemSettings("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("沒存過回 ErrNotFound", err)
	}
	s := model.OrgSystemSettings{Features: map[string]bool{"development.board": true, "channels": false}, UpdatedAt: &at, UpdatedBy: "boss@x"}
	if err := f.SaveSystemSettings("o", s); err != nil {
		t.Fatal(err)
	}
	s.Features["cabinet"] = false // 存進去之後改原本的不影響
	x, _ := f.GetSystemSettings("o")
	x.Features["zzz"] = true // 拿回去改不影響存著的
	f2, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f2.GetSystemSettings("o")
	if err != nil || !g.Saved || len(g.Features) != 2 || !g.Features["development.board"] || g.Features["channels"] || g.UpdatedBy != "boss@x" || !g.UpdatedAt.Equal(at.Time) {
		t.Fatalf("重開讀回:%+v %v", g, err)
	}
	if err := f2.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.GetSystemSettings("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪組織一併刪除", err)
	}
}

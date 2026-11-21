package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// 組織的系統設定:讀寫(深拷貝)、刪組織一併刪除(一致性測試的一項,見 conformance_test.go)
func suiteSystemSettings(t *testing.T, impl storeImpl) {
	f := impl.open(t)
	at := mt("2026-11-13T01:02:03Z")
	if err := f.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetSystemSettings("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("沒存過回 ErrNotFound", err)
	}
	s := model.OrgSystemSettings{Features: map[string]bool{"development.board": true, "channels": false}, UpdatedAt: &at, UpdatedBy: emailActor(t, f, "boss@x")}
	if err := f.SaveSystemSettings("o", s); err != nil {
		t.Fatal(err)
	}
	s.Features["cabinet"] = false // 存進去之後改原本的不影響
	x, _ := f.GetSystemSettings("o")
	x.Features["zzz"] = true // 拿回去改不影響存著的
	f2 := impl.reopen(t, f)
	g, err := f2.GetSystemSettings("o")
	if err != nil || !g.Saved || len(g.Features) != 2 || !g.Features["development.board"] || g.Features["channels"] || g.UpdatedBy != emailActor(t, f2, "boss@x") || !g.UpdatedAt.Equal(at.Time) {
		t.Fatalf("重開讀回:%+v %v", g, err)
	}
	// nil 的開關讀回是 {};整份取代
	if err := f2.SaveSystemSettings("o", model.OrgSystemSettings{UpdatedAt: &at}); err != nil {
		t.Fatal(err)
	}
	g, _ = f2.GetSystemSettings("o")
	sameJSON(t, "空的設定", g, model.OrgSystemSettings{Features: map[string]bool{}, Saved: true, UpdatedAt: &at})
	if _, err := f2.GetSystemSettings("p"); !errors.Is(err, ErrNotFound) {
		t.Fatal("別的組織", err)
	}
	if err := f2.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	f2 = impl.reopen(t, f2)
	if _, err := f2.GetSystemSettings("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪組織一併刪除", err)
	}
}

// 系統層的功能設定(PG 是 omixor_server_features 表):讀寫(深拷貝)、刪組織不影響
// (一致性測試的一項,見 conformance_test.go)
func suiteServerFeatures(t *testing.T, impl storeImpl) {
	f := impl.open(t)
	if _, err := f.GetServerFeatures(); !errors.Is(err, ErrNotFound) {
		t.Fatal("沒存過回 ErrNotFound", err)
	}
	at := mt("2026-11-13T01:02:03Z")
	s := model.ServerFeatures{Features: map[string]bool{"familyAssistant": true, "channels": false}, UpdatedAt: &at, UpdatedBy: emailActor(t, f, "root@x")}
	if err := f.SaveServerFeatures(s); err != nil {
		t.Fatal(err)
	}
	s.Features["cabinet"] = false
	x, _ := f.GetServerFeatures()
	x.Features["zzz"] = true
	_ = f.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at})
	_ = f.DeleteOrg("o")
	f2 := impl.reopen(t, f)
	g, err := f2.GetServerFeatures()
	if err != nil || !g.Saved || len(g.Features) != 2 || !g.Features["familyAssistant"] || g.Features["channels"] || g.UpdatedBy != emailActor(t, f2, "root@x") || !g.UpdatedAt.Equal(at.Time) {
		t.Fatalf("重開讀回:%+v %v", g, err)
	}
	// 整份取代(只有一份)
	at2 := mt("2026-11-14T00:00:00Z")
	if err := f2.SaveServerFeatures(model.ServerFeatures{Features: map[string]bool{"cabinet": true}, UpdatedAt: &at2}); err != nil {
		t.Fatal(err)
	}
	f2 = impl.reopen(t, f2)
	g, _ = f2.GetServerFeatures()
	sameJSON(t, "整份取代", g, model.ServerFeatures{Features: map[string]bool{"cabinet": true}, Saved: true, UpdatedAt: &at2})
}

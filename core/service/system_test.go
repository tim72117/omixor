package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

func featureState(v *service.SystemSettingsView, key string) service.FeatureState {
	for _, f := range v.Features {
		if f.Key == key {
			return f
		}
	}
	return service.FeatureState{}
}

// 系統設定:成員讀(附完整清單)、管理員改;沒登記的 key、關必要功能 400;reset 回預設
func TestSystemSettings(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	v, err := svc.GetSystemSettings(alice, "o1")
	if err != nil || v.Saved || v.CanEdit || len(v.Features) != len(model.AvailableFeatures()) || v.Edition != model.BuildEdition {
		t.Fatalf("沒存過全部預設(只列這次編譯有的):%+v %v", v, err)
	}
	if model.BuildEdition != model.EditionDev {
		testSystemStable(t, svc, boss, v)
		return
	}
	if len(v.Features) != len(model.Features) {
		t.Fatal("開發版列全部")
	}
	if f := featureState(v, "development.board"); !f.Experimental || f.Enabled || f.On || f.ExperimentalNote == "" || f.Edition != model.EditionDev {
		t.Fatalf("實驗功能預設關:%+v", f)
	}
	if f := featureState(v, "development.issues"); !f.Enabled || f.Experimental || f.Edition != model.EditionStable {
		t.Fatalf("已實作的預設開:%+v", f)
	}
	if b, _ := svc.GetSystemSettings(boss, "o1"); !b.CanEdit {
		t.Fatal("管理員 canEdit")
	}
	if _, err := svc.GetSystemSettings(&model.Principal{User: model.User{ID: "nobody"}}, "o1"); code(err) != 403 {
		t.Fatal("不是成員不能讀", err)
	}
	if _, err := svc.SaveSystemSettings(alice, "o1", service.SystemSettingsInput{Features: map[string]bool{"development.board": true}}); code(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"nope": true}}); code(err) != 400 {
		t.Fatal("沒登記的 key 400", err)
	}
	if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"settings": false}}); code(err) != 400 {
		t.Fatal("必要功能不能關", err)
	}
	if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{}); code(err) != 400 {
		t.Fatal("沒東西改 400", err)
	}
	v, err = svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"development.board": true, "channels": false, "system": true}})
	if err != nil || !v.Saved || v.UpdatedBy != "boss@x" || v.UpdatedAt == nil {
		t.Fatalf("%+v %v", v, err)
	}
	if f := featureState(v, "development.board"); !f.Enabled || !f.Changed {
		t.Fatalf("%+v", f)
	}
	if f := featureState(v, "system"); !f.Enabled || f.Changed {
		t.Fatalf("必要的不存:%+v", f)
	}
	v, _ = svc.GetSystemSettings(alice, "o1")
	if featureState(v, "channels").Enabled || !featureState(v, "development.board").Enabled || !v.Saved {
		t.Fatalf("成員讀到改過的:%+v", v)
	}
	// 只給有變的:其他的不動
	v, _ = svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"cabinet": false}})
	if featureState(v, "channels").Enabled || featureState(v, "cabinet").Enabled || !featureState(v, "development.board").Enabled {
		t.Fatalf("%+v", v)
	}
	// reset 回預設
	v, err = svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Reset: []string{"channels", "development.board"}})
	if err != nil || !featureState(v, "channels").Enabled || featureState(v, "development.board").Enabled || featureState(v, "channels").Changed {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Reset: []string{"nope"}}); code(err) != 400 {
		t.Fatal(err)
	}
}

// 正式版:開發版的功能不列、不能開也不能 reset(400);存著的舊值保留但不生效,換成開發版時照舊
func testSystemStable(t *testing.T, svc *service.Service, boss *model.Principal, v *service.SystemSettingsView) {
	t.Helper()
	for _, f := range v.Features {
		if f.Edition != model.EditionStable || f.Experimental {
			t.Fatalf("正式版只列正式版的功能:%+v", f)
		}
	}
	if featureState(v, "familyAssistant").Key != "" || featureState(v, "development.board").Key != "" {
		t.Fatal("開發版的功能不列")
	}
	for _, in := range []service.SystemSettingsInput{
		{Features: map[string]bool{"familyAssistant": true}},
		{Features: map[string]bool{"development.board": true, "channels": false}},
		{Reset: []string{"familyAssistant"}},
	} {
		if _, err := svc.SaveSystemSettings(boss, "o1", in); code(err) != 400 || !strings.Contains(err.Error(), "正式版") {
			t.Fatal("正式版沒有的功能 400 並說明", in, err)
		}
	}
	// 舊值(例如之前用開發版開過):直接寫進 store,讀的時候不列也不生效,改別的開關時保留
	st := svc.Store
	_ = st.SaveSystemSettings("o1", model.OrgSystemSettings{Features: map[string]bool{"familyAssistant": true, "development.board": true}})
	v, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"channels": false}})
	if err != nil || featureState(v, "familyAssistant").Key != "" || featureState(v, "channels").Enabled {
		t.Fatal(v, err)
	}
	x, _ := st.GetSystemSettings("o1")
	if !x.Features["familyAssistant"] || !x.Features["development.board"] || x.Features["channels"] {
		t.Fatalf("舊值保留:%+v", x.Features)
	}
	if x.Enabled("familyAssistant") || x.Enabled("development.board") {
		t.Fatal("舊值不生效")
	}
}

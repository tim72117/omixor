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

// rootP 伺服器管理員(測試用)
var rootP = &model.Principal{User: model.User{ID: "root", Email: "root@x", Role: "admin"}}

// serverOn 系統層開放這些功能(伺服器管理員)
func serverOn(t *testing.T, svc *service.Service, keys ...string) {
	t.Helper()
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	if _, err := svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Features: m}); err != nil {
		t.Fatal(err)
	}
}

func serverState(v *service.ServerFeaturesView, key string) service.ServerFeatureState {
	for _, f := range v.Features {
		if f.Key == key {
			return f
		}
	}
	return service.ServerFeatureState{}
}

// 系統層:任何登入者讀(角色憑證不行)、只有伺服器管理員改(組織管理員、成員不行);預設照 Edition;必要的不能關;reset
func TestServerFeatures(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	_ = st.SaveUser(model.User{ID: "root", Email: "root@x", Role: "admin", CreatedAt: model.Now()}) // 署名要對得到 actor
	v, err := svc.GetServerFeatures(alice)
	if err != nil || v.Saved || v.CanEdit || len(v.Features) != len(model.Features) {
		t.Fatalf("沒存過全部預設、列全部功能:%+v %v", v, err)
	}
	if f := serverState(v, "familyAssistant"); f.On || f.Available || f.SystemDefault || f.Changed {
		t.Fatalf("原本只有開發版的預設不開放:%+v", f)
	}
	if f := serverState(v, "development.issues"); !f.On || !f.Available || !f.SystemDefault {
		t.Fatalf("原本正式版的預設開放:%+v", f)
	}
	if x, _ := svc.GetServerFeatures(rootP); !x.CanEdit {
		t.Fatal("伺服器管理員 canEdit")
	}
	if x, _ := svc.GetServerFeatures(boss); x.CanEdit {
		t.Fatal("組織管理員不能改系統層")
	}
	agent := &model.Principal{User: model.User{ID: "root", Role: "admin"}, Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto"}}
	if _, err := svc.GetServerFeatures(agent); code(err) != 403 {
		t.Fatal("角色憑證不能讀", err)
	}
	in := service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}
	for _, p := range []*model.Principal{boss, alice, agent} {
		if _, err := svc.SaveServerFeatures(p, in); code(err) != 403 {
			t.Fatal("只有伺服器管理員能改", p.User.Email, err)
		}
	}
	if _, err := svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Features: map[string]bool{"nope": true}}); code(err) != 400 {
		t.Fatal("沒登記的 key 400", err)
	}
	if _, err := svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Features: map[string]bool{"system": false}}); code(err) != 400 {
		t.Fatal("必要功能不能關", err)
	}
	if _, err := svc.SaveServerFeatures(rootP, service.SystemSettingsInput{}); code(err) != 400 {
		t.Fatal("沒東西改 400", err)
	}
	v, err = svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true, "channels": false, "settings": true}})
	if err != nil || !v.Saved || v.UpdatedBy != "root@x" || v.UpdatedAt == nil {
		t.Fatalf("%+v %v", v, err)
	}
	if f := serverState(v, "familyAssistant"); !f.On || !f.Available || !f.Changed {
		t.Fatalf("%+v", f)
	}
	if f := serverState(v, "settings"); !f.On || f.Changed {
		t.Fatalf("必要的不存:%+v", f)
	}
	// 組織層:系統層關掉的不列、serverFeatures 標 false;系統層開放的列出(組織層預設照 Default)
	o, _ := svc.GetSystemSettings(alice, "o1")
	if featureState(o, "channels").Key != "" || o.ServerFeatures["channels"] || !o.ServerFeatures["familyAssistant"] {
		t.Fatalf("系統層關掉的不列:%+v", o.ServerFeatures)
	}
	if f := featureState(o, "familyAssistant"); f.Key == "" || !f.System || f.Enabled || f.On {
		t.Fatalf("系統層開放、組織層預設關:%+v", f)
	}
	if o.Edition != model.EditionDev {
		t.Fatal("相容的 edition:開放了原本開發版的功能是 dev", o.Edition)
	}
	// reset 回預設
	v, _ = svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Reset: []string{"channels", "familyAssistant"}})
	if f := serverState(v, "channels"); !f.On || f.Changed || serverState(v, "familyAssistant").On {
		t.Fatalf("%+v", v)
	}
	if _, err := svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Reset: []string{"nope"}}); code(err) != 400 {
		t.Fatal(err)
	}
}

// 系統設定(組織層):成員讀(附系統層開放的清單)、管理員改;沒登記的 key、關必要功能 400;reset 回預設
func TestSystemSettings(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	v, err := svc.GetSystemSettings(alice, "o1")
	def := model.ServerFeatures{}
	if err != nil || v.Saved || v.CanEdit || len(v.Features) != len(def.Available()) || v.Edition != model.EditionStable || len(v.ServerFeatures) != len(model.Features) {
		t.Fatalf("沒存過全部預設(只列系統層開放的):%+v %v", v, err)
	}
	testSystemServerOff(t, svc, boss, v)
	// 系統層全部開放後:和以前的開發版一樣
	all := []string{}
	for _, f := range model.Features {
		all = append(all, f.Key)
	}
	serverOn(t, svc, all...)
	v, _ = svc.GetSystemSettings(alice, "o1")
	if len(v.Features) != len(model.Features) {
		t.Fatal("系統層全開放時列全部")
	}
	_ = svc.Store.SaveSystemSettings("o1", model.OrgSystemSettings{Features: map[string]bool{}})
	v, _ = svc.GetSystemSettings(alice, "o1")
	if f := featureState(v, "development.overview"); !f.Experimental || f.Enabled || f.On || f.ExperimentalNote == "" || f.Edition != model.EditionDev || !f.System {
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
	if _, err := svc.SaveSystemSettings(alice, "o1", service.SystemSettingsInput{Features: map[string]bool{"development.overview": true}}); code(err) != 403 {
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
	v, err = svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"development.overview": true, "channels": false, "system": true}})
	if err != nil || !v.Saved || v.UpdatedBy != "boss@x" || v.UpdatedAt == nil {
		t.Fatalf("%+v %v", v, err)
	}
	if f := featureState(v, "development.overview"); !f.Enabled || !f.Changed {
		t.Fatalf("%+v", f)
	}
	if f := featureState(v, "system"); !f.Enabled || f.Changed {
		t.Fatalf("必要的不存:%+v", f)
	}
	v, _ = svc.GetSystemSettings(alice, "o1")
	if featureState(v, "channels").Enabled || !featureState(v, "development.overview").Enabled || !v.Saved {
		t.Fatalf("成員讀到改過的:%+v", v)
	}
	// 只給有變的:其他的不動
	v, _ = svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"cabinet": false}})
	if featureState(v, "channels").Enabled || featureState(v, "cabinet").Enabled || !featureState(v, "development.overview").Enabled {
		t.Fatalf("%+v", v)
	}
	// reset 回預設
	v, err = svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Reset: []string{"channels", "development.overview"}})
	if err != nil || !featureState(v, "channels").Enabled || featureState(v, "development.overview").Enabled || featureState(v, "channels").Changed {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Reset: []string{"nope"}}); code(err) != 400 {
		t.Fatal(err)
	}
	// 合併規則:組織層開著、系統層關掉 → 不可用;系統層再開放 → 照組織存著的值
	_, _ = svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"development.overview": true}})
	if _, err := svc.SaveServerFeatures(rootP, service.SystemSettingsInput{Features: map[string]bool{"development": false}}); err != nil {
		t.Fatal(err)
	}
	v, _ = svc.GetSystemSettings(alice, "o1")
	if featureState(v, "development").Key != "" || featureState(v, "development.overview").Key != "" || v.ServerFeatures["development.overview"] {
		t.Fatal("系統層關掉頁面,頁面與分頁都不列")
	}
	serverOn(t, svc, "development")
	v, _ = svc.GetSystemSettings(alice, "o1")
	if !featureState(v, "development.overview").Enabled {
		t.Fatal("系統層再開放,組織存著的值照舊生效")
	}
}

// 系統層沒開放的功能(預設的原本開發版功能):不列、組織層開、關、reset 都 400 並說明;存著的舊值保留但不生效
func testSystemServerOff(t *testing.T, svc *service.Service, boss *model.Principal, v *service.SystemSettingsView) {
	t.Helper()
	for _, f := range v.Features {
		if f.Edition != model.EditionStable || f.Experimental || !f.System {
			t.Fatalf("系統層預設只開放原本正式版的功能:%+v", f)
		}
	}
	if featureState(v, "familyAssistant").Key != "" || featureState(v, "development.overview").Key != "" || v.ServerFeatures["familyAssistant"] {
		t.Fatal("系統層沒開放的功能不列")
	}
	for _, in := range []service.SystemSettingsInput{
		{Features: map[string]bool{"familyAssistant": true}},
		{Features: map[string]bool{"familyAssistant": false}},
		{Features: map[string]bool{"development.overview": true, "channels": false}},
		{Reset: []string{"familyAssistant"}},
	} {
		if _, err := svc.SaveSystemSettings(boss, "o1", in); code(err) != 400 || !strings.Contains(err.Error(), "這台伺服器沒有開放") || !strings.Contains(err.Error(), "系統") {
			t.Fatal("系統層沒開放的功能 400 並說明", in, err)
		}
	}
	// 舊值(例如之前用開發版開過):直接寫進 store,讀的時候不列也不生效,改別的開關時保留
	st := svc.Store
	_ = st.SaveSystemSettings("o1", model.OrgSystemSettings{Features: map[string]bool{"familyAssistant": true, "development.overview": true}})
	v, err := svc.SaveSystemSettings(boss, "o1", service.SystemSettingsInput{Features: map[string]bool{"channels": false}})
	if err != nil || featureState(v, "familyAssistant").Key != "" || featureState(v, "channels").Enabled {
		t.Fatal(v, err)
	}
	x, _ := st.GetSystemSettings("o1")
	if !x.Features["familyAssistant"] || !x.Features["development.overview"] || x.Features["channels"] {
		t.Fatalf("舊值保留:%+v", x.Features)
	}
	if x.Enabled(model.ServerFeatures{}, "familyAssistant") || x.Enabled(model.ServerFeatures{}, "development.overview") {
		t.Fatal("舊值不生效")
	}
}

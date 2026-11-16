package api_test

import (
	"strings"
	"testing"

	"omixor-core/client"
	"omixor-core/service"
)

// 系統設定的 API:成員讀(角色憑證也可以)、管理員改、非管理員 403
func TestSystemAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveUser("alice@example.com", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("o1", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	v, err := alice.GetSystemSettings("o1")
	if err != nil || v.Saved || v.CanEdit || len(v.Features) == 0 {
		t.Fatal(v, err)
	}
	if _, err := alice.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"channels": false}}); status(err) != 403 {
		t.Fatal("成員不能改", err)
	}
	v, err = boss.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"channels": false, "monitoring.visitors": false}})
	if err != nil || !v.CanEdit || v.UpdatedBy != "boss@example.com" {
		t.Fatal(v, err)
	}
	if _, err := boss.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"system": false}}); status(err) != 400 {
		t.Fatal("必要功能不能關", err)
	}
	got, err := alice.GetSystemSettings("o1")
	if err != nil || !got.Saved {
		t.Fatal(got, err)
	}
	for _, f := range got.Features {
		if (f.Key == "channels" && f.Enabled) || (f.Key == "monitoring.visitors" && f.Enabled) {
			t.Fatalf("成員讀到改過的:%+v", f)
		}
	}
	if _, err := boss.GetSystemSettings("nope"); status(err) != 404 {
		t.Fatal(err)
	}

	// 角色憑證可以讀、不能改
	if _, err := boss.SaveAgent("o1", agentNamed("cto")); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.AssignTask("o1", service.TaskInput{AgentID: "cto", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	cl, err := boss.ClaimTask("o1", service.ClaimInput{AgentID: "cto"})
	if err != nil || cl.Credential == nil {
		t.Fatal(cl, err)
	}
	role := newClient(e)
	cred := "cto:" + cl.Credential.Secret
	role.Token = func() string { return cred }
	if rv, err := role.GetSystemSettings("o1"); err != nil || rv.CanEdit {
		t.Fatal("角色憑證可以讀", rv, err)
	}
	if _, err := role.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"channels": true}}); status(err) != 401 {
		t.Fatal("角色憑證不能改", err)
	}
	if _, err := role.GetServerFeatures(); status(err) != 401 {
		t.Fatal("角色憑證不能讀系統層", err)
	}
}

// 系統層的 API:任何登入者讀;只有伺服器管理員改(組織管理員、成員 403);組織層對系統層沒開放的功能 400
func TestServerFeaturesAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss") // 第一個登入的是伺服器管理員
	if _, err := boss.CreateOrg("o1", "組織一"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ email, role string }{{"alice@example.com", "member"}, {"bob@example.com", "admin"}} {
		if _, err := boss.SaveUser(u.email, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := boss.SaveMember("o1", u.email, u.role); err != nil {
			t.Fatal(err)
		}
	}
	alice, carol := e.login("evo-alice"), e.login("evo-bob")
	v, err := alice.GetServerFeatures()
	if err != nil || v.Saved || v.CanEdit || len(v.Features) == 0 {
		t.Fatal(v, err)
	}
	if b, _ := boss.GetServerFeatures(); !b.CanEdit {
		t.Fatal("伺服器管理員 canEdit")
	}
	in := service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true, "cabinet": false}}
	for _, c := range []*client.Client{alice, carol} {
		if _, err := c.SaveServerFeatures(in); status(err) != 403 {
			t.Fatal("組織管理員與成員不能改系統層", err)
		}
	}
	// 組織管理員:系統層沒開放的功能 400 並說明
	if _, err := carol.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}); status(err) != 400 ||
		!strings.Contains(err.Error(), "這台伺服器沒有開放「家庭助理」功能") {
		t.Fatal(err)
	}
	v, err = boss.SaveServerFeatures(in)
	if err != nil || !v.Saved || v.UpdatedBy != "boss@example.com" {
		t.Fatal(v, err)
	}
	o, err := alice.GetSystemSettings("o1")
	if err != nil || o.ServerFeatures["cabinet"] || !o.ServerFeatures["familyAssistant"] {
		t.Fatal(o, err)
	}
	for _, f := range o.Features {
		if f.Key == "cabinet" || !f.System {
			t.Fatalf("系統層關掉的不列:%+v", f)
		}
	}
	if _, err := carol.SaveSystemSettings("o1", service.SystemSettingsInput{Reset: []string{"cabinet"}}); status(err) != 400 {
		t.Fatal("系統層關掉的 reset 也 400", err)
	}
	if x, err := carol.SaveSystemSettings("o1", service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}); err != nil || !x.CanEdit {
		t.Fatal("系統層開放後組織管理員可以開", x, err)
	}
}

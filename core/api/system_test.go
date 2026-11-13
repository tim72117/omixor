package api_test

import (
	"testing"

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
}

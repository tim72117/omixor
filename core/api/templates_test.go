package api_test

import (
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 組織樣板的 API:開關關閉時列表空、套用 403;打開後管理員套用,再套用全部略過;成員不能套用
func TestTemplatesAPI(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("home", "測試家庭"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveUser("alice@example.com", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("home", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	if l, err := alice.ListOrgTemplates("home"); err != nil || len(l) != 0 {
		t.Fatal(l, err)
	}
	if model.BuildEdition != model.EditionDev {
		// 正式版:沒有家庭助理,開不了、套用 404
		if _, err := boss.SaveSystemSettings("home", service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}); status(err) != 400 {
			t.Fatal(err)
		}
		if _, err := boss.ApplyOrgTemplate("home", "family", service.TemplateApplyInput{}); status(err) != 404 {
			t.Fatal(err)
		}
		if _, err := alice.PreviewOrgTemplate("home", "family", false); status(err) != 404 {
			t.Fatal(err)
		}
		return
	}
	if _, err := boss.ApplyOrgTemplate("home", "family", service.TemplateApplyInput{}); status(err) != 403 {
		t.Fatal(err)
	}
	if _, err := boss.SaveSystemSettings("home", service.SystemSettingsInput{Features: map[string]bool{"familyAssistant": true}}); err != nil {
		t.Fatal(err)
	}
	if l, err := alice.ListOrgTemplates("home"); err != nil || len(l) != 1 {
		t.Fatal(l, err)
	}
	if pv, err := alice.PreviewOrgTemplate("home", "family", true); err != nil || pv.CanApply || len(pv.Items) == 0 {
		t.Fatal(pv, err)
	}
	if _, err := alice.ApplyOrgTemplate("home", "family", service.TemplateApplyInput{}); status(err) != 403 {
		t.Fatal("成員不能套用", err)
	}
	res, err := boss.ApplyOrgTemplate("home", "family", service.TemplateApplyInput{Scenarios: true})
	if err != nil || res.Failed != 0 || res.Created == 0 {
		t.Fatal(res, err)
	}
	// boss 是伺服器管理員、不是組織成員:和他有關的組織鏈邊略過(其他邊照建)
	skippedEdge := false
	for _, it := range res.Items {
		skippedEdge = skippedEdge || (it.Kind == "chain" && it.Status == "skipped" && it.Detail != "")
	}
	if !skippedEdge {
		t.Fatalf("%+v", res.Items)
	}
	res, err = boss.ApplyOrgTemplate("home", "family", service.TemplateApplyInput{Scenarios: true})
	if err != nil || res.Created != 0 || res.Failed != 0 {
		t.Fatal(res, err)
	}
}

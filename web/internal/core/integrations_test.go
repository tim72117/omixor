package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omixor-core/service"
)

func glInput(base *string) service.GitLabSettingsInput {
	return service.GitLabSettingsInput{BaseURL: base}
}

// 這台的舊設定(github.json / gitlab.json 的 repos、projects,連線的 GitLab 網址):核心還沒存過時匯入一次,原檔不刪不改;
// 核心存過之後以核心的為準(核心的 GitLab 網址會套到這台的連線)
func TestIntegrationSettingsImportLegacyFiles(t *testing.T) {
	a := connTestApp(t)
	dir := filepath.Join(DataRoot(), "orgs", DefaultOrgID)
	_ = os.MkdirAll(dir, 0o700)
	ghOld := `{"connection":"github","repos":["https://github.com/tim72117/evoleap.git","bad"]}`
	glOld := `{"connection":"gitlab","projects":["web/shop"]}`
	if err := os.WriteFile(filepath.Join(dir, "github.json"), []byte(ghOld), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gitlab.json"), []byte(glOld), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GLEnsureConnection(a, "https://gitlab.corp.test"); err != nil {
		t.Fatal(err)
	}
	GHSyncSettings(a)
	GLSyncSettings(a)
	be, err := agentBackend()
	if err != nil {
		t.Fatal(err)
	}
	gh, err := be.GetGitHubSettings(DefaultOrgID)
	if err != nil || !gh.Saved || strings.Join(gh.Repos, ",") != "tim72117/evoleap" {
		t.Fatalf("GitHub 匯入:%+v %v", gh, err)
	}
	gl, err := be.GetGitLabSettings(DefaultOrgID)
	if err != nil || !gl.Saved || gl.BaseURL != "https://gitlab.corp.test" || strings.Join(gl.Projects, ",") != "web/shop" {
		t.Fatalf("GitLab 匯入:%+v %v", gl, err)
	}
	// 原檔不動(GLEnsureConnection 只寫 connection,projects 保留)
	if b, _ := os.ReadFile(filepath.Join(dir, "github.json")); string(b) != ghOld {
		t.Fatalf("github.json 被改了:%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "gitlab.json")); !strings.Contains(string(b), "web/shop") {
		t.Fatalf("gitlab.json 的 projects 不能拿掉:%s", b)
	}
	// 之後以核心為準:別的裝置改了核心,這台下次讀就跟著變;核心換了 GitLab 網址 → 這台的連線跟著換
	if err := GHAddRepo(a, "a/b"); err != nil {
		t.Fatal(err)
	}
	newBase := "https://gitlab2.corp.test"
	if _, err := be.SaveGitLabSettings(DefaultOrgID, glInput(&newBase)); err != nil {
		t.Fatal(err)
	}
	GHSyncSettings(a)
	GLSyncSettings(a)
	a.Mu.Lock()
	repos := strings.Join(GHSettingsNow(a).Repos, ",")
	c, _ := GLConnection(a)
	a.Mu.Unlock()
	if repos != "tim72117/evoleap,a/b" || c.BaseURL != newBase {
		t.Fatalf("%s %s", repos, c.BaseURL)
	}
	// 第二次不會再匯入(核心存過):舊檔有別的 repo 也不影響
	_ = os.WriteFile(filepath.Join(dir, "github.json"), []byte(`{"repos":["x/y"]}`), 0o600)
	GHSyncSettings(a)
	a.Mu.Lock()
	repos = strings.Join(GHSettingsNow(a).Repos, ",")
	a.Mu.Unlock()
	if repos != "tim72117/evoleap,a/b" {
		t.Fatal(repos)
	}
}

package store

import (
	"errors"
	"testing"

	"omixor-core/model"
)

// 組織的 GitHub、GitLab 設定:Mem 讀寫、刪組織一併刪除;File 寫入會存檔,重開讀得回來
func TestIntegrationSettingsMemAndFile(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	at := mt("2026-11-13T01:02:03Z")
	if err := f.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetGitHubSettings("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("沒存過回 ErrNotFound", err)
	}
	if _, err := f.GetGitLabSettings("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	gh := model.GitHubSettings{Repos: []string{"a/b"}, UpdatedAt: &at, UpdatedBy: "boss@x"}
	gl := model.GitLabSettings{BaseURL: "https://gl.example.com", Projects: []string{"g/p"}, UpdatedAt: &at}
	if err := f.SaveGitHubSettings("o", gh); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveGitLabSettings("o", gl); err != nil {
		t.Fatal(err)
	}
	// 拿回去改不影響存著的(深拷貝)
	x, _ := f.GetGitHubSettings("o")
	x.Repos[0] = "zzz"
	f2, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f2.GetGitHubSettings("o")
	if err != nil || !g.Saved || len(g.Repos) != 1 || g.Repos[0] != "a/b" || g.UpdatedBy != "boss@x" || !g.UpdatedAt.Equal(at.Time) {
		t.Fatalf("重開讀回 GitHub:%+v %v", g, err)
	}
	l, err := f2.GetGitLabSettings("o")
	if err != nil || l.BaseURL != "https://gl.example.com" || len(l.Projects) != 1 {
		t.Fatalf("重開讀回 GitLab:%+v %v", l, err)
	}
	if err := f2.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	if _, err := f2.GetGitHubSettings("o"); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪組織一併刪除", err)
	}
}

package server

import (
	"fmt"
	"strings"
	"testing"

	"omixor-core/config"
	"omixor-core/service"
)

// OAuth 自我檢查要在 GitLab 登入套好之後跑,摘要才有 GitLab 要登記的回呼網址
func TestConfigureLoginsChecksAfterGitLab(t *testing.T) {
	cfg := config.LoadFrom(envMap(map[string]string{
		"OMIXOR_PUBLIC_URL":           "https://omixor.example.com/",
		"OMIXOR_GITLAB_URL":           "https://gitlab.example.test",
		"OMIXOR_GITLAB_CLIENT_ID":     "id",
		"OMIXOR_GITLAB_CLIENT_SECRET": "test-secret",
	}), config.Server)
	var out strings.Builder
	logf := func(format string, args ...any) { fmt.Fprintf(&out, format+"\n", args...) }
	if err := configureLogins(&service.Service{}, cfg, logf); err != nil {
		t.Fatal(err)
	}
	if want := "https://omixor.example.com/oauth/gitlab/callback"; !strings.Contains(out.String(), want) {
		t.Fatalf("摘要要有 %s:\n%s", want, out.String())
	}
}

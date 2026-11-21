package mcp

import (
	"testing"

	"omixor-core/httperr"
	"omixor-web/internal/core"
)

// issue_create 帶 project、project_save 帶 key、issue_update 的 version 與 409(current、currentVersion 原樣帶出)
func TestIssueToolsProjectKeyAndVersion(t *testing.T) {
	a := iaApp(t)
	// 沒有代號的專案放不進 Issue
	p := iaRun(t, a, core.MCPActor, "project_save", map[string]any{"name": "App"})
	if _, err := Run(a, "issue_create", map[string]any{"title": "x", "project": p["id"]}, core.MCPActor); err == nil {
		t.Fatal("專案沒有代號,Issue 放不進去")
	}
	// 代號的驗證錯誤原樣帶回
	if _, err := Run(a, "project_save", map[string]any{"id": p["id"], "key": "bad key"}, core.MCPActor); err == nil {
		t.Fatal("壞的代號要報錯")
	}
	p = iaRun(t, a, core.MCPActor, "project_save", map[string]any{"id": p["id"], "key": "app"})
	if p["key"] != "APP" {
		t.Fatalf("key:%v", p)
	}
	i := iaRun(t, a, core.MCPActor, "issue_create", map[string]any{"title": "專案內", "project": p["id"]})
	if i["project"] != p["id"] || i["key"] != "APP-1" {
		t.Fatalf("issue_create project:%v", i)
	}
	v1 := i["version"]
	i2 := iaRun(t, a, core.MCPActor, "issue_update", map[string]any{"id": i["id"], "title": "改過", "version": v1})
	if i2["title"] != "改過" {
		t.Fatal(i2)
	}
	_, err := Run(a, "issue_update", map[string]any{"id": i["id"], "title": "又改", "version": v1}, core.MCPActor)
	in := httperr.Report("test", err)
	if err == nil || in.Status != 409 || in.Data["currentVersion"] == nil || in.Data["current"] == nil {
		t.Fatalf("409 要帶 current、currentVersion:%v %+v", err, in)
	}
}

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omixor-core/service"

	"omixor-web/internal/core/coretest"
)

// newEmptyApp 完全空的資料庫(沒有任何組織)上的 app;測完換回有預設組織的測試核心
func newEmptyApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	old := newTestCore
	t.Cleanup(func() { newTestCore = old })
	UseTestStores(coretest.NewEmptyStores().Get)
	a := New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	return a
}

func coreOrgIDs(t *testing.T) []string {
	t.Helper()
	l := localCore()
	list, err := l.Svc.Store.ListOrgs()
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, o := range list {
		ids = append(ids, o.ID)
	}
	return ids
}

func TestNoOrgNothingCreated(t *testing.T) {
	a := newEmptyApp(t)
	for _, name := range ViewNames() { // 每個 view 都要能讀,不 panic
		if _, ok := View(a, name); !ok {
			t.Fatal("沒有 view", name)
		}
	}
	a.RoleCallFinished()
	a.AgentsChanged()
	a.SystemRefresh()
	a.Mu.Lock()
	a.ReloadIssues()
	a.RefreshIssuesIfStale(0)
	cur, n := a.CurrentOrgID(), len(a.Orgs().Orgs)
	a.Mu.Unlock()
	if cur != "" || n != 0 {
		t.Fatalf("不該有組織:%q %d", cur, n)
	}
	for name, args := range map[string]map[string]any{
		"connection_upsert": {"name": "x"}, "channel_create": {"name": "x"}, "member_save": {"email": "a@b.c"},
	} {
		if _, err := Action(a, name, args); err == nil || !strings.Contains(err.Error(), "還沒有組織") {
			t.Fatalf("%s 應該回還沒有組織:%v", name, err)
		}
	}
	l := localCore()
	if _, err := l.CreateIssue(DefaultOrgID, service.IssueInput{Title: "x"}); err == nil {
		t.Fatal("沒有組織時不該補建 default")
	}
	if _, err := ImportIssuesPath(writeIssueFile(t), "", false); err == nil {
		t.Fatal("沒指定 -org 又沒有 default 組織要回錯誤")
	}
	if ids := coreOrgIDs(t); len(ids) != 0 {
		t.Fatal("核心不該有任何組織", ids)
	}
}

func TestFirstOrgIsDefault(t *testing.T) {
	a := newEmptyApp(t)
	// 升級或匯入留下的檔案:建第一個組織不能蓋掉
	dir := filepath.Join(DataRoot(), "orgs", DefaultOrgID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := `[{"id":"keep","name":"舊連線","baseURL":"https://example.test","auth":"none"}]`
	if err := WriteData(filepath.Join(dir, "connections.json"), []byte(keep)); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "org_create", map[string]any{"name": "我的團隊", "template": "copyCurrent"}); err != nil {
		t.Fatal(err)
	}
	a.Mu.Lock()
	cur, conns, seed := a.CurrentOrgID(), a.conns().List, len(a.C["services"])
	a.Mu.Unlock()
	if cur != DefaultOrgID || orgScope(cur) != "" {
		t.Fatal("第一個組織要是目前的 default 組織", cur)
	}
	if len(conns) != 1 || conns[0].ID != "keep" {
		t.Fatal("舊的連線要照讀", conns)
	}
	if seed == 0 {
		t.Fatal("開了範本時第一個組織要看得到範例資料")
	}
	if _, err := Action(a, "channel_create", map[string]any{"name": "一般"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Action(a, "org_create", map[string]any{"name": "Second"}); err != nil {
		t.Fatal(err)
	}
	if ids := coreOrgIDs(t); len(ids) != 2 {
		t.Fatal(ids)
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.CurrentOrgID() != DefaultOrgID {
		t.Fatal("第二個組織沒指定切換就不切", a.CurrentOrgID())
	}
}

func writeIssueFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "issues.json")
	if err := os.WriteFile(p, []byte(`[{"id":"ISS-1","title":"a","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

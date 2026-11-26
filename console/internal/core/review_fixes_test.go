package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	"omixor-core/claude"
	"omixor-core/service"
)

// 人工啟動的任務以啟動的人署名領取,回報照舊成功
func TestRunnerManualStartSignsAsCaller(t *testing.T) {
	claimedBy := ""
	var a *App
	var org, id string
	a, org, _ = setupRunner(t, &fakeRun{do: func(context.Context, string) (*claude.Result, error) {
		claimedBy = taskNow(t, a, org, id).ClaimedBy
		return &claude.Result{Text: "做好了"}, nil
	}})
	tk := assignTask(t, a, org, "triager", "呼叫者啟動")
	id = tk.ID
	who := NewCaller("runner-alice", "Alice", "runner-alice@example.com", true, service.LDAPSettings{})
	done, err := a.startTask(context.Background(), org, tk.ID, who)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	got := taskNow(t, a, org, tk.ID)
	if got.Status != "done" || claimedBy != "runner-alice@example.com" {
		t.Fatalf("要由 runner-alice 領取並做完:%q %s", claimedBy, Marshal(got))
	}
}

// 角色端點、OAuth 寫入之後要通知全部組織層的主題(寫的可能不是系統工作組織)
func TestRoleCallFinishedPublishesOrgTopics(t *testing.T) {
	a, _, _ := setupRunner(t, &fakeRun{})
	ch := a.Subscribe()
	defer a.Unsubscribe(ch)
	a.RoleCallFinished()
	var got []string
	for len(ch) > 0 {
		got = append(got, (<-ch).Topics...)
	}
	for _, topic := range []string{"issues", "agents", "cabinet", "incidents", "chain", "members", "channels", "badges"} {
		if !slices.Contains(got, topic) {
			t.Error("少了主題", topic, got)
		}
	}
}

// 別的組織寫的動態:原本的 App 收著的那份要跟上,換成系統工作組織時看得到
func TestScopedActivityReachesStash(t *testing.T) {
	a, org, _ := setupRunner(t, &fakeRun{})
	a.Mu.Lock()
	other := a.CreateOrg("別的組織", "", "", "")
	a.SwitchOrg(other.ID)
	a.SwitchOrg(org) // other 的資料收起來
	a.Mu.Unlock()
	s, release := a.Scoped(other.ID, false)
	defer release()
	s.Mu.Lock()
	s.Log(Person("x"), "flame", "在別的組織寫的動態", "incidents")
	s.Mu.Unlock()
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.SwitchOrg(other.ID)
	if !strings.Contains(string(Marshal(a.C["activity"])), "在別的組織寫的動態") {
		t.Fatal(string(Marshal(a.C["activity"])))
	}
}

// 功能開關、受限專案:各組織快取在原本的 App 上,每個請求的一次性 App 共用;寫入後作廢
func TestScopedCachesShared(t *testing.T) {
	a, org, _ := setupRunner(t, &fakeRun{})
	s1, r1 := a.Scoped(org, true)
	defer r1()
	s2, r2 := a.Scoped(org, true)
	defer r2()
	if sysOf(s1) != sysOf(s2) || sysOf(s1) == sysOf(a) {
		t.Fatal("一次性 App 要共用組織的功能開關快取,不是原本 App 的那份")
	}
	if _, err := s1.RestrictedProjectIDsOf(org); err != nil {
		t.Fatal(err)
	}
	c1, ok := restrictedCaches.Load(scopedKey{a, org})
	if !ok {
		t.Fatal("受限專案要快取在原本的 App 上")
	}
	if _, err := s2.RestrictedProjectIDsOf(org); err != nil {
		t.Fatal(err)
	}
	if c2, _ := restrictedCaches.Load(scopedKey{a, org}); c2 != c1 {
		t.Fatal("要沿用")
	}
	s2.InvalidateScoped()
	if _, ok := restrictedCaches.Load(scopedKey{a, org}); ok {
		t.Fatal("作廢")
	}
	s3, r3 := a.Scoped(org, true)
	defer r3()
	if sysOf(s3) == sysOf(s1) {
		t.Fatal("功能開關快取要作廢")
	}
}

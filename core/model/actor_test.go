package model

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestActorKindOfID(t *testing.T) {
	const u = "0198f1c2-7a3b-7c4d-8e5f-0123456789ab"
	cases := []struct {
		id   string
		kind ActorKind
		ok   bool
	}{
		{"usr_" + u, ActorKindUser, true},
		{"agt_" + u, ActorKindAgent, true},
		{"sys_omixor", ActorKindSystem, true},
		{"sys_legacy_1a2b3c4d", ActorKindSystem, true},
		{"sys_a", ActorKindSystem, true},
		{"sys_" + strings.Repeat("a", 64), ActorKindSystem, true},
		{"", "", false},
		{"usr_", "", false},
		{"sys_", "", false},
		{"usr_" + strings.ToUpper(u), "", false}, // 只收小寫
		{"usr_" + u + "0", "", false},
		{"usr_" + u[:35], "", false},
		{"usr_not-a-uuid", "", false},
		{"u_abc123", "", false}, // 舊的使用者 id 格式
		{"helper", "", false},
		{"agent:helper", "", false},
		{"USR_" + u, "", false},
		{" usr_" + u, "", false},
		{"sys_Omixor", "", false},
		{"sys__x", "", false}, // 名稱第一個字不能是底線
		{"sys_a-b", "", false},
		{"sys_" + strings.Repeat("a", 65), "", false},
		{"usr_" + u + "\n", "", false},
	}
	for _, c := range cases {
		k, ok := ActorKindOfID(c.id)
		if k != c.kind || ok != c.ok {
			t.Errorf("ActorKindOfID(%q) = %q,%v,應為 %q,%v", c.id, k, ok, c.kind, c.ok)
		}
		if ValidActorID(c.id) != c.ok {
			t.Errorf("ValidActorID(%q) 與 ActorKindOfID 不一致", c.id)
		}
		if c.ok != ValidActorIDOf(c.kind, c.id) {
			t.Errorf("ValidActorIDOf(%q,%q)", c.kind, c.id)
		}
	}
	if ValidActorIDOf(ActorKindAgent, "usr_0198f1c2-7a3b-7c4d-8e5f-0123456789ab") {
		t.Error("使用者 id 不能算角色 id")
	}
}

func TestNewActorID(t *testing.T) {
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	u, a := NewUserActorID(now), NewAgentActorID(now)
	if !strings.HasPrefix(u, "usr_") || !ValidActorIDOf(ActorKindUser, u) {
		t.Fatal(u)
	}
	if !strings.HasPrefix(a, "agt_") || !ValidActorIDOf(ActorKindAgent, a) {
		t.Fatal(a)
	}
	if u == NewUserActorID(now) {
		t.Fatal("同一毫秒也要不同(隨機部分)")
	}
	// UUIDv7:版本 7、variant 10xx
	if u[4+14] != '7' || !strings.ContainsRune("89ab", rune(u[4+19])) {
		t.Fatalf("不是 UUIDv7:%s", u)
	}
	for _, k := range []ActorKind{ActorKindUser, ActorKindAgent} {
		id, err := NewActorID(k, now)
		if err != nil || !ValidActorIDOf(k, id) {
			t.Fatal(k, id, err)
		}
	}
	for _, k := range []ActorKind{ActorKindSystem, "", "robot"} {
		if id, err := NewActorID(k, now); err == nil {
			t.Fatalf("%q 不能自動產生:%s", k, id)
		}
	}
}

// UUIDv7 依時間排序:時間不同的 id 字串排序等於時間排序
func TestActorIDSortsByTime(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var ids []string
	for i := 0; i < 50; i++ {
		ids = append(ids, NewUserActorID(base.Add(time.Duration(i)*time.Millisecond*3)))
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("依時間產生的 id 要由小到大")
	}
	if NewAgentActorID(base.Add(time.Hour))[4:] <= NewAgentActorID(base)[4:] {
		t.Fatal("時間晚的 id 要比較大")
	}
}

func TestSystemActorsFixed(t *testing.T) {
	want := map[string]string{"sys_omixor": "omixor", "sys_routine": "routine", "sys_chain": "chain"}
	if len(SystemActors) != len(want) {
		t.Fatalf("系統 actor 清單變了:%v", SystemActors)
	}
	seen := map[string]bool{}
	for _, s := range SystemActors {
		if want[s.ID] != s.Handle {
			t.Errorf("%s 的 handle 不對:%q", s.ID, s.Handle)
		}
		if !ValidActorIDOf(ActorKindSystem, s.ID) || s.ID != ActorIDPrefixSystem+s.Handle || s.Name == "" || seen[s.ID] {
			t.Errorf("系統 actor %+v 不合格", s)
		}
		seen[s.ID] = true
		if !IsSystemActorID(s.ID) {
			t.Error(s.ID)
		}
		if err := ValidateActorRecord(ActorRecord{ID: s.ID, Kind: ActorKindSystem, Name: s.Name, Handle: s.Handle}); err != nil {
			t.Errorf("%s:%v", s.ID, err)
		}
	}
	if SystemActorOmixor != "sys_omixor" || SystemActorRoutine != "sys_routine" || SystemActorChain != "sys_chain" {
		t.Fatal("固定 id 不能改(資料庫裡已經寫了)")
	}
	if IsSystemActorID("sys_legacy_mcp") || IsSystemActorID("usr_x") {
		t.Fatal("不在固定清單")
	}
}

func TestCleanUserHandle(t *testing.T) {
	cases := []struct {
		in, out string
		err     error
	}{
		{"Tim@Example.COM", "tim@example.com", nil},
		{"  tim@x.com \t", "tim@x.com", nil},
		{"@tim", "tim", nil},
		{" @Tim ", "tim", nil},
		{"tim", "tim", nil},
		{"omixor-console", "omixor-console", nil},
		{"", "", ErrActorHandleEmpty},
		{"   ", "", ErrActorHandleEmpty},
		{"@", "", ErrActorHandleEmpty},
		{"a b@x.com", "", ErrActorHandleInvalid},
		{"a\tb", "", ErrActorHandleInvalid},
		{"a\nb@x", "", ErrActorHandleInvalid},
		{"a b", "", ErrActorHandleInvalid},
		{"a　b", "", ErrActorHandleInvalid},
		{"a\x00b", "", ErrActorHandleInvalid},
		{strings.Repeat("a", 255), "", ErrActorHandleTooLong},
		{"中文@x.com", "中文@x.com", nil},
	}
	for _, c := range cases {
		got, err := CleanUserHandle(c.in)
		if got != c.out && c.err == nil || err != c.err {
			t.Errorf("CleanUserHandle(%q) = %q,%v,應為 %q,%v", c.in, got, err, c.out, c.err)
		}
		if c.err == nil && got != c.out {
			t.Errorf("CleanUserHandle(%q) = %q,應為 %q", c.in, got, c.out)
		}
	}
	if h, err := CleanUserHandle(strings.Repeat("a", 254)); err != nil || len(h) != 254 {
		t.Error("254 字元要通過")
	}
	// 冪等
	for _, in := range []string{"Tim@X.com", "@Tim"} {
		a, _ := CleanUserHandle(in)
		b, _ := CleanUserHandle(a)
		if a != b {
			t.Errorf("不冪等:%q → %q → %q", in, a, b)
		}
	}
}

func TestCleanAgentHandle(t *testing.T) {
	cases := []struct {
		in, out string
		ok      bool
	}{
		{"helper", "helper", true},
		{"@Helper", "helper", true},
		{" HELPER ", "helper", true},
		{"ceo-2", "ceo-2", true},
		{"0a", "0a", true},
		{strings.Repeat("a", 64), strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), "", false},
		{"", "", false},
		{"-x", "", false},
		{"a_b", "", false},
		{"a b", "", false},
		{"a:b", "", false},
		{"agent:helper", "", false},
		{"中文", "", false},
		{"tim@x.com", "", false},
	}
	for _, c := range cases {
		got, err := CleanAgentHandle(c.in)
		if (err == nil) != c.ok || got != c.out {
			t.Errorf("CleanAgentHandle(%q) = %q,%v", c.in, got, err)
		}
	}
	if _, err := CleanAgentHandle(""); err != ErrActorHandleEmpty {
		t.Error("空字串要回 ErrActorHandleEmpty")
	}
}

func TestCleanSystemAndByKind(t *testing.T) {
	for in, ok := range map[string]bool{"omixor": true, " Routine ": true, "legacy_ab12": true, "": false, "a-b": false, "_x": false, "a b": false} {
		_, err := CleanSystemHandle(in)
		if (err == nil) != ok {
			t.Errorf("CleanSystemHandle(%q):%v", in, err)
		}
	}
	if h, err := CleanActorHandle(ActorKindUser, "@A@b.c"); err != nil || h != "a@b.c" {
		t.Error(h, err)
	}
	if h, err := CleanActorHandle(ActorKindAgent, "@Ops"); err != nil || h != "ops" {
		t.Error(h, err)
	}
	if h, err := CleanActorHandle(ActorKindSystem, "Chain"); err != nil || h != "chain" {
		t.Error(h, err)
	}
	if _, err := CleanActorHandle("robot", "x"); err == nil {
		t.Error("不認得的種類要錯")
	}
}

func TestValidateActorRecord(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	uid, aid := NewUserActorID(now), NewAgentActorID(now)
	okUser := ActorRecord{ID: uid, Kind: ActorKindUser, Name: "Tim", Handle: "tim@x.com"}
	okAgent := ActorRecord{ID: aid, Kind: ActorKindAgent, Org: "o", Name: "Helper", Handle: "helper", Owner: uid}
	okSys := ActorRecord{ID: "sys_omixor", Kind: ActorKindSystem, Name: "omixor", Handle: "omixor"}
	for _, a := range []ActorRecord{okUser, okAgent, okSys, {ID: aid, Kind: ActorKindAgent, Org: "o", Handle: "h"}} {
		if err := ValidateActorRecord(a); err != nil {
			t.Errorf("%+v:%v", a, err)
		}
	}
	mut := func(a ActorRecord, f func(*ActorRecord)) ActorRecord { f(&a); return a }
	bad := map[string]ActorRecord{
		"種類空":          mut(okUser, func(a *ActorRecord) { a.Kind = "" }),
		"種類與前綴不符":      mut(okUser, func(a *ActorRecord) { a.Kind = ActorKindAgent; a.Org = "o" }),
		"id 格式":        mut(okUser, func(a *ActorRecord) { a.ID = "u_abc" }),
		"handle 沒規範化":  mut(okUser, func(a *ActorRecord) { a.Handle = "Tim@x.com" }),
		"handle 空":     mut(okUser, func(a *ActorRecord) { a.Handle = "" }),
		"使用者有組織":       mut(okUser, func(a *ActorRecord) { a.Org = "o" }),
		"使用者有 owner":   mut(okUser, func(a *ActorRecord) { a.Owner = uid }),
		"角色沒組織":        mut(okAgent, func(a *ActorRecord) { a.Org = "" }),
		"角色 owner 是角色": mut(okAgent, func(a *ActorRecord) { a.Owner = aid }),
		"角色 owner 格式":  mut(okAgent, func(a *ActorRecord) { a.Owner = "tim" }),
		"系統有組織":        mut(okSys, func(a *ActorRecord) { a.Org = "o" }),
		"系統 handle 不符": mut(okSys, func(a *ActorRecord) { a.Handle = "routine" }),
	}
	for name, a := range bad {
		if err := ValidateActorRecord(a); err == nil {
			t.Errorf("%s 應該被拒絕:%+v", name, a)
		}
	}
}

func TestActorRef(t *testing.T) {
	a := ActorRecord{ID: "sys_chain", Kind: ActorKindSystem, Name: "組織鏈", Handle: "chain", Avatar: "x", CreatedAt: Now()}
	if r := a.Ref(); r != (ActorRef{ID: "sys_chain", Kind: ActorKindSystem, Name: "組織鏈", Handle: "chain"}) {
		t.Fatal(r)
	}
	if a.Disabled() {
		t.Fatal("現役")
	}
	n := Now()
	a.DisabledAt = &n
	if !a.Disabled() {
		t.Fatal("退役")
	}
	z := Time{}
	a.DisabledAt = &z
	if a.Disabled() {
		t.Fatal("零值時間視為現役")
	}
}

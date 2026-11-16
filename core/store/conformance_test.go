package store

// 三種實作(Mem、File、PG)共用的一致性測試:同一組測試函式對每一種 store 都跑一次,斷言行為一樣。
// PG 有設 OPS_TEST_DATABASE_URL 就連它,沒設就自動起 embedded PostgreSQL(-short 或 OPS_TEST_NO_EMBEDDED_PG=1 時略過;見 pg_embedded_test.go);
// 每個測試建一個暫時的 schema,測完刪掉(見 issue_comment_test.go 的 testPG)。
// File 在讀回之前會重開同一個資料夾(impl.reopen),確認寫進 store.json 再讀回來也一樣;Mem、PG 的 reopen 原樣回傳。
//
// 時間一律用到秒的 UTC(PG 存到秒,File 的 JSON 也到秒);比對整筆資料用 sameJSON(model.Time 編成到秒的字串,nil 與空陣列會不同)。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
)

type storeImpl struct {
	name string
	// open 開一個新的空 store
	open func(t *testing.T) Store
	// reopen File 重開同一個資料夾(讀回 store.json);其他原樣回傳
	reopen func(t *testing.T, s Store) Store
}

const pgEnv = "OPS_TEST_DATABASE_URL"

func storeImpls() []storeImpl {
	same := func(_ *testing.T, s Store) Store { return s }
	return []storeImpl{
		{name: "mem", open: func(*testing.T) Store { return NewMem() }, reopen: same},
		{name: "file", open: func(t *testing.T) Store {
			t.Helper()
			f, err := OpenFile(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return f
		}, reopen: func(t *testing.T, s Store) Store {
			t.Helper()
			g, err := OpenFile(s.(*File).Dir())
			if err != nil {
				t.Fatal("重開", err)
			}
			return g
		}},
		{name: "pg", open: func(t *testing.T) Store {
			t.Helper()
			return testPG(t, testPGURL(t))
		}, reopen: same},
	}
}

// storeSuite 每一項用自己的新 store(PG 是自己的 schema)
var storeSuite = []struct {
	name string
	fn   func(t *testing.T, impl storeImpl)
}{
	{"Users", suiteUsers},
	{"Tokens", suiteTokens},
	{"Audit", suiteAudit},
	{"OrgsMembers", suiteOrgsMembers},
	{"Issues", suiteIssues},
	{"IssueIDCaseInsensitive", suiteIssueIDCaseInsensitive},
	{"AddIssueCommentConcurrent", suiteAddIssueCommentConcurrent},
	{"Attachments", suiteAttachments},
	{"Agents", suiteAgents},
	{"AgentTasks", suiteAgentTasks},
	{"AgentCredentials", suiteAgentCredentials},
	{"OrgChain", suiteOrgChain},
	{"Cabinet", suiteCabinet},
	{"Channels", suiteChannels},
	{"Incidents", suiteIncidents},
	{"OrgDoc", suiteOrgDoc},
	{"MCPConfig", suiteMCPConfig},
	{"SystemSettings", suiteSystemSettings},
	{"ServerFeatures", suiteServerFeatures},
	{"DeleteOrgCascade", suiteDeleteOrgCascade},
	{"ConcurrentCreate", suiteConcurrentCreate},
	{"ConcurrentClaim", suiteConcurrentClaim},
}

func TestStoreSuite(t *testing.T) {
	for _, impl := range storeImpls() {
		t.Run(impl.name, func(t *testing.T) {
			if impl.name == "pg" {
				testPGURL(t) // 沒有可用的 PG 就整組略過(見 pg_embedded_test.go)
			}
			runStoreSuite(t, impl)
		})
	}
}

func runStoreSuite(t *testing.T, impl storeImpl) {
	for _, c := range storeSuite {
		t.Run(c.name, func(t *testing.T) { c.fn(t, impl) })
	}
}

// ---- 小工具 ----

func chk(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantNotFound(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("%s:要 ErrNotFound,拿到 %v", what, err)
	}
}

// sameJSON 編成 JSON 比對(時間到秒;nil 與空陣列不同)
func sameJSON(t *testing.T, what string, got, want any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(g, w) {
		t.Fatalf("%s 不一樣\n拿到:%s\n要的:%s", what, g, w)
	}
}

func mtp(s string) *model.Time {
	x := mt(s)
	return &x
}

func strp(s string) *string { return &s }

func newOrg(t *testing.T, s Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		chk(t, s.CreateOrg(model.Org{ID: id, Name: id, CreatedAt: mt("2026-01-01T00:00:00Z")}))
	}
}

func issueIDs(l []model.Issue) []string {
	out := []string{}
	for _, i := range l {
		out = append(out, i.ID)
	}
	return out
}

func ids[T any](l []T, id func(T) string) []string {
	out := []string{}
	for _, x := range l {
		out = append(out, id(x))
	}
	return out
}

// ---- 使用者 ----

func suiteUsers(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	if n, err := s.CountUsers(); err != nil || n != 0 {
		t.Fatal("一開始沒有使用者", n, err)
	}
	if l, err := s.ListUsers(); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("空的要是 [] 不是 nil:%#v %v", l, err)
	}
	u1 := model.User{ID: "u1", EvoleapID: "e1", Email: "a@x", Name: "A", Role: "admin", CreatedAt: mt("2026-01-02T00:00:00Z")}
	u2 := model.User{ID: "u2", Email: "b@x", Name: "B", Role: "member", CreatedAt: mt("2026-01-01T00:00:00Z"),
		LastLoginAt: mtp("2026-01-03T04:05:06Z")}
	chk(t, s.SaveUser(u1))
	chk(t, s.SaveUser(u2))
	if err := s.SaveUser(model.User{ID: "u3", Email: "a@x", Role: "member", CreatedAt: mt("2026-01-04T00:00:00Z")}); err == nil {
		t.Fatal("email 重複要拒絕")
	}
	if err := s.SaveUser(model.User{ID: "u3", EvoleapID: "e1", Email: "c@x", Role: "member", CreatedAt: mt("2026-01-04T00:00:00Z")}); err == nil {
		t.Fatal("evoleap id 重複要拒絕")
	}
	// 沒有 evoleap id 的可以有很多個
	u4 := model.User{ID: "u4", Email: "d@x", Role: "member", CreatedAt: mt("2026-01-05T00:00:00Z")}
	chk(t, s.SaveUser(u4))
	// 同 id 再存 = 修改(建立時間不變)
	u2.Name, u2.Disabled, u2.LastLoginAt = "B2", true, mtp("2026-02-01T00:00:00Z")
	chk(t, s.SaveUser(u2))
	s = impl.reopen(t, s)
	if n, err := s.CountUsers(); err != nil || n != 3 {
		t.Fatal("使用者數", n, err)
	}
	l, err := s.ListUsers()
	chk(t, err)
	sameJSON(t, "ListUsers(依建立時間)", l, []model.User{u2, u1, u4})
	g, err := s.UserByID("u1")
	chk(t, err)
	sameJSON(t, "UserByID", g, u1)
	g, err = s.UserByEmail("b@x")
	chk(t, err)
	sameJSON(t, "UserByEmail", g, u2)
	g, err = s.UserByEvoleapID("e1")
	chk(t, err)
	sameJSON(t, "UserByEvoleapID", g, u1)
	_, err = s.UserByID("zz")
	wantNotFound(t, "UserByID", err)
	_, err = s.UserByEmail("zz@x")
	wantNotFound(t, "UserByEmail", err)
	_, err = s.UserByEvoleapID("")
	wantNotFound(t, "UserByEvoleapID 空字串", err)
	_, err = s.UserByEvoleapID("zz")
	wantNotFound(t, "UserByEvoleapID", err)
}

// ---- token(建立、到期、撤銷) ----

func suiteTokens(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	chk(t, s.SaveUser(model.User{ID: "u1", Email: "a@x", Role: "member", CreatedAt: mt("2026-01-01T00:00:00Z")}))
	// File 存檔時用真的現在時間丟掉過期的 token,所以這裡的「現在」放在很後面
	now := mt("2099-03-01T12:00:00Z").Time
	t1 := model.Token{ID: "t1", UserID: "u1", Device: "mac", CreatedAt: mt("2099-03-01T00:00:00Z"), Hash: []byte{1}}
	t2 := model.Token{ID: "t2", UserID: "u1", Device: "ios", CreatedAt: mt("2099-03-01T01:00:00Z"), Hash: []byte{2},
		ExpiresAt: mtp("2099-03-01T13:00:00Z")}
	t3 := model.Token{ID: "t3", UserID: "u1", Device: "old", CreatedAt: mt("2099-02-01T00:00:00Z"), Hash: []byte{3},
		ExpiresAt: mtp("2099-03-01T12:00:00Z")} // 到期時間 == now:已經不能用
	for _, x := range []model.Token{t1, t2, t3} {
		chk(t, s.CreateToken(x))
	}
	s = impl.reopen(t, s)
	tk, u, err := s.TokenByHash([]byte{1}, now)
	chk(t, err)
	if tk.ID != "t1" || tk.Email != "a@x" || tk.Device != "mac" || !tk.CreatedAt.Equal(t1.CreatedAt.Time) || tk.ExpiresAt != nil || u.ID != "u1" || u.Email != "a@x" {
		t.Fatalf("TokenByHash:%+v %+v", tk, u)
	}
	if _, _, err := s.TokenByHash([]byte{2}, t2.ExpiresAt.Add(-time.Second)); err != nil {
		t.Fatal("到期前一秒還能用", err)
	}
	_, _, err = s.TokenByHash([]byte{2}, t2.ExpiresAt.Time)
	wantNotFound(t, "到期時間到了", err)
	_, _, err = s.TokenByHash([]byte{3}, now)
	wantNotFound(t, "過期的 token", err)
	_, _, err = s.TokenByHash([]byte{9}, now)
	wantNotFound(t, "沒有的 token", err)
	l, err := s.ListTokens(now)
	chk(t, err)
	w2, w1 := t2, t1
	w2.Email, w1.Email = "a@x", "a@x"
	sameJSON(t, "ListTokens(新的在前、不含過期)", l, []model.Token{w2, w1})

	used := mt("2099-03-01T11:00:00Z")
	chk(t, s.TouchToken("t1", used.Time))
	chk(t, s.TouchToken("zz", used.Time)) // 沒有的不算錯
	s = impl.reopen(t, s)
	tk, _, err = s.TokenByHash([]byte{1}, now)
	chk(t, err)
	if tk.LastUsedAt == nil || !tk.LastUsedAt.Equal(used.Time) {
		t.Fatal("TouchToken", tk.LastUsedAt)
	}

	chk(t, s.RevokeToken("t1", now))
	wantNotFound(t, "撤銷兩次", s.RevokeToken("t1", now))
	wantNotFound(t, "撤銷沒有的", s.RevokeToken("zz", now))
	s = impl.reopen(t, s)
	_, _, err = s.TokenByHash([]byte{1}, now)
	wantNotFound(t, "撤銷的 token", err)
	l, err = s.ListTokens(now)
	chk(t, err)
	sameJSON(t, "撤銷後的 ListTokens", l, []model.Token{w2})
}

// ---- 操作紀錄 ----

func suiteAudit(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	if l, err := s.ListAudit(10); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("空的要是 []:%#v %v", l, err)
	}
	at := mt("2026-01-02T03:04:05Z")
	chk(t, s.AddAudit(model.AuditEntry{At: at, UserID: "u1", Email: "a@x", Action: "login"}))
	chk(t, s.AddAudit(model.AuditEntry{At: at, UserID: "u1", Email: "a@x", Action: "org.create", Org: "o", Detail: map[string]any{"name": "O"}}))
	chk(t, s.AddAudit(model.AuditEntry{At: mt("2026-01-02T03:04:06Z"), Action: "x"}))
	s = impl.reopen(t, s)
	l, err := s.ListAudit(2)
	chk(t, err)
	sameJSON(t, "ListAudit(新的在前、限筆數)", l, []model.AuditEntry{
		{ID: 3, At: mt("2026-01-02T03:04:06Z"), Action: "x"},
		{ID: 2, At: at, UserID: "u1", Email: "a@x", Action: "org.create", Org: "o", Detail: map[string]any{"name": "O"}},
	})
	chk(t, s.AddAudit(model.AuditEntry{At: at, Action: "y"}))
	if l, _ := s.ListAudit(10); len(l) != 4 || l[0].ID != 4 || l[3].ID != 1 {
		t.Fatal("id 要接續", l)
	}
}

// ---- 組織與成員 ----

func suiteOrgsMembers(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	if l, err := s.ListOrgs(); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("空的要是 []:%#v %v", l, err)
	}
	t1, t2 := mt("2026-01-01T00:00:00Z"), mt("2026-01-02T00:00:00Z")
	chk(t, s.CreateOrg(model.Org{ID: "a", Name: "A", CreatedAt: t2}))
	chk(t, s.CreateOrg(model.Org{ID: "c", Name: "C", CreatedAt: t1}))
	chk(t, s.CreateOrg(model.Org{ID: "b", Name: "B", CreatedAt: t1, Symbol: "star"}))
	if err := s.CreateOrg(model.Org{ID: "a", Name: "again", CreatedAt: t1}); err == nil {
		t.Fatal("重複的組織要拒絕")
	}
	_, err := s.GetOrg("zz")
	wantNotFound(t, "GetOrg", err)
	// UpdateOrg 只改名稱、圖示、顏色、備註,建立時間不變
	chk(t, s.UpdateOrg(model.Org{ID: "a", Name: "A2", Symbol: "gear", Color: "blue", Note: "n", CreatedAt: t1}))
	wantNotFound(t, "UpdateOrg 沒有的", s.UpdateOrg(model.Org{ID: "zz", Name: "z"}))

	for _, u := range []model.User{
		{ID: "u1", Email: "a@x", Name: "甲", Role: "member", CreatedAt: t1},
		{ID: "u2", Email: "b@x", Name: "乙", Role: "member", CreatedAt: t1},
		{ID: "u3", Email: "c@x", Name: "丙", Role: "member", CreatedAt: t1},
	} {
		chk(t, s.SaveUser(u))
	}
	added := mt("2026-01-03T00:00:00Z")
	chk(t, s.SaveMember(model.Member{Org: "a", UserID: "u2", Role: "member", AddedAt: added}))
	chk(t, s.SaveMember(model.Member{Org: "a", UserID: "u1", Role: "admin", AddedAt: added}))
	chk(t, s.SaveMember(model.Member{Org: "b", UserID: "u1", Role: "member", AddedAt: added}))
	chk(t, s.SaveMember(model.Member{Org: "c", UserID: "u2", Role: "member", AddedAt: added}))
	s = impl.reopen(t, s)

	l, err := s.ListOrgs()
	chk(t, err)
	sameJSON(t, "ListOrgs(建立時間、同時間依 id)", l, []model.Org{
		{ID: "b", Name: "B", CreatedAt: t1, Symbol: "star"},
		{ID: "c", Name: "C", CreatedAt: t1},
		{ID: "a", Name: "A2", CreatedAt: t2, Symbol: "gear", Color: "blue", Note: "n"},
	})
	if r, err := s.MemberRole("a", "u1"); err != nil || r != "admin" {
		t.Fatal("MemberRole", r, err)
	}
	_, err = s.MemberRole("a", "u3")
	wantNotFound(t, "MemberRole 不是成員", err)
	_, err = s.MemberRole("zz", "u1")
	wantNotFound(t, "MemberRole 沒有的組織", err)
	ms, err := s.ListMembers("a")
	chk(t, err)
	sameJSON(t, "ListMembers(依 email,帶 email、名字)", ms, []model.Member{
		{Org: "a", UserID: "u1", Email: "a@x", Name: "甲", Role: "admin", AddedAt: added},
		{Org: "a", UserID: "u2", Email: "b@x", Name: "乙", Role: "member", AddedAt: added},
	})
	if ms, err := s.ListMembers("zz"); err != nil || ms == nil || len(ms) != 0 {
		t.Fatalf("沒有成員要是 []:%#v %v", ms, err)
	}
	// 再存一次 = 改角色
	chk(t, s.SaveMember(model.Member{Org: "a", UserID: "u2", Role: "admin", AddedAt: added}))
	if r, _ := s.MemberRole("a", "u2"); r != "admin" {
		t.Fatal("改角色", r)
	}
	uo, err := s.OrgsOfUser("u1")
	chk(t, err)
	sameJSON(t, "OrgsOfUser(同 ListOrgs 的順序,帶 MyRole)", uo, []model.Org{
		{ID: "b", Name: "B", CreatedAt: t1, Symbol: "star", MyRole: "member"},
		{ID: "a", Name: "A2", CreatedAt: t2, Symbol: "gear", Color: "blue", Note: "n", MyRole: "admin"},
	})
	if uo, err := s.OrgsOfUser("u3"); err != nil || uo == nil || len(uo) != 0 {
		t.Fatalf("沒有組織要是 []:%#v %v", uo, err)
	}
	chk(t, s.RemoveMember("a", "u2"))
	wantNotFound(t, "RemoveMember 兩次", s.RemoveMember("a", "u2"))
	chk(t, s.DeleteOrg("a"))
	wantNotFound(t, "DeleteOrg 兩次", s.DeleteOrg("a"))
	s = impl.reopen(t, s)
	_, err = s.GetOrg("a")
	wantNotFound(t, "刪掉的組織", err)
	_, err = s.MemberRole("a", "u1")
	wantNotFound(t, "刪組織一併刪成員", err)
	if uo, _ := s.OrgsOfUser("u1"); len(uo) != 1 || uo[0].ID != "b" {
		t.Fatal("刪組織後的 OrgsOfUser", uo)
	}
	if l, _ := s.ListOrgs(); len(l) != 2 {
		t.Fatal("刪組織後的 ListOrgs", l)
	}
}

// ---- Issue ----

func suiteIssues(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o", "p")
	at := mt("2026-01-02T03:04:05Z")
	base := model.Issue{Title: "一", Status: "open", Priority: "normal", CreatedBy: "a@x", CreatedAt: at, UpdatedAt: at}
	i1, err := s.CreateIssue("o", base) // Labels、Comments 是 nil
	chk(t, err)
	full := model.Issue{Title: "二", Body: "內文", Status: "closed", Priority: "urgent", Labels: []string{"bug", "ui"}, Source: "app",
		Link: "https://x", CreatedBy: "b@x", CreatedAt: at, UpdatedAt: mt("2026-01-02T04:04:05Z"), ClosedAt: mtp("2026-01-02T05:04:05Z"),
		Assignee: "dev", Repo: "github:a/b", Comments: []model.Comment{{ID: "c1", Author: "a@x", Text: "hi", At: at}}}
	i2, err := s.CreateIssue("o", full)
	chk(t, err)
	b3 := base
	b3.Title = "三"
	i3, err := s.CreateIssue("o", b3)
	chk(t, err)
	if i1.ID != "ISS-1" || i2.ID != "ISS-2" || i3.ID != "ISS-3" {
		t.Fatal("建號要連號", i1.ID, i2.ID, i3.ID)
	}
	s = impl.reopen(t, s)
	l, err := s.ListIssues("o")
	chk(t, err)
	sameJSON(t, "ListIssues(新的在前)", issueIDs(l), []string{"ISS-3", "ISS-2", "ISS-1"})
	if l, err := s.ListIssues("zz"); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("沒有 Issue 要是 []:%#v %v", l, err)
	}
	w1 := base
	w1.ID, w1.Labels, w1.Comments = "ISS-1", []string{}, []model.Comment{}
	g, err := s.GetIssue("o", "ISS-1")
	chk(t, err)
	sameJSON(t, "nil 的 labels、留言讀回是 []", g, w1)
	w2 := full
	w2.ID = "ISS-2"
	g, err = s.GetIssue("o", "iss-2")
	chk(t, err)
	sameJSON(t, "所有欄位(labels、repo、assignee、closedAt、留言)", g, w2)
	_, err = s.GetIssue("p", "ISS-1")
	wantNotFound(t, "別的組織", err)
	_, err = s.GetIssue("o", "ISS-9")
	wantNotFound(t, "沒有的 Issue", err)

	// 修改:id 不分大小寫、位置不變、Comments nil 留言不動、其他欄位整筆取代
	u := w2
	u.ID, u.Title, u.Status, u.Labels, u.Repo, u.Assignee, u.ClosedAt, u.Comments = "iss-2", "二改", "open", nil, "", "", nil, nil
	chk(t, s.UpdateIssue("o", u))
	wantNotFound(t, "UpdateIssue 沒有的", s.UpdateIssue("o", model.Issue{ID: "ISS-9", Title: "x", Status: "open", Priority: "normal"}))
	s = impl.reopen(t, s)
	wu := u
	wu.ID, wu.Labels, wu.Comments = "ISS-2", []string{}, w2.Comments
	g, err = s.GetIssue("o", "ISS-2")
	chk(t, err)
	sameJSON(t, "UpdateIssue 後", g, wu)
	l, _ = s.ListIssues("o")
	sameJSON(t, "UpdateIssue 位置不變", issueIDs(l), []string{"ISS-3", "ISS-2", "ISS-1"})

	// 刪掉最大號後再建:號碼 = 剩下的最大號 + 1
	chk(t, s.DeleteIssue("o", "iss-3"))
	i4, err := s.CreateIssue("o", base)
	chk(t, err)
	if i4.ID != "ISS-3" {
		t.Fatal("刪掉後建號", i4.ID)
	}

	// 匯入:保留 id、作者、時間、留言順序;已經有 Issue 的組織拒絕;之後建號接續
	imp := []model.Issue{
		{ID: "ISS-10", Title: "十", Status: "open", Priority: "high", CreatedBy: "c@x", CreatedAt: at, UpdatedAt: at, Labels: []string{"x"},
			Comments: []model.Comment{{ID: "k1", Author: "c@x", Text: "1", At: at}, {ID: "k2", Author: "d@x", Text: "2", At: mt("2026-01-03T00:00:00Z")}}},
		{ID: "ISS-7", Title: "七", Status: "closed", Priority: "low", CreatedBy: "c@x", CreatedAt: at, UpdatedAt: at, ClosedAt: mtp("2026-01-04T00:00:00Z"),
			Labels: []string{}, Comments: []model.Comment{}},
	}
	if n, err := s.ImportIssues("p", imp); err != nil || n != 2 {
		t.Fatal("ImportIssues", n, err)
	}
	if _, err := s.ImportIssues("p", imp); err == nil {
		t.Fatal("已經有 Issue 要拒絕匯入")
	}
	if _, err := s.ImportIssues("o", imp[:1]); err == nil {
		t.Fatal("已經有 Issue 要拒絕匯入")
	}
	i5, err := s.CreateIssue("p", base)
	chk(t, err)
	if i5.ID != "ISS-11" {
		t.Fatal("匯入後建號", i5.ID)
	}
	s = impl.reopen(t, s)
	l, _ = s.ListIssues("p")
	sameJSON(t, "匯入後的順序", issueIDs(l), []string{"ISS-11", "ISS-10", "ISS-7"})
	sameJSON(t, "匯入的資料", l[1:], imp)

	// 可以重跑的匯入:已經有的 id(不分大小寫)略過或整筆取代(位置不變),新的依順序排在既有的後面
	m1 := []model.Issue{
		{ID: "ISS-10", Title: "十改", Status: "open", Priority: "high", CreatedBy: "c@x", CreatedAt: at, UpdatedAt: at, Labels: []string{}, Comments: []model.Comment{}},
		{ID: "ISS-20", Title: "二十", Status: "open", Priority: "normal", CreatedBy: "c@x", CreatedAt: at, UpdatedAt: at, Labels: []string{}, Comments: []model.Comment{}},
		{ID: "ISS-21", Title: "二一", Status: "open", Priority: "low", CreatedBy: "c@x", CreatedAt: at, UpdatedAt: at, Labels: []string{}, Comments: []model.Comment{}},
	}
	if n, err := s.MergeIssues("p", m1, false); err != nil || n != (MergeCounts{Inserted: 2, Skipped: 1}) {
		t.Fatal("MergeIssues", n, err)
	}
	ow := m1[0]
	ow.ID = "iss-10"
	if n, err := s.MergeIssues("p", []model.Issue{ow}, true); err != nil || n != (MergeCounts{Updated: 1}) {
		t.Fatal("MergeIssues overwrite", n, err)
	}
	s = impl.reopen(t, s)
	l, _ = s.ListIssues("p")
	sameJSON(t, "MergeIssues 的順序", issueIDs(l), []string{"ISS-11", "ISS-10", "ISS-7", "ISS-20", "ISS-21"})
	if l[1].Title != "十改" || len(l[1].Comments) != 0 {
		t.Fatal("MergeIssues 整筆取代", l[1])
	}
	newOrg(t, s, "q")
	if n, err := s.MergeIssues("q", m1[1:], false); err != nil || n.Inserted != 2 {
		t.Fatal("MergeIssues 空的組織", n, err)
	}
	l, _ = s.ListIssues("q")
	sameJSON(t, "MergeIssues 空的組織順序", issueIDs(l), []string{"ISS-20", "ISS-21"})
	chk(t, s.DeleteIssue("q", "ISS-20"))
	chk(t, s.DeleteIssue("q", "ISS-21"))
	if o, err := s.IssueOrgs(); err != nil {
		t.Fatal(err)
	} else {
		sameJSON(t, "IssueOrgs", o, []string{"o", "p"})
	}
}

// ---- 附件 ----

func suiteAttachments(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	_, err := s.GetAttachment("o", "h1")
	wantNotFound(t, "沒有的附件", err)
	chk(t, s.PutAttachment("o", "h1", []byte("png")))
	chk(t, s.PutAttachment("o", "h1", []byte("other"))) // 同 id 不覆蓋
	s = impl.reopen(t, s)
	if b, err := s.GetAttachment("o", "h1"); err != nil || string(b) != "png" {
		t.Fatal("GetAttachment", string(b), err)
	}
	_, err = s.GetAttachment("p", "h1")
	wantNotFound(t, "別的組織的附件", err)
}

// ---- 虛擬角色 ----

func suiteAgents(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o", "p")
	if l, err := s.ListAgents("o"); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("空的要是 []:%#v %v", l, err)
	}
	t1, t2 := mt("2026-01-01T00:00:00Z"), mt("2026-01-02T00:00:00Z")
	ops := model.Agent{ID: "ops", Name: "Ops", Role: "維運", Instructions: "看監控", Active: true, Symbol: "gear", Color: "blue", CreatedAt: t2,
		DocAccess: map[string]string{"docs": "read", "/docs/a": "write"},
		Routines:  []model.AgentRoutine{{ID: "r1", Kind: "daily", Hour: 9, Title: "早報", Enabled: true, Priority: "normal", CreatedAt: t1, LastRunAt: mtp("2026-01-03T09:00:00Z")}}}
	dev := model.Agent{ID: "dev", Name: "Dev", CreatedAt: t1} // DocAccess、Routines 是 nil
	// 先存建立時間比較晚的:列出時依建立時間,不是依存的順序
	chk(t, s.SaveAgent("o", ops))
	chk(t, s.SaveAgent("o", dev))
	s = impl.reopen(t, s)
	wdev := dev
	wdev.DocAccess, wdev.Routines = map[string]string{}, []model.AgentRoutine{}
	l, err := s.ListAgents("o")
	chk(t, err)
	sameJSON(t, "ListAgents(依建立時間;nil 讀回是 {}、[])", l, []model.Agent{wdev, ops})
	g, err := s.GetAgent("o", "ops")
	chk(t, err)
	sameJSON(t, "GetAgent", g, ops)
	_, err = s.GetAgent("o", "DEV")
	wantNotFound(t, "角色 id 大小寫要一致", err)
	_, err = s.GetAgent("p", "dev")
	wantNotFound(t, "別的組織", err)
	// 同 id 再存 = 整筆取代(建立時間不變)
	wdev.Name, wdev.Active, wdev.DocAccess = "Dev2", true, map[string]string{"docs": "none"}
	chk(t, s.SaveAgent("o", wdev))
	chk(t, s.DeleteAgent("o", "ops"))
	wantNotFound(t, "DeleteAgent 兩次", s.DeleteAgent("o", "ops"))
	s = impl.reopen(t, s)
	l, _ = s.ListAgents("o")
	sameJSON(t, "改過、刪過後", l, []model.Agent{wdev})
}

// ---- 角色任務(建號、整筆取代、領取的條件式更新、匯入) ----

func suiteAgentTasks(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o", "p")
	chk(t, s.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", CreatedAt: mt("2026-01-01T00:00:00Z")}))
	if l, err := s.ListAgentTasks("o"); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("空的要是 []:%#v %v", l, err)
	}
	at := mt("2026-01-02T03:04:05Z")
	base := model.AgentTask{AgentID: "dev", Title: "一", Priority: "normal", Status: "queued", CreatedBy: "a@x", CreatedAt: at, UpdatedAt: at}
	a, err := s.CreateAgentTask("o", base)
	chk(t, err)
	full := base
	full.Title, full.Instructions, full.Links, full.RoutineID = "二", "做事", []string{"ISS-1"}, strp("r1")
	full.Notes = []model.AgentTaskNote{{ID: "n1", Author: "dev", Text: "開始", At: at}}
	b, err := s.CreateAgentTask("o", full)
	chk(t, err)
	c, err := s.CreateAgentTask("o", base)
	chk(t, err)
	if a.ID != "T-1" || b.ID != "T-2" || c.ID != "T-3" {
		t.Fatal("建號要連號", a.ID, b.ID, c.ID)
	}
	s = impl.reopen(t, s)
	l, err := s.ListAgentTasks("o")
	chk(t, err)
	sameJSON(t, "ListAgentTasks(新的在前)", ids(l, func(x model.AgentTask) string { return x.ID }), []string{"T-3", "T-2", "T-1"})
	w1 := base
	w1.ID, w1.Links, w1.Notes = "T-1", []string{}, []model.AgentTaskNote{}
	g, err := s.GetAgentTask("o", "T-1")
	chk(t, err)
	sameJSON(t, "nil 的 links、notes 讀回是 []", g, w1)
	w2 := full
	w2.ID = "T-2"
	g, err = s.GetAgentTask("o", "T-2")
	chk(t, err)
	sameJSON(t, "GetAgentTask 所有欄位", g, w2)
	_, err = s.GetAgentTask("o", "t-1")
	wantNotFound(t, "任務 id 大小寫要一致", err)

	// 整筆取代
	u := w2
	u.Status, u.Result, u.CompletedAt, u.UpdatedAt = "done", "完成", mtp("2026-01-02T05:00:00Z"), mt("2026-01-02T05:00:00Z")
	u.Notes = append(u.Notes, model.AgentTaskNote{ID: "n2", Author: "dev", Text: "好了", At: u.UpdatedAt})
	chk(t, s.UpdateAgentTask("o", u))
	wantNotFound(t, "UpdateAgentTask 沒有的", s.UpdateAgentTask("o", model.AgentTask{ID: "T-9", AgentID: "dev", Title: "x", Status: "queued", CreatedAt: at, UpdatedAt: at}))
	// 選填時間給零值 = 沒有
	z := w1
	z.ID, z.ClaimedAt, z.CompletedAt, z.LockedUntil = "T-3", &model.Time{}, &model.Time{}, &model.Time{}
	chk(t, s.UpdateAgentTask("o", z))
	s = impl.reopen(t, s)
	g, _ = s.GetAgentTask("o", "T-2")
	sameJSON(t, "UpdateAgentTask 後", g, u)
	g, _ = s.GetAgentTask("o", "T-3")
	wz := w1
	wz.ID = "T-3"
	sameJSON(t, "零值時間讀回是 nil", g, wz)

	// 領取:進行中且鎖還沒到期就不改(ErrLocked);鎖到期(== now)就可以再領
	now := mt("2026-02-01T00:00:00Z").Time
	lock := func(t time.Time) *model.Time { x := model.Time{Time: t}; return &x }
	cl := w1
	cl.Status, cl.ClaimedBy, cl.ClaimedAt, cl.LockedUntil, cl.UpdatedAt = "inProgress", "w1", lock(now), lock(now.Add(10*time.Minute)), model.Time{Time: now}
	chk(t, s.ClaimAgentTask("o", cl, now))
	cl2 := cl
	cl2.ClaimedBy, cl2.LockedUntil = "w2", lock(now.Add(20*time.Minute))
	if err := s.ClaimAgentTask("o", cl2, now.Add(5*time.Minute)); !errors.Is(err, ErrLocked) {
		t.Fatal("鎖住時要 ErrLocked", err)
	}
	if g, _ := s.GetAgentTask("o", "T-1"); g.ClaimedBy != "w1" {
		t.Fatal("被擋下的領取不能改到任務", g.ClaimedBy)
	}
	chk(t, s.ClaimAgentTask("o", cl2, now.Add(10*time.Minute)))
	wantNotFound(t, "ClaimAgentTask 沒有的", s.ClaimAgentTask("o", model.AgentTask{ID: "T-9", AgentID: "dev", Title: "x", Status: "inProgress", CreatedAt: at, UpdatedAt: at}, now))
	// 不是進行中的任務,有鎖也可以領
	bl := wz
	bl.Status, bl.LockedUntil = "blocked", lock(now.Add(time.Hour))
	chk(t, s.UpdateAgentTask("o", bl))
	bl.Status, bl.ClaimedBy = "inProgress", "w3"
	chk(t, s.ClaimAgentTask("o", bl, now))
	s = impl.reopen(t, s)
	g, _ = s.GetAgentTask("o", "T-1")
	sameJSON(t, "領取後", g, cl2)
	if g, _ := s.GetAgentTask("o", "T-3"); g.ClaimedBy != "w3" {
		t.Fatal("blocked 的任務要能領", g.ClaimedBy)
	}

	// 匯入:保留 id 與內容;順序依號碼(新的在前);已經有角色或任務的組織拒絕;之後建號接續
	impT := []model.AgentTask{
		{ID: "T-2", AgentID: "x", Title: "二", Priority: "low", Status: "done", CreatedAt: at, UpdatedAt: at, Links: []string{}, Notes: []model.AgentTaskNote{}},
		{ID: "T-5", AgentID: "x", Title: "五", Priority: "high", Status: "queued", CreatedAt: at, UpdatedAt: at, Links: []string{"a"}, Notes: []model.AgentTaskNote{}},
	}
	chk(t, s.ImportAgents("p", []model.Agent{{ID: "x", Name: "X", CreatedAt: at}}, impT))
	if err := s.ImportAgents("p", nil, impT[:1]); err == nil {
		t.Fatal("已經有任務要拒絕匯入")
	}
	if err := s.ImportAgents("o", []model.Agent{{ID: "y", Name: "Y", CreatedAt: at}}, nil); err == nil {
		t.Fatal("已經有角色要拒絕匯入")
	}
	n, err := s.CreateAgentTask("p", model.AgentTask{AgentID: "x", Title: "六", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if n.ID != "T-6" {
		t.Fatal("匯入後建號", n.ID)
	}
	s = impl.reopen(t, s)
	l, _ = s.ListAgentTasks("p")
	sameJSON(t, "匯入後的順序(號碼大的在前)", ids(l, func(x model.AgentTask) string { return x.ID }), []string{"T-6", "T-5", "T-2"})
	sameJSON(t, "匯入的資料", []model.AgentTask{l[2], l[1]}, impT)
	if l, _ := s.ListAgents("p"); len(l) != 1 || l[0].ID != "x" {
		t.Fatal("匯入的角色", l)
	}
}

// ---- 角色憑證 ----

func suiteAgentCredentials(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o", "p")
	at := mt("2026-01-02T03:04:05Z")
	far := mt("2099-01-01T00:00:00Z")
	c1 := model.AgentCredential{Org: "o", AgentID: "dev", TaskID: "T-1", Hash: []byte{1}, IssuedAt: at, ExpiresAt: far}
	chk(t, s.SaveAgentCredential(c1))
	s = impl.reopen(t, s)
	g, err := s.AgentCredentialByHash([]byte{1})
	chk(t, err)
	sameJSON(t, "AgentCredentialByHash", g, c1)
	if !bytes.Equal(g.Hash, []byte{1}) {
		t.Fatal("雜湊", g.Hash)
	}
	g.Hash[0] = 99 // 拿回去改不影響存著的
	g, err = s.AgentCredentialForTask("o", "T-1")
	chk(t, err)
	sameJSON(t, "AgentCredentialForTask", g, c1)
	if !bytes.Equal(g.Hash, []byte{1}) {
		t.Fatal("雜湊被改到了", g.Hash)
	}
	_, err = s.AgentCredentialForTask("o", "T-9")
	wantNotFound(t, "沒有憑證的任務", err)
	_, err = s.AgentCredentialForTask("p", "T-1")
	wantNotFound(t, "別的組織", err)
	_, err = s.AgentCredentialByHash([]byte{9})
	wantNotFound(t, "沒有的雜湊", err)

	// 同任務再存:舊的作廢
	c1b := c1
	c1b.Hash = []byte{2}
	chk(t, s.SaveAgentCredential(c1b))
	_, err = s.AgentCredentialByHash([]byte{1})
	wantNotFound(t, "同任務的舊憑證", err)
	if g, err := s.AgentCredentialForTask("o", "T-1"); err != nil || !bytes.Equal(g.Hash, []byte{2}) {
		t.Fatal("同任務只留新的", g, err)
	}
	// 延長
	ext := mt("2099-06-01T00:00:00Z")
	chk(t, s.ExtendAgentCredential("o", "T-1", ext.Time))
	chk(t, s.ExtendAgentCredential("o", "T-9", ext.Time)) // 沒有憑證就什麼都不做
	s = impl.reopen(t, s)
	if g, _ := s.AgentCredentialForTask("o", "T-1"); !g.ExpiresAt.Equal(ext.Time) {
		t.Fatal("ExtendAgentCredential", g.ExpiresAt)
	}

	// 刪除:依任務、依角色;兩個都空什麼都不刪;只刪這個組織的
	for _, c := range []model.AgentCredential{
		{Org: "o", AgentID: "dev", TaskID: "T-2", Hash: []byte{3}, IssuedAt: at, ExpiresAt: far},
		{Org: "o", AgentID: "ops", TaskID: "T-3", Hash: []byte{4}, IssuedAt: at, ExpiresAt: far},
		{Org: "p", AgentID: "dev", TaskID: "T-1", Hash: []byte{5}, IssuedAt: at, ExpiresAt: far},
	} {
		chk(t, s.SaveAgentCredential(c))
	}
	alive := func(want ...byte) {
		t.Helper()
		var got []byte
		for h := byte(1); h <= 20; h++ {
			if _, err := s.AgentCredentialByHash([]byte{h}); err == nil {
				got = append(got, h)
			}
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("還在的憑證 %v,要 %v", got, want)
		}
	}
	alive(2, 3, 4, 5)
	chk(t, s.DeleteAgentCredentials("o", "", ""))
	alive(2, 3, 4, 5)
	chk(t, s.DeleteAgentCredentials("o", "T-1", ""))
	alive(3, 4, 5)
	chk(t, s.DeleteAgentCredentials("o", "", "dev"))
	alive(4, 5)

	// 清掉到期的:到期時間 <= now 的刪掉
	now := mt("2026-06-01T00:00:00Z")
	for k, exp := range []time.Time{now.Add(-time.Second), now.Time, now.Add(time.Second)} {
		chk(t, s.SaveAgentCredential(model.AgentCredential{Org: "o", AgentID: "dev", TaskID: fmt.Sprintf("T-%d", 10+k),
			Hash: []byte{byte(6 + k)}, IssuedAt: at, ExpiresAt: model.Time{Time: exp}}))
	}
	alive(4, 5, 6, 7, 8)
	chk(t, s.PruneAgentCredentials(now.Time))
	s = impl.reopen(t, s)
	alive(4, 5, 8)
}

// ---- 組織鏈 ----

func suiteOrgChain(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o", "p")
	_, err := s.GetOrgChain("o")
	wantNotFound(t, "沒存過", err)
	c := model.OrgChain{Edges: []model.ChainEdge{{From: "user:a@x", To: "agent:dev", Label: "派工"}, {From: "agent:dev", To: "agent:ops"}},
		UpdatedAt: mtp("2026-01-02T03:04:05Z")}
	chk(t, s.SaveOrgChain("o", c))
	s = impl.reopen(t, s)
	g, err := s.GetOrgChain("o")
	chk(t, err)
	sameJSON(t, "GetOrgChain", g, c)
	_, err = s.GetOrgChain("p")
	wantNotFound(t, "別的組織", err)
	// 整份取代;nil 的 edges 讀回是 []
	chk(t, s.SaveOrgChain("o", model.OrgChain{UpdatedAt: mtp("2026-01-03T00:00:00Z")}))
	s = impl.reopen(t, s)
	g, _ = s.GetOrgChain("o")
	sameJSON(t, "nil 的 edges", g, model.OrgChain{Edges: []model.ChainEdge{}, UpdatedAt: mtp("2026-01-03T00:00:00Z")})
}

// ---- 文件櫃 ----

func suiteCabinet(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o", "p")
	_, err := s.GetCabinetLayout("o")
	wantNotFound(t, "沒存過結構", err)
	layout := []model.CabinetSection{
		{ID: "s1", Label: "一", Path: "/s1", Tabs: []model.CabinetTab{{Label: "介紹", Path: "/s1/a", Code: strp("L0")}, {Label: "群組", Path: "/s1/b", Group: strp("g")}}},
		{ID: "s2", Label: "二", Path: "/s2"}, // Tabs 是 nil
	}
	chk(t, s.SaveCabinetLayout("o", layout))
	s = impl.reopen(t, s)
	want := model.CloneLayout(layout)
	want[1].Tabs = []model.CabinetTab{}
	g, err := s.GetCabinetLayout("o")
	chk(t, err)
	sameJSON(t, "GetCabinetLayout(nil 的 tabs 讀回是 [])", g, want)
	chk(t, s.SaveCabinetLayout("p", nil))
	s = impl.reopen(t, s)
	if g, err := s.GetCabinetLayout("p"); err != nil || g == nil || len(g) != 0 {
		t.Fatalf("存 nil 讀回是 []:%#v %v", g, err)
	}

	if l, err := s.ListDocs("o"); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("空的要是 []:%#v %v", l, err)
	}
	at := mt("2026-01-02T03:04:05Z")
	d1 := model.CabinetDoc{Title: "一", Body: "內文", Section: "s1", Author: "a@x", CreatedAt: at, UpdatedAt: at} // Tab、Tags 是 nil
	d2 := model.CabinetDoc{Title: "二", Section: "s1", Tab: strp("/s1/a"), Tags: []string{"a", "b"}, Author: "b@x", CreatedAt: at, UpdatedAt: at}
	x1, err := s.CreateDoc("o", d1)
	chk(t, err)
	x2, err := s.CreateDoc("o", d2)
	chk(t, err)
	if x1.ID != "DOC-1" || x2.ID != "DOC-2" {
		t.Fatal("建號要連號", x1.ID, x2.ID)
	}
	s = impl.reopen(t, s)
	l, err := s.ListDocs("o")
	chk(t, err)
	w1, w2 := d1, d2
	w1.ID, w1.Tags, w2.ID = "DOC-1", []string{}, "DOC-2"
	sameJSON(t, "ListDocs(新的在前;nil 的 tags 讀回是 [])", l, []model.CabinetDoc{w2, w1})
	g1, err := s.GetDoc("o", "DOC-1")
	chk(t, err)
	sameJSON(t, "GetDoc", g1, w1)
	_, err = s.GetDoc("o", "doc-1")
	wantNotFound(t, "文件 id 大小寫要一致", err)
	_, err = s.GetDoc("p", "DOC-1")
	wantNotFound(t, "別的組織", err)
	// 整筆取代(建立時間不變),位置不變
	u := w1
	u.Title, u.Body, u.Tab, u.Tags, u.UpdatedAt = "一改", "新內文", strp("/s1/b"), []string{"z"}, mt("2026-01-03T00:00:00Z")
	chk(t, s.UpdateDoc("o", u))
	wantNotFound(t, "UpdateDoc 沒有的", s.UpdateDoc("o", model.CabinetDoc{ID: "DOC-9", Title: "x", Section: "s1", CreatedAt: at, UpdatedAt: at}))
	s = impl.reopen(t, s)
	l, _ = s.ListDocs("o")
	sameJSON(t, "UpdateDoc 後", l, []model.CabinetDoc{w2, u})
	// 刪除;刪掉最大號後再建
	chk(t, s.DeleteDoc("o", "DOC-2"))
	wantNotFound(t, "DeleteDoc 兩次", s.DeleteDoc("o", "DOC-2"))
	x3, err := s.CreateDoc("o", d1)
	chk(t, err)
	if x3.ID != "DOC-2" {
		t.Fatal("刪掉後建號", x3.ID)
	}
	// 匯入
	imp := []model.CabinetDoc{
		{ID: "DOC-8", Title: "八", Section: "s1", Tags: []string{}, Author: "c@x", CreatedAt: at, UpdatedAt: at},
		{ID: "DOC-3", Title: "三", Section: "s2", Tab: strp("/s2/x"), Tags: []string{"t"}, Author: "c@x", CreatedAt: at, UpdatedAt: at},
	}
	if n, err := s.ImportDocs("p", imp); err != nil || n != 2 {
		t.Fatal("ImportDocs", n, err)
	}
	if _, err := s.ImportDocs("p", imp); err == nil {
		t.Fatal("已經有文件要拒絕匯入")
	}
	x4, err := s.CreateDoc("p", d1)
	chk(t, err)
	if x4.ID != "DOC-9" {
		t.Fatal("匯入後建號", x4.ID)
	}
	s = impl.reopen(t, s)
	l, _ = s.ListDocs("p")
	sameJSON(t, "匯入後的順序", ids(l, func(d model.CabinetDoc) string { return d.ID }), []string{"DOC-9", "DOC-8", "DOC-3"})
	sameJSON(t, "匯入的資料", l[1:], imp)
}

// ---- 訊息頻道與訊息(分頁、since、已讀、未讀、提及) ----

func msgIDs(l []model.ChannelMessage) []string {
	return ids(l, func(m model.ChannelMessage) string { return m.ChannelID + "/" + m.ID })
}

func suiteChannels(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o", "p")
	if l, err := s.ListChannels("o"); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("空的要是 []:%#v %v", l, err)
	}
	at := mt("2026-01-02T03:04:05Z")
	c1, err := s.CreateChannel("o", model.Channel{Name: "一般", Topic: "閒聊", Members: []string{"user:a", "user:b"}, CreatedBy: "user:a", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	c2, err := s.CreateChannel("o", model.Channel{Name: "空", CreatedBy: "user:a", CreatedAt: at, UpdatedAt: at}) // Members 是 nil
	chk(t, err)
	if c1.ID != "CH-1" || c2.ID != "CH-2" {
		t.Fatal("建號要連號", c1.ID, c2.ID)
	}
	s = impl.reopen(t, s)
	l, err := s.ListChannels("o")
	chk(t, err)
	w2 := c2
	w2.Members = []string{}
	sameJSON(t, "ListChannels(依建立順序;nil 的成員讀回是 [])", l, []model.Channel{c1, w2})
	_, err = s.GetChannel("o", "ch-1")
	wantNotFound(t, "頻道 id 大小寫要一致", err)
	_, err = s.GetChannel("p", "CH-1")
	wantNotFound(t, "別的組織", err)
	u := c1
	u.Name, u.Topic, u.IssueID, u.Members, u.UpdatedAt = "一般2", "新主題", "ISS-1", []string{"user:a", "user:b", "agent:dev"}, mt("2026-01-03T00:00:00Z")
	chk(t, s.UpdateChannel("o", u))
	wantNotFound(t, "UpdateChannel 沒有的", s.UpdateChannel("o", model.Channel{ID: "CH-9", Name: "x", CreatedAt: at, UpdatedAt: at}))

	// CH-1:5 則(M-3、M-4 同一秒),CH-2:1 則;每個頻道各自編號
	type in struct {
		ch, author string
		mentions   []string
		at         string
	}
	for _, m := range []in{
		{"CH-1", "user:a", nil, "2026-01-05T00:00:01Z"},
		{"CH-1", "user:b", []string{"user:a"}, "2026-01-05T00:00:02Z"},
		{"CH-1", "user:b", []string{"user:a"}, "2026-01-05T00:00:03Z"},
		{"CH-1", "user:b", []string{"user:a", "user:c"}, "2026-01-05T00:00:03Z"},
		{"CH-1", "user:a", []string{"user:b"}, "2026-01-05T00:00:04Z"},
		{"CH-2", "user:c", []string{"user:a"}, "2026-01-05T00:00:10Z"},
	} {
		_, err := s.CreateChannelMessage("o", model.ChannelMessage{ChannelID: m.ch, Author: m.author, Text: "hi " + m.at, Mentions: m.mentions, At: mt(m.at)})
		chk(t, err)
	}
	s = impl.reopen(t, s)
	g, err := s.GetChannel("o", "CH-1")
	chk(t, err)
	sameJSON(t, "UpdateChannel 後", g, u)
	all, more, err := s.ListChannelMessages("o", "CH-1", model.MessageQuery{})
	chk(t, err)
	if more || len(all) != 5 {
		t.Fatal("全部", len(all), more)
	}
	if all[0].Mentions == nil || len(all[0].Mentions) != 0 || all[0].Author != "user:a" || !all[0].At.Equal(mt("2026-01-05T00:00:01Z").Time) {
		t.Fatalf("第一則(nil 的提及讀回是 []):%#v", all[0])
	}
	if len(all[3].Mentions) != 2 || all[3].Mentions[1] != "user:c" {
		t.Fatal("提及", all[3].Mentions)
	}
	for _, c := range []struct {
		q    model.MessageQuery
		want []string
		more bool
	}{
		{model.MessageQuery{Limit: 2}, []string{"M-1", "M-2"}, true},
		{model.MessageQuery{Limit: 2, Latest: true}, []string{"M-4", "M-5"}, true},
		{model.MessageQuery{Limit: 5}, []string{"M-1", "M-2", "M-3", "M-4", "M-5"}, false},
		{model.MessageQuery{Limit: 5, Latest: true}, []string{"M-1", "M-2", "M-3", "M-4", "M-5"}, false},
		{model.MessageQuery{After: 2}, []string{"M-3", "M-4", "M-5"}, false},
		{model.MessageQuery{After: 2, Limit: 2}, []string{"M-3", "M-4"}, true},
		{model.MessageQuery{After: 5}, []string{}, false},
		{model.MessageQuery{Before: 4, Limit: 2, Latest: true}, []string{"M-2", "M-3"}, true},
		{model.MessageQuery{Before: 3}, []string{"M-1", "M-2"}, false},
		{model.MessageQuery{After: 1, Before: 3}, []string{"M-2"}, false},
	} {
		l, more, err := s.ListChannelMessages("o", "CH-1", c.q)
		chk(t, err)
		got := ids(l, func(m model.ChannelMessage) string { return m.ID })
		if fmt.Sprint(got) != fmt.Sprint(c.want) || more != c.more || l == nil {
			t.Fatalf("ListChannelMessages %+v:拿到 %v more=%v,要 %v more=%v", c.q, got, more, c.want, c.more)
		}
	}
	if l, more, err := s.ListChannelMessages("o", "CH-9", model.MessageQuery{Limit: 10}); err != nil || l == nil || len(l) != 0 || more {
		t.Fatalf("沒有的頻道要是 []:%#v %v %v", l, more, err)
	}
	last, err := s.LastChannelMessages("o")
	chk(t, err)
	if len(last) != 2 || last["CH-1"].ID != "M-5" || last["CH-2"].ID != "M-1" || last["CH-2"].ChannelID != "CH-2" {
		t.Fatal("LastChannelMessages", last)
	}
	if last, _ := s.LastChannelMessages("p"); last == nil || len(last) != 0 {
		t.Fatal("沒有訊息要是 {}", last)
	}

	// 已讀
	if r, err := s.ChannelReads("o", "user:a"); err != nil || r == nil || len(r) != 0 {
		t.Fatalf("沒有已讀要是 {}:%#v %v", r, err)
	}
	chk(t, s.SaveChannelRead("o", model.ChannelRead{ChannelID: "CH-1", Member: "user:a", LastRead: 1, At: at}))
	chk(t, s.SaveChannelRead("o", model.ChannelRead{ChannelID: "CH-1", Member: "user:a", LastRead: 2, At: mt("2026-01-06T00:00:00Z")}))
	chk(t, s.SaveChannelRead("o", model.ChannelRead{ChannelID: "CH-1", Member: "user:b", LastRead: 5, At: at}))
	s = impl.reopen(t, s)
	r, err := s.ChannelReads("o", "user:a")
	chk(t, err)
	sameJSON(t, "ChannelReads(取代)", r, map[string]model.ChannelRead{"CH-1": {ChannelID: "CH-1", Member: "user:a", LastRead: 2, At: mt("2026-01-06T00:00:00Z")}})

	// 未讀數:號碼 > 已讀、作者不是自己(0 的不一定在裡面)
	nonzero := func(m map[string]int) map[string]int {
		out := map[string]int{}
		for k, v := range m {
			if v != 0 {
				out[k] = v
			}
		}
		return out
	}
	uc, err := s.UnreadCounts("o", "user:a", []string{"CH-1", "CH-2"})
	chk(t, err)
	sameJSON(t, "UnreadCounts user:a", nonzero(uc), map[string]int{"CH-1": 2, "CH-2": 1})
	uc, _ = s.UnreadCounts("o", "user:b", []string{"CH-1"})
	sameJSON(t, "UnreadCounts user:b(都讀了)", nonzero(uc), map[string]int{})
	uc, _ = s.UnreadCounts("o", "user:c", []string{"CH-1"})
	sameJSON(t, "UnreadCounts user:c(沒有已讀)", nonzero(uc), map[string]int{"CH-1": 5})
	if uc, err := s.UnreadCounts("o", "user:a", nil); err != nil || uc == nil || len(uc) != 0 {
		t.Fatal("沒給頻道要是 {}", uc, err)
	}
	// 提及:時間新的在前,同一秒號碼大的在前;limit
	um, err := s.UnreadMentions("o", "user:a", []string{"CH-1", "CH-2"}, 10)
	chk(t, err)
	sameJSON(t, "UnreadMentions", msgIDs(um), []string{"CH-2/M-1", "CH-1/M-4", "CH-1/M-3"})
	um, _ = s.UnreadMentions("o", "user:a", []string{"CH-1", "CH-2"}, 2)
	sameJSON(t, "UnreadMentions limit", msgIDs(um), []string{"CH-2/M-1", "CH-1/M-4"})
	um, _ = s.UnreadMentions("o", "user:c", []string{"CH-1"}, 10)
	sameJSON(t, "UnreadMentions user:c", msgIDs(um), []string{"CH-1/M-4"})
	if um, err := s.UnreadMentions("o", "user:a", nil, 10); err != nil || um == nil || len(um) != 0 {
		t.Fatal("沒給頻道要是 []", um, err)
	}
}

// ---- 刪組織:這個組織的資料全部刪掉,別的組織與系統層設定不受影響;同 id 重建後是空的 ----

func fillOrg(t *testing.T, s Store, org string, hash byte) {
	t.Helper()
	at := mt("2026-01-02T03:04:05Z")
	newOrg(t, s, org)
	chk(t, s.SaveMember(model.Member{Org: org, UserID: "u1", Role: "admin", AddedAt: at}))
	iss, err := s.CreateIssue(org, model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "a@x", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	_, err = s.AddIssueComment(org, iss.ID, model.Comment{ID: "c1", Author: "a@x", Text: "hi", At: at})
	chk(t, err)
	chk(t, s.PutAttachment(org, "h", []byte("png")))
	chk(t, s.SaveAgent(org, model.Agent{ID: "dev", Name: "Dev", CreatedAt: at}))
	tk, err := s.CreateAgentTask(org, model.AgentTask{AgentID: "dev", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	chk(t, s.SaveAgentCredential(model.AgentCredential{Org: org, AgentID: "dev", TaskID: tk.ID, Hash: []byte{hash}, IssuedAt: at, ExpiresAt: mt("2099-01-01T00:00:00Z")}))
	chk(t, s.SaveCabinetLayout(org, []model.CabinetSection{{ID: "s", Tabs: []model.CabinetTab{}}}))
	_, err = s.CreateDoc(org, model.CabinetDoc{Title: "d", Section: "s", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	chk(t, s.SaveOrgChain(org, model.OrgChain{Edges: []model.ChainEdge{{From: "a", To: "b"}}, UpdatedAt: &at}))
	ch, err := s.CreateChannel(org, model.Channel{Name: "c", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	_, err = s.CreateChannelMessage(org, model.ChannelMessage{ChannelID: ch.ID, Author: "user:a", Text: "hi", At: at})
	chk(t, err)
	chk(t, s.SaveChannelRead(org, model.ChannelRead{ChannelID: ch.ID, Member: "user:a", LastRead: 1, At: at}))
	_, err = s.CreateIncident(org, model.Incident{Title: "i", Sev: 2, Status: "investigating", StartedAt: at})
	chk(t, err)
	chk(t, s.SaveOrgDoc(org, "github", OrgDoc{Data: json.RawMessage(`{"repos":["a/b"]}`), UpdatedAt: &at}))
	chk(t, s.SaveMCPConfig(org, model.OrgMCPConfig{Servers: []model.MCPServerReg{{ID: "fs", Name: "fs", Transport: "stdio", Command: "x"}}, UpdatedAt: &at}))
	chk(t, s.SaveSystemSettings(org, model.OrgSystemSettings{Features: map[string]bool{"channels": false}, UpdatedAt: &at}))
}

// orgEmpty 這個組織(連組織本身)沒有任何資料
func orgEmpty(t *testing.T, s Store, org string, hash byte, keepsAttachments bool) {
	t.Helper()
	_, err := s.GetOrg(org)
	wantNotFound(t, "GetOrg", err)
	_, err = s.MemberRole(org, "u1")
	wantNotFound(t, "MemberRole", err)
	empty := func(what string, n int, err error) {
		t.Helper()
		if err != nil || n != 0 {
			t.Fatalf("%s 要是空的:%d %v", what, n, err)
		}
	}
	ms, err := s.ListMembers(org)
	empty("ListMembers", len(ms), err)
	is, err := s.ListIssues(org)
	empty("ListIssues", len(is), err)
	// File 刪組織時附件檔留在磁碟上(見 File 的說明;有意的差異)
	if _, err = s.GetAttachment(org, "h"); !keepsAttachments {
		wantNotFound(t, "GetAttachment", err)
	}
	as, err := s.ListAgents(org)
	empty("ListAgents", len(as), err)
	ts, err := s.ListAgentTasks(org)
	empty("ListAgentTasks", len(ts), err)
	_, err = s.AgentCredentialByHash([]byte{hash})
	wantNotFound(t, "AgentCredentialByHash", err)
	_, err = s.GetCabinetLayout(org)
	wantNotFound(t, "GetCabinetLayout", err)
	ds, err := s.ListDocs(org)
	empty("ListDocs", len(ds), err)
	_, err = s.GetOrgChain(org)
	wantNotFound(t, "GetOrgChain", err)
	cs, err := s.ListChannels(org)
	empty("ListChannels", len(cs), err)
	ml, _, err := s.ListChannelMessages(org, "CH-1", model.MessageQuery{})
	empty("ListChannelMessages", len(ml), err)
	rs, err := s.ChannelReads(org, "user:a")
	empty("ChannelReads", len(rs), err)
	ins, err := s.ListIncidents(org)
	empty("ListIncidents", len(ins), err)
	_, err = s.GetOrgDoc(org, "github")
	wantNotFound(t, "GetOrgDoc", err)
	_, err = s.GetMCPConfig(org)
	wantNotFound(t, "GetMCPConfig", err)
	_, err = s.GetSystemSettings(org)
	wantNotFound(t, "GetSystemSettings", err)
}

func suiteDeleteOrgCascade(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	chk(t, s.SaveUser(model.User{ID: "u1", Email: "a@x", Role: "member", CreatedAt: mt("2026-01-01T00:00:00Z")}))
	chk(t, s.SaveServerFeatures(model.ServerFeatures{Features: map[string]bool{"cabinet": false}, UpdatedAt: mtp("2026-01-01T00:00:00Z")}))
	fillOrg(t, s, "o", 1)
	fillOrg(t, s, "p", 2)
	chk(t, s.DeleteOrg("o"))
	s = impl.reopen(t, s)
	orgEmpty(t, s, "o", 1, impl.name == "file")
	if o, _ := s.IssueOrgs(); len(o) != 1 || o[0] != "p" {
		t.Fatal("IssueOrgs", o)
	}
	// 別的組織不受影響
	if l, _ := s.ListIssues("p"); len(l) != 1 || len(l[0].Comments) != 1 {
		t.Fatal("別的組織的 Issue", l)
	}
	if _, err := s.AgentCredentialByHash([]byte{2}); err != nil {
		t.Fatal("別的組織的憑證", err)
	}
	if _, err := s.GetMCPConfig("p"); err != nil {
		t.Fatal("別的組織的 MCP 登記", err)
	}
	if _, err := s.GetSystemSettings("p"); err != nil {
		t.Fatal("別的組織的系統設定", err)
	}
	if f, err := s.GetServerFeatures(); err != nil || f.Features["cabinet"] {
		t.Fatal("系統層設定不受刪組織影響", f, err)
	}
	// 同 id 重建:是空的,號碼從頭開始
	newOrg(t, s, "o")
	if i, err := s.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "a@x", CreatedAt: mt("2026-01-01T00:00:00Z"), UpdatedAt: mt("2026-01-01T00:00:00Z")}); err != nil || i.ID != "ISS-1" {
		t.Fatal("重建後的 Issue", i.ID, err)
	}
}

// ---- 並發:同時建號不重複、同時領取只有一個成功 ----

func suiteConcurrentCreate(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	at := mt("2026-01-02T03:04:05Z")
	ch, err := s.CreateChannel("o", model.Channel{Name: "c", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	const n = 25
	var mu sync.Mutex
	got := map[string][]string{}
	var wg sync.WaitGroup
	errs := make(chan error, 6*n)
	add := func(kind, id string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs <- err
			return
		}
		got[kind] = append(got[kind], id)
	}
	for range n {
		wg.Add(6)
		go func() {
			defer wg.Done()
			x, err := s.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "a@x", CreatedAt: at, UpdatedAt: at})
			add("issue", x.ID, err)
		}()
		go func() {
			defer wg.Done()
			x, err := s.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at})
			add("task", x.ID, err)
		}()
		go func() {
			defer wg.Done()
			x, err := s.CreateDoc("o", model.CabinetDoc{Title: "d", Section: "s", CreatedAt: at, UpdatedAt: at})
			add("doc", x.ID, err)
		}()
		go func() {
			defer wg.Done()
			x, err := s.CreateChannel("o", model.Channel{Name: "c", CreatedAt: at, UpdatedAt: at})
			add("channel", x.ID, err)
		}()
		go func() {
			defer wg.Done()
			x, err := s.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Author: "user:a", Text: "hi", At: at})
			add("message", x.ID, err)
		}()
		go func() {
			defer wg.Done()
			x, err := s.CreateIncident("o", model.Incident{Title: "i", Sev: 3, Status: "investigating", StartedAt: at})
			add("incident", x.ID, err)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	expect := func(kind, prefix string, from int) {
		t.Helper()
		l := got[kind]
		sort.Strings(l)
		want := []string{}
		for k := range n {
			want = append(want, fmt.Sprintf("%s%d", prefix, from+k))
		}
		sort.Strings(want)
		if fmt.Sprint(l) != fmt.Sprint(want) {
			t.Fatalf("%s 的號碼重複或跳號:%v", kind, l)
		}
	}
	expect("issue", "ISS-", 1)
	expect("task", "T-", 1)
	expect("doc", "DOC-", 1)
	expect("channel", "CH-", 2)
	expect("message", "M-", 1)
	expect("incident", "INC-", model.IncidentNumberBase+1)
	s = impl.reopen(t, s)
	if l, _ := s.ListIssues("o"); len(l) != n {
		t.Fatal("Issue 數", len(l))
	}
	if l, _, _ := s.ListChannelMessages("o", ch.ID, model.MessageQuery{}); len(l) != n {
		t.Fatal("訊息數", len(l))
	}
}

func suiteConcurrentClaim(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	at := mt("2026-01-02T03:04:05Z")
	tk, err := s.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	now := mt("2026-02-01T00:00:00Z").Time
	until := model.Time{Time: now.Add(10 * time.Minute)}
	const n = 20
	var wg sync.WaitGroup
	res := make(chan string, n)
	errs := make(chan error, n)
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := tk
			c.Status, c.ClaimedBy, c.LockedUntil = "inProgress", fmt.Sprintf("w%d", k), &until
			err := s.ClaimAgentTask("o", c, now)
			switch {
			case err == nil:
				res <- c.ClaimedBy
			case !errors.Is(err, ErrLocked):
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(res)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var winners []string
	for w := range res {
		winners = append(winners, w)
	}
	if len(winners) != 1 {
		t.Fatalf("同時領取只能有一個成功:%v", winners)
	}
	s = impl.reopen(t, s)
	if g, _ := s.GetAgentTask("o", tk.ID); g.ClaimedBy != winners[0] || g.Status != "inProgress" {
		t.Fatal("領到的人", g.ClaimedBy, winners)
	}
}

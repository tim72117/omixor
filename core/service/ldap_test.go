package service_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"omixor-core/ldapauth"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

// fakeDirectory 假的 LDAP 目錄:帳號 → 密碼與資料
type fakeDirectory struct {
	users map[string]fakeEntry
	err   error // 非 nil:模擬連不上等設定問題
	calls int
}
type fakeEntry struct{ pw, name, mail string }

func (f *fakeDirectory) Authenticate(username, password string) (*ldapauth.Identity, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	u, ok := f.users[username]
	if !ok || u.pw != password {
		return nil, ldapauth.ErrInvalidCredentials
	}
	return &ldapauth.Identity{ID: username, Name: u.name, Email: u.mail, DN: "cn=" + username}, nil
}

func ldapEnv(t *testing.T) (*service.Service, *fakeDirectory, store.Store) {
	t.Helper()
	st := storetest.New(t)
	dir := &fakeDirectory{users: map[string]fakeEntry{
		"amy":   {pw: "pw-amy", name: "Amy Lin", mail: "Amy@Corp.Test"},
		"boss":  {pw: "pw-boss", name: "Boss", mail: "boss@corp.test"},
		"noml":  {pw: "pw-n", name: "No Mail"},
		"carol": {pw: "pw-c", name: "Carol", mail: "carol@corp.test"},
	}}
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	svc := &service.Service{Store: st, TTL: 90 * 24 * time.Hour, LDAP: dir, LDAPAdmins: []string{"boss"},
		LoginPerIP: 1000, LoginTotal: 1000, Now: func() time.Time { return now }}
	return svc, dir, st
}

func statusOf(err error) int { return service.Code(err) }

func TestLoginWithLDAP(t *testing.T) {
	svc, _, st := ldapEnv(t)

	// 第一次通過:自動建成一般使用者(email 轉小寫),token 能用
	res, err := svc.LoginWithLDAP(" Amy ", "pw-amy", "我的 Mac", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if res.User.Email != "amy@corp.test" || res.User.Role != "member" || res.User.Name != "Amy Lin" || res.User.EvoleapID != "ldap:amy" {
		t.Fatalf("使用者:%+v", res.User)
	}
	p, err := svc.Authenticate(res.Token)
	if err != nil || p.User.ID != res.User.ID {
		t.Fatalf("token 不能用:%v", err)
	}
	if !strings.HasPrefix(res.Token, service.TokenPrefix) || res.ExpiresAt == nil {
		t.Fatalf("token:%+v", res)
	}
	// 沒有任何組織:一般使用者要管理員用 email 加進組織
	if orgs, _ := svc.ListOrgs(p); len(orgs) != 0 {
		t.Fatalf("不該有組織:%v", orgs)
	}

	// 再登入:同一個人(不重複建立),LastLoginAt 更新
	res2, err := svc.LoginWithLDAP("amy", "pw-amy", "", "1.1.1.1")
	if err != nil || res2.User.ID != res.User.ID {
		t.Fatalf("第二次:%+v %v", res2, err)
	}
	if n, _ := st.CountUsers(); n != 1 {
		t.Fatalf("使用者數 %d", n)
	}

	// 密碼錯、帳號不存在:同一句話,不洩漏帳號存不存在
	_, e1 := svc.LoginWithLDAP("amy", "wrong", "", "1.1.1.1")
	_, e2 := svc.LoginWithLDAP("nobody", "x", "", "1.1.1.1")
	if statusOf(e1) != 401 || statusOf(e2) != 401 || e1.Error() != e2.Error() {
		t.Fatalf("認證失敗:%v / %v", e1, e2)
	}
	if _, e := svc.LoginWithLDAP("", "x", "", "1.1.1.1"); statusOf(e) != 400 {
		t.Fatalf("沒給帳號:%v", e)
	}
	if _, e := svc.LoginWithLDAP("amy", "", "", "1.1.1.1"); statusOf(e) != 400 {
		t.Fatalf("沒給密碼:%v", e)
	}
}

func TestLoginWithLDAPAdmins(t *testing.T) {
	svc, _, st := ldapEnv(t)

	// 名單上的人:第一次登入就是伺服器管理員
	res, err := svc.LoginWithLDAP("boss", "pw-boss", "", "1.1.1.1")
	if err != nil || res.User.Role != "admin" {
		t.Fatalf("boss:%+v %v", res, err)
	}
	// 名單外的人:一般使用者
	if r, err := svc.LoginWithLDAP("carol", "pw-c", "", "1.1.1.1"); err != nil || r.User.Role != "member" {
		t.Fatalf("carol:%+v %v", r, err)
	}

	// 後來才加進名單(已經是一般使用者):下次登入升成管理員
	svc.LDAPAdmins = append(svc.LDAPAdmins, "Carol ")
	if r, err := svc.LoginWithLDAP("carol", "pw-c", "", "1.1.1.1"); err != nil || r.User.Role != "admin" {
		t.Fatalf("升級:%+v %v", r, err)
	}
	// 從名單拿掉:不自動降級(要管理員自己改)
	svc.LDAPAdmins = nil
	if r, err := svc.LoginWithLDAP("boss", "pw-boss", "", "1.1.1.1"); err != nil || r.User.Role != "admin" {
		t.Fatalf("不降級:%+v %v", r, err)
	}
	u, _ := st.UserByEvoleapID("ldap:boss")
	if u == nil || u.Role != "admin" {
		t.Fatalf("存起來的:%+v", u)
	}
}

func TestLoginWithLDAPEmail(t *testing.T) {
	svc, _, st := ldapEnv(t)

	// 沒有 mail、也沒設網域:不能建立(組織成員是用 email 加的)
	_, err := svc.LoginWithLDAP("noml", "pw-n", "", "1.1.1.1")
	if statusOf(err) != 403 || !strings.Contains(err.Error(), "沒有 email") {
		t.Fatalf("沒 email:%v", err)
	}
	// 設了網域:用 帳號@網域(開頭的 @ 也可以)
	svc.LDAPEmailDomain = "@Corp.Test"
	res, err := svc.LoginWithLDAP("noml", "pw-n", "", "1.1.1.1")
	if err != nil || res.User.Email != "noml@corp.test" {
		t.Fatalf("網域:%+v %v", res, err)
	}

	// 管理員先用 email 加進名單(還沒對上任何身分)的人:預設不自動對上(目錄的 mail 使用者自己改得動的話,
	// 能冒用別人預先加入的身分),要伺服器管理員開 LDAPTrustMail
	_ = st.SaveUser(model.User{ID: "u_pending", Email: "carol@corp.test", Role: "admin", CreatedAt: model.Now()})
	_, err = svc.LoginWithLDAP("carol", "pw-c", "", "1.1.1.1")
	if statusOf(err) != 403 || !strings.Contains(err.Error(), "OMIXOR_LDAP_TRUST_MAIL") {
		t.Fatalf("預設不對上:%v", err)
	}
	if u, _ := st.UserByID("u_pending"); u == nil || u.EvoleapID != "" {
		t.Fatalf("不該動到預先加入的人:%+v", u)
	}
	// 開了之後:對上,不重複建立
	svc.LDAPTrustMail = true
	r, err := svc.LoginWithLDAP("carol", "pw-c", "", "1.1.1.1")
	if err != nil || r.User.ID != "u_pending" || r.User.EvoleapID != "ldap:carol" {
		t.Fatalf("對上名單:%+v %v", r, err)
	}

	// 這個 email 已經是另一個身分(例如 evoleap 的管理員)在用:不能接管
	_ = st.SaveUser(model.User{ID: "u_evo", EvoleapID: "evo-1", Email: "amy@corp.test", Role: "admin", CreatedAt: model.Now()})
	_, err = svc.LoginWithLDAP("amy", "pw-amy", "", "1.1.1.1")
	if statusOf(err) != 403 || !strings.Contains(err.Error(), "已經是另一個帳號在用") {
		t.Fatalf("email 衝突:%v", err)
	}
	if u, _ := st.UserByID("u_evo"); u == nil || u.EvoleapID != "evo-1" || u.Role != "admin" {
		t.Fatalf("不該動到原本的帳號:%+v", u)
	}
}

func TestLoginWithLDAPDisabledAndErrors(t *testing.T) {
	svc, dir, st := ldapEnv(t)

	// 沒啟用
	off := &service.Service{Store: storetest.New(t)}
	if _, err := off.LoginWithLDAP("amy", "x", "", "1.1.1.1"); statusOf(err) != 404 {
		t.Fatalf("沒啟用:%v", err)
	}

	// 被管理員停用的人:LDAP 通過也不行
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByEvoleapID("ldap:amy")
	u.Disabled = true
	_ = st.SaveUser(*u)
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", "1.1.1.1"); statusOf(err) != 403 {
		t.Fatalf("停用:%v", err)
	}

	// 目錄連不上、服務帳號壞掉:給登入的人一句通用的話,不洩漏內部細節
	dir.err = errors.New("LDAP 服務帳號 bind 失敗:cn=Administrator password=hunter2")
	_, err := svc.LoginWithLDAP("carol", "pw-c", "", "1.1.1.1")
	if statusOf(err) != 502 || strings.Contains(err.Error(), "Administrator") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("目錄錯誤:%v", err)
	}
}

// 同一個帳號輸錯太多次:擋在 omixor 這一層,不再送到目錄(AD 的鎖定是整個網域的)
func TestLoginWithLDAPAccountLockout(t *testing.T) {
	svc, dir, _ := ldapEnv(t)
	cur := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return cur }
	// 來源 IP 每次都不同(例如亂填 X-Forwarded-For):來源限制擋不住,帳號限制要擋住
	ip := func(i int) string { return fmt.Sprintf("10.0.0.%d", i) }

	for i := 0; i < 5; i++ {
		if _, err := svc.LoginWithLDAP("amy", "wrong", "", ip(i)); statusOf(err) != 401 {
			t.Fatalf("第 %d 次:%v", i+1, err)
		}
	}
	before := dir.calls
	// 第 6 次:就算密碼對了也擋,而且不送到目錄
	_, err := svc.LoginWithLDAP("amy", "pw-amy", "", ip(6))
	if statusOf(err) != 429 || !strings.Contains(err.Error(), "連續輸錯太多次") || dir.calls != before {
		t.Fatalf("要擋:%v 目錄被呼叫 %d 次", err, dir.calls-before)
	}
	// 別的帳號不受影響
	if _, err := svc.LoginWithLDAP("carol", "pw-c", "", ip(7)); err != nil {
		t.Fatalf("別的帳號:%v", err)
	}
	// 不存在的帳號也一樣算,不洩漏哪些帳號存在
	for i := 0; i < 5; i++ {
		_, _ = svc.LoginWithLDAP("ghost", "x", "", ip(10+i))
	}
	if _, err := svc.LoginWithLDAP("ghost", "x", "", ip(20)); statusOf(err) != 429 {
		t.Fatalf("不存在的帳號也要擋:%v", err)
	}
	// 一輪(預設 10 分鐘)過了就解開
	cur = cur.Add(10*time.Minute + time.Second)
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", ip(21)); err != nil {
		t.Fatalf("過一輪:%v", err)
	}
	// 通過驗證會清掉次數:再輸錯 4 次還沒被擋,成功後重新算
	for i := 0; i < 4; i++ {
		_, _ = svc.LoginWithLDAP("amy", "wrong", "", ip(30+i))
	}
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", ip(40)); err != nil {
		t.Fatalf("4 次還沒到上限:%v", err)
	}
	for i := 0; i < 4; i++ {
		_, _ = svc.LoginWithLDAP("amy", "wrong", "", ip(50+i))
	}
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", ip(60)); err != nil {
		t.Fatalf("成功後重新算:%v", err)
	}
	// 目錄連不上不算輸錯:不能因為伺服器那邊壞掉就把人擋住
	dir.err = errors.New("連不到 LDAP 伺服器")
	for i := 0; i < 8; i++ {
		_, _ = svc.LoginWithLDAP("carol", "pw-c", "", ip(70+i))
	}
	dir.err = nil
	if _, err := svc.LoginWithLDAP("carol", "pw-c", "", ip(80)); err != nil {
		t.Fatalf("基礎設施錯誤不該計入:%v", err)
	}
}

func TestLoginWithLDAPRateLimit(t *testing.T) {
	svc, dir, _ := ldapEnv(t)
	svc.LoginPerIP, svc.LoginTotal = 3, 100
	for i := 0; i < 3; i++ {
		_, _ = svc.LoginWithLDAP("amy", "wrong", "", "9.9.9.9")
	}
	before := dir.calls
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", "9.9.9.9"); statusOf(err) != 429 {
		t.Fatalf("同一個來源要被擋:%v", err)
	}
	if dir.calls != before {
		t.Fatal("被擋的請求不該送到 LDAP")
	}
	// 別的來源不受影響
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginWithLDAPAudit(t *testing.T) {
	svc, _, st := ldapEnv(t)
	_, _ = svc.LoginWithLDAP("amy", "wrong", "", "1.1.1.1")
	_, _ = svc.LoginWithLDAP("amy", "pw-amy", "Mac", "1.1.1.1")
	logs, _ := st.ListAudit(50)
	var denied, login, created bool
	for _, e := range logs {
		switch e.Action {
		case "login.denied":
			denied = true
		case "login":
			login = e.Detail["via"] == "ldap"
		case "user.created":
			created = true
		}
		if strings.Contains(strings.ToLower(e.Action), "pw-amy") {
			t.Fatal("操作紀錄不能有密碼")
		}
	}
	if !denied || !login || !created {
		t.Fatalf("操作紀錄:denied=%v login=%v created=%v", denied, login, created)
	}
}

// slowDir 每次驗證停一下,模擬並行嘗試
type slowDir struct{ calls atomic.Int32 }

func (d *slowDir) Authenticate(u, p string) (*ldapauth.Identity, error) {
	d.calls.Add(1)
	time.Sleep(30 * time.Millisecond)
	return nil, ldapauth.ErrInvalidCredentials
}

// 失敗計數先預扣再送目錄:並行嘗試送到目錄的次數不超過上限;共用的 Failures 跨途徑累計
func TestLDAPFailuresReservedBeforeDirectory(t *testing.T) {
	svc, fd, _ := ldapEnv(t)
	slow := &slowDir{}
	shared := &service.LoginFailures{}
	set := service.LDAPSettings{Dir: slow, Failures: shared}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.ProveLDAP(set, "amy", "x", "1.1.1.1")
		}()
	}
	wg.Wait()
	if n := slow.calls.Load(); n != 5 {
		t.Fatalf("送到目錄 %d 次,要 5", n)
	}
	// 別的途徑用同一個 shared 就看得到
	if _, ok := shared.Reserve("AMY", svc.Now()); ok {
		t.Fatal("共用計數要已經滿了")
	}
	// 目錄故障不算次數
	fd.err = errors.New("down")
	for i := 0; i < 12; i++ {
		if _, err := svc.ProveLDAP(svc.LDAPSettings(), "carol", "x", "1.1.1.1"); service.Code(err) != 502 {
			t.Fatalf("目錄故障:%v", err)
		}
	}
}

// 宿主自己的限額(LDAPSettings.Rate):登入成功不佔名額、失敗照佔;和共用的限額分開
func TestVerifyDirectoryOwnRate(t *testing.T) {
	svc, _, _ := ldapEnv(t)
	svc.LoginPerIP = 3
	set := svc.LDAPSettings()
	set.Rate = &service.LoginRate{}
	for i := 0; i < 10; i++ {
		if _, err := svc.VerifyDirectory(set, "amy", "pw-amy", "9.9.9.9"); err != nil {
			t.Fatal(i, err)
		}
	}
	for _, u := range []string{"x1", "x2", "x3"} {
		if _, err := svc.VerifyDirectory(set, u, "wrong", "9.9.9.9"); statusOf(err) != 401 {
			t.Fatal(u, err)
		}
	}
	if _, err := svc.VerifyDirectory(set, "amy", "pw-amy", "9.9.9.9"); statusOf(err) != 429 {
		t.Fatal("失敗滿了要 429:", err)
	}
	if _, err := svc.VerifyDirectory(svc.LDAPSettings(), "amy", "pw-amy", "9.9.9.9"); err != nil {
		t.Fatal("共用的限額不受影響:", err)
	}
}

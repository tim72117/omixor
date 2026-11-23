package service_test

import (
	"errors"
	"testing"

	"omixor-core/store"
	"time"
)

// 各入口各自限額:一個被灌爆(全域額度用完)不影響別的入口。
// 判斷「被放行」:LDAP 錯誤密碼是 401、evoleap 空 token 是 400、配對錯碼是 401;被擋都是 429

func TestEntrypointLimitsIndependent(t *testing.T) {
	// 配對用完 → LDAP、evoleap 仍可
	svc, _, _ := ldapEnv(t)
	svc.LoginPerIP, svc.LoginTotal = 1000, 3
	for i := 0; i < 3; i++ {
		_, _ = svc.ClaimPairCode("AAAAAAAA", "x", "9.9.9.1")
	}
	if _, err := svc.ClaimPairCode("AAAAAAAA", "x", "9.9.9.2"); statusOf(err) != 429 {
		t.Fatalf("配對額度用完要 429:%v", err)
	}
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", "9.9.9.2"); err != nil {
		t.Fatalf("配對被灌爆不該影響 LDAP 登入:%v", err)
	}
	if _, err := svc.LoginWithEvoleap("", "", "9.9.9.2"); statusOf(err) != 400 {
		t.Fatalf("配對被灌爆不該影響 evoleap 登入:%v", err)
	}

	// 反過來:LDAP 用完 → 配對、evoleap 仍可
	svc, _, _ = ldapEnv(t)
	svc.LoginPerIP, svc.LoginTotal = 1000, 3
	for i := 0; i < 3; i++ {
		_, _ = svc.LoginWithLDAP("amy", "wrong", "", "9.9.9.1")
	}
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", "9.9.9.2"); statusOf(err) != 429 {
		t.Fatalf("LDAP 額度用完要 429:%v", err)
	}
	if _, err := svc.ClaimPairCode("AAAAAAAA", "x", "9.9.9.2"); statusOf(err) != 401 {
		t.Fatalf("LDAP 被灌爆不該影響配對:%v", err)
	}
	if _, err := svc.LoginWithEvoleap("", "", "9.9.9.2"); statusOf(err) != 400 {
		t.Fatalf("LDAP 被灌爆不該影響 evoleap:%v", err)
	}

	// evoleap 用完 → 配對、LDAP 仍可
	svc, _, _ = ldapEnv(t)
	svc.LoginPerIP, svc.LoginTotal = 1000, 3
	for i := 0; i < 3; i++ {
		_, _ = svc.LoginWithEvoleap("", "", "9.9.9.1")
	}
	if _, err := svc.LoginWithEvoleap("", "", "9.9.9.2"); statusOf(err) != 429 {
		t.Fatalf("evoleap 額度用完要 429:%v", err)
	}
	if _, err := svc.ClaimPairCode("AAAAAAAA", "x", "9.9.9.2"); statusOf(err) != 401 {
		t.Fatalf("evoleap 被灌爆不該影響配對:%v", err)
	}
	if _, err := svc.LoginWithLDAP("amy", "pw-amy", "", "9.9.9.2"); err != nil {
		t.Fatalf("evoleap 被灌爆不該影響 LDAP:%v", err)
	}
}

func TestClaimPairCodeDisabledKeepsCode(t *testing.T) {
	svc, p, _ := pairEnv(t)
	c, err := svc.CreatePairCode(p)
	if err != nil {
		t.Fatal(err)
	}
	u := p.User
	u.Disabled = true
	if err := svc.Store.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ClaimPairCode(c.Code, "x", "8.8.8.8"); statusOf(err) != 403 {
		t.Fatalf("停用要 403:%v", err)
	}
	u.Disabled = false
	if err := svc.Store.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ClaimPairCode(c.Code, "x", "8.8.8.8"); err != nil {
		t.Fatalf("帳號啟用後,先前被 403 擋下的配對碼要還能用:%v", err)
	}
}

// revokeFailStore 讓 RevokeToken 回指定的錯誤
type revokeFailStore struct {
	store.Store
	err error
}

func (r revokeFailStore) RevokeToken(id string, at time.Time) error { return r.err }

func TestRedeemPairRevokeErrors(t *testing.T) {
	svc, p, _ := pairEnv(t)
	real := svc.Store
	c, err := svc.CreatePairCode(p)
	if err != nil {
		t.Fatal(err)
	}
	// 資料庫錯誤:500,不是 401;配對碼還在
	svc.Store = revokeFailStore{Store: real, err: errors.New("connection reset")}
	if _, err := svc.ClaimPairCode(c.Code, "x", "8.8.8.8"); statusOf(err) != 500 {
		t.Fatalf("資料庫錯誤要 500:%v", err)
	}
	// 找不到(已被別人用掉):401
	svc.Store = revokeFailStore{Store: real, err: store.ErrNotFound}
	if _, err := svc.ClaimPairCode(c.Code, "x", "8.8.8.8"); statusOf(err) != 401 {
		t.Fatalf("已被用掉要 401:%v", err)
	}
	// 恢復後仍可用
	svc.Store = real
	if _, err := svc.ClaimPairCode(c.Code, "x", "8.8.8.8"); err != nil {
		t.Fatalf("失敗時不該消耗配對碼:%v", err)
	}
}

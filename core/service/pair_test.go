package service_test

import (
	"strings"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

func pairEnv(t *testing.T) (*service.Service, *model.Principal, *time.Time) {
	t.Helper()
	st := store.NewMem()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	svc := &service.Service{Store: st, TTL: 90 * 24 * time.Hour, LoginPerIP: 1000, LoginTotal: 1000, Now: func() time.Time { return now }}
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: model.Now()})
	mac := model.Token{ID: "t_mac", UserID: "u1", Device: "Mac", CreatedAt: model.Now(), Hash: service.HashToken("omx_mac")}
	_ = st.CreateToken(mac)
	p, err := svc.Authenticate("omx_mac")
	if err != nil {
		t.Fatal(err)
	}
	return svc, p, &now
}

func TestPairCodeFormat(t *testing.T) {
	svc, p, _ := pairEnv(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, err := svc.CreatePairCode(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Code) != service.PairCodeLength || service.NormalizePairCode(c.Code) != c.Code || strings.ContainsAny(c.Code, "0O1IL") {
			t.Fatalf("配對碼格式不對:%q", c.Code)
		}
		seen[c.Code] = true
	}
	if len(seen) < 50 {
		t.Fatal("配對碼要隨機", len(seen))
	}
	if service.NormalizePairCode(" abcd-2345 ") != "ABCD2345" || service.NormalizePairCode("ABCD234") != "" || service.NormalizePairCode("ABCD234O") != "" {
		t.Fatal("正規化:不分大小寫、忽略空白與 -、長度與字元要對")
	}
}

func TestPairClaimOnceAndExpiry(t *testing.T) {
	svc, p, now := pairEnv(t)
	c, err := svc.CreatePairCode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ExpiresAt.Equal(now.Add(service.PairTTL)) {
		t.Fatal("效期 5 分鐘", c.ExpiresAt)
	}
	// 配對碼不能直接當 token 用
	if _, err := svc.Authenticate(c.Code); code(err) != 401 {
		t.Fatal("配對碼不能登入", err)
	}
	if _, err := svc.Authenticate("omx_" + c.Code); code(err) != 401 {
		t.Fatal("配對碼加前綴也不能登入", err)
	}
	// 只存雜湊:token 表裡沒有原文
	toks, _ := svc.Store.ListTokens(*now)
	for _, tk := range toks {
		if strings.Contains(string(tk.Hash), c.Code) || strings.Contains(tk.ID, c.Code) || strings.Contains(tk.Device, c.Code) {
			t.Fatal("不能存原文", tk)
		}
	}
	// 小寫、加 - 也可以
	formatted := strings.ToLower(c.Code[:4] + "-" + c.Code[4:])
	r, err := svc.ClaimPairCode(formatted, "iPhone 17 Pro", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Token, service.TokenPrefix) || r.User.ID != "u1" || r.ExpiresAt == nil {
		t.Fatalf("%+v", r)
	}
	mp, err := svc.Authenticate(r.Token)
	if err != nil || mp.User.ID != "u1" || mp.Token.Device != "iPhone 17 Pro" || mp.Token.ID == p.Token.ID {
		t.Fatalf("要是新的、同一個使用者的 token:%+v %v", mp, err)
	}
	// 用過就失效,錯誤訊息跟不存在一樣
	_, e1 := svc.ClaimPairCode(c.Code, "x", "1.2.3.4")
	_, e2 := svc.ClaimPairCode("ZZZZZZZZ", "x", "1.2.3.4")
	_, e3 := svc.ClaimPairCode("短", "x", "1.2.3.4")
	if code(e1) != 401 || code(e2) != 401 || code(e3) != 401 || e1.Error() != e2.Error() || e2.Error() != e3.Error() {
		t.Fatal("用過、不存在、格式不對都回同一句 401", e1, e2, e3)
	}
	// 新 token 可以個別撤銷,不影響 Mac
	if err := svc.Logout(mp); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(r.Token); code(err) != 401 {
		t.Fatal("手機登出後失效", err)
	}
	if _, err := svc.Authenticate("omx_mac"); err != nil {
		t.Fatal("Mac 不受影響", err)
	}
	// 過期
	c2, _ := svc.CreatePairCode(p)
	*now = now.Add(service.PairTTL + time.Second)
	if _, err := svc.ClaimPairCode(c2.Code, "x", "1.2.3.4"); code(err) != 401 {
		t.Fatal("過期要 401", err)
	}
}

func TestPairPermissionsAndRateLimit(t *testing.T) {
	svc, p, _ := pairEnv(t)
	if _, err := svc.CreatePairCode(&model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "pm"}}); code(err) != 403 {
		t.Fatal("角色憑證不能配對", err)
	}
	c, _ := svc.CreatePairCode(p)
	svc.LoginPerIP = 2
	svc.ClaimPairCode("AAAAAAAA", "x", "9.9.9.9")
	svc.ClaimPairCode("AAAAAAAA", "x", "9.9.9.9")
	if _, err := svc.ClaimPairCode(c.Code, "x", "9.9.9.9"); code(err) != 429 {
		t.Fatal("超過頻率要 429", err)
	}
	// 其他來源不受影響;429 沒有用掉配對碼
	if _, err := svc.ClaimPairCode(c.Code, "x", "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	// 停用的帳號不能換
	c3, _ := svc.CreatePairCode(p)
	_ = svc.Store.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", Disabled: true})
	if _, err := svc.ClaimPairCode(c3.Code, "x", "8.8.8.8"); code(err) != 403 {
		t.Fatal("停用要 403", err)
	}
}

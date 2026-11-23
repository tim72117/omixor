package store

import (
	"errors"
	"testing"
	"time"

	"omixor-core/model"
)

func oauthTestClient(t *testing.T, s Store, id string, created time.Time, max int) error {
	t.Helper()
	return s.CreateOAuthClient(model.OAuthClient{ID: id, Name: id, RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
		GrantTypes: []string{"authorization_code"}, CreatedAt: created}, max)
}

// 用戶端數量上限:滿了回 ErrOAuthFull;滿了時淘汰「註冊超過一小時、沒簽發過授權碼或 token」的,有用過的不動
func TestOAuthClientCapAndEviction(t *testing.T) {
	s := testPG(t)
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if err := oauthTestClient(t, s, "c1", base, 2); err != nil {
		t.Fatal(err)
	}
	if err := oauthTestClient(t, s, "c2", base.Add(time.Minute), 2); err != nil {
		t.Fatal(err)
	}
	if err := oauthTestClient(t, s, "c3", base.Add(2*time.Minute), 2); !errors.Is(err, ErrOAuthFull) {
		t.Fatal("滿了要 ErrOAuthFull", err)
	}
	// 一小時後:c1、c2 都沒人用過,淘汰最舊的 c1
	if err := oauthTestClient(t, s, "c3", base.Add(2*time.Hour), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthClientByID("c1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("c1 要被淘汰", err)
	}
	if cl, err := s.OAuthClientByID("c2"); err != nil || len(cl.RedirectURIs) != 1 || cl.GrantTypes[0] != "authorization_code" {
		t.Fatal("c2 要還在", cl, err)
	}
	// c2 簽發過 code(用過):不能被淘汰;c3 太新也不能淘汰 → 滿了
	_ = s.SaveUser(model.User{ID: "u1", Email: "a@x", Role: "member", CreatedAt: model.Now()})
	if err := s.PutOAuthToken(model.OAuthToken{ID: "t1", Kind: model.OAuthKindCode, Hash: []byte("h1"), ClientID: "c2", UserID: "u1",
		CreatedAt: base, ExpiresAt: base.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := oauthTestClient(t, s, "c4", base.Add(3*time.Hour+time.Second), 2); err != nil { // c3 超過一小時了:淘汰 c3
		t.Fatal(err)
	}
	if err := oauthTestClient(t, s, "c5", base.Add(3*time.Hour+time.Minute), 2); !errors.Is(err, ErrOAuthFull) {
		t.Fatal("c2 有用過、c4 太新:滿了", err)
	}
	if _, err := s.OAuthClientByID("c2"); err != nil {
		t.Fatal("用過的 c2 不能被淘汰", err)
	}
}

// 條件式「用過一次」:同一列只有一次成功(ErrStale),同一個交易附帶新增;過期、撤銷也不能用;家族撤銷;清理
func TestOAuthExchangeOnceRevokeAndPurge(t *testing.T) {
	s := testPG(t)
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	_ = s.SaveUser(model.User{ID: "u1", Email: "a@x", Role: "member", CreatedAt: model.Now()})
	if err := oauthTestClient(t, s, "c1", now, 10); err != nil {
		t.Fatal(err)
	}
	put := func(id, kind, fam string, exp time.Time) model.OAuthToken {
		t.Helper()
		tk := model.OAuthToken{ID: id, Kind: kind, Hash: []byte("hash-" + id), ClientID: "c1", UserID: "u1", Org: "o", Scope: "mcp:read",
			Resource: "https://x/mcp/org/o", FamilyID: fam, CreatedAt: now, ExpiresAt: exp}
		if err := s.PutOAuthToken(tk); err != nil {
			t.Fatal(err)
		}
		return tk
	}
	put("code1", model.OAuthKindCode, "f1", now.Add(time.Minute))
	add := []model.OAuthToken{
		{ID: "acc1", Kind: model.OAuthKindAccess, Hash: []byte("hash-acc1"), ClientID: "c1", UserID: "u1", FamilyID: "f1", CreatedAt: now, ExpiresAt: now.Add(time.Minute)},
		{ID: "ref1", Kind: model.OAuthKindRefresh, Hash: []byte("hash-ref1"), ClientID: "c1", UserID: "u1", FamilyID: "f1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	if err := s.ExchangeOAuthToken("code1", now, add); err != nil {
		t.Fatal(err)
	}
	if err := s.ExchangeOAuthToken("code1", now, nil); !errors.Is(err, ErrStale) {
		t.Fatal("第二次要 ErrStale", err)
	}
	got, err := s.OAuthTokenByHash(model.OAuthKindCode, []byte("hash-code1"))
	if err != nil || got.UsedAt == nil || got.UserID != "u1" || got.FamilyID != "f1" {
		t.Fatal(got, err)
	}
	if _, err := s.OAuthTokenByHash(model.OAuthKindAccess, []byte("hash-code1")); !errors.Is(err, ErrNotFound) {
		t.Fatal("種類不同要找不到", err)
	}
	// 失敗的交換不新增任何列
	if err := s.ExchangeOAuthToken("code1", now, []model.OAuthToken{{ID: "ghost", Kind: model.OAuthKindAccess, Hash: []byte("hash-ghost"), ClientID: "c1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if _, err := s.OAuthTokenByHash(model.OAuthKindAccess, []byte("hash-ghost")); !errors.Is(err, ErrNotFound) {
		t.Fatal("失敗的交換不能留下新列", err)
	}
	// 過期的不能用
	put("code2", model.OAuthKindCode, "f2", now.Add(time.Minute))
	if err := s.ExchangeOAuthToken("code2", now.Add(2*time.Minute), nil); !errors.Is(err, ErrStale) {
		t.Fatal("過期不能用", err)
	}
	// 家族撤銷:同家族還沒撤銷的全部標 revoked,別的家族不受影響
	put("code3", model.OAuthKindCode, "f3", now.Add(time.Minute))
	if err := s.RevokeOAuthFamily("f1", now); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"acc1", "ref1"} {
		k := model.OAuthKindAccess
		if h == "ref1" {
			k = model.OAuthKindRefresh
		}
		if x, err := s.OAuthTokenByHash(k, []byte("hash-"+h)); err != nil || x.RevokedAt == nil {
			t.Fatal(h, "要被撤銷", x, err)
		}
	}
	if x, _ := s.OAuthTokenByHash(model.OAuthKindCode, []byte("hash-code3")); x.RevokedAt != nil {
		t.Fatal("別的家族不受影響")
	}
	if err := s.ExchangeOAuthToken("code3", now, nil); err != nil {
		t.Fatal(err)
	}
	// 清理:過期超過 before 的刪掉
	n, err := s.PurgeOAuthTokens(now.Add(10 * time.Minute))
	if err != nil || n < 3 { // code1、code2、code3、acc1 都過期了
		t.Fatal(n, err)
	}
	if _, err := s.OAuthTokenByHash(model.OAuthKindRefresh, []byte("hash-ref1")); err != nil {
		t.Fatal("還沒過期的不能刪", err)
	}
}

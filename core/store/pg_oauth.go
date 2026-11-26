// pg_oauth.go OAuth(Claude 自訂 connector 的唯讀授權)的 PG 實作:omixor_oauth_clients、omixor_oauth_tokens(定義在 pg_schema.go 的 pgSchema)。
// 只存雜湊;授權碼、refresh、CSRF 的「用過一次」靠條件式 update(used_at is null),多個實例同時換也只有一個成功。

package store

import (
	"time"

	"omixor-core/model"
)

func (p *PG) CreateOAuthClient(cl model.OAuthClient, max int) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	// 序列化註冊:數量檢查與新增之間不會被另一個註冊插隊
	if _, err := tx.Exec(c, `select pg_advisory_xact_lock(hashtext('omixor-oauth-clients'))`); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(c, `select count(*) from omixor_oauth_clients`).Scan(&n); err != nil {
		return err
	}
	if max > 0 && n >= max {
		// 滿了:先淘汰一個「註冊超過一小時、從來沒有簽發過授權碼或 token」的用戶端(只在同意畫面出現過、沒人完成授權的垃圾註冊),
		// 避免有人灌滿註冊名額讓真正的使用者註冊不了;沒有可淘汰的才算滿
		tag, err := tx.Exec(c, `delete from omixor_oauth_clients where client_id = (
  select c.client_id from omixor_oauth_clients c
  where c.created_at < $1 and not exists (select 1 from omixor_oauth_tokens t where t.client_id = c.client_id and t.kind <> 'csrf')
  order by c.created_at limit 1)`, cl.CreatedAt.Add(-time.Hour))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrOAuthFull
		}
	}
	if _, err := tx.Exec(c, `insert into omixor_oauth_clients (client_id, client_name, redirect_uris, grant_types, created_at) values ($1,$2,$3,$4,$5)`,
		cl.ID, cl.Name, nonNil(cl.RedirectURIs), nonNil(cl.GrantTypes), cl.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(c)
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func (p *PG) OAuthClientByID(id string) (*model.OAuthClient, error) {
	c, cancel := ctx()
	defer cancel()
	var cl model.OAuthClient
	err := p.pool.QueryRow(c, `select client_id, client_name, redirect_uris, grant_types, created_at from omixor_oauth_clients where client_id = $1`, id).
		Scan(&cl.ID, &cl.Name, &cl.RedirectURIs, &cl.GrantTypes, &cl.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	cl.CreatedAt = cl.CreatedAt.UTC()
	return &cl, nil
}

const oauthTokenInsert = `insert into omixor_oauth_tokens (id, kind, token_hash, client_id, user_id, org, scope, resource, redirect_uri, code_challenge, family_id, created_at, expires_at)
values ($1,$2,$3,$4,nullif($5,''),$6,$7,$8,$9,$10,$11,$12,$13)`

func oauthTokenArgs(t model.OAuthToken) []any {
	return []any{t.ID, t.Kind, t.Hash, t.ClientID, t.UserID, t.Org, t.Scope, t.Resource, t.RedirectURI, t.CodeChallenge, t.FamilyID, t.CreatedAt, t.ExpiresAt}
}

func (p *PG) PutOAuthToken(t model.OAuthToken) error {
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, oauthTokenInsert, oauthTokenArgs(t)...)
	return err
}

func (p *PG) OAuthTokenByHash(kind string, hash []byte) (*model.OAuthToken, error) {
	c, cancel := ctx()
	defer cancel()
	var t model.OAuthToken
	err := p.pool.QueryRow(c, `select id, kind, token_hash, client_id, coalesce(user_id,''), org, scope, resource, redirect_uri, code_challenge, family_id,
  created_at, expires_at, used_at, revoked_at from omixor_oauth_tokens where kind = $1 and token_hash = $2`, kind, hash).
		Scan(&t.ID, &t.Kind, &t.Hash, &t.ClientID, &t.UserID, &t.Org, &t.Scope, &t.Resource, &t.RedirectURI, &t.CodeChallenge, &t.FamilyID,
			&t.CreatedAt, &t.ExpiresAt, &t.UsedAt, &t.RevokedAt)
	if err != nil {
		return nil, notFound(err)
	}
	t.CreatedAt, t.ExpiresAt = t.CreatedAt.UTC(), t.ExpiresAt.UTC()
	return &t, nil
}

func (p *PG) ExchangeOAuthToken(useID string, at time.Time, add []model.OAuthToken) error {
	c, cancel := ctx()
	defer cancel()
	tx, err := p.pool.Begin(c)
	if err != nil {
		return err
	}
	defer tx.Rollback(c)
	tag, err := tx.Exec(c, `update omixor_oauth_tokens set used_at = $2 where id = $1 and used_at is null and revoked_at is null and expires_at > $2`, useID, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrStale
	}
	for _, t := range add {
		if _, err := tx.Exec(c, oauthTokenInsert, oauthTokenArgs(t)...); err != nil {
			return err
		}
	}
	return tx.Commit(c)
}

func (p *PG) RevokeOAuthFamily(family string, at time.Time) error {
	if family == "" {
		return nil
	}
	c, cancel := ctx()
	defer cancel()
	_, err := p.pool.Exec(c, `update omixor_oauth_tokens set revoked_at = $2 where family_id = $1 and revoked_at is null`, family, at)
	return err
}

func (p *PG) RevokeOAuthUser(userID string, at time.Time) (int, error) {
	if userID == "" {
		return 0, nil
	}
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `update omixor_oauth_tokens set revoked_at = $2 where user_id = $1 and revoked_at is null`, userID, at)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (p *PG) PurgeOAuthTokens(before time.Time) (int, error) {
	c, cancel := ctx()
	defer cancel()
	tag, err := p.pool.Exec(c, `delete from omixor_oauth_tokens where expires_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

package service

import (
	"errors"

	"omixor-core/model"
	"omixor-core/store"
)

// externalIdentity 外部目錄(LDAP、GitLab)已經驗證過的身分,以及對應到 omixor 使用者時的政策
type externalIdentity struct {
	key   string // 存在 EvoleapID 欄位的穩定鍵(ldap:<帳號>、gitlab:<數字 id>)
	email string // 已正規化(小寫、合法)的 email;空 = 沒有可用的 email,不能建立新使用者
	name  string
	// admin 是來源的管理員名單上的人:建立或登入時升級成伺服器管理員(只升不降)
	admin bool
	// trustMail 管理員預先用 email 加入、還沒對上身分的人,可不可以讓這個身分接手
	trustMail bool

	via     string         // 稽核的 via,管理員升級的動作名稱是 <via>.admin
	detail  map[string]any // 稽核要附的識別欄位(例如 username)
	noEmail error          // email 空又要建立時回的錯誤
	pending string         // email-pending 時的說明(%s = email)
}

func (x externalIdentity) auditDetail(extra ...any) map[string]any {
	d := map[string]any{"via": x.via}
	for k, v := range x.detail {
		d[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		d[extra[i].(string)] = extra[i+1]
	}
	return d
}

// resolveExternalUser 外部身分對上 omixor 使用者(LDAP、GitLab 共用):
//   - 用 key 找;第一次出現的人自動建立成一般使用者(admin 為真則是伺服器管理員)
//   - 同 email 已被另一個身分用 → 403;管理員預先用 email 加入但還沒對上身分的人,trustMail 才接手
//   - 停用的帳號 403;admin 為真但目前不是管理員 → 升級
//
// 回傳已存好的使用者(LastLoginAt 已更新)
func (s *Service) resolveExternalUser(x externalIdentity) (*model.User, error) {
	now := s.mt()
	u, err := s.Store.UserByEvoleapID(x.key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if u == nil {
		if x.email == "" {
			s.Audit(nil, "login.denied", "", x.auditDetail("reason", "no-email"))
			return nil, x.noEmail
		}
		if other, err := s.Store.UserByEmail(x.email); err == nil {
			if other.EvoleapID != "" {
				// 這個 email 已經是另一個身分(evoleap、別的 LDAP 或 GitLab 帳號)在用:不能接管
				s.Audit(nil, "login.denied", "", x.auditDetail("reason", "email-in-use", "other", other.ID))
				return nil, fail(403, "這個 email(%s)已經是另一個帳號在用,請聯絡管理員", x.email)
			}
			// 管理員先用 email 加進名單、還沒對上身分的人。來源的 mail 使用者自己改得動的話,
			// 這一步等於讓人用自己填的 email 接手別人預先加入的身分(連同組織權限),所以要管理員明確開信任設定
			if !x.trustMail {
				s.Audit(nil, "login.denied", "", x.auditDetail("reason", "email-pending", "other", other.ID))
				return nil, fail(403, x.pending, x.email)
			}
			u = other
			u.EvoleapID = x.key
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if u == nil {
		role := "member"
		if x.admin {
			role = "admin"
		}
		u = &model.User{ID: RandID("u_", 9), EvoleapID: x.key, Email: x.email, Role: role, CreatedAt: now}
		s.Audit(&model.Principal{User: *u}, "user.created", "", x.auditDetail("role", role))
	}
	if u.Disabled {
		d := map[string]any{"via": x.via, "reason": "disabled"}
		s.Audit(&model.Principal{User: *u}, "login.denied", "", d)
		return nil, fail(403, "這個帳號已停用")
	}
	if x.admin && u.Role != "admin" {
		u.Role = "admin"
		d := map[string]any{}
		for k, v := range x.detail {
			d[k] = v
		}
		s.Audit(&model.Principal{User: *u}, x.via+".admin", "", d)
	}
	if x.name != "" {
		u.Name = x.name
	}
	u.LastLoginAt = &now
	if err := s.saveUser(u); err != nil {
		return nil, err
	}
	return u, nil
}

// pg_actor_names.go 輸出相容字串用的批次查詢(給 service/present.go;ISS-36 C-2 共用地基)

package store

// UserInfo 使用者 actor 對應的使用者列(輸出相容字串用:email、稽核的 userId)
type UserInfo struct {
	ID    string // omixor_users.id
	Email string
	// Disabled 帳號被停用(ActorView.Status 用)
	Disabled bool
}

// UserInfoByActorIDs 使用者 actor id → 使用者列的 id 與 email(有使用者列的才有;「未註冊使用者」、角色、系統 actor 不在結果裡)
func (p *PG) UserInfoByActorIDs(ids []string) (map[string]UserInfo, error) {
	out := map[string]UserInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	c, cancel := ctx()
	defer cancel()
	rows, err := p.pool.Query(c, `select actor_id, id, email, disabled from omixor_users where actor_id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var aid string
		var u UserInfo
		if err := rows.Scan(&aid, &u.ID, &u.Email, &u.Disabled); err != nil {
			return nil, err
		}
		out[aid] = u
	}
	return out, rows.Err()
}

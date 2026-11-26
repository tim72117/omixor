package core

import (
	"errors"
	"fmt"
	"strings"

	"omixor-core/service"
	"omixor-core/store"
)

// UpgradeLoginWarnings 啟用 LDAP 時啟動要提醒的事:誰會登入不了(本機模式;規則見 caller_access.go)。admins 是管理員帳號名單
func UpgradeLoginWarnings(admins []string) []string {
	if IsRemote() {
		return nil
	}
	out := []string{"啟用 LDAP 時,只有對得上 omixor 使用者、沒停用,而且是某個組織成員(或管理員名單上)的帳號能登入;" +
		"救援方式見 console/README.md「升級與救援」"}
	l := localCore()
	if l == nil {
		return append(out, "連不到資料庫:沒辦法確認誰能登入")
	}
	for _, a := range admins {
		u, err := l.Svc.Store.UserByEvoleapID(service.LDAPIDPrefix + strings.ToLower(a))
		switch {
		case errors.Is(err, store.ErrNotFound):
			out = append(out, "管理員 "+a+" 還沒有對應的 omixor 使用者,第一次登入時建立;對不上的話他登入後只能改認證設定")
		case err != nil:
			out = append(out, "查管理員 "+a+" 的使用者失敗:"+err.Error())
		case u.Disabled:
			out = append(out, "管理員 "+a+" 的 omixor 使用者已停用,不能登入")
		}
	}
	users, err := l.Svc.Store.ListUsers()
	if err != nil {
		return append(out, "讀不到使用者清單:"+err.Error())
	}
	none := 0
	for _, u := range users {
		account, ok := strings.CutPrefix(u.EvoleapID, service.LDAPIDPrefix)
		if !ok || u.Disabled || u.Role == "admin" || containsFold(admins, account) {
			continue
		}
		if orgs, err := l.Svc.Store.OrgsOfUser(u.ID); err == nil && len(orgs) == 0 {
			none++
		}
	}
	if none > 0 {
		out = append(out, fmt.Sprintf("有 %d 個登入過的 LDAP 帳號不屬於任何組織,現在不能登入;請在成員頁用 email 把他們加進組織", none))
	}
	return out
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

package core

import (
	"os/user"
	"strings"
	"sync"
)

// 本機使用者的顯示名稱與「是不是同一個人」。身分(id)固定是 LocalIdentity;畫面上的名稱用作業系統的登入帳號。

// localUnknownName 取不到作業系統帳號(或是容器、服務帳號)時的顯示名稱
const localUnknownName = "本機使用者"

var localDisplayName = sync.OnceValue(func() string {
	u, err := user.Current()
	if err != nil {
		return localUnknownName
	}
	return cleanLocalName(u.Name, u.Username)
})

// cleanLocalName 全名優先,沒有就用帳號(去掉 Windows 的網域);服務帳號不當人名
func cleanLocalName(full, username string) string {
	if n := strings.TrimSpace(full); n != "" && !isServiceAccount(n) {
		return n
	}
	n := strings.TrimSpace(username[strings.LastIndex(username, `\`)+1:])
	if n == "" || isServiceAccount(n) {
		return localUnknownName
	}
	return n
}

func isServiceAccount(n string) bool {
	switch strings.ToLower(n) {
	case "root", "nobody", "nonroot", "system":
		return true
	}
	return false
}

// samePerson 兩個人員 id 是不是同一個人:本機使用者的新舊寫法(LocalIdentity、早期的 tim、ops-web)視為同一人
func samePerson(a, b string) bool {
	return a == b || isLocalPersonID(a) && isLocalPersonID(b)
}

func isLocalPersonID(id string) bool {
	return id == LocalIdentity || id == legacyLocalIdentity || id == legacyLocalPerson
}

package core

// 本機身分(沒有登入者時的系統身分)的顯示名稱與「是不是同一個人」。身分(id)固定是 LocalIdentity。
// 畫面上不顯示主機作業系統的登入帳號:console 是自建伺服器,看畫面的人不一定是主機上的那個人。

// localUnknownName 沒啟用登入時「登入身分」的顯示名稱
const localUnknownName = "本機使用者"

// MeName 沒啟用登入時「登入身分」的顯示名稱(呼叫時拿著 Mu)
func (a *App) MeName() string {
	if a.Me == LocalIdentity {
		return localUnknownName
	}
	return a.Name(Person(a.Me))
}

// samePerson 兩個人員 id 是不是同一個人:本機使用者的新舊寫法(LocalIdentity、早期的 tim、ops-web)視為同一人
func samePerson(a, b string) bool {
	return a == b || isLocalPersonID(a) && isLocalPersonID(b)
}

func isLocalPersonID(id string) bool {
	return id == LocalIdentity || id == legacyLocalIdentity || id == legacyLocalPerson
}

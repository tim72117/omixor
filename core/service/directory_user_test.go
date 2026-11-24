package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// ResolveDirectoryUser 直接測(不經過密碼驗證):建立、重複登入、管理員升級、email 規則、停用
func TestResolveDirectoryUser(t *testing.T) {
	svc, _, st := ldapEnv(t)

	// 第一次:建成一般使用者,email 轉小寫,對應鍵 ldap:<帳號>
	u, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "amy", Email: " Amy@Corp.Test ", Name: "Amy Lin"}, false)
	if err != nil || u.Email != "amy@corp.test" || u.Role != "member" || u.Name != "Amy Lin" || u.EvoleapID != service.LDAPIDPrefix+"amy" || u.LastLoginAt == nil {
		t.Fatalf("建立:%+v %v", u, err)
	}
	if got, _ := st.UserByEvoleapID("ldap:amy"); got == nil || got.ID != u.ID {
		t.Fatalf("沒有存好:%+v", got)
	}
	// 再來一次:同一個人
	if again, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "amy", Email: "amy@corp.test"}, false); err != nil || again.ID != u.ID {
		t.Fatalf("重複登入:%+v %v", again, err)
	}
	// 名單上的人升級成管理員(之後不在名單也不降級)
	if adm, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "amy"}, true); err != nil || adm.Role != "admin" {
		t.Fatalf("升級:%+v %v", adm, err)
	}
	if adm, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "amy"}, false); err != nil || adm.Role != "admin" {
		t.Fatalf("不自動降級:%+v %v", adm, err)
	}
	// 新來的名單上的人直接建成管理員
	if b, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "boss", Email: "boss@corp.test"}, true); err != nil || b.Role != "admin" {
		t.Fatalf("管理員:%+v %v", b, err)
	}
}

func TestResolveDirectoryUserEmailRules(t *testing.T) {
	svc, _, st := ldapEnv(t)

	// 沒有 mail、沒設網域:不能建立;不合法的 mail 當作沒有
	for _, mail := range []string{"", "not-an-email"} {
		_, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "noml", Email: mail}, false)
		if statusOf(err) != 403 || !strings.Contains(err.Error(), "沒有 email") {
			t.Fatalf("mail=%q:%v", mail, err)
		}
	}
	// 設了網域:帳號@網域
	svc.LDAPEmailDomain = "@Corp.Test"
	if u, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "noml"}, false); err != nil || u.Email != "noml@corp.test" {
		t.Fatalf("網域:%+v %v", u, err)
	}
	// 預先加入但還沒對上身分的人:預設不接手,開 LDAPTrustMail 才接
	_ = st.SaveUser(model.User{ID: "u_pending", Email: "carol@corp.test", Role: "admin", CreatedAt: model.Now()})
	id := service.DirectoryIdentity{ID: "carol", Email: "carol@corp.test"}
	if _, err := svc.ResolveDirectoryUser(id, false); statusOf(err) != 403 || !strings.Contains(err.Error(), "OMIXOR_LDAP_TRUST_MAIL") {
		t.Fatalf("預設不對上:%v", err)
	}
	svc.LDAPTrustMail = true
	if u, err := svc.ResolveDirectoryUser(id, false); err != nil || u.ID != "u_pending" || u.EvoleapID != "ldap:carol" {
		t.Fatalf("對上名單:%+v %v", u, err)
	}
	// email 已被另一個身分用:不能接管
	_ = st.SaveUser(model.User{ID: "u_evo", EvoleapID: "evo-1", Email: "dave@corp.test", Role: "member", CreatedAt: model.Now()})
	if _, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "dave", Email: "dave@corp.test"}, false); statusOf(err) != 403 || !strings.Contains(err.Error(), "已經是另一個帳號在用") {
		t.Fatalf("email 衝突:%v", err)
	}
}

func TestResolveDirectoryUserDisabled(t *testing.T) {
	svc, _, st := ldapEnv(t)
	u, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "amy", Email: "amy@corp.test"}, false)
	if err != nil {
		t.Fatal(err)
	}
	u.Disabled = true
	_ = st.SaveUser(*u)
	if _, err := svc.ResolveDirectoryUser(service.DirectoryIdentity{ID: "amy", Email: "amy@corp.test"}, false); statusOf(err) != 403 {
		t.Fatalf("停用:%v", err)
	}
}

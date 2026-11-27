package auth

import (
	"testing"

	"omixor-web/internal/auth/authtest"
)

// 管理員只看帳號名單:名單空白沒有人是管理員(舊的 AdminGroup 不算);OMIXOR_LDAP_ADMINS 蓋掉名單;
// 每個請求照目前的名單,不用登入當下的快照
func TestAdminsByAccountList(t *testing.T) {
	f := authtest.Start(t)
	s := newSvc("svc-pw")
	cfg := searchCfg(f)
	cfg.Admins, cfg.LDAP.AdminGroup = nil, authtest.AdminsDN
	if err := s.Save(cfg, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Save(Config{Mode: "off"}, nil) })
	tok, u, err := signIn(s, "alice", "alice-pw", "127.0.0.1")
	if err != nil || u.Admin {
		t.Fatalf("名單空白不該有管理員:%+v %v", u, err)
	}
	if w := s.Config().Warnings(); len(w) < 1 {
		t.Fatal("名單空白要警告")
	}
	t.Setenv("OMIXOR_LDAP_ADMINS", " ALICE , someone")
	if got, ok := s.User(tok); !ok || !got.Admin {
		t.Fatalf("環境變數名單要立刻生效:%+v", got)
	}
	cfg.Admins = []string{"bob"}
	if cfg.IsAdminAccount("bob") || !cfg.IsAdminAccount("alice") {
		t.Fatal("有設環境變數時以它為準")
	}
	t.Setenv("OMIXOR_LDAP_ADMINS", "")
	if got, _ := s.User(tok); got.Admin {
		t.Fatal("拿掉後立刻不是管理員")
	}
	if off := (Config{Mode: "off"}); off.Warnings() != nil {
		t.Fatal("沒啟用 LDAP 不警告")
	}
}

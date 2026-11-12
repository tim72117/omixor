// fake-ldap:開發時試登入用的假 LDAP 伺服器(只聽 127.0.0.1)。
//
//	go run ./tools/fake-ldap            # ldap://127.0.0.1:3389
//
// 帳號:alice / alice-pw(管理員)、bob / bob-pw(一般使用者)、carol / carol-pw(不在允許的群組)。
// 服務帳號 cn=ops-svc,ou=svc,dc=example,dc=com / svc-pw;Base DN dc=example,dc=com;
// 允許群組 cn=ops-users,ou=groups,dc=example,dc=com;管理員群組 cn=ops-admins,ou=groups,dc=example,dc=com。
package main

import (
	"log"
	"os"
	"os/signal"

	"omixor-web/internal/auth/authtest"
)

func main() {
	f, stop, err := authtest.StartServer("127.0.0.1:3389")
	if err != nil {
		log.Fatal(err)
	}
	defer stop()
	log.Printf("假 LDAP:ldap://%s", f.Addr)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
}

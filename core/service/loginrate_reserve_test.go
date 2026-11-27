package service_test

import (
	"fmt"
	"testing"

	"omixor-core/service"
)

// 少數來源把整個入口灌滿失敗嘗試,沒失敗過的來源照樣能登入;失敗嘗試的總數仍不超過 LoginTotal
func TestVerifyDirectoryReserveForCleanSources(t *testing.T) {
	svc, _, _ := ldapEnv(t)
	svc.LoginPerIP, svc.LoginTotal = 10, 60
	set := svc.LDAPSettings()
	set.Rate = &service.LoginRate{}
	failed := 0
	for i := 0; i < 200; i++ {
		_, err := svc.VerifyDirectory(set, fmt.Sprintf("x%d", i), "wrong", fmt.Sprintf("9.9.9.%d", i%8))
		if statusOf(err) == 401 {
			failed++
		}
	}
	// 40 個一般名額;8 個來源的第一次都在一般名額裡
	if failed != 40 {
		t.Fatalf("少數來源送到目錄 %d 次,要 40", failed)
	}
	for i := 0; i < 3; i++ { // 成功不佔名額
		if _, err := svc.VerifyDirectory(set, "amy", "pw-amy", "8.8.8.8"); err != nil {
			t.Fatal("沒失敗過的來源要能登入:", err)
		}
	}
	// 大量新來源:每個只多一次,總數停在 60
	for i := 0; i < 100; i++ {
		_, err := svc.VerifyDirectory(set, fmt.Sprintf("y%d", i), "wrong", fmt.Sprintf("7.7.7.%d", i))
		if statusOf(err) == 401 {
			failed++
		} else if se, ok := err.(*service.Error); !ok || se.Data[service.DataRateLimited] != true {
			t.Fatal("要標成太頻繁:", err)
		}
	}
	if failed != 60 {
		t.Fatalf("總共送到目錄 %d 次,要 60", failed)
	}
}

// server 用的共用限額:429 的 Data 不多帶東西
func TestVerifyDirectorySharedRateDataUnchanged(t *testing.T) {
	svc, _, _ := ldapEnv(t)
	svc.LoginPerIP = 1
	_, _ = svc.VerifyDirectory(svc.LDAPSettings(), "x", "wrong", "9.9.9.9")
	_, err := svc.VerifyDirectory(svc.LDAPSettings(), "x", "wrong", "9.9.9.9")
	se, ok := err.(*service.Error)
	if !ok || se.Code != 429 || len(se.Data) != 1 {
		t.Fatalf("%#v", err)
	}
}

package service_test

import (
	"fmt"
	"testing"
)

// 同一個 /64 內輪換 IPv6 位址不能繞過每來源限額;不同 /64 互不影響

func TestEntrypointLimitsIPv6Prefix(t *testing.T) {
	svc, _, _ := pairEnv(t)
	svc.LoginPerIP, svc.LoginTotal = 10, 1000 // pairEnv 預設放寬到 1000,這裡要驗證每個來源 10 次的限額
	for i := 1; i <= 10; i++ {
		_, err := svc.ClaimPairCode("AAAAAAAA", "x", fmt.Sprintf("2001:db8:1:2::%x", i))
		if statusOf(err) != 401 {
			t.Fatalf("第 %d 次應放行(401):%v", i, err)
		}
	}
	if _, err := svc.ClaimPairCode("AAAAAAAA", "x", "2001:db8:1:2:aaaa:bbbb:cccc:dddd"); statusOf(err) != 429 {
		t.Fatalf("同一 /64 第 11 次要 429:%v", err)
	}
	if _, err := svc.ClaimPairCode("AAAAAAAA", "x", "2001:db8:1:3::1"); statusOf(err) != 401 {
		t.Fatalf("不同 /64 不該被影響:%v", err)
	}
	if _, err := svc.ClaimPairCode("AAAAAAAA", "x", "9.9.9.9"); statusOf(err) != 401 {
		t.Fatalf("IPv4 不該被影響:%v", err)
	}
}

func TestAgentFailIPv6Prefix(t *testing.T) {
	svc, _, _ := pairEnv(t)
	for i := 1; i <= 11; i++ {
		_, err := svc.AuthenticateAgent("nobody", "bad-secret", fmt.Sprintf("2001:db8:1:2::%x", i))
		if statusOf(err) != 401 {
			t.Fatalf("第 %d 次失敗應是 401:%v", i, err)
		}
	}
	if _, err := svc.AuthenticateAgent("nobody", "bad-secret", "2001:db8:1:2:1:2:3:4"); statusOf(err) != 429 {
		t.Fatalf("同一 /64 失敗超過 10 次要 429:%v", err)
	}
	if _, err := svc.AuthenticateAgent("nobody", "bad-secret", "2001:db8:1:3::1"); statusOf(err) != 401 {
		t.Fatalf("不同 /64 不該被擋:%v", err)
	}
}

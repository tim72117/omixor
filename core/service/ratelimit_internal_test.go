package service

import (
	"fmt"
	"testing"
	"time"
)

func TestClientIPFrom(t *testing.T) {
	cases := []struct {
		name, remote, xff string
		hops              int
		want              string
	}{
		{"層數 0 忽略偽造的 XFF", "10.0.0.9:5555", "6.6.6.6, 7.7.7.7", 0, "10.0.0.9"},
		{"負數當 0", "10.0.0.9:5555", "6.6.6.6", -1, "10.0.0.9"},
		{"層數 1 取最右(左邊是偽造的)", "10.0.0.9:5555", "6.6.6.6, 1.2.3.4", 1, "1.2.3.4"},
		{"層數 1 只有一項", "10.0.0.9:5555", "1.2.3.4", 1, "1.2.3.4"},
		{"層數 2 取右數第二個", "10.0.0.9:5555", "6.6.6.6, 1.2.3.4, 35.1.1.1", 2, "1.2.3.4"},
		{"項目不足退回連線位址", "10.0.0.9:5555", "1.2.3.4", 2, "10.0.0.9"},
		{"沒有 XFF 退回連線位址", "10.0.0.9:5555", "", 1, "10.0.0.9"},
		{"非法 IP 退回連線位址", "10.0.0.9:5555", "1.2.3.4, not-an-ip", 1, "10.0.0.9"},
		{"含埠的 IPv4 取位址部分(行為變更:以前退回連線位址)", "10.0.0.9:5555", "1.2.3.4:80", 1, "1.2.3.4"},
		{"空項目退回", "10.0.0.9:5555", "1.2.3.4,", 1, "10.0.0.9"},
		{"IPv6", "[2001:db8::9]:5555", "6.6.6.6, 2001:db8::1", 1, "2001:db8::1"},
		{"IPv6 連線位址", "[2001:db8::9]:5555", "", 1, "2001:db8::9"},
		{"IPv4-mapped 轉成 IPv4", "10.0.0.9:5555", "::ffff:1.2.3.4", 1, "1.2.3.4"},
		{"連線位址沒有埠", "10.0.0.9", "", 0, "10.0.0.9"},
	}
	for _, c := range cases {
		if got := ClientIPFrom(c.remote, c.xff, c.hops); got != c.want {
			t.Errorf("%s:ClientIPFrom(%q, %q, %d) = %q,要 %q", c.name, c.remote, c.xff, c.hops, got, c.want)
		}
	}
}

func TestFailCounterEvictsInsteadOfClearing(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	c := &failCounter{max: 100}
	win := time.Minute
	// 一個已經被限制的來源
	for i := 0; i < 11; i++ {
		c.add("victim-blocked", now, win)
	}
	// 灌大量不同的來源(遠超過上限)
	for i := 0; i < 5000; i++ {
		c.add(fmt.Sprintf("flood-%d", i), now, win)
	}
	if c.size() > 100 {
		t.Fatalf("表超過上限:%d", c.size())
	}
	if n, _ := c.get("victim-blocked", now, win); n != 11 {
		t.Fatalf("已被限制的來源被洗掉了:n=%d", n)
	}
	if got := c.add("victim-blocked", now, win); got != 12 {
		t.Fatalf("計數要接著算:%d", got)
	}
}

func TestFailCounterWindowAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	c := &failCounter{max: 3}
	win := time.Minute
	c.add("a", now, win)
	c.add("a", now, win)
	if n, _ := c.get("a", now.Add(30*time.Second), win); n != 2 {
		t.Fatal("視窗內要累計", n)
	}
	if n, _ := c.get("a", now.Add(time.Minute), win); n != 0 {
		t.Fatal("過了視窗要歸零", n)
	}
	if got := c.add("a", now.Add(2*time.Minute), win); got != 1 {
		t.Fatal("過期後重新算", got)
	}
	// 滿了先丟過期的
	c.add("b", now, win)
	c.add("c", now, win)
	later := now.Add(5 * time.Minute)
	c.add("d", later, win) // b、c、a 都過期,應被清掉,d 進得去
	if c.size() > 3 {
		t.Fatal("超過上限", c.size())
	}
	c.clear("d")
	if n, _ := c.get("d", later, win); n != 0 {
		t.Fatal("clear 失敗")
	}
}

func TestAgentFailTableBounded(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	s := &Service{Now: func() time.Time { return now }}
	s.agents.fails.max = 50
	for i := 0; i < 11; i++ {
		s.agentFailed("1.1.1.1")
	}
	if !s.agentBlocked("1.1.1.1") {
		t.Fatal("失敗超過 10 次要被擋")
	}
	for i := 0; i < 2000; i++ {
		s.agentFailed(fmt.Sprintf("10.1.%d.%d", i/250, i%250))
	}
	if !s.agentBlocked("1.1.1.1") {
		t.Fatal("灌不同來源後,已被擋的來源不該被放行")
	}
	if s.agents.fails.size() > 50 {
		t.Fatal("表超過上限")
	}
}

func TestLoginLimitsIndependent(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	s := &Service{Now: func() time.Time { return now }, LoginPerIP: 2, LoginTotal: 3}
	// 用完配對的全域額度
	for i := 0; i < 3; i++ {
		if !s.allowLogin(&s.logins.pair, fmt.Sprintf("9.9.9.%d", i)) {
			t.Fatal("額度內要放行")
		}
	}
	if s.allowLogin(&s.logins.pair, "1.1.1.1") {
		t.Fatal("配對的全域額度用完要擋")
	}
	if !s.allowLogin(&s.logins.ldap, "1.1.1.1") || !s.allowLogin(&s.logins.evoleap, "1.1.1.1") {
		t.Fatal("別的入口不該受影響")
	}
}

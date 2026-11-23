package service

import "testing"

func TestClientIPFromPortAndBrackets(t *testing.T) {
	cases := []struct {
		name, remote, xff string
		hops              int
		want              string
	}{
		{"IPv4 帶埠", "10.0.0.9:5555", "6.6.6.6, 1.2.3.4:5678", 1, "1.2.3.4"},
		{"IPv6 帶埠", "10.0.0.9:5555", "[::1]:80", 1, "::1"},
		{"IPv6 方括號無埠", "10.0.0.9:5555", "[2001:db8::1]", 1, "2001:db8::1"},
		{"純 IPv6", "10.0.0.9:5555", "2001:db8::1", 1, "2001:db8::1"},
		{"IPv4-mapped 帶埠", "10.0.0.9:5555", "[::ffff:1.2.3.4]:80", 1, "1.2.3.4"},
		{"壞的方括號退回", "10.0.0.9:5555", "[nope]", 1, "10.0.0.9"},
		{"空方括號退回", "10.0.0.9:5555", "[]", 1, "10.0.0.9"},
		{"舊案例:非法退回", "10.0.0.9:5555", "1.2.3.4, not-an-ip", 1, "10.0.0.9"},
		{"舊案例:IPv4", "10.0.0.9:5555", "6.6.6.6, 1.2.3.4", 1, "1.2.3.4"},
		{"舊案例:層數 0", "10.0.0.9:5555", "1.2.3.4:80", 0, "10.0.0.9"},
	}
	for _, c := range cases {
		if got := ClientIPFrom(c.remote, c.xff, c.hops); got != c.want {
			t.Errorf("%s:ClientIPFrom(%q, %q, %d) = %q,要 %q", c.name, c.remote, c.xff, c.hops, got, c.want)
		}
	}
}

func TestLimitKey(t *testing.T) {
	if a, b := limitKey("2001:db8:1:2::1"), limitKey("2001:db8:1:2:ffff:ffff:ffff:ffff"); a != b {
		t.Errorf("同一個 /64 要同一把鍵:%q %q", a, b)
	}
	if a, b := limitKey("2001:db8:1:2::1"), limitKey("2001:db8:1:3::1"); a == b {
		t.Errorf("不同 /64 要不同鍵:%q", a)
	}
	if got := limitKey("1.2.3.4"); got != "1.2.3.4" {
		t.Errorf("IPv4 原樣:%q", got)
	}
	if got := limitKey("::ffff:1.2.3.4"); got != limitKey("1.2.3.4") {
		t.Errorf("IPv4-mapped 要等於 IPv4 鍵:%q", got)
	}
	if got := limitKey("fe80::1%eth0"); got != limitKey("fe80::2") {
		t.Errorf("zone 要忽略:%q", got)
	}
	for _, bad := range []string{"", "ip", "not-an-ip", "1.2.3", "[::1]"} {
		if got := limitKey(bad); got != bad {
			t.Errorf("壞字串原樣:%q -> %q", bad, got)
		}
	}
}

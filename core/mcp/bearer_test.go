package mcp

import (
	"net/http/httptest"
	"testing"
)

func TestBearerToken(t *testing.T) {
	cases := []struct {
		h    string
		want string
		ok   bool
	}{
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer  abc  ", "abc", true},
		{"Bearerabc", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Bearer    ", "", false},
		{"Basic abc", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if c.h != "" {
			r.Header.Set("Authorization", c.h)
		}
		got, ok := BearerToken(r)
		if got != c.want || ok != c.ok {
			t.Errorf("%q:得到 (%q,%v),要 (%q,%v)", c.h, got, ok, c.want, c.ok)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	ok := []struct{ h, extra string }{
		{"localhost", ""}, {"localhost:3765", ""}, {"LOCALHOST:3765", ""}, {"127.0.0.1", ""}, {"127.0.0.1:3765", ""},
		{"[::1]", ""}, {"[::1]:3765", ""}, {"::1", ""}, {"localhost.:3765", ""},
		{"10.0.0.5:3765", "10.0.0.5"}, {"mcp.lan", "mcp.lan"}, {"[fd00::5]:3765", "fd00::5"},
		{"localhost:3765", "0.0.0.0"},
	}
	for _, c := range ok {
		if !HostAllowed(c.h, c.extra) {
			t.Errorf("%q(extra=%q)該放行", c.h, c.extra)
		}
	}
	bad := []struct{ h, extra string }{
		{"", ""}, {"evil.example", ""}, {"evil.example:3765", ""}, {"localhost.evil.example", ""}, {"127.0.0.1.evil.example:3765", ""},
		{"localhost:abc", ""}, {"localhost:99999", ""}, {"localhost:80:80", ""}, {"0.0.0.0:3765", "0.0.0.0"}, {"10.0.0.5", ""},
		{"10.0.0.6:3765", "10.0.0.5"}, {"evil.example", "::"},
	}
	for _, c := range bad {
		if HostAllowed(c.h, c.extra) {
			t.Errorf("%q(extra=%q)不該放行", c.h, c.extra)
		}
	}
}

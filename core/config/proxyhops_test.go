package config

import (
	"testing"
)

func hopsCfg(env map[string]string) *Config {
	return LoadFrom(func(k string) string { return env[k] })
}

func TestProxyHops(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want int
	}{
		{"都沒有", map[string]string{}, 0},
		{"只有 K_SERVICE", map[string]string{"K_SERVICE": "omixor"}, 1},
		{"明確設 2", map[string]string{"OMIXOR_TRUSTED_PROXY_HOPS": "2"}, 2},
		{"明確設 2 優先於 K_SERVICE", map[string]string{"OMIXOR_TRUSTED_PROXY_HOPS": "2", "K_SERVICE": "omixor"}, 2},
		{"明確設 0 即使有 K_SERVICE", map[string]string{"OMIXOR_TRUSTED_PROXY_HOPS": "0", "K_SERVICE": "omixor"}, 0},
	}
	for _, c := range cases {
		if got := hopsCfg(c.env).ProxyHops(); got != c.want {
			t.Errorf("%s:%d,要 %d", c.name, got, c.want)
		}
	}
}

package mcp

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	for h, want := range map[string]bool{
		"localhost": true, "foo.localhost": true, "127.0.0.1": true, "127.1.2.3": true,
		"::1": true, "[::1]": true, "127.example.com": false, "example.com": false, "10.0.0.1": false,
	} {
		if got := isLoopbackHost(h); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", h, got, want)
		}
	}
}

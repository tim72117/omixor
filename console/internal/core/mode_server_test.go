package core

import "testing"

func TestServerOrgIDAndKey(t *testing.T) {
	t.Setenv("OMIXOR_SERVERS", `[{"id":"cloud","url":"https://a.example.com"},{"id":"local test","url":"http://127.0.0.1:8080"}]`)
	for in, want := range map[string]string{"~cloud~default": "default", "~default": "default", "~x~a~b": "a~b"} {
		if got := serverOrgID(in); got != want {
			t.Errorf("serverOrgID(%q) = %q,要 %q", in, got, want)
		}
	}
	if k := serverKey("https://a.example.com"); k != "cloud" {
		t.Fatal(k)
	}
	if k := serverKey("http://127.0.0.1:8080"); k != "local_test" {
		t.Fatal("名稱只留英數字", k)
	}
	if k := serverKey("http://other.example.com:9000"); k != "other.example.com_9000" {
		t.Fatal("不在設定裡用 host", k)
	}
	if !isLegacyServer("https://a.example.com") || isLegacyServer("http://127.0.0.1:8080") {
		t.Fatal("舊格式快取只屬於第一個遠端")
	}
	if remoteOrgPrefixFor("http://127.0.0.1:8080") != "~local_test~" {
		t.Fatal(remoteOrgPrefixFor("http://127.0.0.1:8080"))
	}
}

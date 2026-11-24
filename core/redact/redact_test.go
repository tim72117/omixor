package redact

import (
	"strings"
	"testing"
)

func TestSecretsMasks(t *testing.T) {
	for _, in := range []string{"Authorization: Bearer abc.def", "omx_abc123DEF", "postgres://u:p@h/db", "password=hunter2", "ghp_abcdefgh12345678", "Cookie: a=b", strings.Repeat("A", 40)} {
		if out := Secrets("x " + in + " y"); !strings.Contains(out, Masked) || strings.Contains(out, "hunter2") || strings.Contains(out, "abc123DEF") {
			t.Errorf("%q → %q 沒遮蔽", in, out)
		}
	}
	if out := Secrets("plain text oc_client ISS-12"); out != "plain text oc_client ISS-12" {
		t.Fatal(out)
	}
}

// 獨立的 git 物件 id(40 / 64 位小寫十六進位)保留,其他規則照遮;一般長字串仍被遮
func TestSecretsKeepGitIDs(t *testing.T) {
	sha1 := strings.Repeat("a1", 20)
	sha256 := strings.Repeat("b2", 32)
	in := "This reverts commit " + sha1 + ".\nparent: " + sha256 + "\npassword=hunter2 and " + strings.Repeat("Z", 45)
	out := SecretsKeepGitIDs(in)
	if !strings.Contains(out, sha1) || !strings.Contains(out, sha256) {
		t.Fatalf("git id 要保留:%q", out)
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, strings.Repeat("Z", 45)) {
		t.Fatalf("其他機密要遮:%q", out)
	}
	if out := Secrets(sha1); out != Masked {
		t.Fatalf("Secrets 本身行為不變(長字串遮蔽):%q", out)
	}
	if out := SecretsKeepGitIDs("x" + sha1 + "y"); strings.Contains(out, sha1) {
		t.Fatalf("黏在別的字元上的長字串不算 git id:%q", out)
	}
	if out := SecretsKeepGitIDs("no ids here"); out != "no ids here" {
		t.Fatal(out)
	}
}

func TestStripControlAndCap(t *testing.T) {
	if got := StripControl("a\x00b\x07c​d\r\ne\tf", true); got != "abcd\ne\tf" {
		t.Fatalf("%q", got)
	}
	if got := StripControl(" a \n b\t c ", false); got != "a b c" {
		t.Fatalf("%q", got)
	}
	if got := StripControl("ok\xffbad", false); got != "okbad" {
		t.Fatalf("%q", got)
	}
	if Cap("你好世界", 2) != "你好" || Cap("ab", 5) != "ab" {
		t.Fatal("cap")
	}
}

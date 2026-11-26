package secretbox

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"omixor-core/config"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func mustAES(t *testing.T, active []byte, old ...[]byte) Box {
	t.Helper()
	b, err := NewAES(active, old...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustSeal(t *testing.T, b Box, plain string) string {
	t.Helper()
	s, err := b.Seal([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoundtrip(t *testing.T) {
	for name, b := range map[string]Box{"aes": mustAES(t, key(1)), "plain": NewPlain()} {
		for _, in := range []string{"", "whsec_abc123", "含中文與 : 冒號 ::", strings.Repeat("x", 5000)} {
			s := mustSeal(t, b, in)
			got, err := b.Open(s)
			if err != nil || string(got) != in {
				t.Errorf("%s: roundtrip %q -> %q, %v", name, in, got, err)
			}
		}
	}
}

func TestFormatAndNoPlaintextLeak(t *testing.T) {
	const secret = "whsec_super-secret-value"
	s := mustSeal(t, mustAES(t, key(1)), secret)
	if !strings.HasPrefix(s, "v1:"+KeyID(key(1))+":") {
		t.Errorf("format: %s", s)
	}
	if strings.Contains(s, secret) || strings.Contains(s, base64.RawURLEncoding.EncodeToString([]byte(secret))) {
		t.Error("sealed output contains plaintext")
	}
	if p := mustSeal(t, NewPlain(), "abc"); !strings.HasPrefix(p, "v0::") {
		t.Errorf("v0 format: %s", p)
	}
	if strings.Contains(KeyID(key(1)), hex.EncodeToString(key(1))) || len(KeyID(key(1))) != 8 {
		t.Error("key id shape")
	}
}

func TestFreshNonce(t *testing.T) {
	b := mustAES(t, key(1))
	if mustSeal(t, b, "same") == mustSeal(t, b, "same") {
		t.Error("two Seal calls produced identical output")
	}
}

func TestTamperAndWrongKey(t *testing.T) {
	b := mustAES(t, key(1))
	s := mustSeal(t, b, "payload")
	i := strings.LastIndex(s, ":") + 3
	flipped := s[:i] + map[bool]string{true: "B", false: "A"}[s[i] == 'A'] + s[i+1:]
	for name, c := range map[string]struct {
		box    Box
		sealed string
	}{
		"tampered body":   {b, flipped},
		"wrong key":       {mustAES(t, key(2)), s},
		"tampered keyid":  {mustAES(t, key(1), key(2)), strings.Replace(s, KeyID(key(1)), KeyID(key(2)), 1)},
		"truncated":       {b, s[:len(s)-30]},
		"garbage":         {b, "nonsense"},
		"bad version":     {b, "v9:abc:def"},
		"plain box on v1": {NewPlain(), s},
	} {
		if _, err := c.box.Open(c.sealed); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestUnknownKeyID(t *testing.T) {
	s := mustSeal(t, mustAES(t, key(1)), "x")
	_, err := mustAES(t, key(2)).Open(s)
	if err == nil || !strings.Contains(err.Error(), KeyID(key(1))) {
		t.Errorf("want unknown key id error naming the id, got %v", err)
	}
}

func TestKeyringRotation(t *testing.T) {
	a, b := key(1), key(2)
	old := mustSeal(t, mustAES(t, a), "secret")
	rotated := mustAES(t, b, a)
	if got, err := rotated.Open(old); err != nil || string(got) != "secret" {
		t.Fatalf("old key value must still open: %q %v", got, err)
	}
	out, changed, err := Reseal(rotated, old)
	if err != nil || !changed || !strings.HasPrefix(out, "v1:"+KeyID(b)+":") {
		t.Fatalf("reseal to active: %q %v %v", out, changed, err)
	}
	if _, err := mustAES(t, b).Open(out); err != nil { // 只剩新金鑰也開得了
		t.Errorf("resealed value needs only the new key: %v", err)
	}
	if again, changed, _ := Reseal(rotated, out); changed || again != out {
		t.Error("already-active value must be left alone")
	}
}

func TestResealV0AndPlain(t *testing.T) {
	v0 := mustSeal(t, NewPlain(), "legacy")
	aesBox := mustAES(t, key(1))
	if got, err := aesBox.Open(v0); err != nil || string(got) != "legacy" {
		t.Fatalf("v0 must open under AES box: %q %v", got, err)
	}
	out, changed, err := Reseal(aesBox, v0)
	if err != nil || !changed || !strings.HasPrefix(out, "v1:") {
		t.Fatalf("v0 -> v1: %q %v %v", out, changed, err)
	}
	if _, changed, _ := Reseal(NewPlain(), v0); changed {
		t.Error("plain box has nothing to upgrade to")
	}
	if _, _, err := Reseal(aesBox, "junk"); err == nil {
		t.Error("malformed value must error")
	}
	if _, _, err := Reseal(mustAES(t, key(9)), out); err == nil {
		t.Error("unopenable value must error, not silently pass")
	}
}

func TestInvalidKeys(t *testing.T) {
	for _, k := range [][]byte{nil, make([]byte, 16), make([]byte, 33)} {
		if _, err := NewAES(k); err == nil {
			t.Errorf("len %d accepted", len(k))
		}
	}
	if _, err := NewAES(key(1), []byte("short-old-key")); err == nil || strings.Contains(err.Error(), "short-old-key") {
		t.Errorf("old key error: %v", err)
	}
}

func cfgOf(env map[string]string) *config.Config {
	return config.LoadFrom(func(k string) string { return env[k] })
}

func TestFromConfig(t *testing.T) {
	hexA := hex.EncodeToString(key(1))
	b64B := base64.StdEncoding.EncodeToString(key(2))
	urlC := base64.RawURLEncoding.EncodeToString(key(3))
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		wantV   string // Seal 結果前綴
	}{
		{"no key -> plain", nil, false, "v0::"},
		{"hex", map[string]string{"OMIXOR_SECRET_KEY": hexA}, false, "v1:" + KeyID(key(1))},
		{"std base64", map[string]string{"OMIXOR_SECRET_KEY": b64B}, false, "v1:" + KeyID(key(2))},
		{"url base64 + old keys", map[string]string{"OMIXOR_SECRET_KEY": urlC, "OMIXOR_SECRET_KEYS_OLD": hexA + ", " + b64B}, false, "v1:" + KeyID(key(3))},
		{"bad key", map[string]string{"OMIXOR_SECRET_KEY": "not-a-key-SECRETVALUE"}, true, ""},
		{"short base64", map[string]string{"OMIXOR_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 16))}, true, ""},
		{"bad old key", map[string]string{"OMIXOR_SECRET_KEY": hexA, "OMIXOR_SECRET_KEYS_OLD": hexA + ",zzz-OLDSECRET"}, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := FromConfig(cfgOf(tc.env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err != nil {
				for _, v := range tc.env {
					if strings.Contains(err.Error(), v) || strings.Contains(err.Error(), "SECRETVALUE") {
						t.Errorf("error leaks value: %v", err)
					}
				}
				return
			}
			if s := mustSeal(t, b, "x"); !strings.HasPrefix(s, tc.wantV) {
				t.Errorf("sealed %q, want prefix %q", s, tc.wantV)
			}
		})
	}
}

func TestFromConfigRotationOpensOld(t *testing.T) {
	sealed := mustSeal(t, mustAES(t, key(1)), "rotate-me")
	b, err := FromConfig(cfgOf(map[string]string{
		"OMIXOR_SECRET_KEY": hex.EncodeToString(key(2)), "OMIXOR_SECRET_KEYS_OLD": hex.EncodeToString(key(1)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.Open(sealed); err != nil || string(got) != "rotate-me" {
		t.Errorf("%q %v", got, err)
	}
}

func TestConfigValidateKeys(t *testing.T) {
	bad := cfgOf(map[string]string{"OMIXOR_SECRET_KEY": "SECRETVALUE", "OMIXOR_SECRET_KEYS_OLD": "OLDVALUE"})
	err := bad.Validate(config.Server)
	if err == nil || !strings.Contains(err.Error(), "OMIXOR_SECRET_KEY") || !strings.Contains(err.Error(), "OMIXOR_SECRET_KEYS_OLD") {
		t.Fatalf("validate should name both variables: %v", err)
	}
	if strings.Contains(err.Error(), "SECRETVALUE") || strings.Contains(err.Error(), "OLDVALUE") {
		t.Errorf("validate leaks value: %v", err)
	}
	good := cfgOf(map[string]string{"OMIXOR_SECRET_KEY": hex.EncodeToString(key(1))})
	if err := good.Validate(config.Server); err != nil && strings.Contains(err.Error(), "SECRET_KEY") {
		t.Errorf("valid key rejected: %v", err)
	}
}

func TestDiagnose(t *testing.T) {
	k := hex.EncodeToString(key(1))
	tests := []struct {
		env  map[string]string
		lvl  Level
		part string
	}{
		{nil, Warn, "without encryption (v0)"},
		{map[string]string{"OMIXOR_SECRET_KEY": k}, Info, KeyID(key(1))},
		{map[string]string{"OMIXOR_SECRET_KEY": "bad"}, Fatal, "OMIXOR_SECRET_KEY"},
	}
	for _, tc := range tests {
		lvl, msg := Diagnose(cfgOf(tc.env))
		if lvl != tc.lvl || !strings.Contains(msg, tc.part) || strings.Contains(msg, k) {
			t.Errorf("%v: got %v %q", tc.env, lvl, msg)
		}
	}
}

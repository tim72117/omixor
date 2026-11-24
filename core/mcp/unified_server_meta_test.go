package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
)

func (u *uni) metaUnified(p *model.Principal, publicURL string, cloud bool) http.Handler {
	return mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{PublicURL: publicURL, Cloud: cloud,
		Resolve: func(*http.Request) (backend.Backend, error) { return &backend.Local{Svc: u.svc, P: p}, nil }})
}

func serverMeta(t *testing.T, r result) map[string]any {
	t.Helper()
	m, _ := r.meta[mcp.ServerMetaKey].(map[string]any)
	if m == nil {
		t.Fatalf("no omixor/server in _meta: %v", r.meta)
	}
	for _, k := range []string{"name", "origin", "version", "env"} {
		if s, _ := m[k].(string); s == "" {
			t.Fatalf("omixor/server.%s missing: %v", k, m)
		}
	}
	if len(m) != 4 {
		t.Fatalf("unexpected keys: %v", m)
	}
	return m
}

func TestUnifiedServerMeta(t *testing.T) {
	u := newUni(t)
	ro := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, true)
	rw := u.oauthPrincipal(model.User{ID: "u1", Email: "boss@x", Role: "member"}, false)
	cases := []struct {
		name string
		h    http.Handler
		tool string
		args map[string]any
		org  bool
	}{
		{"omx read", u.metaUnified(u.boss, "https://omixor.io", true), "issues", map[string]any{"org": "lab"}, true},
		{"omx write", u.metaUnified(u.boss, "https://omixor.io", true), "issue_create", map[string]any{"org": "lab", "title": "meta"}, true},
		{"oauth read", u.metaUnified(ro, "https://omixor.io", true), "issues", map[string]any{"org": "lab"}, true},
		{"oauth write", u.metaUnified(rw, "https://omixor.io", true), "issue_create", map[string]any{"org": "lab", "title": "meta2"}, true},
		{"orgs", u.metaUnified(u.boss, "https://omixor.io", true), "orgs", map[string]any{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := callRes(t, c.h, c.tool, c.args)
			if r.isErr {
				t.Fatal(r)
			}
			m := serverMeta(t, r)
			if m["name"] != "omixor.io" || m["origin"] != "https://omixor.io" || m["env"] != "cloud" {
				t.Fatalf("unexpected: %v", m)
			}
			org, has := r.meta["omixor/org"]
			if c.org && org != "lab" || !c.org && has {
				t.Fatalf("omixor/org changed: %v", r.meta)
			}
		})
	}
}

func TestUnifiedServerMetaLocalVsCloud(t *testing.T) {
	u := newUni(t)
	local := serverMeta(t, callRes(t, u.metaUnified(u.boss, "http://127.0.0.1:8080", false), "orgs", nil))
	if local["env"] != "local" || local["name"] != "127.0.0.1:8080" || local["origin"] != "http://127.0.0.1:8080" {
		t.Fatalf("local: %v", local)
	}
	// https non-loopback public URL counts as cloud even without the explicit flag; https loopback does not
	if m := serverMeta(t, callRes(t, u.metaUnified(u.boss, "https://omixor.io", false), "orgs", nil)); m["env"] != "cloud" {
		t.Fatalf("%v", m)
	}
	if m := serverMeta(t, callRes(t, u.metaUnified(u.boss, "https://localhost:8443", false), "orgs", nil)); m["env"] != "local" {
		t.Fatalf("%v", m)
	}
	// no public URL: r.Host only, sanitized and length-limited; X-Forwarded-Host ignored
	h := u.metaUnified(u.boss, "", false)
	req := mcpReq(t, "orgs")
	req.Host = "evil\x00host" + strings.Repeat("a", 400)
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	w := serveReq(h, req)
	m := serverMeta(t, parseBody(t, w))
	if n := m["name"].(string); len(n) > 255 || strings.ContainsAny(n, "\x00 ") || strings.Contains(n, "attacker") {
		t.Fatalf("name: %q", n)
	}
	if m["env"] != "local" {
		t.Fatal(m)
	}
}

func TestUnifiedServerMetaNoSecrets(t *testing.T) {
	u := newUni(t)
	r := callRes(t, u.metaUnified(u.boss, "https://omixor.io", true), "orgs", nil)
	m := serverMeta(t, r)
	re := regexp.MustCompile(`(?i)omx_|omo_|postgres|token|secret|password|://[^/]*@`)
	for k, v := range m {
		if re.MatchString(v.(string)) {
			t.Fatalf("%s looks secret: %v", k, v)
		}
	}
}

func mcpReq(t *testing.T, tool string) *http.Request {
	t.Helper()
	b, _ := json.Marshal(rpcReq(1, tool, map[string]any{}))
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	return req
}

func serveReq(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func parseBody(t *testing.T, w *httptest.ResponseRecorder) result {
	t.Helper()
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return parseResult(t, m)
}

func TestUnifiedServerMetaFake127Host(t *testing.T) {
	u := newUni(t)
	if m := serverMeta(t, callRes(t, u.metaUnified(u.boss, "https://127.example.com", false), "orgs", nil)); m["env"] != "cloud" {
		t.Fatalf("%v", m)
	}
}

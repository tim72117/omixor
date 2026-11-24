package mcp_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
)

// MCP protocol revision 2026-07-28 (minimal subset): modern requests carry _meta protocolVersion + clientCapabilities and routing headers;
// legacy requests (no _meta version key) must behave exactly as before.

const (
	vKey  = "io.modelcontextprotocol/protocolVersion"
	cKey  = "io.modelcontextprotocol/clientCapabilities"
	siKey = "io.modelcontextprotocol/serverInfo"
)

func modernParams(extra map[string]any) map[string]any {
	p := map[string]any{"_meta": map[string]any{
		vKey: "2026-07-28", cKey: map[string]any{},
		"io.modelcontextprotocol/clientInfo": map[string]any{"name": "t", "version": "1"},
	}}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func modernHeaders(method, name string) map[string]string {
	h := map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": method}
	if name != "" {
		h["Mcp-Name"] = name
	}
	return h
}

// call POSTs one JSON-RPC request with the given headers and returns the recorder and the decoded body.
func rpc26(t *testing.T, h http.Handler, method string, params any, hdr map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 7, "method": method}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w, m
}

func errOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	e, _ := m["error"].(map[string]any)
	if e == nil {
		t.Fatalf("expected a JSON-RPC error: %v", m)
	}
	return e
}

func toolNames26(t *testing.T, m map[string]any) []string {
	t.Helper()
	res, _ := m["result"].(map[string]any)
	var out []string
	for _, d := range res["tools"].([]any) {
		out = append(out, d.(map[string]any)["name"].(string))
	}
	return out
}

func (u *uni) worker() http.Handler {
	return mcp.WorkerEndpoint(mcp.WorkerEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: u.svc, P: u.boss}, "lab", nil
	}})
}

func TestModernToolsList(t *testing.T) {
	t.Setenv("OMIXOR_MCP_LIST_TTL_SECONDS", "300")
	u := newUni(t)
	for name, h := range map[string]http.Handler{"unified": u.unified, "admin": u.old, "worker": u.worker()} {
		hdr := modernHeaders("tools/list", "")
		hdr["Mcp-Session-Id"] = "ignored-session"
		w, m := rpc26(t, h, "tools/list", modernParams(nil), hdr)
		if w.Code != 200 {
			t.Fatalf("%s: HTTP %d %s", name, w.Code, w.Body)
		}
		if w.Header().Get("Mcp-Session-Id") != "" {
			t.Fatalf("%s: modern response must not carry Mcp-Session-Id", name)
		}
		res := m["result"].(map[string]any)
		if res["resultType"] != "complete" || res["ttlMs"] != float64(300000) || res["cacheScope"] != "private" {
			t.Fatalf("%s: %v", name, res)
		}
		si, _ := res["_meta"].(map[string]any)[siKey].(map[string]any)
		if si["name"] == nil || si["version"] == nil {
			t.Fatalf("%s: missing serverInfo meta: %v", name, res["_meta"])
		}
		if len(toolNames26(t, m)) == 0 {
			t.Fatalf("%s: no tools", name)
		}
		// the same request without _meta is the legacy shape: no resultType, ttl, cacheScope or _meta
		w, m = rpc26(t, h, "tools/list", nil, nil)
		legacy := m["result"].(map[string]any)
		for _, k := range []string{"resultType", "ttlMs", "cacheScope", "_meta"} {
			if _, has := legacy[k]; has || w.Code != 200 {
				t.Fatalf("%s: legacy result must not carry %s: %v", name, k, legacy)
			}
		}
	}
}

func TestModernTTLZeroAndConfig(t *testing.T) {
	u := newUni(t)
	t.Setenv("OMIXOR_MCP_LIST_TTL_SECONDS", "0")
	_, m := rpc26(t, u.unified, "tools/list", modernParams(nil), modernHeaders("tools/list", ""))
	res := m["result"].(map[string]any)
	if v, ok := res["ttlMs"]; !ok || v != float64(0) {
		t.Fatalf("ttl 0 must be emitted as 0: %v", res)
	}
	t.Setenv("OMIXOR_MCP_LIST_TTL_SECONDS", "12")
	_, m = rpc26(t, u.unified, "tools/list", modernParams(nil), modernHeaders("tools/list", ""))
	if m["result"].(map[string]any)["ttlMs"] != float64(12000) {
		t.Fatal(m)
	}
}

func TestModernUnsupportedVersion(t *testing.T) {
	u := newUni(t)
	p := modernParams(nil)
	p["_meta"].(map[string]any)[vKey] = "2099-01-01"
	w, m := rpc26(t, u.unified, "tools/list", p, map[string]string{"MCP-Protocol-Version": "2099-01-01", "Mcp-Method": "tools/list"})
	e := errOf(t, m)
	if w.Code != 400 || e["code"] != float64(-32022) || e["message"] != "Unsupported protocol version" {
		t.Fatal(w.Code, m)
	}
	data := e["data"].(map[string]any)
	want := []any{"2026-07-28", "2025-06-18", "2025-03-26", "2024-11-05"}
	if !reflect.DeepEqual(data["supported"], want) || data["requested"] != "2099-01-01" || m["id"] != float64(7) {
		t.Fatal(m)
	}
}

func TestModernMissingClientCapabilities(t *testing.T) {
	u := newUni(t)
	for name, caps := range map[string]any{"missing": nil, "not an object": "x"} {
		p := modernParams(nil)
		meta := p["_meta"].(map[string]any)
		delete(meta, cKey)
		if caps != nil {
			meta[cKey] = caps
		}
		w, m := rpc26(t, u.unified, "tools/list", p, modernHeaders("tools/list", ""))
		if w.Code != 400 || errOf(t, m)["code"] != float64(-32602) {
			t.Fatalf("%s: %d %v", name, w.Code, m)
		}
	}
}

func TestModernHeaderMismatch(t *testing.T) {
	u := newUni(t)
	params := modernParams(map[string]any{"name": "orgs", "arguments": map[string]any{}})
	cases := map[string]map[string]string{
		"wrong method":           {"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/list", "Mcp-Name": "orgs"},
		"wrong name":             {"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "issues"},
		"missing name":           {"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call"},
		"missing method":         {"MCP-Protocol-Version": "2026-07-28", "Mcp-Name": "orgs"},
		"missing protocol":       {"Mcp-Method": "tools/call", "Mcp-Name": "orgs"},
		"protocol differs":       {"MCP-Protocol-Version": "2025-06-18", "Mcp-Method": "tools/call", "Mcp-Name": "orgs"},
		"bad base64":             {"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "=?base64?***?="},
		"name is case-sensitive": {"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call", "Mcp-Name": "Orgs"},
	}
	for name, hdr := range cases {
		w, m := rpc26(t, u.unified, "tools/call", params, hdr)
		e := errOf(t, m)
		if w.Code != 400 || e["code"] != float64(-32020) || !strings.HasPrefix(e["message"].(string), "Header mismatch: ") || m["id"] != float64(7) {
			t.Fatalf("%s: %d %v", name, w.Code, m)
		}
	}
	// message wording
	_, m := rpc26(t, u.unified, "tools/call", params, cases["wrong name"])
	if got := errOf(t, m)["message"]; got != "Header mismatch: Mcp-Name header value 'issues' does not match body value 'orgs'" {
		t.Fatal(got)
	}
	// header names are case-insensitive; all correct => 200
	w, m := rpc26(t, u.unified, "tools/call", params, map[string]string{"mcp-protocol-version": "2026-07-28", "MCP-METHOD": "tools/call", "mcp-name": "orgs"})
	if w.Code != 200 || m["result"] == nil {
		t.Fatal(w.Code, m)
	}
	// legacy requests need no headers at all
	if w, m := rpc26(t, u.unified, "tools/call", map[string]any{"name": "orgs", "arguments": map[string]any{}}, nil); w.Code != 200 || m["result"] == nil {
		t.Fatal(w.Code, m)
	}
}

func TestModernBase64HeaderValue(t *testing.T) {
	u := newUni(t)
	uri := "ui://omixor/流程圖"
	params := modernParams(map[string]any{"uri": uri})
	enc := "=?base64?" + base64.StdEncoding.EncodeToString([]byte(uri)) + "?="
	hdr := modernHeaders("resources/read", enc)
	w, m := rpc26(t, u.unified, "resources/read", params, hdr)
	// decoded value matches the body: it passes the header check and reaches resources/read (resource missing => -32602 with data.uri)
	e := errOf(t, m)
	if w.Code != 200 || e["code"] != float64(-32602) || e["data"].(map[string]any)["uri"] != uri {
		t.Fatal(w.Code, m)
	}
	// the encoded form of a different value mismatches
	hdr["Mcp-Name"] = "=?base64?" + base64.StdEncoding.EncodeToString([]byte("other")) + "?="
	if w, m := rpc26(t, u.unified, "resources/read", params, hdr); w.Code != 400 || errOf(t, m)["code"] != float64(-32020) {
		t.Fatal(w.Code, m)
	}
}

func TestModernResources(t *testing.T) {
	t.Setenv("OMIXOR_MCP_LIST_TTL_SECONDS", "300")
	u := newUni(t)
	for name, h := range map[string]http.Handler{"unified": u.unified, "admin": u.old} {
		_, m := rpc26(t, h, "resources/list", modernParams(nil), modernHeaders("resources/list", ""))
		res := m["result"].(map[string]any)
		if res["resultType"] != "complete" || res["ttlMs"] != float64(300000) || res["cacheScope"] != "private" || len(res["resources"].([]any)) == 0 {
			t.Fatalf("%s: %v", name, res)
		}
		_, m = rpc26(t, h, "resources/read", modernParams(map[string]any{"uri": mcp.UIFlowchartURI}), modernHeaders("resources/read", mcp.UIFlowchartURI))
		res = m["result"].(map[string]any)
		if res["resultType"] != "complete" || res["ttlMs"] != float64(300000) || res["cacheScope"] != "private" || len(res["contents"].([]any)) != 1 {
			t.Fatalf("%s: read: %v", name, res)
		}
		// missing uri: -32602 + data.uri for modern requests, never -32002 / empty contents; legacy keeps -32002
		_, m = rpc26(t, h, "resources/read", modernParams(map[string]any{"uri": "ui://nope"}), modernHeaders("resources/read", "ui://nope"))
		if e := errOf(t, m); e["code"] != float64(-32602) || e["data"].(map[string]any)["uri"] != "ui://nope" || m["result"] != nil {
			t.Fatalf("%s: %v", name, m)
		}
		_, m = rpc26(t, h, "resources/read", map[string]any{"uri": "ui://nope"}, nil)
		if errOf(t, m)["code"] != float64(-32002) {
			t.Fatalf("%s legacy: %v", name, m)
		}
	}
}

func TestModernUnknownMethodAndTool(t *testing.T) {
	u := newUni(t)
	w, m := rpc26(t, u.unified, "nope/method", modernParams(nil), modernHeaders("nope/method", ""))
	if w.Code != 404 || errOf(t, m)["code"] != float64(-32601) {
		t.Fatal(w.Code, m)
	}
	if w, m := rpc26(t, u.unified, "nope/method", nil, nil); w.Code != 200 || errOf(t, m)["code"] != float64(-32601) {
		t.Fatalf("legacy unknown method keeps HTTP 200: %d %v", w.Code, m)
	}
	// unknown tool: protocol error -32602 (no HTTP error status), also for modern requests
	w, m = rpc26(t, u.unified, "tools/call", modernParams(map[string]any{"name": "nope", "arguments": map[string]any{}}), modernHeaders("tools/call", "nope"))
	if w.Code != 200 || errOf(t, m)["code"] != float64(-32602) {
		t.Fatal(w.Code, m)
	}
	// modern initialize: no handshake in that revision
	w, m = rpc26(t, u.unified, "initialize", modernParams(nil), modernHeaders("initialize", ""))
	if w.Code != 404 || errOf(t, m)["code"] != float64(-32601) {
		t.Fatal(w.Code, m)
	}
}

func TestModernToolCallMeta(t *testing.T) {
	u := newUni(t)
	params := modernParams(map[string]any{"name": "issues", "arguments": map[string]any{"org": "lab"}})
	_, m := rpc26(t, u.unified, "tools/call", params, modernHeaders("tools/call", "issues"))
	res := m["result"].(map[string]any)
	meta := res["_meta"].(map[string]any)
	if res["resultType"] != "complete" || meta["omixor/org"] != "lab" || meta[mcp.ServerMetaKey] == nil || meta[siKey] == nil {
		t.Fatal(res)
	}
	if _, has := res["ttlMs"]; has {
		t.Fatal("tools/call must not carry cache hints")
	}
	// tool execution errors stay isError results (HTTP 200), no cache hints
	params = modernParams(map[string]any{"name": "issues", "arguments": map[string]any{}})
	w, m := rpc26(t, u.unified, "tools/call", params, modernHeaders("tools/call", "issues"))
	res = m["result"].(map[string]any)
	if w.Code != 200 || res["isError"] != true || res["ttlMs"] != nil {
		t.Fatal(w.Code, res)
	}
	// notifications: 202 without a body
	b := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	u.unified.ServeHTTP(rec, req)
	if rec.Code != 202 || rec.Body.Len() != 0 {
		t.Fatal(rec.Code)
	}
}

func TestServerDiscover(t *testing.T) {
	t.Setenv("OMIXOR_MCP_LIST_TTL_SECONDS", "300")
	u := newUni(t)
	// authenticated endpoint: discover goes through the same authentication as every other request
	authed := mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{Resolve: func(r *http.Request) (backend.Backend, error) {
		if r.Header.Get("Authorization") != "Bearer good" {
			return nil, errors.New("unauthorized")
		}
		return &backend.Local{Svc: u.svc, P: u.boss}, nil
	}})
	if w, _ := rpc26(t, authed, "server/discover", modernParams(nil), modernHeaders("server/discover", "")); w.Code != 401 {
		t.Fatalf("discover without Authorization must be 401, got %d", w.Code)
	}
	hdr := modernHeaders("server/discover", "")
	hdr["Authorization"] = "Bearer good"
	handlers := map[string]http.Handler{"unified": authed, "admin": u.old, "worker": u.worker()}
	for name, h := range handlers {
		w, m := rpc26(t, h, "server/discover", modernParams(nil), hdr)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
		res := m["result"].(map[string]any)
		if res["resultType"] != "complete" || res["ttlMs"] != float64(300000) || res["cacheScope"] != "private" {
			t.Fatalf("%s: %v", name, res)
		}
		if !reflect.DeepEqual(res["supportedVersions"], []any{"2026-07-28", "2025-06-18", "2025-03-26", "2024-11-05"}) {
			t.Fatalf("%s: %v", name, res["supportedVersions"])
		}
		caps := res["capabilities"].(map[string]any)
		if tools, _ := caps["tools"].(map[string]any); tools == nil || tools["listChanged"] != false {
			t.Fatalf("%s: %v", name, caps)
		}
		if _, hasRes := caps["resources"]; hasRes != (name != "worker") {
			t.Fatalf("%s: resources capability: %v", name, caps)
		}
		ins, _ := res["instructions"].(string)
		if !strings.Contains(ins, "2026-07-28") || !strings.Contains(ins, "cache hints") {
			t.Fatalf("%s: instructions: %q", name, ins)
		}
		si := res["_meta"].(map[string]any)[siKey].(map[string]any)
		if si["name"] == nil || si["version"] == nil {
			t.Fatalf("%s: %v", name, si)
		}
		// same data as legacy initialize
		_, im := rpc26(t, h, "initialize", map[string]any{"protocolVersion": "2025-06-18"}, map[string]string{"Authorization": "Bearer good"})
		ir := im["result"].(map[string]any)
		if ir["instructions"] != res["instructions"] || !reflect.DeepEqual(ir["capabilities"], res["capabilities"]) || ir["serverInfo"].(map[string]any)["name"] != si["name"] {
			t.Fatalf("%s: discover and initialize differ:\n%v\n%v", name, ir, res)
		}
	}
	// probing clients may send server/discover without _meta
	hdr = map[string]string{"Authorization": "Bearer good"}
	w, m := rpc26(t, authed, "server/discover", nil, hdr)
	if w.Code != 200 || m["result"].(map[string]any)["supportedVersions"] == nil {
		t.Fatal(w.Code, m)
	}
}

func TestLegacyInitializeUnchanged(t *testing.T) {
	u := newUni(t)
	for _, tc := range []struct{ ask, want string }{
		{"2025-06-18", "2025-06-18"}, {"2025-03-26", "2025-03-26"}, {"2024-11-05", "2024-11-05"},
		{"2026-07-28", "2025-06-18"}, // that revision has no handshake: a legacy initialize gets the latest legacy version
		{"1999-01-01", "2025-06-18"}, {"", "2025-06-18"},
	} {
		w, m := rpc26(t, u.unified, "initialize", map[string]any{"protocolVersion": tc.ask, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "c", "version": "1"}}, nil)
		res := m["result"].(map[string]any)
		if w.Code != 200 || res["protocolVersion"] != tc.want {
			t.Fatalf("ask %q: %d %v", tc.ask, w.Code, res)
		}
		for _, k := range []string{"resultType", "ttlMs", "cacheScope"} {
			if _, has := res[k]; has {
				t.Fatalf("legacy initialize must not carry %s", k)
			}
		}
	}
}

func TestToolsListOrderStable(t *testing.T) {
	u1, u2 := newUni(t), newUni(t)
	var first []string
	for i, h := range []http.Handler{u1.unified, u1.unified, u2.unified, u1.old, u1.old} {
		_, m := rpc26(t, h, "tools/list", nil, nil)
		got := toolNames26(t, m)
		_, mm := rpc26(t, h, "tools/list", modernParams(nil), modernHeaders("tools/list", ""))
		if !reflect.DeepEqual(got, toolNames26(t, mm)) {
			t.Fatal("modern and legacy tools/list order differ")
		}
		if i < 3 {
			if first == nil {
				first = got
			} else if !reflect.DeepEqual(first, got) {
				t.Fatalf("unified tools/list order differs between calls/instances")
			}
		}
	}
	// order is derived from fixed slices (spec definitions), not map iteration: running a few more times must not change it
	for i := 0; i < 20; i++ {
		_, m := rpc26(t, u1.unified, "tools/list", nil, nil)
		if !reflect.DeepEqual(first, toolNames26(t, m)) {
			t.Fatal("unstable tools/list order")
		}
	}
}

func TestModernOAuthReadOnly(t *testing.T) {
	u := newUni(t)
	ro := u.readOnlyEndpoint()
	_, legacy := rpc26(t, ro, "tools/list", nil, nil)
	w, m := rpc26(t, ro, "tools/list", modernParams(nil), modernHeaders("tools/list", ""))
	res := m["result"].(map[string]any)
	if w.Code != 200 || res["cacheScope"] != "private" || res["resultType"] != "complete" {
		t.Fatal(w.Code, res)
	}
	if !reflect.DeepEqual(toolNames26(t, legacy), toolNames26(t, m)) {
		t.Fatal("read-only tool list must be the same for modern and legacy requests")
	}
	for _, n := range toolNames26(t, m) {
		if !mcp.ReadOnlyTools[n] {
			t.Fatalf("read-only grant lists %s", n)
		}
	}
}

func TestGetAndDeleteAre405(t *testing.T) {
	u := newUni(t)
	for name, h := range map[string]http.Handler{"unified": u.unified, "admin": u.old, "worker": u.worker(), "readonly": u.readOnlyEndpoint()} {
		for _, method := range []string{"GET", "DELETE"} {
			req := httptest.NewRequest(method, "/mcp", nil)
			req.Header.Set("Last-Event-ID", "5")
			req.Header.Set("Mcp-Session-Id", "x")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 405 || w.Header().Get("Allow") != "POST" {
				t.Fatalf("%s %s: %d", name, method, w.Code)
			}
		}
	}
}

func TestMissingClientCapabilityError(t *testing.T) {
	m := mcp.MissingClientCapabilityError(3, map[string]any{"sampling": map[string]any{}})
	b, _ := json.Marshal(m)
	var got struct {
		ID    int `json:"id"`
		Error struct {
			Code int
			Data struct{ RequiredCapabilities map[string]any }
		}
	}
	_ = json.Unmarshal(b, &got)
	if got.ID != 3 || got.Error.Code != -32021 || got.Error.Data.RequiredCapabilities["sampling"] == nil {
		t.Fatal(string(b))
	}
}

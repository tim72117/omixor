package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"omixor-core/config"
)

// MCP protocol revision 2026-07-28 (minimal subset). The revision is stateless: there is no initialize handshake and every request
// carries its protocol version and client capabilities in params._meta. A request is "modern" iff _meta carries the protocolVersion key;
// everything else is legacy (2025-06-18, 2025-03-26, 2024-11-05) and behaves exactly as before.

// ModernVersion is the protocol revision without a handshake.
const ModernVersion = "2026-07-28"

// LegacyVersions are the revisions negotiated through initialize (newest first).
var LegacyVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// SupportedVersions lists every supported revision, newest first (modern first, then legacy).
// initialize only ever negotiates one of LegacyVersions.
var SupportedVersions = append([]string{ModernVersion}, LegacyVersions...)

// _meta keys of a modern request / result.
const (
	MetaProtocolVersionKey = "io.modelcontextprotocol/protocolVersion"
	MetaClientCapsKey      = "io.modelcontextprotocol/clientCapabilities"
	MetaClientInfoKey      = "io.modelcontextprotocol/clientInfo"
	MetaServerInfoKey      = "io.modelcontextprotocol/serverInfo"
)

// JSON-RPC error codes added by the revision.
const (
	CodeHeaderMismatch          = -32020
	CodeMissingClientCapability = -32021
	CodeUnsupportedVersion      = -32022
)

// DefaultListTTLSeconds is the cache TTL of list results when the configuration is not available.
const DefaultListTTLSeconds = 300

// cacheScopePrivate: every endpoint is authenticated and its tool/resource lists can vary by the caller's authorization.
const cacheScopePrivate = "private"

// ListTTLSeconds is the ttlMs source (seconds): OMIXOR_MCP_LIST_TTL_SECONDS, 0 = always stale.
func ListTTLSeconds() int {
	if n := config.Current().Int(config.MCPListTTLSeconds); n >= 0 {
		return n
	}
	return DefaultListTTLSeconds
}

// rpcError is a JSON-RPC error with optional data.
type rpcError struct {
	Code int
	Msg  string
	Data any
}

func errorResponseData(id any, e *rpcError) map[string]any {
	obj := map[string]any{"code": e.Code, "message": e.Msg}
	if e.Data != nil {
		obj["data"] = e.Data
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": obj}
}

// MissingClientCapabilityError builds the -32021 error for a request whose client did not declare a capability the method needs.
// Use it only where a tool or method truly requires a client capability (none do today).
func MissingClientCapabilityError(id any, required map[string]any) map[string]any {
	return errorResponseData(id, &rpcError{CodeMissingClientCapability, "Missing required client capability",
		map[string]any{"requiredCapabilities": required}})
}

// modernMeta returns params._meta and whether the request is modern (the version key is present).
func modernMeta(params map[string]any) (map[string]any, bool) {
	meta, _ := params["_meta"].(map[string]any)
	if meta == nil {
		return nil, false
	}
	_, ok := meta[MetaProtocolVersionKey]
	return meta, ok
}

// validateModern checks a modern request: supported version first, then the required clientCapabilities object.
// clientInfo is optional and ignored. Nothing is remembered across requests.
func validateModern(meta map[string]any) *rpcError {
	requested, _ := meta[MetaProtocolVersionKey].(string)
	if !supportedVersion(requested) {
		var shown any = requested
		if _, isStr := meta[MetaProtocolVersionKey].(string); !isStr {
			shown = meta[MetaProtocolVersionKey]
		}
		return &rpcError{CodeUnsupportedVersion, "Unsupported protocol version",
			map[string]any{"supported": supportedList(), "requested": shown}}
	}
	if _, ok := meta[MetaClientCapsKey].(map[string]any); !ok {
		return &rpcError{-32602, "Invalid params: _meta[\"" + MetaClientCapsKey + "\"] is required and must be an object", nil}
	}
	return nil
}

func supportedVersion(v string) bool {
	for _, s := range SupportedVersions {
		if s == v {
			return true
		}
	}
	return false
}

func supportedList() []any {
	out := make([]any, len(SupportedVersions))
	for i, v := range SupportedVersions {
		out[i] = v
	}
	return out
}

// capabilities is what the server advertises (initialize and server/discover): tools, plus resources when UI resources exist.
func (s *Server) capabilities(h *Hooks) map[string]any {
	caps := map[string]any{}
	if c, ok := s.initInfo["capabilities"].(map[string]any); ok {
		for k, v := range c {
			caps[k] = v
		}
	}
	if len(h.Resources) > 0 {
		caps["resources"] = map[string]any{"listChanged": false, "subscribe": false}
	}
	return caps
}

func (s *Server) discover(h *Hooks) map[string]any {
	r := map[string]any{
		"supportedVersions": supportedList(),
		"capabilities":      s.capabilities(h),
	}
	if ins, ok := s.initInfo["instructions"]; ok {
		r["instructions"] = ins
	}
	return r
}

// decorateModern adds resultType, the serverInfo meta and (for cacheable methods, complete results only) ttlMs and cacheScope.
func (s *Server) decorateModern(method string, resp map[string]any) {
	res, ok := resp["result"].(map[string]any)
	if !ok {
		return
	}
	res["resultType"] = "complete"
	meta := map[string]any{}
	if old, ok := res["_meta"].(map[string]any); ok {
		for k, v := range old {
			meta[k] = v
		}
	}
	if si, ok := s.initInfo["serverInfo"]; ok {
		meta[MetaServerInfoKey] = si
	}
	res["_meta"] = meta
	switch method {
	case "server/discover", "tools/list", "resources/list", "resources/templates/list", "resources/read", "prompts/list":
		res["ttlMs"] = ListTTLSeconds() * 1000
		res["cacheScope"] = cacheScopePrivate
	}
}

// ---- HTTP: routing headers and status mapping (modern requests only) ----

// decodeHeaderValue decodes the =?base64?<b64>?= form used for non-ASCII values.
func decodeHeaderValue(v string) (string, bool) {
	if strings.HasPrefix(v, "=?base64?") && strings.HasSuffix(v, "?=") && len(v) >= len("=?base64??=") {
		b, err := base64.StdEncoding.DecodeString(v[len("=?base64?") : len(v)-2])
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return v, true
}

func headerMismatch(id any, msg string) []byte {
	return marshal(errorResponseData(id, &rpcError{CodeHeaderMismatch, "Header mismatch: " + msg, nil}))
}

// modernPreflight validates a modern single request before dispatch. It returns (status, body) when the request must be rejected
// and modern=true when the request is modern (so the caller can map the response status afterwards). Legacy requests, batches,
// notifications and unparseable bodies pass through untouched.
func modernPreflight(r *http.Request, body []byte) (status int, out []byte, modern bool) {
	t := bytes.TrimSpace(body)
	if len(t) == 0 || t[0] != '{' {
		return 0, nil, false
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(t, &msg) != nil || msg.Method == "" || len(msg.ID) == 0 {
		return 0, nil, false
	}
	var params map[string]any
	if json.Unmarshal(msg.Params, &params) != nil {
		return 0, nil, false
	}
	meta, isModern := modernMeta(params)
	if !isModern {
		return 0, nil, false
	}
	var id any = msg.ID
	if e := validateModern(meta); e != nil {
		return 400, marshal(errorResponseData(id, e)), true
	}
	check := func(header, want string) []byte {
		got, present := r.Header.Values(header), len(r.Header.Values(header)) > 0
		if !present {
			return headerMismatch(id, header+" header is required (value '"+want+"')")
		}
		v, ok := decodeHeaderValue(got[0])
		if !ok {
			return headerMismatch(id, header+" header value is not valid base64 encoding")
		}
		if v != want {
			return headerMismatch(id, fmt.Sprintf("%s header value '%s' does not match body value '%s'", header, v, want))
		}
		return nil
	}
	if b := check("MCP-Protocol-Version", meta[MetaProtocolVersionKey].(string)); b != nil {
		return 400, b, true
	}
	if b := check("Mcp-Method", msg.Method); b != nil {
		return 400, b, true
	}
	nameKey := map[string]string{"tools/call": "name", "resources/read": "uri", "prompts/get": "name"}[msg.Method]
	if nameKey != "" {
		if want, ok := params[nameKey].(string); ok {
			if b := check("Mcp-Name", want); b != nil {
				return 400, b, true
			}
		}
	}
	return 0, nil, true
}

// modernStatus maps a modern response to its HTTP status: unknown method => 404.
func modernStatus(out []byte) int {
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(out, &resp) == nil && resp.Error != nil && resp.Error.Code == -32601 {
		return 404
	}
	return 200
}

// protocolInstructions is the one sentence appended to every endpoint's instructions.
const protocolInstructions = " This server supports MCP protocol versions 2026-07-28, 2025-06-18, 2025-03-26 and 2024-11-05; 2026-07-28 is stateless (no initialize needed, send _meta[\"io.modelcontextprotocol/protocolVersion\"] and clientCapabilities on every request, use server/discover) and returns cache hints (ttlMs, cacheScope) on list results."

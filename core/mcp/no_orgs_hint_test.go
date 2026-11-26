package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
)

// ISS-64:讀寫授權不帶伺服器管理員的權力;不是任何組織成員的伺服器管理員,orgs 是空的,要說明原因(唯讀授權照舊看到全部)

func (u *uni) adminNoMembership(readOnly bool) http.Handler {
	p := &model.Principal{User: model.User{ID: "u9", Email: "root@x", Role: "admin"}, Via: model.ViaOAuth, ReadOnly: readOnly}
	return mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{Resolve: func(*http.Request) (backend.Backend, error) {
		return &backend.Local{Svc: u.svc, P: p}, nil
	}})
}

func TestNoOrgsHintWriteGrantServerAdmin(t *testing.T) {
	u := newUni(t)
	w := u.adminNoMembership(false)
	r := callRes(t, w, "orgs", map[string]any{})
	if r.isErr || !strings.Contains(r.all(), `"orgs":[]`) || !strings.Contains(r.all(), mcp.NoOrgsWriteGrantMessage) {
		t.Fatal(r)
	}
	r = callRes(t, w, "issue_create", map[string]any{"org": "lab", "title": "x"})
	if !r.isErr || !strings.Contains(r.all(), "You do not have access to organization lab") || !strings.Contains(r.all(), mcp.NoOrgsWriteGrantMessage) {
		t.Fatal(r)
	}
	// 唯讀授權:伺服器管理員看到全部,沒有說明
	r = callRes(t, u.adminNoMembership(true), "orgs", map[string]any{})
	if r.isErr || !strings.Contains(r.all(), "lab2") || strings.Contains(r.all(), mcp.NoOrgsWriteGrantMessage) {
		t.Fatal(r)
	}
}

// 有成員資格的讀寫授權:查不到的 org 只是沒有權限,不該出現「沒有任何組織」的說明
func TestNoOrgsHintAbsentWithMemberships(t *testing.T) {
	u := newUni(t)
	p := *u.boss
	p.Via = model.ViaOAuth
	h := mcp.UnifiedEndpoint(mcp.UnifiedEndpointConfig{Resolve: func(*http.Request) (backend.Backend, error) {
		return &backend.Local{Svc: u.svc, P: &p}, nil
	}})
	r := callRes(t, h, "issue_create", map[string]any{"org": "other", "title": "x"})
	if !r.isErr || strings.Contains(r.all(), mcp.NoOrgsWriteGrantMessage) || !strings.Contains(r.all(), "Available organizations: lab") {
		t.Fatal(r)
	}
	if r := callRes(t, h, "orgs", map[string]any{}); r.isErr || strings.Contains(r.all(), mcp.NoOrgsWriteGrantMessage) {
		t.Fatal(r)
	}
}

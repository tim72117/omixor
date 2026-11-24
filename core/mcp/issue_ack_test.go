package mcp_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
)

func ackHandler(t *testing.T) (http.Handler, *service.Service, *model.Principal) {
	_, svc, amy := issueToolsEnv(t)
	h := mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(*http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: amy}, "lab", nil
	}})
	return h, svc, amy
}

func ackKeys(m map[string]any) []string {
	k := make([]string, 0, len(m))
	for key := range m {
		k = append(k, key)
	}
	slices.Sort(k)
	return k
}

// issue_create / issue_update / issue_accept default to a compact ack; return:"full" gives the whole issue
func TestIssueWriteAckShape(t *testing.T) {
	h, _, _ := ackHandler(t)

	created := ok(t, h, "", "issue_create", map[string]any{"title": "t", "body": "big body"})
	want := []string{"changed", "id", "key", "status", "updatedAt", "version"}
	if !slices.Equal(ackKeys(created), want) {
		t.Fatalf("create ack keys %v", ackKeys(created))
	}
	if ch, _ := created["changed"].([]any); len(ch) != 1 || ch[0] != "created" {
		t.Fatalf("create changed %v", created["changed"])
	}
	id, _ := created["id"].(string)

	fullCreate := ok(t, h, "", "issue_create", map[string]any{"title": "t2", "body": "b", "return": "full"})
	for _, k := range []string{"body", "comments", "createdBy", "labels", "priority", "title"} {
		if _, has := fullCreate[k]; !has {
			t.Fatalf("full create missing %s: %v", k, ackKeys(fullCreate))
		}
	}

	upd := ok(t, h, "", "issue_update", map[string]any{"id": id, "title": "new", "priority": "high", "comment": "hello"})
	if upd["version"] == created["version"] || upd["comment_id"] == nil || upd["comment_id"] == "" {
		t.Fatalf("update ack %v", upd)
	}
	if _, has := upd["body"]; has {
		t.Fatalf("ack must not carry body: %v", upd)
	}
	ch, _ := upd["changed"].([]any)
	if fmt.Sprint(ch) != "[title priority comment]" {
		t.Fatalf("changed %v", ch)
	}
	// optimistic version: ack returns the NEW version
	v := int(upd["version"].(float64))
	again := ok(t, h, "", "issue_update", map[string]any{"id": id, "status": "closed", "version": v})
	if int(again["version"].(float64)) <= v || again["status"] != "closed" || again["comment_id"] != nil {
		t.Fatalf("version/status %v", again)
	}
	fullUpd := ok(t, h, "", "issue_update", map[string]any{"id": id, "return": "full", "comment": "two"})
	if cs, _ := fullUpd["comments"].([]any); len(cs) != 2 || fullUpd["title"] != "new" {
		t.Fatalf("full update %v", fullUpd)
	}
	if _, has := fullUpd["comment_id"]; has {
		t.Fatalf("full must keep the old shape: %v", fullUpd)
	}

	other := ok(t, h, "", "issue_create", map[string]any{"title": "acc"})
	acc := ok(t, h, "", "issue_accept", map[string]any{"id": other["id"]})
	if acc["message"] == nil || acc["status"] == nil || acc["version"] == nil || acc["body"] != nil || acc["comments"] != nil {
		t.Fatalf("accept ack %v", acc)
	}
	other2 := ok(t, h, "", "issue_create", map[string]any{"title": "acc2"})
	accFull := ok(t, h, "", "issue_accept", map[string]any{"id": other2["id"], "return": "full"})
	if accFull["message"] == nil || accFull["comments"] == nil || accFull["title"] != "acc2" {
		t.Fatalf("accept full %v", accFull)
	}
	fails(t, h, "", "issue_update", map[string]any{"id": id, "return": "x"}, `invalid value "x" for argument "return": must be one of: ack, full`)
}

func commentTexts(t *testing.T, m map[string]any) []string {
	t.Helper()
	var out []string
	for _, c := range m["comments"].([]any) {
		out = append(out, c.(map[string]any)["text"].(string))
	}
	return out
}

func TestIssuesCommentPagination(t *testing.T) {
	h, _, _ := ackHandler(t)
	id := ok(t, h, "", "issue_create", map[string]any{"title": "t"})["id"].(string)
	for k := 1; k <= 8; k++ {
		ok(t, h, "", "issue_update", map[string]any{"id": id, "comment": fmt.Sprintf("c%d", k)})
	}

	// default: 5 newest, chronological order
	d := ok(t, h, "", "issues", map[string]any{"id": id})
	if got := commentTexts(t, d); fmt.Sprint(got) != "[c4 c5 c6 c7 c8]" {
		t.Fatalf("default %v", got)
	}
	if d["comments_total"] != float64(8) || d["comments_returned"] != float64(5) || d["comments_truncated"] != true {
		t.Fatalf("counts %v", d)
	}

	// page back with comments_before = id of the oldest returned
	oldest := d["comments"].([]any)[0].(map[string]any)["id"].(string)
	if oldest == "" {
		t.Fatal("comments in issues(id) need an id")
	}
	p := ok(t, h, "", "issues", map[string]any{"id": id, "comments_before": oldest, "comments_limit": 2})
	if got := commentTexts(t, p); fmt.Sprint(got) != "[c2 c3]" || p["comments_truncated"] != true || p["comments_total"] != float64(8) {
		t.Fatalf("page 2 %v %v", got, p)
	}
	first := p["comments"].([]any)[0].(map[string]any)["id"].(string)
	p = ok(t, h, "", "issues", map[string]any{"id": id, "comments_before": first})
	if got := commentTexts(t, p); fmt.Sprint(got) != "[c1]" || p["comments_truncated"] != false {
		t.Fatalf("page 3 %v", got)
	}

	// RFC3339 timestamp: far future = everything is older, far past = nothing
	p = ok(t, h, "", "issues", map[string]any{"id": id, "comments_before": "2999-01-01T00:00:00Z"})
	if got := commentTexts(t, p); fmt.Sprint(got) != "[c4 c5 c6 c7 c8]" {
		t.Fatalf("future %v", got)
	}
	p = ok(t, h, "", "issues", map[string]any{"id": id, "comments_before": "2000-01-01T00:00:00Z"})
	if p["comments_returned"] != float64(0) || p["comments_truncated"] != false {
		t.Fatalf("past %v", p)
	}
	fails(t, h, "", "issues", map[string]any{"id": id, "comments_before": "nope"},
		"comments_before must be a comment id or an RFC3339 timestamp (e.g. 2026-01-02T15:04:05Z)")

	// 0 / negative = no comments; large = all
	for _, n := range []float64{0, -3} {
		z := ok(t, h, "", "issues", map[string]any{"id": id, "comments_limit": n})
		if z["comments_returned"] != float64(0) || z["comments_total"] != float64(8) || z["comments_truncated"] != true || len(z["comments"].([]any)) != 0 {
			t.Fatalf("limit %v: %v", n, z)
		}
	}
	all := ok(t, h, "", "issues", map[string]any{"id": id, "comments_limit": 1000000})
	if all["comments_returned"] != float64(8) || all["comments_truncated"] != false {
		t.Fatalf("all %v", all)
	}
}

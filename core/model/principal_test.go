package model

import "testing"

// 規範化規則(PG 的 omixor_principal_node 與它一字不差,store 的 PG 測試會逐一比對這張表)
var principalSamples = []struct{ raw, node, kind string }{
	{"", "", ""},
	{"   ", "", ""},
	{"agent:dev", "agent:dev", "agent"},
	{"agent:", "local:agent:", "local"},
	{"user:A@B.com", "user:a@b.com", "user"},
	{"  User:x ", "local:User:x", "local"}, // 前綴區分大小寫;只去頭尾空白
	{"user: A@B.com ", "user:a@b.com", "user"},
	{"user:", "local:user:", "local"},
	{"user:omixor-console", "user:omixor-console", "user"},
	{"A@B.com", "user:a@b.com", "user"},
	{" a@b.com ", "user:a@b.com", "user"},
	{"local:foo", "local:foo", "local"},
	{"local:", "local:local:", "local"},
	{"omixor-console", "local:omixor-console", "local"},
	{"ops-web", "local:ops-web", "local"},
	{"mcp", "local:mcp", "local"},
	{"routine", "local:routine", "local"},
	{"chain", "local:chain", "local"},
	{"system", "local:system", "local"},
	{"某人", "local:某人", "local"},
}

func TestPrincipalNode(t *testing.T) {
	for _, c := range principalSamples {
		got := PrincipalNode(c.raw)
		if got != c.node {
			t.Errorf("PrincipalNode(%q) = %q,要 %q", c.raw, got, c.node)
		}
		if c.node != "" && PrincipalKind(got) != c.kind {
			t.Errorf("PrincipalKind(%q) = %q,要 %q", got, PrincipalKind(got), c.kind)
		}
		if c.node != "" && PrincipalNode(got) != got { // 冪等:規範化節點再規範化不變
			t.Errorf("PrincipalNode(%q) 不冪等:%q", got, PrincipalNode(got))
		}
	}
}

func TestAssigneeAndActorNode(t *testing.T) {
	for in, want := range map[string]string{"": "", " ": "", "dev": "agent:dev", " dev ": "agent:dev"} {
		if got := AssigneeNode(in); got != want {
			t.Errorf("AssigneeNode(%q) = %q,要 %q", in, got, want)
		}
	}
	for _, c := range []struct{ k, id, want string }{
		{"ai", "dev", "agent:dev"}, {"ai", "mcp", "local:mcp"}, {"ai", "", ""},
		{"person", "A@x.com", "user:a@x.com"}, {"person", "u-1", "local:u-1"}, {"person", "", ""},
		{"system", "", ""}, {"weird", "x", ""},
	} {
		if got := ActorNode(c.k, c.id); got != c.want {
			t.Errorf("ActorNode(%q,%q) = %q,要 %q", c.k, c.id, got, c.want)
		}
	}
}

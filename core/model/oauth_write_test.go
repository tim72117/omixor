package model

import (
	"slices"
	"sort"
	"testing"
)

// OAuth 寫入白名單的分類完整性:登記表裡每個 Admin 工具,不是唯讀(ReadOnly)、就是 OAuth 寫入白名單(OAuthWriteTools)、
// 就是明確列為禁止(OAuthForbiddenTools,附原因)。新增工具漏分類,這裡會失敗;三組互斥,名稱都要是登記的 Admin 工具。
func TestOAuthToolClassificationComplete(t *testing.T) {
	for _, ct := range CoreTools {
		if !ct.Admin {
			if OAuthWriteTools[ct.Name] || OAuthAdminTools[ct.Name] || OAuthForbiddenTools[ct.Name] != "" {
				t.Errorf("%s 不是 Admin 工具,不能出現在 OAuth 白名單或禁止清單", ct.Name)
			}
			continue
		}
		n := 0
		if ct.ReadOnly {
			n++
		}
		if OAuthWriteTools[ct.Name] {
			n++
		}
		if OAuthAdminTools[ct.Name] {
			n++
		}
		if OAuthForbiddenTools[ct.Name] != "" {
			n++
		}
		if n != 1 {
			t.Errorf("Admin 工具 %s 必須恰好分到 ReadOnly、OAuthWriteTools、OAuthAdminTools、OAuthForbiddenTools 其中一類(目前 %d 類)", ct.Name, n)
		}
	}
	for n := range OAuthWriteTools {
		if ct, ok := CoreToolByName(n); !ok || !ct.Admin {
			t.Errorf("OAuthWriteTools 的 %s 不是登記的 Admin 工具", n)
		}
	}
	for n := range OAuthAdminTools {
		if ct, ok := CoreToolByName(n); !ok || !ct.Admin {
			t.Errorf("OAuthAdminTools 的 %s 不是登記的 Admin 工具", n)
		}
	}
	for n, why := range OAuthForbiddenTools {
		if ct, ok := CoreToolByName(n); !ok || !ct.Admin || why == "" {
			t.Errorf("OAuthForbiddenTools 的 %s 不是登記的 Admin 工具或沒寫原因", n)
		}
	}
}

// 白名單固定在這裡:要加進 OAuth 寫入必須同時改這份清單;絕不能包含會簽發角色憑證、管理角色與組織結構、伺服器管理的工具
func TestOAuthWriteToolsPinned(t *testing.T) {
	want := []string{"agent_task_report", "channel_create", "channel_post", "doc_save", "issue_accept", "issue_create",
		"issue_decision_add", "issue_decision_answer", "issue_decision_to_issue", "issue_item_add", "issue_item_promote", "issue_item_report", "issue_item_update", "issue_update", "report_problem"}
	var got []string
	for n := range OAuthWriteTools {
		got = append(got, n)
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("OAuth 寫入白名單變了:\n得到 %v\n要   %v", got, want)
	}
	for _, n := range []string{"agent_task_create", "agent_task_claim", "issue_item_claim", "agent_upsert", "chain_save", "project_save", "cabinet_layout_save", "agent_task_transfer", "incident_declare", "incident_update"} {
		if OAuthWriteTools[n] {
			t.Errorf("%s 不能開放給 OAuth 寫入(mcp:write)", n)
		}
	}
}

// mcp:admin 的工具名單固定在這裡;絕不能含會簽發角色憑證、替角色排工作、寫 commit 的工具(它們維持禁止,附原因)
func TestOAuthAdminToolsPinned(t *testing.T) {
	want := []string{"agent_upsert", "cabinet_layout_save", "chain_save", "incident_declare", "incident_update", "project_save"}
	var got []string
	for n := range OAuthAdminTools {
		got = append(got, n)
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("OAuth 管理員工具名單變了:\n得到 %v\n要   %v", got, want)
	}
	for _, n := range []string{"agent_task_claim", "issue_item_claim", "agent_task_create", "agent_task_transfer", "commits_report", "issue_commit_link", "issue_commit_unlink", "agent_context", "channel_messages", "wireframe_render"} {
		if OAuthAdminTools[n] || OAuthForbiddenTools[n] == "" {
			t.Errorf("%s 必須維持禁止(不在 OAuthAdminTools、在 OAuthForbiddenTools)", n)
		}
	}
	for n := range OAuthAdminTools {
		if OAuthWriteTools[n] {
			t.Errorf("%s 同時在寫入與管理員名單", n)
		}
	}
}

// OAuth 寫入身分不是伺服器管理員;唯讀的 OAuth 身分與一般登入不受影響
func TestServerAdminForOAuthWrite(t *testing.T) {
	admin := User{ID: "a", Role: "admin"}
	if !(Principal{User: admin, Via: ViaLogin}).ServerAdmin() {
		t.Fatal("登入的伺服器管理員照舊")
	}
	if !(Principal{User: admin, Via: ViaOAuth, ReadOnly: true}).ServerAdmin() {
		t.Fatal("唯讀 OAuth 身分的行為不變")
	}
	w := Principal{User: admin, Via: ViaOAuth, ReadOnly: false}
	if w.ServerAdmin() || !w.OAuthWrite() {
		t.Fatal("OAuth 寫入身分不是伺服器管理員")
	}
	if (Principal{User: admin, Via: ViaLogin}).OAuthWrite() {
		t.Fatal("一般登入不是 OAuth 寫入")
	}
}

// mcp:admin 身分:隱含寫入、仍不是伺服器管理員;OAuthAdmin 旗標只在 OAuth 寫入身分上有意義
func TestServerAdminForOAuthAdminGrant(t *testing.T) {
	admin := User{ID: "a", Role: "admin"}
	p := Principal{User: admin, Via: ViaOAuth, OAuthAdmin: true}
	if p.ServerAdmin() || !p.OAuthWrite() || !p.OAuthAdminGrant() {
		t.Fatal("mcp:admin 身分:OAuthWrite、OAuthAdminGrant 為真,ServerAdmin 為假")
	}
	if (Principal{User: admin, Via: ViaOAuth, ReadOnly: true, OAuthAdmin: true}).OAuthAdminGrant() {
		t.Fatal("唯讀身分不能是管理員授權")
	}
	if (Principal{User: admin, Via: ViaLogin, OAuthAdmin: true}).OAuthAdminGrant() {
		t.Fatal("一般登入不是 OAuth 管理員授權")
	}
}

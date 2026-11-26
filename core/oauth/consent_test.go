package oauth

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// 回歸:同意頁的 grant 單選鈕在 <form> 之前渲染,瀏覽器不會送出 grant,導致所有授權變成唯讀。
// 修正是讓單選鈕用 form="<表單 id>" 歸屬到表單。
func TestConsentGrantRadiosBelongToForm(t *testing.T) {
	var buf bytes.Buffer
	err := consentTmpl.Execute(&buf, consentView{
		Title: "授權確認", ClientName: "c", RedirectHost: "example.com", Org: "o",
		Scope: "mcp:read mcp:write", RequestedScope: "mcp:read mcp:write", WriteRequested: true,
		CSRF: "x", ClientID: "cid", RedirectURI: "https://example.com/cb",
	})
	if err != nil {
		t.Fatal(err)
	}
	page := buf.String()

	formRe := regexp.MustCompile(`(?s)<form\b([^>]*)>(.*?)</form>`)
	fm := formRe.FindStringSubmatchIndex(page)
	if fm == nil {
		t.Fatal("no <form> found")
	}
	formAttrs := page[fm[2]:fm[3]]
	formStart, formEnd := fm[0], fm[1]
	if !regexp.MustCompile(`method="post"`).MatchString(formAttrs) || !regexp.MustCompile(`action="/oauth/authorize"`).MatchString(formAttrs) {
		t.Fatalf("unexpected form attrs: %s", formAttrs)
	}
	idm := regexp.MustCompile(`\bid="([^"]+)"`).FindStringSubmatch(formAttrs)

	inputs := regexp.MustCompile(`<input\b[^>]*\bname="grant"[^>]*>`).FindAllStringIndex(page, -1)
	if len(inputs) != 2 {
		t.Fatalf("want 2 grant radios, got %d", len(inputs))
	}
	for _, loc := range inputs {
		tag := page[loc[0]:loc[1]]
		inside := loc[0] > formStart && loc[1] < formEnd
		fa := regexp.MustCompile(`\bform="([^"]*)"`).FindStringSubmatch(tag)
		linked := idm != nil && fa != nil && fa[1] == idm[1]
		if !inside && !linked {
			t.Errorf("grant radio not associated with form: %s", tag)
		}
	}
	if !regexp.MustCompile(`value="read"[^>]*checked|checked[^>]*value="read"`).MatchString(page) {
		t.Error("read radio should stay checked by default")
	}
}

func renderConsentPage(t *testing.T, v consentView) string {
	t.Helper()
	var buf bytes.Buffer
	v.Title, v.ClientName, v.RedirectHost, v.Account = "授權確認", "c", "example.com", true
	v.CSRF, v.ClientID, v.RedirectURI = "x", "cid", "https://example.com/cb"
	if err := consentTmpl.Execute(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// mcp:admin:三個單選鈕(唯讀、讀寫、組織管理員)都用 form 屬性歸屬到表單;唯讀預設選取;管理員選項不預設選取
func TestConsentAdminRadio(t *testing.T) {
	page := renderConsentPage(t, consentView{Scope: "mcp:read", RequestedScope: "mcp:read mcp:write mcp:admin", WriteRequested: true, AdminRequested: true})
	inputs := regexp.MustCompile(`<input\b[^>]*\bname="grant"[^>]*>`).FindAllString(page, -1)
	if len(inputs) != 3 {
		t.Fatalf("want 3 grant radios, got %d", len(inputs))
	}
	want := []string{"read", "write", "admin"}
	for i, tag := range inputs {
		if !strings.Contains(tag, `form="consent"`) || !strings.Contains(tag, `value="`+want[i]+`"`) {
			t.Errorf("radio %d: %s", i, tag)
		}
		if strings.Contains(tag, "disabled") || (want[i] != "read") == strings.Contains(tag, "checked") {
			t.Errorf("radio %s: 只有 read 預設選取、都沒停用:%s", want[i], tag)
		}
	}
	for _, w := range []string{"組織管理員", "領取任務或簽發角色憑證", "伺服器管理員"} {
		if !strings.Contains(page, w) {
			t.Errorf("缺少說明 %q", w)
		}
	}
}

// 剛證明身分的使用者不是任何組織的管理員:管理員選項停用並說明,其他選項照舊
func TestConsentAdminDisabledForNonAdmin(t *testing.T) {
	page := renderConsentPage(t, consentView{Scope: "mcp:read", RequestedScope: "mcp:read mcp:write mcp:admin", WriteRequested: true, AdminRequested: true, AdminDenied: true})
	adm := regexp.MustCompile(`<input\b[^>]*value="admin"[^>]*>`).FindString(page)
	if adm == "" || !strings.Contains(adm, "disabled") || !strings.Contains(page, "你不是任何組織的管理員") {
		t.Fatalf("管理員選項要停用並說明:%s", adm)
	}
	// 沒要求 mcp:admin 就沒有這個選項
	page = renderConsentPage(t, consentView{Scope: "mcp:read", WriteRequested: true})
	if strings.Contains(page, `value="admin"`) {
		t.Error("沒要求 mcp:admin 不該出現管理員選項")
	}
}

// ISS-64:讀寫選項底下有靜態說明「讀寫只作用於你是成員的組織」;只讀的同意畫面沒有
func TestConsentWriteOptionMembershipHint(t *testing.T) {
	const hint = "讀寫只作用於你是成員的組織"
	if page := renderConsentPage(t, consentView{Scope: "mcp:read", RequestedScope: "mcp:read mcp:write", WriteRequested: true}); !strings.Contains(page, hint) {
		t.Errorf("讀寫選項缺少說明 %q", hint)
	}
	if page := renderConsentPage(t, consentView{Scope: "mcp:read", RequestedScope: "mcp:read"}); strings.Contains(page, hint) {
		t.Error("只讀的同意畫面不該有讀寫說明")
	}
}

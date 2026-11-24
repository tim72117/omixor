package oauth

import (
	"bytes"
	"regexp"
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

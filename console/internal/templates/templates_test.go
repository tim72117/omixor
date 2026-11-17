package templates

import (
	"os"
	"strings"
	"testing"
)

// 轉換器編進執行檔:沒開 OMIXOR_TEMPLATES 也要有(不然套範本的文件一片空白),而且要和 repo 的 templates/doc/docrender.js 一樣
func TestDocRenderJSEmbedded(t *testing.T) {
	t.Setenv("OMIXOR_TEMPLATES", "")
	if Enabled() || !strings.Contains(DocRenderJS(), "MdRender") {
		t.Fatal("沒開範本時也要有轉換器")
	}
	want, err := os.ReadFile("../../../templates/doc/docrender.js")
	if err != nil {
		t.Fatal(err)
	}
	if DocRenderJS() != string(want) {
		t.Fatal("console/internal/templates/docrender.js 和 templates/doc/docrender.js 不一樣:改了原檔要複製過來")
	}
}

package core

import (
	"bytes"
	"strings"
	"testing"
)

func TestAttachments(t *testing.T) {
	a := newTestApp(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 32)...)
	id, err := SaveAttachment(a, png)
	if err != nil || !attachmentIDRe.MatchString(id) || !strings.HasSuffix(id, ".png") {
		t.Fatalf("存檔:%q %v", id, err)
	}
	if id2, _ := SaveAttachment(a, png); id2 != id {
		t.Fatal("同一張圖要同一個 id")
	}
	data, mime, err := ReadAttachment(a, id)
	if err != nil || mime != "image/png" || !bytes.Equal(data, png) {
		t.Fatalf("讀檔:%v %s", err, mime)
	}
	for name, bad := range map[string][]byte{
		"svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html": []byte("<html><script>x</script></html>"),
		"空的":   nil,
		"太大":   append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxAttachment)...),
	} {
		if _, err := SaveAttachment(a, bad); err == nil {
			t.Errorf("%s 應該擋", name)
		}
	}
	for _, bad := range []string{"../orgs.json", "x.png", strings.Repeat("a", 32) + ".svg"} {
		if _, _, err := ReadAttachment(a, bad); err == nil {
			t.Errorf("%q 不該讀得到", bad)
		}
	}
	// 分組織:別的組織讀不到
	org := a.CreateOrg("另一個", "", "", "blank")
	a.Mu.Lock()
	a.SwitchOrg(org.ID)
	a.Mu.Unlock()
	if _, _, err := ReadAttachment(a, id); err == nil {
		t.Fatal("別的組織不該讀到這個附件")
	}
}

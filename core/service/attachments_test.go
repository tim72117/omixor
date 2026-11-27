package service_test

import (
	"bytes"
	"strings"
	"testing"

	"omixor-core/service"
)

// 附件的規則(server 與 console 共用):指定 id 的匯入同樣受大小、類型、成員身分限制,而且不覆蓋
func TestImportAttachment(t *testing.T) {
	e := newAuthzEnv(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 32)...)
	id := service.AttachmentID([]byte("jira:https://j:900"), "image/png")
	if !service.ValidAttachmentID(id) || !strings.HasSuffix(id, ".png") {
		t.Fatal(id)
	}
	for name, p := range map[string]error{
		"外部人":   e.svc.ImportAttachment(e.eve, "o1", id, png),
		"角色憑證":  e.svc.ImportAttachment(e.agent, "o1", id, png),
		"id 不對": e.svc.ImportAttachment(e.alice, "o1", "../x.png", png),
		"副檔名不符": e.svc.ImportAttachment(e.alice, "o1", strings.TrimSuffix(id, ".png")+".gif", png),
		"不是圖片":  e.svc.ImportAttachment(e.alice, "o1", id, []byte("<svg/>")),
		"太大":    e.svc.ImportAttachment(e.alice, "o1", id, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, service.MaxAttachment)...)),
	} {
		if p == nil {
			t.Errorf("%s 應該擋", name)
		}
	}
	if _, _, err := e.svc.GetAttachment(e.alice, "o1", id); err == nil {
		t.Fatal("被擋的匯入不該存進去")
	}
	if err := e.svc.ImportAttachment(e.alice, "o1", id, png); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ImportAttachment(e.alice, "o1", id, append(png, 2)); err != nil {
		t.Fatal(err)
	}
	if data, mime, err := e.svc.GetAttachment(e.alice, "o1", id); err != nil || mime != "image/png" || !bytes.Equal(data, png) {
		t.Fatalf("讀回(同 id 不覆蓋):%v %s", err, mime)
	}
	if _, _, err := e.svc.GetAttachment(e.zed, "o2", id); err == nil {
		t.Fatal("別的組織不該讀到")
	}
}

package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"omixor-core/model"
)

// 舊的 console 附件(omixor_files、資料夾)搬進核心:id 不變、冪等、舊資料保留,刪組織時一起清(需要 PostgreSQL)
func TestPGMigrateLegacyAttachments(t *testing.T) {
	t.Setenv("OMIXOR_CONSOLE_DATA_DIR", t.TempDir())
	withDB(t)
	l := localCore()
	if l == nil {
		t.Fatal("沒有本機核心")
	}
	if err := l.Svc.Store.CreateOrg(model.Org{ID: "o1", Name: "組織", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 32)...)
	const inDB, onDisk, orphan, bad = "0123456789abcdef0123456789abcdef.png", "11111111111111111111111111111111.png",
		"22222222222222222222222222222222.png", "33333333333333333333333333333333.png"
	row := func(org, id string, data []byte) {
		ctx, cancel := pgCtx()
		defer cancel()
		if _, err := pgExec(ctx, `insert into omixor_files (path, data) values ($1, $2)`, "orgs/"+org+"/attachments/"+id, data); err != nil {
			t.Fatal(err)
		}
	}
	rows := func() (n int) {
		ctx, cancel := pgCtx()
		defer cancel()
		if err := pgQueryRow(ctx, `select count(*) from omixor_files`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	row("o1", inDB, png)
	row("gone", orphan, png)
	row("o1", bad, []byte("<svg/>"))
	if err := writeFileAtomic(filepath.Join(legacyAttachmentDir("o1"), onDisk), png); err != nil {
		t.Fatal(err)
	}

	// 搬移前:核心沒有,console 仍從舊存放讀得到
	if _, _, err := l.GetAttachment("o1", inDB); err == nil {
		t.Fatal("搬移前核心不該有")
	}
	if _, mime, err := readOrgAttachment(nil, "o1", inDB); err != nil || mime != "image/png" {
		t.Fatalf("舊存放讀取:%v %s", err, mime)
	}

	// 建本機核心時自動搬
	localCoreMu.Lock()
	localCoreVal = nil
	localCoreMu.Unlock()
	l = localCore()
	for _, id := range []string{inDB, onDisk} {
		if data, mime, err := l.Svc.GetAttachment(l.P, "o1", id); err != nil || mime != "image/png" || !bytes.Equal(data, png) {
			t.Fatalf("%s 沒搬進核心:%v %s", id, err, mime)
		}
	}
	for org, id := range map[string]string{"gone": orphan, "o1": bad} {
		if _, err := l.Svc.Store.GetAttachment(org, id); err == nil {
			t.Fatalf("%s/%s 不該搬", org, id)
		}
	}
	if _, err := l.Svc.Store.GetOrg("gone"); err == nil {
		t.Fatal("搬移不該建立組織")
	}
	if n := migrateLegacyAttachments(l.Svc, l.P); n != 0 {
		t.Fatalf("重跑不該再複製:%d", n)
	}
	if rows() != 3 {
		t.Fatalf("舊資料要保留:%d", rows())
	}

	removeLegacyAttachments("o1")
	if _, err := os.Stat(legacyAttachmentDir("o1")); !os.IsNotExist(err) || rows() != 1 {
		t.Fatalf("刪組織要清掉這個組織的舊附件:%v,剩 %d 列", err, rows())
	}
}

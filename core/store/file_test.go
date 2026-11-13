package store

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"omixor-core/model"
)

func mt(s string) model.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return model.Time{Time: t}
}

// 寫入各種資料、重開,讀回來要一樣
func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := mt("2026-01-02T03:04:05Z")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.SaveUser(model.User{ID: "u1", Email: "a@x", Role: "admin", CreatedAt: at}))
	must(f.CreateToken(model.Token{ID: "t1", UserID: "u1", Device: "d", CreatedAt: at, Hash: []byte{1, 2, 3}}))
	must(f.CreateToken(model.Token{ID: "t2", UserID: "u1", Device: "d", CreatedAt: at, Hash: []byte{9}}))
	must(f.RevokeToken("t2", now))
	must(f.AddAudit(model.AuditEntry{At: at, Action: "x"}))
	must(f.CreateOrg(model.Org{ID: "o", Name: "O", CreatedAt: at}))
	must(f.SaveMember(model.Member{Org: "o", UserID: "u1", Role: "admin", AddedAt: at}))
	iss, err := f.CreateIssue("o", model.Issue{Title: "hi", CreatedAt: at, UpdatedAt: at, Labels: []string{"a"}, Comments: []model.Comment{}})
	must(err)
	must(f.SaveAgent("o", model.Agent{ID: "dev", Name: "Dev", CreatedAt: at}))
	task, err := f.CreateAgentTask("o", model.AgentTask{AgentID: "dev", Title: "t", CreatedAt: at})
	must(err)
	must(f.SaveAgentCredential(model.AgentCredential{Org: "o", AgentID: "dev", TaskID: task.ID, Hash: []byte{7, 7}, IssuedAt: at,
		ExpiresAt: model.Time{Time: now.Add(time.Hour).Truncate(time.Second)}}))
	must(f.SaveCabinetLayout("o", []model.CabinetSection{{ID: "s", Tabs: []model.CabinetTab{}}}))
	doc, err := f.CreateDoc("o", model.CabinetDoc{Title: "d", Section: "s", CreatedAt: at, UpdatedAt: at})
	must(err)
	must(f.SaveOrgChain("o", model.OrgChain{Edges: []model.ChainEdge{{From: "user:a@x", To: "agent:dev"}}}))
	ch, err := f.CreateChannel("o", model.Channel{Name: "c", Members: []string{"user:a@x"}, CreatedAt: at, UpdatedAt: at})
	must(err)
	msg, err := f.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Author: "user:a@x", Text: "hi", At: at})
	must(err)
	must(f.SaveChannelRead("o", model.ChannelRead{ChannelID: ch.ID, Member: "user:a@x", LastRead: 1}))
	must(f.PutAttachment("o", "abc", []byte("png")))

	g, err := OpenFile(dir)
	must(err)
	if u, err := g.UserByEmail("a@x"); err != nil || u.ID != "u1" {
		t.Fatal("使用者", u, err)
	}
	if tk, _, err := g.TokenByHash([]byte{1, 2, 3}, now); err != nil || tk.ID != "t1" {
		t.Fatal("token 要讀得回來", err)
	}
	if _, _, err := g.TokenByHash([]byte{9}, now); err == nil {
		t.Fatal("撤銷的 token 不能用")
	}
	if l, _ := g.ListAudit(10); len(l) != 1 || l[0].ID != 1 {
		t.Fatal("操作紀錄", l)
	}
	must(g.AddAudit(model.AuditEntry{At: at, Action: "y"}))
	if l, _ := g.ListAudit(10); len(l) != 2 || l[0].ID != 2 {
		t.Fatal("讀回後的操作紀錄 id 要接續", l)
	}
	if r, err := g.MemberRole("o", "u1"); err != nil || r != "admin" {
		t.Fatal("成員", r, err)
	}
	if i, err := g.GetIssue("o", iss.ID); err != nil || i.Title != "hi" || !i.CreatedAt.Equal(at.Time) || len(i.Labels) != 1 {
		t.Fatal("Issue", i, err)
	}
	if x, err := g.GetAgentTask("o", task.ID); err != nil || x.Title != "t" {
		t.Fatal("任務", x, err)
	}
	if c, err := g.AgentCredentialByHash([]byte{7, 7}); err != nil || c.TaskID != task.ID {
		t.Fatal("憑證", c, err)
	}
	if l, err := g.GetCabinetLayout("o"); err != nil || len(l) != 1 {
		t.Fatal("文件櫃結構", l, err)
	}
	if d, err := g.GetDoc("o", doc.ID); err != nil || d.Title != "d" {
		t.Fatal("文件", d, err)
	}
	if c, err := g.GetOrgChain("o"); err != nil || len(c.Edges) != 1 {
		t.Fatal("組織鏈", c, err)
	}
	if l, _, err := g.ListChannelMessages("o", ch.ID, model.MessageQuery{}); err != nil || len(l) != 1 || l[0].ID != msg.ID {
		t.Fatal("訊息", l, err)
	}
	if r, _ := g.ChannelReads("o", "user:a@x"); r[ch.ID].LastRead != 1 {
		t.Fatal("已讀", r)
	}
	if b, err := g.GetAttachment("o", "abc"); err != nil || !bytes.Equal(b, []byte("png")) {
		t.Fatal("附件", err)
	}
	// 編號要接續
	if i2, _ := g.CreateIssue("o", model.Issue{Title: "2"}); i2.ID != "ISS-2" {
		t.Fatal("Issue 編號", i2.ID)
	}
	// 刪除也要存
	must(g.DeleteIssue("o", iss.ID))
	h, err := OpenFile(dir)
	must(err)
	if _, err := h.GetIssue("o", iss.ID); err != ErrNotFound {
		t.Fatal("刪掉的要不見")
	}
	// 只有一個主檔,沒有留下暫存檔
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != "store.json" && e.Name() != "attachments" {
			t.Fatal("多出檔案", e.Name())
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, "store.json")); st.Mode().Perm() != 0o600 {
		t.Fatal("權限要 600", st.Mode())
	}
}

// 附件的組織與 id 不能跳出資料夾
func TestFileStoreAttachmentPath(t *testing.T) {
	f, _ := OpenFile(t.TempDir())
	if err := f.PutAttachment("o", "../x", []byte("a")); err == nil {
		t.Fatal("要拒絕")
	}
	if _, err := f.GetAttachment("..", "x"); err != ErrNotFound {
		t.Fatal("要找不到")
	}
}

// 壞掉的檔案要回錯誤,不能當成空的(避免下一次寫入蓋掉)
func TestFileStoreCorrupt(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte("{"), 0o600)
	if _, err := OpenFile(dir); err == nil {
		t.Fatal("要回錯誤")
	}
}

// Mem 多了欄位(新的資料)時,File 的快照也要一起存:這裡列的是 snapshot/restore 已經處理的欄位
func TestFileStoreCoversMem(t *testing.T) {
	handled := map[string]bool{"mu": true, "users": true, "tokens": true, "audit": true, "orgs": true, "members": true,
		"issues": true, "attach": true, "agents": true, "tasks": true, "creds": true, "layouts": true, "docs": true,
		"chains": true, "channels": true, "messages": true, "reads": true, "incidents": true, "integrations": true, "system": true, "mcp": true}
	tp := reflect.TypeOf(Mem{})
	for i := 0; i < tp.NumField(); i++ {
		if n := tp.Field(i).Name; !handled[n] {
			t.Errorf("Mem 的欄位 %s 沒有存進 store.json:請在 file.go 的 fileSnapshot、snapshot、restore 補上", n)
		}
	}
}

// 批次:成功時整份只存一次;失敗時記憶體與磁碟都回到批次開始前;進行中其他寫入要等批次結束
func TestFileBatch(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.CreateOrg(model.Org{ID: "o", Name: "o"}); err != nil {
		t.Fatal(err)
	}
	ch, err := f.CreateChannel("o", model.Channel{Name: "a", Members: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	// 失敗:寫了 10 則訊息後回錯誤 → 全部不算
	err = f.Batch(func(tx *File) error {
		for i := 0; i < 10; i++ {
			if _, err := tx.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Text: "x", Mentions: []string{}}); err != nil {
				return err
			}
		}
		return os.ErrInvalid
	})
	if err != os.ErrInvalid {
		t.Fatalf("要回 fn 的錯誤:%v", err)
	}
	if ms, _, _ := f.ListChannelMessages("o", ch.ID, model.MessageQuery{Limit: 1000}); len(ms) != 0 {
		t.Fatalf("失敗的批次要還原記憶體,還有 %d 則", len(ms))
	}
	// 成功:100 則,存檔後重開要讀得回來
	err = f.Batch(func(tx *File) error {
		for i := 0; i < 100; i++ {
			if _, err := tx.CreateChannelMessage("o", model.ChannelMessage{ChannelID: ch.ID, Text: "y", Mentions: []string{}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	g, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ms, _, _ := g.ListChannelMessages("o", ch.ID, model.MessageQuery{Limit: 1000}); len(ms) != 100 {
		t.Fatalf("重開後要有 100 則,有 %d", len(ms))
	}
	// 批次進行中,批次以外的寫入要等批次結束
	started, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = f.Batch(func(tx *File) error {
			close(started)
			<-release
			return os.ErrInvalid
		})
	}()
	<-started
	wrote := make(chan struct{})
	go func() { _ = f.CreateOrg(model.Org{ID: "p", Name: "p"}); close(wrote) }()
	select {
	case <-wrote:
		t.Fatal("批次進行中,別的寫入不能先做")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-wrote
	if _, err := f.GetOrg("p"); err != nil {
		t.Fatal("批次失敗還原後,等待中的寫入要照常做完")
	}
}

// 打開時清掉上次寫到一半留下的暫存檔
func TestFileCleansTemps(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, ".store.json.tmp-123")
	_ = os.WriteFile(tmp, []byte("{"), 0o600)
	if _, err := OpenFile(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("暫存檔要清掉")
	}
}

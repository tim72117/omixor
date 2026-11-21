package store

// 外鍵與刪除規則、角色任務轉移的一致性測試:只跑 PG(PG 另有只有資料庫才有的測試,見 pg_fk_test.go)

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"omixor-core/model"
)

func TestStoreFK(t *testing.T) {
	suites := []struct {
		name string
		fn   func(t *testing.T, impl storeImpl)
	}{
		{"RefsMustExist", suiteFKRefsMustExist},
		{"AgentRestrict", suiteFKAgentRestrict},
		{"IssueSetNull", suiteFKIssueSetNull},
		{"TransferAgentTasks", suiteTransferAgentTasks},
		{"TransferEdgeCases", suiteTransferEdgeCases},
		{"AtomicRollback", suiteAtomicRollback},
	}
	for _, impl := range storeImpls() {
		t.Run(impl.name, func(t *testing.T) {
			for _, c := range suites {
				t.Run(c.name, func(t *testing.T) { c.fn(t, impl) })
			}
		})
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s:要 %v,拿到 %v", what, want, err)
	}
}

// 寫入的資料指向不存在的對象:ErrBadRef(” 專案、沒綁 Issue 不檢查)
func suiteFKRefsMustExist(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	at := mt("2026-01-02T03:04:05Z")
	// 任務的角色
	_, err := s.CreateAgentTask("o", model.AgentTask{AgentID: "ghost", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at})
	wantErr(t, "任務的角色不存在", err, ErrBadRef)
	makeTasks(t, s, "o", 1)
	tk, _ := s.GetAgentTask("o", "T-1")
	tk.AgentID = "ghost"
	wantErr(t, "改任務的角色成不存在的", s.UpdateAgentTask("o", *tk), ErrBadRef)
	if g, _ := s.GetAgentTask("o", "T-1"); g.AgentID != "dev" {
		t.Fatal("失敗不能改到", g.AgentID)
	}
	// 憑證的任務
	wantErr(t, "憑證的任務不存在", s.SaveAgentCredential(model.AgentCredential{Org: "o", AgentID: "dev", TaskID: "T-9", Hash: []byte{1}, IssuedAt: at, ExpiresAt: at}), ErrBadRef)
	chk(t, s.SaveAgentCredential(model.AgentCredential{Org: "o", AgentID: "dev", TaskID: "T-1", Hash: []byte{1}, IssuedAt: at, ExpiresAt: at}))
	// 文件的專案:'' 是未分專案
	_, err = s.CreateDoc("o", model.CabinetDoc{Project: "PRJ-9", Title: "x", Section: "s", Tags: []string{}, CreatedAt: at, UpdatedAt: at})
	wantErr(t, "文件的專案不存在", err, ErrBadRef)
	d, err := s.CreateDoc("o", model.CabinetDoc{Title: "x", Section: "s", Tags: []string{}, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	d.Project = "PRJ-9"
	wantErr(t, "把文件搬到不存在的專案", s.UpdateDoc("o", d), ErrBadRef)
	d.Project = makeProject(t, s, "o")
	chk(t, s.UpdateDoc("o", d))
	// 頻道的 Issue:空字串是沒綁
	_, err = s.CreateChannel("o", model.Channel{Name: "c", IssueID: "ISS-9", CreatedAt: at, UpdatedAt: at})
	wantErr(t, "頻道的 Issue 不存在", err, ErrBadRef)
	ch, err := s.CreateChannel("o", model.Channel{Name: "c", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	ch.IssueID = "ISS-9"
	wantErr(t, "頻道改綁不存在的 Issue", s.UpdateChannel("o", ch), ErrBadRef)
	// 區塊的專案:空結構沒有區塊列,不檢查;有區塊要專案存在
	_, err = s.SaveCabinetLayout("o", "PRJ-9", []model.CabinetSection{{ID: "a", Label: "a", Tabs: []model.CabinetTab{}}}, nil)
	wantErr(t, "區塊的專案不存在", err, ErrBadRef)
	// 匯入:任務的角色要在匯入的角色裡;文件的專案要存在
	newOrg(t, s, "q")
	wantErr(t, "匯入的任務指向沒有的角色", s.ImportAgents("q", []model.Agent{{ID: "x", Name: "X", CreatedAt: at}},
		[]model.AgentTask{{ID: "T-1", AgentID: "y", Title: "t", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at}}), ErrBadRef)
	if l, _ := s.ListAgents("q"); len(l) != 0 {
		t.Fatal("匯入失敗不能留下角色", l)
	}
	_, err = s.ImportDocs("q", []model.CabinetDoc{{ID: "DOC-1", Project: "PRJ-1", Title: "x", Section: "s", Tags: []string{}, CreatedAt: at, UpdatedAt: at}})
	wantErr(t, "匯入的文件指向沒有的專案", err, ErrBadRef)
}

// 名下還有任務(任何狀態)的角色不能刪;沒任務的可以
func suiteFKAgentRestrict(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	makeTasks(t, s, "o", 1)
	wantErr(t, "名下有排隊的任務", s.DeleteAgent("o", "dev"), ErrInUse)
	tk, _ := s.GetAgentTask("o", "T-1")
	for _, st := range []string{"inProgress", "blocked", "done", "canceled"} {
		tk.Status = st
		chk(t, s.UpdateAgentTask("o", *tk))
		wantErr(t, "任何狀態的任務都擋("+st+")", s.DeleteAgent("o", "dev"), ErrInUse)
	}
	if _, err := s.GetAgent("o", "dev"); err != nil {
		t.Fatal("被擋下的角色還在", err)
	}
	chk(t, s.DeleteAgent("o", "ops")) // 沒任務的
	wantNotFound(t, "再刪一次", s.DeleteAgent("o", "ops"))
	// 別的組織同 id 的任務不擋
	newOrg(t, s, "p")
	makeAgent(t, s, "p", "dev")
	chk(t, s.DeleteAgent("p", "dev"))
}

// 刪 Issue:綁它的頻道變成沒綁(on delete set null),頻道本身與訊息都在
func suiteFKIssueSetNull(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	iss := newTestIssue(t, s, "o")
	at := mt("2026-01-02T03:04:05Z")
	other, err := s.CreateIssue("o", model.Issue{Title: "u", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	c1, err := s.CreateChannel("o", model.Channel{Name: "綁 1", IssueID: iss.ID, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	c2, err := s.CreateChannel("o", model.Channel{Name: "綁 2", IssueID: other.ID, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	_, err = s.CreateChannelMessage("o", model.ChannelMessage{ChannelID: c1.ID, Author: lid(t, s, "o", "user:a"), Text: "hi", At: at})
	chk(t, err)
	chk(t, s.DeleteIssue("o", iss.ID))
	g, err := s.GetChannel("o", c1.ID)
	chk(t, err)
	if g.IssueID != "" || g.Name != "綁 1" {
		t.Fatal("刪 Issue 後頻道要沒綁", g)
	}
	if l, _, _ := s.ListChannelMessages("o", c1.ID, model.MessageQuery{}); len(l) != 1 {
		t.Fatal("訊息不能掉", l)
	}
	if g, _ := s.GetChannel("o", c2.ID); g.IssueID != other.ID {
		t.Fatal("別的頻道不受影響", g)
	}
	// 多個沒綁的頻道並存(唯一索引不把空值算重複)
	_, err = s.CreateChannel("o", model.Channel{Name: "沒綁", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if l, _ := s.ListChannels("o"); len(l) != 3 {
		t.Fatal("頻道數", len(l))
	}
}

func transferFixture(t *testing.T, s Store) (at model.Time) {
	t.Helper()
	newOrg(t, s, "o")
	makeAgent(t, s, "o", "dev")
	makeAgent(t, s, "o", "ops")
	at = mt("2026-01-02T03:04:05Z")
	until := mt("2026-01-02T05:04:05Z")
	mk := func(status string) {
		x := model.AgentTask{AgentID: "dev", Title: status, Priority: "normal", Status: status, CreatedAt: at, UpdatedAt: at, Links: []string{},
			Notes: []model.AgentTaskNote{{ID: "n0", Author: whoID(t, s, "o", "a@x"), Text: "舊備註", At: at}}}
		switch status {
		case "inProgress":
			x.ClaimedAt, x.LockedUntil, x.ClaimedBy = &at, &until, whoID(t, s, "o", "a@x")
		case "done", "canceled":
			x.CompletedAt = &at
		}
		_, err := s.CreateAgentTask("o", x)
		chk(t, err)
	}
	for _, st := range []string{"queued", "inProgress", "blocked", "done", "canceled"} { // T-1..T-5
		mk(st)
	}
	_, err := s.CreateAgentTask("o", model.AgentTask{AgentID: "ops", Title: "別人的", Priority: "normal", Status: "queued", CreatedAt: at, UpdatedAt: at}) // T-6
	chk(t, err)
	chk(t, s.SaveAgentCredential(model.AgentCredential{Org: "o", AgentID: "dev", TaskID: "T-2", Hash: []byte{2}, IssuedAt: at, ExpiresAt: mt("2099-01-01T00:00:00Z")}))
	return at
}

func suiteTransferAgentTasks(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	transferFixture(t, s)
	now := mt("2026-03-04T05:06:07Z")
	// 只轉 queued、blocked
	r, err := s.TransferAgentTasks("o", AgentTransfer{From: "dev", To: "ops", OpenOnly: true, At: now.Time, NoteAuthor: "sys_omixor", NoteText: "由 Dev 轉移"})
	chk(t, err)
	sameJSON(t, "open 範圍的結果", r, AgentTransferResult{ByStatus: map[string]int{"queued": 1, "blocked": 1}, Total: 2})
	want := map[string]string{"T-1": "ops", "T-2": "dev", "T-3": "ops", "T-4": "dev", "T-5": "dev", "T-6": "ops"}
	for id, agent := range want {
		g, err := s.GetAgentTask("o", id)
		chk(t, err)
		if g.AgentID != agent {
			t.Fatalf("%s 的角色 %s,要 %s", id, g.AgentID, agent)
		}
	}
	if g, _ := s.GetAgentTask("o", "T-2"); g.Status != "inProgress" || g.LockedUntil == nil || len(g.Notes) != 1 {
		t.Fatal("進行中的不動", g)
	}
	if _, err := s.AgentCredentialForTask("o", "T-2"); err != nil {
		t.Fatal("進行中的憑證不動", err)
	}
	g1, _ := s.GetAgentTask("o", "T-1")
	if len(g1.Notes) != 2 || g1.Notes[1].Text != "由 Dev 轉移" || g1.Notes[1].Author != "sys_omixor" || g1.Notes[1].ID == "" ||
		!g1.Notes[1].At.Equal(now.Time) || !g1.UpdatedAt.Equal(now.Time) || g1.Status != "queued" {
		t.Fatal("轉移的備註與狀態", g1)
	}
	if g3, _ := s.GetAgentTask("o", "T-3"); g3.Status != "blocked" {
		t.Fatal("blocked 保持狀態", g3.Status)
	}
	// 全部轉:進行中退回待領取(鎖、領取者清掉、憑證作廢),done、canceled 照樣轉、狀態與完成時間不變
	r, err = s.TransferAgentTasks("o", AgentTransfer{From: "dev", To: "ops", At: now.Time, NoteAuthor: "sys_omixor", NoteText: "由 Dev 轉移"})
	chk(t, err)
	sameJSON(t, "all 範圍的結果", r, AgentTransferResult{ByStatus: map[string]int{"inProgress": 1, "done": 1, "canceled": 1}, Total: 3, Released: 1})
	g2, _ := s.GetAgentTask("o", "T-2")
	if g2.AgentID != "ops" || g2.Status != "queued" || g2.LockedUntil != nil || g2.ClaimedBy != "" || len(g2.Notes) != 2 || g2.CompletedAt != nil {
		t.Fatal("進行中的要退回待領取", g2)
	}
	_, err = s.AgentCredentialForTask("o", "T-2")
	wantNotFound(t, "退回的任務憑證作廢", err)
	_, err = s.AgentCredentialByHash([]byte{2})
	wantNotFound(t, "退回的任務憑證作廢(雜湊)", err)
	for _, id := range []string{"T-4", "T-5"} {
		g, _ := s.GetAgentTask("o", id)
		if g.AgentID != "ops" || g.CompletedAt == nil || len(g.Notes) != 2 {
			t.Fatal("已結束的也轉,備註照加", g)
		}
	}
	if g, _ := s.GetAgentTask("o", "T-4"); g.Status != "done" {
		t.Fatal(g.Status)
	}
	if g, _ := s.GetAgentTask("o", "T-5"); g.Status != "canceled" {
		t.Fatal(g.Status)
	}
	if g6, _ := s.GetAgentTask("o", "T-6"); len(g6.Notes) != 0 {
		t.Fatal("原本就是目標角色的不加備註", g6.Notes)
	}
	// dev 名下沒有任務了:可以刪
	chk(t, s.DeleteAgent("o", "dev"))
	// 沒有任務可轉:零筆
	makeAgent(t, s, "o", "empty")
	r, err = s.TransferAgentTasks("o", AgentTransfer{From: "empty", To: "ops", At: now.Time})
	chk(t, err)
	sameJSON(t, "沒任務", r, AgentTransferResult{ByStatus: map[string]int{}})
}

func suiteTransferEdgeCases(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	transferFixture(t, s)
	now := mt("2026-03-04T05:06:07Z").Time
	if _, err := s.TransferAgentTasks("o", AgentTransfer{From: "dev", To: "dev", At: now}); err == nil {
		t.Fatal("不能轉給自己")
	}
	// 目標角色不存在:外鍵擋下,整個轉移還原(不會留下半套)
	_, err := s.TransferAgentTasks("o", AgentTransfer{From: "dev", To: "ghost", At: now, NoteText: "x"})
	wantErr(t, "目標角色不存在", err, ErrBadRef)
	l, _ := s.ListAgentTasks("o")
	for _, x := range l {
		if x.ID != "T-6" && x.AgentID != "dev" {
			t.Fatal("失敗的轉移不能留下半套", x.ID, x.AgentID)
		}
		if len(x.Notes) > 1 {
			t.Fatal("失敗的轉移不能留下備註", x.ID, x.Notes)
		}
	}
	if g, _ := s.GetAgentTask("o", "T-2"); g.Status != "inProgress" {
		t.Fatal("失敗不能動進行中的", g.Status)
	}
	if _, err := s.AgentCredentialForTask("o", "T-2"); err != nil {
		t.Fatal("失敗不能作廢憑證", err)
	}
}

// Atomic:fn 回錯誤就全部還原(角色、任務、憑證、Issue、組織鏈、頻道都在快照裡)
func suiteAtomicRollback(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	at := transferFixture(t, s)
	ua, dev := lid(t, s, "o", "user:a"), lid(t, s, "o", "agent:dev")
	chk(t, s.SaveOrgChain("o", model.OrgChain{Edges: []model.ChainEdge{{From: ua, To: dev}}}))
	ch, err := s.CreateChannel("o", model.Channel{Name: "c", Members: []string{dev, ua}, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	iss, err := s.CreateIssue("o", model.Issue{Title: "i", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", Assignee: dev, CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	boom := errors.New("boom")
	err = s.Atomic(func(tx Store) error {
		if _, err := tx.TransferAgentTasks("o", AgentTransfer{From: "dev", To: "ops", At: at.Time, NoteAuthor: "sys_omixor", NoteText: "x"}); err != nil {
			return err
		}
		chk(t, tx.SaveAgent("o", model.Agent{ID: "new", Name: "N", CreatedAt: at}))
		chk(t, tx.SaveOrgChain("o", model.OrgChain{Edges: []model.ChainEdge{}}))
		c := ch
		c.Members = []string{ua}
		chk(t, tx.UpdateChannel("o", c))
		i := iss
		i.Assignee = ""
		chk(t, tx.UpdateIssue("o", i))
		chk(t, tx.DeleteAgentCredentials("o", "", "dev"))
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal("Atomic 要回傳 fn 的錯誤", err)
	}
	l, _ := s.ListAgentTasks("o")
	for _, x := range l {
		if x.ID != "T-6" && x.AgentID != "dev" {
			t.Fatal("還原失敗:任務", x.ID, x.AgentID)
		}
		if x.ID == "T-1" && len(x.Notes) != 1 {
			t.Fatal("還原失敗:備註", x.Notes)
		}
	}
	if _, err := s.GetAgent("o", "new"); !errors.Is(err, ErrNotFound) {
		t.Fatal("還原失敗:新角色", err)
	}
	if c, _ := s.GetOrgChain("o"); len(c.Edges) != 1 {
		t.Fatal("還原失敗:組織鏈", c)
	}
	if g, _ := s.GetChannel("o", ch.ID); len(g.Members) != 2 {
		t.Fatal("還原失敗:頻道成員", g.Members)
	}
	if g, _ := s.GetIssue("o", iss.ID); g.Assignee != dev {
		t.Fatal("還原失敗:Issue 指派", g.Assignee)
	}
	if g, err := s.AgentCredentialForTask("o", "T-2"); err != nil || !bytes.Equal(g.Hash, []byte{2}) {
		t.Fatal("還原失敗:憑證", g, err)
	}
	// 成功就都留下;巢狀的 Atomic 沿用外層
	err = s.Atomic(func(tx Store) error {
		return tx.Atomic(func(in Store) error {
			_, err := in.TransferAgentTasks("o", AgentTransfer{From: "dev", To: "ops", OpenOnly: true, At: time.Now()})
			return err
		})
	})
	chk(t, err)
	if g, _ := s.GetAgentTask("o", "T-1"); g.AgentID != "ops" {
		t.Fatal("成功的 Atomic 要留下", g.AgentID)
	}
}

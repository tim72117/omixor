package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 組織鏈測試用:組織 o1,管理員 boss、成員 alice、bob;角色 cto(研發長)、pm(產品經理)、designer(設計師)
func chainEnv(t *testing.T) (*service.Service, *store.Mem, *model.Principal, *model.Principal, *model.Principal) {
	t.Helper()
	st := store.NewMem()
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.CreateOrg(model.Org{ID: "o1", Name: "組織一", CreatedAt: now})
	for i, u := range []struct{ id, email, role string }{{"u1", "boss@x", "admin"}, {"u2", "alice@x", "member"}, {"u3", "bob@x", "member"}} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		_ = st.SaveMember(model.Member{Org: "o1", UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
		_ = i
	}
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	alice := &model.Principal{User: model.User{ID: "u2", Email: "Alice@X", Role: "member"}}
	bob := &model.Principal{User: model.User{ID: "u3", Email: "bob@x", Role: "member"}}
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長"}, {ID: "pm", Name: "產品經理"}, {ID: "designer", Name: "設計師"}} {
		g.Active = true
		if _, err := svc.SaveAgent(boss, "o1", g); err != nil {
			t.Fatal(err)
		}
	}
	return svc, st, boss, alice, bob
}

func edges(list ...string) []model.ChainEdge {
	out := []model.ChainEdge{}
	for _, s := range list { // "from>to>label"
		p := strings.Split(s, ">")
		e := model.ChainEdge{From: p[0], To: p[1]}
		if len(p) > 2 {
			e.Label = p[2]
		}
		out = append(out, e)
	}
	return out
}

func TestChainValidate(t *testing.T) {
	svc, _, boss, alice, _ := chainEnv(t)
	c, err := svc.GetChain(alice, "o1")
	if err != nil || len(c.Edges) != 0 {
		t.Fatalf("沒存過是空的:%+v %v", c, err)
	}
	// 成員不能改
	if _, err := svc.SaveChain(alice, "o1", edges("agent:cto>agent:pm")); code(err) != 403 {
		t.Fatal("成員不能改組織鏈", err)
	}
	bad := map[string][]model.ChainEdge{
		"指向自己":   edges("agent:cto>agent:cto"),
		"找不到角色":  edges("agent:cto>agent:nope"),
		"不是組織成員": edges("user:stranger@x>agent:cto"),
		"重複了":    edges("agent:cto>agent:pm", "agent:CTO>agent:pm>任務"),
		"循環":     edges("agent:cto>agent:pm", "agent:pm>agent:designer", "agent:designer>agent:cto"),
		"要寫成":    edges("cto>agent:pm"),
	}
	for want, e := range bad {
		if _, err := svc.SaveChain(boss, "o1", e); code(err) != 400 || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s:%v", want, err)
		}
	}
	if _, err := svc.SaveChain(boss, "o1", edges("agent:cto>agent:pm", "agent:pm>agent:designer", "agent:designer>agent:cto")); !strings.Contains(err.Error(), "研發長 → 產品經理 → 設計師 → 研發長") {
		t.Fatalf("循環要說明路徑:%v", err)
	}
	// 正常:名稱、大小寫都換成存起來的寫法
	saved, err := svc.SaveChain(boss, "o1", edges("user:BOSS@x>agent:研發長>目標", "agent:CTO>agent:pm> 任務 ", "agent:cto>agent:designer"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Edges[0].From != "user:boss@x" || saved.Edges[0].To != "agent:cto" || saved.Edges[1].From != "agent:cto" || saved.Edges[1].Label != "任務" || saved.UpdatedAt == nil {
		t.Fatalf("%+v", saved)
	}
	if c, _ := svc.GetChain(alice, "o1"); len(c.Edges) != 3 {
		t.Fatal(c)
	}
	// 刪角色:相關的邊一起拿掉
	if err := svc.DeleteAgent(boss, "o1", "designer"); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.GetChain(alice, "o1"); len(c.Edges) != 2 {
		t.Fatalf("刪角色後的邊:%+v", c.Edges)
	}
}

func TestChainMemberAssign(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("user:alice@x>agent:cto>目標")); err != nil {
		t.Fatal(err)
	}
	// 管理員照舊可以派給任何角色,說明不加前綴
	if tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "pm", Title: "x", Instructions: "做"}); err != nil || tk.Instructions != "做" {
		t.Fatal(tk, err)
	}
	// 成員:鏈上有邊才可以,label 寫進說明
	tk, err := svc.AssignTask(alice, "o1", service.TaskInput{AgentID: "cto", Title: "Q4 目標", Instructions: "營收翻倍"})
	if err != nil || tk.CreatedBy != "Alice@X" || tk.Instructions != "【目標】\n\n營收翻倍" {
		t.Fatalf("%+v %v", tk, err)
	}
	if _, err := svc.AssignTask(alice, "o1", service.TaskInput{AgentID: "pm", Title: "x"}); code(err) != 403 || !strings.Contains(err.Error(), "可以派給:研發長(目標)") {
		t.Fatal("鏈上沒有的邊要 403 並說明可以派給誰", err)
	}
	if _, err := svc.AssignTask(bob, "o1", service.TaskInput{AgentID: "cto", Title: "x"}); code(err) != 403 || !strings.Contains(err.Error(), "沒有") {
		t.Fatal("沒有邊的成員", err)
	}
	// 移除成員:相關的邊拿掉
	if err := svc.RemoveMember(boss, "o1", "alice@x"); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.GetChain(boss, "o1"); len(c.Edges) != 0 {
		t.Fatalf("移除成員後的邊:%+v", c.Edges)
	}
}

// claim 管理員派一筆給角色並領取,回傳角色身分
func claimAs(t *testing.T, svc *service.Service, boss *model.Principal, agent, title string) (*model.AgentTask, *model.Principal) {
	t.Helper()
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: agent, Title: title, Links: []string{"ISS-9"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: agent, TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.AuthenticateAgent(agent, res.Credential.Secret, "ip")
	if err != nil {
		t.Fatal(err)
	}
	return res.Task, p
}

func TestChainDelegateAndReport(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("user:alice@x>agent:cto>目標", "agent:cto>agent:pm", "agent:cto>agent:designer>設計")); err != nil {
		t.Fatal(err)
	}
	src, cto := claimAs(t, svc, boss, "cto", "規劃 Q4")
	// 只有角色身分可以委派
	if _, err := svc.DelegateTask(alice, "o1", service.DelegateInput{To: "pm", Title: "x"}); code(err) != 403 {
		t.Fatal("人不能委派", err)
	}
	d, err := svc.DelegateTask(cto, "o1", service.DelegateInput{To: "設計師", Title: "畫圖", Instructions: "首頁"})
	if err != nil {
		t.Fatal(err)
	}
	if d.CreatedBy != "agent:cto" || d.AgentID != "designer" || strings.Join(d.Links, ",") != src.ID+",ISS-9" || d.Instructions != "【設計】\n\n首頁" {
		t.Fatalf("%+v", d)
	}
	// 鏈上沒有的邊
	_, designer := claimAs(t, svc, boss, "designer", "別的")
	if _, err := svc.DelegateTask(designer, "o1", service.DelegateInput{To: "pm", Title: "x"}); code(err) != 403 {
		t.Fatal("設計師不能派給產品經理", err)
	}
	if _, err := svc.DelegateTask(cto, "o1", service.DelegateInput{To: "cto", Title: "x"}); code(err) != 400 {
		t.Fatal("不能委派給自己", err)
	}

	// 設計師做完 → 替研發長排「檢視回報」
	res, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "designer", TaskID: d.ID})
	if err != nil {
		t.Fatal(err)
	}
	dp, _ := svc.AuthenticateAgent("designer", res.Credential.Secret, "ip")
	done := "done"
	if _, err := svc.ReportTask(dp, "o1", "", service.TaskReport{Status: &done, Result: sp("首頁畫好了")}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListAgentTasks("o1")
	var review *model.AgentTask
	for i := range all {
		if all[i].CreatedBy == model.ChainAuthor {
			review = &all[i]
		}
	}
	if review == nil || review.AgentID != "cto" || review.Title != "檢視回報:畫圖" || review.Status != "queued" ||
		review.Links[0] != d.ID || !strings.Contains(review.Instructions, "首頁畫好了") || !strings.Contains(review.Instructions, d.ID) {
		t.Fatalf("檢視回報任務:%+v", review)
	}
	if got, _ := st.GetAgentTask("o1", d.ID); !strings.Contains(got.Notes[len(got.Notes)-1].Text, "已回報給 研發長("+review.ID+")") {
		t.Fatalf("原任務的備註:%+v", got.Notes)
	}
	// 檢視回報任務做完不會再連鎖
	r2, err := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: review.ID})
	if err != nil {
		t.Fatal(err)
	}
	rp, _ := svc.AuthenticateAgent("cto", r2.Credential.Secret, "ip")
	if _, err := svc.ReportTask(rp, "o1", "", service.TaskReport{Status: &done}); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.ListAgentTasks("o1"); len(after) != len(all) {
		t.Fatalf("不能連鎖:%d → %d", len(all), len(after))
	}

	// 真人派的:blocked 回報 → 不建任務,原任務留備註,可以查「回報給我的任務」
	ta, err := svc.AssignTask(alice, "o1", service.TaskInput{AgentID: "cto", Title: "定目標"})
	if err != nil {
		t.Fatal(err)
	}
	r3, _ := svc.ClaimTask(boss, "o1", service.ClaimInput{AgentID: "cto", TaskID: ta.ID})
	cp, _ := svc.AuthenticateAgent("cto", r3.Credential.Secret, "ip")
	blocked := "blocked"
	before, _ := st.ListAgentTasks("o1")
	if _, err := svc.ReportTask(cp, "o1", "", service.TaskReport{Status: &blocked, Note: sp("預算多少?")}); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.ListAgentTasks("o1"); len(after) != len(before) {
		t.Fatal("真人不建任務")
	}
	got, _ := st.GetAgentTask("o1", ta.ID)
	if n := got.Notes[len(got.Notes)-1]; n.Author != "chain" || n.Text != "已回報給 alice@x" {
		t.Fatalf("%+v", got.Notes)
	}
	mine, err := svc.ListAgentTasks(alice, "o1", service.TaskFilter{ReportTo: "me"})
	if err != nil || len(mine) != 1 || mine[0].ID != ta.ID {
		t.Fatalf("回報給我的任務:%+v %v", mine, err)
	}
	if l, _ := svc.ListAgentTasks(boss, "o1", service.TaskFilter{ReportTo: "user:ALICE@x"}); len(l) != 1 {
		t.Fatal(l)
	}
	if l, _ := svc.ListAgentTasks(boss, "o1", service.TaskFilter{ReportTo: "me"}); len(l) != 0 {
		t.Fatal("boss 沒有回報給他的", l)
	}
	_ = cto
}

func TestChainWorkerDelegate(t *testing.T) {
	svc, _, boss, alice, bob := chainEnv(t)
	if _, err := svc.SaveChain(boss, "o1", edges("agent:cto>agent:pm>任務")); err != nil {
		t.Fatal(err)
	}
	tk, err := svc.AssignTask(boss, "o1", service.TaskInput{AgentID: "cto", Title: "規劃"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ClaimTask(alice, "o1", service.ClaimInput{AgentID: "cto", TaskID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	in := service.DelegateInput{To: "pm", Title: "寫規格", From: "designer"}
	if _, err := svc.WorkerDelegate(alice, "o1", tk.ID, "", in); code(err) != 400 {
		t.Fatal("要驗證碼", err)
	}
	if _, err := svc.WorkerDelegate(bob, "o1", tk.ID, res.Credential.Code, in); code(err) != 403 {
		t.Fatal("非領取者", err)
	}
	d, err := svc.WorkerDelegate(alice, "o1", tk.ID, res.Credential.Code, in)
	if err != nil || d.CreatedBy != "agent:cto" || d.AgentID != "pm" || d.Links[0] != tk.ID {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := svc.WorkerDelegate(alice, "o1", tk.ID, res.Credential.Code, service.DelegateInput{To: "designer", Title: "x"}); code(err) != 403 {
		t.Fatal("鏈上沒有的邊", err)
	}
}

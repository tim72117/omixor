package service_test

import (
	"fmt"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/storetest"
)

// 第 3 階段:專案成員與受限專案(restricted)。每個可能回傳受限專案內容的入口,都有「非成員看不到」的測試(表驅動),
// 還有成員管理、非受限專案的行為保留(回歸)、衍生資料(任務、頻道綁定、角色 docAccess)的檢查。

type rEnv struct {
	t     *testing.T
	svc   *service.Service
	boss  *model.Principal // o1 組織管理員
	alice *model.Principal // o1 一般成員,不是專案成員
	bob   *model.Principal // o1 一般成員,SEC 專案成員(member)
	carol *model.Principal // o1 一般成員,SEC 專案管理員(admin)
	eve   *model.Principal // 只屬於 o2
	cto   *model.Principal // 角色憑證:SEC 專案成員
	pm    *model.Principal // 角色憑證:不是專案成員
	xa    *model.Principal // o2 的角色憑證

	sec, app         *model.Project // sec 受限(PRJ-1,代號 SEC)、app 不受限(PRJ-2,代號 APP)
	secIssue         *model.Issue   // SEC-1
	movedIssue       *model.Issue   // 原本是 APP-2,搬進 SEC 後是 SEC-2(舊 key APP-2 留在別名表)
	appIssue, orgIss *model.Issue   // APP-1、ISS-1
	secDoc, appDoc   *model.CabinetDoc
	orgDoc           *model.CabinetDoc
	secChannel       string // 綁 SEC-1 的頻道(boss、alice 是成員)
}

func newREnv(t *testing.T) *rEnv {
	t.Helper()
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	for _, o := range []string{"o1", "o2"} {
		_ = st.CreateOrg(model.Org{ID: o, Name: o, CreatedAt: now})
	}
	for _, u := range []struct{ id, email, org, role string }{
		{"u1", "boss@x", "o1", "admin"}, {"u2", "alice@x", "o1", "member"}, {"u3", "bob@x", "o1", "member"},
		{"u4", "carol@x", "o1", "member"}, {"u5", "eve@x", "o2", "member"}, {"u6", "zed@x", "o2", "admin"},
	} {
		_ = st.SaveUser(model.User{ID: u.id, Email: u.email, Role: "member", CreatedAt: now})
		_ = st.SaveMember(model.Member{Org: u.org, UserID: u.id, Email: u.email, Role: u.role, AddedAt: now})
	}
	mk := func(id, email string) *model.Principal {
		return &model.Principal{User: model.User{ID: id, Email: email, Role: "member"}}
	}
	e := &rEnv{t: t, svc: svc, boss: mk("u1", "boss@x"), alice: mk("u2", "alice@x"), bob: mk("u3", "bob@x"), carol: mk("u4", "carol@x"), eve: mk("u5", "eve@x")}
	zed := mk("u6", "zed@x")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []model.Agent{{ID: "cto", Name: "研發長", Active: true}, {ID: "pm", Name: "產品經理", Active: true}} {
		_, err := svc.SaveAgent(e.boss, "o1", g)
		must(err)
	}
	_, err := svc.SaveAgent(zed, "o2", model.Agent{ID: "xa", Name: "外部角色", Active: true})
	must(err)
	e.cto = &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto", TaskID: "T-1"}}
	e.pm = &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "pm", TaskID: "T-2"}}
	e.xa = &model.Principal{Agent: &model.AgentIdentity{Org: "o2", AgentID: "xa", TaskID: "T-3"}}

	e.sec, err = svc.CreateProject(e.boss, "o1", service.ProjectInput{Name: "機密", Key: "SEC", Restricted: true})
	must(err)
	e.app, err = svc.CreateProject(e.boss, "o1", service.ProjectInput{Name: "公開", Key: "APP"})
	must(err)
	for _, m := range []service.ProjectMemberInput{{Actor: "bob@x", Role: "member"}, {Actor: "carol@x", Role: "admin"}, {Actor: "agent:cto", Role: "member"}} {
		_, err := svc.SetProjectMember(e.boss, "o1", e.sec.ID, m)
		must(err)
	}
	e.secIssue, err = svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "機密 Issue 標題", Body: "機密內文", Project: sp(e.sec.ID)})
	must(err)
	e.appIssue, err = svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "公開 Issue", Project: sp(e.app.ID)})
	must(err)
	e.movedIssue, err = svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "搬進來的", Project: sp(e.app.ID)})
	must(err)
	e.movedIssue, err = svc.UpdateIssue(e.boss, "o1", e.movedIssue.ID, service.IssuePatch{Project: sp(e.sec.ID)})
	must(err)
	e.orgIss, err = svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "組織層級"})
	must(err)
	e.secDoc, err = svc.CreateDoc(e.boss, "o1", service.DocInput{Project: e.sec.ID, Title: "機密文件", Body: "# 機密\n", Section: "docs"})
	must(err)
	e.appDoc, err = svc.CreateDoc(e.boss, "o1", service.DocInput{Project: e.app.ID, Title: "公開文件", Body: "# 公開\n", Section: "docs"})
	must(err)
	e.orgDoc, err = svc.CreateDoc(e.boss, "o1", service.DocInput{Title: "組織文件", Body: "# 組織\n", Section: "docs"})
	must(err)
	ch, err := svc.CreateChannel(e.boss, "o1", service.ChannelInput{Name: "機密討論", IssueID: e.secIssue.ID, Members: []string{"alice@x"}})
	must(err)
	e.secChannel = ch.ID
	return e
}

func keyList(l []model.Issue) string {
	out := []string{}
	for _, i := range l {
		out = append(out, i.Key)
	}
	return strings.Join(out, ",")
}

// stripRef 把錯誤訊息裡的編號換掉,比對「看不到」與「不存在」是不是同一句話
func stripRef(err error, refs ...string) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	for _, r := range refs {
		s = strings.ReplaceAll(s, r, "<ref>")
	}
	return s
}

// ---- 非成員看不到:表驅動,每個入口一列 ----

type hiddenCase struct {
	name string
	// probe 用 p 打這個入口;回傳 (err, leaked):leaked 非空 = 看到了不該看的(描述);err 是入口回的錯誤(單筆入口要求 404/403)
	probe func(e *rEnv, p *model.Principal) (err error, leaked string)
	// wantNotFound 單筆入口:人類非成員要回 404(不是 403,403 會洩漏存在);角色憑證可以是 403(該入口不開給角色)
	wantNotFound bool
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func hiddenCases() []hiddenCase {
	leakIssues := func(l []model.Issue) string {
		for _, i := range l {
			if i.Project == "PRJ-1" || strings.HasPrefix(i.Key, "SEC-") || strings.Contains(i.Title, "機密") {
				return "Issue " + i.Key
			}
		}
		return ""
	}
	return []hiddenCase{
		{name: "ListIssues", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListIssues(p, "o1")
			return err, leakIssues(l)
		}},
		{name: "QueryIssues 全部", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.QueryIssues(p, "o1", model.IssueQuery{})
			return err, leakIssues(l)
		}},
		{name: "QueryIssues 搜尋標題", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.QueryIssues(p, "o1", model.IssueQuery{Q: "機密"})
			return err, leakIssues(l)
		}},
		{name: "QueryIssues 搜尋 key", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.QueryIssues(p, "o1", model.IssueQuery{Q: "SEC-1"})
			return err, leakIssues(l)
		}},
		{name: "QueryIssues 指定專案", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.QueryIssues(p, "o1", model.IssueQuery{Project: "PRJ-1"})
			return err, leakIssues(l)
		}},
		{name: "IssueBoard", probe: func(e *rEnv, p *model.Principal) (error, string) {
			b, err := e.svc.IssueBoard(p, "o1", model.IssueQuery{})
			if b == nil {
				return err, ""
			}
			all := []model.Issue{}
			for _, c := range b.Columns {
				all = append(all, c.Issues...)
			}
			if b.Total != len(all) && leakIssues(all) == "" && b.Total > b.Matched {
				return err, "看板 total 帶出隱藏 Issue 的數量"
			}
			return err, leakIssues(all)
		}},
		{name: "GetIssue 內部 id", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			i, err := e.svc.GetIssue(p, "o1", e.secIssue.ID)
			return err, ifp(i != nil, "拿到 Issue")
		}},
		{name: "GetIssue 目前的 key", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			i, err := e.svc.GetIssue(p, "o1", "SEC-1")
			return err, ifp(i != nil, "拿到 Issue")
		}},
		{name: "GetIssue 小寫 key", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			i, err := e.svc.GetIssue(p, "o1", "sec-1")
			return err, ifp(i != nil, "拿到 Issue")
		}},
		{name: "GetIssue 舊 key(別名)", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			i, err := e.svc.GetIssue(p, "o1", "APP-2")
			return err, ifp(i != nil, "拿到 Issue")
		}},
		{name: "ResolveIssueID key", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			id, err := e.svc.ResolveIssueID(p, "o1", "SEC-1")
			return err, ifp(id != "", "解析到 id")
		}},
		{name: "ResolveIssueID 別名", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			id, err := e.svc.ResolveIssueID(p, "o1", "APP-2")
			return err, ifp(id != "", "解析到 id")
		}},
		{name: "ResolveIssueID UUID", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			id, err := e.svc.ResolveIssueID(p, "o1", e.secIssue.ID)
			return err, ifp(id != "", "解析到 id")
		}},
		{name: "UpdateIssue", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.UpdateIssue(p, "o1", "SEC-1", service.IssuePatch{Title: sp("被改")})
			return err, ""
		}},
		{name: "UpdateIssue 帶舊版本(不能靠 409 取得內容)", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			v := 999
			_, err := e.svc.UpdateIssue(p, "o1", "SEC-1", service.IssuePatch{Title: sp("被改"), Version: &v})
			return err, ""
		}},
		{name: "AddIssueComment", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.AddIssueComment(p, "o1", "SEC-1", service.CommentInput{Text: "偷留言"})
			return err, ""
		}},
		{name: "DeleteIssue", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			return e.svc.DeleteIssue(p, "o1", "SEC-1"), ""
		}},
		{name: "CreateIssue 指定受限專案", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.CreateIssue(p, "o1", service.IssueInput{Title: "塞進去", Project: sp("PRJ-1")})
			return err, ""
		}},
		{name: "UpdateIssue 搬進受限專案", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.UpdateIssue(p, "o1", e.appIssue.ID, service.IssuePatch{Project: sp("PRJ-1")})
			return err, ""
		}},
		{name: "ListProjects", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListProjects(p, "o1")
			for _, x := range l {
				if x.ID == "PRJ-1" || x.Key == "SEC" {
					return err, "專案 " + x.ID
				}
			}
			return err, ""
		}},
		{name: "GetProject", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			x, err := e.svc.GetProject(p, "o1", "PRJ-1")
			return err, ifp(x != nil, "拿到專案")
		}},
		{name: "ListProjectRepos 單一專案", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.ListProjectRepos(p, "o1", "PRJ-1")
			return err, ""
		}},
		{name: "ListProjectRepos 整個組織", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListProjectRepos(p, "o1", "")
			for _, x := range l {
				if x.Project == "PRJ-1" {
					return err, "repo 連結 " + x.Repo
				}
			}
			return err, ""
		}},
		{name: "ListProjectMembers", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListProjectMembers(p, "o1", "PRJ-1")
			return err, ifp(len(l) > 0, "拿到成員名單")
		}},
		{name: "SetProjectMember", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.SetProjectMember(p, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "alice@x"})
			return err, ""
		}},
		{name: "RemoveProjectMember", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			return e.svc.RemoveProjectMember(p, "o1", "PRJ-1", "bob@x"), ""
		}},
		{name: "GetCabinet 受限專案", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			c, err := e.svc.GetCabinet(p, "o1", "PRJ-1")
			return err, ifp(c != nil, "拿到文件櫃")
		}},
		{name: "GetCabinet 未分專案", probe: func(e *rEnv, p *model.Principal) (error, string) {
			c, err := e.svc.GetCabinet(p, "o1", "")
			if c != nil {
				for _, d := range c.Docs {
					if d.Project == "PRJ-1" || strings.Contains(d.Title, "機密") {
						return err, "文件 " + d.ID
					}
				}
			}
			return err, ""
		}},
		{name: "GetDoc", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			d, err := e.svc.GetDoc(p, "o1", e.secDoc.ID)
			return err, ifp(d != nil, "拿到文件")
		}},
		{name: "ReadDoc", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			d, err := e.svc.ReadDoc(p, "o1", e.secDoc.ID, service.DocRead{})
			return err, ifp(d != nil, "讀到文件")
		}},
		{name: "UpdateDoc", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.UpdateDoc(p, "o1", e.secDoc.ID, service.DocPatch{Title: sp("被改")})
			return err, ""
		}},
		{name: "EditDoc", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.EditDoc(p, "o1", e.secDoc.ID, service.DocEdit{AppendToSection: "機密", Content: "x"})
			return err, ""
		}},
		{name: "DeleteDoc", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			return e.svc.DeleteDoc(p, "o1", e.secDoc.ID), ""
		}},
		{name: "CreateDoc 指定受限專案", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.CreateDoc(p, "o1", service.DocInput{Project: "PRJ-1", Title: "塞進去", Section: "docs"})
			return err, ""
		}},
		{name: "UpdateDoc 搬進受限專案", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.UpdateDoc(p, "o1", e.appDoc.ID, service.DocPatch{Project: sp("PRJ-1")})
			return err, ""
		}},
		{name: "ListChannels 依受限 Issue 篩選", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListChannels(p, "o1", service.ChannelFilter{IssueID: "SEC-1"})
			return err, ifp(len(l) > 0, "找到綁定的頻道")
		}},
		{name: "ListChannels 輸出不含受限 Issue 編號", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListChannels(p, "o1", service.ChannelFilter{})
			for _, c := range l {
				if c.IssueID != "" {
					return err, "頻道 " + c.ID + " 的 issueId"
				}
			}
			return err, ""
		}},
		{name: "GetChannel 輸出不含受限 Issue 編號", probe: func(e *rEnv, p *model.Principal) (error, string) {
			c, err := e.svc.GetChannel(p, "o1", "CH-1", "")
			if c != nil && c.IssueID != "" {
				return err, "issueId"
			}
			return err, ""
		}},
		{name: "CreateChannel 綁受限 Issue", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			_, err := e.svc.CreateChannel(p, "o1", service.ChannelInput{Name: "探測", IssueID: "SEC-1"})
			return err, ""
		}},
		{name: "ListAgentTasks", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListAgentTasks(p, "o1", service.TaskFilter{})
			for _, t := range l {
				if strings.Contains(t.Title, "機密") || strings.Contains(t.Instructions, "機密") {
					return err, "任務 " + t.ID
				}
			}
			return err, ""
		}},
		{name: "GetAgentTask", wantNotFound: true, probe: func(e *rEnv, p *model.Principal) (error, string) {
			t, err := e.svc.GetAgentTask(p, "o1", "T-1")
			return err, ifp(t != nil, "拿到任務")
		}},
		{name: "ListAgents 的 docAccess 不含受限專案", probe: func(e *rEnv, p *model.Principal) (error, string) {
			l, err := e.svc.ListAgents(p, "o1")
			for _, g := range l {
				if g.ID == "pm" || g.ID == "cto" {
					for k := range g.DocAccess {
						if strings.HasPrefix(k, "PRJ-1:") {
							return err, "docAccess " + k
						}
					}
				}
			}
			return err, ""
		}},
		{name: "GetAgent 的 docAccess 不含受限專案", probe: func(e *rEnv, p *model.Principal) (error, string) {
			g, err := e.svc.GetAgent(p, "o1", "cto")
			if g != nil {
				for k := range g.DocAccess {
					if strings.HasPrefix(k, "PRJ-1:") {
						return err, "docAccess " + k
					}
				}
			}
			return err, ""
		}},
	}
}

func ifp(c bool, s string) string {
	if c {
		return s
	}
	return ""
}

// 非成員:人類一般成員(alice)與沒有被加成員的角色憑證(pm)在每個入口都看不到;
// 單筆入口人類回 404(不是 403),而且訊息和「真的不存在」一樣(不洩漏存在)
func TestRestrictedHiddenFromNonMembers(t *testing.T) {
	e := newREnv(t)
	// 準備衍生資料:任務(指派給成員角色 cto)與 docAccess(pm、cto 各有一筆指向受限專案;cto 的另有 docs 權限)
	if _, err := e.svc.UpdateIssue(e.boss, "o1", e.secIssue.ID, service.IssuePatch{Assignee: sp("cto")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveAgent(e.boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: true, DocAccess: map[string]string{"PRJ-1:docs": "read"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveAgent(e.boss, "o1", model.Agent{ID: "pm", Name: "產品經理", Active: true, DocAccess: map[string]string{"PRJ-2:docs": "read"}}); err != nil {
		t.Fatal(err)
	}
	for _, hc := range hiddenCases() {
		for _, who := range []struct {
			name  string
			p     *model.Principal
			agent bool
		}{{"alice", e.alice, false}, {"pm", e.pm, true}} {
			t.Run(hc.name+"/"+who.name, func(t *testing.T) {
				err, leaked := hc.probe(e, who.p)
				if leaked != "" {
					t.Fatalf("洩漏:%s(err=%v)", leaked, err)
				}
				if hc.wantNotFound {
					c := service.Code(err)
					switch {
					case err == nil:
						t.Fatal("單筆入口應該被拒,卻成功了")
					case who.agent && (c == 403 || c == 404):
					case c == 404:
					default:
						t.Fatalf("要 404(不洩漏存在),實際 %d:%v", c, err)
					}
				}
			})
		}
	}
	// 成員、專案管理員、組織管理員、成員角色看得到(同一份探測要「洩漏」才對:證明探測有效,不是永遠空的)
	sees := []struct {
		name string
		p    *model.Principal
	}{{"boss", e.boss}, {"bob", e.bob}, {"carol", e.carol}}
	for _, who := range sees {
		l, err := e.svc.ListIssues(who.p, "o1")
		if err != nil || !strings.Contains(keyList(l), "SEC-1") || !strings.Contains(keyList(l), "SEC-2") {
			t.Fatal(who.name, "要看得到受限專案的 Issue", keyList(l), err)
		}
		if i, err := e.svc.GetIssue(who.p, "o1", "APP-2"); err != nil || i.Key != "SEC-2" {
			t.Fatal(who.name, "舊 key 要解析到搬進來的 Issue", i, err)
		}
		if x, err := e.svc.GetProject(who.p, "o1", "PRJ-1"); err != nil || x.Name != "機密" || !x.Restricted {
			t.Fatal(who.name, "要看得到受限專案", x, err)
		}
		if d, err := e.svc.GetDoc(who.p, "o1", e.secDoc.ID); err != nil || d.Title != "機密文件" {
			t.Fatal(who.name, "要看得到受限專案的文件", d, err)
		}
		if c, err := e.svc.GetCabinet(who.p, "o1", "PRJ-1"); err != nil || len(c.Docs) != 1 {
			t.Fatal(who.name, "要看得到受限專案的文件櫃", c, err)
		}
	}
	// 成員角色 cto:看得到專案與 Issue;文件靠 docAccess(上面設了 PRJ-1:docs 讀取)
	if l, err := e.svc.ListIssues(e.cto, "o1"); err != nil || !strings.Contains(keyList(l), "SEC-1") {
		t.Fatal("成員角色看得到受限專案的 Issue", keyList(l), err)
	}
	if d, err := e.svc.GetDoc(e.cto, "o1", e.secDoc.ID); err != nil || d.ID != e.secDoc.ID {
		t.Fatal("成員角色有 docAccess 讀得到文件", d, err)
	}
	// 非成員角色 pm 的 docAccess 沒有 PRJ-1:看不到受限專案的文件(404,不是 403)
	if _, err := e.svc.GetDoc(e.pm, "o1", e.secDoc.ID); service.Code(err) != 404 {
		t.Fatal("pm 沒有 docAccess:404", err)
	}
	// 沒有成員資格、docAccess 明確列出受限專案的角色:文件櫃與專案看得到(管理員明確給的),Issue 看不到
	if _, err := e.svc.SaveAgent(e.boss, "o1", model.Agent{ID: "pm", Name: "產品經理", Active: true, DocAccess: map[string]string{"PRJ-1:docs": "read"}}); err != nil {
		t.Fatal(err)
	}
	if d, err := e.svc.GetDoc(e.pm, "o1", e.secDoc.ID); err != nil || d.ID != e.secDoc.ID {
		t.Fatal("docAccess 明確列出的專案,文件讀得到", d, err)
	}
	if _, err := e.svc.GetIssue(e.pm, "o1", "SEC-1"); service.Code(err) != 404 {
		t.Fatal("docAccess 不等於專案成員:Issue 看不到", err)
	}
}

// 看不到(受限)與不存在是同一句話:key、UUID、專案、文件、頻道綁定
func TestRestrictedNotFoundIsIndistinguishable(t *testing.T) {
	e := newREnv(t)
	for _, c := range []struct {
		name         string
		hidden, none func() error
		refs         []string
	}{
		{"GetIssue key", func() error { _, err := e.svc.GetIssue(e.alice, "o1", "SEC-1"); return err },
			func() error { _, err := e.svc.GetIssue(e.alice, "o1", "SEC-99"); return err }, []string{"SEC-1", "SEC-99"}},
		{"GetIssue UUID", func() error { _, err := e.svc.GetIssue(e.alice, "o1", e.secIssue.ID); return err },
			func() error {
				_, err := e.svc.GetIssue(e.alice, "o1", "0190a000-0000-7000-8000-000000000000")
				return err
			},
			[]string{e.secIssue.ID, "0190a000-0000-7000-8000-000000000000"}},
		{"ResolveIssueID", func() error { _, err := e.svc.ResolveIssueID(e.alice, "o1", "SEC-1"); return err },
			func() error { _, err := e.svc.ResolveIssueID(e.alice, "o1", "SEC-99"); return err }, []string{"SEC-1", "SEC-99"}},
		{"UpdateIssue", func() error {
			_, err := e.svc.UpdateIssue(e.alice, "o1", "SEC-1", service.IssuePatch{Title: sp("x")})
			return err
		},
			func() error {
				_, err := e.svc.UpdateIssue(e.alice, "o1", "SEC-99", service.IssuePatch{Title: sp("x")})
				return err
			}, []string{"SEC-1", "SEC-99"}},
		{"GetProject", func() error { _, err := e.svc.GetProject(e.alice, "o1", "PRJ-1"); return err },
			func() error { _, err := e.svc.GetProject(e.alice, "o1", "PRJ-99"); return err }, []string{"PRJ-1", "PRJ-99"}},
		{"CreateIssue 專案", func() error {
			_, err := e.svc.CreateIssue(e.alice, "o1", service.IssueInput{Title: "x", Project: sp("PRJ-1")})
			return err
		},
			func() error {
				_, err := e.svc.CreateIssue(e.alice, "o1", service.IssueInput{Title: "x", Project: sp("PRJ-99")})
				return err
			}, []string{"PRJ-1", "PRJ-99"}},
		{"GetCabinet", func() error { _, err := e.svc.GetCabinet(e.alice, "o1", "PRJ-1"); return err },
			func() error { _, err := e.svc.GetCabinet(e.alice, "o1", "PRJ-99"); return err }, []string{"PRJ-1", "PRJ-99"}},
		{"GetDoc", func() error { _, err := e.svc.GetDoc(e.alice, "o1", e.secDoc.ID); return err },
			func() error { _, err := e.svc.GetDoc(e.alice, "o1", "DOC-99"); return err }, []string{e.secDoc.ID, "DOC-99"}},
		{"CreateChannel 綁 Issue", func() error {
			_, err := e.svc.CreateChannel(e.alice, "o1", service.ChannelInput{Name: "x", IssueID: "SEC-1"})
			return err
		}, func() error {
			_, err := e.svc.CreateChannel(e.alice, "o1", service.ChannelInput{Name: "x", IssueID: "SEC-99"})
			return err
		}, []string{"SEC-1", "SEC-99"}},
	} {
		h, n := c.hidden(), c.none()
		if service.Code(h) != 404 || service.Code(n) != 404 {
			t.Fatal(c.name, "都要 404:", h, "|", n)
		}
		if stripRef(h, c.refs...) != stripRef(n, c.refs...) {
			t.Fatalf("%s:看不到與不存在的訊息不同:\n  看不到:%v\n  不存在:%v", c.name, h, n)
		}
	}
}

// 外部組織的成員、外部組織的角色憑證:整個組織都看不到(403),受限專案不會因為「看不到組織」而變成不同的錯誤型態洩漏
func TestRestrictedExternalOrg(t *testing.T) {
	e := newREnv(t)
	for _, who := range []*model.Principal{e.eve, e.xa} {
		if _, err := e.svc.ListIssues(who, "o1"); service.Code(err) != 403 {
			t.Fatal("外部:列表 403", err)
		}
		if _, err := e.svc.GetIssue(who, "o1", "SEC-1"); service.Code(err) != 403 {
			t.Fatal("外部:單筆 403", err)
		}
		if _, err := e.svc.GetProject(who, "o1", "PRJ-1"); service.Code(err) != 403 {
			t.Fatal("外部:專案 403", err)
		}
		if _, err := e.svc.GetDoc(who, "o1", e.secDoc.ID); service.Code(err) != 403 {
			t.Fatal("外部:文件 403", err)
		}
	}
	// 外部組織的使用者不能被加成員
	if _, err := e.svc.SetProjectMember(e.boss, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "eve@x"}); service.Code(err) != 400 {
		t.Fatal("不是組織成員的使用者不能加:400", err)
	}
	// 外部組織的角色不能被加
	if _, err := e.svc.SetProjectMember(e.boss, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "agent:xa"}); service.Code(err) != 400 {
		t.Fatal("別的組織的角色不能加:400", err)
	}
}

// 回歸:非受限專案、組織層級的 Issue 與文件,所有組織成員(含角色憑證)照舊看得到、能寫
func TestUnrestrictedProjectKeepsOrgVisibility(t *testing.T) {
	e := newREnv(t)
	for _, p := range []*model.Principal{e.alice, e.bob, e.pm, e.cto} {
		l, err := e.svc.ListIssues(p, "o1")
		if err != nil || !strings.Contains(keyList(l), "APP-1") || !strings.Contains(keyList(l), "ISS-1") {
			t.Fatal("非受限專案與組織層級的 Issue 要看得到", keyList(l), err)
		}
		if i, err := e.svc.GetIssue(p, "o1", "APP-1"); err != nil || i.Title != "公開 Issue" {
			t.Fatal(i, err)
		}
		if _, err := e.svc.AddIssueComment(p, "o1", "APP-1", service.CommentInput{Text: "留言"}); err != nil {
			t.Fatal("非受限專案可留言", err)
		}
		ps, err := e.svc.ListProjects(p, "o1")
		if err != nil {
			t.Fatal(err)
		}
		hasApp := false
		for _, x := range ps {
			hasApp = hasApp || x.ID == "PRJ-2"
		}
		if !hasApp && p.Agent == nil {
			t.Fatal("非受限專案要列出", ps)
		}
	}
	for _, p := range []*model.Principal{e.alice, e.bob} {
		if x, err := e.svc.GetProject(p, "o1", "PRJ-2"); err != nil || x.Restricted {
			t.Fatal(x, err)
		}
		if c, err := e.svc.GetCabinet(p, "o1", "PRJ-2"); err != nil || len(c.Docs) < 1 || c.Docs[len(c.Docs)-1].Title != "公開文件" {
			t.Fatal("非受限專案的文件櫃", c, err)
		}
		if _, err := e.svc.CreateIssue(p, "o1", service.IssueInput{Title: "新的", Project: sp("PRJ-2")}); err != nil {
			t.Fatal("非受限專案可建 Issue", err)
		}
		if _, err := e.svc.CreateDoc(p, "o1", service.DocInput{Project: "PRJ-2", Title: "新文件", Section: "docs"}); err != nil {
			t.Fatal("非受限專案可建文件", err)
		}
		if _, err := e.svc.ListProjectMembers(p, "o1", "PRJ-2"); err != nil {
			t.Fatal("非受限專案的成員名單看得到", err)
		}
	}
	// 沒有任何受限專案的組織:alice 與以前完全一樣
	if _, err := e.svc.UpdateProject(e.boss, "o1", "PRJ-1", service.ProjectPatch{Restricted: bp(false)}); err != nil {
		t.Fatal(err)
	}
	if l, err := e.svc.ListIssues(e.alice, "o1"); err != nil || !strings.Contains(keyList(l), "SEC-1") {
		t.Fatal("改成公開後組織成員都看得到", keyList(l), err)
	}
	if d, err := e.svc.GetDoc(e.alice, "o1", e.secDoc.ID); err != nil || d.ID == "" {
		t.Fatal("改成公開後文件看得到", err)
	}
}

// 切換受限:公開 → 受限立刻生效(不是成員的人馬上看不到),受限 → 公開立刻生效;只有組織管理員能改旗標
func TestRestrictedToggle(t *testing.T) {
	e := newREnv(t)
	if _, err := e.svc.UpdateProject(e.carol, "o1", "PRJ-1", service.ProjectPatch{Restricted: bp(false)}); service.Code(err) != 403 {
		t.Fatal("專案管理員不能把受限專案改成公開(只有組織管理員)", err)
	}
	if _, err := e.svc.UpdateProject(e.alice, "o1", "PRJ-2", service.ProjectPatch{Restricted: bp(true)}); service.Code(err) != 403 {
		t.Fatal("一般成員不能改", err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", "APP-1"); err != nil {
		t.Fatal(err)
	}
	x, err := e.svc.UpdateProject(e.boss, "o1", "PRJ-2", service.ProjectPatch{Restricted: bp(true)})
	if err != nil || !x.Restricted || x.MyRole != "admin" {
		t.Fatal(x, err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", "APP-1"); service.Code(err) != 404 {
		t.Fatal("改成受限後,非成員立刻看不到", err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", "APP-2"); service.Code(err) != 404 {
		t.Fatal("(APP-2 是別名,搬進 SEC 的)", err)
	}
	if _, err := e.svc.UpdateProject(e.boss, "o1", "PRJ-2", service.ProjectPatch{Restricted: bp(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", "APP-1"); err != nil {
		t.Fatal("改回公開後看得到", err)
	}
	// 建立時就受限
	y, err := e.svc.CreateProject(e.boss, "o1", service.ProjectInput{Name: "新受限", Key: "NEW", Restricted: true})
	if err != nil || !y.Restricted {
		t.Fatal(y, err)
	}
	if _, err := e.svc.GetProject(e.alice, "o1", y.ID); service.Code(err) != 404 {
		t.Fatal("新受限專案對非成員 404", err)
	}
}

// 專案成員管理:誰能管、角色、冪等、移除後立刻看不到、專案管理員的範圍
func TestProjectMembersManagement(t *testing.T) {
	e := newREnv(t)
	// 名單(boss 與成員都看得到;依加入順序)
	l, err := e.svc.ListProjectMembers(e.bob, "o1", "PRJ-1")
	roles := map[string]string{}
	for _, m := range l {
		roles[m.Node] = m.Role
		if m.Actor == nil || m.Actor.ID != m.ActorID && m.ActorID != "" {
			t.Fatal("成員要帶 actor 物件", m)
		}
		if (m.Node == "agent:cto") != (m.Actor.Kind == model.ActorKindAgent) {
			t.Fatal("actor 種類", m)
		}
	}
	if err != nil || len(l) != 3 || roles["user:bob@x"] != "member" || roles["user:carol@x"] != "admin" || roles["agent:cto"] != "member" {
		t.Fatalf("%+v %v", l, err)
	}
	// 一般成員(不是專案管理員)不能管;專案管理員 carol 可以
	if _, err := e.svc.SetProjectMember(e.bob, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "alice@x"}); service.Code(err) != 403 {
		t.Fatal("成員不是專案管理員:403", err)
	}
	if err := e.svc.RemoveProjectMember(e.bob, "o1", "PRJ-1", "carol@x"); service.Code(err) != 403 {
		t.Fatal("成員不能移除別人:403", err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", "SEC-1"); service.Code(err) != 404 {
		t.Fatal(err)
	}
	m, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "alice@x"})
	if err != nil || m.Role != "member" || m.Node != "user:alice@x" {
		t.Fatal("專案管理員可加(預設 member)", m, err)
	}
	if i, err := e.svc.GetIssue(e.alice, "o1", "SEC-1"); err != nil || i.Key != "SEC-1" {
		t.Fatal("加入後立刻看得到", i, err)
	}
	// 冪等、改角色(Role 空 = 不變)
	if m, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "alice@x"}); err != nil || m.Role != "member" {
		t.Fatal(m, err)
	}
	if m, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "alice@x", Role: "admin"}); err != nil || m.Role != "admin" {
		t.Fatal("改角色", m, err)
	}
	if _, err := e.svc.SetProjectMember(e.alice, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "bob@x", Role: "admin"}); err != nil {
		t.Fatal("alice 現在是專案管理員", err)
	}
	if _, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "bob@x", Role: "boss"}); service.Code(err) != 400 {
		t.Fatal("role 不認得 400", err)
	}
	if _, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "nobody@x"}); service.Code(err) != 400 {
		t.Fatal("找不到的人 400", err)
	}
	if _, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-99", service.ProjectMemberInput{Actor: "bob@x"}); service.Code(err) != 404 {
		t.Fatal("專案不存在 404", err)
	}
	// carol 是 SEC 的專案管理員,不是 APP 的:APP 是公開專案,看得到但不能管
	if _, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-2", service.ProjectMemberInput{Actor: "bob@x"}); service.Code(err) != 403 {
		t.Fatal("別的專案的管理員不能管:403", err)
	}
	// 角色可以加成員(pm),加了之後看得到 Issue
	if _, err := e.svc.GetIssue(e.pm, "o1", "SEC-1"); service.Code(err) != 404 {
		t.Fatal(err)
	}
	if _, err := e.svc.SetProjectMember(e.boss, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "pm"}); err != nil {
		t.Fatal(err)
	}
	if i, err := e.svc.GetIssue(e.pm, "o1", "SEC-1"); err != nil || i.Key != "SEC-1" {
		t.Fatal("角色成員看得到", err)
	}
	// 角色憑證不能管理成員
	if _, err := e.svc.SetProjectMember(e.pm, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "alice@x"}); service.Code(err) != 403 {
		t.Fatal("角色憑證 403", err)
	}
	if _, err := e.svc.ListProjectMembers(e.pm, "o1", "PRJ-1"); service.Code(err) != 403 {
		t.Fatal("角色憑證不能列成員 403", err)
	}
	// 移除:立刻看不到;不是成員 404;移除舊人用 email、handle 都可以
	if err := e.svc.RemoveProjectMember(e.carol, "o1", "PRJ-1", "alice"); err == nil {
		t.Fatal("alice 是 email 不是 handle 簡寫:應該找不到(不猜)")
	}
	if err := e.svc.RemoveProjectMember(e.carol, "o1", "PRJ-1", "user:alice@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", "SEC-1"); service.Code(err) != 404 {
		t.Fatal("移除後立刻看不到", err)
	}
	if err := e.svc.RemoveProjectMember(e.carol, "o1", "PRJ-1", "alice@x"); service.Code(err) != 404 {
		t.Fatal("已經不是成員 404", err)
	}
	// 組織管理員(不是專案成員)也能管;被移除的專案管理員自己就失去權限
	if err := e.svc.RemoveProjectMember(e.boss, "o1", "PRJ-1", "carol@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetProjectMember(e.carol, "o1", "PRJ-1", service.ProjectMemberInput{Actor: "alice@x"}); service.Code(err) != 404 {
		t.Fatal("被移除的專案管理員:專案已經看不到 404", err)
	}
}

// 使用者被移出組織、角色被刪:專案成員資格一起清掉;再加回組織不會自動恢復受限專案的權限
func TestProjectMembershipDroppedWithOrgMemberAndAgent(t *testing.T) {
	e := newREnv(t)
	if err := e.svc.RemoveMember(e.boss, "o1", "bob@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SaveMember(e.boss, "o1", "bob@x", "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetIssue(e.bob, "o1", "SEC-1"); service.Code(err) != 404 {
		t.Fatal("移出再加回組織:不會自動恢復專案成員資格", err)
	}
	l, err := e.svc.ListProjectMembers(e.boss, "o1", "PRJ-1")
	if err != nil || len(l) != 2 {
		t.Fatal(l, err)
	}
	if err := e.svc.DeleteAgent(e.boss, "o1", "cto"); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.svc.ListProjectMembers(e.boss, "o1", "PRJ-1"); len(l) != 1 {
		t.Fatal("刪角色:成員資格清掉", l)
	}
	// 同 id 重建的角色是新的 actor,不繼承
	if _, err := e.svc.SaveAgent(e.boss, "o1", model.Agent{ID: "cto", Name: "研發長", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetIssue(e.cto, "o1", "SEC-1"); service.Code(err) != 404 {
		t.Fatal("重建的角色不是成員", err)
	}
}

// 搬移:搬出受限專案要專案管理員;搬進受限專案要看得到;指派的角色要是專案成員
func TestRestrictedMoveAndAssign(t *testing.T) {
	e := newREnv(t)
	// bob 是一般成員:不能把 SEC-1 搬出去(公開)
	if _, err := e.svc.UpdateIssue(e.bob, "o1", "SEC-1", service.IssuePatch{Project: sp("PRJ-2")}); service.Code(err) != 403 {
		t.Fatal("成員搬出受限專案 403", err)
	}
	if _, err := e.svc.UpdateIssue(e.bob, "o1", "SEC-1", service.IssuePatch{Project: sp("")}); service.Code(err) != 403 {
		t.Fatal("搬到組織層級也算搬出 403", err)
	}
	// 成員可以把公開的 Issue 搬進去(看得到);專案管理員可以搬出
	moved, err := e.svc.UpdateIssue(e.bob, "o1", "APP-1", service.IssuePatch{Project: sp("PRJ-1")})
	if err != nil || moved.Project != "PRJ-1" {
		t.Fatal(moved, err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", "APP-1"); service.Code(err) != 404 {
		t.Fatal("搬進受限專案後,舊 key 對非成員也是 404", err)
	}
	back, err := e.svc.UpdateIssue(e.carol, "o1", moved.ID, service.IssuePatch{Project: sp("PRJ-2")})
	if err != nil || back.Project != "PRJ-2" {
		t.Fatal(back, err)
	}
	if _, err := e.svc.GetIssue(e.alice, "o1", back.Key); err != nil {
		t.Fatal("搬出後公開", err)
	}
	// 指派:受限專案只能指派給成員角色
	if _, err := e.svc.UpdateIssue(e.boss, "o1", "SEC-1", service.IssuePatch{Assignee: sp("pm")}); service.Code(err) != 400 {
		t.Fatal("非成員角色不能被指派受限專案的 Issue:400", err)
	}
	if _, err := e.svc.CreateIssue(e.boss, "o1", service.IssueInput{Title: "x", Project: sp("PRJ-1"), Assignee: "pm"}); service.Code(err) != 400 {
		t.Fatal("建立時指派非成員角色 400", err)
	}
	if i, err := e.svc.UpdateIssue(e.boss, "o1", "SEC-1", service.IssuePatch{Assignee: sp("cto")}); err != nil || i.Assignee == "" {
		t.Fatal("成員角色可以被指派", i, err)
	}
	// 已指派給非成員角色的公開 Issue 搬進受限專案:擋下來
	if _, err := e.svc.UpdateIssue(e.boss, "o1", e.appIssue.ID, service.IssuePatch{Assignee: sp("pm")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateIssue(e.boss, "o1", e.appIssue.ID, service.IssuePatch{Project: sp("PRJ-1")}); service.Code(err) != 400 {
		t.Fatal("指派給非成員角色的 Issue 不能搬進受限專案:400", err)
	}
	// 沒指派的預設:repo 連到受限專案時,看不到的人建立的 Issue 退回組織層級(不會被塞進看不到的專案)
	if _, err := e.svc.LinkProjectRepo(e.boss, "o1", "PRJ-1", "acme/secret"); err != nil {
		t.Fatal(err)
	}
	i, err := e.svc.CreateIssue(e.alice, "o1", service.IssueInput{Title: "x", Repo: "acme/secret"})
	if err != nil || i.Project != "" {
		t.Fatal("非成員用 repo 預設:組織層級", i, err)
	}
	j, err := e.svc.CreateIssue(e.bob, "o1", service.IssueInput{Title: "y", Repo: "acme/secret"})
	if err != nil || j.Project != "PRJ-1" {
		t.Fatal("成員用 repo 預設:進受限專案", j, err)
	}
}

// 任務(角色排任務會帶出 Issue 標題與內文):非成員看不到、領不到;改成受限、移除成員時取消非成員角色的待領取任務
func TestRestrictedTasks(t *testing.T) {
	e := newREnv(t)
	// cto 是成員:指派排任務
	if _, err := e.svc.UpdateIssue(e.boss, "o1", "SEC-1", service.IssuePatch{Assignee: sp("cto")}); err != nil {
		t.Fatal(err)
	}
	all, err := e.svc.ListAgentTasks(e.boss, "o1", service.TaskFilter{})
	if err != nil || len(all) != 1 || !strings.Contains(all[0].Instructions, "機密內文") {
		t.Fatal(all, err)
	}
	tid := all[0].ID
	if l, err := e.svc.ListAgentTasks(e.alice, "o1", service.TaskFilter{}); err != nil || len(l) != 0 {
		t.Fatal("非成員列不到", l, err)
	}
	if l, err := e.svc.ListAgentTasks(e.bob, "o1", service.TaskFilter{}); err != nil || len(l) != 1 {
		t.Fatal("成員列得到", l, err)
	}
	if _, err := e.svc.GetAgentTask(e.alice, "o1", tid); service.Code(err) != 404 {
		t.Fatal(err)
	}
	if _, err := e.svc.ClaimTask(e.alice, "o1", service.ClaimInput{AgentID: "cto", TaskID: tid}); service.Code(err) != 404 {
		t.Fatal("非成員不能領:404", err)
	}
	if r, err := e.svc.ClaimTask(e.alice, "o1", service.ClaimInput{AgentID: "cto"}); err != nil || r.Task != nil {
		t.Fatal("自動挑也不會挑到(沒有可領的)", r, err)
	}
	if r, err := e.svc.ClaimNext(e.alice, "o1", ""); err != nil || r.Task != nil {
		t.Fatal("ClaimNext 不會挑到", r, err)
	}
	if _, err := e.svc.ReportTask(e.alice, "o1", tid, service.TaskReport{Note: sp("x")}); service.Code(err) != 404 {
		t.Fatal("非成員回報 404", err)
	}
	// 移除成員角色 cto:待領取的任務取消
	if err := e.svc.RemoveProjectMember(e.boss, "o1", "PRJ-1", "agent:cto"); err != nil {
		t.Fatal(err)
	}
	tk, err := e.svc.GetAgentTask(e.boss, "o1", tid)
	if err != nil || tk.Status != "canceled" {
		t.Fatal("移除成員角色後,待領取任務取消", tk, err)
	}
	// 公開專案轉受限:非成員角色手上待領取的任務取消
	if _, err := e.svc.UpdateIssue(e.boss, "o1", e.appIssue.ID, service.IssuePatch{Assignee: sp("pm")}); err != nil {
		t.Fatal(err)
	}
	l, _ := e.svc.ListAgentTasks(e.boss, "o1", service.TaskFilter{AgentID: "pm", Status: "open"})
	if len(l) != 1 {
		t.Fatal(l)
	}
	if _, err := e.svc.UpdateProject(e.boss, "o1", "PRJ-2", service.ProjectPatch{Restricted: bp(true)}); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.svc.ListAgentTasks(e.boss, "o1", service.TaskFilter{AgentID: "pm", Status: "open"}); len(l) != 0 {
		t.Fatal("改成受限:非成員角色的待領取任務取消", l)
	}
}

// 角色轉移任務、刪角色時,受限專案 Issue 的任務不會留在非成員角色手上;Issue 的指派也不會改給非成員
func TestRestrictedTransferAndReassign(t *testing.T) {
	e := newREnv(t)
	if _, err := e.svc.UpdateIssue(e.boss, "o1", "SEC-1", service.IssuePatch{Assignee: sp("cto")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.TransferAgentTasks(e.boss, "o1", service.AgentTransferInput{From: "cto", To: "pm"}); err != nil {
		t.Fatal(err)
	}
	l, _ := e.svc.ListAgentTasks(e.boss, "o1", service.TaskFilter{AgentID: "pm", Status: "open"})
	if len(l) != 0 {
		t.Fatal("轉給非成員角色:待領取任務取消", l)
	}
	if err := e.svc.DeleteAgentTo(e.boss, "o1", "cto", "pm"); err != nil {
		t.Fatal(err)
	}
	i, err := e.svc.GetIssue(e.boss, "o1", "SEC-1")
	if err != nil || i.Assignee != "" {
		t.Fatal("刪角色改派給非成員:只清掉指派", i, err)
	}
}

// 頻道綁受限 Issue:非成員看不到綁定(issueId 清空),成員看得到
func TestRestrictedChannelBinding(t *testing.T) {
	e := newREnv(t)
	c, err := e.svc.GetChannel(e.alice, "o1", e.secChannel, "") // alice 是頻道成員,不是專案成員
	if err != nil || c.IssueID != "" {
		t.Fatal("非專案成員:綁定不輸出", c, err)
	}
	if c.Name != "機密討論" {
		t.Fatal("頻道本身仍然是頻道成員的", c)
	}
	b, err := e.svc.GetChannel(e.boss, "o1", e.secChannel, "")
	if err != nil || b.IssueID != e.secIssue.ID {
		t.Fatal("組織管理員看得到綁定", b, err)
	}
	if l, err := e.svc.ListChannels(e.bob, "o1", service.ChannelFilter{IssueID: "SEC-1"}); err != nil || len(l) != 0 {
		t.Fatal("bob 不是頻道成員,列不到(頻道自己的 ACL)", l, err)
	}
	if l, err := e.svc.ListChannels(e.boss, "o1", service.ChannelFilter{IssueID: "SEC-1"}); err != nil || len(l) != 1 {
		t.Fatal("管理員依 key 篩選", l, err)
	}
}

// 角色 docAccess 指向受限專案的 key:非成員看不到(不洩漏專案 id 與位置);角色看自己的不篩
func TestRestrictedAgentDocAccessHidden(t *testing.T) {
	e := newREnv(t)
	if _, err := e.svc.SaveAgent(e.boss, "o1", model.Agent{ID: "pm", Name: "產品經理", Active: true,
		DocAccess: map[string]string{"PRJ-1:docs": "read", "PRJ-2:docs": "write", "docs": "read"}}); err != nil {
		t.Fatal(err)
	}
	g, err := e.svc.GetAgent(e.alice, "o1", "pm")
	if err != nil || len(g.DocAccess) != 2 || g.DocAccess["PRJ-2:docs"] != "write" || g.DocAccess["docs"] != "read" {
		t.Fatal("非成員:拿掉 PRJ-1 的 key", g, err)
	}
	g, err = e.svc.GetAgent(e.bob, "o1", "pm")
	if err != nil || len(g.DocAccess) != 3 {
		t.Fatal("專案成員看得到", g, err)
	}
	g, err = e.svc.GetAgent(e.pm, "o1", "pm")
	if err != nil || len(g.DocAccess) != 3 {
		t.Fatal("角色看自己的不篩", g, err)
	}
	// 篩選不能弄壞儲存的資料
	st, _ := e.svc.GetAgent(e.boss, "o1", "pm")
	if len(st.DocAccess) != 3 {
		t.Fatal("儲存的 docAccess 不受影響", st)
	}
}

// 刪除專案:成員跟著刪;專案管理員不能刪(只有組織管理員)
func TestProjectDeleteDropsMembers(t *testing.T) {
	e := newREnv(t)
	if err := e.svc.DeleteProject(e.carol, "o1", "PRJ-1"); service.Code(err) != 403 {
		t.Fatal("專案管理員不能刪專案", err)
	}
	p, err := e.svc.CreateProject(e.boss, "o1", service.ProjectInput{Name: "暫時", Key: "TMP", Restricted: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetProjectMember(e.boss, "o1", p.ID, service.ProjectMemberInput{Actor: "alice@x"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteProject(e.boss, "o1", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetProject(e.alice, "o1", p.ID); service.Code(err) != 404 {
		t.Fatal(err)
	}
	if l, err := e.svc.ListProjectMembers(e.boss, "o1", "PRJ-1"); err != nil || len(l) != 3 {
		t.Fatal("別的專案的成員不受影響", l, err)
	}
	_ = fmt.Sprint()
}

// 專案列表:MyRole 與受限旗標
func TestProjectListMyRole(t *testing.T) {
	e := newREnv(t)
	role := func(p *model.Principal, id string) string {
		l, err := e.svc.ListProjects(p, "o1")
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range l {
			if x.ID == id {
				return x.MyRole + "/" + fmt.Sprint(x.Restricted)
			}
		}
		return "-"
	}
	for _, c := range []struct {
		p       *model.Principal
		sec, ap string
	}{
		{e.boss, "admin/true", "admin/false"}, {e.carol, "admin/true", "/false"}, {e.bob, "member/true", "/false"}, {e.alice, "-", "/false"},
	} {
		if got := role(c.p, "PRJ-1"); got != c.sec {
			t.Fatal("SEC", c.p.User.Email, got, c.sec)
		}
		if got := role(c.p, "PRJ-2"); got != c.ap {
			t.Fatal("APP", c.p.User.Email, got, c.ap)
		}
	}
}

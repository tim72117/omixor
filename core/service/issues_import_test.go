package service_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// Issue 匯入、匯出(規格:雲端 omixor DOC-10「服務」一節):id 是 UUID、key 是顯示編號、專案各自編號、垃圾桶、別名表

// impEnv 組織 o1 加上專案 PRJ-1(代號 APP)、PRJ-2(沒有代號);回傳 service、伺服器管理員、組織管理員
func impEnv(t *testing.T) (*service.Service, store.Store, *model.Principal, *model.Principal) {
	t.Helper()
	st := storetestPG(t)
	svc, root, boss, _, _ := issueEnv(t, st)
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "App", Key: "APP"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(boss, "o1", service.ProjectInput{Name: "無代號"}); err != nil {
		t.Fatal(err)
	}
	return svc, st, root, boss
}

func storetestPG(t *testing.T) store.Store {
	t.Helper()
	return issueStores(t)["pg"]
}

func byKey(t *testing.T, l []model.Issue) map[string]model.Issue {
	t.Helper()
	m := map[string]model.Issue{}
	for _, i := range l {
		if _, dup := m[i.Key]; dup {
			t.Fatalf("key 重複 %s", i.Key)
		}
		m[i.Key] = i
	}
	return m
}

func allIssues(t *testing.T, svc *service.Service, p *model.Principal, org string) []model.Issue {
	t.Helper()
	l, err := svc.ExportIssuesWith(p, org, true)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func isV7(id string) bool { return model.IsUUID(id) && id[14] == '7' }

// 只有 key、沒有 id:發新的 UUIDv7、key 照給的(小寫會轉大寫)、對應範圍的序號推進,之後新建的接在後面
func TestImportKeyOnly(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	list := []model.Issue{
		{Key: "ISS-7", Title: "七"},
		{Key: "iss-5", Title: "五"},
		{Key: "app-3", Project: "prj-1", Title: "APP 三"},
	}
	r, err := svc.ImportIssues(boss, "o1", list)
	if err != nil || r.Inserted != 3 {
		t.Fatal(r, err)
	}
	m := byKey(t, allIssues(t, svc, root, "o1"))
	for _, k := range []string{"ISS-7", "ISS-5", "APP-3"} {
		i, ok := m[k]
		if !ok || !isV7(i.ID) || i.Version != 1 {
			t.Fatalf("%s:%+v", k, i)
		}
	}
	if m["ISS-7"].Num != 7 || m["APP-3"].Num != 3 || m["APP-3"].Project != "PRJ-1" || m["ISS-7"].Project != "" {
		t.Fatalf("號碼與專案:%+v", m)
	}
	// 之後新建:各範圍接在最大號碼後面
	a, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "新的"})
	if err != nil || a.Key != "ISS-8" {
		t.Fatal(a, err)
	}
	b, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "新的 APP", Project: "PRJ-1"})
	if err != nil || b.Key != "APP-4" {
		t.Fatal(b, err)
	}
	// 用 key 查得到
	if g, err := svc.GetIssue(root, "o1", "iss-5"); err != nil || g.Title != "五" {
		t.Fatal(g, err)
	}
}

// 沒有 key、沒有 id(例如 Jira 來的):依 Project 欄位在對應範圍發新 key
func TestImportNoKeyNoID(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	list := []model.Issue{
		{Title: "組織一"}, {Title: "APP 一", Project: "PRJ-1"}, {Title: "組織二"}, {Title: "APP 二", Project: "PRJ-1"},
	}
	if _, err := svc.ImportIssues(boss, "o1", list); err != nil {
		t.Fatal(err)
	}
	m := byKey(t, allIssues(t, svc, root, "o1"))
	for k, title := range map[string]string{"ISS-1": "組織一", "ISS-2": "組織二", "APP-1": "APP 一", "APP-2": "APP 二"} {
		if i := m[k]; i.Title != title || !isV7(i.ID) || i.Num == 0 {
			t.Fatalf("%s:%+v", k, i)
		}
	}
	// 同樣的情形走 Merge(可重跑的匯入):沒有 id 也沒有 key 的新增,接在後面
	if _, err := svc.ImportIssuesWith(boss, "o1", []model.Issue{{Title: "再一筆"}, {Title: "APP 再一筆", Project: "PRJ-1"}}, service.IssueImportOptions{Merge: true}); err != nil {
		t.Fatal(err)
	}
	m = byKey(t, allIssues(t, svc, root, "o1"))
	if m["ISS-3"].Title != "再一筆" || m["APP-3"].Title != "APP 再一筆" {
		t.Fatalf("Merge 發號:%+v", m)
	}
}

// 有 id、有 key(自家匯出再匯入):保留兩者與版本;同組織重複匯入依 overwrite 合併或略過
func TestImportKeepsIDAndKey(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	id1, id2 := model.NewIssueID(time.Now()), model.NewIssueID(time.Now())
	list := []model.Issue{
		{ID: strings.ToUpper(id1), Key: "ISS-4", Title: "一", Version: 5},
		{ID: id2, Key: "APP-2", Project: "PRJ-1", Title: "二"},
	}
	if _, err := svc.ImportIssues(boss, "o1", list); err != nil {
		t.Fatal(err)
	}
	m := byKey(t, allIssues(t, svc, root, "o1"))
	if m["ISS-4"].ID != id1 || m["ISS-4"].Version != 5 || m["APP-2"].ID != id2 || m["APP-2"].Version != 1 {
		t.Fatalf("保留:%+v", m)
	}
	// 重跑:略過(不改現有)
	r, err := svc.ImportIssuesWith(boss, "o1", []model.Issue{{ID: id1, Key: "ISS-4", Title: "改過"}}, service.IssueImportOptions{Merge: true})
	if err != nil || r.Skipped != 1 || r.Inserted != 0 || r.Updated != 0 {
		t.Fatal(r, err)
	}
	if g, _ := svc.GetIssue(root, "o1", "ISS-4"); g.Title != "一" {
		t.Fatal("略過時不能改", g)
	}
	// overwrite:整筆取代,key、專案、號碼不動
	r, err = svc.ImportIssuesWith(boss, "o1", []model.Issue{{ID: id2, Key: "APP-2", Project: "PRJ-1", Title: "改過"}}, service.IssueImportOptions{Merge: true, Overwrite: true})
	if err != nil || r.Updated != 1 {
		t.Fatal(r, err)
	}
	if g, _ := svc.GetIssue(root, "o1", id2); g.Title != "改過" || g.Key != "APP-2" || g.Project != "PRJ-1" || g.Num != 2 {
		t.Fatal("取代:", g)
	}
	// 沒給 id 只給 key:認得出是同一筆
	r, err = svc.ImportIssuesWith(boss, "o1", []model.Issue{{Key: "iss-4", Title: "只有 key"}}, service.IssueImportOptions{Merge: true, Overwrite: true})
	if err != nil || r.Updated != 1 || r.Inserted != 0 {
		t.Fatal(r, err)
	}
	if g, _ := svc.GetIssue(root, "o1", id1); g.Title != "只有 key" || g.ID != id1 {
		t.Fatal("以 key 對到", g)
	}
}

// key 格式與專案代號驗證;錯誤整批不寫入、序號也不動
func TestImportKeyValidation(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	cases := []struct {
		name string
		list []model.Issue
		code int
		msg  string
	}{
		{"格式", []model.Issue{{Key: "ISS-x", Title: "a"}}, 400, "ISS-X"},
		{"沒有號碼", []model.Issue{{Key: "ISS", Title: "a"}}, 400, "key"},
		{"組織層級用了專案代號", []model.Issue{{Key: "APP-1", Title: "a"}}, 400, "ISS"},
		{"專案用了 ISS", []model.Issue{{Key: "ISS-1", Project: "PRJ-1", Title: "a"}}, 400, "APP"},
		{"專案代號不對", []model.Issue{{Key: "WEB-1", Project: "PRJ-1", Title: "a"}}, 400, "APP"},
		{"專案不存在", []model.Issue{{Title: "ok"}, {Title: "壞的", Project: "PRJ-9"}}, 400, "PRJ-9"},
		{"專案沒有代號", []model.Issue{{Title: "壞的", Project: "PRJ-2"}}, 400, "PRJ-2"},
		{"批次裡 key 重複", []model.Issue{{Key: "ISS-2", Title: "a"}, {Key: "iss-2", Title: "b"}}, 409, "ISS-2"},
		{"舊 id 撞到別筆的 key", []model.Issue{{ID: "ISS-3", Key: "ISS-9", Title: "a"}, {Key: "ISS-3", Title: "b"}}, 409, "ISS-3"},
	}
	for _, c := range cases {
		for _, merge := range []bool{false, true} {
			var err error
			if merge {
				_, err = svc.ImportIssuesWith(boss, "o1", c.list, service.IssueImportOptions{Merge: true})
			} else {
				_, err = svc.ImportIssues(boss, "o1", c.list)
			}
			if service.Code(err) != c.code || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("%s(merge=%v):%v", c.name, merge, err)
			}
		}
	}
	if l := allIssues(t, svc, root, "o1"); len(l) != 0 {
		t.Fatalf("失敗要整批不寫入:%+v", l)
	}
	if i, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "第一筆"}); err != nil || i.Key != "ISS-1" {
		t.Fatal("失敗不推進序號", i, err)
	}
}

// 和組織裡現有的 Issue 衝突:Merge 整批不寫入(包括前面已經處理的更新與新增)
func TestImportKeyConflictRollsBack(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	first, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "既有"})
	if err != nil {
		t.Fatal(err)
	}
	other := model.NewIssueID(time.Now())
	list := []model.Issue{
		{Key: "ISS-50", Title: "會被回滾的新增"},
		{ID: first.ID, Title: "會被回滾的更新"},
		{ID: other, Key: first.Key, Title: "id 是新的、key 是別人的"},
	}
	_, err = svc.ImportIssuesWith(boss, "o1", list, service.IssueImportOptions{Merge: true, Overwrite: true})
	if service.Code(err) != 409 || !strings.Contains(err.Error(), first.Key) {
		t.Fatal(err)
	}
	l := allIssues(t, svc, root, "o1")
	if len(l) != 1 || l[0].Title != "既有" {
		t.Fatalf("要整批不寫入:%+v", l)
	}
	if i, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "下一筆"}); err != nil || i.Key != "ISS-2" {
		t.Fatal("序號沒有被推進", i, err)
	}
	// id 和 key 各指向不同的 Issue
	second, _ := svc.GetIssue(root, "o1", "ISS-2")
	_, err = svc.ImportIssuesWith(boss, "o1", []model.Issue{{ID: first.ID, Key: second.Key, Title: "x"}}, service.IssueImportOptions{Merge: true, Overwrite: true})
	if service.Code(err) != 409 {
		t.Fatal(err)
	}
	// 嚴格模式:組織已經有 Issue
	if _, err := svc.ImportIssues(boss, "o1", []model.Issue{{Title: "x"}}); service.Code(err) != 409 {
		t.Fatal(err)
	}
}

// 專案必須存在、有代號;Merge 也一樣
func TestImportProject(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	for _, merge := range []bool{false, true} {
		list := []model.Issue{{Title: "好的"}, {Title: "有問題的", Project: "PRJ-9"}}
		var err error
		if merge {
			_, err = svc.ImportIssuesWith(boss, "o1", list, service.IssueImportOptions{Merge: true})
		} else {
			_, err = svc.ImportIssues(boss, "o1", list)
		}
		if service.Code(err) != 400 || !strings.Contains(err.Error(), "有問題的") || !strings.Contains(err.Error(), "PRJ-9") {
			t.Fatalf("要指出是哪一筆(merge=%v):%v", merge, err)
		}
	}
	if len(allIssues(t, svc, root, "o1")) != 0 {
		t.Fatal("整批不寫入")
	}
	// 專案沒有代號:不指派專案可以
	if _, err := svc.ImportIssues(boss, "o1", []model.Issue{{Title: "不指派專案"}}); err != nil {
		t.Fatal(err)
	}
}

// 舊格式:id 是 ISS-<n>(第一階段之前的匯出)→ 當 key、發新的 UUIDv7、舊 id 進別名表;其他舊 id(gh-1)發新 key、也進別名表
func TestImportLegacyIDs(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	list := []model.Issue{
		{ID: "ISS-9", Title: "九"},
		{ID: "gh-1", Title: "GitHub 一", Source: "github"},
		{ID: "ISS-2", Title: "二", Comments: []model.Comment{{Author: "tim", Text: "嗨"}}},
	}
	if _, err := svc.ImportIssues(boss, "o1", list); err != nil {
		t.Fatal(err)
	}
	m := byKey(t, allIssues(t, svc, root, "o1"))
	if m["ISS-9"].Title != "九" || m["ISS-2"].Title != "二" || !isV7(m["ISS-9"].ID) || m["ISS-9"].Num != 9 {
		t.Fatalf("舊格式當 key:%+v", m)
	}
	gh := m["ISS-10"]
	if gh.Title != "GitHub 一" || !isV7(gh.ID) {
		t.Fatalf("其他舊 id 發新 key(在推進序號之後):%+v", m)
	}
	// 舊 id 查得到
	for ref, title := range map[string]string{"iss-9": "九", "ISS-2": "二", "GH-1": "GitHub 一", "gh-1": "GitHub 一"} {
		g, err := svc.GetIssue(root, "o1", ref)
		if err != nil || g.Title != title {
			t.Fatalf("%s:%v %v", ref, g, err)
		}
	}
	if g, _ := svc.GetIssue(root, "o1", "ISS-2"); len(g.Comments) != 1 || g.Comments[0].ID == "" {
		t.Fatal("留言要保留並補 id", g.Comments)
	}
	// 重跑 Merge:舊 id 認得出是同一筆(不重複新增)
	r, err := svc.ImportIssuesWith(boss, "o1", list, service.IssueImportOptions{Merge: true})
	if err != nil || r.Inserted != 0 || r.Skipped != 3 {
		t.Fatal(r, err)
	}
	// 專案裡的舊格式 id:發專案的新 key,舊 id 進別名表
	if _, err := svc.ImportIssuesWith(boss, "o1", []model.Issue{{ID: "ISS-77", Project: "PRJ-1", Title: "專案裡的舊編號"}}, service.IssueImportOptions{Merge: true}); err != nil {
		t.Fatal(err)
	}
	if g, err := svc.GetIssue(root, "o1", "ISS-77"); err != nil || g.Key != "APP-1" || g.Project != "PRJ-1" {
		t.Fatal(g, err)
	}
}

// 版本:匯入的 version 大於 0 保留(備份還原時並行修改的檢查延續),沒給(或負數)是 1
func TestImportVersion(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	if _, err := svc.ImportIssues(boss, "o1", []model.Issue{{Title: "a", Version: 9}, {Title: "b"}, {Title: "c", Version: -3}}); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, i := range allIssues(t, svc, root, "o1") {
		got[i.Title] = i.Version
	}
	if got["a"] != 9 || got["b"] != 1 || got["c"] != 1 {
		t.Fatal(got)
	}
}

// 匯出帶 project、key、number、version、deletedAt/deletedBy(withDeleted 時);欄位名稱和 model 一致
func TestExportFields(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	a, _ := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "專案的", Project: "PRJ-1"})
	b, _ := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "要刪的"})
	if err := svc.DeleteIssue(boss, "o1", b.ID); err != nil {
		t.Fatal(err)
	}
	live, err := svc.ExportIssuesWith(boss, "o1", false)
	if err != nil || len(live) != 1 || live[0].ID != a.ID {
		t.Fatal("預設不含垃圾桶", live, err)
	}
	if _, err := svc.ExportIssuesWith(alice0(t, svc), "o1", true); service.Code(err) != 403 {
		t.Fatal("成員不能匯出", err)
	}
	all := allIssues(t, svc, root, "o1")
	if len(all) != 2 {
		t.Fatal(all)
	}
	raw, _ := json.Marshal(all)
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		for _, f := range []string{"id", "key", "project", "number", "version"} {
			if _, ok := r[f]; !ok {
				t.Fatalf("匯出少了 %s:%v", f, r)
			}
		}
		del := r["key"] == b.Key
		if _, ok := r["deletedAt"]; ok != del {
			t.Fatalf("deletedAt 只有垃圾桶裡的有:%v", r)
		}
		if del && r["deletedBy"] == "" {
			t.Fatalf("deletedBy:%v", r)
		}
	}
	byk := byKey(t, all)
	if byk[a.Key].Project != "PRJ-1" || byk[a.Key].Num != 1 || byk[a.Key].Version != 1 || byk[b.Key].DeletedAt == nil {
		t.Fatalf("%+v", byk)
	}
}

func alice0(t *testing.T, svc *service.Service) *model.Principal {
	t.Helper()
	return &model.Principal{User: model.User{ID: "u2", Email: "alice@x", Role: "member"}}
}

// 匯出(含垃圾桶)再匯入另一個組織:id、key、專案、號碼、版本、留言、垃圾桶都還原;垃圾桶不出現在列表;匯入後新建不撞號
func TestExportImportRoundTrip(t *testing.T) {
	svc, st, root, boss := impEnv(t)
	for _, in := range []service.IssueInput{
		{Title: "組織一", Labels: []string{"a"}}, {Title: "APP 一", Project: "PRJ-1", Body: "內文", Priority: "high"},
		{Title: "要進垃圾桶", Project: "PRJ-1"}, {Title: "組織二"},
	} {
		if _, err := svc.CreateIssue(boss, "o1", in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.AddIssueComment(boss, "o1", "ISS-1", service.CommentInput{Text: "留言"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteIssue(boss, "o1", "APP-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIssue(boss, "o1", "ISS-2", service.IssuePatch{Title: sp("組織二(改)")}); err != nil {
		t.Fatal(err)
	}
	src := allIssues(t, svc, root, "o1")
	if len(src) != 4 {
		t.Fatal(src)
	}
	// 目標組織:同樣的專案與代號
	if err := st.CreateOrg(model.Org{ID: "o2", Name: "組織二", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(root, "o2", service.ProjectInput{Name: "App", Key: "APP"}); err != nil {
		t.Fatal(err)
	}
	// 經過 JSON(等於寫檔再讀)
	raw, _ := json.Marshal(src)
	var in []model.Issue
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	r, err := svc.ImportIssues(root, "o2", in)
	if err != nil || r.Inserted != 4 {
		t.Fatal(r, err)
	}
	dst := byKey(t, allIssues(t, svc, root, "o2"))
	for _, s := range src {
		d, ok := dst[s.Key]
		if !ok {
			t.Fatalf("少了 %s", s.Key)
		}
		if d.ID != s.ID || d.Project != s.Project || d.Num != s.Num || d.Version != s.Version || d.Title != s.Title ||
			d.Body != s.Body || d.Priority != s.Priority || len(d.Comments) != len(s.Comments) ||
			(d.DeletedAt == nil) != (s.DeletedAt == nil) || d.DeletedBy != s.DeletedBy {
			t.Fatalf("不一致:\n%+v\n%+v", s, d)
		}
	}
	// 垃圾桶不出現在列表,出現在垃圾桶列表,而且可以還原
	live, _ := svc.ListIssues(root, "o2")
	if len(live) != 3 {
		t.Fatalf("列表不含垃圾桶:%+v", live)
	}
	bin, _ := svc.ListDeletedIssues(root, "o2")
	if len(bin) != 1 || bin[0].Key != "APP-2" {
		t.Fatalf("垃圾桶:%+v", bin)
	}
	if g, err := svc.RestoreIssue(root, "o2", "APP-2"); err != nil || g.DeletedAt != nil {
		t.Fatal("還原", g, err)
	}
	// 新建不撞號,key 也都查得到
	a, err := svc.CreateIssue(root, "o2", service.IssueInput{Title: "新的"})
	if err != nil || a.Key != "ISS-3" {
		t.Fatal(a, err)
	}
	b, err := svc.CreateIssue(root, "o2", service.IssueInput{Title: "新的 APP", Project: "PRJ-1"})
	if err != nil || b.Key != "APP-3" {
		t.Fatal(b, err)
	}
	for _, s := range src {
		if g, err := svc.GetIssue(root, "o2", s.Key); err != nil || g.ID != s.ID {
			t.Fatal("用 key 查", s.Key, g, err)
		}
	}
	// 同一份再匯入(Merge):全部略過,不重複
	rr, err := svc.ImportIssuesWith(root, "o2", in, service.IssueImportOptions{Merge: true})
	if err != nil || rr.Inserted != 0 || rr.Skipped != 4 {
		t.Fatal(rr, err)
	}
	// 專案還在、代號不同的組織:拒絕
	if err := st.CreateOrg(model.Org{ID: "o3", Name: "組織三", CreatedAt: model.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(root, "o3", service.ProjectInput{Name: "App", Key: "WEB"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportIssues(root, "o3", in); service.Code(err) != 400 {
		t.Fatal("代號不同要擋", err)
	}
	if len(allIssues(t, svc, root, "o3")) != 0 {
		t.Fatal("整批不寫入")
	}
}

// 垃圾桶裡的 Issue 匯入後仍佔著 key 與號碼:匯入後新建不撞號;Merge 的 overwrite 不動垃圾桶裡的
func TestImportTrashKeepsNumber(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	now := model.Now()
	list := []model.Issue{{Key: "ISS-3", Title: "垃圾桶", DeletedAt: &now, DeletedBy: "boss@x"}, {Key: "ISS-1", Title: "正常"}}
	if _, err := svc.ImportIssues(boss, "o1", list); err != nil {
		t.Fatal(err)
	}
	live, _ := svc.ListIssues(root, "o1")
	if len(live) != 1 || live[0].Key != "ISS-1" {
		t.Fatal(live)
	}
	if i, err := svc.CreateIssue(boss, "o1", service.IssueInput{Title: "新的"}); err != nil || i.Key != "ISS-4" {
		t.Fatal(i, err)
	}
	r, err := svc.ImportIssuesWith(boss, "o1", []model.Issue{{Key: "ISS-3", Title: "想蓋過"}}, service.IssueImportOptions{Merge: true, Overwrite: true})
	if err != nil || r.Updated != 0 || r.Skipped != 1 {
		t.Fatal(r, err)
	}
	// 沒有 deletedAt 的 deletedBy 會被丟掉
	if _, err := svc.ImportIssuesWith(boss, "o1", []model.Issue{{Key: "ISS-20", Title: "x", DeletedBy: "誰"}}, service.IssueImportOptions{Merge: true}); err != nil {
		t.Fatal(err)
	}
	if g, _ := svc.GetIssue(root, "o1", "ISS-20"); g.DeletedBy != "" || g.DeletedAt != nil {
		t.Fatal(g)
	}
}

// 並發:多批匯入(有 key、沒有 key)與並發建立同時進行,key、號碼都不重複、沒有死鎖
func TestImportConcurrentWithCreate(t *testing.T) {
	svc, _, root, boss := impEnv(t)
	const imports, creates, per = 6, 24, 5
	var wg sync.WaitGroup
	errs := make(chan error, imports+creates)
	for g := range imports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var list []model.Issue
			for k := range per {
				if g%2 == 0 { // 有 key:每批一段不重疊的號碼
					list = append(list, model.Issue{Key: fmt.Sprintf("ISS-%d", 1000+g*100+k), Title: fmt.Sprintf("匯入 %d-%d", g, k)})
					list = append(list, model.Issue{Key: fmt.Sprintf("APP-%d", 1000+g*100+k), Project: "PRJ-1", Title: fmt.Sprintf("匯入 APP %d-%d", g, k)})
				} else { // 沒有 key:伺服器發
					list = append(list, model.Issue{Title: fmt.Sprintf("匯入 %d-%d", g, k)})
					list = append(list, model.Issue{Project: "PRJ-1", Title: fmt.Sprintf("匯入 APP %d-%d", g, k)})
				}
			}
			_, err := svc.ImportIssuesWith(boss, "o1", list, service.IssueImportOptions{Merge: true})
			errs <- err
		}()
	}
	for k := range creates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := service.IssueInput{Title: fmt.Sprintf("建立 %d", k)}
			if k%2 == 1 {
				in.Project = "PRJ-1"
			}
			_, err := svc.CreateIssue(boss, "o1", in)
			errs <- err
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("逾時:疑似死鎖")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	l := allIssues(t, svc, root, "o1")
	if len(l) != imports*per*2+creates {
		t.Fatalf("筆數 %d", len(l))
	}
	keys, nums, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, i := range l {
		n := fmt.Sprintf("%s/%d", i.Project, i.Num)
		if keys[i.Key] || nums[n] || ids[i.ID] || i.Num != model.IssueKeyNumber(i.Key) {
			t.Fatalf("重複或不一致:%+v", i)
		}
		keys[i.Key], nums[n], ids[i.ID] = true, true, true
	}
}

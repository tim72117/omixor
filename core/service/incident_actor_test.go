package service_test

import (
	"sync/atomic"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// incidentCountStore 數批次查名稱的次數(證明列表不是每筆事件、每則時間軸各查一次)
type incidentCountStore struct {
	store.Store
	byIDs *int32
}

func (c incidentCountStore) ActorsByIDs(ids []string) (map[string]model.ActorRecord, error) {
	atomic.AddInt32(c.byIDs, 1)
	return c.Store.ActorsByIDs(ids)
}

// 宣告:created_by 與 commander 存 actor id,輸出相容字串;commander 用 email、handle、actor id、agent: 前綴都能解析,找不到 400
func TestIncidentCommanderInputs(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	bobRec, _ := st.UserByID("u3")
	cto, _ := st.GetAgent("o1", "cto")
	for _, c := range []struct{ in, want string }{
		{"bob@x", "bob@x"}, {"BOB@X", "bob@x"}, {"@bob@x", "bob@x"}, {"user:bob@x", "bob@x"}, {bobRec.ActorID, "bob@x"},
		{"cto", "agent:cto"}, {"@cto", "agent:cto"}, {"agent:cto", "agent:cto"}, {cto.ActorID, "agent:cto"},
		{"", "alice@x"},
	} {
		inc, err := svc.DeclareIncident(alice, "o1", service.IncidentInput{Title: "x", Sev: 3, Commander: c.in})
		if err != nil || inc.Commander != c.want || inc.CreatedBy != "alice@x" {
			t.Fatalf("commander %q:%+v %v", c.in, inc, err)
		}
		raw, _ := st.GetIncident("o1", inc.ID)
		if !model.ValidActorID(raw.Commander) || !model.ValidActorID(raw.CreatedBy) {
			t.Fatalf("store 裡要是 actor id:%+v", raw)
		}
	}
	for _, bad := range []string{"nobody@x", "ghost", "agent:ghost", "usr_00000000-0000-7000-8000-000000000000", "sys_omixor"} {
		if _, err := svc.DeclareIncident(alice, "o1", service.IncidentInput{Title: "x", Sev: 3, Commander: bad}); code(err) != 400 {
			t.Fatalf("commander %q 找不到(或不是人、角色)要 400:%v", bad, err)
		}
	}
	// 更新指揮官:同樣嚴格
	inc, _ := svc.DeclareIncident(boss, "o1", service.IncidentInput{Title: "y", Sev: 2})
	u, err := svc.UpdateIncident(boss, "o1", inc.ID, service.IncidentPatch{Commander: sp("alice@x")})
	if err != nil || u.Commander != "alice@x" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := svc.UpdateIncident(boss, "o1", inc.ID, service.IncidentPatch{Commander: sp("nobody@x")}); code(err) != 400 {
		t.Fatal("更新指揮官找不到要 400", err)
	}
	// 指揮官索引:(org, commander) 查得到
	l, _ := svc.ListIncidents(boss, "o1")
	n := 0
	for _, i := range l {
		if i.Commander == "alice@x" {
			n++
		}
	}
	if n != 2 { // commander 空白的那筆(預設是宣告者 alice)與更新指定的那筆
		t.Fatal("alice 是 2 筆事件的指揮官", n)
	}
}

// 時間軸:使用者、角色、系統三種 actor 往返,輸出舊的 {kind, id};管理員代署名只能是角色或系統(D9);已刪角色的事件仍顯示
func TestIncidentTimelineActorsRoundTrip(t *testing.T) {
	svc, st, boss, alice, _ := chainEnv(t)
	_ = st.SaveUser(model.User{ID: "root", Email: "root@x", Role: "admin"})
	root := &model.Principal{User: model.User{ID: "root", Email: "root@x", Role: "admin"}}
	inc, err := svc.DeclareIncident(alice, "o1", service.IncidentInput{Title: "t", Sev: 2})
	if err != nil {
		t.Fatal(err)
	}
	// 角色憑證、一般成員、系統事件
	if _, err := svc.UpdateIncident(agentP("pm"), "o1", inc.ID, service.IncidentPatch{Note: sp("角色註記")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIncident(alice, "o1", inc.ID, service.IncidentPatch{Events: []service.IncidentEventInput{{Kind: "deploy", Text: "系統", Actor: &model.IncidentActor{Kind: "system"}}}}); err != nil {
		t.Fatal(err)
	}
	// 管理員代角色署名;代別的使用者、找不到的名字:相容模式靜默當自己(D9 的相容修正),不會變成別人的署名
	if _, err := svc.UpdateIncident(root, "o1", inc.ID, service.IncidentPatch{Note: sp("代角色"), Author: "agent:cto"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateIncident(root, "o1", inc.ID, service.IncidentPatch{Note: sp("冒名"), Author: "boss@x"}); err != nil {
		t.Fatal("管理員給別的使用者:相容模式當自己", err)
	}
	if _, err := svc.UpdateIncident(root, "o1", inc.ID, service.IncidentPatch{Note: sp("不存在"), Actor: &model.IncidentActor{Kind: "ai", ID: "ghost"}}); err != nil {
		t.Fatal("管理員署名找不到:相容模式當自己", err)
	}
	// 一般成員給別人的名字:靜默當自己
	if _, err := svc.UpdateIncident(alice, "o1", inc.ID, service.IncidentPatch{Note: sp("自己"), Author: "boss@x", Actor: &model.IncidentActor{Kind: "ai", ID: "cto"}}); err != nil {
		t.Fatal(err)
	}
	want := []model.IncidentActor{model.PersonActor("alice@x"), model.AIActor("pm"), model.SystemActor(), model.AIActor("cto"), model.PersonActor("root@x"), model.PersonActor("root@x"), model.PersonActor("alice@x")}
	check := func(stage string) {
		t.Helper()
		g, err := svc.GetIncident(boss, "o1", inc.ID)
		if err != nil || len(g.Timeline) != len(want) {
			t.Fatalf("%s:%+v %v", stage, g, err)
		}
		for k, w := range want {
			if g.Timeline[k].Actor != w {
				t.Fatalf("%s:時間軸 %d 要是 %+v,實際 %+v", stage, k, w, g.Timeline[k].Actor)
			}
		}
		if raw, _ := st.GetIncident("o1", inc.ID); raw.Timeline[1].ActorID == "" || raw.Timeline[1].Actor != (model.IncidentActor{}) {
			t.Fatalf("store 裡存 actor id:%+v", raw.Timeline[1])
		}
	}
	check("寫入後")
	// 角色被刪(退役):事件仍輸出 ai/<handle>
	if err := svc.DeleteAgent(boss, "o1", "pm"); err != nil {
		t.Fatal(err)
	}
	check("角色刪除後")
	if l, _ := svc.ListIncidents(boss, "o1"); l[0].Timeline[1].Actor != model.AIActor("pm") {
		t.Fatal("列表也要顯示已刪角色", l[0].Timeline[1])
	}
}

// 列表輸出批次補名字:不隨事件數與時間軸長度增加查詢次數
func TestIncidentListNoNPlusOne(t *testing.T) {
	_, st, boss, alice, _ := chainEnv(t)
	var calls int32
	cs := &service.Service{Store: incidentCountStore{Store: st, byIDs: &calls}}
	for k := 0; k < 6; k++ {
		inc, err := cs.DeclareIncident(alice, "o1", service.IncidentInput{Title: "t", Sev: 2, Commander: "bob@x"})
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			if _, err := cs.UpdateIncident(boss, "o1", inc.ID, service.IncidentPatch{Note: sp("n")}); err != nil {
				t.Fatal(err)
			}
		}
	}
	atomic.StoreInt32(&calls, 0)
	l, err := cs.ListIncidents(alice, "o1")
	if err != nil || len(l) != 6 || len(l[0].Timeline) < 4 {
		t.Fatal(l, err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("列表只能批次查名稱 1 次,實際 %d", n)
	}
}

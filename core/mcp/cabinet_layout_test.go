package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	"omixor-core/backend"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
	"omixor-core/storetest"
)

func TestCabinetLayoutSave(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.SaveUser(model.User{ID: "u2", Email: "amy@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u2", Email: "amy@x", Role: "member", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	amy := &model.Principal{User: model.User{ID: "u2", Email: "amy@x", Role: "member"}}
	ep := func(p *model.Principal) http.Handler {
		return mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(r *http.Request) (backend.Backend, string, error) {
			return &backend.Local{Svc: svc, P: p}, "lab", nil
		}})
	}
	admin := ep(boss)
	secs := func(out map[string]any) []any { return out["sections"].([]any) }

	// 還沒存過結構(空白結構:總覽、文件):新增區塊與頁籤,id 自動編;頁籤可以掛在這次新增的區塊
	out := ok(t, admin, "", "cabinet_layout_save", map[string]any{
		"add_sections": []any{map[string]any{"label": "課程"}, map[string]any{"label": "行銷", "id": "marketing"}},
		"add_tabs": []any{
			map[string]any{"section": "課程", "label": "AI 工具介紹", "code": "L0"},
			map[string]any{"section": "課程", "label": "零基礎"},
			map[string]any{"section": "marketing", "label": "FB", "path": "fb"},
		},
	})
	if s := secs(out); len(s) != 4 || s[2].(map[string]any)["section"] != "s3" || len(s[2].(map[string]any)["tabs"].([]any)) != 2 {
		t.Fatalf("結構:%v", out)
	}
	added := out["added"].([]any)
	if len(added) != 5 || added[2].(map[string]any)["tab"] != "/s3/1" || added[2].(map[string]any)["label"] != "L0 AI 工具介紹" ||
		added[3].(map[string]any)["tab"] != "/s3/2" || added[4].(map[string]any)["tab"] != "/fb" {
		t.Fatalf("added:%v", added)
	}

	// 新的位置馬上能放文件
	ok(t, admin, "", "doc_save", map[string]any{"title": "腳本", "tab": "L0"})

	// 改名:區塊與頁籤(代號清掉)
	ok(t, admin, "", "cabinet_layout_save", map[string]any{"update": []any{
		map[string]any{"section": "課程", "label": "課程規劃"},
		map[string]any{"tab": "L0", "label": "AI 工具", "code": ""},
	}})
	l, _ := getLayoutOnly(st)
	if sec := model.Layout(l).Section("s3"); sec == nil || sec.Label != "課程規劃" || sec.Tabs[0].Label != "AI 工具" || sec.Tabs[0].Code != nil {
		t.Fatalf("改名:%+v", l)
	}

	// 錯誤:重複名稱、找不到、還有文件的位置不能移除(整批都不存)
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"add_sections": []any{map[string]any{"label": "行銷"}}}, "Section \"行銷\" already exists")
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"add_tabs": []any{map[string]any{"section": "nope", "label": "x"}}}, "Section \"nope\" not found")
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"add_tabs": []any{map[string]any{"section": "行銷", "label": "IG"}}, "remove": []any{"AI 工具"}},
		"「腳本」還在被拿掉的頁籤裡,先搬走或刪除")
	if l, _ := getLayoutOnly(st); len(model.Layout(l).Section("marketing").Tabs) != 1 {
		t.Fatal("失敗時不能存一半")
	}
	fails(t, admin, "", "cabinet_layout_save", map[string]any{}, "Give at least one of add_sections, add_tabs, update or remove")

	// 移除:頁籤、區塊(連同頁籤)
	out = ok(t, admin, "", "cabinet_layout_save", map[string]any{"remove": []any{"零基礎", "行銷"}})
	if s := secs(out); len(s) != 3 || len(s[2].(map[string]any)["tabs"].([]any)) != 1 {
		t.Fatalf("移除:%v", out)
	}

	// 對不準的時候不猜:區塊限定找不到、名稱對到多個頁籤、區塊和頁籤同名,都要報錯而不是改到別的
	ok(t, admin, "", "cabinet_layout_save", map[string]any{
		"add_sections": []any{map[string]any{"label": "行銷", "id": "mk"}},
		"add_tabs":     []any{map[string]any{"section": "mk", "label": "AI 工具"}, map[string]any{"section": "mk", "label": "Y", "code": "K1"}},
	})
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"update": []any{map[string]any{"section": "nope", "tab": "AI 工具", "label": "x"}}}, "Section \"nope\" not found")
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"update": []any{map[string]any{"tab": "AI 工具", "label": "x"}}}, "\"AI 工具\" matches several tabs (/s3/1, /mk/1); specify the tab by its path")
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"remove": []any{"AI 工具"}}, "\"AI 工具\" matches several tabs (/s3/1, /mk/1); specify the tab by its path")
	ok(t, admin, "", "cabinet_layout_save", map[string]any{"update": []any{map[string]any{"section": "mk", "tab": "AI 工具", "label": "AI"}}})
	l, _ = getLayoutOnly(st)
	if model.Layout(l).TabByPath("/mk/1").Label != "AI" || model.Layout(l).TabByPath("/s3/1").Label != "AI 工具" {
		t.Fatalf("限定區塊改名:%+v", l)
	}
	// 代號整個文件櫃不能重複;名稱同一個區塊不能重複
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"add_tabs": []any{map[string]any{"section": "課程規劃", "label": "Z", "code": "K1"}}}, "Tab code \"K1\" is already used by \"行銷 › K1 Y\"")
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"update": []any{map[string]any{"tab": "/s3/1", "code": "K1"}}}, "Tab code \"K1\" is already used by \"行銷 › K1 Y\"")
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"update": []any{map[string]any{"tab": "/mk/1", "label": "Y"}}}, "\"行銷\" already has a tab \"Y\"")
	ok(t, admin, "", "cabinet_layout_save", map[string]any{"update": []any{map[string]any{"tab": "K1", "label": "Y2", "code": "K1"}}}) // 自己的代號不算重複
	// 區塊名稱剛好也是頁籤名稱:要用區塊 id 或頁籤網址
	ok(t, admin, "", "cabinet_layout_save", map[string]any{"add_sections": []any{map[string]any{"label": "AI", "id": "ai"}}})
	fails(t, admin, "", "cabinet_layout_save", map[string]any{"remove": []any{"AI"}}, "\"AI\" is both a section name and a tab name (/mk/1): refer to the section by id \"ai\" and to the tab by its path")
	out = ok(t, admin, "", "cabinet_layout_save", map[string]any{"remove": []any{"ai", "/mk/1"}})
	if l, _ := getLayoutOnly(st); model.Layout(l).Section("ai") != nil || model.Layout(l).TabByPath("/mk/1") != nil || len(model.Layout(l).Section("mk").Tabs) != 1 {
		t.Fatalf("用 id 與網址移除:%+v", l)
	}

	// 一般成員不能改結構
	if _, e := call(t, ep(amy), "", "cabinet_layout_save", map[string]any{"add_sections": []any{map[string]any{"label": "x"}}}); e == "" {
		t.Fatal("一般成員不能改結構")
	}
}

func getLayoutOnly(st store.Store) ([]model.CabinetSection, error) {
	l, _, err := st.GetCabinetLayout("lab", "")
	return l, err
}

// 樂觀鎖:cabinet 回傳 layoutVersion;cabinet_layout_save 帶 expectedVersion,版本不符拒絕(不覆蓋);不帶就用這次呼叫開頭讀到的版本
func TestCabinetLayoutSaveVersion(t *testing.T) {
	st := storetest.New(t)
	svc := &service.Service{Store: st}
	now := model.Now()
	_ = st.SaveUser(model.User{ID: "u1", Email: "boss@x", Role: "member", CreatedAt: now})
	_ = st.CreateOrg(model.Org{ID: "lab", Name: "實驗室", CreatedAt: now})
	_ = st.SaveMember(model.Member{Org: "lab", UserID: "u1", Email: "boss@x", Role: "admin", AddedAt: now})
	boss := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "member"}}
	admin := mcp.AdminEndpoint(mcp.AdminEndpointConfig{Resolve: func(r *http.Request) (backend.Backend, string, error) {
		return &backend.Local{Svc: svc, P: boss}, "lab", nil
	}})
	ver := func(out map[string]any) int {
		t.Helper()
		v, ok := out["layoutVersion"].(float64)
		if !ok {
			t.Fatalf("要有 layoutVersion:%v", out)
		}
		return int(v)
	}
	if v := ver(ok(t, admin, "", "cabinet", nil)); v != 0 {
		t.Fatal("沒存過是 0", v)
	}
	add := func(label string, extra map[string]any) map[string]any {
		args := map[string]any{"add_sections": []any{map[string]any{"label": label}}}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	if v := ver(ok(t, admin, "", "cabinet_layout_save", add("甲", map[string]any{"expectedVersion": 0}))); v != 1 {
		t.Fatal("第一次存檔後 1", v)
	}
	// 沒帶 expectedVersion:向下相容,照常存
	if v := ver(ok(t, admin, "", "cabinet_layout_save", add("乙", nil))); v != 2 {
		t.Fatal("不帶也能存", v)
	}
	// 帶舊版本:拒絕,結構不變
	_, e := call(t, admin, "", "cabinet_layout_save", add("丙", map[string]any{"expectedVersion": 1}))
	if !strings.Contains(e, "結構已被別人更新") || !strings.Contains(e, "目前版本 2") {
		t.Fatalf("要擋下並說明:%q", e)
	}
	if l, _ := getLayoutOnly(st); model.Layout(l).Section("丙") != nil {
		t.Fatal("被擋的存檔不能寫入")
	}
	// 帶目前的版本:成功
	if v := ver(ok(t, admin, "", "cabinet_layout_save", add("丁", map[string]any{"expectedVersion": 2}))); v != 3 {
		t.Fatal(v)
	}
	if v := ver(ok(t, admin, "", "cabinet", nil)); v != 3 {
		t.Fatal(v)
	}
}

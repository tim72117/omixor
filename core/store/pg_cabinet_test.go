package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"omixor-core/model"
)

// cabPG 開一個空的 PG,並先建好測試用的組織(o、m、e、bad、keep;外鍵:資料的組織要存在)與每個組織的第一個專案(PRJ-1,bad 除外)
func cabPG(t *testing.T) *PG {
	t.Helper()
	p := testPG(t).(*PG)
	at := model.Now()
	for _, org := range []string{"o", "m", "e", "bad", "keep"} {
		if err := p.CreateOrg(model.Org{ID: org, Name: org, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if org == "bad" {
			continue
		}
		if _, err := p.CreateProject(org, model.Project{Name: "p", CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func sp(s string) *string { return &s }

func sampleLayout() []model.CabinetSection {
	return []model.CabinetSection{
		{ID: "course", Label: "課程", Path: "/course", Tabs: []model.CabinetTab{
			{Code: sp("L0"), Group: sp("基礎"), Label: "AI 工具介紹", Path: "/course/l0"},
			{Label: "沒代號", Path: "/course/none"},
			{Code: sp("L1"), Label: "只有代號", Path: "/course/l1"},
		}},
		{ID: "empty", Label: "空區塊", Tabs: []model.CabinetTab{}},
		{ID: "last", Label: "最後", Tabs: []model.CabinetTab{{Group: sp("g"), Label: "頁", Path: "/last/a"}}},
	}
}

func count(t *testing.T, p *PG, q string, args ...any) int {
	t.Helper()
	var n int
	if err := p.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPGCabinetRoundTrip(t *testing.T) {
	p := cabPG(t)
	want := sampleLayout()
	if err := saveLay(p, "o", "", want); err != nil {
		t.Fatal(err)
	}
	got, err := getLay(p, "o", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("往返不同\n got %+v\nwant %+v", got, want)
	}
	if got[1].Tabs == nil {
		t.Fatal("空 tabs 要是空陣列不是 nil")
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_sections where org = 'o'`); n != 3 {
		t.Fatal("區塊列數", n)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_tabs where org = 'o'`); n != 4 {
		t.Fatal("頁籤列數", n)
	}
}

func TestPGCabinetReplaceLeavesNoStale(t *testing.T) {
	p := cabPG(t)
	if err := saveLay(p, "o", "", sampleLayout()); err != nil {
		t.Fatal(err)
	}
	next := []model.CabinetSection{
		{ID: "last", Label: "改名了", Tabs: []model.CabinetTab{
			{Label: "新頁", Path: "/last/new"},
			{Code: sp("L0"), Label: "搬過來", Path: "/course/l0"}, // 頁籤換區塊
		}},
		{ID: "course", Label: "課程", Tabs: []model.CabinetTab{}}, // 換順序、拿掉頁籤
	}
	if err := saveLay(p, "o", "", next); err != nil {
		t.Fatal(err)
	}
	got, err := getLay(p, "o", "")
	if err != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("got %+v err %v", got, err)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_sections where org = 'o'`); n != 2 {
		t.Fatal("區塊有殘留", n)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_tabs where org = 'o'`); n != 2 {
		t.Fatal("頁籤有殘留", n)
	}
	// 重複的區塊 id 或頁籤網址要拒絕,舊結構不動
	bad := []model.CabinetSection{{ID: "a", Label: "a", Tabs: []model.CabinetTab{{Label: "x", Path: "/x"}, {Label: "y", Path: "/x"}}}}
	if err := saveLay(p, "o", "", bad); err == nil {
		t.Fatal("重複的頁籤網址要出錯")
	}
	if g, _ := getLay(p, "o", ""); !reflect.DeepEqual(g, next) {
		t.Fatal("出錯後結構要維持不變", g)
	}
}

func TestPGCabinetEmptyVsNeverSaved(t *testing.T) {
	p := cabPG(t)
	if _, err := getLay(p, "o", ""); err != ErrNotFound {
		t.Fatal("沒存過要 ErrNotFound", err)
	}
	if err := saveLay(p, "o", "", nil); err != nil {
		t.Fatal(err)
	}
	g, err := getLay(p, "o", "")
	if err != nil || g == nil || len(g) != 0 {
		t.Fatal("空結構要是空陣列", g, err)
	}
	if err := saveLay(p, "o", "", sampleLayout()); err != nil {
		t.Fatal(err)
	}
	if err := saveLay(p, "o", "", []model.CabinetSection{}); err != nil {
		t.Fatal(err)
	}
	if g, err := getLay(p, "o", ""); err != nil || len(g) != 0 {
		t.Fatal("改回空結構", g, err)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_sections`) + count(t, p, `select count(*) from omixor_cabinet_tabs`); n != 0 {
		t.Fatal("空結構不該有列", n)
	}
}

func TestPGCabinetProjectIsolation(t *testing.T) {
	p := cabPG(t)
	a := sampleLayout()
	b := []model.CabinetSection{{ID: "course", Label: "專案的課程", Tabs: []model.CabinetTab{{Label: "同網址", Path: "/course/l0"}}}}
	if err := saveLay(p, "o", "", a); err != nil {
		t.Fatal(err)
	}
	if err := saveLay(p, "o", "PRJ-1", b); err != nil { // 區塊 id、頁籤網址和未分專案的相同也不衝突
		t.Fatal(err)
	}
	if g, _ := getLay(p, "o", ""); !reflect.DeepEqual(g, a) {
		t.Fatal("未分專案被動到", g)
	}
	if g, _ := getLay(p, "o", "PRJ-1"); !reflect.DeepEqual(g, b) {
		t.Fatal("專案的結構", g)
	}
	if err := saveLay(p, "o", "PRJ-1", nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := getLay(p, "o", ""); !reflect.DeepEqual(g, a) {
		t.Fatal("清空專案後未分專案被動到", g)
	}
	if _, err := getLay(p, "o", "PRJ-2"); err != ErrNotFound {
		t.Fatal(err)
	}
}

func jsonbLayout(t *testing.T, p *PG, org, project string) []model.CabinetSection {
	t.Helper()
	var b []byte
	if err := p.pool.QueryRow(context.Background(), `select layout from omixor_cabinet_layouts where org = $1 and project = $2`, org, project).Scan(&b); err != nil {
		t.Fatal(err)
	}
	l := []model.CabinetSection{}
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// 存檔不再寫舊的 layout jsonb(保持預設的 '[]'),但 split_at 要標記(讓啟動遷移不碰這一列),版本遞增
func TestPGCabinetNoLegacyWrite(t *testing.T) {
	p := cabPG(t)
	for i, l := range [][]model.CabinetSection{sampleLayout(), {}, sampleLayout()[:1]} {
		v, err := p.SaveCabinetLayout("o", "PRJ-1", l, nil)
		if err != nil || v != int64(i+1) {
			t.Fatal(v, err)
		}
		if j := jsonbLayout(t, p, "o", "PRJ-1"); len(j) != 0 {
			t.Fatalf("不該再寫 jsonb:%+v", j)
		}
		if n := count(t, p, `select count(*) from omixor_cabinet_layouts where org = 'o' and project = 'PRJ-1' and split_at is not null and version = $1`, i+1); n != 1 {
			t.Fatal("split_at 要標記、版本要遞增", n)
		}
		g, err := getLay(p, "o", "PRJ-1")
		if err != nil || !reflect.DeepEqual(g, l) {
			t.Fatalf("讀回 %+v %v", g, err)
		}
	}
}

func TestPGCabinetDeleteOrgAndProject(t *testing.T) {
	p := cabPG(t)
	for _, org := range []string{"o", "keep"} {
		if err := saveLay(p, org, "", sampleLayout()); err != nil {
			t.Fatal(err)
		}
		if err := saveLay(p, org, "PRJ-1", sampleLayout()); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.DeleteOrg("o"); err != nil {
		t.Fatal(err)
	}
	for _, tb := range []string{"omixor_cabinet_layouts", "omixor_cabinet_sections", "omixor_cabinet_tabs"} {
		if n := count(t, p, `select count(*) from `+tb+` where org = 'o'`); n != 0 {
			t.Fatal(tb, "刪除組織後還有列", n)
		}
		if n := count(t, p, `select count(*) from `+tb+` where org = 'keep'`); n == 0 {
			t.Fatal(tb, "別的組織被刪了")
		}
	}
	prj, err := p.CreateProject("keep", model.Project{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := saveLay(p, "keep", prj.ID, sampleLayout()); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteProject("keep", prj.ID); err != nil {
		t.Fatal(err)
	}
	for _, tb := range []string{"omixor_cabinet_sections", "omixor_cabinet_tabs"} {
		if n := count(t, p, `select count(*) from `+tb+` where org = 'keep' and project = $1`, prj.ID); n != 0 {
			t.Fatal(tb, "刪除專案後還有列", n)
		}
	}
}

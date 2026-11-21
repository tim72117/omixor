package store

import (
	"context"
	"encoding/json"
	"errors"
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

func TestPGCabinetMigration(t *testing.T) {
	p := cabPG(t)
	c := context.Background()
	legacy := sampleLayout()
	put := func(org, project, raw string) {
		t.Helper()
		if _, err := p.pool.Exec(c, `insert into omixor_cabinet_layouts (org, project, layout) values ($1, $2, $3::jsonb)`, org, project, raw); err != nil {
			t.Fatal(err)
		}
	}
	put("o", "", string(jb(legacy)))
	put("o", "PRJ-1", `[{"id":"s","label":"缺 tabs 的舊格式"}]`)
	put("e", "", `[]`)
	put("bad", "dup-tab", `[{"id":"a","label":"a","tabs":[{"label":"x","path":"/x"}]},{"id":"b","label":"b","tabs":[{"label":"y","path":"/x"}]}]`)
	put("bad", "dup-sec", `[{"id":"a","label":"a"},{"id":"a","label":"b"}]`)
	put("bad", "not-array", `{"id":"a"}`)

	if err := p.migrateCabinetLayouts(); err != nil {
		t.Fatal("不合格的列不能讓遷移失敗", err)
	}
	if g, v, err := p.GetCabinetLayout("o", ""); err != nil || !reflect.DeepEqual(g, legacy) || v != 0 {
		t.Fatalf("遷移後讀回 %+v v=%d %v", g, v, err)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_tabs where org = 'o' and project = ''`); n != 4 {
		t.Fatal("新表要有資料", n)
	}
	if g, err := getLay(p, "o", "PRJ-1"); err != nil || len(g) != 1 || g[0].Tabs == nil || len(g[0].Tabs) != 0 {
		t.Fatal("缺 tabs 的列", g, err)
	}
	if g, err := getLay(p, "e", ""); err != nil || g == nil || len(g) != 0 {
		t.Fatal("空陣列的列", g, err)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_layouts where org = 'e' and split_at is not null`); n != 1 {
		t.Fatal("空結構也要標記")
	}
	// 不合格的列:新表沒有資料、沒標記(修好之後重啟會再試);讀取退回舊 layout
	if n := count(t, p, `select count(*) from omixor_cabinet_sections where org = 'bad'`); n != 0 {
		t.Fatal("不合格的列不能展開", n)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_layouts where org = 'bad' and split_at is not null`); n != 0 {
		t.Fatal("不合格的列不能標記")
	}
	if g, err := getLay(p, "bad", "dup-sec"); err != nil || len(g) != 2 || g[1].Label != "b" {
		t.Fatal("展開不了的列讀取退回舊 layout(不是空結構)", g, err)
	}
	if _, err := getLay(p, "bad", "not-array"); !errors.Is(err, ErrUnmigratedRow) {
		t.Fatal("舊 layout 解不出來要回明確錯誤", err)
	}

	// 再跑一次:不重複、不覆蓋手動改過的新表資料
	if _, err := p.pool.Exec(c, `update omixor_cabinet_sections set label = '手改' where org = 'o' and project = '' and id = 'course'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(c, `delete from omixor_cabinet_tabs where org = 'o' and project = '' and path = '/last/a'`); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateCabinetLayouts(); err != nil {
		t.Fatal(err)
	}
	g, _ := getLay(p, "o", "")
	if g[0].Label != "手改" || len(g[2].Tabs) != 0 {
		t.Fatal("第二次遷移覆蓋了新表的資料", g)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_sections where org = 'o' and project = ''`); n != 3 {
		t.Fatal("重複展開", n)
	}
}

// 新版存過的列(split_at 非 NULL),就算舊的 layout jsonb 內容不同(舊資料、或回滾後被舊版改過 updated_at),遷移也不覆蓋新表
func TestPGCabinetMigrationKeepsNewTables(t *testing.T) {
	p := cabPG(t)
	c := context.Background()
	newer := []model.CabinetSection{{ID: "fromNew", Label: "新版存的", Tabs: []model.CabinetTab{}}}
	if _, err := p.SaveCabinetLayout("o", "", newer, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pool.Exec(c, `update omixor_cabinet_layouts set layout = $1, updated_at = now() + interval '1 hour' where org = 'o' and project = ''`, jb(sampleLayout())); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateCabinetLayouts(); err != nil {
		t.Fatal(err)
	}
	if g, v, err := p.GetCabinetLayout("o", ""); err != nil || !reflect.DeepEqual(g, newer) || v != 1 {
		t.Fatalf("遷移覆蓋了新版的資料 %+v v=%d %v", g, v, err)
	}
	if n := count(t, p, `select count(*) from omixor_cabinet_tabs where org = 'o'`); n != 0 {
		t.Fatal("新表不該被舊 jsonb 填入", n)
	}
	// 遷移過的舊資料(版本 0)之後存檔:版本 0 → 1,split_at 仍標記
	put := func() {
		if _, err := p.pool.Exec(c, `insert into omixor_cabinet_layouts (org, project, layout) values ('m', '', $1)`, jb(sampleLayout())); err != nil {
			t.Fatal(err)
		}
	}
	put()
	if err := p.migrateCabinetLayouts(); err != nil {
		t.Fatal(err)
	}
	if _, v, err := p.GetCabinetLayout("m", ""); err != nil || v != 0 {
		t.Fatal(v, err)
	}
	if v, err := p.SaveCabinetLayout("m", "", newer, i64(0)); err != nil || v != 1 {
		t.Fatal(v, err)
	}
	if err := p.migrateCabinetLayouts(); err != nil {
		t.Fatal(err)
	}
	if g, _ := getLay(p, "m", ""); !reflect.DeepEqual(g, newer) {
		t.Fatal("存檔後再遷移不能回到舊資料", g)
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

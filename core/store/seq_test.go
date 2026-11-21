package store

import (
	"fmt"
	"sync"
	"testing"

	"omixor-core/model"
)

// 編號刪掉後不重用:建三筆、刪最後一筆、再建,號碼要是 4;匯入帶指定號碼的資料之後也要接得上
func suiteNumberNoReuse(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	at := mt("2026-01-02T03:04:05Z")
	mkIssue := func() model.Issue {
		x, err := s.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
		chk(t, err)
		return x
	}
	mkDoc := func() model.CabinetDoc {
		x, err := s.CreateDoc("o", model.CabinetDoc{Title: "d", Section: "s", CreatedAt: at, UpdatedAt: at})
		chk(t, err)
		return x
	}
	for range 3 {
		mkIssue()
		mkDoc()
	}
	chk(t, s.DeleteIssue("o", "ISS-3"))
	chk(t, s.DeleteDoc("o", "DOC-3"))
	if x := mkIssue(); x.Key != "ISS-4" {
		t.Fatal("刪掉最大號之後 Issue 號碼不能重用:", x.ID)
	}
	if x := mkDoc(); x.ID != "DOC-4" {
		t.Fatal("刪掉最大號之後文件號碼不能重用:", x.ID)
	}
	// 連刪最後兩筆,再重新連線(序號要存在資料庫裡,不是靠現有資料)
	chk(t, s.DeleteIssue("o", "ISS-4"))
	chk(t, s.DeleteIssue("o", "ISS-2"))
	s = impl.reopen(t, s)
	if x := mkIssue(); x.Key != "ISS-5" {
		t.Fatal("重新連線後 Issue 號碼要接著發:", x.ID)
	}

	// 匯入帶指定號碼的資料:序號要跟上,刪掉最大號之後也不重用
	newOrg(t, s, "imp")
	imp := func(n int) model.Issue {
		return model.Issue{ID: fmt.Sprintf("ISS-%d", n), Title: "i", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at}
	}
	if _, err := s.ImportIssues("imp", []model.Issue{imp(7), imp(2)}); err != nil {
		t.Fatal(err)
	}
	chk(t, s.DeleteIssue("imp", "ISS-7"))
	x, err := s.CreateIssue("imp", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	if x.Key != "ISS-8" {
		t.Fatal("匯入後號碼要接在最大號之後:", x.ID)
	}

	// 刪組織後再建同名組織,號碼從頭開始
	chk(t, s.DeleteOrg("imp"))
	newOrg(t, s, "imp")
	if x, err := s.CreateIssue("imp", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at}); err != nil || x.Key != "ISS-1" {
		t.Fatal("刪組織要清掉序號:", x.ID, err)
	}

	// PG:序號表被清空(例如舊資料庫升級)之後重新連線,要從現有最大號碼接上
	if pg, ok := s.(*PG); ok {
		c, cancel := ctx()
		defer cancel()
		if _, err := pg.pool.Exec(c, `delete from omixor_seq`); err != nil {
			t.Fatal(err)
		}
		s2, err := NewPGFromPool(pg.root) // 重新跑遷移
		if err != nil {
			t.Fatal(err)
		}
		s = s2
		if x := mkIssue(); x.Key != "ISS-6" {
			t.Fatal("遷移要從現有最大號碼初始化:", x.ID)
		}
		if x := mkDoc(); x.ID != "DOC-5" { // 現有 DOC-1、2、4 → 下一個 5
			t.Fatal("遷移要從現有最大號碼初始化(文件):", x.ID)
		}
	}
}

// 同一個組織同時建 Issue:號碼不重複、不跳號
func suiteConcurrentIssueNumbers(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	at := mt("2026-01-02T03:04:05Z")
	const n = 40
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for k := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x, err := s.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
			ids[k], errs[k] = x.Key, err
		}()
	}
	wg.Wait()
	got := map[string]bool{}
	for k := range n {
		chk(t, errs[k])
		got[ids[k]] = true
	}
	for k := 1; k <= n; k++ {
		if !got[fmt.Sprintf("ISS-%d", k)] {
			t.Fatalf("號碼重複或跳號,缺 ISS-%d:%v", k, ids)
		}
	}
	if len(got) != n {
		t.Fatal("號碼重複", ids)
	}
}

// 非數字 id(gh-1 這類)與數字超過 int32 的 id 可以匯入(number = 0,不參與唯一編號),之後建立的號碼不受影響;匯入的清單裡 id 重複就整批拒絕
func suiteImportNonNumericIDs(t *testing.T, impl storeImpl) {
	s := impl.open(t)
	newOrg(t, s, "o")
	at := mt("2026-01-02T03:04:05Z")
	mk := func(id string) model.Issue {
		return model.Issue{ID: id, Title: id, Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at}
	}
	if _, err := s.ImportIssues("o", []model.Issue{mk("ISS-1"), mk("ISS-1")}); err == nil {
		t.Fatal("清單裡 id 重複要拒絕")
	}
	if l, _ := s.ListIssues("o"); len(l) != 0 {
		t.Fatal("被拒絕的匯入不能留下資料", len(l))
	}
	n, err := s.ImportIssues("o", []model.Issue{mk("gh-1"), mk("gh-2"), mk("ISS-99999999999"), mk("ISS-7")})
	chk(t, err)
	if n != 4 {
		t.Fatal("匯入筆數", n)
	}
	x, err := s.CreateIssue("o", model.Issue{Title: "t", Status: "open", Priority: "normal", CreatedBy: "sys_omixor", CreatedAt: at, UpdatedAt: at})
	chk(t, err)
	// gh-1、gh-2、ISS-99999999999 匯入時沒有可用的 key:在推進到 7 之後依序發 ISS-8、9、10
	if x.Key != "ISS-11" {
		t.Fatal("新號碼接在最大的數字號碼後面:", x.Key)
	}
}

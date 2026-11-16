package store

import (
	"testing"

	"omixor-core/model"
)

// 事件:編號從 INC-2001 開始、新的在前、整筆取代、匯入只在空的時候;File 寫入後重開還在(一致性測試的一項,見 conformance_test.go)
func suiteIncidents(t *testing.T, impl storeImpl) {
	st := impl.open(t)
	at := mt("2026-01-02T03:04:05Z")
	a, err := st.CreateIncident("o", model.Incident{Title: "a", Sev: 2, Status: "investigating", StartedAt: at})
	chk(t, err)
	b, err := st.CreateIncident("o", model.Incident{Title: "b", Sev: 1, Status: "investigating", StartedAt: at, Services: []string{"api"}})
	chk(t, err)
	if a.ID != "INC-2001" || b.ID != "INC-2002" {
		t.Fatal(a.ID, b.ID)
	}
	l, _ := st.ListIncidents("o")
	if len(l) != 2 || l[0].ID != "INC-2002" {
		t.Fatal(l)
	}
	b.Status, b.ResolvedAt, b.Impact = "resolved", mtp("2026-01-02T05:00:00Z"), "部分使用者"
	b.Timeline = []model.IncidentEvent{{ID: "e1", Date: at, Kind: "note", Text: "開始處理"}}
	if err := st.UpdateIncident("o", b); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateIncident("o", model.Incident{ID: "INC-9"}); err != ErrNotFound {
		t.Fatal(err)
	}
	_, err = st.GetIncident("o", "INC-9")
	wantNotFound(t, "沒有的事件", err)
	_, err = st.GetIncident("o", "inc-2002")
	wantNotFound(t, "事件 id 大小寫要一致", err)
	if _, err := st.ImportIncidents("o", []model.Incident{{ID: "INC-1"}}); err == nil {
		t.Fatal("已經有事件要拒絕匯入")
	}
	if n, err := st.ImportIncidents("p", []model.Incident{{ID: "INC-2041", Title: "x", StartedAt: at}, {ID: "INC-2040", Title: "y", StartedAt: at}}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if c, _ := st.CreateIncident("p", model.Incident{Title: "z", StartedAt: at}); c.ID != "INC-2042" {
		t.Fatal(c.ID)
	}
	st = impl.reopen(t, st)
	g, err := st.GetIncident("o", "INC-2002")
	chk(t, err)
	wb := b.Clone()
	wb.FollowUps = []string{}
	sameJSON(t, "UpdateIncident 後(nil 的清單讀回是 [])", g, wb)
	if l, _ := st.ListIncidents("o"); len(l) != 2 || l[0].Status != "resolved" {
		t.Fatal("重開後要讀回事件", l)
	}
	if l, _ := st.ListIncidents("p"); len(l) != 3 || l[0].ID != "INC-2042" || l[1].ID != "INC-2041" || l[2].ID != "INC-2040" {
		t.Fatal(l)
	}
}

package store

import (
	"testing"

	"omixor-core/model"
)

// 事件:編號從 INC-2001 開始、新的在前、整筆取代、匯入只在空的時候;File 寫入後重開還在
func TestIncidentsMemAndFile(t *testing.T) {
	dir := t.TempDir()
	f, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []Store{NewMem(), f} {
		a, _ := st.CreateIncident("o", model.Incident{Title: "a", Sev: 2, Status: "investigating"})
		b, _ := st.CreateIncident("o", model.Incident{Title: "b", Sev: 1, Status: "investigating"})
		if a.ID != "INC-2001" || b.ID != "INC-2002" {
			t.Fatal(a.ID, b.ID)
		}
		l, _ := st.ListIncidents("o")
		if len(l) != 2 || l[0].ID != "INC-2002" {
			t.Fatal(l)
		}
		b.Status = "resolved"
		if err := st.UpdateIncident("o", b); err != nil {
			t.Fatal(err)
		}
		if g, _ := st.GetIncident("o", "INC-2002"); g.Status != "resolved" {
			t.Fatal(g)
		}
		if err := st.UpdateIncident("o", model.Incident{ID: "INC-9"}); err != ErrNotFound {
			t.Fatal(err)
		}
		if _, err := st.ImportIncidents("o", []model.Incident{{ID: "INC-1"}}); err == nil {
			t.Fatal("已經有事件要拒絕匯入")
		}
		if n, err := st.ImportIncidents("p", []model.Incident{{ID: "INC-2041", Title: "x"}, {ID: "INC-2040", Title: "y"}}); err != nil || n != 2 {
			t.Fatal(n, err)
		}
		if c, _ := st.CreateIncident("p", model.Incident{Title: "z"}); c.ID != "INC-2042" {
			t.Fatal(c.ID)
		}
	}
	g, err := OpenFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := g.ListIncidents("o"); len(l) != 2 || l[0].Status != "resolved" {
		t.Fatal("重開後要讀回事件", l)
	}
	if l, _ := g.ListIncidents("p"); len(l) != 3 || l[0].ID != "INC-2042" {
		t.Fatal(l)
	}
}

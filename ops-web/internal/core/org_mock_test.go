package core

import "testing"

// 新組織不帶範例資料;切回預設組織時範例資料與變更都還在
func TestOrgMockDataPerOrg(t *testing.T) {
	a := newTestApp(t)
	a.Mu.Lock()
	defer a.Mu.Unlock()

	seedServices := len(a.C["services"])
	seedActivity := len(a.C["activity"])
	if seedServices == 0 || len(a.C["alerts"]) == 0 {
		t.Fatal("預設組織應該有範例資料")
	}
	// 預設組織的變更
	a.C["alerts"] = a.C["alerts"][1:]
	alerts := len(a.C["alerts"])

	for _, tpl := range []string{"blank", "copyCurrent"} {
		org := a.CreateOrg("新組織 "+tpl, "", "", tpl)
		if !a.SwitchOrg(org.ID) {
			t.Fatal("切換失敗")
		}
		for _, n := range mockCollections {
			if len(a.C[n]) != 0 {
				t.Fatalf("%s 的新組織 %s 應該是空的,有 %d 筆", tpl, n, len(a.C[n]))
			}
		}
		if len(a.Sprint) != 0 {
			t.Fatalf("%s 的新組織不該有 sprint", tpl)
		}
		// 動態牆也分組織:只有剛剛「切換到組織」這一筆,而且帶 org
		if len(a.C["activity"]) != 1 || a.C["activity"][0]["org"] != org.ID {
			t.Fatalf("%s 的新組織動態牆應該只有切換這一筆:%v", tpl, a.C["activity"])
		}
		// 新組織加一筆,切走再回來還在
		a.C["services"] = append(a.C["services"], map[string]any{"id": "x"})
		a.SwitchOrg(DefaultOrgID)
		if len(a.C["services"]) != seedServices || len(a.C["alerts"]) != alerts {
			t.Fatalf("回到預設組織,範例資料或變更不對:services %d alerts %d", len(a.C["services"]), len(a.C["alerts"]))
		}
		if len(a.C["activity"]) <= seedActivity {
			t.Fatal("預設組織的動態牆(含種子)要還在")
		}
		a.SwitchOrg(org.ID)
		if len(a.C["services"]) != 1 {
			t.Fatalf("新組織的變更沒保留:%d", len(a.C["services"]))
		}
		a.SwitchOrg(DefaultOrgID)
	}
}

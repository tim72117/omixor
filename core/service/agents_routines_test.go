package service_test

import (
	"testing"

	"omixor-core/model"
)

// 例行任務 id 空白會補 uuid;使用者給的 id 重複回 400(不是讓資料庫報純字串錯誤變 500)
func TestSaveAgentRoutineIDs(t *testing.T) {
	svc, boss, _ := cabEnv(t)
	a, err := svc.SaveAgent(boss, "o1", model.Agent{ID: "dev", Name: "工程師", Active: true,
		Routines: []model.AgentRoutine{{Title: "一", Kind: "daily"}, {Title: "二", Kind: "daily"}, {ID: "r", Title: "三", Kind: "daily"}}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range a.Routines {
		if r.ID == "" || seen[r.ID] {
			t.Fatalf("id 要補成唯一:%+v", a.Routines)
		}
		seen[r.ID] = true
	}
	_, err = svc.SaveAgent(boss, "o1", model.Agent{ID: "dev", Name: "工程師", Active: true,
		Routines: []model.AgentRoutine{{ID: "r", Title: "一", Kind: "daily"}, {ID: "r", Title: "二", Kind: "daily"}}})
	if code(err) != 400 {
		t.Fatalf("重複的 routine id 要回 400:%v", err)
	}
}

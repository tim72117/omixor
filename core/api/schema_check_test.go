package api_test

import "testing"

// GET /api/system/schema-check:非伺服器管理員 403;伺服器管理員 200、全新資料庫 ok(改壞的檢出由 store 測試涵蓋)
func TestSchemaCheckEndpoint(t *testing.T) {
	e := newEnv(t)
	boss := e.login("evo-boss")
	if _, err := boss.CreateOrg("lab", "實驗室"); err != nil {
		t.Fatal(err)
	}
	if _, err := boss.SaveMember("lab", "alice@example.com", "member"); err != nil {
		t.Fatal(err)
	}
	alice := e.login("evo-alice")
	if _, err := alice.SchemaCheck(); status(err) != 403 {
		t.Fatalf("成員要 403:%v", err)
	}
	rep, err := boss.SchemaCheck()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || len(rep.Tables) == 0 || rep.MissingTables == nil || rep.OrphanTables == nil || rep.SchemaVersion < 1 || rep.SchemaVersion != rep.ExpectedVersion {
		t.Fatalf("全新資料庫應 ok:%+v", rep)
	}
}

package service_test

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-core/store"
)

// 沒有實作 store.SchemaChecker 的 store(內嵌介面,不碰資料庫;SchemaCheck 在這些分支不該呼叫任何 Store 方法)
type noDBStore struct{ store.Store }

// 實作 SchemaChecker 的假 store,回固定報告
type fakeCheckerStore struct {
	store.Store
	rep *model.SchemaReport
}

func (f fakeCheckerStore) CheckSchema() (*model.SchemaReport, error) { return f.rep, nil }

func TestSchemaCheckNoDatabase(t *testing.T) {
	var st store.Store = noDBStore{}
	if _, ok := st.(store.SchemaChecker); ok {
		t.Fatal("測試前提:noDBStore 不該實作 SchemaChecker")
	}
	svc := &service.Service{Store: st}
	admin := &model.Principal{User: model.User{ID: "u1", Email: "boss@x", Role: "admin"}}
	rep, err := svc.SchemaCheck(admin)
	if rep != nil || code(err) != 400 || !strings.Contains(err.Error(), "沒有使用資料庫") {
		t.Fatalf("沒有資料庫要 400 且訊息含「沒有使用資料庫」:rep=%v err=%v", rep, err)
	}
}

// 先檢查伺服器管理員:非管理員、停用的管理員即使沒有資料庫也是 403(不是 400)
func TestSchemaCheckNeedsServerAdminBeforeDB(t *testing.T) {
	svc := &service.Service{Store: noDBStore{}}
	for name, p := range map[string]*model.Principal{
		"成員":     {User: model.User{ID: "u2", Email: "amy@x", Role: "member"}},
		"停用的管理員": {User: model.User{ID: "u3", Email: "off@x", Role: "admin", Disabled: true}},
	} {
		if _, err := svc.SchemaCheck(p); code(err) != 403 {
			t.Errorf("%s 要 403:%v", name, err)
		}
	}
}

// 角色憑證(Principal.Agent,沒有使用者身分)一律 403,即使 store 有資料庫、也不會去查
func TestSchemaCheckAgentCredentialForbidden(t *testing.T) {
	rep := &model.SchemaReport{OK: true}
	for name, st := range map[string]store.Store{"沒有資料庫": noDBStore{}, "有資料庫": fakeCheckerStore{rep: rep}} {
		svc := &service.Service{Store: st}
		agent := &model.Principal{Agent: &model.AgentIdentity{Org: "o1", AgentID: "cto"}, Via: model.ViaAgentCredential}
		if r, err := svc.SchemaCheck(agent); r != nil || code(err) != 403 {
			t.Errorf("%s:角色憑證要 403:rep=%v err=%v", name, r, err)
		}
	}
	// 對照:同一個假 store,管理員拿得到報告
	svc := &service.Service{Store: fakeCheckerStore{rep: rep}}
	got, err := svc.SchemaCheck(&model.Principal{User: model.User{ID: "u1", Role: "admin"}})
	if err != nil || got != rep {
		t.Fatalf("管理員應拿到報告:%v %v", got, err)
	}
}

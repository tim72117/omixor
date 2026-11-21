package storetest

// 測試用的固定 actor(ISS-36 C-2 共用地基;契約見 core/store/ACTOR_CONTRACT.md 第 7 節)。
// 「誰」欄位現在存 actor id、外鍵指向 omixor_actors,測試不能再隨手寫一個 email 字串進 store:用這裡的函式取得真的 actor 的 id。
// 全部走 store.Store 介面(PG 與批次裡的交易都行),測試資料庫回收時會一併清掉這些 actor。

import (
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/store"
)

// User 建一位使用者(role member;id 依 email 產生)與它的 actor,回傳 actor 記錄(.ID 就是要寫進「誰」欄位的值)。
// 同一個 email 再呼叫回傳同一個 actor。email 要是合法 handle(小寫會被規範化)
func User(t testing.TB, s store.Store, email string) model.ActorRecord {
	t.Helper()
	return userRole(t, s, email, "member")
}

// Admin 同 User,但是伺服器管理員
func Admin(t testing.TB, s store.Store, email string) model.ActorRecord {
	t.Helper()
	return userRole(t, s, email, "admin")
}

func userRole(t testing.TB, s store.Store, email, role string) model.ActorRecord {
	t.Helper()
	if u, err := s.UserByEmail(email); err == nil && u.ActorID != "" {
		return actorOf(t, s, u.ActorID)
	}
	id := "u_" + strings.NewReplacer("@", "_", ".", "_").Replace(strings.ToLower(email))
	if err := s.SaveUser(model.User{ID: id, Email: email, Name: email, Role: role, CreatedAt: model.Now()}); err != nil {
		t.Fatalf("建測試使用者 %s:%v", email, err)
	}
	u, err := s.UserByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return actorOf(t, s, u.ActorID)
}

// Agent 在組織 org(要先存在)建一個現役角色(id = handle)與它的 actor,回傳 actor 記錄;角色已經在就回傳它的 actor
func Agent(t testing.TB, s store.Store, org, handle string) model.ActorRecord {
	t.Helper()
	g, err := s.GetAgent(org, handle)
	if err != nil {
		if err := s.SaveAgent(org, model.Agent{ID: handle, Name: handle, Active: true, CreatedAt: model.Now()}); err != nil {
			t.Fatalf("建測試角色 %s/%s:%v", org, handle, err)
		}
		if g, err = s.GetAgent(org, handle); err != nil {
			t.Fatal(err)
		}
	}
	return actorOf(t, s, g.ActorID)
}

// System 固定的系統 actor(sys_omixor、sys_routine、sys_chain;見 model.SystemActors),回傳 actor 記錄
func System(t testing.TB, s store.Store, id string) model.ActorRecord {
	t.Helper()
	return actorOf(t, s, id)
}

// Legacy 舊字串(email、user:<email>、agent:<id>、mcp…)對到的 actor id(寬鬆解析器;缺的 actor 會建立)。
// 給「還在用舊字串」的測試過渡用;新測試用 User、Agent、System 取得明確的 actor
func Legacy(t testing.TB, s store.Store, org, raw string) string {
	t.Helper()
	id, err := s.ResolveLegacy(org, raw)
	if err != nil {
		t.Fatalf("舊值 %q 對 actor:%v", raw, err)
	}
	return id
}

func actorOf(t testing.TB, s store.Store, id string) model.ActorRecord {
	t.Helper()
	a, err := s.ActorByID(id)
	if err != nil {
		t.Fatalf("找不到 actor %s:%v", id, err)
	}
	return *a
}

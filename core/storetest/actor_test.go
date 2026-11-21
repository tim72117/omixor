package storetest

import (
	"errors"
	"testing"

	"omixor-core/model"
	"omixor-core/store"
)

// omixor_actors 是結構中繼資料表(固定的系統 actor 在範本裡):測試建的使用者、角色 actor 不會害資料庫被丟棄,
// 回收後下一個測試只看到系統 actor(同一個資料庫、同樣的固定列)
func TestActorsRecycledWithSystemRowsKept(t *testing.T) {
	var first string
	check := func(t *testing.T, s *store.PG) {
		t.Helper()
		list, err := s.ListActors(store.ActorFilter{IncludeDisabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != len(model.SystemActors) {
			t.Fatalf("新拿到的資料庫只該有系統 actor,實際 %d 筆:%+v", len(list), list)
		}
		for _, sa := range model.SystemActors {
			a, err := s.ActorByID(sa.ID)
			if err != nil || a.Handle != sa.Handle || a.Kind != model.ActorKindSystem {
				t.Fatalf("系統 actor %s:%v %+v", sa.ID, err, a)
			}
		}
	}
	t.Run("a", func(t *testing.T) {
		u0 := URL(t)
		s := attach(t, u0)
		first = u0
		check(t, s)
		u, err := s.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "Tim", Handle: "tim@x.com"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateActor(model.ActorRecord{Kind: model.ActorKindAgent, Org: "o", Name: "H", Handle: "helper", Owner: u.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "Dup", Handle: "TIM@x.com"}); !errors.Is(err, store.ErrDuplicateKey) {
			t.Fatalf("要 ErrDuplicateKey:%v", err)
		}
	})
	t.Run("b", func(t *testing.T) {
		u1 := URL(t)
		s := attach(t, u1)
		if u1 != first {
			t.Fatalf("測試只建了系統 actor 以外的列,資料庫應該被回收(同一個):%s vs %s", first, u1)
		}
		check(t, s)
		// 測試建的使用者已清掉:同一個 email 可以再建
		if _, err := s.CreateActor(model.ActorRecord{Kind: model.ActorKindUser, Name: "Tim", Handle: "tim@x.com"}); err != nil {
			t.Fatal(err)
		}
	})
}

func attach(t *testing.T, url string) *store.PG {
	t.Helper()
	s, err := store.AttachMigratedPG(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close) // 先於 testdb 的還原回收執行
	return s
}

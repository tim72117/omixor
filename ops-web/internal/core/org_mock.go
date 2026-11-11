package core

import (
	"encoding/json"
	"log"
	"strings"
)

// 範例資料分組織:
//   - 預設組織(default)用 spec/seed.json 的範例資料(資料庫模式是 ops_state 裡存的)
//   - 其他組織從空的開始,不帶任何範例資料
//   - 切換組織時把目前組織的資料收起來、換成目標組織的;各組織的變更分開保留
//   - 動態牆(activity)也分組織:新的動態帶 org 欄位(預設組織不帶)
// 資料庫模式:預設組織的 ops_state 名稱照舊(people、services…),其他組織是 "<組織 id>/<集合>";
// ops_activity 依 item->>'org' 分(沒有 org 的屬於預設組織)。

type mockData struct {
	C      map[string][]map[string]any
	Sprint map[string]any
}

type mockState struct {
	org   string               // a.C / a.Sprint 目前屬於哪個組織
	stash map[string]*mockData // 其他組織的資料(切換過才有)
}

func (a *App) mockSt() *mockState {
	if s, ok := a.Ext["mock"].(*mockState); ok {
		return s
	}
	// New() 載入的種子屬於預設組織
	s := &mockState{org: DefaultOrgID, stash: map[string]*mockData{}}
	a.Ext["mock"] = s
	return s
}

// mockOrg a.C 目前屬於哪個組織(呼叫時要拿著 Mu)
func (a *App) mockOrg() string { return a.mockSt().org }

// mockStateKey ops_state 的名稱
func mockStateKey(org, name string) string {
	if org == "" || org == DefaultOrgID {
		return name
	}
	return org + "/" + name
}

// activityOrg ops_activity 的 org 欄位值(預設組織是空字串)
func activityOrg(org string) string {
	if org == DefaultOrgID {
		return ""
	}
	return org
}

// emptyMock 空的組織:每個集合(含動態牆)都是空陣列,沒有 sprint
func emptyMock() *mockData {
	d := &mockData{C: map[string][]map[string]any{"activity": {}}, Sprint: map[string]any{}}
	for _, n := range mockCollections {
		d.C[n] = []map[string]any{}
	}
	return d
}

// switchMock 把 a.C / a.Sprint 換成目前組織的資料(呼叫時要拿著 Mu)
func (a *App) switchMock() {
	s := a.mockSt()
	target := a.CurrentOrgID()
	if target == "" || target == s.org {
		return
	}
	// 收起目前組織的資料;資料庫模式先把還沒存的變更以舊組織的名稱存掉
	cur := &mockData{C: a.C, Sprint: a.Sprint}
	if pgPool != nil {
		flushMockAsync(s.org, cur)
	}
	s.stash[s.org] = cur

	next, ok := s.stash[target]
	if !ok {
		next = emptyMock()
		if pgPool != nil {
			loadMockPG(target, next)
		}
	}
	delete(s.stash, target)
	a.C = next.C
	a.Sprint = next.Sprint
	s.org = target
}

// dropMock 刪除組織時丟掉它的資料(呼叫時要拿著 Mu)
func (a *App) dropMock(org string) {
	delete(a.mockSt().stash, org)
	if pgPool == nil || org == DefaultOrgID {
		return
	}
	go func() {
		ctx, cancel := pgCtx()
		defer cancel()
		if _, err := pgPool.Exec(ctx, `delete from ops_state where name like $1`, escapeLike(org)+"/%"); err != nil {
			log.Printf("刪除組織 %s 的範例資料失敗:%v", org, err)
		}
	}()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// flushMockAsync 切換前把舊組織還沒存的集合存起來(拿著 Mu 時先序列化,放到背景寫)
func flushMockAsync(org string, d *mockData) {
	pgSt.mu.Lock()
	names := make([]string, 0, len(pgSt.dirty))
	for n := range pgSt.dirty {
		names = append(names, n)
	}
	pgSt.dirty = map[string]bool{}
	pgSt.mu.Unlock()
	if len(names) == 0 {
		return
	}
	data := map[string][]byte{}
	for _, n := range names {
		if n == "sprint" {
			data[mockStateKey(org, n)] = Marshal(d.Sprint)
		} else {
			data[mockStateKey(org, n)] = Marshal(d.C[n])
		}
	}
	go pgWriteState(data)
}

// loadMockPG 讀資料庫裡這個組織存過的集合(沒有的維持空的)
func loadMockPG(org string, d *mockData) {
	ctx, cancel := pgCtx()
	defer cancel()
	rows, err := pgPool.Query(ctx, `select name, data::text from ops_state where name like $1`, escapeLike(org)+"/%")
	if err != nil {
		log.Printf("讀組織 %s 的範例資料失敗:%v", org, err)
		return
	}
	defer rows.Close()
	prefix := org + "/"
	for rows.Next() {
		var key string
		var b []byte
		if rows.Scan(&key, &b) != nil {
			continue
		}
		name := strings.TrimPrefix(key, prefix)
		var v any
		if json.Unmarshal(b, &v) != nil {
			continue
		}
		if name == "sprint" {
			if m, ok := v.(map[string]any); ok {
				d.Sprint = m
			}
			continue
		}
		list, _ := v.([]any)
		items := make([]map[string]any, 0, len(list))
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				items = append(items, m)
			}
		}
		d.C[name] = items
	}
	rows.Close()

	// 這個組織的動態牆(最新的 activityKeep 筆)
	arows, err := pgPool.Query(ctx, `select item::text from ops_activity where item->>'org' = $1 order by at desc, seq desc limit $2`, org, activityKeep)
	if err != nil {
		log.Printf("讀組織 %s 的動態失敗:%v", org, err)
		return
	}
	defer arows.Close()
	for arows.Next() {
		var b []byte
		var m map[string]any
		if arows.Scan(&b) == nil && json.Unmarshal(b, &m) == nil {
			d.C["activity"] = append(d.C["activity"], m)
		}
	}
}

func init() {
	OnOrgLoad(func(a *App) { a.switchMock() })
	OnOrgDelete(func(a *App, id, dir string) { a.dropMock(id) })
}

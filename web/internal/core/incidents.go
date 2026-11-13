package core

import (
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 事件:資料與規則都在核心(omixor-core 的 service/incident.go):
//
//	本機模式:內建核心 + 本機 PostgreSQL(omixor_incidents);遠端模式:omixor server 的 /api/orgs/{org}/incidents
//
// 這裡是轉接層:
//   - 讀:畫面、助理、ops_overview 照舊讀 a.C["incidents"](當成核心的快取):寫入後、切換組織時、每 5 秒從核心重讀
//   - 寫:DeclareIncident、UpdateIncidentCore 交給核心,成功後更新快取、把關聯告警的 incidentId 補上、寫動態、通知畫面
//   - 舊資料:本機模式第一次讀時,核心還沒有事件而畫面資料有(範例資料或舊版存在 ops_state 的)→ 匯入核心一次
//
// 呼叫這些函式時不要拿著 Mu(會打網路或資料庫)。

// incMigrated 已經檢查過要不要匯入的組織(key:App 位址 + 組織)
var incMigrated sync.Map

func incKey(a *App, org string) string { return fmt.Sprintf("%p|%s", a, org) }

// incErr 最近一次讀核心的錯誤(組織 → 訊息;成功後清掉)
var incErr sync.Map

// IncidentsError 目前組織最近一次讀核心事件的錯誤(沒有是 "")。不要拿著 Mu
func (a *App) IncidentsError() string {
	if v, ok := incErr.Load(incKey(a, a.currentOrgNow())); ok {
		return v.(string)
	}
	return ""
}

// actorOf 核心的執行者 → web 的
func actorOf(x model.Actor) Actor { return Actor{Kind: x.Kind, ID: x.ID} }

// coreActor web 的執行者 → 核心的
func coreActor(x Actor) *model.Actor {
	m := model.Actor{Kind: x.Kind, ID: x.ID}
	if !m.Valid() {
		return nil
	}
	return &m
}

// migrateIncidents 本機模式:核心沒有事件、快取有 → 匯入一次(不拿 Mu)
func (a *App) migrateIncidents(be backend.Backend, org string) {
	if IsRemote() || org == "" {
		return
	}
	if _, done := incMigrated.Load(incKey(a, org)); done {
		return
	}
	cur, err := be.ListIncidents(org)
	if err != nil {
		return // 下次再試
	}
	incMigrated.Store(incKey(a, org), true)
	if len(cur) > 0 {
		return
	}
	var old []model.Incident
	a.Mu.Lock()
	if a.mockOrg() == org && len(a.C["incidents"]) > 0 {
		_ = ReadInto(a.C["incidents"], &old)
	}
	a.Mu.Unlock()
	if len(old) == 0 {
		return
	}
	if _, err := be.ImportIncidents(org, old); err != nil {
		log.Printf("事件舊資料匯入核心失敗(%s):%v", org, err)
	}
}

// IncidentsRefresh 從核心重讀 org 的事件換進快取,回傳內容有沒有變(不拿 Mu 呼叫)
func (a *App) IncidentsRefresh(org string) bool {
	if org == "" {
		return false
	}
	be, err := agentBackend()
	if err != nil {
		incErr.Store(incKey(a, org), err.Error())
		return false
	}
	a.migrateIncidents(be, org)
	list, err := be.ListIncidents(org)
	if err != nil {
		incErr.Store(incKey(a, org), err.Error())
		return false
	}
	incErr.Delete(incKey(a, org))
	items := []map[string]any{}
	for _, i := range list {
		if m, ok := Object(i).(map[string]any); ok {
			items = append(items, m)
		}
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.mockOrg() != org {
		return false
	}
	changed := string(Marshal(a.C["incidents"])) != string(Marshal(items))
	if changed {
		a.C["incidents"] = items
	}
	// 關聯告警:核心記著告警 id,告警在 web 這邊;還沒標事件的補上
	for _, i := range list {
		for _, id := range i.AlertIDs {
			if _, al := a.Find("alerts", id); al != nil && Str(al, "incidentId") == "" {
				al["incidentId"] = i.ID
				changed = true
			}
		}
	}
	return changed
}

// incidentsChanged 寫入成功後:重讀快取、通知畫面
func (a *App) incidentsChanged(org string) {
	a.IncidentsRefresh(org)
	a.Changed("incidents", "alerts", "activity", "badges")
}

// DeclareIncident 宣告事件(畫面與 MCP 共用;檢查人員、服務、告警在呼叫端)。不要拿著 Mu
func (a *App) DeclareIncident(org string, in service.IncidentInput) (*model.Incident, error) {
	be, err := agentBackend()
	if err != nil {
		return nil, err
	}
	a.migrateIncidents(be, org)
	inc, err := be.DeclareIncident(org, in)
	if err != nil {
		return nil, err
	}
	who := ActorFromCore(in)
	a.Mu.Lock()
	for _, id := range inc.AlertIDs {
		if _, al := a.Find("alerts", id); al != nil {
			al["incidentId"] = inc.ID
		}
	}
	if a.mockOrg() == org {
		a.Log(who, "flame.fill", fmt.Sprintf("宣告 SEV%d 事件 %s「%s」", inc.Sev, inc.ID, inc.Title), "incidents")
	}
	a.Mu.Unlock()
	a.incidentsChanged(org)
	return inc, nil
}

// UpdateIncidentCore 更新事件(畫面與 MCP 共用)。不要拿著 Mu
func (a *App) UpdateIncidentCore(org, id string, p service.IncidentPatch) (*model.Incident, error) {
	be, err := agentBackend()
	if err != nil {
		return nil, err
	}
	a.migrateIncidents(be, org)
	inc, err := be.UpdateIncident(org, id, p)
	if err != nil {
		return nil, err
	}
	if p.Status != nil {
		who := ActorFromCore(service.IncidentInput{Actor: p.Actor, Author: p.Author})
		a.Mu.Lock()
		if a.mockOrg() == org {
			a.Log(who, "flame", inc.ID+" 狀態改為"+IncidentStatusLabel[*p.Status], "incidents")
		}
		a.Mu.Unlock()
	}
	a.incidentsChanged(org)
	return inc, nil
}

// ActorFromCore 寫動態用的執行者:有給 actor 用它,否則從 author 換(同核心的規則)
func ActorFromCore(in service.IncidentInput) Actor {
	if in.Actor != nil && in.Actor.Valid() {
		return actorOf(*in.Actor)
	}
	return actorOf(service.ActorFromAuthor(in.Author))
}

func init() {
	OnOrgLoad(func(a *App) {
		// 啟動與切換組織後重讀(OnOrgLoad 是拿著 Mu 呼叫的,改在背景做)。測試不在背景讀(寫入時會當場重讀)
		if testing.Testing() {
			return
		}
		org := a.CurrentOrgID()
		go func() {
			if a.IncidentsRefresh(org) {
				a.Changed("incidents", "alerts", "badges")
			}
		}()
	})
	OnStart(func(a *App) {
		if os.Getenv("OPS_NO_TIMERS") == "1" {
			return
		}
		// 每 5 秒重讀目前組織的事件(App、MCP 用戶端或別台機器改了才看得到;網路與資料庫請求都在鎖外)
		go func() {
			for range time.Tick(5 * time.Second) {
				if org := a.currentOrgNow(); org != "" && a.IncidentsRefresh(org) {
					a.Changed("incidents", "alerts", "badges")
				}
			}
		}()
	})
	RegisterAction("incident_note", func(a *App, args map[string]any) (any, error) {
		id := argString(args, "id")
		text := argString(args, "text")
		if text == "" {
			return nil, UserError{"註記不能是空的"}
		}
		a.Mu.Lock()
		org, who := a.CurrentOrgID(), a.Actor()
		a.Mu.Unlock()
		inc, err := a.UpdateIncidentCore(org, id, service.IncidentPatch{Note: &text, Actor: coreActor(who), Author: ActorAuthor(who)})
		if err != nil {
			return nil, UserError{err.Error()}
		}
		return Object(inc), nil
	})
}

// ListIncidentsCore 從核心讀 org 的事件(不拿 Mu)
func (a *App) ListIncidentsCore(org string) ([]model.Incident, error) {
	be, err := agentBackend()
	if err != nil {
		return nil, err
	}
	a.migrateIncidents(be, org)
	return be.ListIncidents(org)
}

// GetIncidentCore 從核心讀一筆事件(不拿 Mu)
func (a *App) GetIncidentCore(org, id string) (*model.Incident, error) {
	be, err := agentBackend()
	if err != nil {
		return nil, err
	}
	a.migrateIncidents(be, org)
	return be.GetIncident(org, id)
}

package mcp

import (
	"strings"

	"omixor-core/model"
	"omixor-core/service"
	"omixor-web/internal/core"
)

// 事件工具(incident_declare、incident_update)由核心提供(omixor-core/mcp 的 tools_incident.go),資料存在核心。
// 這裡是 appBackend 的事件部分:先做 web 這邊才有的檢查與補值(和 Swift 版一樣),再交給 core.App(寫入核心、
// 補告警的 incidentId、寫動態、更新畫面快取):
//   - commander 沒給:目前的 Primary 值班,沒有就是我;要是人員清單裡的人(「找不到 person …」)
//   - services 要是服務清單裡的(「找不到服務 …」);alert_ids 要存在(「找不到告警 …」),標題寫進時間軸
// 呼叫時沒有拿著 Mu(事件工具不鎖 Env)。

func coreActorOf(x core.Actor) *model.Actor {
	m := model.Actor{Kind: x.Kind, ID: x.ID}
	if !m.Valid() {
		return nil
	}
	return &m
}

func (b appBackend) ListIncidents(org string) ([]model.Incident, error) {
	return b.a.ListIncidentsCore(org)
}

func (b appBackend) GetIncident(org, id string) (*model.Incident, error) {
	return b.a.GetIncidentCore(org, id)
}

func (b appBackend) DeclareIncident(org string, in service.IncidentInput) (*model.Incident, error) {
	a := b.a
	a.Mu.Lock()
	err := func() error {
		if strings.TrimSpace(in.Commander) == "" {
			in.Commander = a.Me
			if oc := a.CurrentOnCall(); oc != nil {
				in.Commander = core.Str(oc, "person")
			}
		}
		_, person := a.Find("people", in.Commander)
		if person == nil || core.Str(person, "id") != in.Commander {
			return Errorf("找不到 person %s", in.Commander)
		}
		in.CommanderName = core.Str(person, "name")
		for k, s := range in.Services {
			id, err := serviceID(a, s)
			if err != nil {
				return err
			}
			in.Services[k] = id
		}
		in.AlertTitles = map[string]string{}
		for _, id := range in.AlertIDs {
			_, al := a.Find("alerts", id)
			if al == nil || core.Str(al, "id") != id {
				return Errorf("找不到告警 %s", id)
			}
			in.AlertTitles[id] = core.Str(al, "title")
		}
		return nil
	}()
	a.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	return a.DeclareIncident(org, in)
}

func (b appBackend) UpdateIncident(org, id string, p service.IncidentPatch) (*model.Incident, error) {
	return b.a.UpdateIncidentCore(org, id, p)
}

func (b appBackend) ImportIncidents(string, []model.Incident) (*model.ImportResult, error) {
	return nil, Errorf("omixor-web 的 MCP 不提供匯入事件")
}

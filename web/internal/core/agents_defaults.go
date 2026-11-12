package core

import (
	"encoding/json"

	"omixor-web/internal/templates"
)

// 預設角色:內容在最外層的 templates/agents/(範本檔),這裡只負責讀進來

func NewAgent(id, name string) Agent {
	return Agent{ID: id, Name: name, Symbol: "person.crop.circle", Color: "blue", Active: true,
		Routines: []AgentRoutine{}, CreatedAt: nowPrecise(), DocAccess: map[string]string{}}
}

func NewRoutine(title string) AgentRoutine {
	return AgentRoutine{ID: UUID(), Title: title, Kind: "daily", Hour: 9, Weekday: 2, EveryHours: 6,
		Priority: "normal", Enabled: true, CreatedAt: nowPrecise()}
}

// defaultAgents 讀範本 templates/agents/(一個角色一個檔);排程與角色的 id、建立時間照一般的寬鬆解碼補上
func defaultAgents() []Agent {
	var list []Agent
	if err := json.Unmarshal(templates.Array("agents"), &list); err != nil {
		panic("templates/agents/ 讀不出來:" + err.Error())
	}
	return list
}

// Package module 是「選用模組」的最小註冊機制(做法一:編譯時決定)。
//
// 模組是獨立的 Go 套件(例如 core/modules/integrations),在自己的 init() 呼叫 Register;
// 要不要編進去由各入口程式(main)決定:用 `import _ "omixor-core/modules/…"` 才會編進去,沒 import 就完全不在執行檔裡
// (同 Caddy 的 xcaddy、CoreDNS 的 plugin.cfg)。各入口程式把要 import 的模組列在同目錄的 modules.go 一個檔(發行組合)。
//
// 核心本身不 import 任何模組;核心在這些地方把「已註冊的模組」接進去:
//
//	功能清單:Register 時把模組擁有的功能登記到 model.Features(見 model.RegisterFeature);
//	          沒註冊的模組,它的功能不出現在 /api/system/features 與 /api/orgs/{org}/system(就像那功能不存在)
//	API 路由:core/api 組路由時呼叫每個模組的 Routes;沒註冊的模組,路由不存在(404)
//	資料:模組透過 store 的通用「組織設定文件」(store.OrgDoc,GetOrgDoc/SaveOrgDoc)存自己的設定,不需要在 store 加專屬方法
//
// 這是第一個試點,只做最小可行:之後要的話再加 MCP 工具、背景工作等欄位。
// Register 只能在 init() 呼叫(不上鎖;程式開始跑之後清單不再變)。
package module

import (
	"net/http"
	"sort"

	"omixor-core/model"
	"omixor-core/service"
)

// Handler 已驗證身分的 API 處理函式(同 core/api 內部用的形式)
type Handler func(w http.ResponseWriter, r *http.Request, p *model.Principal)

// API 核心 api 交給模組註冊路由用的工具(驗證、讀寫 JSON 的方式和核心的路由一致)
type API struct {
	Svc *service.Service
	// Authed 要 omixor token
	Authed func(Handler) http.HandlerFunc
	// AuthedOrAgent omixor token 或角色憑證(Bearer <agentId>:<secret>)
	AuthedOrAgent func(Handler) http.HandlerFunc
	// Reply 成功回 {"result": v};錯誤依 service.Code 回狀態碼與 {"error"}
	Reply func(w http.ResponseWriter, v any, err error)
	// Read 讀 JSON body(空的 body 不算錯)
	Read func(r *http.Request, v any) error
	// Bad 400 格式錯誤
	Bad func(w http.ResponseWriter, err error)
}

// Module 一個選用模組
type Module struct {
	// ID 模組代號(英數,例如 integrations)
	ID string `json:"id"`
	// Name 顯示名稱
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Features 模組擁有的功能(登記到 model.Features;Parent 是既有頁面時排在該頁面的分頁最後)
	Features []model.Feature `json:"-"`
	// Routes 註冊模組的 API 路由(可以是 nil)
	Routes func(mux *http.ServeMux, api API) `json:"-"`
}

// Info 給用戶端看的模組資訊(GET /api/modules)
type Info struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Features    []string `json:"features"`
}

var modules []Module

// Register 註冊模組(在模組套件的 init() 呼叫)。id 空白或重複會 panic;功能 key 重複也會 panic(見 model.RegisterFeature)
func Register(m Module) {
	if m.ID == "" {
		panic("模組 id 空白")
	}
	if Has(m.ID) {
		panic("模組重複註冊:" + m.ID)
	}
	for _, f := range m.Features {
		model.RegisterFeature(f)
	}
	modules = append(modules, m)
}

// All 已註冊的模組(依 id 排序)
func All() []Module {
	out := append([]Module{}, modules...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Has 這次編譯有沒有這個模組
func Has(id string) bool {
	for _, m := range modules {
		if m.ID == id {
			return true
		}
	}
	return false
}

// Infos 已註冊模組的資訊(依 id 排序)
func Infos() []Info {
	out := []Info{}
	for _, m := range All() {
		keys := []string{}
		for _, f := range m.Features {
			keys = append(keys, f.Key)
		}
		out = append(out, Info{ID: m.ID, Name: m.Name, Description: m.Description, Features: keys})
	}
	return out
}

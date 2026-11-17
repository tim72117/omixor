package core

import (
	"time"

	"omixor-core/backend"
)

// 給 web 選用模組(internal/modules/…,見 modules.go)用的內部工具公開版本。行為和套件內的同名小寫函式一樣,
// 只是讓另一個套件也能用;套件內照舊用小寫的。

// JSON 欄位讀取(同 conn_json.go)
func JStr(v any) string                    { return jStr(v) }
func JNum(v any) (float64, bool)           { return jNum(v) }
func JInt(v any) int                       { return jInt(v) }
func JMap(v any) map[string]any            { return jMap(v) }
func JMaps(v any) []map[string]any         { return jMaps(v) }
func JBool(v any) bool                     { return jBool(v) }
func OptString(v any) *string              { return optString(v) }
func PercentEncode(s, extra string) string { return percentEncode(s, extra) }

// TimeOrPast 排序用:nil 當作最早
func TimeOrPast(t *Time) time.Time { return timeOrPast(t) }

// ConnErr 連線的錯誤(給畫面看的訊息)
func ConnErr(msg string) error { return connErr(msg) }

// ConnCheck 回應不是 2xx 時轉成 ConnError
func ConnCheck(r *ConnResponse, err error) (*ConnResponse, error) { return connCheck(r, err) }

// ConnHasTokenCached 有沒有 token(只看快取,可以拿著 Mu)
func ConnHasTokenCached(a *App, c Connection) bool { return connHasTokenCached(a, c) }

// ConnStateCopy 連線狀態的複本(拿著 Mu)
func ConnStateCopy(a *App, id string) *ConnectionState { return connStateCopy(a, id) }

// ConnGen 目前組織資料的世代(每次重新讀組織資料加一;背景工作用來判斷是不是過期了;拿著 Mu)
func ConnGen(a *App) int { return a.conns().Gen }

// ErrNoDB 本機模式沒有資料庫
var ErrNoDB = errNoDB

// AgentBackend 目前的核心(遠端模式是 omixor server,本機模式是同一個程式裡的核心;本機沒有資料庫回 ErrNoDB)
func AgentBackend() (backend.Backend, error) { return agentBackend() }

// AgentUserErr 核心的錯誤 → 畫面看得懂的
func AgentUserErr(err error) error { return agentUserErr(err) }

// CurrentOrgNow 目前的組織(不用拿著 Mu)
func (a *App) CurrentOrgNow() string { return a.currentOrgNow() }

// TimePtr 時間的指標(JSON 用)
func TimePtr(t time.Time) *Time { return timePtr(t) }

// VaultTokens 鑰匙圈裡這個連線的 token(沒有是 nil;測試與模組用)
func VaultTokens(scope, id string) *TokenSet { return vaultTokens(scope, id) }

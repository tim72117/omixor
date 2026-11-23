package core

import (
	"strings"
	"sync"
	"time"

	"omixor-core/version"
)

// 遠端核心的功能偵測:查 GET /api/version(免登入)的 features,畫面用它決定要不要顯示某功能(例如 issue_accept 的接受按鈕)。
// 不比版本號:舊伺服器沒有 features 欄位、查不到、逾時都是空的,畫面預設不顯示。結果依伺服器網址快取,連線期間每 30 秒更新一次。

var (
	remoteVerMu      sync.Mutex
	remoteVerServer  string
	remoteVerFeats   []string
	remoteVerPending string // 正在查的伺服器(避免畫面重繪時重複開查詢)
)

// remoteFeatures 目前連的遠端支援的功能;這個伺服器還沒查過就背景查一次(查完通知畫面),先回空的。可以拿著 Mu 呼叫(不連網)
func remoteFeatures(a *App, server string) []string {
	server = strings.TrimRight(server, "/")
	remoteVerMu.Lock()
	defer remoteVerMu.Unlock()
	if remoteVerServer == server {
		return append([]string{}, remoteVerFeats...)
	}
	if server != "" && remoteVerPending != server {
		remoteVerPending = server
		go refreshRemoteVersion(a, server)
	}
	return []string{}
}

// refreshRemoteVersion 查遠端的 /api/version 並記下 features,有變就通知畫面。不能拿著 Mu(會連網,逾時 3 秒)
func refreshRemoteVersion(a *App, server string) {
	server = strings.TrimRight(server, "/")
	var feats []string
	if d, err := version.Fetch(server, 3*time.Second); err == nil {
		feats = d.Features
	}
	remoteVerMu.Lock()
	changed := remoteVerServer != server || strings.Join(remoteVerFeats, ",") != strings.Join(feats, ",")
	remoteVerServer, remoteVerFeats = server, feats
	if remoteVerPending == server {
		remoteVerPending = ""
	}
	remoteVerMu.Unlock()
	if changed && a != nil {
		a.Changed("account")
	}
}

// remoteLacks 已經查過這個遠端、而且它沒宣告支援 feature(還沒查過或查不到不算:不擋,交給伺服器回應)
func remoteLacks(server, feature string) bool {
	server = strings.TrimRight(server, "/")
	remoteVerMu.Lock()
	defer remoteVerMu.Unlock()
	if remoteVerServer != server {
		return false
	}
	for _, f := range remoteVerFeats {
		if f == feature {
			return false
		}
	}
	return true
}

package oauth

import (
	"omixor-core/config"
	"omixor-core/service"
)

// ConfigureService 把 OAuth 登入要用的設定套到 service(啟動時、開始服務請求之前呼叫一次):
// evoleap 網址與核准頁(EVOLEAP_URL、OMIXOR_EVOLEAP_CONSOLE)、同意畫面的 evoleap 登入開關(OMIXOR_OAUTH_EVOLEAP_LOGIN),
// 以及信任的代理層數(OMIXOR_TRUSTED_PROXY_HOPS;每 IP 的頻率限制在代理後面才算得對)。
// omixor-server 與 console 的 OAuth 連接埠共用,兩邊的 evoleap 登入設定因此一致
func ConfigureService(svc *service.Service, cfg *config.Config) {
	svc.Evoleap = cfg.Str(config.EvoleapURL)
	svc.EvoleapConsole = cfg.Str(config.EvoleapConsole)
	svc.OAuthEvoleapOff = !cfg.Bool(config.OAuthEvoleapLogin)
	service.SetTrustedProxyHops(cfg.ProxyHops())
}

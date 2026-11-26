package server

import (
	"errors"
	"strings"

	"omixor-core/config"
	"omixor-core/oauth"
	"omixor-core/service"
)

// configureLogins 依序開選用的登入方式(LDAP、GitLab),再跑 OAuth 啟動自我檢查:印摘要(含各 provider 要登記的回呼網址),
// 致命的問題回傳錯誤(和 console 共用 core/oauth 的 Diagnose)。自我檢查看的是 svc 上已經套好的設定,所以一定要放在最後;
// 呼叫前先 oauth.ConfigureService(evoleap)
func configureLogins(svc *service.Service, cfg *config.Config, logf func(format string, args ...any)) error {
	public := strings.TrimRight(cfg.Str(config.PublicURL), "/")
	if err := configureLDAP(svc, cfg.Getenv); err != nil {
		return errors.New("LDAP 設定:" + cfg.Redact(err.Error()))
	}
	if err := configureGitLab(svc, cfg.Getenv, public); err != nil {
		return errors.New("GitLab 設定:" + cfg.Redact(err.Error()))
	}
	return oauth.Startup(oauth.Env{Cfg: cfg, Prog: config.Server, Providers: oauth.DefaultProviders(svc, public)}, logf)
}

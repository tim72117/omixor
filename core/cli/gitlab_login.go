package cli

import (
	"fmt"
	"io"
	"slices"
	"time"

	"omixor-core/client"
	"omixor-core/evoleap"
	"omixor-core/service"
	"omixor-core/version"
)

// gitlabLogin omixor login --gitlab:用伺服器設定的自建 GitLab 登入(瀏覽器要在這台電腦上,GitLab 登入後導回本機)
func gitlabLogin(out io.Writer, srv, device string) (*service.LoginResult, error) {
	v, err := version.Fetch(srv, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("連不到 %s:%v", srv, err)
	}
	if !slices.Contains(v.Features, version.FeatureGitLabLogin) {
		return nil, fmt.Errorf("%s 沒有啟用 GitLab 登入", srv)
	}
	res, err := client.New(srv).GitLabBrowserLogin(device, func(u string) {
		fmt.Fprintln(out, "瀏覽器會打開 GitLab 的登入頁,完成後回到這裡…")
		fmt.Fprintln(out, "登入頁:"+u)
		if f, ok := out.(interface{ Sync() error }); ok {
			_ = f.Sync()
		}
		evoleap.OpenBrowser(u) // 開瀏覽器的方式和 evoleap 登入共用(測試可以換掉)
	}, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("GitLab 登入失敗:%v", err)
	}
	return res, nil
}

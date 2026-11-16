package integrations

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"

	"omixor-web/internal/core"
)

// 測試的共同設定(原本在 internal/core 的 conn_test.go 與 templates_main_test.go)

func TestMain(m *testing.M) {
	_ = os.Setenv("OMIXOR_TEMPLATES", "1")
	core.UseMemoryCores() // 沒有資料庫時用記憶體的核心
	os.Exit(m.Run())
}

// connTestApp 新的 App(資料在暫存資料夾、不開計時器、鑰匙圈在記憶體;瀏覽器不會真的打開)
func connTestApp(t *testing.T) *core.App {
	t.Helper()
	t.Setenv("OPS_DATA_DIR", t.TempDir())
	t.Setenv("OPS_NO_TIMERS", "1")
	core.Vault = core.NewMemoryVault()
	saved := core.OpenBrowser
	t.Cleanup(func() { core.OpenBrowser = saved })
	a := core.New()
	a.Mu.Lock()
	a.LoadOrgData()
	a.Mu.Unlock()
	return a
}

func formOf(body []byte) map[string]string {
	out := map[string]string{}
	v, _ := url.ParseQuery(string(body))
	for k := range v {
		out[k] = v.Get(k)
	}
	return out
}

// browserApproves 模擬瀏覽器:使用者按同意後,把瀏覽器導回 redirect_uri?code=…&state=…(同 internal/core 的 conn_test.go)
func browserApproves(t *testing.T, f *core.FakeNet, code, param string, extra map[string]string, opened *string) {
	var mu sync.Mutex
	core.OpenBrowser = func(u string) {
		mu.Lock()
		if opened != nil {
			*opened = u
		}
		mu.Unlock()
		parsed, _ := url.Parse(u)
		q := parsed.Query()
		redirect := q.Get("redirect_uri")
		if redirect == "" {
			if r, ok := f.Find("/api/cli-auth/start"); ok {
				var o map[string]any
				_ = json.Unmarshal(r.Body, &o)
				redirect, _ = o["redirect_uri"].(string)
			}
		}
		value := q.Get(param)
		if value == "" {
			value = extra[param]
		}
		go func() {
			cb, _ := url.Parse(redirect)
			cb.RawQuery = url.Values{"code": {code}, param: {value}}.Encode()
			resp, err := http.Get(cb.String())
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
}

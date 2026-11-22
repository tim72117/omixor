package web

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// view 與 action 的回應帶 X-Omixor-Version(算資料前的版本),畫面拿來略過已經反映的 SSE 通知
func TestDataVersionHeader(t *testing.T) {
	c, app := newServer(t, nil)
	app.Changed("alerts")
	want := app.Version()
	get := func(method, path, body string) string {
		req, _ := http.NewRequest(method, c.srv.URL+path, strings.NewReader(body))
		if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s %s: HTTP %d", method, path, resp.StatusCode)
		}
		return resp.Header.Get("X-Omixor-Version")
	}
	for _, h := range []string{get("GET", "/api/view/alerts", ""), get("POST", "/api/action/navigate", `{"section":"overview"}`)} {
		v, err := strconv.ParseUint(h, 10, 64)
		if err != nil || v < want {
			t.Fatalf("X-Omixor-Version = %q,要是 ≥ %d 的整數", h, want)
		}
	}
}

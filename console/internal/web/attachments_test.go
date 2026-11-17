package web

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAttachmentUploadAndServe(t *testing.T) {
	c, _ := newServer(t, nil)
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x01", 40)
	st, out := c.do("POST", "/api/attachments", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(png))}, nil)
	res, _ := out["result"].(map[string]any)
	url, _ := res["url"].(string)
	if st != 200 || !strings.HasPrefix(url, "/api/attachments/") {
		t.Fatalf("上傳:%d %v", st, out)
	}

	resp, err := http.Get(c.srv.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != png || resp.Header.Get("Content-Type") != "image/png" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("讀取:%d %q %v", resp.StatusCode, body, resp.Header)
	}

	// 不是圖片、不是 base64、不是 JSON 都擋
	if _, out := c.do("POST", "/api/attachments", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("<svg onload=alert(1)>"))}, nil); out["error"] == nil {
		t.Fatal("SVG 應該擋")
	}
	if st, _ := c.do("POST", "/api/attachments", map[string]any{"data": "%%%"}, nil); st != 400 {
		t.Fatal("不是 base64 應該 400")
	}
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/attachments", strings.NewReader(png))
	req.Header.Set("Content-Type", "image/png")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Fatalf("非 JSON 的 POST 要 415(CSRF 防護),得到 %d", resp.StatusCode)
	}
	if resp, _ := http.Get(c.srv.URL + "/api/attachments/..%2Forgs.json"); resp.StatusCode != 404 {
		t.Fatalf("路徑穿越要 404,得到 %d", resp.StatusCode)
	}
}

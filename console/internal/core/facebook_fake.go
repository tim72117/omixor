package core

import (
	"fmt"
	"net/http"
	"strings"
)

// 測試用的假 graph.facebook.com(搭配 NewFakeNet),同 Swift FacebookTests 的 fakeGraph。core 與 mcp 的測試共用。

const (
	FBFakePage1      = "1001"
	FBFakePage2      = "1002"
	FBFakePageToken  = "EAAG-page-1001"
	FBFakePage2Token = "EAAG-page-1002"
	FBFakeUserToken  = "EAAG-user"
	FBFakeLongUser   = "EAAG-long-user"
)

var fbFakeDeprecated = map[string]bool{"post_impressions_unique": true, "page_impressions_unique": true, "page_impressions": true,
	"page_fans": true, "page_post_engagements": true, "page_fan_adds_unique": true}

func fbFakeError(status int, message string, code int, sub ...int) (int, any) {
	e := map[string]any{"message": message, "type": "OAuthException", "code": code, "fbtrace_id": "Abc"}
	if len(sub) > 0 {
		e["error_subcode"] = sub[0]
	}
	return status, map[string]any{"error": e}
}

// FBFakeGraph 假的 Graph API handler
func FBFakeGraph(r *http.Request, body []byte) (int, any) {
	if r.Host != FBHost || !strings.HasPrefix(r.URL.Path, "/v24.0/") {
		return 404, map[string]any{"error": "wrong host"}
	}
	path := strings.TrimPrefix(r.URL.Path, "/v24.0")
	q := map[string]string{}
	for k, v := range r.URL.Query() {
		q[k] = v[0]
	}
	auth := r.Header.Get("Authorization")

	if path == "/oauth/access_token" {
		if q["grant_type"] != "fb_exchange_token" || q["client_id"] != "APPID" || q["client_secret"] != "APPSECRET" || q["fb_exchange_token"] != "SHORT" {
			return fbFakeError(400, "Invalid OAuth access token", 190)
		}
		return 200, map[string]any{"access_token": FBFakeLongUser, "token_type": "bearer", "expires_in": 5184000}
	}
	if auth == "Bearer EXPIRED" {
		return fbFakeError(400, "Error validating access token: Session has expired", 190, 463)
	}
	page1Info := func() map[string]any {
		return map[string]any{"id": FBFakePage1, "name": "evoleap", "category": "Education", "link": "https://facebook.com/evoleap",
			"fan_count": 120, "followers_count": 150, "picture": map[string]any{"data": map[string]any{"url": "https://img/p.png"}}}
	}
	switch r.Method + " " + path {
	case "GET /me":
		if auth == "Bearer "+FBFakePageToken {
			return 200, page1Info()
		}
		if auth == "Bearer "+FBFakeUserToken {
			return 200, map[string]any{"id": "u1", "name": "Tim"}
		}
		return fbFakeError(400, "Invalid OAuth access token", 190)
	case "GET /me/accounts":
		if auth != "Bearer "+FBFakeUserToken && auth != "Bearer "+FBFakeLongUser {
			return fbFakeError(400, "bad", 190)
		}
		if q["after"] == "P2" {
			return 200, map[string]any{"data": []any{map[string]any{"id": FBFakePage2, "name": "evoleap 兒童", "category": "Education", "access_token": FBFakePage2Token}}}
		}
		p := page1Info()
		p["access_token"] = FBFakePageToken
		return 200, map[string]any{"data": []any{p}, "paging": map[string]any{"cursors": map[string]any{"after": "P2"}, "next": "https://graph.facebook.com/v24.0/me/accounts?after=P2"}}
	}
	if auth != "Bearer "+FBFakePageToken {
		return fbFakeError(400, "Invalid OAuth access token", 190)
	}
	switch r.Method + " " + path {
	case "GET /" + FBFakePage1:
		return 200, page1Info()
	case "GET /" + FBFakePage1 + "/posts":
		post := func(n int) map[string]any {
			return map[string]any{"id": fmt.Sprintf("%s_%d", FBFakePage1, n), "message": fmt.Sprintf("第 %d 篇", n),
				"created_time": fmt.Sprintf("2026-11-0%dT08:00:00+0000", n), "permalink_url": fmt.Sprintf("https://facebook.com/%d", n),
				"is_published": true, "status_type": "mobile_status_update",
				"reactions": map[string]any{"data": []any{}, "summary": map[string]any{"total_count": 7}},
				"comments":  map[string]any{"data": []any{}, "summary": map[string]any{"total_count": 2}},
				"shares":    map[string]any{"count": 1}}
		}
		if q["after"] == "X" {
			return 200, map[string]any{"data": []any{post(3)}}
		}
		return 200, map[string]any{"data": []any{post(1), post(2)},
			"paging": map[string]any{"next": "https://graph.facebook.com/v24.0/" + FBFakePage1 + "/posts?after=X"}}
	case "GET /" + FBFakePage1 + "/scheduled_posts":
		return 200, map[string]any{"data": []any{map[string]any{"id": FBFakePage1 + "_900", "message": "排程的", "scheduled_publish_time": 1800000000, "is_published": false}}}
	case "POST /" + FBFakePage1 + "/feed":
		if strings.Contains(string(body), "FAIL") {
			return fbFakeError(403, "(#200) Requires pages_manage_posts permission", 200)
		}
		return 200, map[string]any{"id": FBFakePage1 + "_555"}
	case "POST /" + FBFakePage1 + "/photos":
		return 200, map[string]any{"id": "777", "post_id": FBFakePage1 + "_777"}
	case "DELETE /" + FBFakePage1 + "_900":
		return 200, map[string]any{"success": true}
	}
	if strings.HasSuffix(path, "/insights") {
		metrics := []string{}
		if q["metric"] != "" {
			metrics = strings.Split(q["metric"], ",")
		}
		for _, m := range metrics {
			if fbFakeDeprecated[m] {
				return fbFakeError(400, "(#100) The value must be a valid insights metric", 100)
			}
		}
		series := func(m string, vals ...int) map[string]any {
			arr := []any{}
			for i, v := range vals {
				arr = append(arr, map[string]any{"value": v, "end_time": fmt.Sprintf("2026-11-0%dT08:00:00+0000", i+1)})
			}
			return map[string]any{"name": m, "period": "day", "values": arr}
		}
		data := []any{}
		for _, m := range metrics {
			switch m {
			case "post_total_media_view_unique":
				data = append(data, map[string]any{"name": m, "period": "lifetime", "values": []any{map[string]any{"value": 345}}})
			case "post_clicks":
				data = append(data, map[string]any{"name": m, "period": "lifetime", "values": []any{map[string]any{"value": 12}}})
			case "post_reactions_by_type_total":
				data = append(data, map[string]any{"name": m, "period": "lifetime", "values": []any{map[string]any{"value": map[string]any{"like": 5, "love": 2}}}})
			case "page_total_media_view_unique", "page_media_view":
				data = append(data, series(m, 10, 20, 30))
			case "page_follows":
				data = append(data, series(m, 150, 151, 152))
			default:
				data = append(data, map[string]any{"name": m, "period": "day", "values": []any{}})
			}
		}
		return 200, map[string]any{"data": data}
	}
	return fbFakeError(400, "Unsupported get request", 100, 33)
}

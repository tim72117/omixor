package core

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Meta Graph API 的 HTTP,同 Swift 版 Facebook/FacebookGraph.swift:一律走 connSend(ConnTransport,測試時被 FakeNet 攔截),
// 權杖放在 Authorization: Bearer 標頭,只送到 graph.facebook.com;分頁的 paging.next 也只跟同一台主機。
// 會發網路請求,呼叫時不能拿著 a.Mu。

const (
	FBConnectionID = "facebook"
	FBHost         = "graph.facebook.com"
	FBScopes       = "pages_show_list,pages_read_engagement,pages_manage_posts,read_insights"
	// FBCallbackPort OAuth 回呼固定用這個埠,Meta App 的「有效的 OAuth 重新導向 URI」要填 http://127.0.0.1:3767/callback
	FBCallbackPort = 3767
	// 排程限制:10 分鐘到 30 天後
	FBMinLead = 10 * time.Minute
	FBMaxLead = 30 * 24 * time.Hour
)

var FBCallbackURL = fmt.Sprintf("http://127.0.0.1:%d/callback", FBCallbackPort)

var FBPeriods = []string{"day", "week", "days_28"}

var fbTaipei = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Taipei"); err == nil {
		return l
	}
	return time.FixedZone("Asia/Taipei", 8*3600)
}()

// FBIsVersion v21 以上(同 Swift 的 FB.isVersion)
func FBIsVersion(v string) bool {
	if !strings.HasPrefix(v, "v") {
		return false
	}
	n, err := strconv.ParseFloat(v[1:], 64)
	return err == nil && n >= 21
}

// FBValidateSchedule 排程時間要在 10 分鐘到 30 天後
func FBValidateSchedule(date, now time.Time) error {
	lead := date.Sub(now)
	if lead < FBMinLead {
		return fbErr("排程時間至少要在 10 分鐘後(" + FBTaipeiString(now.Add(FBMinLead)) + " 之後)")
	}
	if lead > FBMaxLead {
		return fbErr("排程時間最多只能到 30 天後(" + FBTaipeiString(now.Add(FBMaxLead)) + " 之前)")
	}
	return nil
}

// FBTaipeiString 2026/11/20 20:00(台北)
func FBTaipeiString(d time.Time) string {
	return d.In(fbTaipei).Format("2006/01/02 15:04") + "(台北)"
}

// FBPresetConnection 同 Swift 的 FB.presetConnection
func FBPresetConnection(version string) Connection {
	c := NewConnection(FBConnectionID, "Facebook", "https://"+FBHost)
	c.IsProduction = true
	c.TestPath = ""
	c.Auth = AuthOAuth
	c.Note = "AI 自動化 › 粉絲專頁:發文、排程與成效"
	port := FBCallbackPort
	c.OAuth = OAuthSettings{Issuer: "https://www.facebook.com",
		AuthorizationEndpoint: "https://www.facebook.com/" + version + "/dialog/oauth",
		TokenEndpoint:         "https://" + FBHost + "/" + version + "/oauth/access_token",
		Scopes:                FBScopes, RegisteredPort: &port}
	return c
}

// MARK: 成效指標(依序嘗試,前面的被淘汰就用後面的)

type FBMetricDef struct {
	Key        string
	Label      string
	Candidates []string
	// Latest 期間合計用加總;追蹤數這種累計值用最後一天
	Latest bool
}

var FBPageMetrics = []FBMetricDef{
	{Key: "reach", Label: "觸及人數", Candidates: []string{"page_impressions_unique", "page_total_media_view_unique"}},
	{Key: "views", Label: "瀏覽次數", Candidates: []string{"page_media_view", "page_impressions"}},
	{Key: "engagement", Label: "貼文互動", Candidates: []string{"page_post_engagements"}},
	{Key: "followers", Label: "追蹤數", Candidates: []string{"page_follows", "page_fans"}, Latest: true},
	{Key: "new_followers", Label: "新增追蹤", Candidates: []string{"page_daily_follows_unique", "page_fan_adds_unique"}},
}

var FBPostMetrics = []FBMetricDef{
	{Key: "reach", Label: "觸及人數", Candidates: []string{"post_impressions_unique", "post_total_media_view_unique"}},
	{Key: "clicks", Label: "點擊", Candidates: []string{"post_clicks"}},
	{Key: "reactions", Label: "心情", Candidates: []string{"post_reactions_by_type_total"}},
}

// MARK: 解析

// fbDate 數字(epoch 秒)、2026-11-10T08:00:00+0000 或 ISO 8601
func fbDate(v any) *time.Time {
	if n, ok := jNum(v); ok {
		t := time.Unix(0, int64(n*1e9)).UTC()
		return &t
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

func fbCount(v any) *int {
	d, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if s, ok := d["summary"].(map[string]any); ok {
		if n, ok := jNum(s["total_count"]); ok {
			i := int(n)
			return &i
		}
	}
	if n, ok := jNum(d["count"]); ok {
		i := int(n)
		return &i
	}
	return nil
}

func fbOptInt(v any) *int {
	if n, ok := jNum(v); ok {
		i := int(n)
		return &i
	}
	return nil
}

func fbParsePage(d map[string]any) FBPage {
	pic := jStr(jMap(jMap(d["picture"])["data"])["url"])
	return FBPage{ID: jStr(d["id"]), Name: jStr(d["name"]), Category: jStr(d["category"]), Link: jStr(d["link"]),
		Picture: pic, FanCount: fbOptInt(d["fan_count"]), FollowersCount: fbOptInt(d["followers_count"]), Managed: true}
}

func fbParsePost(d map[string]any, pageID string) FBPost {
	pub := true
	if b, ok := d["is_published"].(bool); ok {
		pub = b
	}
	return FBPost{ID: jStr(d["id"]), PageID: pageID, Message: jStr(d["message"]), CreatedTime: fbDate(d["created_time"]),
		ScheduledTime: fbDate(d["scheduled_publish_time"]), Permalink: jStr(d["permalink_url"]),
		Picture: jStr(d["full_picture"]), IsPublished: pub, StatusType: jStr(d["status_type"]),
		Reactions: fbCount(d["reactions"]), Comments: fbCount(d["comments"]), Shares: fbCount(d["shares"])}
}

const (
	fbPostFields = "id,message,created_time,permalink_url,full_picture,is_published,status_type," +
		"reactions.summary(total_count).limit(0),comments.summary(total_count).limit(0),shares"
	fbScheduledFields = "id,message,created_time,scheduled_publish_time,permalink_url,full_picture,is_published"
	fbPageFields      = "id,name,category,link,fan_count,followers_count,picture{url}"
)

type fbValue struct {
	date  *time.Time
	value any
}

// fbInsightValues insights 的 data 陣列 → 指標名稱 → 每天的值(數字)或分類(心情)
func fbInsightValues(data []map[string]any) map[string][]fbValue {
	out := map[string][]fbValue{}
	for _, m := range data {
		name := jStr(m["name"])
		vals := out[name]
		for _, v := range jMaps(m["values"]) {
			value, ok := v["value"]
			if !ok {
				continue
			}
			vals = append(vals, fbValue{fbDate(v["end_time"]), value})
		}
		out[name] = vals
	}
	return out
}

// MARK: - 請求

// fbQuery key 排序(同 Swift 的 URLComponents.queryItems)
func fbQuery(q map[string]string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, percentEncode(k, "-._~")+"="+percentEncode(q[k], "-._~!$'()*,;:@/?"))
	}
	return strings.Join(parts, "&")
}

// FBURL https://graph.facebook.com/<version><path>?<query>
func FBURL(version, path string, query map[string]string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := "https://" + FBHost + "/" + version + EscapePath(path)
	if len(query) > 0 {
		u += "?" + fbQuery(query)
	}
	return u
}

// fbCheck 解析回應:錯誤是 {error:{message, type, code, error_subcode}}
func fbCheck(r *ConnResponse) (map[string]any, error) {
	obj, _ := r.JSON().(map[string]any)
	if obj == nil {
		obj = map[string]any{}
	}
	if e, ok := obj["error"].(map[string]any); ok {
		msg, ok := e["message"].(string)
		if !ok {
			msg = "Graph API 錯誤"
		}
		code, _ := jNum(e["code"])
		sub, _ := jNum(e["error_subcode"])
		return nil, FBError{Message: msg, Code: int(code), Subcode: int(sub), Status: r.Status}
	}
	if !r.OK() {
		return nil, FBError{Message: fmt.Sprintf("HTTP %d:%s", r.Status, connErrorMessage(r)), Status: r.Status}
	}
	return obj, nil
}

func fbHostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// FBSend token 是空字串表示不帶 Authorization
func FBSend(method, rawURL, token string, body []byte, contentType string) (map[string]any, error) {
	if h := fbHostOf(rawURL); h != FBHost {
		if h == "" {
			h = "?"
		}
		return nil, fbErr("不會把權杖送到 " + h)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	r, err := connSend(nil, method, rawURL, headers, body)
	if err != nil {
		return nil, fbErr("連不上 Facebook:" + err.Error())
	}
	return fbCheck(r)
}

func FBGet(version, path, token string, query map[string]string) (map[string]any, error) {
	return FBSend("GET", FBURL(version, path, query), token, nil, "")
}

// FBGetAll 跟著 paging.next 讀完(最多 maxPages 頁;limit <= 0 表示不限);next 只接受 graph.facebook.com
func FBGetAll(version, path, token string, query map[string]string, maxPages, limit int) ([]map[string]any, error) {
	out := []map[string]any{}
	next := FBURL(version, path, query)
	pages := 0
	for next != "" && pages < maxPages {
		pages++
		obj, err := FBSend("GET", next, token, nil, "")
		if err != nil {
			return nil, err
		}
		out = append(out, jMaps(obj["data"])...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		next = ""
		if n, ok := jMap(obj["paging"])["next"].(string); ok {
			if _, err := url.Parse(n); err == nil && fbHostOf(n) == FBHost {
				next = n
			}
		}
	}
	return out, nil
}

func FBPostForm(version, path, token string, fields map[string]string) (map[string]any, error) {
	return FBSend("POST", FBURL(version, path, nil), token, []byte(FormEncode(fields)), "application/x-www-form-urlencoded")
}

func FBPostMultipart(version, path, token string, fields map[string]string, name, filename, mime string, data []byte) (map[string]any, error) {
	boundary := "ops-" + UUID()
	var b strings.Builder
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", boundary, k, fields[k])
	}
	fmt.Fprintf(&b, "--%s\r\nContent-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\nContent-Type: %s\r\n\r\n", boundary, name, filename, mime)
	body := append([]byte(b.String()), data...)
	body = append(body, []byte("\r\n--"+boundary+"--\r\n")...)
	return FBSend("POST", FBURL(version, path, nil), token, body, "multipart/form-data; boundary="+boundary)
}

func FBDelete(version, path, token string) (map[string]any, error) {
	return FBSend("DELETE", FBURL(version, path, nil), token, nil, "")
}

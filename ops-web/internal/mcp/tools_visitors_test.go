package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opsweb/internal/core"
)

// 網站訪客,照 Swift 的 SiteVisitorsTests.swift 一個一個搬

// 依 evoleap store.AdminOverview 的格式:Go 的 nil slice 是 null、時間是 RFC 3339(有時帶小數秒)
const siteFixture = `{
  "users": 12, "activeUsers": 5, "guests": 340,
  "lessons": [
    {"lesson": "l0/0.0", "learners": 4, "completed": 2, "avgPercent": 63.5, "guestViews": 120, "guestVisitors": 90,
     "guestWatchedMs": 3600000, "guestBots": 7, "guestTracked": 100, "guestReady": 92, "guestFailed": 3,
     "guestPlayed": 40, "guestIdleVisibleMs": 15000, "guestReadyAvgMs": 1800},
    {"lesson": "gate:/courses/tools/0", "guestTracked": 5, "guestBots": 1},
    {"lesson": "page:/", "guestTracked": 30},
    {"lesson": "l0/0.1", "learners": 0, "completed": 0, "avgPercent": 0, "guestViews": 10, "guestVisitors": 8,
     "guestWatchedMs": 0, "guestBots": 0, "guestTracked": 10, "guestReady": 9, "guestFailed": 0,
     "guestPlayed": 2, "guestIdleVisibleMs": 0, "guestReadyAvgMs": 2100}
  ],
  "guestStats": {
    "devices": [{"name": "mobile", "count": 70}, {"name": "bot", "count": 7}],
    "sources": null,
    "errors": [{"name": "timeout", "count": 3}],
    "idleVisible": [{"name": "<10s", "count": 30}],
    "idleVisits": [{"lesson": "l0/0.0", "device": "mobile", "source": "google.com", "readyMs": 1500, "loadError": "",
                    "visibleMs": 8000, "events": [{"t": 1200, "e": "ready"}, {"t": 7000, "e": "leave:/"}],
                    "openedAt": "2026-11-10T08:00:00.123456Z"}],
    "homePaths": [{"steps": [{"lesson": "page:/", "played": false}, {"lesson": "l0/0.0", "played": true}], "count": 9, "played": 6}],
    "homeVisits": 55,
    "homeStays": [{"guest": "a1b2c3d4", "device": "desktop", "source": "", "visits": 3, "totalMs": 95000,
                   "longestMs": 60000, "maxScroll": 75, "lastOpened": "2026-11-10T09:30:00Z"}],
    "login": {"visits": 14, "outcomes": [{"name": "google", "count": 4}], "next": null, "fails": null,
              "googleReady": 12, "googleReadyAvgMs": 900, "googleFailed": 1, "googleErrors": null}
  }
}`

func siteFixtureObj() any {
	var v any
	_ = json.Unmarshal([]byte(siteFixture), &v)
	return v
}

func TestSiteDecodesGoStyleOverview(t *testing.T) {
	d, err := core.DecodeSiteOverview(siteFixtureObj())
	if err != nil {
		t.Fatal(err)
	}
	if d.Users != 12 || len(d.Lessons) != 4 {
		t.Fatalf("%+v", d)
	}
	pl := []string{}
	for _, l := range d.PlayerLessons() {
		pl = append(pl, l.Lesson)
	}
	if strings.Join(pl, ",") != "l0/0.0,l0/0.1" {
		t.Fatal("登入門檻與一般頁面不算課", pl)
	}
	if g := d.Gates(); len(g) != 1 || g[0] != (core.SiteCount{Name: "/courses/tools/0", Count: 5}) {
		t.Fatal(g)
	}
	if d.GuestStats.Sources == nil || len(d.GuestStats.Sources) != 0 || len(d.GuestStats.Login.Next) != 0 {
		t.Fatal("Go 的 null slice 要變成空陣列")
	}
	if d.GuestStats.IdleVisits[0].OpenedAt == nil || d.GuestStats.HomeStays[0].LastOpened == nil {
		t.Fatal("帶小數秒的時間也要解得出來")
	}
	if !d.GuestStats.HomePaths[0].Steps[1].Played {
		t.Fatal("played")
	}
	f := d.Funnel()
	if f.Tracked != 110 || f.Ready != 101 || f.Played != 42 || f.Bots != 7 {
		t.Fatalf("%+v", f)
	}
	if core.SitePercent(42, 110) != "38%" || core.SitePercent(0, 0) != "—" {
		t.Fatal(core.SitePercent(42, 110))
	}
	// 輸出時 null 也是 []
	b := string(core.Marshal(d))
	if strings.Contains(b, "null") {
		t.Fatal(b)
	}
}

func TestSiteToleratesMissingFields(t *testing.T) {
	d, err := core.DecodeSiteOverview(map[string]any{"users": 1.0, "lessons": []any{map[string]any{"lesson": "x"}}, "extraField": true,
		"guests": "很多", "guestStats": map[string]any{"homeVisits": 2.5, "devices": []any{"壞的"}}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Users != 1 || d.Lessons[0].GuestTracked != 0 || d.GuestStats.HomeVisits != 0 || d.Guests != 0 || len(d.GuestStats.Devices) != 0 {
		t.Fatalf("%+v", d)
	}
	if _, err := core.DecodeSiteOverview([]any{}); err == nil {
		t.Fatal("最外層不是物件要失敗")
	}
}

func TestSiteRangePathsAndFormat(t *testing.T) {
	if core.SiteRangePath("") != "/api/admin/overview" || core.SiteRangePath("7d") != "/api/admin/overview?range=7d" {
		t.Fatal(core.SiteRangePath("7d"))
	}
	for ms, want := range map[int64]string{1500: "1.5 秒", 95000: "1 分 35 秒", 30000: "30 秒", 7_380_000: "2 小時 3 分"} {
		if got := core.SiteFmtMs(ms); got != want {
			t.Errorf("%d → %s,要 %s", ms, got, want)
		}
	}
}

func TestSiteMCPToolReadsThroughConnection(t *testing.T) {
	a := extTestApp(t)
	f := core.NewFakeNet(func(r *http.Request, body []byte) (int, any) {
		if r.Header.Get("Authorization") != "Bearer admin-token" {
			return 401, map[string]any{"error": "unauthorized"}
		}
		return 200, siteFixtureObj()
	})
	defer f.Close()

	// 還沒登入:回錯誤,不會當機
	if msg := extErr(t, a, "ops_site_visitors", nil); msg != "讀不到訪客統計:HTTP 401:還沒登入「evoleap 正式站」(請在 Ops 登入對應的連線)" {
		t.Fatal(msg)
	}
	if len(f.Requests()) != 0 {
		t.Fatal("沒登入不該送出請求")
	}
	if msg := extErr(t, a, "ops_site_visitors", map[string]any{"connection": "nope"}); msg != "讀不到訪客統計:找不到連線 nope(請在 Ops 登入對應的連線)" {
		t.Fatal(msg)
	}

	core.ConnSetBearer(a, "evoleap", "admin-token")
	out := extOK(t, a, "ops_site_visitors", map[string]any{"range": "7d"})
	if r, ok := f.Find("/api/admin/overview"); !ok || r.URL != "https://evoleap.io/api/admin/overview?range=7d" {
		t.Fatalf("%+v", r)
	}
	if out["funnel"].(map[string]any)["played"] != 42.0 || out["range"] != "7d" {
		t.Fatalf("%v", out)
	}
	ls := []string{}
	for _, l := range out["lessons"].([]any) {
		ls = append(ls, l.(map[string]any)["lesson"].(string))
	}
	if strings.Join(ls, ",") != "l0/0.0,l0/0.1" {
		t.Fatal(ls)
	}
	if g := out["gates"].([]any)[0].(map[string]any); g["views"] != 5.0 || g["path"] != "/courses/tools/0" {
		t.Fatal(g)
	}
	gs := out["guestStats"].(map[string]any)
	if gs["homeStaysTotal"] != 1.0 || gs["idleVisitsTotal"] != 1.0 || gs["idleVisits"].([]any)[0].(map[string]any)["openedAt"] != "2026-11-10T08:00:00Z" {
		t.Fatalf("%v", gs)
	}
	if all := extOK(t, a, "ops_site_visitors", map[string]any{"range": "all"}); all["range"] != "all" {
		t.Fatal(all["range"])
	}
	if r, _ := f.Find("/api/admin/overview"); r.URL != "https://evoleap.io/api/admin/overview" {
		t.Fatal(r.URL)
	}
	if def := extOK(t, a, "ops_site_visitors", nil); def["range"] != "1d" {
		t.Fatal(def["range"])
	}

	if msg := extErr(t, a, "ops_site_visitors", map[string]any{"range": "2y"}); msg != "range 必須是 all、30m、12h、1d、7d 之一" {
		t.Fatal(msg)
	}
	// token 失效
	core.ConnSetBearer(a, "evoleap", "old")
	if msg := extErr(t, a, "ops_site_visitors", nil); msg != "讀不到訪客統計:HTTP 401:登入已失效,請重新登入(請在 Ops 登入對應的連線)" {
		t.Fatal(msg)
	}
}

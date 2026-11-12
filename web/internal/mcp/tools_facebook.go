package mcp

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"omixor-web/internal/core"
)

// Facebook 粉絲專頁工具(只讀與草稿;沒有發佈工具),逐行對照 Swift MCPHandler.swift 的
// fbConnected、fbTargets、fbPages、fbPosts、fbInsights、fbDrafts、fbDraftSave。

func fbConnected() error { return ToolError{Message: core.FBNotConnected} }

// fbFail 同 Swift 的 fbFail:錯誤轉成工具錯誤(FBError 用 errorDescription)
func fbFail(err error) ToolError {
	if te, ok := err.(ToolError); ok {
		return te
	}
	return ToolError{Message: err.Error()}
}

func fbAvailable(pages []core.FBPage) string {
	names := []string{}
	for _, p := range pages {
		names = append(names, p.Name+"("+p.ID+")")
	}
	return strings.Join(names, "、")
}

// fbTargets page 參數 → 要看的專頁(管理中且有權杖)
func fbTargets(c *Call) ([]core.FBPage, error) {
	a := c.App
	if ref := c.Args.Str("page"); ref != "" {
		a.Mu.Lock()
		p := core.FBFindPage(a, ref)
		all := core.FBSettingsNow(a).Pages
		a.Mu.Unlock()
		if p == nil {
			return nil, Errorf("找不到專頁 %s。可用:%s", ref, fbAvailable(all))
		}
		if _, ok := core.FBPageToken(a, p.ID); !ok {
			return nil, ToolError{Message: "專頁「" + p.Name + "」沒有權杖。" + core.FBNotConnected}
		}
		return []core.FBPage{*p}, nil
	}
	a.Mu.Lock()
	managed := core.FBSettingsNow(a).ManagedPages()
	a.Mu.Unlock()
	out := []core.FBPage{}
	for _, p := range managed {
		if _, ok := core.FBPageToken(a, p.ID); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

var fbISORe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:?\d{2})$`)

// fbParseISO 同 Swift 的 ISO8601DateFormatter()(預設格式:沒有小數秒,要有時區)
func fbParseISO(s string) (time.Time, bool) {
	if !fbISORe.MatchString(s) {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func init() {
	Register("ops_fb_pages", func(c *Call) (any, error) {
		a := c.App
		if !core.FBIsConnected(a) {
			return nil, fbConnected()
		}
		errs := map[string]string{}
		if b, _ := c.Args.Bool("refresh"); b {
			a.Mu.Lock()
			managed := core.FBSettingsNow(a).ManagedPages()
			a.Mu.Unlock()
			for _, p := range managed {
				if _, ok := core.FBPageToken(a, p.ID); !ok {
					continue
				}
				if _, err := core.FBRefreshPage(a, p.ID); err != nil {
					errs[p.ID] = fbFail(err).Message
				}
			}
		}
		tokens := core.FBTokens(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		s := core.FBSettingsNow(a)
		pages := []any{}
		for _, p := range s.Pages {
			o := core.FBPageJSON(p, tokens[p.ID])
			if e, ok := errs[p.ID]; ok {
				o["error"] = e
			}
			pages = append(pages, o)
		}
		return map[string]any{"connected": true, "mode": s.Mode, "apiVersion": s.APIVersion, "pages": pages}, nil
	})

	Register("ops_fb_posts", func(c *Call) (any, error) {
		a := c.App
		if !core.FBIsConnected(a) {
			return nil, fbConnected()
		}
		limit := 10
		if n, ok := c.Args.Int("limit"); ok {
			limit = n
		}
		limit = min(50, max(1, limit))
		withInsights, withScheduled := true, true
		if b, ok := c.Args.Bool("insights"); ok {
			withInsights = b
		}
		if b, ok := c.Args.Bool("scheduled"); ok {
			withScheduled = b
		}
		targets, err := fbTargets(c)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, p := range targets {
			o := map[string]any{"pageId": p.ID, "pageName": p.Name}
			err := func() error {
				posts, err := core.FBPosts(a, p.ID, limit, withInsights)
				if err != nil {
					return err
				}
				list := []any{}
				for _, x := range posts {
					list = append(list, core.FBPostJSON(x))
				}
				o["posts"] = list
				sched := []any{}
				if withScheduled {
					s, err := core.FBScheduled(a, p.ID)
					if err != nil {
						return err
					}
					for _, x := range s {
						sched = append(sched, core.FBPostJSON(x))
					}
				}
				o["scheduled"] = sched
				return nil
			}()
			if err != nil {
				if _, ok := o["posts"]; !ok {
					o["posts"] = []any{}
				}
				if _, ok := o["scheduled"]; !ok {
					o["scheduled"] = []any{}
				}
				o["error"] = fbFail(err).Message
			}
			out = append(out, o)
		}
		return map[string]any{"pages": out}, nil
	})

	Register("ops_fb_insights", func(c *Call) (any, error) {
		a := c.App
		if !core.FBIsConnected(a) {
			return nil, fbConnected()
		}
		period := "day"
		if s, ok := c.Args.String("period"); ok {
			period = s
		}
		if !slicesContains(core.FBPeriods, period) {
			return nil, ToolError{Message: "period 必須是 " + strings.Join(core.FBPeriods, "、") + " 之一"}
		}
		days := 28
		if n, ok := c.Args.Int("days"); ok {
			days = n
		}
		days = min(90, max(1, days))
		targets, err := fbTargets(c)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, p := range targets {
			o := map[string]any{"pageId": p.ID, "pageName": p.Name, "period": period,
				"fanCount": intOrNil(p.FanCount), "followersCount": intOrNil(p.FollowersCount)}
			r, err := core.FBFetchPageInsights(a, p.ID, period, days)
			if err != nil {
				o["metrics"] = []any{}
				o["error"] = fbFail(err).Message
			} else {
				o["since"] = core.ISO(r.Since)
				o["until"] = core.ISO(r.Until)
				ms := []any{}
				for _, m := range r.Metrics {
					ms = append(ms, core.FBMetricJSON(m))
				}
				o["metrics"] = ms
			}
			out = append(out, o)
		}
		return map[string]any{"pages": out}, nil
	})

	Register("ops_fb_drafts", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if id := c.Args.Str("id"); id != "" {
			d := core.FBFindDraft(a, id)
			if d == nil {
				return nil, Errorf("找不到草稿 %s", id)
			}
			return core.FBDraftJSON(a, *d), nil
		}
		status := "draft"
		if s, ok := c.Args.String("status"); ok {
			status = s
		}
		if !slicesContains([]string{"draft", "scheduled", "published", "all"}, status) {
			return nil, ToolError{Message: "status 必須是 draft、scheduled、published、all 之一"}
		}
		list := []core.FBDraft{}
		for _, d := range core.FBSettingsNow(a).Drafts {
			if status == "all" || d.Status == status {
				list = append(list, d)
			}
		}
		if ref := c.Args.Str("page"); ref != "" {
			p := core.FBFindPage(a, ref)
			if p == nil {
				return nil, Errorf("找不到專頁 %s", ref)
			}
			kept := []core.FBDraft{}
			for _, d := range list {
				if d.PageID == p.ID {
					kept = append(kept, d)
				}
			}
			list = kept
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt.Time) })
		limit := 30
		if n, ok := c.Args.Int("limit"); ok {
			limit = n
		}
		limit = max(1, limit)
		drafts := []any{}
		for i, d := range list {
			if i >= limit {
				break
			}
			drafts = append(drafts, core.FBDraftJSON(a, d))
		}
		return map[string]any{"drafts": drafts, "total": len(list)}, nil
	})

	Register("ops_fb_draft_save", func(c *Call) (any, error) {
		a := c.App
		a.Mu.Lock()
		defer a.Mu.Unlock()
		author, err := issueAuthor(c)
		if err != nil {
			return nil, err
		}
		for _, k := range []string{"link", "image_url"} {
			if u := c.Args.Str(k); u != "" {
				l := strings.ToLower(u)
				if !strings.HasPrefix(l, "https://") && !strings.HasPrefix(l, "http://") {
					return nil, Errorf("%s 必須是 http 或 https 網址", k)
				}
			}
		}
		var proposed *time.Time
		if t := c.Args.Str("proposed_time"); t != "" {
			d, ok := fbParseISO(t)
			if !ok {
				return nil, ToolError{Message: "proposed_time 要用 ISO 8601,例如 2026-11-20T20:00:00+08:00"}
			}
			proposed = &d
		}
		var pageID *string
		if ref := c.Args.Str("page"); ref != "" {
			p := core.FBFindPage(a, ref)
			if p == nil {
				return nil, Errorf("找不到專頁 %s。可用:%s", ref, fbAvailable(core.FBSettingsNow(a).Pages))
			}
			pageID = &p.ID
		}
		var d core.FBDraft
		isNew := false
		if id := c.Args.Str("id"); id != "" {
			old := core.FBFindDraft(a, id)
			if old == nil {
				return nil, Errorf("找不到草稿 %s", id)
			}
			if old.Status != core.FBDraftDraft {
				return nil, Errorf("草稿 %s %s,不能修改", old.ID, core.FBStatusLabel(old.Status))
			}
			d = *old
		} else {
			d = core.NewFBDraft(core.FBNewDraftID(a), author)
			if m := core.FBSettingsNow(a).ManagedPages(); len(m) > 0 {
				d.PageID = m[0].ID
			}
			isNew = true
		}
		if v, ok := c.Args.RawString("message"); ok {
			d.Message = v
		}
		if v, ok := c.Args.String("link"); ok {
			d.Link = v
		}
		if v, ok := c.Args.String("image_url"); ok {
			d.ImageURL = v
		}
		if proposed != nil {
			d.ProposedTime = &core.Time{Time: *proposed}
		}
		if v, ok := c.Args.String("source"); ok {
			d.Source = v
		}
		if pageID != nil {
			d.PageID = *pageID
		}
		if d.IsEmpty() {
			return nil, ToolError{Message: "草稿至少要有 message、link 或 image_url"}
		}
		saved := core.FBSaveDraft(a, d, author)
		verb := "修改"
		if isNew {
			verb = "新增"
		}
		a.Log(core.AuthorActor(author), "square.and.pencil", verb+" Facebook 草稿 "+saved.ID+"「"+saved.TitleLine()+"」", "automation")
		o := core.FBDraftJSON(a, saved)
		o["note"] = "只存成草稿。發佈或排程要由人在 omixor 的 AI 自動化 › 粉絲專頁 按。"
		return o, nil
	})
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func intOrNil(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

package core

import (
	"path/filepath"
	"strings"
	"time"
)

// Facebook 粉絲專頁:組織鉤子、給畫面的 View facebook 與 Action(連線、選專頁、草稿 CRUD、發佈 / 排程 / 取消排程)。
// 發佈類 Action 以登入者(UserActor)署名;MCP 沒有發佈工具。

// fbView View facebook(拿著鎖呼叫,不能讀鑰匙圈:權杖只看快取)
func fbView(a *App) any {
	s := a.fbSt()
	tokens := map[string]bool{}
	connected := false
	for _, p := range s.Settings.Pages {
		has := fbHasTokenCached(a, p.ID)
		tokens[p.ID] = has
		if has && p.Managed {
			connected = true
		}
	}
	posts := map[string]any{}
	for id, list := range s.Snap.Posts {
		arr := []any{}
		for _, p := range list {
			arr = append(arr, FBPostJSON(p))
		}
		posts[id] = arr
	}
	scheduled := map[string]any{}
	for id, list := range s.Snap.Scheduled {
		arr := []any{}
		for _, p := range list {
			arr = append(arr, FBPostJSON(p))
		}
		scheduled[id] = arr
	}
	insights := map[string]any{}
	for id, r := range s.Snap.Insights {
		insights[id] = fbInsightsJSON(r)
	}
	drafts := []any{}
	for _, d := range s.Settings.Drafts {
		drafts = append(drafts, FBDraftJSON(a, d))
	}
	errs := map[string]string{}
	for k, v := range s.Snap.Errors {
		errs[k] = v
	}
	var conn any
	hasSecret := false
	if c, ok := ConnFind(a, FBConnectionID); ok {
		conn = c
		hasSecret = c.OAuth.HasClientSecret
	}
	return map[string]any{
		"settings": map[string]any{"apiVersion": s.Settings.APIVersion, "mode": s.Settings.Mode, "pages": s.Settings.Pages},
		"drafts":   drafts,
		"snapshot": map[string]any{"posts": posts, "scheduled": scheduled, "insights": insights, "errors": errs,
			"error": optStr(s.Snap.Error), "busy": s.Snap.Busy, "fetchedAt": optISO(s.Snap.FetchedAt), "tokenExpired": s.Snap.TokenExpired},
		"tokens": tokens, "connected": connected, "notConnected": FBNotConnected,
		"connection": conn, "hasAppSecret": hasSecret, "callback": FBCallbackURL, "scopes": FBScopes,
		"periods": FBPeriods, "minLeadSeconds": int(FBMinLead.Seconds()), "maxLeadSeconds": int(FBMaxLead.Seconds()),
	}
}

func fbArg(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}

func fbUserErr(err error) error {
	if err == nil {
		return nil
	}
	return UserError{err.Error()}
}

// fbParseISO 畫面送來的時間(ISO 8601)
func fbParseISO(k, s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := ParseTime(strings.TrimSpace(s))
	if err != nil {
		return nil, UserError{k + " 要用 ISO 8601,例如 2026-11-20T20:00:00+08:00"}
	}
	return &t, nil
}

// fbWarm 預熱權杖快取,讓 View(拿著鎖、只看快取)知道哪些專頁有權杖
func fbWarm(a *App) { _ = FBTokens(a) }

func init() {
	OnOrgLoad(func(a *App) {
		fbLoad(a)
		if BackgroundAccountCheck {
			go fbWarm(a) // 鑰匙圈在鎖外讀
		}
	})
	OnOrgDelete(func(a *App, id, dir string) {
		// 這個組織在鑰匙圈裡的專頁權杖一併清掉(鑰匙圈是外部程式,放到鎖外做)
		pages := readFBSettings(filepath.Join(dir, "facebook.json")).Pages
		scope := orgScope(id)
		go func() {
			for _, p := range pages {
				vaultSet(fbPageAccount(scope, p.ID), nil)
			}
		}()
	})

	RegisterView("facebook", fbView)

	RegisterAction("facebook_ensure_connection", func(a *App, args map[string]any) (any, error) {
		return FBEnsureConnection(a), nil
	})
	// {appId, secret?}:Meta App 的 App ID 與 App secret
	RegisterAction("facebook_app", func(a *App, args map[string]any) (any, error) {
		id := fbArg(args, "appId")
		if strings.TrimSpace(id) == "" {
			a.Mu.Lock()
			if c, ok := ConnFind(a, FBConnectionID); ok {
				id = c.OAuth.ClientID
			}
			a.Mu.Unlock()
		}
		secret := strings.TrimSpace(fbArg(args, "secret"))
		FBSetApp(a, id, &secret)
		fbWarm(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return fbView(a), nil
	})
	// Meta 登入(開瀏覽器、等回呼,完成才回傳)
	RegisterAction("facebook_login", func(a *App, args map[string]any) (any, error) {
		if err := FBLoginWithMeta(a, UserActor(a, args)); err != nil {
			return nil, fbUserErr(err)
		}
		fbWarm(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return fbView(a), nil
	})
	// {token}:貼上 Page access token 或使用者權杖
	RegisterAction("facebook_token", func(a *App, args map[string]any) (any, error) {
		if err := FBAddToken(a, fbArg(args, "token"), UserActor(a, args)); err != nil {
			return nil, fbUserErr(err)
		}
		fbWarm(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return fbView(a), nil
	})
	RegisterAction("facebook_disconnect", func(a *App, args map[string]any) (any, error) {
		FBDisconnect(a, UserActor(a, args))
		fbWarm(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return fbView(a), nil
	})
	// {pageId, managed}:要不要在 Ops 管理這個專頁
	RegisterAction("facebook_managed", func(a *App, args map[string]any) (any, error) {
		id := fbArg(args, "pageId")
		on, _ := args["managed"].(bool)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if !FBSetManaged(a, id, on) {
			return nil, UserError{"找不到專頁 " + id}
		}
		return fbView(a), nil
	})
	// 重新讀所有管理中專頁的貼文、排程與成效(等讀完才回傳)
	RegisterAction("facebook_refresh", func(a *App, args map[string]any) (any, error) {
		FBRefreshAll(a)
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return fbView(a), nil
	})
	// {pageId, period?, days?}:專頁成效(畫面切換期間用)
	RegisterAction("facebook_insights", func(a *App, args map[string]any) (any, error) {
		days := 28
		if n, ok := jNum(args["days"]); ok {
			days = int(n)
		}
		period := fbArg(args, "period")
		if period == "" {
			period = "day"
		}
		r, err := FBFetchPageInsights(a, fbArg(args, "pageId"), period, days)
		if err != nil {
			return nil, fbUserErr(err)
		}
		return fbInsightsJSON(r), nil
	})

	// MARK: 草稿 CRUD(人署名)

	RegisterAction("facebook_draft_new", func(a *App, args map[string]any) (any, error) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		me := ActorAuthor(UserActor(a, args))
		d := NewFBDraft(FBNewDraftID(a), me)
		if m := a.fbSt().Settings.ManagedPages(); len(m) > 0 {
			d.PageID = m[0].ID
		}
		return FBDraftJSON(a, FBSaveDraft(a, d, me)), nil
	})
	// {id, pageId?, message?, link?, imageURL?, localImage?, proposedTime?(ISO 或空字串清掉), source?}:只能改還是草稿的
	RegisterAction("facebook_draft_update", func(a *App, args map[string]any) (any, error) {
		id := fbArg(args, "id")
		var proposed *time.Time
		clearProposed := false
		if v, ok := args["proposedTime"]; ok {
			s, _ := v.(string)
			t, err := fbParseISO("proposedTime", s)
			if err != nil {
				return nil, err
			}
			proposed, clearProposed = t, t == nil
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		me := ActorAuthor(UserActor(a, args))
		d := FBFindDraft(a, id)
		if d == nil {
			return nil, UserError{"找不到草稿 " + id}
		}
		if d.Status != FBDraftDraft {
			return nil, UserError{"草稿 " + d.ID + " " + FBStatusLabel(d.Status) + ",不能修改"}
		}
		for k, p := range map[string]*string{"pageId": &d.PageID, "message": &d.Message, "link": &d.Link,
			"imageURL": &d.ImageURL, "localImage": &d.LocalImage, "source": &d.Source} {
			if v, ok := args[k].(string); ok {
				*p = v
			}
		}
		if proposed != nil {
			d.ProposedTime = &Time{proposed.UTC()}
		} else if clearProposed {
			d.ProposedTime = nil
		}
		return FBDraftJSON(a, FBSaveDraft(a, *d, me)), nil
	})
	RegisterAction("facebook_draft_delete", func(a *App, args map[string]any) (any, error) {
		id := fbArg(args, "id")
		a.Mu.Lock()
		defer a.Mu.Unlock()
		d := FBFindDraft(a, id)
		if d == nil {
			return nil, UserError{"找不到草稿 " + id}
		}
		FBDeleteDraft(a, d.ID)
		return map[string]any{}, nil
	})

	// MARK: 發佈(只有人能按,以登入者署名)

	// {id, schedule?(ISO 8601)}:立即發佈或排程,等送完才回傳 {postId, draft}
	RegisterAction("facebook_publish", func(a *App, args map[string]any) (any, error) {
		when, err := fbParseISO("schedule", fbArg(args, "schedule"))
		if err != nil {
			return nil, err
		}
		id := fbArg(args, "id")
		post, err := FBPublish(a, id, when, time.Now(), UserActor(a, args))
		if err != nil {
			return nil, fbUserErr(err)
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		out := map[string]any{"postId": post, "draft": nil}
		if d := FBFindDraft(a, id); d != nil {
			out["draft"] = FBDraftJSON(a, *d)
		}
		if when == nil {
			out["message"] = "已發佈(" + post + ")"
		} else {
			out["message"] = "已排程:" + FBTaipeiString(*when)
		}
		return out, nil
	})
	// {postId, pageId}:取消排程貼文
	RegisterAction("facebook_cancel_scheduled", func(a *App, args map[string]any) (any, error) {
		if err := FBCancelScheduled(a, fbArg(args, "postId"), fbArg(args, "pageId"), UserActor(a, args)); err != nil {
			return nil, fbUserErr(err)
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		return fbView(a), nil
	})
}

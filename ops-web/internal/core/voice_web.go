package core

// 旁白批次給畫面的 View、Action 與徽章(CI/CD › 旁白發佈,同 Swift 的 PipeVoiceTab)。
//
//	View   voice_jobs      [VoiceJob](完整內容,含 baseline)
//	View   voice_settings  {connection, pollMinutes, shuttlePath, target: {id,name,baseURL,production} 或 null, hasToken}
//	Action voice_publish   {id}  發佈(只能由人按;等全部送完才回傳)→ VoiceJob
//	Action voice_cancel    {id}  取消排隊中或失敗的批次 → VoiceJob
//	Action voice_retry     {id}  立即檢查(Swift 的「立即檢查」按鈕)→ VoiceJob
//	Action voice_discard   {id}  從清單移除已結束的批次(Go 版才有)→ {}
//	Action voice_settings  {connection?, pollMinutes?, shuttlePath?} → 同 View voice_settings
// 送出與試算用 POST /api/tool/ops_voice_submit(dry_run)。

func init() {
	RegisterView("voice_jobs", func(a *App) any {
		p := VoiceOf(a)
		if p == nil || p.Jobs == nil {
			return []VoiceJob{}
		}
		return p.Jobs
	})
	RegisterView("voice_settings", func(a *App) any { return voiceSettingsView(a) })
	OnBadge(func(a *App) map[string]int {
		p := VoiceOf(a)
		if p == nil {
			return nil
		}
		n := 0
		for _, j := range p.Jobs {
			if j.State == VoiceReady {
				n++
			}
		}
		return map[string]int{"pipelines": n}
	})

	jobAction := func(name string, f func(p *VoicePipeline, id string, by Actor)) {
		RegisterAction(name, func(a *App, args map[string]any) (any, error) {
			p := VoiceOf(a)
			if p == nil {
				return nil, UserError{"旁白功能還沒啟動"}
			}
			id, _ := args["id"].(string)
			a.Mu.Lock()
			found := p.Job(id) != nil
			a.Mu.Unlock()
			if !found {
				return nil, UserError{"找不到旁白批次 " + id}
			}
			f(p, id, UserActor(a, args))
			a.Mu.Lock()
			defer a.Mu.Unlock()
			if j := p.Job(id); j != nil {
				return *j, nil
			}
			return map[string]any{}, nil
		})
	}
	jobAction("voice_publish", func(p *VoicePipeline, id string, by Actor) { p.PublishAs(id, by) })
	jobAction("voice_cancel", func(p *VoicePipeline, id string, _ Actor) {
		p.app.Mu.Lock()
		p.Cancel(id)
		p.app.Mu.Unlock()
	})
	jobAction("voice_retry", func(p *VoicePipeline, id string, _ Actor) { p.CheckNow(id) })
	jobAction("voice_discard", func(p *VoicePipeline, id string, _ Actor) {
		p.app.Mu.Lock()
		p.Discard(id)
		p.app.Mu.Unlock()
	})
	RegisterAction("voice_settings", func(a *App, args map[string]any) (any, error) {
		p := VoiceOf(a)
		if p == nil {
			return nil, UserError{"旁白功能還沒啟動"}
		}
		a.Mu.Lock()
		defer a.Mu.Unlock()
		if s, ok := args["connection"].(string); ok {
			p.Settings.Connection = s
		}
		if s, ok := args["shuttlePath"].(string); ok {
			p.Settings.ShuttlePath = s
		}
		if v, ok := args["pollMinutes"]; ok {
			n, ok := v.(float64)
			if !ok {
				if i, ok2 := v.(int64); ok2 {
					n, ok = float64(i), true
				}
			}
			if !ok || n < 1 || n > 120 {
				return nil, UserError{"pollMinutes 必須是 1–120 的整數"}
			}
			p.Settings.PollMinutes = int(n)
		}
		p.SaveSettings()
		go a.Changed("voice_settings")
		return voiceSettingsView(a), nil
	})
}

func voiceSettingsView(a *App) any {
	p := VoiceOf(a)
	if p == nil {
		return nil
	}
	out := map[string]any{"connection": p.TargetID(), "pollMinutes": p.PollMinutes(), "shuttlePath": p.ShuttleRoot(),
		"target": nil, "hasToken": false}
	if t, ok := p.Target(); ok {
		out["target"] = map[string]any{"id": t.ID, "name": t.Name, "baseURL": t.BaseURL, "production": t.Production}
		out["hasToken"] = p.connector().HasTokenCached(a, t.ID)
	}
	return out
}

// Discard 從清單移除已結束的批次(Go 版才有;呼叫時拿著 Mu)
func (p *VoicePipeline) Discard(id string) {
	for i, j := range p.Jobs {
		if j.ID == id && j.State.IsFinal() {
			p.Jobs = append(p.Jobs[:i:i], p.Jobs[i+1:]...)
			p.save()
			return
		}
	}
}

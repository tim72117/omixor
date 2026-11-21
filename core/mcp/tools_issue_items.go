package mcp

import (
	"encoding/json"
	"fmt"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項(issue_item,ISS-37)工具:issue_item_add(新增)、issue_items(查)、issue_item_update(改、取消)、issue_item_claim(領取)、
// issue_item_report(回報進度、完成、卡住)、issue_item_promote(提升為 Issue)。規則、可見性與授權在 service/issue_items.go(透過 Backend)。
// 子項的編號是 <Issue key>.<序號>,例如 ISS-37.2;可見性完全繼承所屬 Issue(受限專案非成員看不到,單筆「找不到」)。
// 角色端點(子代理)開放 issue_item_add、issue_items、issue_item_report:可以提出下一步、看清單、回報自己綁定任務的子項;
// 領取、改、取消、提升只有人。署名一律是呼叫者本人,工具沒有 agent、author 參數。
// issue_block / issue_unblock(Issue 級卡住旗標)這一版沒有做:要動 Issue 的結構與遷移,範圍比子項大,見 ISS-37 的說明。

// ItemToolDefs 子項工具的規格(接在 CoreToolDefs 後面)
func ItemToolDefs() []any {
	itemID := "子項編號(例如 ISS-37.2,Issue key 加序號)"
	return []any{
		coreDef("issue_item_add",
			"在 Issue 底下新增一個子項(待辦清單的一條)。門檻:只有「跨階段、跨子代理、或需要人決定」的工作才開子項,太細的不要開。"+
				"子代理做到一半發現下一步(或需要拆給別的子代理的工作)就用這個提出,不要只寫在對話裡。回傳子項(含編號 <Issue key>.<序號>)。"+
				"depends_on 指同一張 Issue 的前置子項(編號、序號或 id;未完成前置的子項不能領取、也不能完成,依賴不能成環);files 是會動到的檔案(讓別人避開衝突);acceptance 是驗收標準。"+
				"assignee 是負責人(人的 email 或 handle、角色 agent:<handle>);負責人是角色時,領取會自動替它排任務並發憑證。角色端點新增時 assignee 只能留空或是自己。", false,
			obj(map[string]any{
				"issue":      pStr("所屬 Issue(例如 ISS-37)"),
				"title":      pStr("一句話說明這個子項要做什麼"),
				"files":      pStrs("會動到的檔案或模組"),
				"acceptance": pStr("驗收標準(完成的條件)"),
				"assignee":   pStr("負責人:人(email、@handle)或角色(agent:<handle>);空 = 沒指派"),
				"depends_on": pStr("前置子項(同一張 Issue 的編號、序號或 id)"),
			}, "issue", "title")),
		coreDef("issue_items",
			"查 Issue 的子項(待辦清單)。給 id(例如 ISS-37.2)回單一筆;給 issue 列那張 Issue 的子項,預設只回未完成(todo / doing / blocked),include_done=true 連完成與取消的一起,"+
				"status 指定單一狀態。每筆有狀態、負責人、領取者、前置、files、驗收標準、卡住原因與等待對象、完成證據。", true,
			obj(map[string]any{
				"id":           pStr(itemID + ";給了就只回這一筆"),
				"issue":        pStr("Issue 編號(列表用;給 id 時可省略)"),
				"include_done": pBool("連 done、canceled 一起列(預設只列未完成)"),
				"status":       pEnum("只列這個狀態", model.ItemStatuses...),
			})),
		coreDef("issue_item_update",
			"修改子項的標題、files、驗收標準、負責人、前置(只有人;角色端點不可)。assignee、depends_on 給空字串是清掉。cancel=true 是取消這個子項(放棄這項工作,不要用刪除表示;綁定的角色任務一併取消)。完成或取消的子項不能再改。", false,
			obj(map[string]any{
				"id":         pStr(itemID),
				"title":      pStr("新標題"),
				"files":      pStrs("會動到的檔案(整份取代)"),
				"acceptance": pStr("驗收標準"),
				"assignee":   pStr("負責人(空字串 = 清掉)"),
				"depends_on": pStr("前置子項(空字串 = 清掉)"),
				"cancel":     pBool("true = 取消這個子項"),
			}, "id")),
		coreDef("issue_item_claim",
			"領取子項(開始做):狀態變 doing、你成為領取者、租約到期前別人不能再領;前置未完成回 409。負責人是人或沒指派時只做輕量領取;"+
				"負責人是角色時會自動建立並領取一筆綁定的角色任務,回傳一次性憑證(credential;連角色端點以該角色身分工作,做完在角色端點用 issue_item_report 回報)。"+
				"卡住(blocked)的子項要先解除(issue_item_report status=todo)。只有人;角色端點不可。", false,
			obj(map[string]any{
				"id":   pStr(itemID),
				"note": pStr("領取備註(例如是哪個 session、子代理;記在稽核)"),
			}, "id")),
		coreDef("issue_item_report",
			"回報子項:status=done(完成;可附 evidence 證據——JSON 物件如 {commit, tests, unverified:[…]} 或一段文字——與 note 備註)、"+
				"blocked(卡住;一定要 block_reason;需要人決定時 wait_for_kind=user_decision 並在 wait_for 寫是什麼決定(例如 DEC-n 或問題),不要猜答案;"+
				"wait_for_kind=item 時 wait_for 是同 Issue 的另一個子項;external 是外部條件)、doing(續約;沒領取時等於領取)、todo(放回待領取,或解除卡住;角色端點不可)。"+
				"人:沒被別人持有有效租約,或是領取者本人、組織管理員才能回報;角色端點:只能回報自己綁定任務的子項(用 issue_item_claim 領取時回傳的憑證)。完成需要前置已完成。", false,
			obj(map[string]any{
				"id":            pStr(itemID),
				"status":        pEnum("todo、doing、blocked 或 done", model.ItemTodo, model.ItemDoing, model.ItemBlocked, model.ItemDone),
				"evidence":      map[string]any{"description": "完成證據:JSON 物件({commit, tests, unverified[]…})或一段文字"},
				"note":          pStr("備註(完成時併進證據,並在 Issue 留一則系統留言)"),
				"block_reason":  pStr("卡住的原因(blocked 必填)"),
				"wait_for_kind": pEnum("在等什麼", model.ItemWaitKinds...),
				"wait_for":      pStr("等待對象:user_decision = 問題或 DEC-n、item = 同 Issue 的子項、external = 說明"),
			}, "id", "status")),
		coreDef("issue_item_promote",
			"把子項提升為獨立的 Issue(子項太大、或值得獨立追蹤時):建立新 Issue(標題 = 子項標題,內文帶驗收標準與檔案,source 記子項編號),"+
				"原子項標為取消並記下新 Issue,兩張 Issue 各留一則系統留言記關聯。只有人;角色端點不可。", false,
			obj(map[string]any{
				"id":       pStr(itemID),
				"priority": pEnum("新 Issue 的優先度(預設同來源 Issue)", IssuePriorities...),
				"labels":   pStrs("新 Issue 的標籤"),
				"project":  pStr("新 Issue 放進的專案 id(空字串 = 組織層級;沒給 = 同來源 Issue)"),
			}, "id")),
	}
}

func itemRefs(c *Call) (issue, item string, err error) {
	id, err := c.Args.Require("id")
	if err != nil {
		return "", "", err
	}
	issue, item = service.SplitItemKey(id)
	if issue == "" {
		return "", "", Errorf("id 要是子項編號,例如 ISS-37.2(Issue key 加序號)")
	}
	return issue, item, nil
}

func itemErr(ref string, err error) error {
	if backend.NotFound(err) && ref != "" {
		return Errorf("找不到 %s", ref)
	}
	return terr(err)
}

// evidenceArg evidence 參數:字串或物件 → JSON
func evidenceArg(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, Errorf("evidence 要是 JSON 物件或字串")
	}
	return b, nil
}

// RegisterItemTools 登記子項工具。執行環境(Call.Ctx)要實作 Env
func RegisterItemTools(s *Server) {
	s.Register("issue_item_add", func(c *Call) (any, error) {
		e := env(c)
		issue, err := c.Args.Require("issue")
		if err != nil {
			return nil, err
		}
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		files, _ := c.Args.Strings("files")
		it, err := e.Backend().AddIssueItem(e.Org(), issue, service.ItemInput{Title: title, Files: files, Acceptance: c.Args.Str("acceptance"),
			Assignee: c.Args.Str("assignee"), DependsOn: c.Args.Str("depends_on")})
		if err != nil {
			return nil, itemErr(issue, err)
		}
		return it, nil
	})

	s.Register("issue_items", func(c *Call) (any, error) {
		e := env(c)
		bk, org := e.Backend(), e.Org()
		if id := c.Args.Str("id"); id != "" {
			issue, item := service.SplitItemKey(id)
			if issue == "" {
				return nil, Errorf("id 要是子項編號,例如 ISS-37.2(Issue key 加序號)")
			}
			it, err := bk.GetIssueItem(org, issue, item)
			if err != nil {
				return nil, itemErr(id, err)
			}
			return it, nil
		}
		issue, err := c.Args.Require("issue")
		if err != nil {
			return nil, err
		}
		status, err := c.Args.OptionalEnum("status", model.ItemStatuses)
		if err != nil {
			return nil, err
		}
		done, _ := c.Args.Bool("include_done")
		list, err := bk.ListIssueItems(org, issue, service.ItemListQuery{IncludeDone: done, Status: status})
		if err != nil {
			return nil, itemErr(issue, err)
		}
		open := 0
		for _, it := range list {
			if !model.ItemTerminal(it.Status) {
				open++
			}
		}
		return map[string]any{"issue": issue, "total": len(list), "open": open, "items": list}, nil
	})

	s.Register("issue_item_update", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		if cancel, _ := c.Args.Bool("cancel"); cancel {
			it, err := e.Backend().CancelIssueItem(e.Org(), issue, item)
			if err != nil {
				return nil, itemErr(id, err)
			}
			return it, nil
		}
		in := service.ItemPatch{Title: c.Args.Opt("title"), Acceptance: c.Args.Opt("acceptance"), Assignee: c.Args.Opt("assignee"), DependsOn: c.Args.Opt("depends_on")}
		if _, ok := c.Args.Raw["files"]; ok {
			files, _ := c.Args.Strings("files")
			if files == nil {
				files = []string{}
			}
			in.Files = &files
		}
		it, err := e.Backend().UpdateIssueItem(e.Org(), issue, item, in)
		if err != nil {
			return nil, itemErr(id, err)
		}
		return it, nil
	})

	s.Register("issue_item_claim", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		res, err := e.Backend().ClaimIssueItem(e.Org(), issue, item, service.ItemClaimInput{Note: c.Args.Str("note")})
		if err != nil {
			if service.Code(err) == 409 { // 含「已被領取、處理中,鎖定到 hh:mm」:原文轉給呼叫端
				return nil, prefixErr("領取失敗:", err)
			}
			return nil, itemErr(id, err)
		}
		out := map[string]any{"item": res.Item}
		msg := "已領取" + id + "。"
		if res.Item.LockedUntil != nil {
			msg += "已鎖定到 " + res.Item.LockedUntil.Time.Local().Format("15:04") + "(其他人不能再領);長時間工作請定期用 issue_item_report(status: doing)續約。"
		}
		if res.Task != nil {
			out["task"] = toMap(*res.Task)
		}
		if cr := res.Credential; cr != nil {
			m := map[string]any{"agentId": cr.AgentID, "secret": cr.Secret, "expiresAt": cr.ExpiresAt}
			if ae, ok := c.Ctx.(AgentEnv); ok {
				if u := ae.EndpointURL(); u != "" {
					m["endpoint"] = u
				}
			}
			out["credential"] = m
			msg += "負責人是角色:已建立並領取綁定的任務 " + res.Task.ID + "。要以該角色身分工作,用 credential 連角色端點(Authorization: Bearer <agentId>:<secret>);做完用 issue_item_report 回報。"
		}
		out["message"] = msg
		return out, nil
	})

	s.Register("issue_item_report", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		status, err := c.Args.RequireEnum("status", []string{model.ItemTodo, model.ItemDoing, model.ItemBlocked, model.ItemDone})
		if err != nil {
			return nil, err
		}
		ev, err := evidenceArg(c.Args.Raw["evidence"])
		if err != nil {
			return nil, err
		}
		it, err := e.Backend().ReportIssueItem(e.Org(), issue, item, service.ItemReport{Status: status, Evidence: ev, Note: c.Args.Str("note"),
			BlockReason: c.Args.Str("block_reason"), WaitForKind: c.Args.Str("wait_for_kind"), WaitFor: c.Args.Str("wait_for")})
		if err != nil {
			return nil, itemErr(id, err)
		}
		return it, nil
	})

	s.Register("issue_item_promote", func(c *Call) (any, error) {
		e := env(c)
		issue, item, err := itemRefs(c)
		if err != nil {
			return nil, err
		}
		id := c.Args.Str("id")
		priority, err := c.Args.OptionalEnum("priority", IssuePriorities)
		if err != nil {
			return nil, err
		}
		labels, _ := c.Args.Strings("labels")
		in := service.ItemPromoteInput{Priority: priority, Labels: labels}
		if v, ok := c.Args.Raw["project"]; ok { // 有給(含空字串 = 組織層級)就明確指定;沒給 = 同來源 Issue
			prj, _ := ScalarString(v)
			in.Project = &prj
		}
		r, err := e.Backend().PromoteIssueItem(e.Org(), issue, item, in)
		if err != nil {
			return nil, itemErr(id, err)
		}
		return map[string]any{"issue": issueJSON(e, *r.Issue), "item": r.Item,
			"message": fmt.Sprintf("已提升為 %s,%s 標為取消並記下關聯", r.Issue.Key, id)}, nil
	})
}

package mcp

import (
	"encoding/json"
	"fmt"

	"omixor-core/backend"
	"omixor-core/model"
	"omixor-core/service"
)

// 待決定 / 需要協助工具:issue_decision_add(提出)、issue_decisions(查)、issue_decision_answer(處理,只有人)、
// issue_decision_to_issue(由它開出問題 Issue)。規則與可見性在 service/decision.go(透過 Backend)。
// 角色端點(子代理)只開放 issue_decision_add 與 issue_decisions:可以提出、不能處理。
// 署名一律是呼叫者本人(人類端點是登入的人、角色端點是憑證的角色),工具沒有 agent、author 參數(人類端點不能指定身分,見 identity_agent.go)。
// 有來源 Issue 的決定依該 Issue 的專案可見性篩選(看不到的單筆回「找不到」、列表沒有)。

// DecisionToolDefs 待決定 / 需要協助工具的規格(接在 CoreToolDefs 後面)
func DecisionToolDefs() []any {
	return []any{
		coreDef("issue_decision_add",
			"提出一筆「需要人」的事。兩種用途(kind):\n"+
				"- decision(預設):需要人從幾個選項裡選一個。要給 options(至少 2 個,每個 {label, description?, recommended?, id?},有建議就把 recommended 設 true);人也可以選「其他」附說明。\n"+
				"- help:需要人做某件事或提供資訊(例如 Cloud SQL 備份、在 Mac 上編譯、提供憑證、做一次實機驗證),沒有選項;不能帶 options。人處理後會回報完成(可附證據)或不處理(附原因)。\n"+
				"子代理遇到需要人做的事就建 help、需要人選擇就建 decision,不要把待決定寫在 Issue 描述或留言裡。source_issue 填來源 Issue(例如 ISS-37),blocks 填被它擋住的 Issue 或說明。回傳 DEC-n。", false,
			obj(map[string]any{
				"kind":         pEnum("decision(要人選擇,預設)或 help(要人做事)", model.DecisionKinds...),
				"title":        pStr("一句話的問題或請求"),
				"body":         pStr("背景說明(Markdown)"),
				"options":      pObjs("decision 的選項:[{label, description?, recommended?, id?}],至少 2 個;help 不要給"),
				"source_issue": pStr("來源 Issue(例如 ISS-37;選填)"),
				"blocks":       pStrs("被它擋住的 Issue key 或說明"),
			}, "title")),
		coreDef("issue_decisions",
			"查待決定(decision)與需要協助(help)。預設列出開放中的(status=open,「待處理」),兩種都含;給 id 回單一筆。可依 kind、來源 Issue(issue)、狀態篩選;mine=true 是「待我處理」(開放中、我可以處理的)。", true,
			obj(map[string]any{
				"id":     pStr("DEC-n(給了就只回這一筆)"),
				"status": pEnum("狀態:open(預設)、decided(已決定 / 已處理)、withdrawn(已撤回)或 all", "open", "decided", "withdrawn", "all"),
				"kind":   pEnum("只列這一種", model.DecisionKinds...),
				"issue":  pStr("只列來源是這張 Issue 的(例如 ISS-37)"),
				"mine":   pBool("待我處理:開放中、兩種都含(角色憑證沒有「我」,回空)"),
				"limit":  pInt("最多幾筆,預設 50、最多 200"),
			})),
		coreDef("issue_decision_answer",
			"處理一筆待決定或需要協助(只有人;角色憑證不可)。decision:answer 是選項 id,或 other 並附 note;help:answer 是 done(可附 note 與 evidence 證據字串)或 declined(要附 note 原因)。已處理的不能再改,要改請組織管理員或原處理人重新開啟(reopen:HTTP API)。結論會自動寫成來源 Issue 的留言(署名是你)。", false,
			obj(map[string]any{
				"id":       pStr("DEC-n"),
				"answer":   pStr("decision:選項 id 或 other;help:done 或 declined"),
				"note":     pStr("說明(選 other、declined 時必填)"),
				"evidence": pStr("help 完成的證據(連結、輸出、版本號;選填)"),
			}, "id", "answer")),
		coreDef("issue_decision_to_issue",
			"由一筆待決定 / 需要協助開出一張問題 Issue(標題 = 問題,內文含背景與選項,source 記 DEC-n),並把它的來源 Issue 指向新 Issue。已經有來源 Issue 的不能再開(只有人;角色憑證不可)。", false,
			obj(map[string]any{
				"id":       pStr("DEC-n"),
				"priority": pEnum("優先度,預設 normal", IssuePriorities...),
				"labels":   pStrs("標籤,預設 [待決定] 或 [需要協助]"),
				"project":  pStr("新 Issue 放進的專案 id(例如 PRJ-1;空字串 = 組織層級;沒給依 repo 的連結規則,同 issue_create)"),
				"repo":     pStr("新 Issue 的 repo(例如 owner/name;選填;沒指定專案時用來決定預設專案)"),
			}, "id")),
	}
}

func decisionErr(id string, err error) error {
	if backend.NotFound(err) && id != "" {
		return Errorf("找不到 %s", id)
	}
	return terr(err)
}

// decisionJSON 輸出:建立者、處理人的相容字串換成顯示名稱(createdByActor、decidedByActor 物件照原樣)
func decisionJSON(e Env, d model.Decision) any {
	b, _ := json.Marshal(d)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["createdBy"] = e.AuthorName(d.CreatedBy)
	if d.DecidedBy != "" {
		m["decidedBy"] = e.AuthorName(d.DecidedBy)
	}
	return m
}

// parseOptions 選項:物件 {label, description, recommended, id},或只給字串(當 label)
func parseOptions(raw any) ([]model.DecisionOption, error) {
	list, ok := raw.([]any)
	if raw == nil {
		return nil, nil
	}
	if !ok {
		return nil, Errorf("options 要是陣列")
	}
	out := []model.DecisionOption{}
	for k, x := range list {
		switch v := x.(type) {
		case string:
			out = append(out, model.DecisionOption{Label: v})
		case map[string]any:
			o := model.DecisionOption{}
			o.ID, _ = ScalarString(v["id"])
			o.Label, _ = ScalarString(v["label"])
			o.Description, _ = ScalarString(v["description"])
			switch r := v["recommended"].(type) {
			case bool:
				o.Recommended = r
			case string:
				o.Recommended = r == "true"
			}
			out = append(out, o)
		default:
			return nil, Errorf("options[%d] 要是物件 {label, description?, recommended?}", k)
		}
	}
	return out, nil
}

// RegisterDecisionTools 登記待決定 / 需要協助工具。執行環境(Call.Ctx)要實作 Env
func RegisterDecisionTools(s *Server) {
	s.Register("issue_decision_add", func(c *Call) (any, error) {
		e := env(c)
		title, err := c.Args.Require("title")
		if err != nil {
			return nil, err
		}
		kind, err := c.Args.OptionalEnum("kind", model.DecisionKinds)
		if err != nil {
			return nil, err
		}
		opts, err := parseOptions(c.Args.Raw["options"])
		if err != nil {
			return nil, err
		}
		body, _ := c.Args.RawString("body")
		blocks, _ := c.Args.Strings("blocks")
		d, err := e.Backend().AddDecision(e.Org(), service.DecisionInput{Kind: kind, Title: title, Body: body, Options: opts,
			SourceIssue: c.Args.Str("source_issue"), Blocks: blocks})
		if err != nil {
			return nil, terr(err)
		}
		return decisionJSON(e, *d), nil
	})

	s.Register("issue_decisions", func(c *Call) (any, error) {
		e := env(c)
		bk, org := e.Backend(), e.Org()
		if id := c.Args.Str("id"); id != "" {
			d, err := bk.GetDecision(org, id)
			if err != nil {
				return nil, decisionErr(id, err)
			}
			return decisionJSON(e, *d), nil
		}
		status, err := c.Args.OptionalEnum("status", []string{"open", "decided", "withdrawn", "all"})
		if err != nil {
			return nil, err
		}
		if status == "" {
			status = model.DecisionOpen
		}
		if status == "all" {
			status = ""
		}
		kind, err := c.Args.OptionalEnum("kind", model.DecisionKinds)
		if err != nil {
			return nil, err
		}
		mine, _ := c.Args.Bool("mine")
		limit, ok := c.Args.Int("limit")
		if !ok || limit <= 0 {
			limit = 50
		}
		limit = min(limit, 200)
		list, err := bk.ListDecisions(org, service.DecisionListQuery{Status: status, Kind: kind, Issue: c.Args.Str("issue"), Mine: mine})
		if err != nil {
			return nil, terr(err)
		}
		total := len(list)
		if len(list) > limit {
			list = list[:limit]
		}
		out := []any{}
		for _, d := range list {
			out = append(out, decisionJSON(e, d))
		}
		return map[string]any{"total": total, "decisions": out}, nil
	})

	s.Register("issue_decision_answer", func(c *Call) (any, error) {
		e := env(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		answer, err := c.Args.Require("answer")
		if err != nil {
			return nil, err
		}
		d, err := e.Backend().AnswerDecision(e.Org(), id, service.DecisionAnswer{Answer: answer, Note: c.Args.Str("note"),
			Evidence: c.Args.Str("evidence")})
		if err != nil {
			return nil, decisionErr(id, err)
		}
		return decisionJSON(e, *d), nil
	})

	s.Register("issue_decision_to_issue", func(c *Call) (any, error) {
		e := env(c)
		id, err := c.Args.Require("id")
		if err != nil {
			return nil, err
		}
		priority, err := c.Args.OptionalEnum("priority", IssuePriorities)
		if err != nil {
			return nil, err
		}
		labels, _ := c.Args.Strings("labels")
		in := service.DecisionToIssueInput{Priority: priority, Labels: labels, Repo: c.Args.Str("repo")}
		if v, ok := c.Args.Raw["project"]; ok { // 有給(含空字串 = 組織層級)就明確指定;沒給依 repo 的連結規則
			prj, _ := ScalarString(v)
			in.Project = &prj
		}
		r, err := e.Backend().DecisionToIssue(e.Org(), id, in)
		if err != nil {
			return nil, decisionErr(id, err)
		}
		return map[string]any{"issue": issueJSON(e, *r.Issue), "decision": decisionJSON(e, *r.Decision),
			"message": fmt.Sprintf("已開出 %s,%s 的來源 Issue 指向它", r.Issue.Key, r.Decision.ID)}, nil
	})
}

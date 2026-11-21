package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// 待決定 / 需要協助(說明見 cli.go 檔頭;規則與可見性在 core/service/decision.go)。
// 署名一律是登入的人(伺服器端依 token 決定),指令沒有指定身分的旗標。

// decisionValuedFlags 需要值的旗標(加進 cli.go 的 valued;kind、status 是 cli.go 本來就有的旗標,decisions 共用)
var decisionValuedFlags = []string{"body", "options", "issue", "note", "evidence", "blocks"}

type decisionFlags struct {
	// kind、status 共用 cli.go 的 --kind(decision / help)、--status(open / decided / withdrawn)
	kind, status                                 *string
	body, options, issue, note, evidence, blocks *string
	mine                                         *bool
}

func newDecisionFlags(fs *flag.FlagSet, kind, status *string) decisionFlags {
	return decisionFlags{
		kind:     kind,
		status:   status,
		body:     fs.String("body", "", "背景說明"),
		options:  fs.String("options", "", "選項,用 ; 分隔,前面加 * 是建議(例如 \"*用 A;用 B\")"),
		issue:    fs.String("issue", "", "來源 Issue(例如 ISS-37)"),
		note:     fs.String("note", "", "處理的說明"),
		evidence: fs.String("evidence", "", "help 完成的證據"),
		blocks:   fs.String("blocks", "", "被它擋住的 Issue,用逗號分隔"),
		mine:     fs.Bool("mine", false, "待我處理"),
	}
}

// parseOptionList "*用 A;用 B" → 選項(* 開頭是建議)
func parseOptionList(s string) []model.DecisionOption {
	out := []model.DecisionOption{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		o := model.DecisionOption{}
		if rest, ok := strings.CutPrefix(part, "*"); ok {
			o.Recommended, part = true, strings.TrimSpace(rest)
		}
		o.Label = part
		out = append(out, o)
	}
	return out
}

func printDecision(out io.Writer, d model.Decision) {
	state := d.Status
	if d.Status == model.DecisionDecided && d.Kind == model.DecisionKindHelp {
		state = "已處理"
	}
	fmt.Fprintf(out, "%s [%s/%s] %s", d.ID, d.Kind, state, d.Title)
	if d.SourceIssue != "" {
		fmt.Fprintf(out, "  ←%s", d.SourceIssue)
	}
	fmt.Fprintln(out)
}

// decisionValued 把待決定的旗標加進需要值的旗標表
func decisionValued(m map[string]bool) map[string]bool {
	for _, k := range decisionValuedFlags {
		m[k] = true
	}
	return m
}

// decisions omixor decisions [list|show|add|answer|withdraw|reopen|to-issue]
func decisions(cl *client.Client, out io.Writer, sub string, pos []string, org string, f decisionFlags) error {
	arg := func(n int) (string, error) {
		if len(pos) <= n || strings.TrimSpace(pos[n]) == "" {
			return "", errors.New("缺少參數(見 core/cli/cli.go 檔頭的 decisions 用法)")
		}
		return pos[n], nil
	}
	switch sub {
	case "", "list":
		l, err := cl.ListDecisions(org, service.DecisionListQuery{Status: *f.status, Kind: *f.kind, Issue: *f.issue, Mine: *f.mine})
		if err != nil {
			return err
		}
		for _, d := range l {
			printDecision(out, d)
		}
	case "show":
		id, err := arg(1)
		if err != nil {
			return err
		}
		d, err := cl.GetDecision(org, id)
		if err != nil {
			return err
		}
		printJSON(out, d)
	case "add":
		title, err := arg(1)
		if err != nil {
			return err
		}
		var blocks []string
		for _, b := range strings.Split(*f.blocks, ",") {
			if b = strings.TrimSpace(b); b != "" {
				blocks = append(blocks, b)
			}
		}
		d, err := cl.AddDecision(org, service.DecisionInput{Kind: *f.kind, Title: title, Body: *f.body, Options: parseOptionList(*f.options),
			SourceIssue: *f.issue, Blocks: blocks})
		if err != nil {
			return err
		}
		printDecision(out, *d)
	case "answer":
		id, err := arg(1)
		if err != nil {
			return err
		}
		ans, err := arg(2)
		if err != nil {
			return err
		}
		d, err := cl.AnswerDecision(org, id, service.DecisionAnswer{Answer: ans, Note: *f.note, Evidence: *f.evidence})
		if err != nil {
			return err
		}
		printDecision(out, *d)
	case "withdraw", "reopen":
		id, err := arg(1)
		if err != nil {
			return err
		}
		var d *model.Decision
		if sub == "withdraw" {
			d, err = cl.WithdrawDecision(org, id)
		} else {
			d, err = cl.ReopenDecision(org, id)
		}
		if err != nil {
			return err
		}
		printDecision(out, *d)
	case "to-issue":
		id, err := arg(1)
		if err != nil {
			return err
		}
		r, err := cl.DecisionToIssue(org, id, service.DecisionToIssueInput{})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已開出 %s(%s 的來源 Issue)\n", r.Issue.ID, r.Decision.ID)
	default:
		return errors.New("用法:omixor decisions [list|show <id>|add <標題>|answer <id> <答案>|withdraw <id>|reopen <id>|to-issue <id>](說明見 core/cli/cli.go)")
	}
	return nil
}

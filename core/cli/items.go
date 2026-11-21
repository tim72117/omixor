package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"omixor-core/client"
	"omixor-core/model"
	"omixor-core/service"
)

// Issue 子項(ISS-37;說明見 cli.go 檔頭;規則在 core/service/issue_items.go)

// itemValuedFlags 需要值的旗標(加進 cli.go 的 valued;--status、--note、--evidence 與 decisions 共用)
var itemValuedFlags = []string{"assignee", "depends", "files", "acceptance", "reason", "wait-kind", "wait-for", "priority", "labels"}

type itemFlags struct {
	assignee, depends, files, acceptance, reason, waitKind, waitFor, priority, labels *string
	includeDone                                                                       *bool
}

func newItemFlags(fs *flag.FlagSet) itemFlags {
	return itemFlags{
		assignee:    fs.String("assignee", "", "負責人(email、@handle、agent:<handle>;空字串清掉)"),
		depends:     fs.String("depends", "", "前置子項(同 Issue 的編號或序號;空字串清掉)"),
		files:       fs.String("files", "", "會動的檔案,用逗號分隔"),
		acceptance:  fs.String("acceptance", "", "驗收標準"),
		reason:      fs.String("reason", "", "卡住的原因"),
		waitKind:    fs.String("wait-kind", "", "等待種類:user_decision、item、external"),
		waitFor:     fs.String("wait-for", "", "等待對象"),
		priority:    fs.String("priority", "", "promote 新 Issue 的優先度"),
		labels:      fs.String("labels", "", "promote 新 Issue 的標籤,用逗號分隔"),
		includeDone: fs.Bool("include-done", false, "列表連完成與取消的一起"),
	}
}

// itemValued 把子項的旗標加進需要值的旗標表
func itemValued(m map[string]bool) map[string]bool {
	for _, k := range itemValuedFlags {
		m[k] = true
	}
	return m
}

func splitList(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func printItem(out io.Writer, it model.IssueItem) {
	who := ""
	if it.Assignee != "" {
		who += "  負責:" + it.Assignee
	}
	if it.ClaimedBy != "" {
		who += "  領取:" + it.ClaimedBy
	}
	fmt.Fprintf(out, "%s [%s] %s%s", it.Key, it.Status, it.Title, who)
	if it.DependsOn != "" {
		fmt.Fprintf(out, "  ←%s", it.DependsOn)
	}
	if it.Status == model.ItemBlocked {
		fmt.Fprintf(out, "  卡住:%s", it.BlockReason)
		if it.WaitForKind != "" {
			fmt.Fprintf(out, "(等待 %s:%s)", it.WaitForKind, it.WaitFor)
		}
	}
	fmt.Fprintln(out)
}

// evidenceFlag --evidence:JSON 物件原樣,其他當文字
func evidenceFlag(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "{") {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}

// items omixor items [list|add|show|claim|release|report|block|unblock|cancel|promote|update|delete]
func items(cl *client.Client, out io.Writer, sub string, pos []string, org string, f itemFlags, d decisionFlags) error {
	arg := func(n int) (string, error) {
		if len(pos) <= n || strings.TrimSpace(pos[n]) == "" {
			return "", errors.New("缺少參數(見 core/cli/cli.go 檔頭的 items 用法)")
		}
		return pos[n], nil
	}
	// itemRef 子項編號(ISS-37.2)→ (Issue, 子項)
	itemRef := func(n int) (issue, item string, err error) {
		ref, err := arg(n)
		if err != nil {
			return "", "", err
		}
		issue, item = service.SplitItemKey(ref)
		if issue == "" {
			return "", "", errors.New("子項編號要像 ISS-37.2(Issue key 加序號)")
		}
		return issue, item, nil
	}
	report := func(status string) service.ItemReport {
		return service.ItemReport{Status: status, Evidence: evidenceFlag(*d.evidence), Note: *d.note, BlockReason: *f.reason, WaitForKind: *f.waitKind, WaitFor: *f.waitFor}
	}
	switch sub {
	case "", "list":
		issue, err := arg(1)
		if err != nil {
			return err
		}
		l, err := cl.ListIssueItems(org, issue, service.ItemListQuery{IncludeDone: *f.includeDone, Status: *d.status})
		if err != nil {
			return err
		}
		for _, it := range l {
			printItem(out, it)
		}
	case "show":
		issue, item, err := itemRef(1)
		if err != nil {
			return err
		}
		it, err := cl.GetIssueItem(org, issue, item)
		if err != nil {
			return err
		}
		printJSON(out, it)
	case "add":
		issue, err := arg(1)
		if err != nil {
			return err
		}
		title, err := arg(2)
		if err != nil {
			return err
		}
		it, err := cl.AddIssueItem(org, issue, service.ItemInput{Title: title, Files: splitList(*f.files), Acceptance: *f.acceptance, Assignee: *f.assignee, DependsOn: *f.depends})
		if err != nil {
			return err
		}
		printItem(out, *it)
	case "update":
		issue, item, err := itemRef(1)
		if err != nil {
			return err
		}
		p := service.ItemPatch{}
		// 只改有給值的欄位(旗標空字串視為沒給,所以 CLI 不能清空欄位;要清空用 API 或 MCP)
		if *f.acceptance != "" {
			p.Acceptance = f.acceptance
		}
		if *f.assignee != "" {
			p.Assignee = f.assignee
		}
		if *f.depends != "" {
			p.DependsOn = f.depends
		}
		if *f.files != "" {
			fl := splitList(*f.files)
			p.Files = &fl
		}
		if len(pos) > 2 && strings.TrimSpace(pos[2]) != "" {
			t := pos[2]
			p.Title = &t
		}
		it, err := cl.UpdateIssueItem(org, issue, item, p)
		if err != nil {
			return err
		}
		printItem(out, *it)
	case "claim":
		issue, item, err := itemRef(1)
		if err != nil {
			return err
		}
		r, err := cl.ClaimIssueItem(org, issue, item, service.ItemClaimInput{Note: *d.note})
		if err != nil {
			return err
		}
		printItem(out, *r.Item)
		if r.Credential != nil { // 負責人是角色:憑證只在這次回傳
			fmt.Fprintf(out, "任務 %s;角色憑證(只這一次):%s:%s(到期 %s)\n", r.Task.ID, r.Credential.AgentID, r.Credential.Secret, r.Credential.ExpiresAt)
		}
	case "release", "unblock", "cancel", "delete":
		issue, item, err := itemRef(1)
		if err != nil {
			return err
		}
		var it *model.IssueItem
		switch sub {
		case "release":
			it, err = cl.ReleaseIssueItem(org, issue, item)
		case "unblock":
			it, err = cl.UnblockIssueItem(org, issue, item)
		case "cancel":
			it, err = cl.CancelIssueItem(org, issue, item)
		default:
			if err = cl.DeleteIssueItem(org, issue, item); err == nil {
				fmt.Fprintf(out, "已刪除 %s\n", pos[1])
			}
		}
		if err != nil {
			return err
		}
		if it != nil {
			printItem(out, *it)
		}
	case "report":
		issue, item, err := itemRef(1)
		if err != nil {
			return err
		}
		status, err := arg(2)
		if err != nil {
			return err
		}
		it, err := cl.ReportIssueItem(org, issue, item, report(status))
		if err != nil {
			return err
		}
		printItem(out, *it)
	case "block":
		issue, item, err := itemRef(1)
		if err != nil {
			return err
		}
		if len(pos) > 2 && *f.reason == "" {
			*f.reason = pos[2]
		}
		it, err := cl.BlockIssueItem(org, issue, item, report(model.ItemBlocked))
		if err != nil {
			return err
		}
		printItem(out, *it)
	case "promote":
		issue, item, err := itemRef(1)
		if err != nil {
			return err
		}
		r, err := cl.PromoteIssueItem(org, issue, item, service.ItemPromoteInput{Priority: *f.priority, Labels: splitList(*f.labels)})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已提升為 %s(%s 標為取消)\n", r.Issue.Key, r.Item.Key)
	default:
		return errors.New("用法:omixor items [list <Issue>|add <Issue> <標題>|show <子項>|claim <子項>|release|report <子項> <狀態>|block <子項> <原因>|unblock|cancel|promote|update|delete](說明見 core/cli/cli.go)")
	}
	return nil
}

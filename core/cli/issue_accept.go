package cli

import (
	"errors"
	"fmt"
	"io"

	"omixor-core/client"
)

// issueAccept omixor issues accept|release <Issue>:接受(你成為負責人、Issue 進行中)或放回(取消負責人、退回待處理)。
// 規則在 core/service/issue_accept.go
func issueAccept(cl *client.Client, out io.Writer, sub string, pos []string, org string) error {
	if len(pos) < 2 || pos[1] == "" {
		return errors.New("用法:omixor issues accept|release <Issue 編號>")
	}
	verb, call := "已接受", cl.AcceptIssue
	if sub == "release" {
		verb, call = "已放回", cl.ReleaseIssue
	}
	i, err := call(org, pos[1])
	if err != nil {
		return err
	}
	who := ""
	if i.AssigneeActor != nil {
		who = "  負責:" + i.AssigneeActor.Name
	}
	fmt.Fprintf(out, "%s %s「%s」%s\n", verb, i.Display(), i.Title, who)
	return nil
}

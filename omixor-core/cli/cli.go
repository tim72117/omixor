// Package cli 是 omixor 管理 CLI(omixor-core/cmd/omixor;ops-web 的 cmd/omixor 也呼叫它)。資料維護一律走 omixor server 的 API,不直連資料庫。
//
//	omixor login [--server 網址] [--device 名稱]   用 evoleap 登入證明身分,換 omixor 自己的 token
//	omixor logout | whoami
//	omixor orgs                                    我能用的組織
//	omixor orgs create <id> [--name 名稱]          建立組織(伺服器管理員)
//	omixor members [--org default]                 組織成員
//	omixor members add <email> [--role admin|member] [--org default]
//	omixor members remove <email> [--org default]
//	omixor users | users add <email> [--role admin|member] | users role <email> <role> | users disable|enable <email>
//	omixor tokens | tokens revoke <id>
//	omixor audit [--limit 100]
//	omixor issues list [--org default]
//	omixor issues export [--org default] [-o 檔案]
//	omixor issues import [--org default] <檔案>     第一次搬資料用;伺服器上已經有 Issue 就會拒絕
//	omixor issues restore <id>… --from <檔案> [--org default]   資料被改壞時改回檔案裡的內容(不是同步)
//	omixor issues delete <id>… | --all [--org default] [--yes]   沒加 --yes 只列出
//
// 登入資料存在 ~/.config/omixor/credentials.json(權限 600;OMIXOR_CREDENTIALS 可改)。
// 登入時的伺服器:--server > OMIXOR_SERVER > 上次登入的 > 正式站;之後的指令只送回發 token 的那台。
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/client"
	"omixor-core/evoleap"
	"omixor-core/model"
	"omixor-core/service"
)

const DefaultServer = "https://omixor-issues-340121279179.asia-east1.run.app"

type credentials struct {
	Server    string      `json:"server"`
	Token     string      `json:"token"`
	TokenID   string      `json:"tokenId"`
	Email     string      `json:"email"`
	ExpiresAt *model.Time `json:"expiresAt,omitempty"`
}

func credPath() string {
	if p := os.Getenv("OMIXOR_CREDENTIALS"); p != "" {
		return p
	}
	dir, _ := os.UserConfigDir()
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "omixor", "credentials.json")
}

func loadCred() credentials {
	var c credentials
	if b, err := os.ReadFile(credPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func saveCred(c credentials) error {
	p := credPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

func trim(s string) string { return strings.TrimRight(s, "/") }

// authed 用存著的 token 連回發它的伺服器;OMIXOR_SERVER 指到別台時拒絕,避免把 token 送給別人
func authed() (*client.Client, error) {
	c := loadCred()
	if c.Token == "" {
		return nil, errors.New("還沒登入:先執行 omixor login")
	}
	srv := trim(c.Server)
	if srv == "" {
		srv = DefaultServer
	}
	if env := trim(os.Getenv("OMIXOR_SERVER")); env != "" && env != srv {
		return nil, fmt.Errorf("登入資料是 %s 發的,但 OMIXOR_SERVER 指到 %s;請先 omixor login --server %s", srv, env, env)
	}
	cl := client.New(srv)
	tok := c.Token
	cl.Token = func() string { return tok }
	return cl, nil
}

// reorder 讓旗標可以放在位置參數後面
func reorder(args []string, valued map[string]bool) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && valued[strings.TrimLeft(a, "-")] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

var valued = map[string]bool{"role": true, "org": true, "o": true, "limit": true, "server": true, "device": true, "from": true, "name": true, "evoleap": true, "evoleap-console": true}

func printJSON(w io.Writer, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(w, string(b))
}

func readIssues(file string) ([]model.Issue, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("讀不到 %s:%v", file, err)
	}
	var list []model.Issue
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("%s 不是 issues.json 格式(要是 Issue 陣列):%v", file, err)
	}
	return list, nil
}

const usage = `用法:omixor login|logout|whoami|orgs|members|users|tokens|audit|issues …(說明見 omixor-core/cli/cli.go)`

// Main 給 cmd 用:執行並在出錯時結束程式
func Main(args []string) {
	if err := Run(args, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Run 執行一個指令(args 不含程式名稱)
func Run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	org := fs.String("org", "default", "組織 id")
	role := fs.String("role", "", "admin 或 member")
	name := fs.String("name", "", "名稱")
	outFile := fs.String("o", "", "輸出檔案")
	from := fs.String("from", "", "還原用的 issues.json")
	yes := fs.Bool("yes", false, "真的刪除")
	all := fs.Bool("all", false, "全部")
	limit := fs.Int("limit", 100, "筆數")
	srvFlag := fs.String("server", "", "omixor server 網址")
	device := fs.String("device", "", "這台的名稱")
	evo := fs.String("evoleap", "https://evoleap.io", "evoleap 網址")
	console := fs.String("evoleap-console", "", "evoleap 核准頁網址")
	if err := fs.Parse(reorder(rest, valued)); err != nil {
		return err
	}
	pos := fs.Args()
	sub := ""
	if len(pos) > 0 {
		sub = pos[0]
	}

	switch cmd {
	case "login":
		srv := DefaultServer
		for _, s := range []string{*srvFlag, os.Getenv("OMIXOR_SERVER"), loadCred().Server} {
			if s != "" {
				srv = trim(s)
				break
			}
		}
		dev := *device
		if dev == "" {
			h, _ := os.Hostname()
			dev = "omixor CLI(" + strings.TrimSuffix(h, ".local") + ")"
		}
		fmt.Fprintln(out, "瀏覽器會打開 evoleap 的核准頁,核准後回到這裡…")
		evoTok, err := evoleap.Login(*evo, *console, 10*time.Minute)
		if err != nil {
			return fmt.Errorf("evoleap 登入失敗:%v", err)
		}
		res, err := client.New(srv).LoginWithEvoleap(evoTok, dev)
		// evoleap 的 token 只用來證明一次身分:成功或失敗都立刻撤銷
		evoleap.Logout(*evo, evoTok)
		if err != nil {
			return fmt.Errorf("omixor 拒絕登入:%v", err)
		}
		if err := saveCred(credentials{Server: srv, Token: res.Token, TokenID: res.TokenID, Email: res.User.Email, ExpiresAt: res.ExpiresAt}); err != nil {
			return err
		}
		exp := "不到期"
		if res.ExpiresAt != nil {
			exp = res.ExpiresAt.Local().Format("2006-01-02") + " 到期"
		}
		fmt.Fprintf(out, "已登入 %s(%s,%s;%s)\n", srv, res.User.Email, res.User.Role, exp)
		return nil
	case "logout":
		c := loadCred()
		if c.Token == "" {
			fmt.Fprintln(out, "本來就沒有登入")
			return nil
		}
		if cl, err := authed(); err == nil {
			if err := cl.Logout(); err != nil {
				fmt.Fprintln(out, "伺服器上撤銷失敗(本機的登入照樣刪掉):", err)
			}
		}
		c.Token, c.TokenID, c.ExpiresAt = "", "", nil
		if err := saveCred(c); err != nil {
			return err
		}
		fmt.Fprintln(out, "已登出")
		return nil
	}

	cl, err := authed()
	if err != nil {
		return err
	}
	switch cmd {
	case "whoami":
		me, err := cl.Me()
		if err != nil {
			return err
		}
		printJSON(out, me)
	case "orgs":
		if sub == "create" {
			if len(pos) < 2 {
				return errors.New("用法:omixor orgs create <id> [--name 名稱]")
			}
			o, err := cl.CreateOrg(pos[1], *name)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "已建立組織 %s(%s)\n", o.ID, o.Name)
			return nil
		}
		l, err := cl.ListOrgs()
		if err != nil {
			return err
		}
		for _, o := range l {
			fmt.Fprintf(out, "%-20s %-6s %s\n", o.ID, o.MyRole, o.Name)
		}
	case "members":
		switch sub {
		case "add":
			if len(pos) < 2 {
				return errors.New("用法:omixor members add <email> [--role admin|member] [--org …]")
			}
			m, err := cl.SaveMember(*org, pos[1], *role)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s 是 %s 的 %s\n", m.Email, *org, m.Role)
		case "remove":
			if len(pos) < 2 {
				return errors.New("用法:omixor members remove <email> [--org …]")
			}
			if err := cl.RemoveMember(*org, pos[1]); err != nil {
				return err
			}
			fmt.Fprintf(out, "已把 %s 移出 %s\n", pos[1], *org)
		default:
			l, err := cl.ListMembers(*org)
			if err != nil {
				return err
			}
			for _, m := range l {
				fmt.Fprintf(out, "%-32s %-6s %s\n", m.Email, m.Role, m.Name)
			}
		}
	case "users":
		if sub == "" || sub == "list" {
			l, err := cl.ListUsers()
			if err != nil {
				return err
			}
			printJSON(out, l)
			return nil
		}
		if len(pos) < 2 {
			return errors.New("要給 email")
		}
		var r *string
		var d *bool
		t, f := true, false
		switch sub {
		case "add":
			if *role != "" {
				r = role
			}
		case "role":
			if len(pos) < 3 {
				return errors.New("用法:omixor users role <email> <admin|member>")
			}
			r = &pos[2]
		case "disable":
			d = &t
		case "enable":
			d = &f
		default:
			return errors.New(usage)
		}
		u, err := cl.SaveUser(pos[1], r, d)
		if err != nil {
			return err
		}
		printJSON(out, u)
	case "tokens":
		if sub == "revoke" && len(pos) >= 2 {
			if err := cl.RevokeToken(pos[1]); err != nil {
				return err
			}
			fmt.Fprintln(out, "已撤銷", pos[1])
			return nil
		}
		l, err := cl.ListTokens()
		if err != nil {
			return err
		}
		printJSON(out, l)
	case "audit":
		l, err := cl.ListAudit(*limit)
		if err != nil {
			return err
		}
		printJSON(out, l)
	case "issues":
		return issues(cl, out, sub, pos, *org, *outFile, *from, *yes, *all)
	default:
		return errors.New(usage)
	}
	return nil
}

func issues(cl *client.Client, out io.Writer, sub string, pos []string, org, outFile, from string, yes, all bool) error {
	ids := []string{}
	if len(pos) > 1 {
		ids = pos[1:]
	}
	switch sub {
	case "list":
		l, err := cl.ListIssues(org)
		if err != nil {
			return err
		}
		for _, i := range l {
			fmt.Fprintf(out, "%-8s %-6s %s\n", i.ID, i.Status, i.Title)
		}
	case "export":
		l, err := cl.ExportIssues(org)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(l, "", "  ")
		if outFile == "" {
			fmt.Fprintln(out, string(b))
			return nil
		}
		if err := os.WriteFile(outFile, b, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(out, "已匯出 %s 的 Issue 到 %s\n", org, outFile)
	case "import":
		if len(ids) == 0 {
			return errors.New("要給 issues.json")
		}
		list, err := readIssues(ids[0])
		if err != nil {
			return err
		}
		r, err := cl.ImportIssues(org, list)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已匯入 %s:%d 筆\n", org, r.Inserted)
	case "restore":
		if from == "" || len(ids) == 0 {
			return errors.New("用法:omixor issues restore <id>… --from <issues.json>")
		}
		list, err := readIssues(from)
		if err != nil {
			return err
		}
		file := map[string]model.Issue{}
		for _, i := range list {
			file[strings.ToUpper(i.ID)] = i
		}
		for _, id := range ids {
			src, ok := file[strings.ToUpper(id)]
			if !ok {
				return fmt.Errorf("%s 裡沒有 %s", from, id)
			}
			labels, comments := src.Labels, src.Comments
			got, err := cl.UpdateIssue(org, src.ID, service.IssuePatch{Title: &src.Title, Body: &src.Body, Status: &src.Status,
				Priority: &src.Priority, Labels: &labels, Source: &src.Source, Link: &src.Link, Comments: &comments, Author: src.CreatedBy})
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "已還原 %s「%s」(%s、%s、%d 則留言)\n", got.ID, got.Title, got.Status, got.Priority, len(got.Comments))
		}
	case "delete":
		if len(ids) == 0 && !all {
			return errors.New("用法:omixor issues delete <id>… | --all [--yes]")
		}
		cur, err := cl.ListIssues(org)
		if err != nil {
			return err
		}
		have := map[string]model.Issue{}
		for _, i := range cur {
			have[strings.ToUpper(i.ID)] = i
			if all {
				ids = append(ids, i.ID)
			}
		}
		if len(ids) == 0 {
			fmt.Fprintln(out, "伺服器上沒有 Issue")
			return nil
		}
		for _, id := range ids {
			i, ok := have[strings.ToUpper(id)]
			if !ok {
				return fmt.Errorf("伺服器上沒有 %s(什麼都還沒刪)", id)
			}
			if !yes {
				fmt.Fprintf(out, "會刪除 %s「%s」\n", i.ID, i.Title)
			}
		}
		if !yes {
			fmt.Fprintf(out, "共 %d 筆。確定要刪除請加 --yes\n", len(ids))
			return nil
		}
		for _, id := range ids {
			i := have[strings.ToUpper(id)]
			if err := cl.DeleteIssue(org, i.ID); err != nil {
				return err
			}
			fmt.Fprintf(out, "已刪除 %s「%s」\n", i.ID, i.Title)
		}
	default:
		return errors.New(usage)
	}
	return nil
}

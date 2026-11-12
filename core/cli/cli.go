// Package cli 是 omixor 管理 CLI(core/cmd/omixor;omixor-web 的 cmd/omixor 也呼叫它)。資料維護一律走 omixor server 的 API,不直連資料庫。
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
//	omixor agents import [--org default] <agents.json>   第一次搬資料用:角色與任務(格式同 app 的 agents.json);伺服器上已經有角色或任務就會拒絕
//
// 登入資料依伺服器分檔:~/.config/omixor/credentials/<伺服器>.json(權限 600;<伺服器> 是網址的 host(:port)),
// 可以同時登入好幾台(例如正式站與本機測試)。OMIXOR_CREDENTIALS 指定單一檔案時只用那個檔(舊的用法)。
// 舊版的 ~/.config/omixor/credentials.json(只記一台)照樣讀得到:用到那台時複製成新的檔案(舊的不刪)。
// 每個指令連哪一台:--server > OMIXOR_SERVER > 上次登入的(~/.config/omixor/current-server)> 舊版登入資料的 > 正式站;
// token 只送回發它的那台。
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/client"
	"omixor-core/evoleap"
	"omixor-core/model"
	"omixor-core/service"
)

const DefaultServer = "https://omixor-340121279179.asia-east1.run.app"

type credentials struct {
	Server    string      `json:"server"`
	Token     string      `json:"token"`
	TokenID   string      `json:"tokenId"`
	Email     string      `json:"email"`
	ExpiresAt *model.Time `json:"expiresAt,omitempty"`
}

// configDir 設定資料夾(OMIXOR_CONFIG_DIR 可改,測試用)
func configDir() string {
	if d := os.Getenv("OMIXOR_CONFIG_DIR"); d != "" {
		return d
	}
	dir, _ := os.UserConfigDir()
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "omixor")
}

func legacyCredPath() string { return filepath.Join(configDir(), "credentials.json") }
func currentSrvPath() string { return filepath.Join(configDir(), "current-server") }

// serverKey 登入資料的檔名:網址的 host(:port),只留英數字與 . _ -
func serverKey(srv string) string {
	k := srv
	if u, err := url.Parse(srv); err == nil && u.Host != "" {
		k = u.Host
	}
	var b strings.Builder
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}

// credPath 這台伺服器的登入資料
func credPath(srv string) string {
	if p := os.Getenv("OMIXOR_CREDENTIALS"); p != "" {
		return p
	}
	return filepath.Join(configDir(), "credentials", serverKey(trim(srv))+".json")
}

func readCred(p string) credentials {
	var c credentials
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// loadCred 這台伺服器的登入資料;新的檔案沒有時,舊版 credentials.json 是這台的就沿用(複製成新的檔案,舊的不刪)
func loadCred(srv string) credentials {
	srv = trim(srv)
	if c := readCred(credPath(srv)); c.Token != "" && (c.Server == "" || trim(c.Server) == srv) {
		return c
	}
	if os.Getenv("OMIXOR_CREDENTIALS") != "" {
		return credentials{}
	}
	if c := readCred(legacyCredPath()); c.Token != "" && trim(c.Server) == srv {
		_ = saveCred(c)
		return c
	}
	return credentials{}
}

func saveCred(c credentials) error {
	p := credPath(c.Server)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

// pickServer 這個指令連哪一台:--server > OMIXOR_SERVER > 上次登入的 > 舊版登入資料的 > 正式站
func pickServer(flagSrv string) string {
	for _, s := range []string{flagSrv, os.Getenv("OMIXOR_SERVER")} {
		if s = trim(s); s != "" {
			return s
		}
	}
	if os.Getenv("OMIXOR_CREDENTIALS") == "" {
		if b, err := os.ReadFile(currentSrvPath()); err == nil {
			if s := trim(string(b)); s != "" {
				return s
			}
		}
	}
	p := legacyCredPath()
	if e := os.Getenv("OMIXOR_CREDENTIALS"); e != "" {
		p = e
	}
	if c := readCred(p); c.Server != "" {
		return trim(c.Server)
	}
	return DefaultServer
}

func setCurrentServer(srv string) error {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(currentSrvPath(), []byte(trim(srv)+"\n"), 0o600)
}

func trim(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

// authed 用這台伺服器存著的 token 連它(token 只送回發它的那台)
func authed(srv string) (*client.Client, error) {
	srv = trim(srv)
	c := loadCred(srv)
	if c.Token == "" {
		return nil, fmt.Errorf("還沒登入 %s:先執行 omixor login --server %s", srv, srv)
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

const usage = `用法:omixor login|logout|whoami|orgs|members|users|tokens|audit|issues|agents …(說明見 core/cli/cli.go)`

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
	srvSel := pickServer(*srvFlag)
	sub := ""
	if len(pos) > 0 {
		sub = pos[0]
	}

	switch cmd {
	case "login":
		srv := srvSel
		dev := *device
		if dev == "" {
			h, _ := os.Hostname()
			dev = "omixor CLI(" + strings.TrimSuffix(h, ".local") + ")"
		}
		fmt.Fprintln(out, "瀏覽器會打開 evoleap 的核准頁,核准後回到這裡…")
		// 也把網址印出來:不在這台電腦旁時,可以用別的裝置打開核准
		openBrowser := evoleap.OpenBrowser
		evoleap.OpenBrowser = func(u string) {
			fmt.Fprintln(out, "核准頁:"+u)
			if f, ok := out.(interface{ Sync() error }); ok {
				_ = f.Sync()
			}
			openBrowser(u)
		}
		defer func() { evoleap.OpenBrowser = openBrowser }()
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
		if os.Getenv("OMIXOR_CREDENTIALS") == "" {
			_ = setCurrentServer(srv) // 之後的指令沒指定 --server 就連這台
		}
		exp := "不到期"
		if res.ExpiresAt != nil {
			exp = res.ExpiresAt.Local().Format("2006-01-02") + " 到期"
		}
		fmt.Fprintf(out, "已登入 %s(%s,%s;%s)\n", srv, res.User.Email, res.User.Role, exp)
		return nil
	case "logout":
		c := loadCred(srvSel)
		if c.Token == "" {
			fmt.Fprintf(out, "本來就沒有登入 %s\n", srvSel)
			return nil
		}
		if cl, err := authed(srvSel); err == nil {
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

	cl, err := authed(srvSel)
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
	case "agents":
		if sub != "import" || len(pos) < 2 {
			return errors.New("用法:omixor agents import <agents.json> --org <組織>(第一次搬資料;檔案格式同 app 的 agents.json:{agents, tasks})")
		}
		b, err := os.ReadFile(pos[1])
		if err != nil {
			return err
		}
		var in service.AgentImport
		if err := json.Unmarshal(b, &in); err != nil {
			return fmt.Errorf("%s 讀不出來:%w", pos[1], err)
		}
		r, err := cl.ImportAgents(*org, in)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已匯入 %s:角色 %d 個、任務 %d 筆\n", r.Org, r.Agents, r.Tasks)
		if len(r.Requeue) > 0 {
			fmt.Fprintf(out, "原本進行中、改成待領取:%s\n", strings.Join(r.Requeue, "、"))
		}
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

// Package cli 是 omixor 管理 CLI(core/cmd/omixor;omixor-web 的 cmd/omixor 也呼叫它)。資料維護一律走 omixor server 的 API,不直連資料庫。
//
//	omixor login [--server 網址] [--device 名稱]   用 evoleap 登入證明身分,換 omixor 自己的 token
//	omixor logout | whoami
//	omixor orgs                                    我能用的組織
//	omixor orgs create <id> [--name 名稱]          建立組織(伺服器管理員)
//	omixor members [--org default]                 組織成員
//	omixor members add <email> [--role admin|member] [--org default]
//	omixor members remove <email> [--org default]
//	omixor actors [--org default] [--kind user|agent|system] [--q 前綴] [--status active|disabled|suspended|retired|all]   組織看得到的使用者、角色、系統 actor(id、種類、handle、名稱、狀態)
//	omixor users | users add <email> [--role admin|member] | users role <email> <role> | users disable|enable <email>
//	omixor tokens | tokens revoke <id>
//	omixor audit [--limit 100]
//	omixor projects [list] [--org default]         專案(id、代號、名稱、連結的 repo)
//	omixor projects link <專案 id> <repo> [--org default]     連結專案與 repo(組織管理員;只用於預設值與篩選,不是權限來源)
//	omixor projects unlink <專案 id> <repo> [--org default]   取消連結
//	omixor projects restrict|unrestrict <專案 id>             受限專案(只有專案成員與組織管理員看得到;組織管理員)
//	omixor projects members <專案 id>                         專案成員
//	omixor projects member-add <專案 id> <email|agent:<角色 id>> [--role admin|member]   加入成員或改角色(組織管理員或專案管理員)
//	omixor projects member-remove <專案 id> <成員>            移除成員
//	omixor issues list [--org default]
//	omixor issues accept|release <Issue>             接受(你成為負責人、Issue 進行中)或放回(取消負責人、退回待處理;接受者本人或組織管理員)
//	omixor issues export [--org default] [-o 檔案]
//	omixor issues import [--org default] <檔案>     第一次搬資料用;伺服器上已經有 Issue 就會拒絕
//	omixor issues restore <id>… --from <檔案> [--org default]   資料被改壞時改回檔案裡的內容(不是同步)
//	omixor issues delete <id>… | --all [--org default] [--yes]   沒加 --yes 只列出
//	omixor decisions [list] [--kind decision|help] [--status open|decided|withdrawn] [--issue ISS-n] [--mine] [--org default]   待決定 / 需要協助
//	omixor decisions show <DEC-n> | add <標題> [--kind help] [--body 背景] [--options "*A;B"] [--issue ISS-n] [--blocks ISS-1,ISS-2]
//	omixor decisions answer <DEC-n> <選項 id|other|done|declined> [--note 說明] [--evidence 證據] | withdraw <DEC-n> | reopen <DEC-n> | to-issue <DEC-n>
//	omixor items list <Issue> [--include-done] [--status todo|doing|blocked|done|canceled]   Issue 子項(待辦清單;編號 ISS-37.2)
//	omixor items add <Issue> <標題> [--files a,b] [--acceptance 驗收] [--assignee 人或 agent:角色] [--depends 序號]
//	omixor items show|claim|release|unblock|cancel|delete <子項>   claim 負責人是角色時會印出一次性憑證
//	omixor items report <子項> todo|doing|blocked|done [--evidence 證據] [--note 備註] [--reason 原因] [--wait-kind user_decision|item|external --wait-for 對象]
//	omixor items block <子項> <原因> | promote <子項> [--priority high] [--labels a,b] | update <子項> [新標題] [--acceptance …]
//	omixor channel [--org default] [--agent 角色] [--interval 30s] [--auto]   任務通知頻道(stdio MCP,給 Claude Code 的 channels;見 core/mcp/channel.go)
//	omixor agents import [--org default] <agents.json>   第一次搬資料用:角色與任務(格式同 app 的 agents.json);伺服器上已經有角色或任務就會拒絕
//
// 登入資料依伺服器分檔:~/.config/omixor/credentials/<伺服器>.json(權限 600;<伺服器> 是網址的 host(:port)),
// 可以同時登入好幾台(例如正式站與本機測試)。OMIXOR_CREDENTIALS 指定單一檔案時只用那個檔(舊的用法)。
// 舊版的 ~/.config/omixor/credentials.json(只記一台)照樣讀得到:用到那台時複製成新的檔案(舊的不刪)。
// 每個指令連哪一台:--server > OMIXOR_SERVER > 上次登入的(~/.config/omixor/current-server)> 舊版登入資料的 > 正式站;
// token 只送回發它的那台。
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omixor-core/backend"
	"omixor-core/client"
	"omixor-core/config"
	"omixor-core/evoleap"
	"omixor-core/mcp"
	"omixor-core/model"
	"omixor-core/service"
)

const DefaultServer = "https://omixor.io"

type credentials struct {
	Server    string      `json:"server"`
	Token     string      `json:"token"`
	TokenID   string      `json:"tokenId"`
	Email     string      `json:"email"`
	ExpiresAt *model.Time `json:"expiresAt,omitempty"`
}

// configDir 設定資料夾(OMIXOR_CONFIG_DIR 可改,測試用)
func configDir() string {
	if d := config.Current().Str(config.ConfigDir); d != "" {
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
	if p := config.Current().Str(config.Credentials); p != "" {
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
	if config.Current().Str(config.Credentials) != "" {
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
	for _, s := range []string{flagSrv, config.Current().Str(config.ServerURL)} {
		if s = trim(s); s != "" {
			return s
		}
	}
	if config.Current().Str(config.Credentials) == "" {
		if b, err := os.ReadFile(currentSrvPath()); err == nil {
			if s := trim(string(b)); s != "" {
				return s
			}
		}
	}
	p := legacyCredPath()
	if e := config.Current().Str(config.Credentials); e != "" {
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

var valued = itemValued(decisionValued(map[string]bool{"role": true, "org": true, "o": true, "limit": true, "server": true, "agent": true, "interval": true, "device": true, "from": true, "name": true, "kind": true, "q": true, "status": true, "evoleap": true, "evoleap-console": true}))

// actorID 輸出用:actor 物件的 id;沒有(查不到)是空字串
func actorID(a *model.ActorView) string {
	if a == nil {
		return ""
	}
	return a.ID
}

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

const usage = `用法:omixor login|logout|whoami|orgs|members|actors|users|tokens|audit|projects|issues|decisions|items|agents|channel …(說明見 core/cli/cli.go)`

// Main 給 cmd 用:執行並在出錯時結束程式
func Main(args []string) {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Println(usage)
		fmt.Println()
		fmt.Print(config.Usage(config.CLI))
		return
	}
	// 設定(環境變數)啟動時集中讀取、驗證一次;有問題一次列完
	config.MustLoad(config.CLI)
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
	kindFlag := fs.String("kind", "", "actor 種類:user、agent、system")
	qFlag := fs.String("q", "", "actor 的 handle 或名稱前綴")
	statusFlag := fs.String("status", "", "actor 狀態:active、disabled、suspended、retired、all")
	outFile := fs.String("o", "", "輸出檔案")
	from := fs.String("from", "", "還原用的 issues.json")
	yes := fs.Bool("yes", false, "真的刪除")
	all := fs.Bool("all", false, "全部")
	limit := fs.Int("limit", 100, "筆數")
	srvFlag := fs.String("server", "", "omixor server 網址")
	device := fs.String("device", "", "這台的名稱")
	evo := fs.String("evoleap", "https://evoleap.io", "evoleap 網址")
	console := fs.String("evoleap-console", "", "evoleap 核准頁網址")
	agentFlag := fs.String("agent", "", "角色 id")
	interval := fs.Duration("interval", 30*time.Second, "查詢間隔")
	auto := fs.Bool("auto", false, "收到任務就領取處理")
	df := newDecisionFlags(fs, kindFlag, statusFlag)
	itf := newItemFlags(fs)
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
		if config.Current().Str(config.Credentials) == "" {
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
				fmt.Fprintf(out, "%-32s %-6s %-20s %s\n", m.Email, m.Role, m.Name, actorID(m.ActorRef))
			}
		}
	case "actors":
		l, err := cl.ListActors(*org, *kindFlag, *qFlag, *statusFlag)
		if err != nil {
			return err
		}
		for _, a := range l {
			fmt.Fprintf(out, "%-44s %-6s %-28s %-9s %s\n", a.ID, a.Kind, a.Handle, a.Status, a.Name)
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
	case "projects":
		return projects(cl, out, sub, pos, *org, *role)
	case "issues":
		return issues(cl, out, sub, pos, *org, *outFile, *from, *yes, *all)
	case "decisions":
		return decisions(cl, out, sub, pos, *org, df)
	case "items":
		return items(cl, out, sub, pos, *org, itf, df)
	case "channel":
		// stdout 是 MCP 協定,訊息與錯誤只寫 stderr
		lg := log.New(os.Stderr, "", log.LstdFlags)
		return mcp.RunChannel(context.Background(), os.Stdin, out, mcp.ChannelConfig{
			Tasks: backend.NewRemote(cl), Org: *org, Server: trim(srvSel), AgentID: *agentFlag, Interval: *interval, Auto: *auto, Logf: lg.Printf,
		})
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

func projects(cl *client.Client, out io.Writer, sub string, pos []string, org, role string) error {
	switch sub {
	case "", "list":
		l, err := cl.ListProjects(org)
		if err != nil {
			return err
		}
		for _, x := range l {
			key := x.Key
			if key == "" {
				key = "-"
			}
			lock := ""
			if x.Restricted {
				lock = " [受限]"
			}
			fmt.Fprintf(out, "%-8s %-10s %-20s %s%s\n", x.ID, key, x.Name, strings.Join(x.Repos, ", "), lock)
		}
	case "restrict", "unrestrict": // 受限旗標(組織管理員)
		if len(pos) < 2 {
			return fmt.Errorf("用法:omixor projects %s <專案 id> [--org 組織]", sub)
		}
		v := sub == "restrict"
		x, err := cl.UpdateProject(org, pos[1], service.ProjectPatch{Restricted: &v})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s 受限:%v\n", x.ID, x.Restricted)
	case "members": // 專案成員名單
		if len(pos) < 2 {
			return errors.New("用法:omixor projects members <專案 id> [--org 組織]")
		}
		l, err := cl.ListProjectMembers(org, pos[1])
		if err != nil {
			return err
		}
		for _, m := range l {
			fmt.Fprintf(out, "%-30s %s\n", m.Node, m.Role)
		}
	case "member-add", "member-remove": // 加入、改角色、移除成員(組織管理員或專案管理員)
		if len(pos) < 3 {
			return fmt.Errorf("用法:omixor projects %s <專案 id> <成員:email 或 agent:<角色 id>> [--role admin|member] [--org 組織]", sub)
		}
		if sub == "member-remove" {
			if err := cl.RemoveProjectMember(org, pos[1], pos[2]); err != nil {
				return err
			}
			fmt.Fprintf(out, "已把 %s 從 %s 移除\n", pos[2], pos[1])
			return nil
		}
		m, err := cl.SetProjectMember(org, pos[1], service.ProjectMemberInput{Actor: pos[2], Role: role})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s 在 %s 是 %s\n", m.Node, pos[1], m.Role)
	case "link", "unlink":
		if len(pos) < 3 {
			return fmt.Errorf("用法:omixor projects %s <專案 id> <repo> [--org 組織]", sub)
		}
		var x *model.Project
		var err error
		if sub == "link" {
			x, err = cl.LinkProjectRepo(org, pos[1], pos[2])
		} else {
			x, err = cl.UnlinkProjectRepo(org, pos[1], pos[2])
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s 連結的 repo:%s\n", x.ID, strings.Join(x.Repos, ", "))
	default:
		return errors.New("用法:omixor projects [list] | link <專案 id> <repo> | unlink <專案 id> <repo> | restrict|unrestrict <專案 id> | members <專案 id> | member-add|member-remove <專案 id> <成員>")
	}
	return nil
}

func issues(cl *client.Client, out io.Writer, sub string, pos []string, org, outFile, from string, yes, all bool) error {
	ids := []string{}
	if len(pos) > 1 {
		ids = pos[1:]
	}
	switch sub {
	case "accept", "release":
		return issueAccept(cl, out, sub, pos, org)
	case "list":
		l, err := cl.ListIssues(org)
		if err != nil {
			return err
		}
		for _, i := range l {
			project := i.Project
			if project == "" {
				project = "-"
			}
			fmt.Fprintf(out, "%-10s %-8s %-6s %s\n", i.Display(), project, i.Status, i.Title)
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
			if i.Key != "" {
				file[strings.ToUpper(i.Key)] = i
			}
		}
		for _, id := range ids {
			src, ok := file[strings.ToUpper(id)]
			if !ok {
				return fmt.Errorf("%s 裡沒有 %s", from, id)
			}
			labels, comments := src.Labels, src.Comments
			got, err := cl.UpdateIssue(org, src.ID, service.IssuePatch{Title: &src.Title, Body: &src.Body, Status: &src.Status,
				Priority: &src.Priority, Labels: &labels, Source: &src.Source, Link: &src.Link, Repo: &src.Repo, Comments: &comments, Restore: true})
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "已還原 %s「%s」(%s、%s、%d 則留言)\n", got.Display(), got.Title, got.Status, got.Priority, len(got.Comments))
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
			if i.Key != "" {
				have[strings.ToUpper(i.Key)] = i
			}
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
				fmt.Fprintf(out, "會刪除 %s「%s」\n", i.Display(), i.Title)
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
			fmt.Fprintf(out, "已刪除 %s「%s」(移到垃圾桶)\n", i.Display(), i.Title)
		}
	default:
		return errors.New(usage)
	}
	return nil
}

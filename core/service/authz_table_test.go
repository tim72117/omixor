package service_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"

	"omixor-core/model"
	"omixor-core/service"
)

// 守門測試(ISS-33 第 1 階段):操作表(service/authz_table.go)與現況一致。
// (a) service 帶 *model.Principal 的公開方法都在表裡;(b) MCP 工具標記與 model/tools.go 一致;表本身的基本一致性。
// (c) 「未授權被拒」見 authz_denied_test.go。

// servicePrincipalMethods 用 go/ast 掃 core/service(排除測試)所有帶 *model.Principal 參數的公開方法
func servicePrincipalMethods(t *testing.T) []string {
	t.Helper()
	fs := token.NewFileSet()
	pkgs, err := parser.ParseDir(fs, ".", func(i os.FileInfo) bool { return !strings.HasSuffix(i.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv == nil || !fd.Name.IsExported() {
					continue
				}
				for _, pa := range fd.Type.Params.List {
					st, ok := pa.Type.(*ast.StarExpr)
					if !ok {
						continue
					}
					if se, ok := st.X.(*ast.SelectorExpr); ok && se.Sel.Name == "Principal" {
						seen[fd.Name.Name] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// (a) 每個帶 Principal 的公開方法都要在表裡,或在 AuthzExempt 寫明原因;反過來表裡的方法都要真的存在
func TestAuthzTableCoversEveryServiceMethod(t *testing.T) {
	methods := servicePrincipalMethods(t)
	if len(methods) < 90 {
		t.Fatalf("掃到的方法太少(%d),掃描可能壞了", len(methods))
	}
	actual := map[string]bool{}
	for _, m := range methods {
		actual[m] = true
	}
	inTable := map[string]string{}
	for _, o := range service.AuthzOps {
		for _, m := range o.Methods {
			if prev, dup := inTable[m]; dup {
				t.Errorf("方法 %s 同時列在 %s 與 %s", m, prev, o.ID)
			}
			inTable[m] = o.ID
		}
	}
	for _, m := range methods {
		_, ok := inTable[m]
		reason, ex := service.AuthzExempt[m]
		switch {
		case ok && ex:
			t.Errorf("方法 %s 同時在操作表與 AuthzExempt", m)
		case !ok && !ex:
			t.Errorf("方法 %s 帶 *model.Principal 但不在操作表(authz_table.go),也不在 AuthzExempt:新增方法要登記操作名與所需能力", m)
		case ex && strings.TrimSpace(reason) == "":
			t.Errorf("AuthzExempt[%s] 沒寫原因", m)
		}
	}
	for m, op := range inTable {
		if !actual[m] {
			t.Errorf("操作表的 %s 列了方法 %s,但 core/service 沒有這個帶 Principal 的公開方法(改名或刪除了?)", op, m)
		}
	}
	for m := range service.AuthzExempt {
		if !actual[m] {
			t.Errorf("AuthzExempt 列了 %s,但 core/service 沒有這個方法", m)
		}
	}
}

// 表本身的基本一致性:操作名唯一、欄位值合法、Level 與 Agent 的組合有意義
func TestAuthzTableWellFormed(t *testing.T) {
	ids := map[string]bool{}
	levels := map[service.AuthzLevel]bool{service.LvAuthenticated: true, service.LvOrgMember: true, service.LvOrgAdmin: true, service.LvServerAdmin: true}
	agents := map[service.AuthzAgent]bool{service.AgentNo: true, service.AgentOrg: true, service.AgentSelf: true, service.AgentOnly: true}
	scopes := map[string]bool{"server": true, "org": true, "project": true, "self": true}
	for _, o := range service.AuthzOps {
		if o.ID == "" || ids[o.ID] {
			t.Errorf("操作名空白或重複:%q", o.ID)
		}
		ids[o.ID] = true
		if !levels[o.Level] || !agents[o.Agent] || !scopes[o.Scope] || o.Group == "" || len(o.Methods) == 0 {
			t.Errorf("%s 欄位不合法:%+v", o.ID, o)
		}
		if (o.Level == service.LvOrgAdmin || o.Level == service.LvServerAdmin) && (o.Agent != service.AgentNo) {
			t.Errorf("%s 要管理員,角色憑證卻不是 AgentNo(角色身分永遠不是管理員)", o.ID)
		}
		if o.Level == service.LvServerAdmin && o.Scope != "server" {
			t.Errorf("%s 要伺服器管理員,範圍應是 server", o.ID)
		}
		for _, r := range o.Routes {
			if !strings.HasSuffix(r, " [a]") && !strings.HasSuffix(r, " [aa]") {
				t.Errorf("%s 的路由 %q 要以 [a] 或 [aa] 結尾", o.ID, r)
			}
			// 角色憑證不能呼叫的操作,不該走 authedOrAgent 的路由之外還開放(authedOrAgent 路由的 service 方法必須自己擋)
			if strings.HasSuffix(r, " [aa]") && o.Agent == service.AgentNo && o.ID != "channel.create" && o.ID != "channel.update" && o.ID != "channel.members" {
				t.Logf("注意:%s 的路由 %q 接受角色憑證,但表上 Agent=no(靠 service 層拒絕)", o.ID, r)
			}
		}
	}
}

// 表的路由與 core/api 實際註冊的 Svc 呼叫對得上:api 用到的每個 Svc 方法都在表裡(掃 core/api,不含測試)
func TestAuthzTableCoversEveryAPIServiceCall(t *testing.T) {
	fs := token.NewFileSet()
	pkgs, err := parser.ParseDir(fs, "../api", func(i os.FileInfo) bool { return !strings.HasSuffix(i.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	open := map[string]string{ // 未登入入口或不是 service 操作
		"LoginWithEvoleap": "未登入入口(頻率限制)", "LoginWithLDAP": "未登入入口(頻率限制)", "ClaimPairCode": "未登入入口(配對碼一次性、頻率限制)",
		"Authenticate": "驗證 token", "AuthenticateAgent": "驗證角色憑證",
	}
	for _, p := range pkgs {
		for fn, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				se, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				inner, ok := se.X.(*ast.SelectorExpr)
				if !ok || inner.Sel.Name != "Svc" {
					return true
				}
				name := se.Sel.Name
				if _, ok := open[name]; ok {
					return true
				}
				if name == "Store" { // PUT /api/orgs/{org} 在 UpdateOrg 成功後讀回
					return true
				}
				if _, ok := service.AuthzOpByMethod(name); !ok {
					t.Errorf("%s 呼叫 Svc.%s,但操作表沒有這個方法", fn, name)
				}
				return true
			})
		}
	}
}

// (b) AuthzTools 與 model.CoreTools 逐欄一致;每個工具有對應操作(或在 AuthzToolsNoOp 寫明原因);Op.Tools 指到真的工具
func TestAuthzToolMarksMatchCoreTools(t *testing.T) {
	marks := map[string]service.AuthzToolMark{}
	for _, m := range service.AuthzTools {
		if _, dup := marks[m.Name]; dup {
			t.Errorf("AuthzTools 重複:%s", m.Name)
		}
		marks[m.Name] = m
	}
	core := map[string]model.CoreTool{}
	for _, c := range model.CoreTools {
		core[c.Name] = c
		m, ok := marks[c.Name]
		if !ok {
			t.Errorf("model.CoreTools 有 %s,AuthzTools 沒有", c.Name)
			continue
		}
		if m.Admin != c.Admin || m.Agent != c.Agent || m.Worker != c.Worker || m.Needs != c.Needs {
			t.Errorf("工具 %s 標記不一致:表 %+v;tools.go Admin=%v Agent=%v Worker=%v Needs=%q", c.Name, m, c.Admin, c.Agent, c.Worker, c.Needs)
		}
	}
	for name := range marks {
		if _, ok := core[name]; !ok {
			t.Errorf("AuthzTools 有 %s,model.CoreTools 沒有", name)
		}
	}
	linked := map[string][]service.AuthzOp{}
	for _, o := range service.AuthzOps {
		for _, tn := range o.Tools {
			if _, ok := core[tn]; !ok {
				t.Errorf("操作 %s 的 Tools 列了 %s,model.CoreTools 沒有這個工具", o.ID, tn)
			}
			linked[tn] = append(linked[tn], o)
		}
	}
	for _, c := range model.CoreTools {
		if len(linked[c.Name]) == 0 {
			if reason, ok := service.AuthzToolsNoOp[c.Name]; !ok || reason == "" {
				t.Errorf("工具 %s 沒有任何操作指向它,也不在 AuthzToolsNoOp(寫明原因)", c.Name)
			}
		} else if _, ok := service.AuthzToolsNoOp[c.Name]; ok {
			t.Errorf("工具 %s 同時有對應操作與 AuthzToolsNoOp", c.Name)
		}
	}
}

// 工具曝光與操作的角色憑證欄位互相對得上。已知差異(表上的工具不開給角色端點,但 service 允許角色憑證)列在白名單,
// 不改行為;白名單的項目被修掉(兩邊一致了)時這個測試會要求把它從白名單拿掉。
var toolAgentKnownDiff = map[string]string{
	"agents":           "ListAgents/GetAgent 允許角色憑證看自己,MCP agents 只開管理端點(agent_context 取代)",
	"chain":            "GetChain 允許角色憑證,MCP chain 只開管理端點(agent_context 帶上下游)",
	"report_problem":   "走 CreateIssue(同 issue_create,角色憑證可以),MCP 的 report_problem 只開管理端點(ISS-68)",
	"incidents":        "R-4:REST/service 對角色憑證開放事件,MCP 不開",
	"incident_declare": "R-4",
	"incident_update":  "R-4",
}

// 表上有 NeedDocRead 的工具,但 service 方法本身不檢查 docAccess(R-8 已修,目前沒有已知差異)
var toolNeedsKnownDiff = map[string]string{}

func TestAuthzToolsConsistentWithOps(t *testing.T) {
	for _, c := range model.CoreTools {
		var ops []service.AuthzOp
		for _, o := range service.AuthzOps {
			for _, tn := range o.Tools {
				if tn == c.Name {
					ops = append(ops, o)
				}
			}
		}
		if len(ops) == 0 {
			continue
		}
		agentAllowed, agentDenied := false, false
		hasDocRead, hasDocWrite := true, true
		for _, o := range ops {
			if o.Agent == service.AgentNo {
				agentDenied = true
			} else {
				agentAllowed = true
			}
			if c.Needs == model.NeedDocRead && !hasCap(o, service.CapDocRead) {
				hasDocRead = false
			}
			if c.Needs == model.NeedDocWrite && !hasCap(o, service.CapDocWrite) {
				hasDocWrite = false
			}
		}
		// 角色端點會列出的工具(Agent=true):對應的操作不能全是角色憑證不可呼叫的
		if c.Agent && !agentAllowed {
			t.Errorf("工具 %s 開給角色端點(Agent=true),但它對應的操作 %v 全都 Agent=no", c.Name, opIDs(ops))
		}
		if c.Agent && agentDenied && c.Name != "agent_context" {
			t.Errorf("工具 %s 開給角色端點(Agent=true),但對應操作 %v 有 Agent=no", c.Name, opIDs(ops))
		}
		// 工具只開管理端點,但 service 允許角色憑證:列為已知差異;沒列就是新的不一致
		_, known := toolAgentKnownDiff[c.Name]
		diff := !c.Agent && c.Admin && agentAllowed && !agentDenied
		switch {
		case diff && !known:
			t.Errorf("新的不一致:工具 %s 不開給角色端點,但對應操作 %v 允許角色憑證;修正表或登記到 toolAgentKnownDiff", c.Name, opIDs(ops))
		case !diff && known:
			t.Errorf("toolAgentKnownDiff[%s] 已不再不一致,請從白名單移除", c.Name)
		}
		_, knownN := toolNeedsKnownDiff[c.Name]
		nd := (c.Needs == model.NeedDocRead && !hasDocRead) || (c.Needs == model.NeedDocWrite && !hasDocWrite)
		switch {
		case nd && !knownN:
			t.Errorf("新的不一致:工具 %s Needs=%s,但對應操作 %v 沒有對應的 docAccess 能力", c.Name, c.Needs, opIDs(ops))
		case !nd && knownN:
			t.Errorf("toolNeedsKnownDiff[%s] 已不再不一致,請從白名單移除", c.Name)
		}
		// Needs 與能力:docWrite 要有 CapDocWrite、channel 要有 CapChannel、downstream 要有 CapDownstream
		if c.Needs == model.NeedChannel || c.Needs == model.NeedDownstream {
			want := service.CapChannel
			if c.Needs == model.NeedDownstream {
				want = service.CapDownstream
			}
			for _, o := range ops {
				if !hasCap(o, want) && o.ID != "channel.create" {
					t.Errorf("工具 %s Needs=%s,操作 %s 沒有能力 %s", c.Name, c.Needs, o.ID, want)
				}
			}
		}
		// 只開給角色端點的工具(委派):操作必須是 AgentOnly
		if c.Agent && !c.Admin && !c.Worker {
			for _, o := range ops {
				if o.Agent != service.AgentOnly {
					t.Errorf("工具 %s 只開給角色端點,操作 %s 應是 AgentOnly", c.Name, o.ID)
				}
			}
		}
		// 執行者端點(Worker):操作要求領取者或是領取/查詢類,角色憑證都不行
		if c.Worker {
			for _, o := range ops {
				if o.Agent == service.AgentOnly {
					t.Errorf("工具 %s 是 Worker 工具,操作 %s 卻是 AgentOnly", c.Name, o.ID)
				}
			}
		}
	}
}

func hasCap(o service.AuthzOp, c service.AuthzCap) bool {
	for _, x := range o.Caps {
		if x == c {
			return true
		}
	}
	return false
}

func opIDs(ops []service.AuthzOp) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.ID
	}
	return out
}

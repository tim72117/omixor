package mcp_test

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"omixor-core/mcp"
)

// 以 node 執行 decisions.html 的 <script>,用最小的假 DOM 與假宿主驗證畫面行為(repo 沒有 JS 測試框架)。
// 沒有 node 時略過;CI 的 test.yml 有 setup-node,所以 CI 會真的跑。
const decisionsHarness = `
const hostCaps = JSON.parse(process.env.HOST_CAPS);
function node(tag) {
  const n = { tag, children: [], listeners: {}, className: "", textContent: "", value: "", disabled: false, style: { setProperty() {} },
    appendChild(c) { this.children.push(c); return c; },
    addEventListener(t, f) { this.listeners[t] = f; },
    setAttribute() {}, getBoundingClientRect() { return { height: 10 }; } };
  Object.defineProperty(n, "innerHTML", { set() { this.children = []; this.textContent = ""; }, get() { return ""; } });
  return n;
}
const root = node("div");
let handler, nextAnswer;
global.document = { getElementById: (id) => (id === "root" ? root : null), createElement: node,
  createTextNode: (t) => ({ tag: "#text", textContent: t, children: [] }), documentElement: node("html"), head: node("head") };
global.window = { innerWidth: 400, addEventListener(t, f) { handler = f; },
  parent: { postMessage(m) { setImmediate(() => reply(m)); } } };
function send(m) { m.jsonrpc = "2.0"; handler({ source: window.parent, data: m }); }
const open = { id: "DEC-1", kind: "decision", status: "open", title: "pick", options: [{ id: "a", label: "A" }] };
const result = (d) => ({ structuredContent: d });
const listData = () => result({ decisions: [open], total: 1 });
function reply(m) {
  if (m.method === "ui/initialize") return send({ id: m.id, result: { hostCapabilities: hostCaps } });
  if (m.method === "tools/call" && process.env.REFUSE) return send({ id: m.id, error: { code: -32601, message: "Method not found" } });
  if (m.method === "tools/call") send({ id: m.id, result: m.params.name === "issue_decision_answer" ? nextAnswer() : listData() });
}
const tick = () => new Promise((r) => setImmediate(() => setImmediate(r)));
function all(n, out = []) { out.push(n); (n.children || []).forEach((c) => all(c, out)); return out; }
const buttons = (label) => all(root).filter((n) => n.tag === "button" && (label ? n.textContent.indexOf(label) === 0 : n.className.indexOf("act") === 0));
const answerButtons = () => buttons().filter((b) => b.textContent !== "重新整理");
const texts = () => all(root).map((n) => n.textContent).join("|");
const snapshot = () => ({ text: texts(), disabled: buttons("其他").map((b) => b.disabled) });
function answerWith(reply) {
  nextAnswer = reply;
  buttons("其他")[0].listeners.click();
  all(root).filter((n) => n.tag === "textarea")[0].value = "note";
  buttons("確認送出")[0].listeners.click();
}
(async () => {
  eval(process.env.VIEW_JS);
  await tick();
  send({ method: "ui/notifications/tool-input", params: { arguments: { org: "o" } } });
  send({ method: "ui/notifications/tool-result", params: listData() });
  const out = { initialButtons: buttons().length, initialText: texts() };
  if (process.env.REFUSE) {
    buttons("重新整理")[0].listeners.click();
    await tick();
    out.refused = { buttons: buttons().length, text: texts() };
  } else if (hostCaps.serverTools !== false) {
    answerWith(() => result({ ...open, status: "decided", answer: "other" }));
    await tick();
    out.afterAnswer = { buttons: answerButtons().length, text: texts() };
    send({ method: "ui/notifications/tool-result", params: listData() });
    answerWith(() => ({ isError: true, structuredContent: { error: { status: 403, message: "read-only connection" } } }));
    await tick();
    out.readOnly = snapshot();
    buttons("重新整理")[0].listeners.click();
    await tick();
    out.afterRefresh = snapshot();
  }
  console.log(JSON.stringify(out));
})();
`

func decisionsScript(t *testing.T) string {
	t.Helper()
	for _, r := range mcp.UIResources() {
		if r.URI == mcp.UIDecisionsURI {
			m := regexp.MustCompile(`(?s)<script>(.*)</script>`).FindStringSubmatch(r.Text)
			if m == nil {
				t.Fatal("decisions view has no <script>")
			}
			return m[1]
		}
	}
	t.Fatal("decisions view not registered")
	return ""
}

// refuse:假宿主對每個 tools/call 回 JSON-RPC「Method not found」
func runDecisionsView(t *testing.T, caps string, refuse bool) map[string]json.RawMessage {
	t.Helper()
	cmd := exec.Command("node", "-e", decisionsHarness)
	cmd.Env = append(cmd.Environ(), "HOST_CAPS="+caps, "VIEW_JS="+decisionsScript(t))
	if refuse {
		cmd.Env = append(cmd.Env, "REFUSE=1")
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, b)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &out); err != nil {
		t.Fatalf("bad harness output: %v\n%s", err, b)
	}
	return out
}

func decodeField(t *testing.T, out map[string]json.RawMessage, key string, v any) {
	t.Helper()
	if err := json.Unmarshal(out[key], v); err != nil {
		t.Fatalf("field %s: %v (%s)", key, err, out[key])
	}
}

type viewState struct {
	Text     string
	Disabled []bool
}

func TestDecisionsViewBehavior(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	t.Run("answer marks decided", func(t *testing.T) {
		var a struct {
			Buttons int
			Text    string
		}
		decodeField(t, runDecisionsView(t, `{"serverTools":{}}`, false), "afterAnswer", &a)
		if a.Buttons != 0 || !strings.Contains(a.Text, "已處理") {
			t.Errorf("after answer = %+v, want no answer buttons and decided", a)
		}
	})
	t.Run("read-only error then refresh re-enables", func(t *testing.T) {
		out := runDecisionsView(t, `{"serverTools":{}}`, false)
		var ro, ar viewState
		decodeField(t, out, "readOnly", &ro)
		decodeField(t, out, "afterRefresh", &ar)
		if !strings.Contains(ro.Text, "read-only") || len(ro.Disabled) == 0 || !ro.Disabled[0] {
			t.Errorf("read-only state = %+v, want message and disabled buttons", ro)
		}
		if strings.Contains(ar.Text, "read-only") || len(ar.Disabled) == 0 || ar.Disabled[0] {
			t.Errorf("after refresh = %+v, want enabled and no warning", ar)
		}
	})
	// 宿主不一定宣告 serverTools,所以沒宣告時照樣顯示按鈕,直到宿主真的拒絕
	t.Run("no serverTools still renders buttons and a diagnostic line", func(t *testing.T) {
		out := runDecisionsView(t, `{}`, false)
		var text string
		decodeField(t, out, "initialText", &text)
		if string(out["initialButtons"]) == "0" || !strings.Contains(text, "tools: unknown") || !strings.Contains(text, "args: yes") {
			t.Errorf("buttons = %s, text = %q, want buttons and diagnostic line", out["initialButtons"], text)
		}
	})
	t.Run("host refusing tools/call hides buttons and shows message", func(t *testing.T) {
		var r struct {
			Buttons int
			Text    string
		}
		decodeField(t, runDecisionsView(t, `{}`, true), "refused", &r)
		if r.Buttons != 0 || !strings.Contains(r.Text, "此宿主不允許從畫面呼叫工具") {
			t.Errorf("after refusal = %+v, want no buttons and refusal message", r)
		}
	})
	t.Run("serverTools false hides buttons from the start", func(t *testing.T) {
		out := runDecisionsView(t, `{"serverTools":false}`, false)
		var text string
		decodeField(t, out, "initialText", &text)
		if string(out["initialButtons"]) != "0" || !strings.Contains(text, "此宿主不允許從畫面呼叫工具") || !strings.Contains(text, "tools: no") {
			t.Errorf("buttons = %s, text = %q", out["initialButtons"], text)
		}
	})
}

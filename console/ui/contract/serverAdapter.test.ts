// omixor server 的資料層(src/server):view / action / tool → 核心 REST,以及能力判斷
import { test } from "node:test";
import assert from "node:assert/strict";
import { createServerAdapter, type ServerHttp } from "../src/server/adapter.ts";
import { attachmentSrc, backendDevTabs, backendHas, onServer, setCapabilities } from "../src/server/scope.ts";
import { loginErrorText } from "../src/auth/serverLoginError.ts";

const ORGS = [{ id: "a", name: "A", myRole: "member" }, { id: "b", name: "B", myRole: "admin" }];

function make(selected = "", reply: Record<string, unknown> = {}) {
  const calls: string[] = [];
  let org = selected;
  let reloads = 0;
  const http: ServerHttp = {
    get: async (url) => {
      calls.push("GET " + url);
      if (url === "/api/orgs") return { result: ORGS };
      if (url === "/api/version") return { features: ["issue_accept"] };
      return { result: reply[url] ?? [] };
    },
    post: async (url, body) => {
      calls.push(`POST ${url} ${JSON.stringify(body)}`);
      return reply[url] ?? { id: "x" };
    },
  };
  const adapter = createServerAdapter({
    http, origin: "https://example.test", user: () => ({ id: "u1", name: "Amy", email: "amy@x" }),
    org: { selected: () => org, select: (v) => { org = v; }, reload: () => { reloads++; } },
  });
  return { adapter, calls, org: () => org, reloads: () => reloads };
}

/** 失敗時的 HTTP 狀態(成功回 0) */
const statusOf = (p: Promise<unknown>) => p.then(() => 0, (e: any) => e.status as number);

test("orgs:沒選過用第一個;選的不能用了就換掉並重新載入", async () => {
  const a = make();
  assert.equal(((await a.adapter.get("/api/view/orgs")) as any).current, "a");
  assert.equal(a.reloads(), 0);
  const gone = make("z");
  await gone.adapter.get("/api/view/orgs");
  assert.equal(gone.org(), "a");
  assert.equal(gone.reloads(), 1);
});

test("view:打目前組織的核心 REST;沒有後端的清單是空的、其他 404", async () => {
  const a = make("b");
  await a.adapter.get("/api/view/issues");
  assert.ok(a.calls.includes("GET /api/orgs/b/issues"));
  assert.deepEqual(await a.adapter.get("/api/view/pull_requests"), []);
  assert.deepEqual(await a.adapter.get("/api/view/members"), { canEdit: true, orgAdmin: true });
  assert.equal(((await a.adapter.get("/api/view/me")) as any).id, "amy@x");
  assert.ok(((await a.adapter.get("/api/view/account")) as any).features.includes("issue_accept"));
  assert.equal(await statusOf(a.adapter.get("/api/view/gcp")), 404);
  assert.equal(await statusOf(a.adapter.post("/api/action/server_reboot", {})), 404);
});

test("issue_query:counts 只看專案範圍,清單照條件", async () => {
  const a = make("a", {
    "/api/orgs/a/issues?project=PRJ-1": [{ id: "1", status: "open" }, { id: "2", status: "closed" }],
    "/api/orgs/a/issues?status=open&project=PRJ-1": [{ id: "1", status: "open" }],
  });
  const r = (await a.adapter.post("/api/action/issue_query", { q: "", status: "open", project: "PRJ-1" })).data as any;
  assert.deepEqual(r.counts, { all: 2, open: 1, closed: 1 });
  assert.equal(r.total, 2);
  assert.deepEqual(r.issues[0].comments, []);
});

test("寫入:路由、參數改名、只有寫入要求重讀", async () => {
  const a = make("a");
  const up = await a.adapter.post("/api/tool/issue_update", { id: "ISS-1", return: "full", version: 3, status: "closed" });
  assert.equal(up.write, true);
  await a.adapter.post("/api/tool/issue_update", { id: "ISS-1", return: "full", comment: "hi" });
  await a.adapter.post("/api/tool/issue_accept", { id: "ISS-1", release: true });
  await a.adapter.post("/api/tool/issue_item_report", { id: "ISS-1.2", status: "blocked", block_reason: "r", wait_for_kind: "external", wait_for: "w" });
  await a.adapter.post("/api/tool/issue_item_update", { id: "ISS-1.2", cancel: true });
  await a.adapter.post("/api/tool/issue_decision_add", { kind: "help", title: "t", source_issue: "ISS-1" });
  await a.adapter.post("/api/tool/issue_decision_answer", { id: "DEC-1", answer: "done" });
  const img = await a.adapter.post("/api/attachments", { data: "AAAA" });
  assert.deepEqual(img.data, { url: "/api/attachments/x" });
  assert.deepEqual(a.calls.filter((c) => c.startsWith("POST")), [
    'POST /api/orgs/a/issues/ISS-1 {"status":"closed","version":3}',
    'POST /api/orgs/a/issues/ISS-1/comments {"text":"hi"}',
    "POST /api/orgs/a/issues/ISS-1/release {}",
    'POST /api/orgs/a/issues/ISS-1/items/ISS-1.2/report {"status":"blocked","blockReason":"r","waitForKind":"external","waitFor":"w"}',
    "POST /api/orgs/a/issues/ISS-1/items/ISS-1.2/cancel {}",
    'POST /api/orgs/a/decisions {"kind":"help","title":"t","sourceIssue":"ISS-1"}',
    'POST /api/orgs/a/decisions/DEC-1/answer {"answer":"done"}',
    'POST /api/orgs/a/attachments {"data":"AAAA"}',
  ]);
  for (const read of ["/api/action/issue_board", "/api/tool/issue_items", "/api/tool/issue_decisions"])
    assert.equal((await a.adapter.post(read, { issue: "ISS-1" })).write, false, read);
});

test("能力:console(沒有 capabilities)全部都有;server 只有列出的", () => {
  const id = "0123456789abcdef0123456789abcdef.png";
  setCapabilities(undefined);
  assert.equal(onServer(), false);
  assert.ok(backendHas("settings") && backendHas("development.repos"));
  assert.equal(attachmentSrc("/api/attachments/" + id, "a"), "/api/attachments/" + id);
  setCapabilities({ sections: ["development"], devTabs: ["board", "issues", "decisions"] });
  assert.ok(backendHas("development") && backendHas("development.board"));
  assert.ok(!backendHas("settings") && !backendHas("overview") && !backendHas("assistant") && !backendHas("development.repos"));
  assert.deepEqual(backendDevTabs(["overview", "board", "issues", "repos"]), ["board", "issues"]);
  assert.equal(attachmentSrc("/api/attachments/" + id, "a"), "/api/web/orgs/a/attachments/" + id);
  assert.equal(attachmentSrc("https://example.test/x.png", "a"), "https://example.test/x.png");
  setCapabilities(undefined);
});

test("登入錯誤只顯示固定的說明", () => {
  assert.equal(loginErrorText(null), undefined);
  assert.ok(loginErrorText("denied")!.includes("權限"));
  assert.equal(loginErrorText("<script>"), loginErrorText("unavailable"));
});

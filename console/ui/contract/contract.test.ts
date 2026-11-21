// 跨語言契約(執行期):讀 core/model/testdata/contract/*.json(core API 實際回傳的形狀),
// 檢查前端用到的關鍵欄位都在、列舉值前端認得(標籤表查得到),再餵給前端實際的轉換函式。
// 執行:npm test(node --experimental-strip-types --test;不加測試框架依賴)。型別的檢查在 types.check.ts(tsc)。
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  agentAccess, arr, boardDropStatus, DOC_ACCESS_LABEL, INC_STATUS_LABEL, leaseLabel, PRIORITY_LABEL, ROUTINE_KIND_LABEL, tabTitle, TASK_LABEL, taskOpen,
  type Agent, type AgentTask, type CabinetDoc, type CabinetSection, type Channel, type ChannelMessage, type ChannelPage,
  type Incident, type OrgChain, type Project,
} from "../src/components/models.ts";
import {
  conflictCurrent, draftChanges, draftDiff, draftOf, findIssue, isIssueRef, isVersionConflict, issueKey, issueRefLabel, mergeLatest,
  projectHasIssues, projectKeyProblem, cleanProjectKey, type Issue,
} from "../src/sections/development/issueModel.ts";
import { ApiError, unwrapPost } from "../src/api.ts";
import type { ActorView } from "../src/types.ts";

const dir = new URL("../../../core/model/testdata/contract/", import.meta.url);
const load = <T,>(name: string): T => JSON.parse(readFileSync(new URL(name, dir), "utf8")) as T;

const ISO = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/;
function isTime(v: unknown, what: string) {
  assert.equal(typeof v, "string", `${what} 應該是字串`);
  assert.ok(ISO.test(v as string), `${what} 應該是到秒的 RFC 3339:${v}`);
  assert.ok(!Number.isNaN(new Date(v as string).getTime()), `${what} 解不開`);
}
function has(obj: object, keys: string[], what: string) {
  for (const k of keys) assert.ok(k in obj, `${what} 少了欄位 ${k}`);
}

test("Issue(開發 › Issue)", () => {
  const list = arr<Issue>(load("issue_list.json"));
  assert.deepEqual(list.map(issueKey), ["APP-12", "ISS-3"]); // 顯示用 key;id 是內部 UUIDv7
  const i = load<Issue>("issue.json");
  has(i, ["key", "project", "number", "version"], "issue");
  assert.equal(issueKey(i), "APP-12");
  assert.equal(i.project, "PRJ-1");
  assert.equal(typeof i.version, "number");
  has(i, ["id", "title", "body", "status", "priority", "labels", "source", "link", "createdBy", "createdAt", "updatedAt", "comments", "repo", "assignee", "closedAt"], "issue");
  assert.equal(i.status, "closed");
  assert.equal(PRIORITY_LABEL[i.priority], "高");
  assert.equal(i.repo, "github:acme/web");
  assert.equal(i.assignee, "eng");
  assert.deepEqual(i.labels, ["bug", "前端"]);
  isTime(i.createdAt, "issue.createdAt");
  isTime(i.closedAt, "issue.closedAt");
  assert.equal(i.comments.length, 2);
  for (const c of i.comments) {
    has(c, ["id", "author", "text", "at"], "issue.comments[]");
    isTime(c.at, "comment.at");
  }
  // 只有必填欄位的:選填的省略
  const m = list[1];
  assert.equal(m.status, "open");
  assert.equal(m.repo, undefined);
  assert.equal(m.assignee, undefined);
  assert.equal(m.closedAt, undefined);
});

test("虛擬角色", () => {
  const a = load<Agent>("agent.json");
  has(a, ["id", "name", "role", "symbol", "color", "instructions", "active", "routines", "createdAt", "docAccess"], "agent");
  for (const v of Object.values(a.docAccess)) assert.ok(DOC_ACCESS_LABEL[v], `docAccess 的值前端不認得:${v}`);
  assert.equal(agentAccess(a, "docs"), "write");
  assert.equal(agentAccess(a, "levels", "/studio/tools"), "read");
  assert.equal(agentAccess(a, "overview"), undefined, "none = 不能存取");
  assert.equal(a.routines.length, 2);
  for (const r of a.routines) {
    has(r, ["id", "title", "instructions", "kind", "hour", "minute", "weekday", "everyHours", "priority", "enabled", "createdAt"], "routine");
    assert.ok(ROUTINE_KIND_LABEL[r.kind], `routine.kind 前端不認得:${r.kind}`);
    assert.ok(PRIORITY_LABEL[r.priority], `routine.priority 前端不認得:${r.priority}`);
  }
  isTime(a.routines[0].lastRunAt, "routine.lastRunAt");
  assert.equal(a.routines[1].lastRunAt, undefined);
  const list = arr<Agent>(load("agent_list.json"));
  assert.deepEqual(list.map((x) => x.active), [true, false]);
  assert.deepEqual(list[1].docAccess, {});
});

test("任務", () => {
  const t = load<AgentTask>("agent_task.json");
  has(t, ["id", "title", "instructions", "agentId", "status", "priority", "createdBy", "routineId", "createdAt", "updatedAt", "result", "links", "notes", "lockedUntil", "claimedBy"], "task");
  assert.ok(TASK_LABEL[t.status], `task.status 前端不認得:${t.status}`);
  assert.ok(taskOpen(t.status));
  isTime(t.lockedUntil, "task.lockedUntil");
  assert.equal(leaseLabel(t), "鎖定已逾時", "範例的租約已過期");
  assert.equal(leaseLabel({ ...t, lockedUntil: new Date(Date.now() + 3600_000).toISOString() })?.endsWith("(tim@example.com)"), true);
  for (const n of t.notes) {
    has(n, ["id", "author", "text", "at"], "task.notes[]");
    isTime(n.at, "note.at");
  }
  const [, q] = arr<AgentTask>(load("agent_task_list.json"));
  assert.equal(q.status, "queued");
  assert.equal(leaseLabel(q), null);
  assert.equal(q.routineId, undefined);
  // 領取結果:{agent, task, credential};沒任務時 task 是 null
  const claim = load<{ agent?: Agent; task: AgentTask | null; credential?: { agentId: string; taskId: string; secret: string; expiresAt: string } }>("agent_task_claim.json");
  assert.equal(claim.task?.id, "T-7");
  has(claim.credential!, ["agentId", "taskId", "secret", "expiresAt"], "claim.credential");
  assert.equal(load<{ task: AgentTask | null }>("agent_task_claim_empty.json").task, null);
});

test("頻道", () => {
  const c = load<Channel>("channel.json");
  has(c, ["id", "name", "topic", "issueId", "members", "createdBy", "createdAt", "updatedAt", "unread", "lastMessageAt"], "channel");
  assert.equal(c.unread, 3);
  isTime(c.lastMessageAt, "channel.lastMessageAt");
  const list = arr<Channel>(load("channel_list.json"));
  assert.equal(list[1].lastMessageAt, null, "沒訊息時是 null(不是省略)");
  assert.equal(list[1].issueId, "", "沒綁 Issue 是空字串");
  const m = load<ChannelMessage>("channel_message.json");
  has(m, ["id", "channelId", "author", "text", "mentions", "at"], "message");
  assert.deepEqual(m.mentions, ["agent:eng"]);
  const p = load<ChannelPage>("channel_messages.json");
  assert.equal(p.hasMore, true);
  assert.deepEqual(p.messages.map((x) => x.id), ["M-4", "M-5"]);
});

test("事件", () => {
  const i = load<Incident>("incident.json");
  has(i, ["id", "title", "sev", "status", "services", "commander", "startedAt", "resolvedAt", "impact", "timeline", "aiSummary", "followUps"], "incident");
  assert.ok(INC_STATUS_LABEL[i.status], `incident.status 前端不認得:${i.status}`);
  isTime(i.resolvedAt, "incident.resolvedAt");
  const kinds = new Set(["status", "note", "action", "alert", "deploy", "aiFinding"]);
  for (const e of i.timeline) {
    has(e, ["id", "date", "actor", "kind", "text"], "incident.timeline[]");
    assert.ok(kinds.has(e.kind), `timeline.kind 前端不認得:${e.kind}`);
    assert.ok(["person", "ai", "system"].includes(e.actor.kind), `actor.kind 前端不認得:${e.actor.kind}`);
  }
  assert.deepEqual(i.timeline.map((e) => e.actor), [{ kind: "system" }, { kind: "person", id: "tim@example.com" }, { kind: "ai", id: "mcp" }]);
  const [, m] = arr<Incident>(load("incident_list.json"));
  assert.equal(m.resolvedAt, undefined);
  assert.equal(m.aiSummary, undefined);
});

test("文件櫃", () => {
  const c = load<{ layout: CabinetSection[]; docs: CabinetDoc[]; layoutSaved: boolean }>("cabinet.json");
  assert.deepEqual(c.layout.map((s) => s.id), ["overview", "levels"]);
  const tabs = c.layout[1].tabs;
  assert.equal(tabTitle(tabs[0]), "L0 AI 工具介紹");
  assert.equal(tabTitle(tabs[1]), "AI 工具技巧");
  assert.equal(tabs[1].code, undefined);
  assert.deepEqual(c.layout[0].tabs, [], "沒有頁籤是空陣列(不是 null)");
  const d = load<CabinetDoc>("cabinet_doc.json");
  has(d, ["id", "title", "body", "section", "tab", "tags", "author", "createdAt", "updatedAt"], "doc");
  assert.equal(d.tab, "/studio/tools");
  assert.equal(c.docs[1].tab, undefined, "放在區塊本身的文件沒有 tab");
});

test("專案與專案的文件櫃", () => {
  const p = load<Project>("project.json");
  has(p, ["id", "name", "key", "description", "createdAt", "updatedAt"], "project");
  assert.ok(/^PRJ-\d+$/.test(p.id), `project.id 應該是 PRJ-<n>:${p.id}`);
  isTime(p.createdAt, "project.createdAt");
  isTime(p.updatedAt, "project.updatedAt");
  const list = arr<Project>(load("project_list.json"));
  assert.deepEqual(list.map((x) => x.id), ["PRJ-1", "PRJ-2"]);
  assert.equal(list[0].description, "", "沒有說明是空字串");
  const d = load<CabinetDoc>("cabinet_doc_project.json");
  has(d, ["id", "title", "body", "section", "tags", "author", "createdAt", "updatedAt", "project"], "doc");
  assert.equal(d.project, "PRJ-2");
  assert.equal(load<CabinetDoc>("cabinet_doc.json").project, undefined, "未分專案的文件不帶 project");
  const c = load<{ project: string; layout: CabinetSection[]; docs: CabinetDoc[]; layoutSaved: boolean }>("cabinet_project.json");
  assert.equal(c.project, "PRJ-2");
  assert.deepEqual(c.layout.map((s) => s.id), ["spec"]);
  assert.equal(tabTitle(c.layout[0].tabs[0]), "P1 首頁");
  assert.ok(c.docs.every((x) => x.project === c.project), "專案的文件櫃只有那個專案的文件");
  assert.equal(load<{ project?: string }>("cabinet.json").project, undefined, "未分專案的文件櫃不帶 project");
});

test("組織鏈", () => {
  const ch = load<OrgChain>("org_chain.json");
  assert.deepEqual(ch.edges.map((e) => [e.from, e.to, e.label]), [["user:tim@example.com", "agent:pm", "目標"], ["agent:pm", "agent:eng", ""]]);
  isTime(ch.updatedAt, "chain.updatedAt");
});

test("actor 物件(ISS-36 C-3):和相容字串並存,id 前綴對得上種類", () => {
  const PREFIX = { user: "usr_", agent: "agt_", system: "sys_" } as const;
  const check = (a: ActorView | undefined, what: string) => {
    assert.ok(a, `${what} 應該有 actor 物件`);
    has(a!, ["id", "kind", "handle", "name", "status"], what);
    assert.ok(a!.id.startsWith(PREFIX[a!.kind]), `${what}:id ${a!.id} 對不上種類 ${a!.kind}`);
  };
  const i = load<Issue>("issue.json");
  check(i.createdByActor, "issue.createdByActor");
  check(i.assigneeActor, "issue.assigneeActor");
  check(i.deletedByActor, "issue.deletedByActor");
  assert.equal(i.createdBy, "tim@example.com"); // 相容字串照舊
  assert.equal(i.createdByActor!.email, "tim@example.com");
  assert.equal(i.assignee, "eng");
  assert.equal(i.assigneeActor!.handle, "eng"); // 角色 handle = 角色 id
  assert.equal(i.assigneeActor!.email, undefined); // 只有使用者有 email
  assert.equal(i.deletedByActor!.status, "disabled");
  for (const c of i.comments) check(c.authorActor, "comment.authorActor");
  const t = load<AgentTask>("agent_task.json");
  check(t.createdByActor, "task.createdByActor");
  check(t.claimedByActor, "task.claimedByActor");
  for (const n of t.notes) check(n.authorActor, "task.notes[].authorActor");
  const ch = load<Channel>("channel.json");
  assert.equal(ch.memberActors!.length, ch.members.length); // 順序同 members
  ch.memberActors!.forEach((a) => check(a, "channel.memberActors[]"));
  check(ch.createdByActor, "channel.createdByActor");
  const m = load<ChannelMessage>("channel_message.json");
  check(m.authorActor, "message.authorActor");
  assert.equal(m.mentionActors!.length, m.mentions.length);
  const inc = load<Incident>("incident.json");
  check(inc.commanderActor, "incident.commanderActor");
  check(inc.createdByActor, "incident.createdByActor");
  for (const e of inc.timeline) check(e.actorRef, "incident.timeline[].actorRef");
  const chain = load<OrgChain>("org_chain.json");
  for (const e of chain.edges) { check(e.fromActor, "edge.fromActor"); check(e.toActor, "edge.toActor"); }
  const doc = load<CabinetDoc>("cabinet_doc.json");
  check(doc.authorActor, "doc.authorActor");
  // GET /actors:使用者有 email、狀態值在列舉內
  const list = load<ActorView[]>("actor_list.json");
  assert.deepEqual(list.map((a) => a.kind), ["user", "user", "agent", "agent", "system"]);
  for (const a of list) {
    check(a, "actor_list[]");
    assert.ok(["active", "disabled", "suspended", "retired"].includes(a.status));
    assert.equal(a.kind === "user", !!a.email);
  }
  // 稽核與成員
  const audit = load<{ actorRef?: ActorView; userId: string; email: string }[]>("audit_list.json");
  check(audit[0].actorRef, "audit[0].actorRef");
  assert.equal(audit[1].actorRef, undefined); // 沒有操作者(login.denied)沒有物件
  const members = load<{ email: string; actorRef?: ActorView }[]>("member_list.json");
  check(members[0].actorRef, "member.actorRef");
});

test("看板拖放:欄的 dropStatus 和 Issue 現在的 status 不同才能放", () => {
  assert.equal(boardDropStatus("closed", "open"), "closed", "拖到已完成 = 關閉");
  assert.equal(boardDropStatus("open", "closed"), "open", "從已完成拖出 = 重新打開");
  assert.equal(boardDropStatus("open", "open"), null, "待處理 ↔ 進行中、原欄:不能放");
  assert.equal(boardDropStatus("closed", "closed"), null);
  assert.equal(boardDropStatus(null, "open"), null);
  assert.equal(boardDropStatus(undefined, "closed"), null);
});

test("agentAccess 依專案算權限(同 core 的 Agent.AccessIn)", () => {
  const a = load<Agent>("agent.json");
  const g: Agent = {
    ...a,
    docAccess: {
      overview: "read", docs: "write", levels: "read", "/levels/hard": "none",
      "PRJ-1:overview": "write", "PRJ-1:levels": "none", "PRJ-1:/levels/easy": "read",
      "PRJ-1:spec": "read", "PRJ-1:/spec/home": "none",
    },
  };
  // 未分專案:空字串、undefined、null 都一樣,只看沒有前綴的 key
  for (const p of ["", undefined, null]) {
    assert.equal(agentAccess(g, "overview", null, p), "read");
    assert.equal(agentAccess(g, "levels", "/levels/hard", p), undefined, "頁籤 none 優先於區塊 read");
    assert.equal(agentAccess(g, "levels", "/levels/easy", p), "read", "頁籤沒設定:跟區塊");
    assert.equal(agentAccess(g, "spec", undefined, p), undefined, "專案的 key 不適用於未分專案");
  }
  assert.equal(agentAccess(g, "overview"), "read", "省略 project = 未分專案");
  // 專案:只看 "PRJ-1:" 前綴的 key,沒有前綴的 key 不適用
  assert.equal(agentAccess(g, "overview", null, "PRJ-1"), "write");
  assert.equal(agentAccess(g, "docs", null, "PRJ-1"), undefined, "未分專案的 docs:write 不適用於專案");
  assert.equal(agentAccess(g, "levels", "/levels/easy", "PRJ-1"), "read", "頁籤 read 優先於區塊 none");
  assert.equal(agentAccess(g, "levels", "/levels/hard", "PRJ-1"), undefined, "頁籤沒設定、區塊 none");
  assert.equal(agentAccess(g, "spec", "/spec/home", "PRJ-1"), undefined, "頁籤 none 優先於區塊 read");
  assert.equal(agentAccess(g, "spec", "/spec/other", "PRJ-1"), "read");
  // 別的專案、新建的專案:什麼都沒有
  for (const s of ["overview", "docs", "levels", "spec"]) assert.equal(agentAccess(g, s, null, "PRJ-2"), undefined);
  assert.equal(agentAccess({ ...a, docAccess: undefined as unknown as Agent["docAccess"] }, "overview", null, "PRJ-1"), undefined);
});

// ---- Issue 顯示與編輯的純邏輯(issueModel.ts)

const mk = (o: Partial<Issue> & { id: string }): Issue => ({
  title: "t", body: "", status: "open", priority: "normal", labels: [], source: "", link: "", createdBy: "u", createdAt: "", updatedAt: "", comments: [], ...o,
});

test("顯示編號:key 優先,沒有退回 id", () => {
  assert.equal(issueKey(mk({ id: "019a-uuid", key: "APP-12" })), "APP-12");
  assert.equal(issueKey(mk({ id: "ISS-3" })), "ISS-3");
  const l = [mk({ id: "019a-uuid", key: "APP-12" }), mk({ id: "ISS-3" })];
  assert.equal(findIssue(l, "app-12")?.id, "019a-uuid");
  assert.equal(findIssue(l, "019A-UUID")?.id, "019a-uuid");
  assert.equal(findIssue(l, "nope"), undefined);
  assert.equal(issueRefLabel("019a-uuid", l), "APP-12");
  assert.equal(issueRefLabel("ISS-99", l), "ISS-99");
});

test("任務連結是不是 Issue 參照:只認 ISS-n、已知專案代號、UUID", () => {
  assert.ok(isIssueRef("ISS-3"));
  assert.ok(isIssueRef("iss-3"));
  assert.ok(isIssueRef("019a3f2c-8e40-7b1a-9c2d-3e4f5a6b7c8d"));
  assert.ok(isIssueRef("APP-12", [], ["APP"]), "已知專案代號");
  assert.ok(isIssueRef("app-12", [], ["APP"]), "不分大小寫");
  assert.ok(!isIssueRef("APP-12"), "不認得的代號不算(可能是外部連結)");
  assert.ok(!isIssueRef("JIRA-12", [], ["APP"]), "Jira 這類外部編號不算");
  assert.ok(!isIssueRef("JIRA-12", [mk({ id: "x", key: "WEB-3" })]));
  assert.ok(isIssueRef("WEB-9", [mk({ id: "x", key: "WEB-3" })]), "清單裡有同代號的 Issue");
  assert.ok(!isIssueRef("PRJ-1", [], ["PRJ"]));
  assert.ok(!isIssueRef("T-5"));
  assert.ok(!isIssueRef("https://example.com/x"));
  assert.ok(isIssueRef("web-3", [mk({ id: "x", key: "WEB-3" })]));
});

test("版本衝突:看 data 而不是訊息文字", () => {
  const cur = mk({ id: "x", version: 5, title: "別人的" });
  assert.ok(isVersionConflict({ status: 200, data: { current: cur, currentVersion: 5 } }));
  assert.ok(isVersionConflict({ status: 409 }));
  assert.ok(isVersionConflict({ data: { currentVersion: 2 } }));
  assert.ok(!isVersionConflict(new Error("這張 Issue 已被別人更新")), "只有訊息文字不算");
  assert.ok(!isVersionConflict({ status: 200, data: { code: "x" } }));
  assert.ok(!isVersionConflict(undefined));
  assert.equal(conflictCurrent({ data: { current: cur } })?.title, "別人的");
  assert.equal(conflictCurrent({ data: { currentVersion: 5 } }), undefined);
});

test("編輯草稿:只送有改的欄位;重新載入後列出和最新內容不同的欄位", () => {
  const base = mk({ id: "x", version: 3, title: "舊標題", body: "內文", labels: ["bug"], repo: "a/b", project: "PRJ-1" });
  const d = { ...draftOf(base), title: "我改的標題", labels: "bug, 前端", project: "" };
  assert.deepEqual(draftChanges(d, base), { title: "我改的標題", labels: ["bug", "前端"], project: "" });
  assert.deepEqual(draftChanges(draftOf(base), base), {});
  // 別人把內文與標題都改了:我的標題(也改了)與他的不同、內文我沒動但他動了 → 兩個欄位都列出,不靜默蓋掉
  const latest = mk({ id: "x", version: 4, title: "別人的標題", body: "別人改的內文", labels: ["bug", "前端"], repo: "a/b", project: "PRJ-1" });
  const diff = draftDiff(d, latest);
  assert.deepEqual(diff.map((x) => x.field).sort(), ["body", "project", "title"]);
  assert.equal(diff.find((x) => x.field === "title")?.theirs, "別人的標題");
  assert.deepEqual(draftDiff(draftOf(latest), latest), []);
});

test("三方合併:沒改的欄位採用最新值,只有雙方都改而且不同的才算衝突", () => {
  const origin = mk({ id: "x", version: 3, title: "舊標題", body: "舊內文", labels: ["bug"], repo: "a/b", project: "PRJ-1" });
  // 我只改了標題;別人改了內文與標籤(我沒動)
  const mine = { ...draftOf(origin), title: "我的標題" };
  const latest = mk({ id: "x", version: 4, title: "舊標題", body: "別人的內文", labels: ["bug", "前端"], repo: "a/b", project: "PRJ-1" });
  const r = mergeLatest(origin, mine, latest);
  assert.deepEqual(r.conflicts, [], "我沒改的欄位不是衝突");
  assert.equal(r.draft.title, "我的標題", "我改的保留");
  assert.equal(r.draft.body, "別人的內文", "我沒改的自動採用最新");
  assert.equal(r.draft.labels, "bug, 前端");
  assert.deepEqual([...r.adopted].sort(), ["body", "labels"]);
  // 合併後以最新內容當基準:只送我改的標題
  assert.deepEqual(draftChanges(r.draft, latest), { title: "我的標題" });

  // 兩邊都改標題而且不同 → 衝突,保留我的;兩邊改成一樣的不算
  const both = { ...draftOf(origin), title: "我的標題", body: "同一個新內文" };
  const l2 = mk({ id: "x", version: 5, title: "別人的標題", body: "同一個新內文", labels: ["bug"], repo: "a/b", project: "PRJ-1" });
  const r2 = mergeLatest(origin, both, l2);
  assert.deepEqual(r2.conflicts.map((c) => [c.field, c.mine, c.theirs]), [["title", "我的標題", "別人的標題"]]);
  assert.equal(r2.draft.title, "我的標題");
  assert.deepEqual(r2.adopted, []);

  // 別人沒改任何我關心的欄位(只是狀態、優先度、指派動了,版本變了):草稿原樣、沒有衝突、沒有採用
  const quick = mk({ id: "x", version: 6, title: "舊標題", body: "舊內文", labels: ["bug"], repo: "a/b", project: "PRJ-1", priority: "urgent" });
  const r3 = mergeLatest(origin, mine, quick);
  assert.deepEqual(r3.draft, mine);
  assert.deepEqual(r3.conflicts, []);
  assert.deepEqual(r3.adopted, []);

  // 空白差異不算改動(標題、標籤去頭尾空白)
  const spaced = { ...draftOf(origin), title: " 舊標題 ", labels: "bug,  " };
  const r4 = mergeLatest(origin, spaced, latest);
  assert.deepEqual(r4.conflicts, []);
  assert.deepEqual(r4.adopted.sort(), ["body", "labels"], "沒改的欄位(空白不算)採用最新");
});

test("POST 回應:2xx 但不是 {result} 給看得懂的錯誤,不是 TypeError", () => {
  assert.equal(unwrapPost({ ok: true, status: 200 }, { result: 5 }), 5);
  assert.equal(unwrapPost({ ok: true, status: 200 }, { result: null }), null, "result 是 null 是合法的結果");
  for (const body of [null, undefined, "<html>bad gateway</html>", [], {}, 3]) {
    let err: unknown;
    try { unwrapPost({ ok: true, status: 200 }, body); } catch (e) { err = e; }
    assert.ok(err instanceof ApiError, `body=${JSON.stringify(body)} 要丟 ApiError`);
    assert.ok(/格式不對/.test((err as ApiError).message));
  }
  let e2: unknown;
  try { unwrapPost({ ok: false, status: 409 }, { error: "衝突", currentVersion: 3 }); } catch (e) { e2 = e; }
  assert.ok(e2 instanceof ApiError && e2.status === 409 && e2.data?.currentVersion === 3);
  let e3: unknown;
  try { unwrapPost({ ok: false, status: 502 }, null); } catch (e) { e3 = e; }
  assert.ok(e3 instanceof ApiError && /502/.test(e3.message));
});

test("專案代號驗證", () => {
  assert.equal(projectKeyProblem(""), "");
  assert.equal(projectKeyProblem("app"), "");
  assert.equal(cleanProjectKey(" app "), "APP");
  assert.equal(projectKeyProblem("AB12"), "");
  assert.ok(/第一個字/.test(projectKeyProblem("1AB")));
  assert.ok(/2 到 10/.test(projectKeyProblem("A")));
  assert.ok(/2 到 10/.test(projectKeyProblem("ABCDEFGHIJK")));
  assert.ok(/英文字母與數字/.test(projectKeyProblem("A_B")));
  for (const k of ["ISS", "prj", "T", "DOC", "CH", "INC"]) assert.ok(projectKeyProblem(k) !== "", k);
  assert.ok(projectHasIssues("PRJ-1", [mk({ id: "a", project: "PRJ-1" })]));
  assert.ok(!projectHasIssues("PRJ-2", [mk({ id: "a", project: "PRJ-1" })]));
});

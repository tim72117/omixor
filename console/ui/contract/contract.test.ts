// 跨語言契約(執行期):讀 core/model/testdata/contract/*.json(core API 實際回傳的形狀),
// 檢查前端用到的關鍵欄位都在、列舉值前端認得(標籤表查得到),再餵給前端實際的轉換函式。
// 執行:npm test(node --experimental-strip-types --test;不加測試框架依賴)。型別的檢查在 types.check.ts(tsc)。
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  agentAccess, arr, boardDropStatus, DOC_ACCESS_LABEL, INC_STATUS_LABEL, leaseLabel, PRIORITY_LABEL, ROUTINE_KIND_LABEL, tabTitle, TASK_LABEL, taskOpen,
  type Agent, type AgentTask, type CabinetDoc, type CabinetSection, type Channel, type ChannelMessage, type ChannelPage,
  type Incident, type OrgChain,
} from "../src/components/models.ts";
import type { Issue } from "../src/sections/development/DevIssuesTab";

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
  assert.deepEqual(list.map((i) => i.id), ["ISS-12", "ISS-3"]);
  const i = load<Issue>("issue.json");
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

test("組織鏈", () => {
  const ch = load<OrgChain>("org_chain.json");
  assert.deepEqual(ch.edges.map((e) => [e.from, e.to, e.label]), [["user:tim@example.com", "agent:pm", "目標"], ["agent:pm", "agent:eng", ""]]);
  isTime(ch.updatedAt, "chain.updatedAt");
});

test("看板拖放:欄的 dropStatus 和 Issue 現在的 status 不同才能放", () => {
  assert.equal(boardDropStatus("closed", "open"), "closed", "拖到已完成 = 關閉");
  assert.equal(boardDropStatus("open", "closed"), "open", "從已完成拖出 = 重新打開");
  assert.equal(boardDropStatus("open", "open"), null, "待處理 ↔ 進行中、原欄:不能放");
  assert.equal(boardDropStatus("closed", "closed"), null);
  assert.equal(boardDropStatus(null, "open"), null);
  assert.equal(boardDropStatus(undefined, "closed"), null);
});

// 開發 › Issue:存在本機的真實 Issue(人或 AI 透過 MCP 建立)。Swift 的 DevIssuesTab.swift。
// 建立、修改、留言、關閉走 MCP 工具(ops_issue_create / ops_issue_update),刪除是畫面專用的 issue_delete。
import { useEffect, useState } from "react";
import { action, tool } from "../../api";
import { color, EmptyState, Modal, Pill } from "../../components/ui";
import { Banner, MarkdownText, MenuButton, monthDayTime, Seg, SIcon, useAct, useConfirm, useList, useOps, type Agent } from "../../components/opsKit";
import { relative } from "../../fmt";
import { useDev } from "./devShared";

export type IssueComment = { id: string; author: string; text: string; at: string };
export type OpsIssue = {
  id: string; title: string; body: string; status: "open" | "closed"; priority: Priority; labels: string[];
  source: string; link: string; createdBy: string; createdAt: string; updatedAt: string; closedAt?: string | null; comments: IssueComment[];
};
type Priority = "urgent" | "high" | "normal" | "low";
export const PRIORITY: Record<Priority, { label: string; color: string; rank: number }> = {
  urgent: { label: "緊急", color: "red", rank: 0 }, high: { label: "高", color: "orange", rank: 1 },
  normal: { label: "一般", color: "blue", rank: 2 }, low: { label: "低", color: "gray", rank: 3 },
};
const PRIORITIES: Priority[] = ["urgent", "high", "normal", "low"];
const prRank = (p: Priority) => PRIORITY[p]?.rank ?? 2;
const isAIAuthor = (a: string) => a === "mcp" || a.startsWith("agent:");
const splitLabels = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

type Filter = "open" | "closed" | "all";

export function DevIssuesTab() {
  const dev = useDev();
  const [issues, err, ready] = useList<OpsIssue>("issues");
  const [filter, setFilter] = useState<Filter>("open");
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const q = query.trim().toLowerCase();
  const list = issues
    .filter((i) => (filter === "all" || i.status === filter) && (!q || i.title.toLowerCase().includes(q) || i.id.toLowerCase().includes(q) || (i.labels ?? []).some((l) => l.toLowerCase().includes(q)) || (i.body ?? "").toLowerCase().includes(q)))
    .sort((a, b) => (a.status === "open" ? 0 : 1) - (b.status === "open" ? 0 : 1) || prRank(a.priority) - prRank(b.priority) || Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
  const count = (f: Filter) => {
    const n = f === "all" ? issues.length : issues.filter((i) => i.status === f).length;
    return n > 0 ? ` ${n}` : "";
  };
  useEffect(() => {
    if (ready && !dev.selectedIssue && list[0]) dev.setSelectedIssue(list[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready]);
  const sel = issues.find((i) => i.id === dev.selectedIssue);
  return (
    <div className="ops-fill">
      <div className="col" style={{ width: 320, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
        <div className="col gap8" style={{ padding: 10 }}>
          <Seg value={filter} onChange={setFilter} options={(["open", "closed", "all"] as Filter[]).map((f) => ({ value: f, label: (f === "open" ? "未解決" : f === "closed" ? "已關閉" : "全部") + count(f) }))} />
          <div className="row gap6">
            <input className="input grow" placeholder="搜尋標題、標籤、內文" value={query} onChange={(e) => setQuery(e.target.value)} />
            <button className="btn" title="新增 Issue" onClick={() => setCreating(true)}><SIcon name="plus" /></button>
          </div>
        </div>
        <div className="divider" />
        {err && issues.length === 0 ? <EmptyState title="讀不到 Issue" icon="exclamationmark.triangle" message={err} />
          : list.length === 0 ? (
            <EmptyState title={issues.length === 0 ? "還沒有 Issue" : "沒有符合的 Issue"} icon="exclamationmark.bubble"
              message={issues.length === 0 ? "按 + 新增,或讓 AI 透過 MCP 的 ops_issue_create 存進來。" : undefined} />
          ) : (
            <div className="ops-list">
              {list.map((i) => <IssueRow key={i.id} issue={i} on={i.id === dev.selectedIssue} onClick={() => dev.setSelectedIssue(i.id)} />)}
            </div>
          )}
      </div>
      <div className="ops-scroll">
        {sel ? <IssueDetail key={sel.id} issue={sel} /> : (
          <EmptyState title="選一個 Issue" icon="exclamationmark.bubble" message="Issue 存在這台 Mac 上(issues.json),重開 app 也還在。AI 可以用 MCP 建立、留言與關閉。" />
        )}
      </div>
      <Modal open={creating} onClose={() => setCreating(false)} width={560}>
        <NewIssue onDone={(id) => { setCreating(false); if (id) { dev.setSelectedIssue(id); setFilter("open"); } }} />
      </Modal>
    </div>
  );
}

function useAuthor() {
  const o = useOps();
  return {
    name: (a: string) => {
      if (a === "mcp") return "MCP 用戶端";
      if (a.startsWith("agent:")) return o.name({ kind: "ai", id: a.slice(6) });
      return o.name({ kind: "person", id: a });
    },
    agent: (a: string): Agent | undefined => (a.startsWith("agent:") ? o.agents.find((x) => x.id === a.slice(6)) : undefined),
  };
}

function AuthorIcon({ author, size = 18 }: { author: string; size?: number }) {
  const { agent } = useAuthor();
  const ag = agent(author);
  if (ag) return <span className="ops-avatar" style={{ width: size, height: size, background: color(ag.color ?? "blue") }}><SIcon name={ag.symbol ?? "person.crop.circle"} size={size * 0.5} color="#fff" /></span>;
  if (author === "mcp") return <SIcon name="sparkles" size={size * 0.8} color="purple" />;
  return <SIcon name="person.crop.circle" size={size * 0.85} className="muted" />;
}

function IssueRow({ issue, on, onClick }: { issue: OpsIssue; on: boolean; onClick: () => void }) {
  const { name } = useAuthor();
  const open = issue.status === "open";
  const p = PRIORITY[issue.priority] ?? PRIORITY.normal;
  return (
    <div className={"ops-list-row col gap4" + (on ? " on" : "")} onClick={onClick}>
      <div className="row gap6" style={{ alignItems: "flex-start" }}>
        <SIcon name={open ? "circle.dotted" : "checkmark.circle.fill"} color={open ? p.color : "secondary"} style={{ marginTop: 2 }} />
        <span style={{ color: open ? undefined : "var(--fg2)", display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{issue.title}</span>
      </div>
      <div className="row gap6">
        <span className="caption mono muted">{issue.id}</span>
        {isAIAuthor(issue.createdBy) && <span title={name(issue.createdBy)}><AuthorIcon author={issue.createdBy} size={14} /></span>}
        {prRank(issue.priority) <= 1 && <Pill text={p.label} color={p.color} />}
        {(issue.labels ?? []).slice(0, 3).map((l) => <Pill key={l} text={l} />)}
        <span className="grow" />
        {issue.comments?.length > 0 && <span className="caption muted row gap4"><SIcon name="bubble.left" size={11} />{issue.comments.length}</span>}
        <span className="caption muted" style={{ flex: "none" }}>{relative(issue.updatedAt)}</span>
      </div>
    </div>
  );
}

function IssueDetail({ issue }: { issue: OpsIssue }) {
  const dev = useDev();
  const { name } = useAuthor();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [labels, setLabels] = useState("");
  const [comment, setComment] = useState("");
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const upd = (args: Record<string, unknown>) => run(() => tool("ops_issue_update", { id: issue.id, ...args }));
  const open = issue.status === "open";
  const p = PRIORITY[issue.priority] ?? PRIORITY.normal;
  let linkHost: string | undefined;
  try { const u = new URL(issue.link); if (u.protocol.startsWith("http")) linkHost = u.host; } catch { /* 不是網址 */ }
  return (
    <div className="col" style={{ padding: 20, gap: 16, maxWidth: 820 }}>
      <div className="col gap8">
        <div className="row gap8" style={{ alignItems: "baseline" }}>
          <span className="h2" style={{ userSelect: "text" }}>{issue.title}</span>
          <span className="mono muted" style={{ fontSize: 15 }}>{issue.id}</span>
          <span className="grow" />
          <MenuButton className="btn" label={<SIcon name="ellipsis.circle" />} align="right" items={[
            { header: "優先度" },
            ...PRIORITIES.map((x) => ({ label: PRIORITY[x].label, checked: x === issue.priority, onClick: () => upd({ priority: x }) })),
            "sep",
            { label: "編輯…", onClick: () => { setTitle(issue.title); setBody(issue.body ?? ""); setLabels((issue.labels ?? []).join(", ")); setEditing(true); } },
            { label: "刪除…", danger: true, onClick: () => ask({ title: `刪除 ${issue.id}?`, buttons: [{ label: "刪除(無法復原)", danger: true, onClick: async () => { const r = await run(() => action("issue_delete", { id: issue.id })); if (r !== undefined) dev.setSelectedIssue(undefined); } }] }) },
          ]} />
          <button className="btn" onClick={() => upd({ status: open ? "closed" : "open" })}>{open ? "關閉" : "重新打開"}</button>
        </div>
        <div className="row gap6 wrap">
          <Pill text={open ? "未解決" : "已關閉"} color={open ? "green" : "secondary"} icon={open ? "circle.dotted" : "checkmark.circle"} />
          <Pill text={"優先度:" + p.label} color={p.color} />
          {(issue.labels ?? []).map((l) => <Pill key={l} text={l} icon="tag" />)}
        </div>
        <div className="row gap12 caption muted wrap">
          <span className="row gap4"><AuthorIcon author={issue.createdBy} size={16} />{name(issue.createdBy)} 建立於 {monthDayTime(issue.createdAt)}</span>
          {Date.parse(issue.updatedAt) - Date.parse(issue.createdAt) > 1000 && <span>更新 {relative(issue.updatedAt)}</span>}
          {issue.source && <span className="row gap4"><SIcon name="scope" size={12} />{issue.source}</span>}
          {linkHost && <a className="row gap4" href={issue.link} target="_blank" rel="noreferrer"><SIcon name="link" size={12} />{linkHost}</a>}
        </div>
      </div>
      <div className="divider" />
      {editing ? (
        <div className="col gap8">
          <input className="input" style={{ fontSize: 15 }} placeholder="標題" value={title} onChange={(e) => setTitle(e.target.value)} />
          <input className="input" placeholder="標籤(逗號分隔)" value={labels} onChange={(e) => setLabels(e.target.value)} />
          <textarea className="textarea" style={{ minHeight: 220 }} value={body} onChange={(e) => setBody(e.target.value)} />
          <div className="row gap8">
            <span className="caption muted">內文支援 Markdown</span>
            <span className="grow" />
            <button className="btn" onClick={() => setEditing(false)}>取消</button>
            <button className="btn primary" disabled={!title.trim()} onClick={async () => {
              const r = await upd({ title: title.trim(), body, labels: splitLabels(labels) });
              if (r !== undefined) setEditing(false);
            }}>儲存</button>
          </div>
        </div>
      ) : issue.body ? <MarkdownText source={issue.body} /> : <div className="muted">沒有內文</div>}
      {(issue.comments?.length > 0 || !editing) && (
        <div className="col" style={{ gap: 10 }}>
          <div className="card-title">留言</div>
          {(issue.comments ?? []).map((c) => (
            <div key={c.id} className="col gap4" style={{ padding: 10, borderRadius: 8, background: "color-mix(in srgb, var(--fg) 5%, transparent)" }}>
              <div className="row gap6"><AuthorIcon author={c.author} /><span className="caption" style={{ fontWeight: 600 }}>{name(c.author)}</span><span className="caption muted">{relative(c.at)}</span></div>
              <MarkdownText source={c.text} />
            </div>
          ))}
          <div className="row gap8" style={{ alignItems: "flex-end" }}>
            <textarea className="textarea" style={{ minHeight: 34, height: 34, fontFamily: "inherit" }} placeholder="寫留言…" value={comment} onChange={(e) => setComment(e.target.value)} />
            <button className="btn" style={{ whiteSpace: "nowrap" }} disabled={!comment.trim()} onClick={async () => { const r = await upd({ comment: comment.trim() }); if (r !== undefined) setComment(""); }}>送出</button>
          </div>
        </div>
      )}
      {dlg}{toastEl}
    </div>
  );
}

function NewIssue({ onDone }: { onDone: (id?: string) => void }) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [labels, setLabels] = useState("");
  const [priority, setPriority] = useState<Priority>("normal");
  const [error, setError] = useState<string>();
  const create = async () => {
    try {
      const r: any = await tool("ops_issue_create", { title: title.trim(), body, priority, labels: splitLabels(labels) });
      const id = typeof r === "string" ? (() => { try { return JSON.parse(r)?.id; } catch { return undefined; } })() : r?.id;
      onDone(id);
    } catch (e: any) { setError(String(e?.message ?? e)); }
  };
  return (
    <div className="col gap12">
      <div className="modal-title">新增 Issue</div>
      <input className="input" placeholder="標題" value={title} autoFocus onChange={(e) => setTitle(e.target.value)} />
      <div className="row gap8">
        <label className="row gap6">優先度
          <select className="select" value={priority} onChange={(e) => setPriority(e.target.value as Priority)}>{PRIORITIES.map((p) => <option key={p} value={p}>{PRIORITY[p].label}</option>)}</select>
        </label>
        <input className="input grow" placeholder="標籤(逗號分隔)" value={labels} onChange={(e) => setLabels(e.target.value)} />
      </div>
      <textarea className="textarea" style={{ minHeight: 180 }} value={body} onChange={(e) => setBody(e.target.value)} />
      {error && <Banner>{error}</Banner>}
      <div className="row gap8">
        <span className="caption muted">內文支援 Markdown</span>
        <span className="grow" />
        <button className="btn" onClick={() => onDone()}>取消</button>
        <button className="btn primary" disabled={!title.trim()} onClick={create}>建立</button>
      </div>
    </div>
  );
}

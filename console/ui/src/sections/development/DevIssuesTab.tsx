// 開發 › Issue:存在本機的真實 Issue(人或 AI 透過 MCP 建立)。Swift 的 DevIssuesTab.swift。
// 建立、修改、留言、關閉走 MCP 工具(issue_create / issue_update),刪除是畫面專用的 issue_delete。
import { useEffect, useState } from "react";
import { action, tool, useActionQuery, useDebounced, useView } from "../../api";
import { color, EmptyState, Modal, Pill } from "../../components/ui";
import { Banner, MarkdownText, MenuButton, monthDayTime, Seg, SIcon, useAct, useConfirm, useList, useOmixor, type Agent } from "../../components/omixorKit";
import { relative } from "../../fmt";
import { repoKey, RepoFilterSelect, usedRepos, useDev } from "./devShared";
import { JiraImport } from "./JiraImport";
import { MdTextarea } from "../../components/MdTextarea";
import { go } from "../../components/bits";

export type IssueComment = { id: string; author: string; text: string; at: string };
export type Issue = {
  id: string; title: string; body: string; status: "open" | "closed"; priority: Priority; labels: string[];
  source: string; link: string; assignee?: string; repo?: string; createdBy: string; createdAt: string; updatedAt: string; closedAt?: string | null; comments: IssueComment[];
};
type Priority = "urgent" | "high" | "normal" | "low";
/** 優先度的顯示文字與顏色(排序規則在核心,畫面不排) */
export const PRIORITY: Record<Priority, { label: string; color: string }> = {
  urgent: { label: "緊急", color: "red" }, high: { label: "高", color: "orange" },
  normal: { label: "一般", color: "blue" }, low: { label: "低", color: "gray" },
};
const PRIORITIES: Priority[] = ["urgent", "high", "normal", "low"];
/** 緊急、高在列表與卡片上顯示標記;一般、低只用顏色 */
export const showPriorityPill = (p: Priority) => p === "urgent" || p === "high";
const isAIAuthor = (a: string) => a === "mcp" || a.startsWith("agent:");
const splitLabels = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

type Filter = "open" | "closed" | "all";

// 所屬倉庫(repo):owner/name(GitHub)或 group/sub/project(GitLab),可加 github:、gitlab: 前綴;"" = 未標記。
// 格式同核心 model.CleanRepo(只允許英數與 - _ . / :,最長 200);存的時候核心會再檢查一次
const REPO_RE = /^[A-Za-z0-9._/:-]{0,200}$/;
/** issue_query 的回應(後端用核心的 model.QueryIssues 篩選、排序;counts、total 是全部 Issue 的) */
type IssueQueryResult = { issues: Issue[]; total: number; counts: Record<"open" | "closed" | "all", number> };
type IssueRepos = { github: string[]; gitlab: string[]; used: string[] };

/** 候選倉庫:GitHub 追蹤的 repos、GitLab 追蹤的專案(加 gitlab: 前綴)、其他 Issue 用過的(去重,不分大小寫與前綴) */
function useRepoOptions(): { label: string; options: string[] }[] {
  const [v] = useView<IssueRepos>("issue_repos", ["github", "gitlab", "issues"]);
  const seen = new Set<string>();
  const take = (l: string[] | undefined) => (l ?? []).filter((r) => {
    if (!r || seen.has(repoKey(r))) return false;
    seen.add(repoKey(r));
    return true;
  });
  return [
    { label: "GitHub", options: take(v?.github) },
    { label: "GitLab", options: take((v?.gitlab ?? []).map((p) => "gitlab:" + p)) },
    { label: "其他 Issue 用過的", options: take(v?.used) },
  ].filter((g) => g.options.length > 0);
}

/** 設定所屬倉庫:下拉選已設定的倉庫、「不標記」,或「其他…」自己輸入 */
function RepoPicker({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const groups = useRepoOptions();
  const known = groups.some((g) => g.options.includes(value));
  const [custom, setCustom] = useState(false);
  const other = custom || (value !== "" && !known);
  const bad = !REPO_RE.test(value.trim());
  return (
    <div className="row gap6" style={{ alignItems: "center" }}>
      <SIcon name="chevron.left.forwardslash.chevron.right" className="muted" />
      <select className="select" title="所屬倉庫" value={other ? "__other" : value} onChange={(e) => {
        if (e.target.value === "__other") { setCustom(true); return; }
        setCustom(false); onChange(e.target.value);
      }}>
        <option value="">不標記倉庫</option>
        {groups.map((g) => <optgroup key={g.label} label={g.label}>{g.options.map((r) => <option key={r} value={r}>{r}</option>)}</optgroup>)}
        <option value="__other">其他…</option>
      </select>
      {other && <input className="input grow" placeholder="owner/name 或 gitlab:group/project" value={value} onChange={(e) => onChange(e.target.value)} />}
      {bad && <span className="caption" style={{ color: "var(--red)" }}>只能用英數與 - _ . / :(最長 200)</span>}
    </div>
  );
}

export function DevIssuesTab() {
  const dev = useDev();
  const [issues, err] = useList<Issue>("issues"); // 全部(詳細資料、倉庫選單用);列表本身照 issue_query 的結果
  const [filter, setFilter] = useState<Filter>("open");
  const [query, setQuery] = useState("");
  const [repo, setRepo] = useState(""); // "" = 全部倉庫;NO_REPO = 未標記;其他 = 只列這個倉庫(比對規則在核心的 model.RepoMatch)
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);
  const q = useDebounced(query, 250);
  // 篩選、搜尋、排序在後端(核心的 model.QueryIssues):未解決的在前 → 優先度 → 最近更新 → 編號
  const [res, qErr] = useActionQuery<IssueQueryResult>("issue_query", { q, status: filter, repo, sort: "status" }, ["issues"]);
  const list = res?.issues ?? [];
  const total = res?.total ?? issues.length;
  const repos = usedRepos(issues);
  const count = (f: Filter) => {
    const n = res?.counts?.[f] ?? 0;
    return n > 0 ? ` ${n}` : "";
  };
  useEffect(() => {
    if (res && !dev.selectedIssue && list[0]) dev.setSelectedIssue(list[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [res !== undefined]);
  const sel = issues.find((i) => i.id === dev.selectedIssue);
  return (
    <div className="ox-fill">
      <div className="col" style={{ width: 320, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
        <div className="col gap8" style={{ padding: 10 }}>
          <Seg value={filter} onChange={setFilter} options={(["open", "closed", "all"] as Filter[]).map((f) => ({ value: f, label: (f === "open" ? "未解決" : f === "closed" ? "已關閉" : "全部") + count(f) }))} />
          <div className="row gap6">
            <input className="input grow" placeholder="搜尋編號、標題、內文、標籤、留言" value={query} onChange={(e) => setQuery(e.target.value)} />
            <button className="btn" title="新增 Issue" onClick={() => setCreating(true)}><SIcon name="plus" /></button>
            <button className="btn" title="從 Jira 匯入" onClick={() => setImporting(true)}><SIcon name="square.and.arrow.down" /></button>
          </div>
          <RepoFilterSelect value={repo} onChange={setRepo} repos={repos} />
        </div>
        <div className="divider" />
        {(err || qErr) && !res ? <EmptyState title="讀不到 Issue" icon="exclamationmark.triangle" message={qErr ?? err} />
          : !res ? null : list.length === 0 ? (
            <EmptyState title={total === 0 ? "還沒有 Issue" : "沒有符合的 Issue"} icon="exclamationmark.bubble"
              message={total === 0 ? "按 + 新增、從 Jira 匯入,或讓 AI 透過 MCP 的 issue_create 存進來。" : undefined} />
          ) : (
            <div className="ox-list">
              {list.map((i) => <IssueRow key={i.id} issue={i} on={i.id === dev.selectedIssue} onClick={() => dev.setSelectedIssue(i.id)} />)}
            </div>
          )}
      </div>
      <div className="ox-scroll">
        {sel ? <IssueDetail key={sel.id} issue={sel} /> : (
          <EmptyState title="選一個 Issue" icon="exclamationmark.bubble" message="Issue 存在這台 Mac 上(issues.json),重開 app 也還在。AI 可以用 MCP 建立、留言與關閉。" />
        )}
      </div>
      <Modal open={creating} onClose={() => setCreating(false)} width={560}>
        <NewIssue onDone={(id) => { setCreating(false); if (id) { dev.setSelectedIssue(id); setFilter("open"); } }} />
      </Modal>
      <Modal open={importing} onClose={() => setImporting(false)} width={560}>
        <JiraImport onDone={() => { setImporting(false); setFilter("all"); }} />
      </Modal>
    </div>
  );
}

function useAuthor() {
  const o = useOmixor();
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
  if (ag) return <span className="ox-avatar" style={{ width: size, height: size, background: color(ag.color ?? "blue") }}><SIcon name={ag.symbol ?? "person.crop.circle"} size={size * 0.5} color="#fff" /></span>;
  if (author === "mcp") return <SIcon name="sparkles" size={size * 0.8} color="purple" />;
  return <SIcon name="person.crop.circle" size={size * 0.85} className="muted" />;
}

function IssueRow({ issue, on, onClick }: { issue: Issue; on: boolean; onClick: () => void }) {
  const { name } = useAuthor();
  const open = issue.status === "open";
  const p = PRIORITY[issue.priority] ?? PRIORITY.normal;
  return (
    <div className={"ox-list-row col gap4" + (on ? " on" : "")} onClick={onClick}>
      <div className="row gap6" style={{ alignItems: "flex-start" }}>
        <SIcon name={open ? "circle.dotted" : "checkmark.circle.fill"} color={open ? p.color : "secondary"} style={{ marginTop: 2 }} />
        <span style={{ color: open ? undefined : "var(--fg2)", display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{issue.title}</span>
      </div>
      <div className="row gap6">
        <span className="caption mono muted">{issue.id}</span>
        {isAIAuthor(issue.createdBy) && <span title={name(issue.createdBy)}><AuthorIcon author={issue.createdBy} size={14} /></span>}
        {showPriorityPill(issue.priority) && <Pill text={p.label} color={p.color} />}
        {issue.repo && <span className="caption mono muted" title={"倉庫:" + issue.repo} style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 140 }}>{issue.repo}</span>}
        {(issue.labels ?? []).slice(0, 3).map((l) => <Pill key={l} text={l} />)}
        <span className="grow" />
        {issue.comments?.length > 0 && <span className="caption muted row gap4"><SIcon name="bubble.left" size={11} />{issue.comments.length}</span>}
        <span className="caption muted" style={{ flex: "none" }}>{relative(issue.updatedAt)}</span>
      </div>
    </div>
  );
}

function IssueDetail({ issue }: { issue: Issue }) {
  const dev = useDev();
  const { name } = useAuthor();
  const kit = useOmixor();
  const assignee = issue.assignee ? kit.agents.find((x) => x.id === issue.assignee) : undefined;
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [labels, setLabels] = useState("");
  const [repo, setRepo] = useState("");
  const [comment, setComment] = useState("");
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  const upd = (args: Record<string, unknown>) => run(() => tool("issue_update", { id: issue.id, ...args }));
  const open = issue.status === "open";
  const p = PRIORITY[issue.priority] ?? PRIORITY.normal;
  let linkHost: string | undefined;
  try { const u = new URL(issue.link); if (u.protocol.startsWith("http")) linkHost = u.host; } catch { /* 不是網址 */ }
  return (
    <div className="col" style={{ padding: 20, gap: 16, maxWidth: 820 }}>
      <div className="col gap8">
        <div className="row gap8" style={{ alignItems: "flex-start", flexWrap: "wrap" }}>
          {/* 標題至少保留 280px、可換行;按鈕群不換行、放不下時整組換到標題下一行,避免被擠成直排或把標題擠窄 */}
          <div className="row gap8" style={{ alignItems: "baseline", minWidth: 0, flexWrap: "wrap", flex: "1 1 280px" }}>
            <span className="h2" style={{ userSelect: "text", minWidth: 0, overflowWrap: "anywhere" }}>{issue.title}</span>
            <span className="mono muted" style={{ fontSize: 15, whiteSpace: "nowrap" }}>{issue.id}</span>
          </div>
          <div className="row gap8" style={{ flex: "0 0 auto", marginLeft: "auto", alignItems: "center", whiteSpace: "nowrap" }}>
          <MenuButton className="btn" label={<SIcon name="ellipsis.circle" />} align="right" items={[
            { header: "優先度" },
            ...PRIORITIES.map((x) => ({ label: PRIORITY[x].label, checked: x === issue.priority, onClick: () => upd({ priority: x }) })),
            "sep",
            { header: "指派給角色(會自動排任務)" },
            { label: "未指派", checked: !issue.assignee, onClick: () => upd({ assignee: "" }) },
            ...kit.agents.filter((x) => x.active !== false || x.id === issue.assignee).map((x) => ({ label: x.name, checked: x.id === issue.assignee, onClick: () => upd({ assignee: x.id }) })),
            "sep",
            { label: "編輯…", onClick: () => { setTitle(issue.title); setBody(issue.body ?? ""); setLabels((issue.labels ?? []).join(", ")); setRepo(issue.repo ?? ""); setEditing(true); } },
            { label: "刪除…", danger: true, onClick: () => ask({ title: `刪除 ${issue.id}?`, buttons: [{ label: "刪除(無法復原)", danger: true, onClick: async () => { const r = await run(() => action("issue_delete", { id: issue.id })); if (r !== undefined) dev.setSelectedIssue(undefined); } }] }) },
          ]} />
          <button className="btn" title="這個 Issue 的對話頻道(已有就跳過去,沒有就建立)" onClick={async () => {
            // 已有綁這個 Issue 的頻道:核心直接回傳那個;沒有就建立(名稱用 Issue 標題,成員含自己與指派的角色)
            const c = await run(() => action<{ id: string }>("channel_create", { name: issue.title.slice(0, 80), issueId: issue.id, members: issue.assignee ? [`agent:${issue.assignee}`] : [] }));
            if (c) go("channels", c.id);
          }}><SIcon name="bubble.left.and.bubble.right" /> 開對話頻道</button>
          <button className="btn" onClick={() => upd({ status: open ? "closed" : "open" })}>{open ? "關閉" : "重新打開"}</button>
          </div>
        </div>
        <div className="row gap6 wrap">
          <Pill text={open ? "未解決" : "已關閉"} color={open ? "green" : "secondary"} icon={open ? "circle.dotted" : "checkmark.circle"} />
          <Pill text={"優先度:" + p.label} color={p.color} />
          {issue.assignee && <Pill text={"指派:" + (assignee?.name ?? issue.assignee)} color="purple" icon="person.crop.circle" />}
          {issue.repo && <Pill text={"倉庫:" + issue.repo} color="teal" icon="chevron.left.forwardslash.chevron.right" />}
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
          <RepoPicker value={repo} onChange={setRepo} />
          <MdTextarea style={{ minHeight: 220 }} value={body} onChange={setBody} />
          <div className="row gap8">
            <span className="caption muted">內文支援 Markdown</span>
            <span className="grow" />
            <button className="btn" onClick={() => setEditing(false)}>取消</button>
            <button className="btn primary" disabled={!title.trim() || !REPO_RE.test(repo.trim())} onClick={async () => {
              const r = await upd({ title: title.trim(), body, labels: splitLabels(labels), ...(repo.trim() !== (issue.repo ?? "") ? { repo: repo.trim() } : {}) });
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
            <MdTextarea style={{ minHeight: 34, fontFamily: "inherit" }} placeholder="寫留言…" value={comment} onChange={setComment} />
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
  const [repo, setRepo] = useState("");
  const [error, setError] = useState<string>();
  const create = async () => {
    try {
      const r: any = await tool("issue_create", { title: title.trim(), body, priority, labels: splitLabels(labels), ...(repo.trim() ? { repo: repo.trim() } : {}) });
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
      <RepoPicker value={repo} onChange={setRepo} />
      <MdTextarea style={{ minHeight: 180 }} value={body} onChange={setBody} />
      {error && <Banner>{error}</Banner>}
      <div className="row gap8">
        <span className="caption muted">內文支援 Markdown</span>
        <span className="grow" />
        <button className="btn" onClick={() => onDone()}>取消</button>
        <button className="btn primary" disabled={!title.trim() || !REPO_RE.test(repo.trim())} onClick={create}>建立</button>
      </div>
    </div>
  );
}

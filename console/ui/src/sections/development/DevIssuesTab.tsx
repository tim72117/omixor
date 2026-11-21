// 開發 › Issue:存在本機的真實 Issue(人或 AI 透過 MCP 建立)。Swift 的 DevIssuesTab.swift。
// 建立、修改、留言、關閉走 MCP 工具(issue_create / issue_update),刪除是畫面專用的 issue_delete。
import { useEffect, useRef, useState } from "react";
import { action, useActionQuery, useDebounced } from "../../api";
import { color, EmptyState, Modal, Pill } from "../../components/ui";
import { MarkdownText, MenuButton, monthDayTime, Seg, SIcon, useAct, useConfirm, useList, useOmixor, type Agent } from "../../components/omixorKit";
import { relative } from "../../fmt";
import { RepoFilterSelect, usedRepos, useDev } from "./devShared";
import { JiraImport } from "./JiraImport";
import { IssueEditForm, NewIssue, projectName } from "./IssueForm";
import { IssueTrash } from "./IssueTrash";
import { resolveIssueRef, updateIssue, useIsOrgAdmin, useProjects } from "./issueApi";
import {
  findIssue, issueKey, PRIORITIES, PRIORITY, PROJECT_ALL, PROJECT_ORG, showPriorityPill,
  type Counts, type Issue,
} from "./issueModel";
import { go } from "../../components/bits";
import { MdTextarea } from "../../components/MdTextarea";

export { PRIORITY, showPriorityPill } from "./issueModel";
export type { Issue, IssueComment } from "./issueModel";
const isAIAuthor = (a: string) => a === "mcp" || a.startsWith("agent:");
type Filter = "open" | "closed" | "all";

/** issue_query 的回應(後端用核心的 model.QueryIssues 篩選、排序;counts、total 是全部 Issue 的) */
type IssueQueryResult = { issues: Issue[]; total: number; counts: Record<"open" | "closed" | "all", number> };

export function DevIssuesTab() {
  const dev = useDev();
  const [issues, err, issuesReady] = useList<Issue>("issues"); // 全部(詳細資料、倉庫選單用);列表本身照 issue_query 的結果
  const [filter, setFilter] = useState<Filter>("open");
  const [query, setQuery] = useState("");
  const [repo, setRepo] = useState(""); // "" = 全部倉庫;NO_REPO = 未標記;其他 = 只列這個倉庫(比對規則在核心的 model.RepoMatch)
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);
  const [trashOpen, setTrashOpen] = useState(false);
  const [project, setProject] = useState(PROJECT_ALL); // "" = 全部;"-" = 只看組織層級;PRJ-n = 只看那個專案(同後端 project 查詢參數)
  const projects = useProjects();
  const isAdmin = useIsOrgAdmin();
  const q = useDebounced(query, 250);
  // 篩選、搜尋、排序在後端(核心的 model.QueryIssues):未解決的在前 → 優先度 → 最近更新 → 編號
  const [res, qErr] = useActionQuery<IssueQueryResult>("issue_query", { q, status: filter, repo, project, sort: "status" }, ["issues"]);
  // 列表、各狀態數量(counts)、總數(total)都以後端回傳的為準(已依 project 計算),畫面不再自己篩、自己算
  const list = res?.issues ?? [];
  const counts: Counts = res?.counts ?? { all: 0, open: 0, closed: 0 };
  const total = res?.total ?? 0;
  const repos = usedRepos(issues);
  const count = (f: Filter) => {
    const n = counts[f] ?? 0;
    return n > 0 ? ` ${n}` : "";
  };
  useEffect(() => {
    if (res && !dev.selectedIssue && list[0]) dev.setSelectedIssue(list[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [res !== undefined]);
  const sel = findIssue(issues, dev.selectedIssue);
  // 選的編號清單裡找不到(舊的 key、剛搬專案、垃圾桶、已永久刪除):請後端解析成目前的 id;解析不了(或在垃圾桶)就取消選取
  const resolved = useRef(new Set<string>());
  useEffect(() => {
    const ref = dev.selectedIssue;
    if (!ref || !issuesReady || sel || resolved.current.has(ref)) return;
    resolved.current.add(ref);
    resolveIssueRef(ref)
      .then((r) => dev.setSelectedIssue(r.deleted ? undefined : r.id))
      .catch(() => dev.setSelectedIssue(undefined));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dev.selectedIssue, issuesReady, sel?.id]);
  return (
    <div className="ox-fill">
      <div className="col" style={{ width: 320, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
        <div className="col gap8" style={{ padding: 10 }}>
          <Seg value={filter} onChange={setFilter} options={(["open", "closed", "all"] as Filter[]).map((f) => ({ value: f, label: (f === "open" ? "未解決" : f === "closed" ? "已關閉" : "全部") + count(f) }))} />
          <div className="row gap6">
            <input className="input grow" placeholder="搜尋編號、標題、內文、標籤、留言" value={query} onChange={(e) => setQuery(e.target.value)} />
            <button className="btn" title="新增 Issue" onClick={() => setCreating(true)}><SIcon name="plus" /></button>
            <button className="btn" title="從 Jira 匯入" onClick={() => setImporting(true)}><SIcon name="square.and.arrow.down" /></button>
            {isAdmin && <button className="btn" title="垃圾桶(已刪除的 Issue,只有組織管理員看得到)" onClick={() => setTrashOpen(true)}><SIcon name="trash" /></button>}
          </div>
          <select className="select" title="依專案篩選" value={project} onChange={(e) => setProject(e.target.value)}>
            <option value={PROJECT_ALL}>全部專案</option>
            <option value={PROJECT_ORG}>組織(未分專案)</option>
            {projects.length > 0 && <option disabled>──────</option>}
            {projects.map((p) => <option key={p.id} value={p.id}>{projectName(p)}</option>)}
            {project !== PROJECT_ALL && project !== PROJECT_ORG && !projects.some((p) => p.id === project) && <option value={project}>{project}</option>}
          </select>
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
        <NewIssue defaultProject={project !== PROJECT_ALL && project !== PROJECT_ORG ? project : ""} onDone={(id) => { setCreating(false); if (id) { dev.setSelectedIssue(id); setFilter("open"); } }} />
      </Modal>
      <Modal open={trashOpen} onClose={() => setTrashOpen(false)} width={640}>
        <IssueTrash onClose={() => setTrashOpen(false)} />
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
        <span className="caption mono muted" title={issue.project ? "專案 " + issue.project : undefined}>{issueKey(issue)}</span>
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
  const [comment, setComment] = useState("");
  const [dlg, ask] = useConfirm();
  const { run, toastEl } = useAct();
  // 改優先度、指派、狀態帶畫面讀到的 version(別人先改了就提示、不蓋掉);留言不算修改本體,不帶
  const upd = (args: Record<string, unknown>) => run(() => updateIssue(issue, args, !("comment" in args)));
  const projects = useProjects();
  const proj = issue.project ? projects.find((p) => p.id === issue.project) : undefined;
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
            <span className="mono muted" style={{ fontSize: 15, whiteSpace: "nowrap" }}>{issueKey(issue)}</span>
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
            { label: "編輯…", onClick: () => setEditing(true) },
            { label: "移到垃圾桶…", danger: true, onClick: () => ask({ title: `把 ${issueKey(issue)} 移到垃圾桶?`, message: "移到垃圾桶後不會出現在列表與看板;組織管理員可以從垃圾桶還原。", buttons: [{ label: "移到垃圾桶", danger: true, onClick: async () => { const r = await run(() => action("issue_delete", { id: issue.id }), "已移到垃圾桶"); if (r !== undefined) dev.setSelectedIssue(undefined); } }] }) },
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
          <Pill text={issue.project ? "專案:" + (proj?.name ?? issue.project) : "組織"} color="indigo" icon="square.stack.3d.up" />
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
        <IssueEditForm issue={issue} onClose={() => setEditing(false)} />
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

// 開發 › Issue:存在本機的真實 Issue(人或 AI 透過 MCP 建立)。Swift 的 DevIssuesTab.swift。
// 建立、修改、留言、關閉走 MCP 工具(issue_create / issue_update),刪除是畫面專用的 issue_delete。
import { useEffect, useRef, useState } from "react";
import { action, useActionQuery, useDebounced } from "../../api";
import { useConfirm } from "../../components/Confirm";
import { EmptyState } from "../../components/EmptyState";
import { MarkdownText } from "../../components/Markdown";
import { MenuButton } from "../../components/Menu";
import { MetaFit, type FitItem } from "../../components/MetaFit";
import { Modal } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import { SIcon } from "../../components/SIcon";
import { useAct } from "../../components/Toast";
import { useOmixor } from "../../lib/directory";
import { monthDayTime } from "../../lib/format";
import { useList } from "../../lib/hooks";
import { go } from "../../lib/nav";
import { relative } from "../../fmt";
import { usedRepos, useDev } from "./devShared";
import { issueFilterDefs, usedLabels } from "./issueFilters";
import { FilterBar } from "../../components/FilterBar";
import { JiraImport } from "./JiraImport";
import { IssueEditForm, NewIssue } from "./IssueForm";
import { IssueTrash } from "./IssueTrash";
import { IssueItems } from "./IssueItems";
import { IssueDecisionSummary } from "./IssueDecisionSummary";
import { IssueHelp, IssueHelpPill } from "./IssueHelp";
import { IssueAcceptButton, IssueAcceptedPill } from "./IssueAccept";
import { itemsCountText } from "./itemModel";
import { resolveIssueRef, updateIssue, useIsOrgAdmin, useProjects } from "./issueApi";
import {
  findIssue, issueKey, PRIORITIES, PRIORITY, PROJECT_ALL, PROJECT_ORG, showPriorityPill,
  type Counts, type Issue,
} from "./issueModel";
import { MdTextarea } from "../../components/MdTextarea";
import ds from "./devShared.module.css";
import s from "./DevIssuesTab.module.css";
import { AuthorIcon as SharedAuthorIcon } from "../../components/Avatar";
import { cardTitleClass } from "../../components/Card";
import { layoutClass } from "../../components/Layout";

export { PRIORITY, showPriorityPill } from "./issueModel";
export type { Issue, IssueComment } from "./issueModel";
const isAIAuthor = (a: string) => a === "mcp" || a.startsWith("agent:");
type Filter = "open" | "closed" | "all";

/** issue_query 的回應(後端用核心的 model.QueryIssues 篩選、排序;counts、total 是全部 Issue 的) */
type IssueQueryResult = { issues: Issue[]; total: number; counts: Record<"open" | "closed" | "all", number> };

export function DevIssuesTab() {
  const dev = useDev();
  const [issues, err, issuesReady] = useList<Issue>("issues"); // 全部(詳細資料、倉庫選單用);列表本身照 issue_query 的結果
  // 篩選和看板共用(DevModel.issueFilters,切換分頁時保留)
  const f = dev.issueFilters, set = dev.setIssueFilters;
  const { query, label, priority, repo, project } = f; // repo:"" 全部、NO_REPO 未標記(比對規則在核心的 model.RepoMatch);project:"" 全部、"-" 只組織層級、PRJ-n
  const filter = f.status;
  const setFilter = (status: Filter) => set({ status });
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);
  const [trashOpen, setTrashOpen] = useState(false);
  const projects = useProjects();
  const isAdmin = useIsOrgAdmin();
  const q = useDebounced(query, 250);
  // 篩選、搜尋、排序在後端(核心的 model.QueryIssues):未解決的在前 → 優先度 → 最近更新 → 編號
  const [res, qErr] = useActionQuery<IssueQueryResult>("issue_query", { q, status: filter, label, priority, repo, project, sort: "status" }, ["issues"]);
  // 列表、各狀態數量(counts)、總數(total)都以後端回傳的為準(已依 project 計算),畫面不再自己篩、自己算
  const list = res?.issues ?? [];
  const counts: Counts = res?.counts ?? { all: 0, open: 0, closed: 0 };
  const total = res?.total ?? 0;
  const repos = usedRepos(issues);
  const statusOpt = (st: Filter) => {
    const text = st === "open" ? "未解決" : st === "closed" ? "已關閉" : "全部";
    const n = counts[st] ?? 0;
    return { value: st, text, label: <>{text}{n > 0 && <span className={"muted num " + s.optCount}>{n}</span>}</> };
  };
  useEffect(() => {
    if (res && !dev.selectedIssue && list[0]) dev.setSelectedIssue(list[0].id, "replace");
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
      .then((r) => dev.setSelectedIssue(r.deleted ? undefined : r.id, "replace"))
      .catch(() => dev.setSelectedIssue(undefined, "replace"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dev.selectedIssue, issuesReady, sel?.id]);
  return (
    <div className={layoutClass.page}>
      <FilterBar search={{ value: query, onChange: (v) => set({ query: v }), placeholder: "搜尋編號、標題、內文、標籤、留言" }}
        filters={[
          { key: "status", label: "狀態", title: "依狀態篩選", options: (["open", "closed", "all"] as Filter[]).map(statusOpt), value: filter, defaultValue: "open", showDefault: true, onChange: (v: string) => setFilter(v as Filter) },
          ...issueFilterDefs(f, set, { labels: usedLabels(issues), repos, projects }),
        ]}
        end={(compact) => (
          <>
            <button className="btn primary" title="新增 Issue" onClick={() => setCreating(true)}><SIcon name="plus" />{!compact && "新增 Issue"}</button>
            <MenuButton label={<SIcon name="ellipsis" />} title="更多動作" align="right" items={[
              { label: <><SIcon name="square.and.arrow.down" />從 Jira 匯入</>, onClick: () => setImporting(true) },
              ...(isAdmin ? [{ label: <><SIcon name="trash" />垃圾桶(已刪除的 Issue)</>, onClick: () => setTrashOpen(true) }] : []),
            ]} />
          </>
        )} />
      <div className={layoutClass.fill}>
        <div className={ds.sideList + " " + s.side}>
          {(err || qErr) && !res ? <EmptyState title="讀不到 Issue" icon="exclamationmark.triangle" message={qErr ?? err} />
            : !res ? null : list.length === 0 ? (
              <EmptyState title={total === 0 ? "還沒有 Issue" : "沒有符合的 Issue"} icon="exclamationmark.bubble"
                message={total === 0 ? "按「新增 Issue」、從 ⋯ 選單匯入 Jira,或讓 AI 透過 MCP 的 issue_create 存進來。" : undefined} />
            ) : (
              <div className="list">
                {list.map((i) => <IssueRow key={i.id} issue={i} showRepo={!repo} on={i.id === dev.selectedIssue} onClick={() => dev.setSelectedIssue(i.id)} />)}
              </div>
            )}
        </div>
        <div className={layoutClass.scroll}>
          {sel ? <IssueDetail key={sel.id} issue={sel} /> : (
            <EmptyState title="選一個 Issue" icon="exclamationmark.bubble" message="Issue 存在資料庫裡,重開也還在。AI 可以用 MCP 建立、留言與關閉。" />
          )}
        </div>
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
  };
}

/** 作者圖示:角色的圖示是 lucide 額外圖示,用 SIcon 畫 */
function AuthorIcon({ author, size = 18 }: { author: string; size?: number }) {
  return <SharedAuthorIcon author={author} size={size} icon={SIcon} mcpScale={0.8} personScale={0.85} />;
}

function IssueRow({ issue, showRepo, on, onClick }: { issue: Issue; showRepo: boolean; on: boolean; onClick: () => void }) {
  const { name } = useAuthor();
  const open = issue.status === "open";
  const p = PRIORITY[issue.priority] ?? PRIORITY.normal;
  // 中段(倉庫、標籤)放得下幾個就完整顯示幾個:倉庫只顯示短名、最先拿掉(已篩單一倉庫時不顯示);標籤依序保留,放不下的收成「+N」
  const fit: FitItem[] = [];
  if (showRepo && issue.repo) fit.push({ key: "repo", drop: 100, shrink: 60, className: "caption mono muted", node: <span title={"倉庫:" + issue.repo}>{issue.repo.split("/").pop()}</span> });
  (issue.labels ?? []).forEach((l, i) => fit.push({ key: "l:" + l, drop: i, label: l, node: <span title={l} className={ds.inlineFlex}><Pill text={l} /></span> }));
  const nComments = issue.comments?.length ?? 0;
  return (
    <div className={"list-row col gap4" + (on ? " on" : "")} onClick={onClick}>
      <div className={ds.rowTop + " gap6"}>
        <SIcon name={open ? "circle.dotted" : "checkmark.circle.fill"} color={open ? p.color : "secondary"} className={ds.rowIcon} />
        <span className={ds.clamp2 + (open ? "" : " " + ds.secondary)}>{issue.title}</span>
      </div>
      <div className="meta gap6">
        <span className="caption mono muted" title={issue.project ? "專案 " + issue.project : undefined}>{issueKey(issue)}</span>
        {isAIAuthor(issue.createdBy) && <span title={name(issue.createdBy)}><AuthorIcon author={issue.createdBy} size={14} /></span>}
        {showPriorityPill(issue.priority) && <Pill text={p.label} color={p.color} />}
        <IssueHelpPill issueId={issue.id} />
        <MetaFit items={fit} />
        {itemsCountText(issue) && <span className={"caption muted row " + s.gap3} title={`子項:${issue.itemsOpen ?? 0} 未完成 / 共 ${issue.itemsTotal}`}><SIcon name="checklist" size={11} />{itemsCountText(issue)}</span>}
        {nComments > 0 && <span className={"caption muted row " + s.gap3} title={`${nComments} 則留言`}><SIcon name="bubble.left" size={11} />{nComments}</span>}
        {nComments > 0 && <span className="caption faint">·</span>}
        <span className="caption muted">{relative(issue.updatedAt)}</span>
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
    <div className={"col " + s.detail}>
      <div className="col gap8">
        <div className={ds.rowTop + " gap8 wrap"}>
          {/* 標題至少保留 280px、可換行;按鈕群不換行、放不下時整組換到標題下一行,避免被擠成直排或把標題擠窄 */}
          <div className={s.titleRow + " gap8"}>
            <span className={"h2 " + s.title}>{issue.title}</span>
            <span className={"muted " + s.key}>{issueKey(issue)}</span>
          </div>
          <div className={"row gap8 " + s.actions}>
          <MenuButton className="btn" label={<SIcon name="ellipsis.circle" />} align="right" items={[
            { header: "優先度" },
            ...PRIORITIES.map((x) => ({ label: PRIORITY[x].label, checked: x === issue.priority, onClick: () => upd({ priority: x }) })),
            "sep",
            { header: "指派給角色(會自動排任務)" },
            { label: "未指派", checked: !issue.assignee && !issue.assigneeActor, onClick: () => upd({ assignee: "" }) },
            ...kit.agents.filter((x) => x.active !== false || x.id === issue.assignee).map((x) => ({ label: x.name, checked: x.id === issue.assignee, onClick: () => upd({ assignee: x.id }) })),
            "sep",
            { label: "編輯…", onClick: () => setEditing(true) },
            { label: "移到垃圾桶…", danger: true, onClick: () => ask({ title: `把 ${issueKey(issue)} 移到垃圾桶?`, message: "移到垃圾桶後不會出現在列表與看板;組織管理員可以從垃圾桶還原。", buttons: [{ label: "移到垃圾桶", danger: true, onClick: async () => { const r = await run(() => action("issue_delete", { id: issue.id }), "已移到垃圾桶"); if (r !== undefined) dev.setSelectedIssue(undefined, "replace"); } }] }) },
          ]} />
          <button className="btn" title="這個 Issue 的對話頻道(已有就跳過去,沒有就建立)" onClick={async () => {
            // 已有綁這個 Issue 的頻道:核心直接回傳那個;沒有就建立(名稱用 Issue 標題,成員含自己與指派的角色)
            const c = await run(() => action<{ id: string }>("channel_create", { name: issue.title.slice(0, 80), issueId: issue.id, members: issue.assignee ? [`agent:${issue.assignee}`] : [] }));
            if (c) go("channels", c.id);
          }}><SIcon name="bubble.left.and.bubble.right" /> 開對話頻道</button>
          <IssueAcceptButton issue={issue} run={run} />
          <button className="btn" onClick={() => upd({ status: open ? "closed" : "open" })}>{open ? "關閉" : "重新打開"}</button>
          </div>
        </div>
        <div className="row gap6 wrap">
          <Pill text={open ? "未解決" : "已關閉"} color={open ? "green" : "secondary"} icon={open ? "circle.dotted" : "checkmark.circle"} />
          <Pill text={"優先度:" + p.label} color={p.color} />
          <IssueAcceptedPill issue={issue} />
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
      {!editing && <IssueHelp issueId={issue.id} />}
      {!editing && <IssueDecisionSummary issueKey={issueKey(issue)} />}
      {!editing && <IssueItems issueKey={issueKey(issue)} quietError={(issue.itemsTotal ?? 0) === 0} />}
      {(issue.comments?.length > 0 || !editing) && (
        <div className={"col " + s.comments}>
          <div className={cardTitleClass}>留言</div>
          {(issue.comments ?? []).map((c) => (
            <div key={c.id} className={"col gap4 " + s.comment}>
              <div className="row gap6"><AuthorIcon author={c.author} /><span className={"caption " + ds.bold}>{name(c.author)}</span><span className="caption muted">{relative(c.at)}</span></div>
              <MarkdownText source={c.text} />
            </div>
          ))}
          <div className={s.composer + " gap8"}>
            <MdTextarea minRows={1} maxRows={8} placeholder="寫留言…" value={comment} onChange={setComment} />
            <button className="btn" disabled={!comment.trim()} onClick={async () => { const r = await upd({ comment: comment.trim() }); if (r !== undefined) setComment(""); }}>送出</button>
          </div>
        </div>
      )}
      {dlg}{toastEl}
    </div>
  );
}

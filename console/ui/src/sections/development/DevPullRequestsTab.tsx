// 開發 › Pull Requests(Swift 的 DevPullRequestsTab.swift)。
// 核准/要求修改/合併/關閉在 Swift 版只改本機的範例資料;這裡呼叫畫面動作 pull_request_set / pull_request_join_review。
import { AutoTextarea } from "../../components/AutoTextarea";
import { useState } from "react";
import { action } from "../../api";
import { ActorLabel, AvatarStack } from "../../components/Avatar";
import { AIBadge } from "../../components/Badge";
import { useConfirm } from "../../components/Confirm";
import { EmptyState } from "../../components/EmptyState";
import { iconButtonClass } from "../../components/IconButton";
import { MenuButton, useContextMenu, type MenuItem } from "../../components/Menu";
import { Pill } from "../../components/Pill";
import { SIcon } from "../../components/SIcon";
import { useAct } from "../../components/Toast";
import { copyText } from "../../lib/clipboard";
import { color } from "../../lib/color";
import { useOmixor } from "../../lib/directory";
import { monthDayTime, t } from "../../lib/format";
import { useList, useWidth } from "../../lib/hooks";
import {
  PR, PR_ORDER, RISK, RUN, WORK_STATUS, WORK_TYPE, type PipelineRun, type PullRequest, type PRStatusKey, type WorkItem,
} from "../../models";
import { relative } from "../../fmt";
import { age, ChecksIcon, DetailSection, DiffStat, isActivePR, useDev } from "./devShared";
import { canGo } from "../../features";
import ds from "./devShared.module.css";
import css from "./DevPullRequestsTab.module.css";
import { StatusDot } from "../../components/StatusDot";
import { layoutClass } from "../../components/Layout";
import { tableClass } from "../../components/Table";
import { plainClass } from "../../components/Tile";

type SortKey = "id" | "title" | "additions" | "createdAt" | "updatedAt";

function usePRActions() {
  const o = useOmixor();
  const { run, toastEl } = useAct();
  const set = (pr: PullRequest, status: PRStatusKey, note = "") => run(() => action("pull_request_set", { id: pr.id, status, note: note.trim() }));
  return {
    toastEl,
    approve: (pr: PullRequest, note?: string) => set(pr, "approved", note),
    requestChanges: (pr: PullRequest, note?: string) => set(pr, "changesRequested", note),
    merge: (pr: PullRequest) => set(pr, "merged"),
    close: (pr: PullRequest) => set(pr, "closed"),
    reopen: (pr: PullRequest) => set(pr, "open"),
    joinReview: (pr: PullRequest) => run(() => action("pull_request_join_review", { id: pr.id, person: o.me })),
  };
}
const canMerge = (pr: PullRequest) => pr.status === "approved" && pr.checks === "passed";
const canReview = (pr: PullRequest) => pr.status === "open" || pr.status === "changesRequested" || pr.status === "approved";

export function DevPullRequestsTab() {
  const o = useOmixor();
  const dev = useDev();
  const [prs, err, ready] = useList<PullRequest>("pull_requests");
  const [statusFilter, setStatusFilter] = useState("active");
  const [authorFilter, setAuthorFilter] = useState("all");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; asc: boolean }>({ key: "updatedAt", asc: false });
  const [ref, w] = useWidth<HTMLDivElement>();
  const menu = useContextMenu();
  const acts = usePRActions();

  const rows = prs.filter((pr) => {
    if (statusFilter === "active" && !isActivePR(pr)) return false;
    if (statusFilter !== "active" && statusFilter !== "all" && pr.status !== statusFilter) return false;
    if (dev.prRepoFilter && pr.repo !== dev.prRepoFilter) return false;
    if (authorFilter === "me" && !(pr.author.kind === "person" && pr.author.id === o.me)) return false;
    if (authorFilter === "ai" && pr.author.kind !== "ai") return false;
    if (authorFilter === "human" && pr.author.kind === "ai") return false;
    if (authorFilter === "review" && !pr.reviewers.includes(o.me)) return false;
    if (query) {
      const q = query.toLowerCase();
      if (!pr.title.toLowerCase().includes(q) && !pr.branch.toLowerCase().includes(q) && String(pr.id) !== q && !`#${pr.id}`.startsWith(q)) return false;
    }
    return true;
  }).sort((a, b) => {
    const k = sort.key;
    const va = k === "createdAt" || k === "updatedAt" ? t(a[k]) : (a as any)[k];
    const vb = k === "createdAt" || k === "updatedAt" ? t(b[k]) : (b as any)[k];
    const c = typeof va === "string" ? va.localeCompare(vb) : va - vb;
    return sort.asc ? c : -c;
  });
  const repoNames = [...new Set([...prs.map((p) => p.repo), ...o.repos.map((r) => r.id)])].sort();
  const th = (label: string, key?: SortKey, width?: number) => (
    <th style={{ width }} onClick={key ? () => setSort((s) => ({ key, asc: s.key === key ? !s.asc : key === "title" })) : undefined}>
      {label}{key && sort.key === key ? (sort.asc ? " ▲" : " ▼") : ""}
    </th>
  );
  const prMenu = (pr: PullRequest): MenuItem[] => [
    ...(canReview(pr) ? [
      { label: "核准", disabled: pr.status === "approved", onClick: () => acts.approve(pr) },
      { label: "要求修改", disabled: pr.status === "changesRequested", onClick: () => acts.requestChanges(pr) },
    ] : []),
    ...(canMerge(pr) ? [{ label: "合併", onClick: () => acts.merge(pr) }] : []),
    "sep",
    ...(isActivePR(pr) ? [{ label: "關閉 PR", onClick: () => acts.close(pr) }] : pr.status === "closed" ? [{ label: "重新開啟", onClick: () => acts.reopen(pr) }] : []),
    { label: "複製分支名稱", onClick: () => copyText(pr.branch) },
  ];
  const selected = prs.find((p) => p.id === dev.selectedPR);
  const wide = w >= 860;

  const table = !ready ? <div className="grow" /> : prs.length === 0 ? (
    <EmptyState title={err ? "讀不到 Pull Request" : "沒有 Pull Request"} icon="arrow.triangle.pull" message={err} />
  ) : (
    <div className={layoutClass.scroll + " " + css.rel}>
      <table className={tableClass.data}>
        <thead><tr>{th("#", "id", 54)}{th("標題", "title")}{th("狀態", undefined, 72)}{th("作者", undefined, 130)}{th("檢查", undefined, 40)}{th("變更", "additions", 90)}{th("審查者", undefined, 84)}{th("AI 風險", undefined, 70)}{th("建立", "createdAt", 60)}</tr></thead>
        <tbody>
          {rows.map((pr) => (
            <tr key={pr.id} className={pr.id === dev.selectedPR ? tableClass.sel : ""} onClick={() => dev.setSelectedPR(pr.id)} onContextMenu={menu.open(prMenu(pr))}>
              <td className="muted num">#{pr.id}</td>
              <td>
                <div className={"col " + css.titleCell}>
                  <span className={"row gap4 " + css.minw0}>{pr.author.kind === "ai" && <SIcon name="sparkles" size={11} color="purple" />}<span className="ellipsis">{pr.title}</span></span>
                  <span className="caption muted ellipsis">{pr.repo} · {pr.branch}</span>
                </div>
              </td>
              <td><Pill text={PR[pr.status]?.label ?? pr.status} color={PR[pr.status]?.color} /></td>
              <td><ActorLabel actor={pr.author} plainAI /></td>
              <td><ChecksIcon s={pr.checks} /></td>
              <td><DiffStat a={pr.additions} d={pr.deletions} /></td>
              <td>{pr.reviewers.length ? <AvatarStack actors={pr.reviewers.map((id) => ({ kind: "person", id }))} size={18} plainAI /> : <span className="faint">—</span>}</td>
              <td>{pr.aiReview ? <span className="row gap4 caption"><StatusDot c={RISK[pr.aiReview.risk].color} size={7} />{RISK[pr.aiReview.risk].label}</span> : <span className="faint">—</span>}</td>
              <td className="muted num" title={monthDayTime(pr.createdAt)}>{age(pr.createdAt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {rows.length === 0 && <EmptyState title="沒有符合條件的 PR" icon="line.3.horizontal.decrease.circle" message="調整上方的篩選條件" />}
    </div>
  );

  return (
    <div className={layoutClass.page}>
      <div className={ds.filterbar}>
        <input className={"input " + css.search} placeholder="搜尋標題、分支或編號" value={query} onChange={(e) => setQuery(e.target.value)} />
        <select className="select" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="active">進行中</option><option value="all">全部狀態</option><option disabled>──────</option>
          {PR_ORDER.map((s) => <option key={s} value={s}>{PR[s].label}</option>)}
        </select>
        <select className="select" value={dev.prRepoFilter ?? ""} onChange={(e) => dev.setPrRepoFilter(e.target.value || undefined)}>
          <option value="">全部 Repo</option>{repoNames.length > 0 && <option disabled>──────</option>}
          {repoNames.map((r) => <option key={r} value={r}>{o.repos.find((x) => x.id === r)?.name ?? r}</option>)}
        </select>
        <select className="select" value={authorFilter} onChange={(e) => setAuthorFilter(e.target.value)}>
          <option value="all">所有作者</option><option value="me">我開的</option><option value="review">等我審查</option><option disabled>──────</option>
          <option value="ai">AI 撰寫</option><option value="human">人撰寫</option>
        </select>
        <span className="grow" />
        <span className="muted num">{rows.length} 個 PR</span>
      </div>
      <div ref={ref} className={layoutClass.fill + (wide ? "" : " " + css.column)}>
        {table}
        {selected && (
          <div className={wide ? css.sideDetail : css.bottomDetail} style={wide ? { width: Math.min(400, w * 0.36) } : undefined}>
            <PRDetail key={selected.id} pr={selected} acts={acts} />
          </div>
        )}
      </div>
      {menu.el}{acts.toastEl}
    </div>
  );
}

function PRDetail({ pr, acts }: { pr: PullRequest; acts: ReturnType<typeof usePRActions> }) {
  const o = useOmixor();
  const dev = useDev();
  const [work] = useList<WorkItem>("work_items");
  const [runs] = useList<PipelineRun>("pipeline_runs");
  const [comment, setComment] = useState("");
  const [dlg, ask] = useConfirm();
  const linked = pr.linkedItem ? work.find((w) => w.id === pr.linkedItem) : undefined;
  const run = runs.filter((r) => r.repo === pr.repo && r.branch === pr.branch).sort((a, b) => t(b.startedAt) - t(a.startedAt))[0];
  const base = o.repos.find((r) => r.id === pr.repo)?.defaultBranch ?? "main";
  return (
    <div className={"col " + css.detail}>
      <div className="col gap6">
        <div className="row gap8">
          <span className="muted num">#{pr.id}</span>
          <Pill text={PR[pr.status]?.label ?? pr.status} color={PR[pr.status]?.color} />
          {pr.author.kind === "ai" && <AIBadge text="AI 撰寫" />}
          <span className="grow" />
          <button className={iconButtonClass.bare} title="關閉詳細" onClick={() => dev.setSelectedPR(undefined)}><SIcon name="xmark" /></button>
        </div>
        <div className="h3">{pr.title}</div>
        <div className="row gap6 muted">
          <SIcon name="arrow.triangle.branch" size={12} /><span className="mono ellipsis">{pr.branch}</span>
          <SIcon name="arrow.right" size={10} className="faint" /><span className="mono">{base}</span>
        </div>
        <div className="row gap12 wrap">
          <span>{pr.repo}</span><DiffStat a={pr.additions} d={pr.deletions} />
          <span className="caption muted">建立於 {relative(pr.createdAt)}・更新 {relative(pr.updatedAt)}</span>
        </div>
      </div>
      {isActivePR(pr) ? (
        <div className="col gap8">
          {canReview(pr) && <AutoTextarea ariaLabel="審查留言" minRows={1} placeholder="審查留言(選填)" value={comment} onChange={setComment} />}
          <div className="row gap8">
            {canReview(pr) && (
              <>
                <button className="btn green" disabled={pr.status === "approved"} onClick={() => { acts.approve(pr, comment); setComment(""); }}><SIcon name="checkmark" size={12} />核准</button>
                <button className="btn" disabled={pr.status === "changesRequested"} onClick={() => { acts.requestChanges(pr, comment); setComment(""); }}><SIcon name="exclamationmark.bubble" size={12} />要求修改</button>
              </>
            )}
            {pr.status === "approved" && (
              <button className="btn purple" disabled={!canMerge(pr)} title={pr.checks === "passed" ? "合併到預設分支" : "檢查尚未通過,不能合併"}
                onClick={() => ask({ title: `合併 PR #${pr.id}?`, message: `會把 ${pr.branch} 合併到預設分支,並觸發部署流程。`, buttons: [{ label: "合併", primary: true, onClick: () => acts.merge(pr) }] })}>
                <SIcon name="arrow.triangle.merge" size={12} />合併
              </button>
            )}
            <span className="grow" />
            <PRMoreMenu pr={pr} acts={acts} />
          </div>
        </div>
      ) : pr.status === "closed" ? (
        <button className={"btn " + css.start} onClick={() => acts.reopen(pr)}><SIcon name="arrow.uturn.backward" size={12} />重新開啟</button>
      ) : (
        <div className={"row gap6 " + ds.purple}><SIcon name="arrow.triangle.merge" />已於 {relative(pr.updatedAt)} 合併</div>
      )}
      {pr.aiReview && (
        <div className={"col gap8 " + css.aiReview}>
          <div className="row gap6"><SIcon name="sparkles" color="purple" /><span className={ds.bold}>AI 審查</span><span className="grow" /><Pill text={RISK[pr.aiReview.risk].label} color={RISK[pr.aiReview.risk].color} icon={pr.aiReview.risk === "high" ? "exclamationmark.triangle.fill" : undefined} /></div>
          <div>{pr.aiReview.summary}</div>
          {pr.aiReview.findings.length > 0 && (
            <div className="col gap4">
              {pr.aiReview.findings.map((f, i) => <div key={i} className={css.finding + " gap6 muted"}><span style={{ color: color(RISK[pr.aiReview!.risk].color) }}>•</span>{f}</div>)}
            </div>
          )}
        </div>
      )}
      <DetailSection title="CI 檢查">
        <div className="row gap8">
          <ChecksIcon s={pr.checks} showLabel />
          {run && <span className="caption muted ellipsis">{run.pipeline} #{run.id}</span>}
          <span className="grow" />
          {canGo("pipelines") && <button className="btn link" onClick={() => o.go("pipelines", run ? String(run.id) : undefined)}>{run ? "查看執行" : "前往 CI/CD"}</button>}
        </div>
        {run && run.stages.length > 0 && (
          <div className="row gap4">
            {run.stages.map((s) => (
              <div key={s.id} className={"col grow " + css.stage} title={`${s.name}:${RUN[s.status].label}`}>
                <span className={css.stageBar} style={{ background: color(RUN[s.status].color) }} />
                <span className={"muted ellipsis " + css.stageName}>{s.name}</span>
              </div>
            ))}
          </div>
        )}
        {run?.aiDiagnosis && pr.checks === "failed" && <div className={"caption gap4 " + ds.rowTop + " " + ds.purple}><SIcon name="sparkles" size={12} />{run.aiDiagnosis}</div>}
      </DetailSection>
      {pr.linkedItem && (
        <DetailSection title="相關工作項目">
          {linked ? (
            <button className={plainClass + " row gap8"} title="在看板中顯示" onClick={() => dev.showItem(linked.id)}>
              <span className={css.stripe} style={{ background: color(WORK_TYPE[linked.type].color) }} />
              <div className={"col grow " + css.gap2}>
                <span>{linked.title}</span>
                <span className="caption muted row gap4"><span className="mono">{linked.id}</span>·<SIcon name={WORK_STATUS[linked.status].icon} size={11} />{WORK_STATUS[linked.status].label}· {linked.points} 點</span>
              </div>
              <SIcon name="chevron.right" size={11} className="faint" />
            </button>
          ) : <span className="mono muted">{pr.linkedItem}</span>}
        </DetailSection>
      )}
      <DetailSection title="作者與審查者">
        <div className="row"><span className={"muted " + css.who}>作者</span><ActorLabel actor={pr.author} plainAI /></div>
        <div className={ds.rowTop}>
          <span className={"muted " + css.who}>審查者</span>
          {pr.reviewers.length === 0 ? <span className="faint">尚未指定</span> : <div className="col gap4">{pr.reviewers.map((id) => <ActorLabel key={id} actor={{ kind: "person", id }} plainAI />)}</div>}
        </div>
      </DetailSection>
      {dlg}
    </div>
  );
}

function PRMoreMenu({ pr, acts }: { pr: PullRequest; acts: ReturnType<typeof usePRActions> }) {
  const o = useOmixor();
  return (
    <MenuButton className="btn" align="right" label={<SIcon name="ellipsis.circle" />} items={[
      // me 還沒讀到(登入者未知)時不提供「加入審查」(原本預設成 "tim");沒啟用登入又沒有對應人員時也不提供
      ...(o.assignable && !pr.reviewers.includes(o.me) ? [{ label: "加入審查", onClick: () => acts.joinReview(pr) }] : []),
      { label: "請 AI 助理說明這個 PR", onClick: () => window.dispatchEvent(new CustomEvent("omixor-show-assistant", { detail: { section: "development", prompt: `說明 PR #${pr.id}` } })) },
      "sep",
      { label: "關閉 PR", danger: true, onClick: () => acts.close(pr) },
    ]} />
  );
}

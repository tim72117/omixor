// 開發進度 › GitLab(公司自架):追蹤中專案的 Pipelines(jobs、stage、失敗紀錄)與開著的 Merge Requests(head pipeline、核准)。只讀,只看資料。
// Swift 版沒有這頁;版面比照 DevGitHubTab。資料:view gitlab;動作:gitlab_refresh、gitlab_jobs、gitlab_job_log、gitlab_mr(見 docs/PORTING.md)。
// 伺服器、登入、追蹤的專案在 設定 › 程式碼倉庫(元件在 ./gitlabSettings);這裡沒設定好時只顯示空狀態與「前往設定」。
import { useEffect, useState } from "react";
import { action, useView } from "../api";
import { Card, EmptyState, Pill } from "../components/ui";
import { Banner, Check, MarkdownText, Seg, SIcon, useAct } from "../components/opsKit";
import { relative } from "../fmt";
import { ManageReposButton, RepoSetupEmpty } from "../sections/development/devShared";
import { InsecureBanner } from "./gitlabSettings";

type Connection = {
  id: string; name: string; baseURL: string; auth: "oauth" | "approval" | "bearer" | "basic" | "none";
  caCert?: string; insecure?: boolean;
  oauth: { clientId: string; hasClientSecret: boolean; [k: string]: unknown }; [k: string]: unknown;
};
type ConnState = { account?: string | null; busy?: boolean; error?: string | null; lastTest?: string | null };
export type GLProject = { id: number; key: string; name: string; path: string; description: string; defaultBranch: string; url: string; visibility: string; lastActivityAt?: string };
type GLApproval = { approved: boolean; required: number; left: number; approvedBy: string[] };
type GLMR = {
  project: string; id: number; iid: number; title: string; author: string; authorName: string; sourceBranch: string; targetBranch: string;
  draft: boolean; labels: string[]; reviewers: string[]; createdAt?: string; updatedAt?: string; url: string; sha: string;
  mergeStatus: string; hasConflicts: boolean; pipeline?: { id: number; status: string; url: string } | null; approval?: GLApproval | null;
};
type GLPipeline = {
  project: string; id: number; iid: number; status: string; statusLabel: string; ref: string; sha: string; source: string; sourceLabel: string;
  user: string; userName: string; createdAt?: string; updatedAt?: string; startedAt?: string; finishedAt?: string; duration?: number; url: string;
  isRunning: boolean; isFailure: boolean;
};
type GLJob = { id: number; name: string; stage: string; status: string; statusLabel: string; duration?: number; url: string; allowFailure: boolean; failureReason: string; isFailure: boolean };
type GLFile = { oldPath: string; newPath: string; status: string; additions: number; deletions: number };
type GLMRDetail = { mr: GLMR; description: string; changesCount: string; additions: number; deletions: number; files: GLFile[] };
export type GitLabView = {
  settings: { projects: string[]; connection: string };
  snapshot: { projects: GLProject[]; mrs: GLMR[]; pipelines: GLPipeline[]; fetchedAt?: string | null; busy: boolean; errors: Record<string, string>; error?: string | null };
  connection: Connection | null; loggedIn: boolean; state: ConnState | null; configured: boolean;
  baseURL: string; caCert: string; insecure: boolean; callback: string; scopes: string; applicationsURL: string; tokenURL: string;
  /** 讀寫組織設定(核心)的錯誤;網址與追蹤的專案是組織的設定,token、CA 憑證檔、不驗證憑證留在這台 */
  settingsError?: string;
};

const LABELS: Record<string, string> = {
  created: "已建立", waiting_for_resource: "等待資源", preparing: "準備中", pending: "排隊中", running: "執行中", success: "成功",
  failed: "失敗", canceled: "已取消", canceling: "取消中", skipped: "略過", manual: "手動", scheduled: "排程",
};
const statusLabel = (s?: string) => (s ? LABELS[s] ?? s : "沒有 pipeline");
function statusIcon(s?: string, allowFailure?: boolean): [string, string] {
  switch (s) {
    case "success": return ["checkmark.circle.fill", "green"];
    case "failed": return allowFailure ? ["exclamationmark.triangle", "orange"] : ["xmark.circle.fill", "red"];
    case "canceled": case "canceling": case "skipped": return ["slash.circle", "secondary"];
    case "manual": case "scheduled": return ["hand.raised", "secondary"];
    case undefined: case "": return ["circle.dashed", "secondary"];
    default: return ["clock.arrow.circlepath", "orange"];
  }
}
const dur = (sec?: number | null) => (sec == null || isNaN(sec) ? undefined : sec < 60 ? `${Math.trunc(sec)} 秒` : `${Math.trunc(sec / 60)} 分 ${Math.trunc(sec % 60)} 秒`);
const openURL = (u: string) => u && window.open(u, "_blank", "noopener");
const err = (e: any) => String(e?.message ?? e);

export function GitLabTab() {
  const [gl, viewErr] = useView<GitLabView>("gitlab", ["connections"]);
  useEffect(() => {
    if (gl?.loggedIn && !gl.snapshot?.fetchedAt && !gl.snapshot?.busy) action("gitlab_refresh").catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gl?.loggedIn, gl?.state?.account]);
  if (viewErr && !gl) return <EmptyState title="讀不到 GitLab 資料" icon="chevron.left.forwardslash.chevron.right" message={viewErr} />;
  if (!gl) return <EmptyState title="讀取中…" icon="chevron.left.forwardslash.chevron.right" />;
  if (!gl.connection || !gl.configured || !gl.loggedIn) {
    return (
      <div className="col" style={{ flex: 1, minHeight: 0 }}>
        {gl.insecure && <div style={{ padding: "8px 16px 0" }}><InsecureBanner gl={gl} /></div>}
        <RepoSetupEmpty title={!gl.connection || !gl.configured ? "還沒設定公司的 GitLab" : "還沒登入 GitLab"}
          message="GitLab 伺服器、登入與要追蹤的專案在 設定 › 程式碼倉庫 設定。" />
      </div>
    );
  }
  return <GitLabMain gl={gl} />;
}

// MARK: - 主畫面

function GitLabMain({ gl }: { gl: GitLabView }) {
  const [mode, setMode] = useState<"pipelines" | "mrs">("pipelines");
  const [selPipeline, setSelPipeline] = useState<string>();
  const [selMR, setSelMR] = useState<string>();
  const [failedOnly, setFailedOnly] = useState(false);
  const [rawProjectFilter, setProjectFilter] = useState("");
  const { run, toastEl } = useAct();
  const snap = gl.snapshot ?? { projects: [], mrs: [], pipelines: [], busy: false, errors: {} };
  const projects = gl.settings?.projects ?? [];
  // 在設定頁停止追蹤後,篩選值可能已不在清單裡,當成「全部專案」
  const projectFilter = projects.includes(rawProjectFilter) ? rawProjectFilter : "";
  const nameOf = (key: string) => snap.projects?.find((p) => p.key === key)?.path ?? key;
  const pipelines = (snap.pipelines ?? []).filter((p) => (!projectFilter || p.project === projectFilter) && (!failedOnly || p.isFailure));
  const mrs = (snap.mrs ?? []).filter((m) => !projectFilter || m.project === projectFilter);
  const errs = Object.entries(snap.errors ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const pSel = snap.pipelines?.find((p) => `${p.project}#${p.id}` === selPipeline);
  const mSel = snap.mrs?.find((m) => `${m.project}!${m.iid}` === selMR);
  return (
    <div className="ops-page">
      <div className="row gap8 wrap" style={{ padding: "8px 16px", gap: 10 }}>
        <SIcon name="person.crop.circle.badge.checkmark" color="green" />
        <span>{gl.state?.account ?? "已登入"}</span>
        <span className="caption muted mono">{gl.baseURL}</span>
        <span style={{ width: 1, height: 16, background: "var(--line)" }} />
        <span className="caption muted">追蹤 {projects.length} 個專案</span>
        {gl.settingsError && <span className="caption ellipsis" style={{ color: "var(--orange)" }} title={gl.settingsError}>組織設定:{gl.settingsError}</span>}
        <span className="grow" />
        {snap.fetchedAt && <span className="caption muted">更新於 {relative(snap.fetchedAt)}</span>}
        {snap.busy && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
        <ManageReposButton />
        <button className="btn" title="重新整理" disabled={snap.busy} onClick={() => run(() => action("gitlab_refresh"))}><SIcon name="arrow.clockwise" /></button>
      </div>
      {gl.insecure && <div style={{ padding: "0 16px 6px" }}><InsecureBanner gl={gl} /></div>}
      {(errs.length > 0 || snap.error) && (
        <div className="col" style={{ background: "color-mix(in srgb, var(--orange) 8%, transparent)" }}>
          {snap.error && <Banner>{snap.error}</Banner>}
          {errs.map(([k, e]) => <Banner key={k}>{k}:{e}</Banner>)}
        </div>
      )}
      <div className="divider" />
      {projects.length === 0 ? (
        <RepoSetupEmpty title="還沒追蹤任何專案" icon="shippingbox" message="要追蹤的專案在 設定 › 程式碼倉庫 › GitLab 加入。" />
      ) : (
        <div className="ops-fill">
          <div className="col" style={{ width: 440, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
            <div className="row gap8" style={{ padding: 10 }}>
              <div className="grow"><Seg value={mode} onChange={setMode} options={[{ value: "pipelines", label: `Pipelines(${snap.pipelines?.length ?? 0})` }, { value: "mrs", label: `Merge Requests(${snap.mrs?.length ?? 0})` }]} /></div>
              {projects.length > 1 && (
                <select className="select" style={{ minWidth: 140 }} value={projectFilter} onChange={(e) => setProjectFilter(e.target.value)}>
                  <option value="">全部專案</option>{projects.map((k) => <option key={k} value={k}>{nameOf(k)}</option>)}
                </select>
              )}
            </div>
            {mode === "pipelines" && <div style={{ padding: "0 12px 6px" }}><Check on={failedOnly} onChange={setFailedOnly}><span className="caption">只看失敗</span></Check></div>}
            <div className="divider" />
            {mode === "pipelines" ? (
              pipelines.length === 0 ? <EmptyState title={snap.fetchedAt ? "沒有 pipeline" : "讀取中…"} icon="arrow.triangle.branch" /> : (
                <div className="ops-list">
                  {pipelines.map((p) => {
                    const id = `${p.project}#${p.id}`;
                    const [icon, c] = statusIcon(p.status);
                    return (
                      <div key={id} className={"ops-list-row row gap8" + (id === selPipeline ? " on" : "")} style={{ alignItems: "flex-start" }} onClick={() => setSelPipeline(id)}>
                        <SIcon name={icon} color={c} style={{ marginTop: 2 }} />
                        <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                          <span className="row gap6"><span className="mono ellipsis">{p.ref}</span><span className="caption muted">#{p.iid || p.id}</span><span className="caption">{p.statusLabel}</span></span>
                          <div className="row gap6 caption muted">
                            <span>{p.sourceLabel}</span>{p.user && <span>{p.user}</span>}<span className="mono">{p.sha.slice(0, 8)}</span>
                            {projects.length > 1 && <span className="faint ellipsis">{nameOf(p.project)}</span>}
                            <span className="grow" />{p.createdAt && <span style={{ flex: "none" }}>{relative(p.createdAt)}</span>}
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )
            ) : mrs.length === 0 ? <EmptyState title={snap.fetchedAt ? "沒有開著的 MR" : "讀取中…"} icon="arrow.triangle.pull" /> : (
              <div className="ops-list">
                {mrs.map((m) => {
                  const id = `${m.project}!${m.iid}`;
                  const [icon, c] = statusIcon(m.pipeline?.status);
                  return (
                    <div key={id} className={"ops-list-row row gap8" + (id === selMR ? " on" : "")} style={{ alignItems: "flex-start" }} onClick={() => setSelMR(id)}>
                      <SIcon name="arrow.triangle.pull" color={m.draft ? "secondary" : "green"} style={{ marginTop: 2 }} />
                      <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                        <span className="row gap4"><span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{m.title}</span>{m.draft && <Pill text="草稿" />}</span>
                        <div className="row gap6 caption muted">
                          <span className="mono">!{m.iid}</span><span>{m.author}</span><span className="mono ellipsis">{m.sourceBranch}</span>
                          <span title={`pipeline:${statusLabel(m.pipeline?.status)}`}><SIcon name={icon} color={c} size={12} /></span>
                          <ApprovalMark a={m.approval} />
                          <span className="grow" />{m.updatedAt && <span style={{ flex: "none" }}>{relative(m.updatedAt)}</span>}
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
          <div className="ops-scroll">
            {mode === "pipelines" && pSel ? <PipelineDetail key={selPipeline} p={pSel} projectName={nameOf(pSel.project)} />
              : mode === "mrs" && mSel ? <MRDetail key={selMR} m={mSel} />
                : <EmptyState title={mode === "pipelines" ? "選一個 pipeline" : "選一個 MR"} icon="arrow.triangle.branch" message="資料每 5 分鐘自動更新,也可以按右上角重新整理。" />}
          </div>
        </div>
      )}
      {toastEl}
    </div>
  );
}

function ApprovalMark({ a }: { a?: GLApproval | null }) {
  if (!a) return null;
  if (a.approved && a.left === 0) return <span title="已核准"><SIcon name="checkmark.seal" color="green" size={12} /></span>;
  return <span title={`還要 ${a.left} 個核准`}>{a.required - a.left}/{a.required}</span>;
}

function approvalText(a?: GLApproval | null) {
  if (!a) return undefined;
  const by = a.approvedBy?.length ? `(${a.approvedBy.join("、")})` : "";
  if (a.left === 0) return (a.approved ? "已核准" : "不需要核准") + by;
  return `還要 ${a.left} 個核准,共要 ${a.required} 個` + by;
}

function PipelineDetail({ p, projectName }: { p: GLPipeline; projectName: string }) {
  const [jobs, setJobs] = useState<GLJob[]>();
  const [stages, setStages] = useState<string[]>([]);
  const [error, setError] = useState<string>();
  const [logJob, setLogJob] = useState<number>();
  const [log, setLog] = useState<string>();
  const [logError, setLogError] = useState<string>();
  useEffect(() => {
    action<{ jobs: GLJob[]; stages: string[] }>("gitlab_jobs", { project: p.project, pipeline: p.id })
      .then((r) => { setJobs(r?.jobs ?? []); setStages(r?.stages ?? []); setError(undefined); })
      .catch((e) => setError(`讀不到 jobs:${err(e)}`));
  }, [p.project, p.id]);
  const loadLog = (id: number) => {
    setLogJob(id); setLog(undefined); setLogError(undefined);
    action<{ tail: string }>("gitlab_job_log", { project: p.project, job: id, lines: 150 })
      .then((x) => setLog(x?.tail ?? ""))
      .catch((e) => setLogError(`讀不到紀錄:${err(e)}`));
  };
  const [icon, c] = statusIcon(p.status);
  return (
    <div className="col" style={{ padding: 20, gap: 12, maxWidth: 860 }}>
      <div className="row" style={{ alignItems: "baseline", gap: 8 }}>
        <span className="h3" style={{ userSelect: "text" }}>Pipeline #{p.iid || p.id}</span><span className="muted">{projectName}</span><span className="grow" />
        {p.url && <button className="btn" onClick={() => openURL(p.url)}>在 GitLab 打開</button>}
      </div>
      <div className="row gap8 wrap">
        <Pill text={p.statusLabel} color={c} icon={icon} />
        <Pill text={p.ref} icon="arrow.triangle.branch" />
        <Pill text={p.sourceLabel} icon="bolt" />
        <span className="caption mono muted">{p.sha.slice(0, 8)}</span>
        {p.user && <span className="caption muted">{p.userName || p.user}</span>}
        {dur(p.duration) && <span className="caption muted">{dur(p.duration)}</span>}
        {p.createdAt && <span className="caption muted">{relative(p.createdAt)}</span>}
      </div>
      <div className="divider" />
      {error && <Banner>{error}</Banner>}
      {jobs ? stages.map((stage) => (
        <Card key={stage} title={stage}>
          {jobs.filter((j) => j.stage === stage).map((j) => {
            const [ji, jc] = statusIcon(j.status, j.allowFailure);
            return (
              <div key={j.id} className="col" style={{ gap: 6 }}>
                <div className="row gap8">
                  <SIcon name={ji} color={jc} size={13} />
                  <span style={{ fontWeight: j.isFailure ? 600 : undefined }}>{j.name}</span>
                  <span className="caption muted">{j.statusLabel}{j.allowFailure && j.status === "failed" ? "(允許失敗)" : ""}</span>
                  {j.failureReason && <span className="caption" style={{ color: "var(--red)" }}>{j.failureReason}</span>}
                  {dur(j.duration) && <span className="caption muted">{dur(j.duration)}</span>}
                  <span className="grow" />
                  {j.status === "failed" && <button className="btn" onClick={() => loadLog(j.id)}>{logJob === j.id && log != null ? "重新讀紀錄" : "看紀錄最後幾行"}</button>}
                  {j.url && <button className="ops-icon-btn" title="在 GitLab 打開" onClick={() => openURL(j.url)}><SIcon name="arrow.up.right.square" size={12} /></button>}
                </div>
                {logJob === j.id && (
                  <>
                    {logError && <div className="caption" style={{ color: "var(--orange)" }}>{logError}</div>}
                    {log != null ? <pre className="ops-code" style={{ fontSize: 11, userSelect: "text", maxHeight: 420, overflow: "auto" }}>{log}</pre> : !logError && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
                  </>
                )}
              </div>
            );
          })}
        </Card>
      )) : !error && <div className="row" style={{ justifyContent: "center" }}><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /></div>}
      {jobs && jobs.length === 0 && <div className="muted">這個 pipeline 沒有 job</div>}
    </div>
  );
}

function MRDetail({ m }: { m: GLMR }) {
  const [detail, setDetail] = useState<GLMRDetail>();
  const [error, setError] = useState<string>();
  useEffect(() => {
    action<GLMRDetail>("gitlab_mr", { project: m.project, iid: m.iid }).then(setDetail).catch((e) => setError(`讀不到 MR:${err(e)}`));
  }, [m.project, m.iid]);
  const mr = detail?.mr ?? m;
  const [icon, c] = statusIcon(mr.pipeline?.status);
  const ap = approvalText(mr.approval);
  return (
    <div className="col" style={{ padding: 20, gap: 12, maxWidth: 860 }}>
      <div className="row gap8" style={{ alignItems: "baseline" }}>
        <span className="h3" style={{ userSelect: "text" }}>{m.title}</span><span className="muted" style={{ fontSize: 15 }}>!{m.iid}</span><span className="grow" />
        {m.url && <button className="btn" onClick={() => openURL(m.url)}>在 GitLab 打開</button>}
      </div>
      <div className="row gap8 wrap">
        <Pill text={`pipeline:${statusLabel(mr.pipeline?.status)}`} color={c} icon={icon} />
        {m.draft && <Pill text="草稿" />}
        {ap && <Pill text={ap} icon="checkmark.seal" color={mr.approval?.left === 0 ? "green" : "orange"} />}
        {m.hasConflicts && <Pill text="有衝突" color="red" icon="exclamationmark.triangle" />}
        <Pill text={`${m.sourceBranch} → ${m.targetBranch}`} icon="arrow.triangle.branch" />
        <span className="caption muted">{m.authorName || m.author}</span>
        {(m.labels ?? []).map((l) => <Pill key={l} text={l} icon="tag" />)}
        {detail && <span className="caption muted num">+{detail.additions} −{detail.deletions} · {detail.changesCount} 個檔案</span>}
      </div>
      {m.reviewers?.length > 0 && <div className="caption muted">審查者:{m.reviewers.join("、")}</div>}
      {mr.pipeline?.url && <button className="btn link" style={{ alignSelf: "flex-start" }} onClick={() => openURL(mr.pipeline!.url)}>打開 head pipeline #{mr.pipeline.id}</button>}
      <div className="divider" />
      {error && <Banner>{error}</Banner>}
      {detail ? (
        <>
          {detail.description ? <MarkdownText source={detail.description} /> : <div className="muted">沒有說明</div>}
          <Card title="變更的檔案">
            {(detail.files ?? []).map((f) => (
              <div key={f.oldPath + "→" + f.newPath} className="row gap8">
                <span className="caption" style={{ width: 44, color: f.status === "added" ? "var(--green)" : f.status === "deleted" ? "var(--red)" : "var(--fg2)" }}>
                  {({ added: "新增", deleted: "刪除", renamed: "改名", modified: "修改" } as Record<string, string>)[f.status] ?? f.status}
                </span>
                <span className="caption mono ellipsis grow">{f.status === "renamed" ? `${f.oldPath} → ${f.newPath}` : f.newPath}</span>
                <span className="caption num" style={{ color: "var(--green)" }}>+{f.additions}</span>
                <span className="caption num" style={{ color: "var(--red)" }}>−{f.deletions}</span>
              </div>
            ))}
          </Card>
        </>
      ) : !error && <div className="row" style={{ justifyContent: "center" }}><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /></div>}
    </div>
  );
}

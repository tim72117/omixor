// 開發進度 › GitLab(公司自架):追蹤中專案的 Pipelines(jobs、stage、失敗紀錄)與開著的 Merge Requests(head pipeline、核准)。只讀。
// Swift 版沒有這頁;版面比照 DevGitHubTab。資料:view gitlab;動作:gitlab_* 與 connection_*(見 docs/PORTING.md)。
// 獨立元件,之後接進開發進度的分頁:import { GitLabTab } from "../../gitlab/GitLabTab"。
import { useEffect, useState } from "react";
import { action, useRun, useView } from "../api";
import { Card, EmptyState, Pill } from "../components/ui";
import { Banner, Check, copyText, MarkdownText, MenuButton, Seg, SIcon, useAct } from "../components/opsKit";
import { relative } from "../fmt";

type Connection = {
  id: string; name: string; baseURL: string; auth: "oauth" | "approval" | "bearer" | "basic" | "none";
  caCert?: string; insecure?: boolean;
  oauth: { clientId: string; hasClientSecret: boolean; [k: string]: unknown }; [k: string]: unknown;
};
type ConnState = { account?: string | null; busy?: boolean; error?: string | null; lastTest?: string | null };
type GLProject = { id: number; key: string; name: string; path: string; description: string; defaultBranch: string; url: string; visibility: string; lastActivityAt?: string };
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
type GitLabView = {
  settings: { projects: string[]; connection: string };
  snapshot: { projects: GLProject[]; mrs: GLMR[]; pipelines: GLPipeline[]; fetchedAt?: string | null; busy: boolean; errors: Record<string, string>; error?: string | null };
  connection: Connection | null; loggedIn: boolean; state: ConnState | null; configured: boolean;
  baseURL: string; caCert: string; insecure: boolean; callback: string; scopes: string; applicationsURL: string; tokenURL: string;
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
    return <div className="page-scroll"><div style={{ padding: 24, maxWidth: 760 }}><GitLabSetup gl={gl} /></div></div>;
  }
  return <GitLabMain gl={gl} />;
}

function InsecureBanner({ gl }: { gl: GitLabView }) {
  if (!gl.insecure) return null;
  return <Banner tint="red">目前「不驗證憑證」:連線可能被攔截,token 也可能外流。只能在測試環境用;正式環境請改成指定公司的 CA 憑證檔。</Banner>;
}

// MARK: - 主畫面

function GitLabMain({ gl }: { gl: GitLabView }) {
  const [mode, setMode] = useState<"pipelines" | "mrs">("pipelines");
  const [selPipeline, setSelPipeline] = useState<string>();
  const [selMR, setSelMR] = useState<string>();
  const [failedOnly, setFailedOnly] = useState(false);
  const [projectFilter, setProjectFilter] = useState("");
  const [newProject, setNewProject] = useState("");
  const [projectError, setProjectError] = useState<string>();
  const [showServer, setShowServer] = useState(false);
  const { run, toastEl } = useAct();
  const snap = gl.snapshot ?? { projects: [], mrs: [], pipelines: [], busy: false, errors: {} };
  const projects = gl.settings?.projects ?? [];
  const nameOf = (key: string) => snap.projects?.find((p) => p.key === key)?.path ?? key;
  const pipelines = (snap.pipelines ?? []).filter((p) => (!projectFilter || p.project === projectFilter) && (!failedOnly || p.isFailure));
  const mrs = (snap.mrs ?? []).filter((m) => !projectFilter || m.project === projectFilter);
  const addProject = async () => {
    try {
      await action("gitlab_add_project", { project: newProject.trim() });
      setNewProject(""); setProjectError(undefined);
      action("gitlab_refresh").catch(() => undefined);
    } catch (e) { setProjectError(err(e)); }
  };
  const errs = Object.entries(snap.errors ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const pSel = snap.pipelines?.find((p) => `${p.project}#${p.id}` === selPipeline);
  const mSel = snap.mrs?.find((m) => `${m.project}!${m.iid}` === selMR);
  const webOf = (key: string) => snap.projects?.find((p) => p.key === key)?.url ?? `${gl.baseURL}/${key}`;
  return (
    <div className="ops-page">
      <div className="row gap8 wrap" style={{ padding: "8px 16px", gap: 10 }}>
        <SIcon name="person.crop.circle.badge.checkmark" color="green" />
        <span>{gl.state?.account ?? "已登入"}</span>
        <span className="caption muted mono">{gl.baseURL}</span>
        <span style={{ width: 1, height: 16, background: "var(--line)" }} />
        {projects.map((k) => (
          <MenuButton key={k} className="btn link" label={<><SIcon name="shippingbox" size={12} />{nameOf(k)}</>} items={[
            { label: "在 GitLab 打開", onClick: () => openURL(webOf(k)) },
            { label: "停止追蹤", danger: true, onClick: () => { run(() => action("gitlab_remove_project", { project: k })); if (projectFilter === k) setProjectFilter(""); } },
          ]} />
        ))}
        <input className="input" style={{ width: 180 }} placeholder="group/project 或 id" value={newProject} onChange={(e) => setNewProject(e.target.value)} onKeyDown={(e) => e.key === "Enter" && newProject.trim() && addProject()} />
        <button className="btn" disabled={!newProject.trim()} onClick={addProject}>加入</button>
        {projectError && <span className="caption ellipsis" style={{ color: "var(--red)" }}>{projectError}</span>}
        <span className="grow" />
        {snap.fetchedAt && <span className="caption muted">更新於 {relative(snap.fetchedAt)}</span>}
        {snap.busy && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
        <button className="btn" title="伺服器與登入設定" onClick={() => setShowServer(!showServer)}><SIcon name="gearshape" /></button>
        <button className="btn" title="重新整理" disabled={snap.busy} onClick={() => run(() => action("gitlab_refresh"))}><SIcon name="arrow.clockwise" /></button>
      </div>
      {gl.insecure && <div style={{ padding: "0 16px 6px" }}><InsecureBanner gl={gl} /></div>}
      {showServer && <div style={{ padding: "0 16px 10px", maxWidth: 760 }}><ServerCard gl={gl} /><LogoutRow gl={gl} /></div>}
      {(errs.length > 0 || snap.error) && (
        <div className="col" style={{ background: "color-mix(in srgb, var(--orange) 8%, transparent)" }}>
          {snap.error && <Banner>{snap.error}</Banner>}
          {errs.map(([k, e]) => <Banner key={k}>{k}:{e}</Banner>)}
        </div>
      )}
      <div className="divider" />
      {projects.length === 0 ? (
        <EmptyState title="還沒追蹤任何專案" icon="shippingbox" message="在上面輸入 group/project(可以有子群組,也可以貼專案網址或數字 id)按「加入」。" />
      ) : (
        <div className="ops-fill">
          <div className="col" style={{ width: 440, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
            <div className="row gap8" style={{ padding: 10 }}>
              <div className="grow"><Seg value={mode} onChange={setMode} options={[{ value: "pipelines", label: `Pipelines(${snap.pipelines?.length ?? 0})` }, { value: "mrs", label: `Merge Requests(${snap.mrs?.length ?? 0})` }]} /></div>
              {projects.length > 1 && (
                <select className="select" style={{ width: 140 }} value={projectFilter} onChange={(e) => setProjectFilter(e.target.value)}>
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

// MARK: - 登入引導

function ServerCard({ gl, onSaved }: { gl: GitLabView; onSaved?: () => void }) {
  const [base, setBase] = useState(gl.baseURL ?? "");
  const [ca, setCA] = useState(gl.caCert ?? "");
  const [insecure, setInsecure] = useState(!!gl.insecure);
  const { busy, error, run } = useRun();
  const changedBase = gl.configured && base.trim().replace(/\/+$/, "") !== gl.baseURL;
  return (
    <Card>
      <div className="card-title">1. 公司的 GitLab 在哪裡</div>
      <input className="input" placeholder="https://gitlab.corp.example.com" value={base} onChange={(e) => setBase(e.target.value)} />
      <div className="caption muted">只要網址(不用 /api/v4)。omixor 只讀,token 只會送到這一台主機。</div>
      <div className="caption muted" style={{ marginTop: 4 }}>自簽憑證時,指定公司的 CA 憑證檔(PEM 格式,例如 ~/certs/corp-ca.pem):</div>
      <input className="input mono" placeholder="CA 憑證檔路徑(選填)" value={ca} disabled={insecure} onChange={(e) => setCA(e.target.value)} />
      <Check on={insecure} onChange={setInsecure}><span className="caption">不驗證憑證(只給測試環境)</span></Check>
      {insecure && <Banner tint="red">不驗證憑證時,連線可能被攔截、token 可能外流。正式環境請改用 CA 憑證檔。</Banner>}
      {changedBase && <div className="caption" style={{ color: "var(--orange)" }}>換網址會登出,要重新登入。</div>}
      <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy || !base.trim()} onClick={() => run(async () => {
        await action("gitlab_settings", { baseURL: base.trim(), caCert: insecure ? "" : ca.trim(), insecure });
        onSaved?.();
      })}>儲存</button>
      {error && <Banner>{error}</Banner>}
    </Card>
  );
}

function LogoutRow({ gl }: { gl: GitLabView }) {
  const { busy, run } = useRun();
  if (!gl.connection) return null;
  return (
    <div className="row gap8" style={{ paddingTop: 6 }}>
      <span className="caption muted">登入方式:{gl.connection.auth === "bearer" ? "個人存取權杖" : "GitLab OAuth"}</span>
      <span className="grow" />
      <button className="btn link" disabled={busy} onClick={() => run(() => action("connection_logout", { id: gl.connection!.id }))}>登出</button>
    </div>
  );
}

function GitLabSetup({ gl }: { gl: GitLabView }) {
  const conn = gl.connection;
  const st = gl.state;
  const [clientId, setClientId] = useState(conn?.oauth?.clientId ?? "");
  const [secret, setSecret] = useState("");
  const [useToken, setUseToken] = useState(false);
  const { busy, error, run } = useRun();
  const copyRow = (label: string, value: string) => (
    <div className="row gap8">
      <span className="caption muted" style={{ width: 150 }}>{label}</span>
      <span className="mono" style={{ userSelect: "text" }}>{value}</span>
      <button className="ops-icon-btn" title="複製" onClick={() => copyText(value)}><SIcon name="doc.on.doc" size={12} /></button>
    </div>
  );
  return (
    <div className="col" style={{ gap: 16 }}>
      <div className="row gap8 h2"><SIcon name="chevron.left.forwardslash.chevron.right" size={20} />連上公司的 GitLab</div>
      <div className="muted">連上後,這裡會列出追蹤中專案的 Pipelines(含 jobs、stage 與失敗紀錄)和開著的 Merge Requests(含 pipeline 與核准狀態)。omixor 只讀,不會開 MR、留言或重跑 pipeline。</div>
      {!conn ? (
        <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy} onClick={() => run(() => action("gitlab_ensure_connection"))}>開始設定</button>
      ) : (
        <>
          <InsecureBanner gl={gl} />
          <ServerCard gl={gl} />
          {gl.configured && (conn.auth === "oauth" ? (
            !conn.oauth?.clientId ? (
              <Card>
                <div className="card-title">2. 在 GitLab 建一個應用程式(只要做一次)</div>
                <div className="muted">打開 GitLab 的「使用者設定 › 應用程式」(管理員也可以在 Admin › Applications 建給全公司用),新增應用程式,照下面填:</div>
                {copyRow("Name", "omixor")}
                {copyRow("Redirect URI", gl.callback)}
                {copyRow("Scopes", gl.scopes)}
                <div className="caption muted">「Confidential」不要勾(omixor 是桌面程式,用 PKCE);若公司規定要勾,建好後把 Secret 也貼到下面。omixor 登入時回呼網址會帶一個隨機連接埠(http://127.0.0.1:&lt;埠&gt;/callback);如果 GitLab 回報 redirect URI 不符,代表這版 GitLab 對本機回呼也比對連接埠,請改用個人存取權杖。</div>
                <button className="btn" style={{ alignSelf: "flex-start" }} disabled={!gl.applicationsURL} onClick={() => openURL(gl.applicationsURL)}>打開 GitLab 的應用程式頁</button>
                <div className="divider" />
                <div className="card-title">3. 把 Application ID 貼到這裡</div>
                <input className="input" placeholder="Application ID" value={clientId} onChange={(e) => setClientId(e.target.value)} />
                <input className="input" type="password" placeholder="Secret(選填,存在鑰匙圈)" value={secret} onChange={(e) => setSecret(e.target.value)} />
                <button className="btn primary" style={{ alignSelf: "flex-start" }} disabled={busy || !clientId.trim()} onClick={() => run(async () => {
                  await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: clientId.trim() } });
                  if (secret.trim()) await action("connection_secret", { id: conn.id, secret: secret.trim() });
                  setSecret("");
                  await action("connection_login", { id: conn.id });
                })}>儲存並用瀏覽器登入</button>
              </Card>
            ) : (
              <Card>
                <div className="card-title">用瀏覽器登入 GitLab</div>
                <div className="muted">會打開 {gl.baseURL} 的授權頁,按 Authorize 後回到 omixor 就完成了。權限:{gl.scopes}。</div>
                <div className="row gap8">
                  <button className="btn primary" disabled={busy || !!st?.busy} onClick={() => run(() => action("connection_login", { id: conn.id }))}>用瀏覽器登入</button>
                  {(busy || st?.busy) && <><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /><span className="caption">等瀏覽器授權…</span></>}
                  <span className="grow" />
                  <button className="btn link" onClick={() => run(async () => {
                    await action("connection_upsert", { ...conn, oauth: { ...conn.oauth, clientId: "" } });
                    await action("connection_secret", { id: conn.id, secret: "" });
                    setClientId("");
                  })}>重新設定應用程式</button>
                </div>
              </Card>
            )
          ) : <TokenStep gl={gl} conn={conn} />)}
          {(st?.error || error) && <Banner>{st?.error || error}</Banner>}
          {gl.configured && conn.auth === "oauth" && (
            <details className="ops-disclosure" open={useToken} onToggle={(e) => setUseToken((e.target as HTMLDetailsElement).open)}>
              <summary>改用個人存取權杖</summary>
              <div style={{ paddingTop: 6 }}><TokenStep gl={gl} conn={conn} /></div>
            </details>
          )}
        </>
      )}
    </div>
  );
}

function TokenStep({ gl, conn }: { gl: GitLabView; conn: Connection }) {
  const [token, setToken] = useState("");
  const { busy, error, run } = useRun();
  return (
    <div className="col gap8">
      <div className="caption muted">在 GitLab 的「使用者設定 › 個人存取權杖」建一個 token,範圍只勾 read_api,設好到期日。</div>
      {gl.tokenURL && <button className="btn" style={{ alignSelf: "flex-start" }} onClick={() => openURL(gl.tokenURL)}>打開建立權杖頁(已帶好 read_api)</button>}
      <div className="row gap8">
        <input className="input grow" type="password" placeholder="glpat-…(存在鑰匙圈)" value={token} onChange={(e) => setToken(e.target.value)} />
        <button className="btn" disabled={busy || !token.trim()} onClick={() => run(async () => {
          if (conn.auth !== "bearer") await action("connection_upsert", { ...conn, auth: "bearer" });
          await action("connection_bearer", { id: conn.id, token });
          setToken("");
        })}>使用這個 token</button>
      </div>
      {conn.auth === "bearer" && <button className="btn link" style={{ alignSelf: "flex-start" }} onClick={() => run(() => action("connection_upsert", { ...conn, auth: "oauth" }))}>改回 GitLab OAuth 登入</button>}
      {error && <Banner>{error}</Banner>}
    </div>
  );
}

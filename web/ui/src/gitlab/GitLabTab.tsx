// GitLab(公司自架)的資料型別、狀態樣式與詳情元件(Pipeline 的 jobs / stage / 失敗原因與紀錄、MR 的 head pipeline、核准與變更檔案)。只讀。
// 原本是開發進度 › GitLab 分頁(Swift 版沒有);已併進 開發進度 › Repositories(sections/development/DevReposTab.tsx),
// 這裡只留給 Repositories 的 repo 詳情與 設定 › 程式碼倉庫(./gitlabSettings)用的部分。
// 資料:view gitlab;動作:gitlab_jobs、gitlab_job_log、gitlab_mr(見 docs/PORTING.md)。
import { useEffect, useState } from "react";
import { action } from "../api";
import { Card, Pill } from "../components/ui";
import { Banner, MarkdownText, SIcon } from "../components/opsKit";
import { relative } from "../fmt";

type Connection = {
  id: string; name: string; baseURL: string; auth: "oauth" | "approval" | "bearer" | "basic" | "none";
  caCert?: string; insecure?: boolean;
  oauth: { clientId: string; hasClientSecret: boolean; [k: string]: unknown }; [k: string]: unknown;
};
type ConnState = { account?: string | null; busy?: boolean; error?: string | null; lastTest?: string | null };
export type GLProject = { id: number; key: string; name: string; path: string; description: string; defaultBranch: string; url: string; visibility: string; lastActivityAt?: string };
export type GLApproval = { approved: boolean; required: number; left: number; approvedBy: string[] };
export type GLMR = {
  project: string; id: number; iid: number; title: string; author: string; authorName: string; sourceBranch: string; targetBranch: string;
  draft: boolean; labels: string[]; reviewers: string[]; createdAt?: string; updatedAt?: string; url: string; sha: string;
  mergeStatus: string; hasConflicts: boolean; pipeline?: { id: number; status: string; url: string } | null; approval?: GLApproval | null;
};
export type GLPipeline = {
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
export const statusLabel = (s?: string) => (s ? LABELS[s] ?? s : "沒有 pipeline");
export function statusIcon(s?: string, allowFailure?: boolean): [string, string] {
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

export function ApprovalMark({ a }: { a?: GLApproval | null }) {
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

export function PipelineDetail({ p, projectName }: { p: GLPipeline; projectName: string }) {
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

export function MRDetail({ m }: { m: GLMR }) {
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

// GitHub 的資料型別、狀態樣式與詳情元件(Actions 執行的 jobs / 步驟 / 失敗紀錄、PR 的檢查與變更檔案)。只讀。
// 原本是開發進度 › GitHub 分頁(Swift 的 DevGitHubTab.swift);GitHub 分頁已併進 開發進度 › Repositories(DevReposTab.tsx),
// 這裡只留給 Repositories 的 repo 詳情與 設定 › 程式碼倉庫 用的部分。
// 資料:view github;動作:github_jobs、github_job_log、github_pull(見 omixor 文件櫃 DOC-32《console/docs/PORTING.md》)。
import { useEffect, useState } from "react";
import { action } from "../../api";
import { Banner } from "../../components/Callout";
import { Card, cardTitleClass } from "../../components/Card";
import { MarkdownText } from "../../components/Markdown";
import { Pill } from "../../components/Pill";
import { SIcon } from "../../components/SIcon";
import { relative } from "../../fmt";
import ds from "./devShared.module.css";
import css from "./DevGitHubTab.module.css";

export type GHChecks = { total: number; success: number; failure: number; pending: number; skipped: number };
export type GHPull = { repo: string; number: number; title: string; author: string; branch: string; base: string; draft: boolean; labels: string[]; reviewers: string[]; headSHA: string; createdAt?: string; updatedAt?: string; url: string; checks?: GHChecks | null };
export type GHRun = { id: number; repo: string; workflow: string; title: string; branch: string; event: string; status: string; conclusion?: string | null; sha: string; actor: string; runNumber: number; attempt: number; createdAt?: string; startedAt?: string; updatedAt?: string; url: string };
type GHStep = { number: number; name: string; status: string; conclusion?: string | null };
type GHJob = { id: number; name: string; status: string; conclusion?: string | null; startedAt?: string; completedAt?: string; url: string; steps: GHStep[] };
type GHFile = { filename: string; status: string; additions: number; deletions: number };
type GHPullDetail = { pull: GHPull; body: string; additions: number; deletions: number; changedFiles: number; mergeableState: string; files: GHFile[] };
export type Connection = { id: string; name: string; baseURL: string; isProduction?: boolean; auth: "oauth" | "approval" | "bearer" | "basic" | "none"; oauth: { clientId: string; hasClientSecret: boolean; [k: string]: unknown }; [k: string]: unknown };
export type ConnState = { account?: string | null; busy?: boolean; error?: string | null; lastTest?: string | null };
export type GitHubView = {
  settings: { repos: string[]; connection: string };
  snapshot: { repos: unknown[]; pulls: GHPull[]; runs: GHRun[]; fetchedAt?: string | null; busy: boolean; errors: Record<string, string>; error?: string | null };
  connection: Connection | null; loggedIn: boolean; state: ConnState | null;
  /** 讀寫組織設定(核心)的錯誤;追蹤的 repo 是組織的設定,token 留在這台 */
  settingsError?: string;
};

// MARK: - 狀態樣式(GHStyle)

export const isFailure = (r: GHRun) => ["failure", "timed_out", "startup_failure"].includes(r.conclusion ?? "");
export const isRunning = (r: GHRun) => r.status !== "completed";
export function runIcon(r: GHRun): [string, string] {
  if (isRunning(r)) return ["clock.arrow.circlepath", "orange"];
  switch (r.conclusion ?? "") {
    case "success": return ["checkmark.circle.fill", "green"];
    case "cancelled": case "skipped": return ["slash.circle", "secondary"];
    default: return isFailure(r) ? ["xmark.circle.fill", "red"] : ["questionmark.circle", "secondary"];
  }
}
function stepIcon(status: string, conclusion?: string | null): [string, string] {
  if (status !== "completed") return ["clock", "orange"];
  switch (conclusion ?? "") {
    case "success": return ["checkmark.circle", "green"];
    case "skipped": case "cancelled": return ["slash.circle", "secondary"];
    default: return ["xmark.circle.fill", "red"];
  }
}
const checksState = (c?: GHChecks | null) => (!c ? undefined : c.failure > 0 ? "failure" : c.pending > 0 ? "pending" : c.total === 0 ? "none" : "success");
export function checksIcon(c?: GHChecks | null): [string, string] {
  switch (checksState(c)) {
    case "failure": return ["xmark.circle.fill", "red"];
    case "pending": return ["clock", "orange"];
    case "success": return ["checkmark.circle.fill", "green"];
    default: return ["circle.dashed", "secondary"];
  }
}
export function checksLabel(c?: GHChecks | null) {
  switch (checksState(c)) {
    case "failure": return `${c!.failure} 個失敗`;
    case "pending": return `${c!.pending} 個執行中`;
    case "success": return `${c!.success} 個通過`;
    default: return "沒有檢查";
  }
}
function stateLabel(r: GHRun) {
  if (isRunning(r)) return ["queued", "waiting", "pending"].includes(r.status) ? "排隊中" : "執行中";
  return ({ success: "成功", failure: "失敗", cancelled: "已取消", skipped: "略過", timed_out: "逾時", action_required: "待處理" } as Record<string, string>)[r.conclusion ?? ""] ?? r.conclusion ?? r.status;
}
const ghDuration = (sec?: number) => (sec == null || isNaN(sec) ? undefined : sec < 60 ? `${Math.trunc(sec)} 秒` : `${Math.trunc(sec / 60)} 分 ${Math.trunc(sec % 60)} 秒`);
function runDuration(r: GHRun) {
  const s = r.startedAt ?? r.createdAt;
  if (isRunning(r) || !s || !r.updatedAt) return undefined;
  return (Date.parse(r.updatedAt) - Date.parse(s)) / 1000;
}
export const eventLabel = (e: string) => ({ push: "push", pull_request: "PR", schedule: "排程", workflow_dispatch: "手動" } as Record<string, string>)[e] ?? e;
const openURL = (u: string) => window.open(u, "_blank", "noopener");

export function RunDetail({ run: r }: { run: GHRun }) {
  const [jobs, setJobs] = useState<GHJob[]>();
  const [error, setError] = useState<string>();
  const [logJob, setLogJob] = useState<number>();
  const [log, setLog] = useState<string>();
  const [logError, setLogError] = useState<string>();
  useEffect(() => {
    action<GHJob[]>("github_jobs", { repo: r.repo, run: r.id }).then((j) => { setJobs(Array.isArray(j) ? j : []); setError(undefined); })
      .catch((e) => setError(`讀不到工作:${e.message ?? e}`));
  }, [r.id, r.repo]);
  const loadLog = (id: number) => {
    setLogJob(id); setLog(undefined); setLogError(undefined);
    action<string | { text: string }>("github_job_log", { repo: r.repo, job: id })
      .then((x) => setLog(typeof x === "string" ? x : x?.text ?? JSON.stringify(x)))
      .catch((e) => setLogError(`讀不到紀錄:${e.message ?? e}`));
  };
  const [icon, c] = runIcon(r);
  const d = ghDuration(runDuration(r));
  return (
    <div className={"col " + css.detail}>
      <div className={css.baseline}>
        <span className={"h3 " + ds.selectable}>{r.title}</span><span className="grow" />
        {r.url && <button className="btn" onClick={() => openURL(r.url)}>在 GitHub 打開</button>}
      </div>
      <div className="row gap8 wrap">
        <Pill text={stateLabel(r)} color={c} icon={icon} />
        <Pill text={`${r.workflow} #${r.runNumber}` + (r.attempt > 1 ? `(第 ${r.attempt} 次)` : "")} icon="gearshape.2" />
        <Pill text={r.branch} icon="arrow.triangle.branch" />
        <span className="caption mono muted">{r.sha.slice(0, 7)}</span>
        <span className="caption muted">{r.actor}</span>
        {d && <span className="caption muted">{d}</span>}
        {r.createdAt && <span className="caption muted">{relative(r.createdAt)}</span>}
      </div>
      <div className="divider" />
      {error && <Banner>{error}</Banner>}
      {jobs ? jobs.map((j) => {
        const [ji, jc] = stepIcon(j.status, j.conclusion);
        const failed = j.status === "completed" && !["success", "skipped", "cancelled"].includes(j.conclusion ?? "");
        const jd = j.startedAt && j.completedAt ? ghDuration((Date.parse(j.completedAt) - Date.parse(j.startedAt)) / 1000) : undefined;
        return (
          <Card key={j.id}>
            <div className="row gap8">
              <SIcon name={ji} color={jc} /><span className={cardTitleClass}>{j.name}</span>{jd && <span className="caption muted">{jd}</span>}
              <span className="grow" />
              {failed && <button className="btn" onClick={() => loadLog(j.id)}>{logJob === j.id && log != null ? "重新讀紀錄" : "看紀錄最後幾行"}</button>}
            </div>
            {(j.steps ?? []).map((s) => {
              const [si, sc] = stepIcon(s.status, s.conclusion);
              return <div key={s.number} className="row gap6"><SIcon name={si} color={sc} size={12} /><span className={(s.conclusion === "skipped" ? ds.secondary : "") + (sc === "red" ? " " + ds.bold : "")}>{s.name}</span></div>;
            })}
            {logJob === j.id && (
              <>
                {logError && <div className={"caption " + ds.orange}>{logError}</div>}
                {log != null ? <pre className={css.log}>{log}</pre> : !logError && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
              </>
            )}
          </Card>
        );
      }) : !error && <div className={"row " + css.center}><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /></div>}
    </div>
  );
}

export function PullDetail({ pull: p }: { pull: GHPull }) {
  const [detail, setDetail] = useState<GHPullDetail>();
  const [error, setError] = useState<string>();
  useEffect(() => {
    action<GHPullDetail>("github_pull", { repo: p.repo, number: p.number }).then(setDetail).catch((e) => setError(`讀不到 PR:${e.message ?? e}`));
  }, [p.repo, p.number]);
  const checks = detail?.pull?.checks ?? p.checks;
  const [icon, c] = checksIcon(checks);
  return (
    <div className={"col " + css.detail}>
      <div className={css.baseline + " gap8"}>
        <span className={"h3 " + ds.selectable}>{p.title}</span><span className={"muted " + css.number}>#{p.number}</span><span className="grow" />
        {p.url && <button className="btn" onClick={() => openURL(p.url)}>在 GitHub 打開</button>}
      </div>
      <div className="row gap8 wrap">
        <Pill text={checks ? checksLabel(checks) : "沒有檢查"} color={c} icon={icon} />
        {p.draft && <Pill text="草稿" />}
        <Pill text={`${p.branch} → ${p.base}`} icon="arrow.triangle.branch" />
        <span className="caption muted">{p.author}</span>
        {(p.labels ?? []).map((l) => <Pill key={l} text={l} icon="tag" />)}
        {detail && <span className="caption muted num">+{detail.additions} −{detail.deletions} · {detail.changedFiles} 個檔案</span>}
      </div>
      {p.reviewers?.length > 0 && <div className="caption muted">等待審查:{p.reviewers.join("、")}</div>}
      <div className="divider" />
      {error && <Banner>{error}</Banner>}
      {detail ? (
        <>
          {detail.body ? <MarkdownText source={detail.body} /> : <div className="muted">沒有說明</div>}
          <Card title="變更的檔案">
            {(detail.files ?? []).map((f) => (
              <div key={f.filename} className="row gap8">
                <span className="caption mono ellipsis grow">{f.filename}</span>
                <span className={"caption num " + ds.green}>+{f.additions}</span>
                <span className={"caption num " + ds.red}>−{f.deletions}</span>
              </div>
            ))}
          </Card>
        </>
      ) : !error && <div className={"row " + css.center}><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /></div>}
    </div>
  );
}

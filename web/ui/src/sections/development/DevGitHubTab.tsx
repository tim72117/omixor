// 開發進度 › GitHub:追蹤中 repo 的真實 PR(含檢查)與 Actions 執行。只讀,只看資料。Swift 的 DevGitHubTab.swift。
// 資料:view github;動作:github_refresh、github_jobs、github_job_log、github_pull(見 docs/PORTING.md)。
// 連線、登入與追蹤的 repo 在 設定 › 程式碼倉庫(sections/settings/SettingsRepos.tsx);這裡沒設定好時只顯示空狀態與「前往設定」。
import { useEffect, useState } from "react";
import { action, useView } from "../../api";
import { Card, EmptyState, Pill } from "../../components/ui";
import { Banner, Check, MarkdownText, Seg, SIcon, useAct } from "../../components/opsKit";
import { relative } from "../../fmt";
import { ManageReposButton, RepoSetupEmpty } from "./devShared";

type GHChecks = { total: number; success: number; failure: number; pending: number; skipped: number };
type GHPull = { repo: string; number: number; title: string; author: string; branch: string; base: string; draft: boolean; labels: string[]; reviewers: string[]; headSHA: string; createdAt?: string; updatedAt?: string; url: string; checks?: GHChecks | null };
type GHRun = { id: number; repo: string; workflow: string; title: string; branch: string; event: string; status: string; conclusion?: string | null; sha: string; actor: string; runNumber: number; attempt: number; createdAt?: string; startedAt?: string; updatedAt?: string; url: string };
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

const isFailure = (r: GHRun) => ["failure", "timed_out", "startup_failure"].includes(r.conclusion ?? "");
const isRunning = (r: GHRun) => r.status !== "completed";
function runIcon(r: GHRun): [string, string] {
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
function checksIcon(c?: GHChecks | null): [string, string] {
  switch (checksState(c)) {
    case "failure": return ["xmark.circle.fill", "red"];
    case "pending": return ["clock", "orange"];
    case "success": return ["checkmark.circle.fill", "green"];
    default: return ["circle.dashed", "secondary"];
  }
}
function checksLabel(c?: GHChecks | null) {
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
const eventLabel = (e: string) => ({ push: "push", pull_request: "PR", schedule: "排程", workflow_dispatch: "手動" } as Record<string, string>)[e] ?? e;
const openURL = (u: string) => window.open(u, "_blank", "noopener");

export function DevGitHubTab() {
  const [gh, err] = useView<GitHubView>("github", ["connections"]);
  const refresh = () => action("github_refresh").catch(() => undefined);
  useEffect(() => {
    if (gh?.loggedIn && gh.connection && !gh.snapshot?.fetchedAt && !gh.snapshot?.busy) refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gh?.loggedIn, gh?.state?.account]);
  if (err && !gh) return <EmptyState title="讀不到 GitHub 資料" icon="chevron.left.forwardslash.chevron.right" message={err} />;
  if (!gh) return <EmptyState title="讀取中…" icon="chevron.left.forwardslash.chevron.right" />;
  if (!gh.connection || !gh.loggedIn) {
    return <RepoSetupEmpty title={!gh.connection ? "還沒設定 GitHub" : "還沒登入 GitHub"} message="GitHub 的連線、登入與要追蹤的 repo 在 設定 › 程式碼倉庫 設定。" />;
  }
  return <GitHubMain gh={gh} />;
}

function GitHubMain({ gh }: { gh: GitHubView }) {
  const [mode, setMode] = useState<"runs" | "pulls">("runs");
  const [selectedRun, setSelectedRun] = useState<number>();
  const [selectedPull, setSelectedPull] = useState<string>();
  const [failedOnly, setFailedOnly] = useState(false);
  const [rawRepoFilter, setRepoFilter] = useState("");
  const { run, toastEl } = useAct();
  const snap = gh.snapshot ?? { repos: [], pulls: [], runs: [], busy: false, errors: {} };
  const repos = gh.settings?.repos ?? [];
  // 在設定頁停止追蹤後,篩選值可能已不在清單裡,當成「全部 repo」
  const repoFilter = repos.includes(rawRepoFilter) ? rawRepoFilter : "";
  const runs = (snap.runs ?? []).filter((r) => (!repoFilter || r.repo === repoFilter) && (!failedOnly || isFailure(r)));
  const pulls = (snap.pulls ?? []).filter((p) => !repoFilter || p.repo === repoFilter);
  const errs = Object.entries(snap.errors ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const selRun = snap.runs?.find((r) => r.id === selectedRun);
  const selPull = snap.pulls?.find((p) => `${p.repo}#${p.number}` === selectedPull);
  return (
    <div className="ops-page">
      <div className="row gap8 wrap" style={{ padding: "8px 16px", gap: 10 }}>
        <SIcon name="person.crop.circle.badge.checkmark" color="green" />
        <span>{gh.state?.account ?? "已登入"}</span>
        <span style={{ width: 1, height: 16, background: "var(--line)" }} />
        <span className="caption muted">追蹤 {repos.length} 個 repo</span>
        {gh.settingsError && <span className="caption ellipsis" style={{ color: "var(--orange)" }} title={gh.settingsError}>組織設定:{gh.settingsError}</span>}
        <span className="grow" />
        {snap.fetchedAt && <span className="caption muted">更新於 {relative(snap.fetchedAt)}</span>}
        {snap.busy && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
        <ManageReposButton />
        <button className="btn" title="重新整理" disabled={snap.busy} onClick={() => run(() => action("github_refresh"))}><SIcon name="arrow.clockwise" /></button>
      </div>
      {(errs.length > 0 || snap.error) && (
        <div className="col" style={{ background: "color-mix(in srgb, var(--orange) 8%, transparent)" }}>
          {snap.error && <Banner>{snap.error}</Banner>}
          {errs.map(([r, e]) => <Banner key={r}>{r}:{e}</Banner>)}
        </div>
      )}
      <div className="divider" />
      {repos.length === 0 ? (
        <RepoSetupEmpty title="還沒追蹤任何 repo" icon="shippingbox" message="要追蹤的 repo 在 設定 › 程式碼倉庫 › GitHub 加入。" />
      ) : (
        <div className="ops-fill">
          <div className="col" style={{ width: 440, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
            <div className="row gap8" style={{ padding: 10 }}>
              <div className="grow"><Seg value={mode} onChange={setMode} options={[{ value: "runs", label: `Actions(${snap.runs?.length ?? 0})` }, { value: "pulls", label: `Pull Requests(${snap.pulls?.length ?? 0})` }]} /></div>
              {repos.length > 1 && (
                <select className="select" style={{ minWidth: 130 }} value={repoFilter} onChange={(e) => setRepoFilter(e.target.value)}>
                  <option value="">全部 repo</option>{repos.map((r) => <option key={r} value={r}>{r}</option>)}
                </select>
              )}
            </div>
            {mode === "runs" && <div style={{ padding: "0 12px 6px" }}><Check on={failedOnly} onChange={setFailedOnly}><span className="caption">只看失敗</span></Check></div>}
            <div className="divider" />
            {mode === "runs" ? (
              runs.length === 0 ? <EmptyState title={snap.fetchedAt ? "沒有執行紀錄" : "讀取中…"} icon="arrow.triangle.branch" /> : (
                <div className="ops-list">
                  {runs.map((r) => {
                    const [icon, c] = runIcon(r);
                    return (
                      <div key={r.id} className={"ops-list-row row gap8" + (r.id === selectedRun ? " on" : "")} style={{ alignItems: "flex-start" }} onClick={() => setSelectedRun(r.id)}>
                        <SIcon name={icon} color={c} style={{ marginTop: 2 }} />
                        <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                          <span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{r.title}</span>
                          <div className="row gap6 caption muted">
                            <span>{r.workflow} #{r.runNumber}</span><span className="mono ellipsis">{r.branch}</span><span>{eventLabel(r.event)}</span>
                            {repos.length > 1 && <span className="faint ellipsis">{r.repo}</span>}
                            <span className="grow" />{r.createdAt && <span style={{ flex: "none" }}>{relative(r.createdAt)}</span>}
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )
            ) : pulls.length === 0 ? <EmptyState title={snap.fetchedAt ? "沒有開著的 PR" : "讀取中…"} icon="arrow.triangle.pull" /> : (
              <div className="ops-list">
                {pulls.map((p) => {
                  const id = `${p.repo}#${p.number}`;
                  const [icon, c] = checksIcon(p.checks);
                  return (
                    <div key={id} className={"ops-list-row row gap8" + (id === selectedPull ? " on" : "")} style={{ alignItems: "flex-start" }} onClick={() => setSelectedPull(id)}>
                      <SIcon name="arrow.triangle.pull" color={p.draft ? "secondary" : "green"} style={{ marginTop: 2 }} />
                      <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                        <span className="row gap4"><span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{p.title}</span>{p.draft && <Pill text="草稿" />}</span>
                        <div className="row gap6 caption muted">
                          <span className="mono">#{p.number}</span><span>{p.author}</span><span className="mono ellipsis">{p.branch}</span>
                          <span title={checksLabel(p.checks)}><SIcon name={icon} color={c} size={12} /></span>
                          <span className="grow" />{p.updatedAt && <span style={{ flex: "none" }}>{relative(p.updatedAt)}</span>}
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
          <div className="ops-scroll">
            {mode === "runs" && selRun ? <RunDetail key={selRun.id} run={selRun} />
              : mode === "pulls" && selPull ? <PullDetail key={selectedPull} pull={selPull} />
                : <EmptyState title={mode === "runs" ? "選一筆執行" : "選一個 PR"} icon="arrow.triangle.branch" message="資料每 5 分鐘自動更新,也可以按右上角重新整理。" />}
          </div>
        </div>
      )}
      {toastEl}
    </div>
  );
}

function RunDetail({ run: r }: { run: GHRun }) {
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
    <div className="col" style={{ padding: 20, gap: 12, maxWidth: 820 }}>
      <div className="row" style={{ alignItems: "baseline" }}>
        <span className="h3" style={{ userSelect: "text" }}>{r.title}</span><span className="grow" />
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
              <SIcon name={ji} color={jc} /><span className="card-title">{j.name}</span>{jd && <span className="caption muted">{jd}</span>}
              <span className="grow" />
              {failed && <button className="btn" onClick={() => loadLog(j.id)}>{logJob === j.id && log != null ? "重新讀紀錄" : "看紀錄最後幾行"}</button>}
            </div>
            {(j.steps ?? []).map((s) => {
              const [si, sc] = stepIcon(s.status, s.conclusion);
              return <div key={s.number} className="row gap6"><SIcon name={si} color={sc} size={12} /><span style={{ color: s.conclusion === "skipped" ? "var(--fg2)" : undefined, fontWeight: sc === "red" ? 600 : undefined }}>{s.name}</span></div>;
            })}
            {logJob === j.id && (
              <>
                {logError && <div className="caption" style={{ color: "var(--orange)" }}>{logError}</div>}
                {log != null ? <pre className="ops-code" style={{ fontSize: 11, userSelect: "text" }}>{log}</pre> : !logError && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
              </>
            )}
          </Card>
        );
      }) : !error && <div className="row" style={{ justifyContent: "center" }}><SIcon name="arrow.triangle.2.circlepath" spin className="muted" /></div>}
    </div>
  );
}

function PullDetail({ pull: p }: { pull: GHPull }) {
  const [detail, setDetail] = useState<GHPullDetail>();
  const [error, setError] = useState<string>();
  useEffect(() => {
    action<GHPullDetail>("github_pull", { repo: p.repo, number: p.number }).then(setDetail).catch((e) => setError(`讀不到 PR:${e.message ?? e}`));
  }, [p.repo, p.number]);
  const checks = detail?.pull?.checks ?? p.checks;
  const [icon, c] = checksIcon(checks);
  return (
    <div className="col" style={{ padding: 20, gap: 12, maxWidth: 820 }}>
      <div className="row gap8" style={{ alignItems: "baseline" }}>
        <span className="h3" style={{ userSelect: "text" }}>{p.title}</span><span className="muted" style={{ fontSize: 15 }}>#{p.number}</span><span className="grow" />
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

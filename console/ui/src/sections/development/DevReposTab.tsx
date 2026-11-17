// 開發進度 › Repositories:組織追蹤的 GitHub repo 與 GitLab 專案(用標籤標平台),點進去看該 repo 的 PR/MR 與 Actions/Pipeline
// (執行的 jobs、失敗的步驟或原因、紀錄最後幾行)。只讀,只看資料。原本的 GitHub、GitLab 分頁併進這裡。
// 功能 development.repos 屬於選用模組 integrations:後端沒編進模組時清單沒有這個 key,分頁不顯示(所以不需要範例資料版)。
// 資料:view code_repos(清單與兩個平台的連線狀態)、view github / gitlab(單一 repo 的 PR/MR、執行;依 repo 篩);
// 動作:code_repos_refresh、github_refresh、gitlab_refresh、github_jobs / github_job_log / github_pull、gitlab_jobs / gitlab_job_log / gitlab_mr。
// 連線、登入與追蹤的 repo / 專案在 設定 › 程式碼倉庫;沒設定或沒登入時只顯示空狀態與「前往設定」。
import { useEffect, useState } from "react";
import { action, useView } from "../../api";
import { EmptyState, Pill } from "../../components/ui";
import { Banner, Check, Seg, SIcon, useAct } from "../../components/omixorKit";
import { relative } from "../../fmt";
import { ManageReposButton, RepoSetupEmpty, useDev } from "./devShared";
import { checksIcon, checksLabel, eventLabel, isFailure, PullDetail, runIcon, RunDetail, type GitHubView } from "./DevGitHubTab";
import { ApprovalMark, MRDetail, PipelineDetail, statusIcon, statusLabel, type GitLabView } from "../../gitlab/GitLabTab";

type Platform = "github" | "gitlab";
type BuildState = "success" | "failure" | "running" | "canceled" | "other";
type CodeRepoBuild = { id: number; state: BuildState; status: string; label: string; title: string; branch: string; sha: string; url: string; at?: string };
type CodeRepo = {
  platform: Platform; key: string; name: string; description: string; url: string; defaultBranch: string; visibility: string;
  loaded: boolean; error?: string; openPulls: number; failingPulls: number; failedRuns: number; lastBuild: CodeRepoBuild | null; activityAt?: string;
};
type CodePlatform = { configured: boolean; loggedIn: boolean; account?: string; error?: string; settingsError?: string; fetchedAt?: string; busy: boolean; tracked: number; baseURL?: string; insecure?: boolean };
type CodeReposView = { github: CodePlatform; gitlab: CodePlatform; repos: CodeRepo[] };

const PLATFORM: Record<Platform, { label: string; color: string }> = {
  github: { label: "GitHub", color: "indigo" },
  gitlab: { label: "GitLab", color: "orange" },
};
const repoId = (r: CodeRepo) => `${r.platform}:${r.key}`;
const VISIBILITY: Record<string, string> = { public: "公開", private: "私有", internal: "內部" };

function buildIcon(s?: BuildState): [string, string] {
  switch (s) {
    case "success": return ["checkmark.circle.fill", "green"];
    case "failure": return ["xmark.circle.fill", "red"];
    case "running": return ["clock.arrow.circlepath", "orange"];
    case "canceled": return ["slash.circle", "secondary"];
    case "other": return ["questionmark.circle", "secondary"];
    default: return ["circle.dashed", "secondary"];
  }
}

const openURL = (u: string) => u && window.open(u, "_blank", "noopener");

export function PlatformTag({ platform }: { platform: Platform }) {
  const m = PLATFORM[platform];
  return <Pill text={m.label} color={m.color} />;
}

export function DevReposTab() {
  const [v, err] = useView<CodeReposView>("code_repos", ["github", "gitlab", "connections"]);
  // 第一次打開、這一輪還沒讀過時,有登入的平台各自讀一次(同原本的 GitHub、GitLab 分頁)
  useEffect(() => {
    if (v?.github.loggedIn && !v.github.fetchedAt && !v.github.busy) action("github_refresh").catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [v?.github.loggedIn, v?.github.account]);
  useEffect(() => {
    if (v?.gitlab.loggedIn && !v.gitlab.fetchedAt && !v.gitlab.busy) action("gitlab_refresh").catch(() => undefined);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [v?.gitlab.loggedIn, v?.gitlab.account]);
  if (err && !v) return <EmptyState title="讀不到 Repositories" icon="shippingbox" message={err} />;
  if (!v) return <EmptyState title="讀取中…" icon="shippingbox" />;
  if (!v.github.loggedIn && !v.gitlab.loggedIn) {
    const configured = v.github.configured || v.gitlab.configured;
    return (
      <div className="col" style={{ flex: 1, minHeight: 0 }}>
        {v.gitlab.insecure && <div style={{ padding: "8px 16px 0" }}><InsecureNote /></div>}
        <RepoSetupEmpty title={configured ? "還沒登入 GitHub 或 GitLab" : "還沒設定 GitHub 或 GitLab"}
          message="GitHub、GitLab 的連線、登入與要追蹤的 repo / 專案在 設定 › 程式碼倉庫 設定。" />
      </div>
    );
  }
  return <ReposMain v={v} />;
}

function InsecureNote() {
  return <Banner tint="red">GitLab 目前「不驗證憑證」:連線可能被攔截,token 也可能外流。只能在測試環境用,請到 設定 › 程式碼倉庫 改成指定 CA 憑證檔。</Banner>;
}

// MARK: - 清單 + 詳情

function ReposMain({ v }: { v: CodeReposView }) {
  const dev = useDev();
  const [filter, setFilter] = useState<"all" | Platform>("all");
  const [query, setQuery] = useState("");
  const { run, toastEl } = useAct();
  const all = v.repos ?? [];
  const q = query.trim().toLowerCase();
  const rows = all.filter((r) => (filter === "all" || r.platform === filter) && (!q || r.name.toLowerCase().includes(q) || r.description?.toLowerCase().includes(q)));
  // 在設定頁停止追蹤後,選的 repo 可能已不在清單裡
  const selected = all.find((r) => repoId(r) === dev.selectedRepo);
  const busy = v.github.busy || v.gitlab.busy;
  const fetched = [v.github.fetchedAt, v.gitlab.fetchedAt].filter(Boolean).sort().pop();
  const both = v.github.tracked > 0 && v.gitlab.tracked > 0;
  return (
    <div className="ox-page">
      <div className="row gap8 wrap" style={{ padding: "8px 16px", gap: 10 }}>
        <PlatformStatus platform="github" p={v.github} />
        <span style={{ width: 1, height: 16, background: "var(--line)" }} />
        <PlatformStatus platform="gitlab" p={v.gitlab} />
        <span className="grow" />
        {fetched && <span className="caption muted">更新於 {relative(fetched)}</span>}
        {busy && <SIcon name="arrow.triangle.2.circlepath" spin className="muted" />}
        <ManageReposButton />
        <button className="btn" title="重新整理" disabled={busy} onClick={() => run(() => action("code_repos_refresh"))}><SIcon name="arrow.clockwise" /></button>
      </div>
      {v.gitlab.insecure && <div style={{ padding: "0 16px 6px" }}><InsecureNote /></div>}
      {(v.github.error || v.gitlab.error) && (
        <div className="col" style={{ background: "color-mix(in srgb, var(--orange) 8%, transparent)" }}>
          {v.github.error && <Banner>GitHub:{v.github.error}</Banner>}
          {v.gitlab.error && <Banner>GitLab:{v.gitlab.error}</Banner>}
        </div>
      )}
      <div className="divider" />
      {all.length === 0 ? (
        <RepoSetupEmpty title="還沒追蹤任何 repo" icon="shippingbox" message="要追蹤的 GitHub repo、GitLab 專案在 設定 › 程式碼倉庫 加入。" />
      ) : (
        <div className="ox-fill">
          <div className="col" style={{ width: 400, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}>
            <div className="col" style={{ padding: 10, gap: 8 }}>
              {both && <Seg value={filter} onChange={setFilter} options={[
                { value: "all", label: `全部(${all.length})` },
                { value: "github", label: `GitHub(${v.github.tracked})` },
                { value: "gitlab", label: `GitLab(${v.gitlab.tracked})` },
              ]} />}
              {all.length > 5 && <input className="input" placeholder="搜尋 repo" value={query} onChange={(e) => setQuery(e.target.value)} />}
            </div>
            <div className="divider" />
            {rows.length === 0 ? <EmptyState title="沒有符合的 repo" icon="shippingbox" /> : (
              <div className="ox-list">
                {rows.map((r) => <RepoRow key={repoId(r)} r={r} on={repoId(r) === dev.selectedRepo} onClick={() => dev.setSelectedRepo(repoId(r))} />)}
              </div>
            )}
          </div>
          <div className="ox-scroll">
            {selected ? <RepoDetail key={repoId(selected)} r={selected} p={selected.platform === "github" ? v.github : v.gitlab} />
              : <EmptyState title="選一個 repo" icon="shippingbox" message="看開著的 PR / MR 與 Actions / Pipeline 執行(含失敗原因)。資料每 5 分鐘自動更新,也可以按右上角重新整理。" />}
          </div>
        </div>
      )}
      {toastEl}
    </div>
  );
}

function PlatformStatus({ platform, p }: { platform: Platform; p: CodePlatform }) {
  const label = PLATFORM[platform].label;
  if (!p.configured || !p.loggedIn) {
    return <span className="row gap4 caption muted" title="在 設定 › 程式碼倉庫 設定"><SIcon name="person.crop.circle" />{label}:{p.configured ? "還沒登入" : "還沒設定"}</span>;
  }
  return (
    <span className="row gap4 caption" title={p.baseURL || undefined}>
      <SIcon name="person.crop.circle.badge.checkmark" color="green" />
      <span>{label}:{p.account ?? "已登入"}</span>
      <span className="muted">· 追蹤 {p.tracked} 個</span>
      {p.settingsError && <span className="ellipsis" style={{ color: "var(--orange)" }} title={p.settingsError}>(組織設定讀不到)</span>}
    </span>
  );
}

function RepoRow({ r, on, onClick }: { r: CodeRepo; on: boolean; onClick: () => void }) {
  const [icon, c] = buildIcon(r.lastBuild?.state);
  const pr = r.platform === "github" ? "PR" : "MR";
  return (
    <div className={"ox-list-row row gap8" + (on ? " on" : "")} style={{ alignItems: "flex-start" }} onClick={onClick}>
      <span title={r.lastBuild ? `${r.defaultBranch} 最近一次建置:${r.lastBuild.label}` : "預設分支還沒有建置"}><SIcon name={icon} color={c} style={{ marginTop: 2 }} /></span>
      <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
        <span className="row gap6"><span className="ellipsis" style={{ fontWeight: 600 }}>{r.name}</span><PlatformTag platform={r.platform} /></span>
        {r.error ? <span className="caption ellipsis" style={{ color: "var(--orange)" }} title={r.error}>{r.error}</span> : (
          <div className="row gap6 caption muted">
            {r.lastBuild ? <span style={{ color: r.lastBuild.state === "failure" ? "var(--red)" : undefined }}>{r.lastBuild.label}</span> : <span>{r.loaded ? "沒有建置" : "讀取中…"}</span>}
            {r.defaultBranch && <span className="mono ellipsis">{r.defaultBranch}</span>}
            <span>{r.openPulls} 個 {pr}{r.failingPulls > 0 && <span style={{ color: "var(--red)" }}>({r.failingPulls} 個失敗)</span>}</span>
            <span className="grow" />
            {r.lastBuild?.at && <span style={{ flex: "none" }}>{relative(r.lastBuild.at)}</span>}
          </div>
        )}
      </div>
    </div>
  );
}

// MARK: - 單一 repo

function RepoDetail({ r, p }: { r: CodeRepo; p: CodePlatform }) {
  const [bi, bc] = buildIcon(r.lastBuild?.state);
  const where = PLATFORM[r.platform].label;
  return (
    <div className="col" style={{ minHeight: "100%" }}>
      <div className="col" style={{ padding: "16px 20px 10px", gap: 8 }}>
        <div className="row gap8" style={{ alignItems: "baseline" }}>
          <span className="h3" style={{ userSelect: "text" }}>{r.name}</span>
          <PlatformTag platform={r.platform} />
          <span className="grow" />
          {r.url && <button className="btn" onClick={() => openURL(r.url)}>在 {where} 打開</button>}
        </div>
        {r.description && <div className="muted" style={{ userSelect: "text" }}>{r.description}</div>}
        <div className="row gap8 wrap">
          {r.lastBuild
            ? <span title={r.lastBuild.url ? `在 ${where} 打開` : undefined} style={{ cursor: r.lastBuild.url ? "pointer" : undefined }} onClick={() => r.lastBuild?.url && openURL(r.lastBuild.url)}>
              <Pill text={`${r.defaultBranch}:${r.lastBuild.label}`} color={bc} icon={bi} />
            </span>
            : r.loaded && <Pill text={`${r.defaultBranch || "預設分支"}:沒有建置`} icon="circle.dashed" />}
          {r.lastBuild && <span className="caption muted">{r.lastBuild.title}{r.lastBuild.at ? ` · ${relative(r.lastBuild.at)}` : ""}</span>}
          {r.visibility && <Pill text={VISIBILITY[r.visibility] ?? r.visibility} icon={r.visibility === "public" ? "globe" : "lock"} />}
          {r.activityAt && <span className="caption muted">最近活動 {relative(r.activityAt)}</span>}
        </div>
        {r.error && <Banner>{r.error}</Banner>}
      </div>
      <div className="divider" />
      {!p.configured || !p.loggedIn ? (
        <RepoSetupEmpty title={p.configured ? `這台還沒登入 ${where}` : `還沒設定 ${where}`} message={`${where} 的連線與登入在 設定 › 程式碼倉庫 設定。`} />
      ) : r.platform === "github" ? <GitHubRepoDetail repo={r.key} /> : <GitLabRepoDetail project={r.key} name={r.name} />}
    </div>
  );
}

/** 列表 ↔ 詳情:選了一筆時顯示詳情與「返回」 */
function BackBar({ label, onBack }: { label: string; onBack: () => void }) {
  return (
    <div className="row" style={{ padding: "8px 16px 0" }}>
      <button className="btn" onClick={onBack}><SIcon name="chevron.left" size={12} />{label}</button>
    </div>
  );
}

function GitHubRepoDetail({ repo }: { repo: string }) {
  const [gh, err] = useView<GitHubView>("github", ["connections"]);
  const [mode, setMode] = useState<"pulls" | "runs">("runs");
  const [failedOnly, setFailedOnly] = useState(false);
  const [selRun, setSelRun] = useState<number>();
  const [selPull, setSelPull] = useState<number>();
  if (!gh) return <EmptyState title={err ? "讀不到 GitHub 資料" : "讀取中…"} icon="arrow.triangle.branch" message={err} />;
  const snap = gh.snapshot ?? { pulls: [], runs: [] };
  const pulls = (snap.pulls ?? []).filter((x) => x.repo === repo);
  const allRuns = (snap.runs ?? []).filter((x) => x.repo === repo);
  const runs = allRuns.filter((x) => !failedOnly || isFailure(x));
  const run = selRun != null ? allRuns.find((x) => x.id === selRun) : undefined;
  const pull = selPull != null ? pulls.find((x) => x.number === selPull) : undefined;
  if (mode === "runs" && run) return <div className="col"><BackBar label="Actions" onBack={() => setSelRun(undefined)} /><RunDetail key={run.id} run={run} /></div>;
  if (mode === "pulls" && pull) return <div className="col"><BackBar label="Pull Requests" onBack={() => setSelPull(undefined)} /><PullDetail key={pull.number} pull={pull} /></div>;
  return (
    <div className="col">
      <div className="row gap8" style={{ padding: "10px 16px" }}>
        <Seg value={mode} onChange={setMode} options={[{ value: "runs", label: `Actions(${allRuns.length})` }, { value: "pulls", label: `Pull Requests(${pulls.length})` }]} />
        <span className="grow" />
        {mode === "runs" && <Check on={failedOnly} onChange={setFailedOnly}><span className="caption">只看失敗</span></Check>}
      </div>
      <div className="divider" />
      {mode === "runs" ? (
        runs.length === 0 ? <EmptyState title={snap.fetchedAt ? (failedOnly ? "沒有失敗的執行" : "沒有執行紀錄") : "讀取中…"} icon="arrow.triangle.branch" /> : (
          <div className="ox-list">
            {runs.map((x) => {
              const [icon, c] = runIcon(x);
              return (
                <div key={x.id} className="ox-list-row row gap8" style={{ alignItems: "flex-start" }} onClick={() => setSelRun(x.id)}>
                  <SIcon name={icon} color={c} style={{ marginTop: 2 }} />
                  <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                    <span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{x.title}</span>
                    <div className="row gap6 caption muted">
                      <span>{x.workflow} #{x.runNumber}</span><span className="mono ellipsis">{x.branch}</span><span>{eventLabel(x.event)}</span>
                      <span className="grow" />{x.createdAt && <span style={{ flex: "none" }}>{relative(x.createdAt)}</span>}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )
      ) : pulls.length === 0 ? <EmptyState title={snap.fetchedAt ? "沒有開著的 PR" : "讀取中…"} icon="arrow.triangle.pull" /> : (
        <div className="ox-list">
          {pulls.map((x) => {
            const [icon, c] = checksIcon(x.checks);
            return (
              <div key={x.number} className="ox-list-row row gap8" style={{ alignItems: "flex-start" }} onClick={() => setSelPull(x.number)}>
                <SIcon name="arrow.triangle.pull" color={x.draft ? "secondary" : "green"} style={{ marginTop: 2 }} />
                <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                  <span className="row gap4"><span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{x.title}</span>{x.draft && <Pill text="草稿" />}</span>
                  <div className="row gap6 caption muted">
                    <span className="mono">#{x.number}</span><span>{x.author}</span><span className="mono ellipsis">{x.branch}</span>
                    <span className="row gap4" title={checksLabel(x.checks)}><SIcon name={icon} color={c} size={12} />{checksLabel(x.checks)}</span>
                    <span className="grow" />{x.updatedAt && <span style={{ flex: "none" }}>{relative(x.updatedAt)}</span>}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function GitLabRepoDetail({ project, name }: { project: string; name: string }) {
  const [gl, err] = useView<GitLabView>("gitlab", ["connections"]);
  const [mode, setMode] = useState<"mrs" | "pipelines">("pipelines");
  const [failedOnly, setFailedOnly] = useState(false);
  const [selPipeline, setSelPipeline] = useState<number>();
  const [selMR, setSelMR] = useState<number>();
  if (!gl) return <EmptyState title={err ? "讀不到 GitLab 資料" : "讀取中…"} icon="arrow.triangle.branch" message={err} />;
  const snap = gl.snapshot ?? { mrs: [], pipelines: [] };
  const mrs = (snap.mrs ?? []).filter((x) => x.project === project);
  const allPipelines = (snap.pipelines ?? []).filter((x) => x.project === project);
  const pipelines = allPipelines.filter((x) => !failedOnly || x.isFailure);
  const pl = selPipeline != null ? allPipelines.find((x) => x.id === selPipeline) : undefined;
  const mr = selMR != null ? mrs.find((x) => x.iid === selMR) : undefined;
  if (mode === "pipelines" && pl) return <div className="col"><BackBar label="Pipelines" onBack={() => setSelPipeline(undefined)} /><PipelineDetail key={pl.id} p={pl} projectName={name} /></div>;
  if (mode === "mrs" && mr) return <div className="col"><BackBar label="Merge Requests" onBack={() => setSelMR(undefined)} /><MRDetail key={mr.iid} m={mr} /></div>;
  return (
    <div className="col">
      <div className="row gap8" style={{ padding: "10px 16px" }}>
        <Seg value={mode} onChange={setMode} options={[{ value: "pipelines", label: `Pipelines(${allPipelines.length})` }, { value: "mrs", label: `Merge Requests(${mrs.length})` }]} />
        <span className="grow" />
        {mode === "pipelines" && <Check on={failedOnly} onChange={setFailedOnly}><span className="caption">只看失敗</span></Check>}
      </div>
      <div className="divider" />
      {mode === "pipelines" ? (
        pipelines.length === 0 ? <EmptyState title={snap.fetchedAt ? (failedOnly ? "沒有失敗的 pipeline" : "沒有 pipeline") : "讀取中…"} icon="arrow.triangle.branch" /> : (
          <div className="ox-list">
            {pipelines.map((x) => {
              const [icon, c] = statusIcon(x.status);
              return (
                <div key={x.id} className="ox-list-row row gap8" style={{ alignItems: "flex-start" }} onClick={() => setSelPipeline(x.id)}>
                  <SIcon name={icon} color={c} style={{ marginTop: 2 }} />
                  <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                    <span className="row gap6"><span className="mono ellipsis">{x.ref}</span><span className="caption muted">#{x.iid || x.id}</span><span className="caption">{x.statusLabel}</span></span>
                    <div className="row gap6 caption muted">
                      <span>{x.sourceLabel}</span>{x.user && <span>{x.user}</span>}<span className="mono">{x.sha.slice(0, 8)}</span>
                      <span className="grow" />{x.createdAt && <span style={{ flex: "none" }}>{relative(x.createdAt)}</span>}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )
      ) : mrs.length === 0 ? <EmptyState title={snap.fetchedAt ? "沒有開著的 MR" : "讀取中…"} icon="arrow.triangle.pull" /> : (
        <div className="ox-list">
          {mrs.map((x) => {
            const [icon, c] = statusIcon(x.pipeline?.status);
            return (
              <div key={x.iid} className="ox-list-row row gap8" style={{ alignItems: "flex-start" }} onClick={() => setSelMR(x.iid)}>
                <SIcon name="arrow.triangle.pull" color={x.draft ? "secondary" : "green"} style={{ marginTop: 2 }} />
                <div className="col grow" style={{ gap: 3, minWidth: 0 }}>
                  <span className="row gap4"><span style={{ display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>{x.title}</span>{x.draft && <Pill text="草稿" />}</span>
                  <div className="row gap6 caption muted">
                    <span className="mono">!{x.iid}</span><span>{x.author}</span><span className="mono ellipsis">{x.sourceBranch}</span>
                    <span className="row gap4" title={`pipeline:${statusLabel(x.pipeline?.status)}`}><SIcon name={icon} color={c} size={12} />{statusLabel(x.pipeline?.status)}</span>
                    <ApprovalMark a={x.approval} />
                    <span className="grow" />{x.updatedAt && <span style={{ flex: "none" }}>{relative(x.updatedAt)}</span>}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

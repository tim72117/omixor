// CI/CD › 執行紀錄:左邊列表、右邊詳情(Swift 的 PipeRunsTab / PipeRunDetail / PipeStageGraph / PipeLogViewer)
import { useEffect, useState, type ReactNode } from "react";
import { action, tool } from "../../api";
import { Card, color, EmptyState, Modal } from "../../components/ui";
import {
  ActorAvatar, ActorLabel, AIBadge, Callout, Check, copyText, dur, ENV, EnvBadge, MenuButton, ProgressBar, RUN, RUN_ORDER, RunStatusBadge, SIcon,
  t, useAct, useConfirm, useContextMenu, useList, useOps, useWidth, type PipelineJob, type PipelineRun, type RunStatusKey,
} from "../../components/opsKit";
import { relative } from "../../fmt";
import { ElapsedText, focusJob, pipelineUpdate, runIsActive, runIsAI, runJobs, runProgress, shortSHA, stageDuration, StatusIcon } from "./pipeShared";

export function PipeRunsTab({ selected, setSelected }: { selected?: number; setSelected: (n?: number) => void }) {
  const o = useOps();
  const [runs, err] = useList<PipelineRun>("pipeline_runs");
  const [status, setStatus] = useState<RunStatusKey | "">("");
  const [repo, setRepo] = useState("");
  const [onlyFailed, setOnlyFailed] = useState(false);
  const [onlyAI, setOnlyAI] = useState(false);
  const [query, setQuery] = useState("");
  const [ref, w] = useWidth<HTMLDivElement>();
  const menu = useContextMenu();
  const { run: act, toastEl } = useAct();
  const q = query.trim().toLowerCase();
  const filtered = runs.filter((r) => {
    if (onlyFailed && r.status !== "failed") return false;
    if (onlyAI && !runIsAI(r)) return false;
    if (status && r.status !== status) return false;
    if (repo && r.repo !== repo) return false;
    if (q) return [r.pipeline, r.branch, r.commitMessage, r.commitSHA, `#${r.id}`, o.name(r.triggeredBy)].some((f) => f.toLowerCase().includes(q));
    return true;
  }).sort((a, b) => t(b.startedAt) - t(a.startedAt));
  const repoIds = [...new Set(runs.map((r) => r.repo))].sort();
  const hasFilters = !!status || !!repo || onlyFailed || onlyAI || !!q;
  const repoName = (id: string) => o.repos.find((r) => r.id === id)?.name ?? id;

  useEffect(() => {
    if (selected != null && runs.some((r) => r.id === selected)) return;
    if (filtered[0]) setSelected(filtered[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filtered.map((r) => r.id).join(",")]);

  return (
    <div ref={ref} className="ops-fill">
      <div className="col" style={{ width: Math.min(400, Math.max(230, w * 0.32)), flex: "none", borderRight: "1px solid var(--line)", minHeight: 0, background: "color-mix(in srgb, var(--bg2) 50%, transparent)" }}>
        <div className="col gap8" style={{ padding: 10 }}>
          <div className="ops-search" style={{ background: "var(--bg2)", border: "1px solid var(--line)" }}>
            <SIcon name="magnifyingglass" className="muted" />
            <input value={query} placeholder="搜尋 pipeline、分支、commit…" onChange={(e) => setQuery(e.target.value)} />
            {query && <button className="ops-icon-btn" onClick={() => setQuery("")}><SIcon name="xmark.circle.fill" className="muted" /></button>}
          </div>
          <div className="row gap8">
            <select className="select grow" disabled={onlyFailed} value={status} onChange={(e) => setStatus(e.target.value as RunStatusKey | "")}>
              <option value="">所有狀態</option><option disabled>──────</option>
              {RUN_ORDER.map((s) => <option key={s} value={s}>{RUN[s].label}</option>)}
            </select>
            <select className="select grow" value={repo} onChange={(e) => setRepo(e.target.value)}>
              <option value="">所有 repo</option><option disabled>──────</option>
              {repoIds.map((r) => <option key={r} value={r}>{repoName(r)}</option>)}
            </select>
          </div>
          <div className="row" style={{ gap: 14 }}>
            <Check on={onlyFailed} onChange={setOnlyFailed}>只看失敗</Check>
            <Check on={onlyAI} onChange={setOnlyAI}><SIcon name="sparkles" size={12} />AI 觸發</Check>
          </div>
        </div>
        <div className="divider" />
        {filtered.length === 0 ? (
          <EmptyState title={err && !runs.length ? "讀不到執行紀錄" : runs.length === 0 ? "還沒有任何執行" : "沒有符合的執行"} icon="arrow.triangle.branch" message={err && !runs.length ? err : hasFilters ? "調整篩選條件試試看" : undefined} />
        ) : (
          <div className="ops-list">
            {filtered.map((r) => (
              <div key={r.id} className={"ops-list-row" + (r.id === selected ? " on" : "")} onClick={() => setSelected(r.id)}
                onContextMenu={menu.open([
                  { label: "重新執行", disabled: runIsActive(r), onClick: () => act(() => pipelineUpdate(r.id, "retry")) },
                  ...(runIsActive(r) ? [{ label: "取消", onClick: () => act(() => pipelineUpdate(r.id, "cancel")) }] : []),
                  "sep",
                  { label: "複製 commit SHA", onClick: () => copyText(r.commitSHA) },
                  { label: "複製執行編號", onClick: () => copyText(`#${r.id}`) },
                ])}>
                <RunRow run={r} />
              </div>
            ))}
          </div>
        )}
        <div className="ops-list-foot">
          <span>{filtered.length} 筆執行</span><span className="grow" />
          {hasFilters && <button className="btn link caption" onClick={() => { setStatus(""); setRepo(""); setOnlyFailed(false); setOnlyAI(false); setQuery(""); }}>清除篩選</button>}
        </div>
      </div>
      <div className="ops-scroll">
        {selected != null && runs.some((r) => r.id === selected)
          ? <RunDetail key={selected} run={runs.find((r) => r.id === selected)!} runs={runs} onOpenRun={setSelected} />
          : <EmptyState title="選一筆執行" icon="sidebar.left" message="左邊選一筆執行就能看到階段、工作與日誌" />}
      </div>
      {menu.el}{toastEl}
    </div>
  );
}

function RunRow({ run }: { run: PipelineRun }) {
  const o = useOps();
  return (
    <div className="row" style={{ gap: 10, alignItems: "flex-start", padding: "4px 0" }}>
      <span style={{ width: 22, flex: "none", paddingTop: 1 }}><StatusIcon s={run.status} size={17} /></span>
      <div className="col grow" style={{ gap: 4, minWidth: 0 }}>
        <div className="row gap6"><span className="ellipsis" style={{ fontWeight: 600 }}>{run.pipeline}</span><span className="grow" /><span className="caption muted num">#{run.id}</span></div>
        <div className="caption muted ellipsis">{run.commitMessage}</div>
        <div className="row gap6 muted" style={{ fontSize: 10.5 }}>
          <SIcon name="arrow.triangle.branch" size={10} /><span className="ellipsis">{run.branch}</span>
          <span className="mono faint">{shortSHA(run)}</span>
          {run.targetEnv && <EnvBadge env={run.targetEnv} />}
        </div>
        {run.status === "running" && <ProgressBar value={runProgress(run)} />}
        <div className="row muted" style={{ gap: 5, fontSize: 10.5 }}>
          <ActorAvatar actor={run.triggeredBy} size={14} />
          <span className="ellipsis" style={{ color: runIsAI(run) ? "var(--purple)" : undefined }}>{o.name(run.triggeredBy)}</span>
          <span className="faint">·</span><span className="ellipsis">{run.trigger}</span>
          <span className="grow" />
          <span style={{ flex: "none" }}>{relative(run.startedAt)}</span>
          <SIcon name="timer" size={10} className="faint" /><ElapsedText run={run} />
        </div>
      </div>
    </div>
  );
}

// MARK: - 詳情

function RunDetail({ run, runs, onOpenRun }: { run: PipelineRun; runs: PipelineRun[]; onOpenRun: (n: number) => void }) {
  const o = useOps();
  const [selectedJob, setSelectedJob] = useState<string>();
  const [analyzing, setAnalyzing] = useState(false);
  const [showFix, setShowFix] = useState(false);
  const [ref, w] = useWidth<HTMLDivElement>();
  const [dlg, ask] = useConfirm();
  const { run: act, toastEl } = useAct();
  const jobs = runJobs(run);
  const current = (selectedJob ? jobs.find((j) => j.id === selectedJob) : undefined) ?? focusJob(run);
  const stageOf = (j?: PipelineJob) => (j ? run.stages.find((s) => s.jobs.some((x) => x.id === j.id)) : undefined);
  const target = run.targetEnv ? ENV[run.targetEnv].label : "Production";
  const cancel = () => act(() => pipelineUpdate(run.id, "cancel"));
  const askCancel = () => ask({ title: `取消 ${run.pipeline} #${run.id}?`, message: "執行中的工作會被中止,已完成的階段結果會保留。", buttons: [{ label: "取消執行", danger: true, onClick: cancel }] });
  const askApprove = () => ask({
    title: `核准部署到 ${target}?`, message: `${run.pipeline} · ${shortSHA(run)}「${run.commitMessage}」會部署到正式環境,影響所有使用者。`,
    buttons: [{ label: "核准並部署", primary: true, onClick: () => act(() => action("pipeline_approve_gate", { id: run.id })) }],
  });
  const retry = async () => {
    const before = Math.max(0, ...runs.map((r) => r.id));
    const r = await act(() => pipelineUpdate(run.id, "retry"));
    if (r !== undefined) onOpenRun(before + 1);
  };
  // Swift 的 cicdDiagnose:由失敗日誌組出分析文字,寫進 run 的 aiDiagnosis
  const diagnose = async () => {
    setAnalyzing(true);
    await new Promise((res) => setTimeout(res, 1400));
    const fs = run.stages.find((s) => s.status === "failed");
    const fj = jobs.find((j) => j.status === "failed");
    const errLine = [...(fj?.log ?? [])].reverse().find((l) => /error|fail|錯誤/i.test(l));
    let text = `失敗發生在「${fs?.name ?? "未知階段"}」的 ${fj?.name ?? "job"}。`;
    if (errLine) text += `關鍵錯誤:${errLine.trim()}。`;
    text += "這個錯誤與本次 commit 的變更有關的機率較高,建議先檢查相關測試與相依版本;若為暫時性問題,重新執行即可。";
    await act(() => tool("ops_pipeline_update", { run_id: run.id, ai_diagnosis: text }));
    setAnalyzing(false);
  };
  const done = run.stages.filter((s) => s.status === "succeeded").length;
  const meta = (icon: string, content: ReactNode, title?: string) => <span className="row gap4" title={title}><SIcon name={icon} size={12} />{content}</span>;
  return (
    <div ref={ref} className="col" style={{ padding: 20, gap: 16 }}>
      <div className="col" style={{ gap: 10 }}>
        <div className="row gap12" style={{ alignItems: "flex-start" }}>
          <div className="col gap6 grow" style={{ minWidth: 0 }}>
            <div className="row gap8 wrap">
              <span className="h3 ellipsis">{run.pipeline}</span><span className="h3 muted num" style={{ fontWeight: 400 }}>#{run.id}</span>
              <RunStatusBadge s={run.status} />
              {run.targetEnv && <EnvBadge env={run.targetEnv} />}
              {runIsAI(run) && <AIBadge text="AI 觸發" />}
            </div>
            <div style={{ userSelect: "text" }}>{run.commitMessage}</div>
          </div>
          <div className="row gap8" style={{ flex: "none" }}>
            {run.status === "waitingApproval" && <button className="btn purple" onClick={askApprove}><SIcon name="checkmark.seal" size={12} />核准部署</button>}
            {(runIsActive(run) || run.status === "waitingApproval") && <button className="btn danger" onClick={askCancel}><SIcon name="stop.circle" size={12} />取消</button>}
            <button className="btn" disabled={runIsActive(run)} title={runIsActive(run) ? "執行中,完成或取消後才能重新執行" : "用同一個 commit 重新執行"} onClick={retry}><SIcon name="arrow.clockwise" size={12} />重新執行</button>
          </div>
        </div>
        <div className="row gap12 wrap caption muted" style={{ gap: 14 }}>
          {meta("arrow.triangle.branch", run.branch)}
          <button className="ops-plain row gap4 mono" title="複製完整 SHA" onClick={() => copyText(run.commitSHA)}><SIcon name="number" size={12} />{shortSHA(run)}</button>
          {meta("shippingbox", o.repos.find((r) => r.id === run.repo)?.name ?? run.repo)}
          {meta("bolt", run.trigger)}
          <ActorLabel actor={run.triggeredBy} />
          {meta("clock", relative(run.startedAt))}
          {meta("timer", <ElapsedText run={run} />)}
        </div>
        {run.status === "running" && <div style={{ maxWidth: 420 }}><ProgressBar value={runProgress(run)} /></div>}
      </div>
      {run.status === "failed" && (
        <Callout tint="purple" icon="sparkles">
          <div className="row gap8"><span className="card-title">AI 診斷</span><AIBadge text="自動分析" /></div>
          {run.aiDiagnosis ? (
            <>
              <div style={{ userSelect: "text" }}>{run.aiDiagnosis}</div>
              <div className="row gap8">
                <button className="btn purple" onClick={() => setShowFix(true)}><SIcon name="wand.and.stars" size={12} />交給 AI 修復</button>
                {jobs.find((j) => j.status === "failed") && <button className="btn" onClick={() => setSelectedJob(jobs.find((j) => j.status === "failed")!.id)}>查看失敗日誌</button>}
              </div>
            </>
          ) : (
            <>
              <div className="muted">這次失敗還沒有 AI 分析。AI 會讀取失敗的日誌與本次變更,整理出可能原因。</div>
              <button className="btn" style={{ alignSelf: "flex-start" }} disabled={analyzing} onClick={diagnose}>
                {analyzing && <SIcon name="arrow.triangle.2.circlepath" spin size={12} />}{analyzing ? "分析中…" : "請 AI 分析"}
              </button>
            </>
          )}
        </Callout>
      )}
      {run.status === "waitingApproval" && (
        <Callout tint="purple" icon="hand.raised.fill">
          <div className="card-title">等待核准</div>
          <div className="muted">前面的階段都已通過。部署到 {target} 前需要有人核准。</div>
          <div className="row gap8">
            <button className="btn purple" onClick={askApprove}><SIcon name="checkmark.seal" size={12} />核准部署</button>
            <button className="btn danger" onClick={cancel}>拒絕</button>
          </div>
        </Callout>
      )}
      <Card title="階段" subtitle={`${done}/${run.stages.length} 個階段完成 · ${jobs.length} 個工作`}>
        {run.stages.length === 0 ? <div className="muted">這次執行沒有階段資料</div> : <StageGraph run={run} selected={current?.id} onSelect={setSelectedJob} />}
      </Card>
      <div className={w > 860 ? "row" : "col"} style={{ gap: 16, alignItems: w > 860 ? "flex-start" : "stretch" }}>
        <div style={{ width: w > 860 ? 270 : undefined, flex: "none" }}>
          <Card title="工作">
            <div className="col" style={{ gap: 10 }}>
              {run.stages.map((s) => (
                <div key={s.id} className="col" style={{ gap: 2 }}>
                  <div className="row gap6"><SIcon name={RUN[s.status].icon} color={RUN[s.status].color} size={12} /><span className="caption muted" style={{ fontWeight: 600 }}>{s.name}</span><span className="grow" /><span className="faint num" style={{ fontSize: 10.5 }}>{dur(stageDuration(s.jobs))}</span></div>
                  {s.jobs.map((j) => (
                    <button key={j.id} className="ops-plain row gap8" onClick={() => setSelectedJob(j.id)}
                      style={{ padding: "5px 8px", borderRadius: 6, background: j.id === current?.id ? "color-mix(in srgb, var(--accent) 18%, transparent)" : undefined }}>
                      <span style={{ width: 16, flex: "none" }}><StatusIcon s={j.status} /></span>
                      <span className="ellipsis grow">{j.name}</span>
                      <span className="caption muted num">{dur(j.duration)}</span>
                    </button>
                  ))}
                  {s.jobs.length === 0 && <span className="caption faint" style={{ paddingLeft: 20 }}>沒有工作</span>}
                </div>
              ))}
            </div>
          </Card>
        </div>
        <LogViewer key={current?.id} job={current} stageName={stageOf(current)?.name} />
      </div>
      <Modal open={showFix} onClose={() => setShowFix(false)} width={480}>
        <AIFixSheet run={run} close={() => setShowFix(false)} onStarted={onOpenRun} />
      </Modal>
      {dlg}{toastEl}
    </div>
  );
}

function StageGraph({ run, selected, onSelect }: { run: PipelineRun; selected?: string; onSelect: (id: string) => void }) {
  return (
    <div className="row" style={{ overflowX: "auto", alignItems: "flex-start", padding: "4px 2px" }}>
      {run.stages.map((stage, i) => {
        const prev = i > 0 ? run.stages[i - 1] : undefined;
        const tint = stage.status === "canceled" ? "var(--gray)" : color(RUN[stage.status].color);
        let conn = "color-mix(in srgb, var(--fg2) 35%, transparent)";
        if (prev?.status === "succeeded") conn = stage.status === "queued" ? "color-mix(in srgb, var(--fg2) 40%, transparent)" : "color-mix(in srgb, var(--green) 70%, transparent)";
        if (prev?.status === "failed") conn = "color-mix(in srgb, var(--red) 50%, transparent)";
        return (
          <div key={stage.id} className="row" style={{ alignItems: "flex-start" }}>
            {prev && <div className="ops-conn"><i style={{ background: conn }} /><SIcon name="chevron.right" size={9} color={conn} /></div>}
            <div className={"ops-stage" + (stage.status === "running" ? " running" : "")}
              style={{ background: `color-mix(in srgb, ${tint} ${stage.status === "queued" ? 4 : 9}%, transparent)`, borderColor: `color-mix(in srgb, ${tint} ${stage.status === "running" ? 70 : 35}%, transparent)` }}>
              <div className="row gap6"><StatusIcon s={stage.status} /><span className="ellipsis" style={{ fontWeight: 600 }}>{stage.name}</span></div>
              <div className="row muted" style={{ fontSize: 10.5 }}><span>{RUN[stage.status].label}</span><span className="grow" /><span className="num">{dur(stageDuration(stage.jobs))}</span></div>
              {stage.jobs.length > 0 && (
                <>
                  <div className="divider" />
                  <div className="col" style={{ gap: 2 }}>
                    {stage.jobs.map((j) => (
                      <button key={j.id} className="ops-plain row gap6" title={`${j.name} · ${RUN[j.status].label} · ${dur(j.duration)}`} onClick={() => onSelect(j.id)}
                        style={{ padding: "2px 4px", borderRadius: 4, background: selected === j.id ? "color-mix(in srgb, var(--accent) 20%, transparent)" : undefined }}>
                        <span className="dot" style={{ width: 6, height: 6, background: j.status === "canceled" ? "var(--gray)" : color(RUN[j.status].color) }} />
                        <span className="caption ellipsis">{j.name}</span>
                      </button>
                    ))}
                  </div>
                </>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function lineColor(s: string) {
  const l = s.toLowerCase();
  if (/error|fail|✗|錯誤|panic/.test(l)) return "var(--red)";
  if (/warn|警告/.test(l)) return "var(--orange)";
  if (/✓|pass|success|done|完成/.test(l)) return "var(--green)";
  if (/^[$+>]/.test(l)) return "var(--fg2)";
  return undefined;
}

function LogViewer({ job, stageName }: { job?: PipelineJob; stageName?: string }) {
  const [query, setQuery] = useState("");
  const [onlyMatches, setOnlyMatches] = useState(false);
  const [wrap, setWrap] = useState(false);
  const lines = (job?.log ?? []).map((text, i) => ({ id: i + 1, text }));
  const ql = query.toLowerCase();
  const matches = (s: string) => !!query && s.toLowerCase().includes(ql);
  const matchCount = query ? lines.filter((l) => matches(l.text)).length : 0;
  const shown = onlyMatches && query ? lines.filter((l) => matches(l.text)) : lines;
  const hl = (s: string) => {
    if (!query) return s;
    const out: ReactNode[] = [];
    let i = 0, k = 0;
    const low = s.toLowerCase();
    for (let p = low.indexOf(ql); p >= 0; p = low.indexOf(ql, i)) { out.push(s.slice(i, p), <mark key={k++}>{s.slice(p, p + query.length)}</mark>); i = p + query.length; }
    out.push(s.slice(i));
    return out;
  };
  const placeholder = (text: string, icon: string, spin?: boolean) => (
    <div className="col gap8" style={{ alignItems: "center", justifyContent: "center", minHeight: 160 }}>
      <SIcon name={spin ? "arrow.triangle.2.circlepath" : icon} size={20} spin={spin} className="faint" /><span className="muted">{text}</span>
    </div>
  );
  return (
    <div className="ops-log">
      <div className="row gap8" style={{ padding: "8px 12px" }}>
        <SIcon name="terminal" className="muted" />
        <div className="col" style={{ minWidth: 0 }}>
          <span className="ellipsis" style={{ fontWeight: 600 }}>{job?.name ?? "日誌"}</span>
          {stageName && <span className="muted" style={{ fontSize: 10.5 }}>{stageName} · {lines.length} 行</span>}
        </div>
        {job && <RunStatusBadge s={job.status} />}
        <span className="grow" />
        <div className="ops-search" style={{ maxWidth: 190 }}>
          <SIcon name="magnifyingglass" size={12} className="muted" />
          <input style={{ fontSize: 11.5, minWidth: 70 }} placeholder="搜尋日誌" value={query} onChange={(e) => setQuery(e.target.value)} />
          {query && <><span className="muted num" style={{ fontSize: 10.5, flex: "none" }}>{matchCount} 筆</span><button className="ops-icon-btn" onClick={() => setQuery("")}><SIcon name="xmark.circle.fill" size={12} className="muted" /></button></>}
        </div>
        <Check on={onlyMatches} onChange={setOnlyMatches} disabled={!query}><span className="caption">只顯示符合</span></Check>
        <MenuButton className="ops-icon-btn" align="right" label={<SIcon name="ellipsis.circle" />} items={[
          { label: "自動換行", checked: wrap, onClick: () => setWrap(!wrap) },
          { label: "複製全部日誌", disabled: !lines.length, onClick: () => copyText(lines.map((l) => l.text).join("\n")) },
        ]} />
      </div>
      <div className="divider" />
      {!job ? placeholder("選一個工作來看日誌", "doc.text.magnifyingglass")
        : lines.length === 0 ? (job.status === "running" ? placeholder("等待輸出…", "ellipsis", true)
          : job.status === "queued" || job.status === "waitingApproval" ? placeholder("工作還沒開始", "clock") : placeholder("這個工作沒有日誌", "doc"))
          : (
            <div className="ops-log-body">
              {shown.map((l) => (
                <div key={l.id} className={"ops-log-line" + (wrap ? " wrap" : "") + (matches(l.text) ? " hit" : "")}>
                  <span className="faint" style={{ width: 34, textAlign: "right", flex: "none" }}>{l.id}</span>
                  <span style={{ color: lineColor(l.text) }}>{hl(l.text)}</span>
                </div>
              ))}
              {job.status === "running" && <div className="ops-cursor" />}
            </div>
          )}
    </div>
  );
}

function AIFixSheet({ run, close, onStarted }: { run: PipelineRun; close: () => void; onStarted: (id: number) => void }) {
  const o = useOps();
  const [openPR, setOpenPR] = useState(true);
  const [rerun, setRerun] = useState(true);
  const [error, setError] = useState<string>();
  const fixer = o.automations.find((a) => /ci|build|修復/i.test(a.id + a.name + a.trigger)) ?? o.automations[0];
  const fixerActor = { kind: "ai" as const, id: fixer?.id ?? "ci-fixer" };
  const branch = `ai/fix-run-${run.id}`;
  const step = (n: number, text: string) => (
    <div className="row gap8"><span className="ops-avatar" style={{ width: 18, height: 18, fontSize: 10, background: "var(--purple)" }}>{n}</span>{text}</div>
  );
  const start = async () => {
    try {
      const r: any = await action("pipeline_ai_fix", { id: run.id, openPR, rerun });
      if (r?.runId) onStarted(r.runId);
      close();
    } catch (e: any) { setError(String(e?.message ?? e)); }
  };
  return (
    <div className="col" style={{ gap: 16 }}>
      <div className="row" style={{ gap: 10 }}>
        <ActorAvatar actor={fixerActor} size={32} />
        <div className="col" style={{ gap: 2 }}>
          <span className="h3">交給 AI 修復</span>
          <span className="muted">{run.pipeline} #{run.id} · {o.name(fixerActor)}</span>
        </div>
      </div>
      {run.aiDiagnosis && <div style={{ padding: 10, borderRadius: 8, background: "color-mix(in srgb, var(--purple) 7%, transparent)" }}>{run.aiDiagnosis}</div>}
      <div className="col gap8">
        <div className="card-title">AI 會依序執行</div>
        {step(1, "讀取失敗日誌與本次 commit 的差異")}
        {step(2, `從 ${run.branch} 開出分支 ${branch}`)}
        {step(3, "修改程式碼並在本機跑相關測試")}
        {openPR && step(4, "開 PR,附上原因與修改說明,等你審查")}
        {rerun && step(openPR ? 5 : 4, "在修復分支上重新執行 CI 驗證")}
      </div>
      <div className="col gap6">
        <Check on={openPR} onChange={setOpenPR}>完成後開 PR(不會直接合併)</Check>
        <Check on={rerun} onChange={setRerun}>在修復分支重新執行 CI</Check>
      </div>
      <div className="caption muted row gap4"><SIcon name="lock.shield" size={12} />AI 不會直接推送到 {run.branch},所有變更都要經過 PR 審查。</div>
      {error && <div className="error-text">{error}</div>}
      <div className="row gap8" style={{ justifyContent: "flex-end" }}>
        <button className="btn" onClick={close}>取消</button>
        <button className="btn purple" onClick={start}><SIcon name="wand.and.stars" size={12} />開始修復</button>
      </div>
    </div>
  );
}

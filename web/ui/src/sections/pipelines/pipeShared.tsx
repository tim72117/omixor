// CI/CD 畫面共用(Swift 的 PipeShared.swift)
import { tool } from "../../api";
import { dur, RUN, SIcon, t, useNow, type CheckKey, type Deployment, type PipelineJob, type PipelineRun, type RunStatusKey } from "../../components/opsKit";

export const runJobs = (r: PipelineRun) => r.stages.flatMap((s) => s.jobs);
export function runProgress(r: PipelineRun) {
  const all = runJobs(r);
  if (!all.length) return r.status === "succeeded" ? 1 : 0;
  return all.filter((j) => ["succeeded", "failed", "canceled"].includes(j.status)).length / all.length;
}
export const runIsActive = (r: PipelineRun) => r.status === "running" || r.status === "queued";
export const runIsAI = (r: PipelineRun) => r.triggeredBy.kind === "ai";
export const shortSHA = (r: PipelineRun) => r.commitSHA.slice(0, 7);
export function focusJob(r: PipelineRun): PipelineJob | undefined {
  const all = runJobs(r);
  return all.find((j) => j.status === "failed") ?? all.find((j) => j.status === "running") ?? all.find((j) => j.status === "waitingApproval")
    ?? [...all].reverse().find((j) => j.log?.length) ?? all[0];
}
export const stageDuration = (jobs: PipelineJob[]) => {
  const ds = jobs.map((j) => j.duration).filter((d): d is number => d != null);
  return ds.length ? Math.max(...ds) : undefined;
};
export const depIsActive = (d: Deployment) => d.status === "running" || d.status === "queued" || d.status === "waitingApproval";
export const depIsRollback = (d: Deployment) => d.changes.some((c) => c.includes("回滾"));
export const CHECK_LABEL: Record<CheckKey, string> = { pending: "等待中", running: "檢查中", passed: "通過", failed: "未通過" };

/** 重新執行 / 取消:pipeline_update */
export const pipelineUpdate = (runId: number, act: "retry" | "cancel") => tool("pipeline_update", { run_id: runId, action: act });

/** 即時更新的經過時間 */
export function ElapsedText({ run }: { run: PipelineRun }) {
  const active = runIsActive(run) || run.status === "waitingApproval";
  const n = useNow(active ? 1000 : 60000);
  return <span className="num">{dur(active ? (run.duration ?? Math.max(0, (n - t(run.startedAt)) / 1000)) : run.duration)}</span>;
}

export function StatusIcon({ s, size }: { s: RunStatusKey; size?: number }) {
  return <span title={RUN[s]?.label} style={{ display: "inline-flex" }}><SIcon name={RUN[s]?.icon} color={RUN[s]?.color} size={size} spin={s === "running"} className={s === "waitingApproval" ? "ops-pulse" : undefined} /></span>;
}

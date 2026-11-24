// AI 自動化 › 執行日誌:這台 Mac 上「自動執行」跑過的任務(後端每個組織留最近 50 筆),可依角色、結果篩選;
// 每筆可展開成工具呼叫的時間線。網址:?tab=log&run=<runId>(展開哪一筆,一次一筆)、&logRole=<角色 id>、&outcome=done|blocked|error。
import { useView } from "../../api";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { Pill } from "../../components/Pill";
import { dateTime, duration } from "../../fmt";
import { useRouteChoice, useRouteParam } from "../../lib/nav";
import type { AgentRunnerView, RunnerRecord } from "../../models";
import css from "./automation.module.css";
import { RunLogPanel, RunWarnings } from "./RunLog";
import { OUTCOME_FILTERS, OUTCOME_FILTER_LABEL, filterRuns, outcomeOf, runRoles, type OutcomeFilter } from "./runLogModel";

export function AutoLog({ openTask }: { openTask: (taskId: string) => void }) {
  const [v, err] = useView<AgentRunnerView>("agent_runner", ["agents", "orgs", "system"]);
  const [role, setRole] = useRouteParam("logRole");
  const [outcome, setOutcome] = useRouteChoice<OutcomeFilter>("outcome", OUTCOME_FILTERS, "all");
  const [openRun, setOpenRun] = useRouteParam("run");
  if (!v) return err ? <div className={"col " + css.tabPage}><ErrorText>{err}</ErrorText></div> : <div className={"muted " + css.loading}>載入中…</div>;
  const rows = filterRuns(v.history, role ?? "", outcome);
  return (
    <div className={"col gap12 " + css.tabPage}>
      <Card title="執行日誌" subtitle={v.history.length ? `${rows.length} / ${v.history.length} 筆` : undefined}>
        {v.history.length === 0
          ? <EmptyState title="還沒有執行紀錄" icon="clock" message="角色自動執行過的任務會出現在這裡。" />
          : <>
            <div className="row gap8">
              <select className="select" value={role ?? ""} onChange={(e) => setRole(e.target.value || undefined)}>
                <option value="">全部角色</option>{runRoles(v.history).map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
              </select>
              <select className="select" value={outcome} onChange={(e) => setOutcome(e.target.value as OutcomeFilter)}>
                {OUTCOME_FILTERS.map((o) => <option key={o} value={o}>{OUTCOME_FILTER_LABEL[o]}</option>)}
              </select>
            </div>
            {rows.length === 0 && <span className="muted">沒有符合的紀錄</span>}
            <div className="col">
              {rows.map((r, i) => <RecordRow key={`${r.taskId}-${r.startedAt}-${i}`} r={r} openTask={openTask}
                open={!!r.runId && r.runId === openRun} toggle={() => setOpenRun(r.runId === openRun ? undefined : r.runId)} />)}
            </div>
          </>}
      </Card>
    </div>
  );
}

function RecordRow({ r, open, toggle, openTask }: { r: RunnerRecord; open: boolean; toggle: () => void; openTask: (taskId: string) => void }) {
  const o = outcomeOf(r.outcome);
  return (
    <div className="col">
      <div className={"row gap8 " + css.recordRow}>
        <Icon name={o.icon} size={13} color={`var(--${o.color})`} />
        <button className="btn link mono" onClick={() => openTask(r.taskId)}>{r.taskId}</button>
        <span className="ellipsis grow" title={r.error || r.title}>{r.title}{r.error ? <span className="caption muted"> · {r.error}</span> : null}</span>
        <RunWarnings r={r} />
        <span className={"caption muted ellipsis " + css.w90}>{r.agentName}</span>
        <Pill text={o.label} color={o.color} />
        <span className={"caption muted num " + css.w80 + " " + css.right} title="耗時">{duration(r.durationSeconds)}</span>
        <span className={"caption muted num " + css.w60 + " " + css.right} title="Claude 估計花費(換算成 API 價格;用訂閱不另外收費)">${(r.costUsd ?? 0).toFixed(2)}</span>
        <span className={"caption muted " + css.w90 + " " + css.right}>{dateTime(r.finishedAt)}</span>
        {r.runId && <button className="btn small" onClick={toggle}>{open ? "收合" : `執行日誌 ${r.entryCount}`}</button>}
      </div>
      {open && <RunLogPanel runId={r.runId} />}
    </div>
  );
}

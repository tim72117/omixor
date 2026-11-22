// 執行紀錄:每次 AI 執行的逐步過程(Swift 的 AutoRunsView)
import { useEffect, useState } from "react";
import { Card } from "../../components/Card";
import { Chip } from "../../components/Chip";
import { CodeBlock } from "../../components/CodeBlock";
import { EmptyState } from "../../components/EmptyState";
import { Icon } from "../../components/Icon";
import { iconButtonClass } from "../../components/IconButton";
import { IconTile } from "../../components/IconTile";
import { MetricTile } from "../../components/MetricTile";
import { Pill } from "../../components/Pill";
import { Spinner } from "../../components/Spinner";
import { color } from "../../lib/color";
import { dur, hm, num } from "../../lib/format";
import { RUN_COLOR, RUN_ICON, RUN_LABEL, RUN_STATUSES, type AIStep, type AutomationRun, type RunStatus } from "../../models";
import { relative } from "../../fmt";
import css from "./automation.module.css";
import { StepIcon, autoName, pendingOf, useAuto } from "./autoShared";

export function AutoRuns({ selection, setSelection, openApproval, openAutomation }: {
  selection?: string; setSelection: (id?: string) => void; openApproval: (id: string) => void; openAutomation: (id: string) => void;
}) {
  const d = useAuto();
  const [status, setStatus] = useState<RunStatus | "">("");
  const [auto, setAuto] = useState("");
  const runs = d.runs.filter((r) => (!status || r.status === status) && (!auto || r.automation === auto)).sort((a, b) => b.startedAt.localeCompare(a.startedAt));
  const sel = d.runs.find((r) => r.id === selection);
  useEffect(() => {
    if (selection || !d.runs.length) return;
    const first = runs.find((r) => r.status === "running" || r.status === "waitingApproval") ?? runs[0];
    if (first) setSelection(first.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [d.runs.length]);
  if (d.loaded && !d.runs.length) return <EmptyState title="還沒有執行紀錄" icon="list.bullet.rectangle" message="AI 自動化每次執行的過程都會記錄在這裡。" />;
  return (
    <div className="split">
      <div className={"split-list " + css.runList}>
        <div className={"row gap8 " + css.listHead}>
          <select className="select grow" value={status} onChange={(e) => setStatus(e.target.value as RunStatus | "")}>
            <option value="">所有狀態</option>{RUN_STATUSES.map((s) => <option key={s} value={s}>{RUN_LABEL[s]}</option>)}
          </select>
          <select className="select grow" value={auto} onChange={(e) => setAuto(e.target.value)}>
            <option value="">所有流程</option>{d.automations.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
        </div>
        {runs.length === 0 ? <EmptyState title="沒有符合條件的紀錄" icon="line.3.horizontal.decrease.circle" /> : runs.map((r) => (
          <div key={r.id} className={css.listRow + " " + css.runRow + (selection === r.id ? " " + css.on : "")} onClick={() => setSelection(r.id)}>
            <span className={css.runIcon}>
              {r.status === "running" ? <Spinner size={13} /> : <Icon name={RUN_ICON[r.status]} size={14} color={color(RUN_COLOR[r.status])} />}
            </span>
            <div className={"col grow " + css.gap3}>
              <div className="row gap6"><span className={"ellipsis grow " + css.medium}>{autoName(d, r.automation)}</span><span className={"caption muted " + css.none}>{relative(r.startedAt)}</span></div>
              <span className={"mono caption ellipsis " + css.indigo}>{r.target}</span>
              <span className={"caption muted " + css.clamp2}>{r.summary}</span>
              {r.status === "waitingApproval" && <span><Pill text="需要核准" color="purple" icon="hand.raised.fill" /></span>}
            </div>
          </div>
        ))}
      </div>
      <div className="split-detail">
        {sel ? <RunDetail run={sel} openApproval={openApproval} openAutomation={openAutomation} />
          : <EmptyState title="選擇一筆執行紀錄" icon="sidebar.left" message="查看 AI 每一步用了什麼工具、得到什麼結果。" />}
      </div>
    </div>
  );
}

function RunDetail({ run, openApproval, openAutomation }: { run: AutomationRun; openApproval: (id: string) => void; openAutomation: (id: string) => void }) {
  const d = useAuto();
  const a = d.automations.find((x) => x.id === run.automation);
  const approval = pendingOf(d).find((p) => p.automation === run.automation);
  const done = run.steps.filter((s) => s.status === "done").length;
  const duration = run.finishedAt ? (new Date(run.finishedAt).getTime() - new Date(run.startedAt).getTime()) / 1000 : undefined;
  return (
    <div className="page-inner">
      <div className={"row gap12 " + css.top}>
        <IconTile icon={a?.icon ?? "sparkles"} size={40} />
        <div className="col gap4 grow">
          <span className="row gap8"><span className="h3">{a?.name ?? run.automation}</span><Pill text={RUN_LABEL[run.status]} color={RUN_COLOR[run.status]} icon={RUN_ICON[run.status]} /></span>
          <span className={"muted " + css.selectable}>{run.summary}</span>
          <span className="row gap6 caption"><span className="muted">對象</span><Chip text={run.target} /><span className="faint">· 執行 {run.id}</span></span>
        </div>
        {a && <button className="btn small" onClick={() => openAutomation(run.automation)}>流程設定</button>}
      </div>
      {run.status === "waitingApproval" && (
        <div className={"row gap8 " + css.waitBox}>
          <Icon name="hand.raised.fill" size={18} color="var(--purple)" />
          <div className="col grow"><span className={css.strong}>這次執行在等人核准</span><span className="caption muted">{approval?.title ?? "AI 已準備好動作,核准後才會繼續。"}</span></div>
          {approval && <button className="btn purple" onClick={() => openApproval(approval.id)}>前往核准</button>}
        </div>
      )}
      <div className={"grid " + css.metricGrid}>
        <MetricTile title="開始" value={hm(run.startedAt)} icon="clock" tint="indigo" />
        <MetricTile title="耗時" value={run.status === "running" ? "進行中" : dur(duration)} icon="timer" tint="blue" />
        <MetricTile title="Tokens" value={num(run.tokens)} icon="text.word.spacing" tint="purple" />
        <MetricTile title="成本" value={`$${run.costUSD.toFixed(3)}`} icon="dollarsign.circle" tint="green" />
      </div>
      <Card title="AI 執行過程" subtitle={run.steps.length ? `${done}/${run.steps.length} 步完成` : "沒有步驟紀錄"}>
        <div className="col">{run.steps.map((s, i) => <StepRow key={s.id} step={s} index={i + 1} last={i === run.steps.length - 1} />)}</div>
      </Card>
    </div>
  );
}

function StepRow({ step, index, last }: { step: AIStep; index: number; last: boolean }) {
  const [open, setOpen] = useState(true);
  const dim = step.status === "pending" || step.status === "skipped";
  return (
    <div className={"row " + css.stepRow}>
      <div className={"col " + css.stepRail}>
        <StepIcon status={step.status} />
        {!last && <div className={css.stepLine} />}
      </div>
      <div className={"col gap6 grow" + (last ? "" : " " + css.stepGap)}>
        <div className="row gap6">
          <span className="faint num">{index}.</span>
          <span className={css.medium + (dim ? " muted" : "")}>{step.title}</span>
          {step.tool && <Chip text={step.tool} icon="terminal" />}
          <div className="grow" />
          {step.status === "failed" && <Pill text="失敗" color="red" />}
          {step.status === "skipped" && <Pill text="略過" color="secondary" />}
          {step.status === "running" && <Pill text="執行中" color="blue" />}
          {step.output && (
            <button className={iconButtonClass.normal} title={open ? "收合輸出" : "展開輸出"} onClick={() => setOpen(!open)}>
              <span className={css.chev + (open ? " " + css.open : "")}><Icon name="chevron.right" size={11} /></span>
            </button>
          )}
        </div>
        {step.output && open && <CodeBlock text={step.output} maxHeight={200} tint={step.status === "failed" ? "red" : undefined} />}
      </div>
    </div>
  );
}

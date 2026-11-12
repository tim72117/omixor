// CI/CD:執行紀錄、部署、環境、旁白發佈(Swift 的 PipelinesView.swift)
import { useEffect, useState } from "react";
import { Pill } from "../../components/ui";
import { now, OpsProvider, Seg, t, useList, type Deployment, type PipelineRun } from "../../components/opsKit";
import type { SectionProps } from "..";
import { runIsActive } from "./pipeShared";
import { PipeRunsTab } from "./PipeRunsTab";
import { PipeDeploymentsTab } from "./PipeDeploymentsTab";
import { PipeEnvironmentsTab } from "./PipeEnvironmentsTab";
import { PipeVoiceTab } from "./PipeVoiceTab";

type Tab = "runs" | "deployments" | "environments" | "voice";
const TABS: { value: Tab; label: string }[] = [
  { value: "runs", label: "執行紀錄" }, { value: "deployments", label: "部署" }, { value: "environments", label: "環境" }, { value: "voice", label: "旁白發佈" },
];

export default function PipelinesView(props: SectionProps) {
  return <OpsProvider><Pipelines {...props} /></OpsProvider>;
}

function Pipelines({ focusId, clearFocus }: SectionProps) {
  const [tab, setTab] = useState<Tab>("runs");
  const [selectedRunId, setSelectedRunId] = useState<number>();
  const [highlight, setHighlight] = useState<string>();
  const [runs, , runsReady] = useList<PipelineRun>("pipeline_runs");
  const [deps, , depsReady] = useList<Deployment>("deployments");

  // 從其他頁跳過來:focusId 可能是 run 編號、部署 id、VJ-… 或 voice
  useEffect(() => {
    const f = focusId;
    if (!f) return;
    if (f.startsWith("VJ-") || f === "voice") { setTab("voice"); clearFocus(); return; }
    if (!runsReady || !depsReady) return;
    const digits = f.replace(/^[#run\- ]+|[#run\- ]+$/g, "");
    const n = Number(digits);
    if (digits && Number.isInteger(n) && runs.some((r) => r.id === n)) { setTab("runs"); setSelectedRunId(n); clearFocus(); }
    else if (deps.some((d) => d.id === f)) { setTab("deployments"); setHighlight(f); clearFocus(); }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, runsReady, depsReady]);

  const running = runs.filter(runIsActive).length;
  const waiting = runs.filter((r) => r.status === "waitingApproval").length + deps.filter((d) => d.status === "waitingApproval").length;
  const failed = runs.filter((r) => r.status === "failed" && t(r.startedAt) > now() - 86400000).length;
  return (
    <div className="ops-page">
      <div className="row wrap" style={{ padding: "16px 20px 12px", borderBottom: "1px solid var(--line)", gap: 16 }}>
        <div className="col" style={{ gap: 2 }}>
          <div className="page-title">CI/CD</div>
          <div className="muted">建置、測試、部署的每一次執行</div>
        </div>
        <span className="grow" />
        <div className="row gap6">
          {running > 0 && <Pill text={`執行中 ${running}`} color="blue" icon="arrow.triangle.2.circlepath" />}
          {waiting > 0 && <Pill text={`待核准 ${waiting}`} color="purple" icon="hand.raised.fill" />}
          {failed > 0 && <Pill text={`24 小時內失敗 ${failed}`} color="red" icon="xmark.circle.fill" />}
        </div>
        <Seg value={tab} onChange={setTab} options={TABS} />
      </div>
      <div className="ops-page">
        {tab === "runs" && <PipeRunsTab selected={selectedRunId} setSelected={setSelectedRunId} />}
        {tab === "deployments" && <PipeDeploymentsTab highlight={highlight} setHighlight={setHighlight} />}
        {tab === "environments" && <PipeEnvironmentsTab openDeployments={(id) => { setHighlight(id); setTab("deployments"); }} />}
        {tab === "voice" && <PipeVoiceTab />}
      </div>
    </div>
  );
}

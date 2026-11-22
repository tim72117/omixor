// AI 自動化:人監督 AI 自動完成維運步驟。分頁:待核准/虛擬角色/派發/組織鏈/自動化流程/執行紀錄/成效(Swift 的 AutomationView)
import { useEffect, useMemo, useState } from "react";
import { useView } from "../../api";
import { AIBadge } from "../../components/Badge";
import { ErrorText } from "../../components/ErrorText";
import { PageHeader } from "../../components/PageHeader";
import { Pill } from "../../components/Pill";
import { Segmented } from "../../components/Segmented";
import { ViewState } from "../../components/ViewState";
import { arr, type AgentsView, type Approval, type Automation, type AutomationRun } from "../../models";
import type { SectionProps } from "../index";
import { AutoAgents } from "./AutoAgents";
import { AutoChain } from "./AutoChain";
import { AutoDispatch } from "./AutoDispatch";
import { AutoFlows, FlowDetailSheet } from "./AutoFlows";
import { AutoInbox } from "./AutoInbox";
import { AutoInsights } from "./AutoInsights";
import { AutoRuns } from "./AutoRuns";
import css from "./automation.module.css";
import { AutoCtx, pendingOf, type AutoData } from "./autoShared";
import { useFeatureTabs } from "../../features";

type Tab = "inbox" | "agents" | "dispatch" | "chain" | "flows" | "runs" | "insights";
const TABS: { id: Tab; label: string }[] = [
  { id: "inbox", label: "待核准" }, { id: "agents", label: "虛擬角色" }, { id: "dispatch", label: "派發" }, { id: "chain", label: "組織鏈" }, { id: "flows", label: "自動化流程" }, { id: "runs", label: "執行紀錄" }, { id: "insights", label: "成效" },
];

export default function AutomationView({ focusId, clearFocus }: SectionProps) {
  const autosView = useView<Automation[]>("automations");
  const [autos] = autosView;
  const [runs, runsErr] = useView<AutomationRun[]>("automation_runs");
  const [approvals, approvalsErr] = useView<Approval[]>("approvals");
  const [agents, agentsErr] = useView<AgentsView>("agents", ["orgs"]);
  const data: AutoData = useMemo(() => ({
    automations: arr(autos), runs: arr(runs), approvals: arr(approvals),
    agents: agents && Array.isArray(agents.agents) ? { agents: agents.agents, tasks: arr(agents.tasks) } : undefined,
    // 讀取失敗也算「有結果」:否則某個 view 一直失敗時頁面永遠停在載入中(錯誤顯示在分頁上方)
    agentsError: agentsErr, loaded: autos !== undefined && (runs !== undefined || !!runsErr) && (approvals !== undefined || !!approvalsErr),
  }), [autos, runs, approvals, agents, agentsErr, runsErr, approvalsErr]);
  const [rawTab, setRawTab] = useState<Tab>("inbox");
  const tabs = useFeatureTabs(TABS.map((x) => x.id), rawTab, setRawTab, (x) => `automation.${x}`);
  const tab = tabs.shown, setTab = tabs.select;
  const [focusApproval, setFocusApproval] = useState<string>();
  const [detail, setDetail] = useState<string>();
  const [selectedRun, setSelectedRun] = useState<string>();
  const [selectedAgent, setSelectedAgent] = useState<string>();
  // 修改後核准時,記住人改過的指令(模型的 action 是唯讀)
  const [edited, setEdited] = useState<Record<string, string>>({});

  /** 跨頁跳轉:focusId 可能是核准請求、自動化、執行紀錄、角色或任務的 id */
  const open = (id: string): boolean => {
    if (id === "agents") { setTab("agents"); return true; }
    const ag = data.agents;
    if (ag?.agents.some((a) => a.id === id)) { setSelectedAgent(id); setTab("agents"); return true; }
    const task = ag?.tasks.find((x) => x.id.toLowerCase() === id.toLowerCase());
    if (task) { setSelectedAgent(task.agentId); setTab("agents"); return true; }
    if (data.approvals.some((a) => a.id === id)) { setFocusApproval(id); setTab("inbox"); return true; }
    if (data.automations.some((a) => a.id === id)) { setTab("flows"); setDetail(id); return true; }
    if (data.runs.some((r) => r.id === id)) { setSelectedRun(id); setTab("runs"); return true; }
    return false;
  };
  useEffect(() => {
    if (!focusId) return;
    if (open(focusId) || data.loaded) clearFocus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusId, data.loaded, data.agents ? 1 : 0]);

  const pending = pendingOf(data);
  const active = data.automations.filter((a) => a.enabled).length;
  const running = data.runs.filter((r) => r.status === "running").length;
  const blocked = data.agents?.tasks.filter((x) => x.status === "blocked").length ?? 0;
  const tabTitle = (t: (typeof TABS)[number]) =>
    (t.id === "agents" || t.id === "dispatch") && blocked > 0 ? `${t.label}(${blocked})` : t.id === "inbox" && pending.length ? `${t.label}(${pending.length})` : t.label;

  return (
    <AutoCtx.Provider value={data}>
      <div className={"col " + css.page}>
        <PageHeader title="AI 自動化" subtitle="AI 負責分析與執行,人負責把關。調整每個流程的自主程度,並在這裡核准高風險動作。"
          right={
            <div className="row gap8">
              <AIBadge text={`${active} 個流程啟用中`} />
              {running > 0 && <Pill text={`${running} 個執行中`} color="blue" icon="arrow.triangle.2.circlepath" />}
              {pending.length > 0 && <Pill text={`${pending.length} 件待核准`} color="orange" icon="hand.raised.fill" />}
            </div>
          }>
          <div className={"tabscroll " + css.tabs}><Segmented value={tab} onChange={setTab} options={TABS.filter((t) => tabs.on(t.id)).map((t) => ({ value: t.id, label: tabTitle(t) }))} /></div>
        </PageHeader>
        <div className={"col " + css.page}>
          <ViewState view={autosView} what="AI 自動化">{() => <>
          {runsErr && <ErrorText>讀不到執行紀錄:{runsErr}</ErrorText>}
          {approvalsErr && <ErrorText>讀不到核准請求:{approvalsErr}</ErrorText>}
          {tab === "inbox" ? <AutoInbox focusId={focusApproval} clearFocus={() => setFocusApproval(undefined)} edited={edited} setEdited={setEdited} openAutomation={setDetail} openRun={(id) => { setSelectedRun(id); setTab("runs"); }} />
            : tab === "agents" ? <AutoAgents selection={selectedAgent} setSelection={setSelectedAgent} />
            : tab === "dispatch" ? <AutoDispatch openTask={(t) => { setSelectedAgent(t.agentId); setTab("agents"); }} />
            : tab === "chain" ? <AutoChain />
            : tab === "flows" ? <AutoFlows openDetail={setDetail} />
            : tab === "runs" ? <AutoRuns selection={selectedRun} setSelection={setSelectedRun} openApproval={(id) => { setFocusApproval(id); setTab("inbox"); }} openAutomation={setDetail} />
            : <AutoInsights openAutomation={setDetail} />}
          </>}</ViewState>
        </div>
      </div>
      <FlowDetailSheet id={detail} onClose={() => setDetail(undefined)} navigate={(target) => { setDetail(undefined); open(target); }} />
    </AutoCtx.Provider>
  );
}

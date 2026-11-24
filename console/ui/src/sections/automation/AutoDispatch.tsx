// AI 自動化 › 派發:看哪一個角色被派了哪些任務(跨所有角色的總表),以及每個角色目前的負載
import { useEffect } from "react";
import { action, refreshTopics, useRun, useView } from "../../api";
import { AgentAvatar } from "../../components/Avatar";
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";
import { Icon } from "../../components/Icon";
import { ErrorText } from "../../components/ErrorText";
import { Pill } from "../../components/Pill";
import { Switch } from "../../components/Switch";
import { color } from "../../lib/color";
import { useDirectory } from "../../lib/directory";
import { go, useRouteChoice, useRouteParam } from "../../lib/nav";
import { pickKnown } from "../../lib/pickKnown";
import type { AgentRunConfig, AgentRunnerView } from "../../models";
import { PRIORITY_COLOR, PRIORITY_LABEL, TASK_COLOR, TASK_ICON, TASK_LABEL, leaseLabel, taskOpen, type AgentTask, type AgentTaskStatus } from "../../models";
import { relative } from "../../fmt";
import { RunnerStrip } from "./RunnerStrip";
import { taskNeedsHelp } from "./needsHelpModel";
import css from "./automation.module.css";
import { useAuto } from "./autoShared";
import { useIssueLabel, useIsIssueRef } from "../development/issueApi";
import { canGo, useFeatures } from "../../features";
import { rowButtonClass } from "../../components/RowButton";

const SCOPES = ["open", "all"] as const;
type Scope = (typeof SCOPES)[number];

/** openHelp:到「需要處理」頁籤並標示這個任務;openLog:到「執行日誌」頁籤 */
export function AutoDispatch({ openTask, openHelp, openLog }: { openTask: (task: AgentTask) => void; openHelp: (taskId: string) => void; openLog: () => void }) {
  const runnerOn = useFeatures().enabled("automation.runner"); // 系統設定 › AI 自動化 › 自動執行
  const d = useAuto();
  const issueLabel = useIssueLabel(), isIssueRef = useIsIssueRef();
  const dir = useDirectory();
  // 範圍與角色篩選存在網址(?scope=all&role=…):重新整理、上一頁都會還原;選角色用 push,其餘 replace
  const [scope, setScope] = useRouteChoice<Scope>("scope", SCOPES, "open");
  const [role, setRole] = useRouteParam("role");
  // 網址上的角色已不存在(被刪、過期的連結)就當作沒篩選,並用 replace 清掉(等角色載入後才判斷,Back 不會被卡住)
  const knownAgents = d.agents?.agents;
  const agentId = knownAgents ? pickKnown(role, knownAgents.map((a) => a.id)) ?? "" : role ?? "";
  useEffect(() => { if (knownAgents && role && !agentId) setRole(undefined, "replace"); }, [knownAgents, role, agentId, setRole]);
  // 各角色的自動/手動(AgentRunConfig.autoRun)與「開始執行」的前提(系統設定、claude、是否忙碌)
  const [rv] = useView<AgentRunnerView>("agent_runner", ["agents", "orgs", "system"]);
  const { busy, error, run } = useRun();
  if (!d.agents) {
    return d.agentsError ? <EmptyState title="讀不到虛擬角色" icon="exclamationmark.triangle" message={d.agentsError} /> : <div className={"muted " + css.loading}>載入中…</div>;
  }
  const { agents, tasks } = d.agents;
  const byId = new Map(agents.map((a) => [a.id, a]));
  const count = (id: string, st: AgentTaskStatus) => tasks.filter((t) => t.agentId === id && t.status === st).length;
  const rows = tasks
    .filter((t) => (scope === "all" || taskOpen(t.status)) && (!agentId || t.agentId === agentId))
    .sort((a, b) => (taskOpen(a.status) ? 0 : 1) - (taskOpen(b.status) ? 0 : 1) || b.updatedAt.localeCompare(a.updatedAt));
  const cfgOf = (id: string): AgentRunConfig => rv?.agents.find((x) => x.id === id)?.config ?? { autoRun: false, model: "", effort: "", workdir: "", allowBash: false, timeoutMinutes: 30 };
  const isAuto = (id: string) => cfgOf(id).autoRun;
  /** 只改 autoRun,其餘設定照原樣存回 */
  const setAuto = (id: string, v: boolean) => run(async () => {
    await action("agent_runner_save", { agent: id, ...cfgOf(id), autoRun: v });
    refreshTopics(["agent_runner"]); // 不等 SSE 通知:存好就重讀,開關馬上換成新狀態
  });
  /** 不能開始執行的原因(空白 = 可以) */
  const startBlock = (t: AgentTask): string => {
    if (!rv) return "載入中";
    if (rv.disabled) return rv.disabledReason ?? "「自動執行」在系統設定裡關閉了";
    if (!rv.claudeFound) return "找不到 claude(Claude Code CLI)";
    if (rv.claudeLoginChecking) return "正在確認 claude 是否已登入";
    if (!rv.claudeLoggedIn) return rv.claudeLoginHint ?? "claude 尚未登入";
    if (rv.agents.find((x) => x.id === t.agentId)?.active === false) return "角色已停用";
    if (rv.current) return `目前正在執行 ${rv.current.taskId},請等它做完或先停止`;
    return "";
  };
  const issueOf = (t: AgentTask) => t.links.find((l) => isIssueRef(l));

  return (
    <div className={"col gap12 " + css.tabPage}>
      {runnerOn && <RunnerStrip openLog={openLog} openTask={(id) => { const t = tasks.find((x) => x.id === id); if (t) openTask(t); }} />}
      <Card title="各角色目前的派發" subtitle="點角色可只看他被派的任務">
        {agents.length === 0 && <span className="muted">還沒有角色</span>}
        <div className="col">
          {agents.map((a) => {
            const open = tasks.filter((t) => t.agentId === a.id && taskOpen(t.status)).length;
            const auto = isAuto(a.id);
            return (
              <div key={a.id} className="row gap8">
                <button className={rowButtonClass + " grow" + (a.active ? "" : " " + css.inactive) + (agentId === a.id ? " " + css.agentPicked : "")}
                  onClick={() => setRole(agentId === a.id ? undefined : a.id, "push")}>
                  <AgentAvatar agent={a} size={24} />
                  <span className={"grow ellipsis " + css.left}>{a.name}<span className="caption muted"> · {a.role}</span></span>
                  {!a.active && <Pill text="停用" color="gray" />}
                  <Pill text={`待領取 ${count(a.id, "queued")}`} color="secondary" />
                  <Pill text={`進行中 ${count(a.id, "inProgress")}`} color="blue" />
                  {count(a.id, "blocked") > 0 && <Pill text={`需要協助 ${count(a.id, "blocked")}`} color="orange" />}
                  <span className={"caption muted num " + css.w60 + " " + css.right}>{open} 件未完成</span>
                </button>
                <label className="row gap6" title={auto ? "自動:派發後自動領取並執行" : "手動:派發後留在待領取,要在派發列表按「開始執行」"}>
                  <Switch on={auto} small disabled={!rv || busy} onChange={(v) => setAuto(a.id, v)} />
                  <span className={"caption " + (auto ? "" : "muted")}>{auto ? "自動" : "手動"}</span>
                </label>
              </div>
            );
          })}
        </div>
      </Card>
      <Card title="派發列表" subtitle={`${rows.length} 筆`}>
        <div className="row gap8">
          <select className="select" value={scope} onChange={(e) => setScope(e.target.value as Scope)}>
            <option value="open">只看未完成</option><option value="all">全部(含完成、取消)</option>
          </select>
          <select className="select" value={agentId} onChange={(e) => setRole(e.target.value || undefined, "push")}>
            <option value="">全部角色</option>{agents.map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
        </div>
        <ErrorText>{error}</ErrorText>
        {rows.length === 0 && <span className="muted">沒有符合的任務</span>}
        {rows.map((t) => {
          const a = byId.get(t.agentId);
          const iss = issueOf(t);
          return (
            <div key={t.id} className={css.lineRow}>
              <div className="row gap8">
                <div className={"row gap6 " + css.w150}>
                  {a ? <AgentAvatar agent={a} size={22} /> : <Icon name="person.crop.circle" size={20} className="muted" />}
                  <span className="ellipsis">{a?.name ?? t.agentId}</span>
                </div>
                <button className={rowButtonClass + " grow"} onClick={() => openTask(t)}>
                  <Icon name={TASK_ICON[t.status]} size={13} color={color(TASK_COLOR[t.status])} />
                  <span className="mono caption muted">{t.id}</span>
                  <span className={"ellipsis grow " + css.left + (taskOpen(t.status) ? "" : " muted")}>{t.title}</span>
                  {leaseLabel(t) && <span className="caption muted ellipsis" title="領取租約:鎖定期間不能被其他人領取">{leaseLabel(t)}</span>}
                </button>
                {t.status === "queued" && rv && (
                  <>
                    {isAuto(t.agentId) && <Pill text="自動" color="green" />}
                    <button className="btn small" disabled={busy || !!startBlock(t)} title={startBlock(t) || (isAuto(t.agentId) ? "不等自動執行的下一輪,現在就啟動" : "在這台 Mac 上啟動 Claude Code 以這個角色身分處理")}
                      onClick={() => run(async () => {
                        await action("agent_runner_start", { task: t.id });
                        refreshTopics(["agent_runner", "agents"]); // 「目前執行中」與按鈕的忙碌狀態馬上更新
                      })}>開始執行</button>
                  </>
                )}
                {iss && canGo("development") && <button className="btn link" onClick={() => go("development", iss)}>{issueLabel(iss)}</button>}
                {(t.priority === "urgent" || t.priority === "high") && <Pill text={PRIORITY_LABEL[t.priority]} color={PRIORITY_COLOR[t.priority]} />}
                {taskNeedsHelp(t)
                  ? <button className="btn link" title="到「需要處理」回覆這個任務" onClick={() => openHelp(t.id)}><Pill text={TASK_LABEL[t.status]} color={TASK_COLOR[t.status]} /></button>
                  : <Pill text={TASK_LABEL[t.status]} color={TASK_COLOR[t.status]} />}
                <span className={"caption muted ellipsis " + css.w110} title="派發者">{dir.authorName(t.createdBy)}</span>
                <span className={"caption muted " + css.w70 + " " + css.right}>{relative(t.updatedAt)}</span>
            </div>
            </div>
          );
        })}
      </Card>
    </div>
  );
}

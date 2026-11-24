// 需要處理的任務卡:角色回報「需要協助」(blocked)的任務,看得到問題、能直接回覆。
// 回覆(agent_task_reply):記成以你署名的備註、任務回到待領取;角色下次領取時會連同備註一起看到。
import { useEffect, useRef, useState } from "react";
import { action, useRun } from "../../api";
import { AgentAvatar } from "../../components/Avatar";
import { AutoTextarea } from "../../components/AutoTextarea";
import { ErrorText } from "../../components/ErrorText";
import { Icon } from "../../components/Icon";
import { relative } from "../../fmt";
import { canGo } from "../../features";
import { go } from "../../lib/nav";
import type { Agent, AgentTask } from "../../models";
import css from "./automation.module.css";
import { replyProblem } from "./needsHelpModel";
import { TaskQuestion } from "./TaskQuestion";
import { useIssueLabel, useIsIssueRef } from "../development/issueApi";

/** 一組需要處理的任務卡(AI 自動化的「需要處理」頁籤、開發進度的 Issue 詳情與待決定分頁共用);focusId 的那張捲到畫面內並標示 */
export function NeedsHelpList({ tasks, agents, openTask, focusId, showIssue = true }: {
  tasks: AgentTask[]; agents: Agent[]; openTask: (task: AgentTask) => void; focusId?: string; showIssue?: boolean;
}) {
  return (
    <div className="col gap12">
      {tasks.map((t) => <NeedsHelpItem key={t.id} task={t} agent={agents.find((a) => a.id === t.agentId)} openTask={openTask} focused={t.id === focusId} showIssue={showIssue} />)}
    </div>
  );
}

function NeedsHelpItem({ task, agent, openTask, focused, showIssue }: { task: AgentTask; agent?: Agent; openTask: (task: AgentTask) => void; focused: boolean; showIssue: boolean }) {
  const issueLabel = useIssueLabel(), isIssueRef = useIsIssueRef();
  const [text, setText] = useState("");
  const { busy, error, run } = useRun();
  const issue = task.links.find((l) => isIssueRef(l));
  const problem = replyProblem(text);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => { if (focused) ref.current?.scrollIntoView({ block: "center" }); }, [focused]);
  return (
    <div ref={ref} className={"col gap8 " + css.needsCard + (focused ? " " + css.focused : "")}>
      <div className="row gap8 wrap">
        {agent ? <AgentAvatar agent={agent} size={22} /> : <Icon name="person.crop.circle" size={20} className="muted" />}
        <button className="btn link" onClick={() => openTask(task)}><span className="mono">{task.id}</span> {task.title}</button>
        <span className="grow" />
        {showIssue && issue && canGo("development") && <button className="btn link" onClick={() => go("development", issue)}>{issueLabel(issue)}</button>}
        <span className="caption muted" title="角色回報卡住至今">{agent?.name ?? task.agentId} · 等了 {relative(task.updatedAt)}</span>
      </div>
      <TaskQuestion task={task} agentName={agent?.name ?? task.agentId} expanded />
      <AutoTextarea ariaLabel="回覆角色" placeholder="回覆角色(會記成你署名的備註,任務回到待領取,角色下次領取時看得到)" value={text} onChange={setText} />
      <div className="row gap8">
        <button className="btn small primary" disabled={busy || !!problem} title={problem || "送出回覆,任務回到待領取;自動的角色會接著執行,手動的要在派發列表按「開始執行」"}
          onClick={() => run(async () => { await action("agent_task_reply", { id: task.id, text }); setText(""); })}>回覆並重新執行</button>
        <button className="btn small danger" disabled={busy} onClick={() => run(() => action("agent_task_cancel", { id: task.id }))}>取消任務</button>
      </div>
      <ErrorText>{error}</ErrorText>
    </div>
  );
}

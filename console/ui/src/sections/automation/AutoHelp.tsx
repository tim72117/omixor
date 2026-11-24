// AI 自動化 › 需要處理:角色回報「需要協助」(blocked)的任務集中在這裡,看得到問題、能直接回覆。
// 回覆(agent_task_reply):記成以你署名的備註、任務回到待領取;角色下次領取時會連同備註一起看到。
import { Card } from "../../components/Card";
import { EmptyState } from "../../components/EmptyState";
import type { AgentTask } from "../../models";
import { NeedsHelpList } from "./NeedsHelp";
import { needsHelpTasks } from "./needsHelpModel";
import css from "./automation.module.css";
import { useAuto } from "./autoShared";

export function AutoHelp({ focusId, openTask }: { focusId?: string; openTask: (task: AgentTask) => void }) {
  const d = useAuto();
  if (!d.agents) return <div className={"muted " + css.loading}>{d.agentsError ?? "載入中…"}</div>;
  const list = needsHelpTasks(d.agents.tasks);
  if (list.length === 0) return <EmptyState title="沒有需要處理的任務" icon="checkmark.circle" message="角色回報卡住、需要你回覆的任務會出現在這裡。" />;
  return (
    <div className={"col gap12 " + css.tabPage}><Card title="需要處理" subtitle={`${list.length} 件:角色卡住了,等你回覆`}>
      <NeedsHelpList tasks={list} agents={d.agents.agents} openTask={openTask} focusId={focusId} />
    </Card></div>
  );
}

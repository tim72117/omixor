// 需要處理的資料 hook:AI 自動化 › 需要處理頁籤與開發進度(看板、Issue 詳情、待決定)共用。
// 讀的是 AI 自動化同一個 agents view(同一份快取、同樣聽 orgs 主題),回覆 / 取消後 agent_task_* 會通知 agents 主題,各處一起更新。
import { useMemo } from "react";
import { useView } from "../../api";
import { arr, type Agent, type AgentsView, type AgentTask } from "../../models";
import { findIssue, type Issue } from "../development/issueModel";
import { useIsIssueRef } from "../development/issueApi";
import { needsHelpByIssue, needsHelpTasks } from "./needsHelpModel";

export type NeedsHelpData = {
  agents: Agent[];
  /** 全部需要處理的任務(等最久的在前) */
  tasks: AgentTask[];
  /** Issue id → 它的需要處理任務 */
  byIssue: Map<string, AgentTask[]>;
};

export function useNeedsHelp(): NeedsHelpData {
  const [v] = useView<AgentsView>("agents", ["orgs"]);
  const [iv] = useView<Issue[]>("issues");
  const isIssueRef = useIsIssueRef();
  const issues = arr<Issue>(iv);
  return useMemo(() => {
    const all = v && Array.isArray(v.tasks) ? v.tasks : [];
    const resolve = (t: AgentTask) => findIssue(issues, t.links.find((l) => isIssueRef(l)))?.id;
    return { agents: v && Array.isArray(v.agents) ? v.agents : [], tasks: needsHelpTasks(all), byIssue: needsHelpByIssue(all, resolve) };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [v, iv]);
}

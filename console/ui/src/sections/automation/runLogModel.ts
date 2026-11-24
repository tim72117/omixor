// 「執行日誌」頁籤的純邏輯:結果的呈現、依角色與結果篩選歷史、歷史裡出現過的角色。
// 不碰 React、不碰網路,node 測試直接跑(contract/runLog.test.ts)。
import type { RunnerRecord } from "../../models";

/** 一筆執行的結果:顯示文字、顏色、圖示(沒列到的結果值照原字串顯示) */
export const OUTCOME: Record<string, { label: string; color: string; icon: string }> = {
  done: { label: "完成", color: "green", icon: "checkmark.circle" },
  blocked: { label: "需要協助", color: "orange", icon: "hand.raised" },
  error: { label: "錯誤", color: "red", icon: "exclamationmark.triangle" },
};
export const outcomeOf = (outcome: string) => OUTCOME[outcome] ?? { label: outcome, color: "gray", icon: "questionmark.circle" };

/** 結果篩選的選項(網址 ?outcome=…):all 不篩 */
export const OUTCOME_FILTERS = ["all", "done", "blocked", "error"] as const;
export type OutcomeFilter = (typeof OUTCOME_FILTERS)[number];
export const OUTCOME_FILTER_LABEL: Record<OutcomeFilter, string> = { all: "全部結果", done: OUTCOME.done.label, blocked: OUTCOME.blocked.label, error: OUTCOME.error.label };

/** 依角色(空字串 = 全部)與結果篩選,順序不變(後端已由新到舊) */
export const filterRuns = (history: RunnerRecord[], agentId: string, outcome: OutcomeFilter): RunnerRecord[] =>
  history.filter((r) => (!agentId || r.agentId === agentId) && (outcome === "all" || r.outcome === outcome));

/** 歷史裡出現過的角色(依第一次出現的順序,同一個角色只列一次) */
export function runRoles(history: RunnerRecord[]): { id: string; name: string }[] {
  const seen = new Map<string, string>();
  for (const r of history) if (!seen.has(r.agentId)) seen.set(r.agentId, r.agentName);
  return [...seen].map(([id, name]) => ({ id, name }));
}

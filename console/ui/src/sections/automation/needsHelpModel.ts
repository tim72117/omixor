// 「需要處理」(角色回報需要協助的任務)的純邏輯:哪些任務算、問題文字取哪裡、摺起時顯示多少、回覆的檢查。
// 不碰 React、不碰網路,node 測試直接跑(contract/needsHelp.test.ts)。派發頁的區塊、列上的原因與 AI 自動化頁籤上的數字都用這裡,不各算各的。
import type { AgentTask } from "../../models";

/** 需要人處理的任務:角色回報了 blocked(核心的徽章也是同一個條件:console/internal/core/agents.go 的 OnBadge) */
export const taskNeedsHelp = (t: Pick<AgentTask, "status">): boolean => t.status === "blocked";

/** 所有需要處理的任務,等最久的排最前面 */
export const needsHelpTasks = (tasks: AgentTask[]): AgentTask[] =>
  tasks.filter(taskNeedsHelp).sort((a, b) => a.updatedAt.localeCompare(b.updatedAt));

/** 依 Issue 分組需要處理的任務:resolve 把任務對到它所屬 Issue 的 id(對不到 = undefined,不收)。
 *  Issue 看板卡、Issue 詳情與待決定分頁都用這個,和 needsHelpTasks 同一個判斷、同樣等最久的在前 */
export function needsHelpByIssue(tasks: AgentTask[], resolve: (t: AgentTask) => string | undefined): Map<string, AgentTask[]> {
  const m = new Map<string, AgentTask[]>();
  for (const t of needsHelpTasks(tasks)) {
    const id = resolve(t);
    if (id) (m.get(id) ?? m.set(id, []).get(id)!).push(t);
  }
  return m;
}

/** 開發進度「待決定」分頁的待處理數:待處理的決定 + 需要處理的任務(兩邊都是「等人處理」) */
export const pendingCount = (openDecisions: number, helpTasks: number): number => openDecisions + helpTasks;

/** 回覆字數上限(同核心的 service.MaxTaskReplyRunes) */
export const REPLY_LIMIT = 4000;

/** 角色的問題:回報 blocked 時寫的結果(通常最完整);沒有就用最後一則備註。author 是備註作者(取自結果時為空,代表角色本人) */
export function blockQuestion(t: Pick<AgentTask, "result" | "notes">): { text: string; author?: string } {
  const result = (t.result ?? "").trim();
  if (result) return { text: result };
  const last = (t.notes ?? [])[(t.notes ?? []).length - 1];
  return last ? { text: last.text.trim(), author: last.author } : { text: "" };
}

/** 摺起時顯示的開頭:最多 lines 行、chars 字;truncated 表示後面還有內容(要提供展開) */
export function excerpt(text: string, lines = 2, chars = 160): { short: string; truncated: boolean } {
  let out = text.split("\n").slice(0, lines).join("\n");
  if (out.length > chars) out = out.slice(0, chars);
  return { short: out, truncated: out.length < text.length };
}

/** 回覆的問題(空字串 = 可以送出) */
export function replyProblem(text: string): string {
  const n = [...text.trim()].length;
  if (n === 0) return "要寫回覆內容";
  return n > REPLY_LIMIT ? `回覆太長(${n} / ${REPLY_LIMIT} 字)` : "";
}

// 待決定(issue_decision*)工具 → 核心 REST /api/orgs/{org}/decisions。給 adapter.ts 用。
import type { Call } from "./adapter.ts";

/** 同畫面的 DECISION_LIMIT(REST 回全部,超過的提示數字不顯示) */
const LIMIT = 200;

/** 不認得的名稱回 undefined */
export async function decisionTool(call: Call, name: string, args: Record<string, any>): Promise<unknown> {
  const one = (action: string) => `/decisions/${encodeURIComponent(String(args.id))}/${action}`;
  const rest = (keys: string[]) => Object.fromEntries(keys.filter((k) => args[k] !== undefined).map((k) => [k, args[k]]));
  switch (name) {
    case "issue_decisions": {
      const q = new URLSearchParams();
      if (args.status && args.status !== "all") q.set("status", args.status);
      if (args.issue) q.set("issue", args.issue);
      const s = q.toString();
      const list: any[] = (await call.get("/decisions" + (s ? "?" + s : ""))) ?? [];
      return { total: Math.min(list.length, LIMIT), decisions: list };
    }
    case "issue_decision_add":
      return call.post("/decisions", { ...rest(["kind", "title", "body", "options"]), ...(args.source_issue ? { sourceIssue: args.source_issue } : {}) });
    case "issue_decision_answer": return call.post(one("answer"), rest(["answer", "note", "evidence"]));
    case "issue_decision_withdraw": return call.post(one("withdraw"));
    case "issue_decision_reopen": return call.post(one("reopen"));
    case "issue_decision_to_issue": return call.post(one("to-issue"));
  }
  return undefined;
}

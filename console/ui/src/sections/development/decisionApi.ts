// 待決定 / 需要協助的網路呼叫與資料 hook(純邏輯在 decisionModel.ts)。
// 全部走既有的 MCP 工具(POST /api/tool/…:issue_decisions、issue_decision_add、issue_decision_answer、issue_decision_to_issue),
// 權限與可見性由後端判斷。撤回(issue_decision_withdraw)、重新開啟(issue_decision_reopen)是 console 自己登記的工具(spec/tools_console.json),account view 的 features 宣告了才會開放按鈕;摘要(summaries)沒有工具。
import { useCallback, useEffect, useState } from "react";
import { refreshTopics, tool, useVersion, useView } from "../../api";
import type { Account } from "../../auth/ConnectGate";
import type { Decision } from "../../models";
import type { Issue } from "./issueModel";
import { buildAddArgs, buildAnswerArgs, DECISION_LIMIT, type AnswerInput, type NewDraft } from "./decisionModel";
import { useIsOrgAdmin } from "./issueApi";

export type DecisionsResult = { total: number; decisions: Decision[] };

/** 全部待決定(含已處理、已撤回,上限 DECISION_LIMIT;篩選在畫面端)。資料版本變了就重讀;reload 手動重讀 */
export function useDecisions(): { list: Decision[] | undefined; total: number; error: string | undefined; reload: () => void } {
  const [res, setRes] = useState<DecisionsResult>();
  const [error, setError] = useState<string>();
  const [n, setN] = useState(0);
  const version = useVersion(500);
  useEffect(() => {
    let live = true;
    tool<DecisionsResult>("issue_decisions", { status: "all", limit: DECISION_LIMIT })
      .then((r) => { if (live) { setRes(r); setError(undefined); } })
      .catch((e) => { if (live) setError(String(e?.message ?? e)); });
    return () => { live = false; };
  }, [version, n]);
  const reload = useCallback(() => setN((x) => x + 1), []);
  return { list: res?.decisions, total: res?.total ?? 0, error, reload };
}

/** 目前使用者(能力偵測用 features;web console 的使用者都是人) */
export function useViewer() {
  const [acc] = useView<Account>("account", ["account"]);
  const isAdmin = useIsOrgAdmin();
  return { viewer: { me: acc?.self ?? acc?.email, isAdmin, isHuman: true }, features: acc?.features };
}

/** 動作之後:重讀待決定,也通知 issues 主題(回答會在來源 Issue 留言、由它開 Issue 會多一張 Issue) */
const touched = () => refreshTopics(["issues"]);

export const addDecision = async (d: NewDraft) => { const r = await tool<Decision>("issue_decision_add", buildAddArgs(d)); touched(); return r; };
export const answerDecision = async (d: Pick<Decision, "id" | "kind">, a: AnswerInput) => { const r = await tool<Decision>("issue_decision_answer", buildAnswerArgs(d, a)); touched(); return r; };
export const decisionToIssue = async (id: string) => {
  const r = await tool<{ issue: Issue; decision: Decision; message?: string }>("issue_decision_to_issue", { id });
  touched();
  return r;
};
/** 撤回、重新開啟:account view 的 features 宣告了對應工具(console 登記時自動宣告)才會被呼叫 */
export const withdrawDecision = async (id: string) => { const r = await tool<Decision>("issue_decision_withdraw", { id }); touched(); return r; };
export const reopenDecision = async (id: string) => { const r = await tool<Decision>("issue_decision_reopen", { id }); touched(); return r; };

/** 某張 Issue 的決定(Issue 詳情的摘要用;資料版本變了就重讀;讀不到就當作沒有) */
export function useIssueDecisions(issueKey: string): Decision[] | undefined {
  const [list, setList] = useState<Decision[]>();
  const version = useVersion(500);
  useEffect(() => {
    let live = true;
    tool<DecisionsResult>("issue_decisions", { issue: issueKey, status: "all", limit: DECISION_LIMIT })
      .then((r) => { if (live) setList(r.decisions ?? []); })
      .catch(() => { if (live) setList([]); });
    return () => { live = false; };
  }, [issueKey, version]);
  return list;
}

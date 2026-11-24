// Issue 詳情的待決定摘要:這張 Issue 有幾筆待處理 / 已處理的待決定或需要協助;按下去打開「待決定」分頁並只看這張 Issue 的。沒有任何一筆就不顯示。
import { SIcon } from "../../components/SIcon";
import { useDev } from "./devShared";
import { useIssueDecisions } from "./decisionApi";
import { issueSummary, summaryText } from "./decisionModel";

export function IssueDecisionSummary({ issueKey }: { issueKey: string }) {
  const dev = useDev();
  const list = useIssueDecisions(issueKey);
  if (!list) return null;
  const c = issueSummary(list, issueKey);
  if (c.all === 0) return null;
  return (
    <div className="row gap8 wrap">
      <span className="h3">待決定</span>
      <span className={"caption " + (c.open > 0 ? "" : "muted")}>{summaryText(c)}</span>
      <button className="btn link" onClick={() => dev.showDecisions(issueKey)}><SIcon name="questionmark.bubble" size={12} /> 查看</button>
    </div>
  );
}

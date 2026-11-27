// Issue 詳情的「接受 / 放回」按鈕與負責人標記。呼叫與規則見 issueAcceptApi.ts;權限由後端判斷,失敗時 run 會顯示後端的訊息。
import { Pill } from "../../components/Pill";
import { SIcon } from "../../components/SIcon";
import type { useAct } from "../../components/Toast";
import { whoName } from "./itemModel";
import { acceptIssue, releaseIssue } from "./issueAcceptApi";
import { useView } from "../../api";
import type { Account } from "../../auth/account";
import { useIsOrgAdmin } from "./issueApi";
import { acceptState, canRelease, supportsAccept } from "./issueAcceptModel";
import type { Issue } from "./issueModel";

type Run = ReturnType<typeof useAct>["run"];

/** 沒人負責且未關閉:「接受」;被人接受且我是接受者本人或組織管理員:「放回」。角色負責的不顯示(用指派選單)。
 * 伺服器沒宣告支援接受功能(舊伺服器、還沒偵測到)時兩個按鈕都不顯示 */
export function IssueAcceptButton({ issue, run }: { issue: Issue; run: Run }) {
  const [acc] = useView<Account>("account", ["account"]);
  const isAdmin = useIsOrgAdmin();
  if (!supportsAccept(acc?.features)) return null;
  const st = acceptState(issue);
  if (st === "none" && issue.status === "open")
    return <button className="btn" title="接受這張 Issue:你成為負責人、Issue 進行中(之後再拆子項)" onClick={() => run(() => acceptIssue(issue), "已接受 " + issue.key)}><SIcon name="hand.raised.fill" /> 接受</button>;
  if (st === "human" && canRelease(issue, acc?.self, isAdmin))
    return <button className="btn" title="放回:取消負責人、退回待處理(接受者本人或組織管理員)" onClick={() => run(() => releaseIssue(issue), "已放回 " + issue.key)}>放回</button>;
  return null;
}

/** 被人接受時顯示「接受:名稱」(角色負責的由原本的「指派」標記顯示) */
export function IssueAcceptedPill({ issue }: { issue: Issue }) {
  if (acceptState(issue) !== "human") return null;
  return <Pill text={"接受:" + whoName(issue.assignee, issue.assigneeActor)} color="purple" icon="person.crop.circle.badge.checkmark" />;
}

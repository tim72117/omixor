// Issue 接受 / 放回的純邏輯(不碰 React、不碰網路,node 測試直接跑):送出的編號、目前狀態判斷。呼叫在 issueAcceptApi.ts。
import type { Issue } from "./issueModel";

/** Issue key 的格式(同 issueModel 的 KEY_RE;這裡只用型別匯入 issueModel,node 測試才載得起來) */
const KEY = /^[A-Za-z][A-Za-z0-9]{0,9}-\d+$/;

/** 送給後端的 Issue 編號:一定是 key(ISS-12、APP-3),不送內部 UUID;沒有 key(舊伺服器)或格式不對就丟錯,不送出 */
export function acceptRef(issue: Pick<Issue, "key">): string {
  const k = issue.key?.trim() ?? "";
  if (!KEY.test(k)) throw new Error("這張 Issue 沒有編號(key),不能接受或放回");
  return k;
}

/** 接受的狀態:none = 沒人負責(可接受);human = 被人接受(畫面提供放回);role = 指派或被角色接受(用「指派給角色」選單改) */
export type AcceptState = "none" | "human" | "role";

export function acceptState(issue: Pick<Issue, "assignee" | "assigneeActor">): AcceptState {
  const a = issue.assigneeActor;
  if (a?.kind === "user") return "human";
  if (issue.assignee || a) return "role";
  return "none";
}

/** 伺服器宣告支援的功能代號(account view 的 features,來自核心 GET /api/version;舊伺服器沒有 = 偵測不到) */
export const FEATURE_ISSUE_ACCEPT = "issue_accept";

/** 這台伺服器支援接受 / 放回嗎。偵測不到(features 沒給、還沒查到、舊伺服器)一律當作不支援,按鈕不顯示 */
export const supportsAccept = (features?: readonly string[] | null): boolean => !!features?.includes(FEATURE_ISSUE_ACCEPT);

/** 放回按鈕要不要顯示:被人接受,而且我是接受者本人(assigneeActor 的 handle 或 email 等於我的 handle,不分大小寫)或組織管理員。
 * 後端仍會再檢查(別人 403),這裡只是不顯示沒權限的按鈕 */
export function canRelease(issue: Pick<Issue, "assignee" | "assigneeActor">, me: string | undefined, isAdmin: boolean): boolean {
  if (acceptState(issue) !== "human") return false;
  if (isAdmin) return true;
  const m = me?.trim().toLowerCase();
  const a = issue.assigneeActor;
  return !!m && !!a && [a.handle, a.email].some((v) => v?.trim().toLowerCase() === m);
}

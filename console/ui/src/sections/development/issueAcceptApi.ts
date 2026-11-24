// Issue 的「接受」與「放回」(核心 service/issue_accept.go;MCP 工具 issue_accept,畫面走 POST /api/tool/issue_accept)。
// 規則、授權與可見性都由後端判斷(已被別人接受 409、已關閉 409、放回只有接受者或組織管理員 403),失敗時顯示後端的訊息。
// 送出的一律是 Issue 編號 key(見 issueAcceptModel.ts 的 acceptRef),不送內部 UUID。
import { tool } from "../../api";
import { acceptRef } from "./issueAcceptModel";
import type { Issue } from "./issueModel";

/** 接受(你成為負責人、Issue 進行中)。已關閉的不能接受(後端也會擋) */
export const acceptIssue = (issue: Pick<Issue, "key">) => call({ id: acceptRef(issue) });

/** 放回(取消負責人、退回待處理) */
export const releaseIssue = (issue: Pick<Issue, "key">) => call({ id: acceptRef(issue), release: true });

/** 舊伺服器(沒有接受功能)由能力偵測擋掉:畫面偵測不到 issue_accept 就不顯示按鈕,console 後端也會直接回「這台伺服器還不支援接受功能」 */
const call = (args: Record<string, unknown>) => tool<Issue & { message?: string }>("issue_accept", { return: "full", ...args });

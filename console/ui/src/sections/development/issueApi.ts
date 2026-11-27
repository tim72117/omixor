// Issue 的網路呼叫與資料 hook(純邏輯在 issueModel.ts)
import { useState } from "react";
import { action, refreshTopics, tool, useActionQuery, useView } from "../../api";
import type { CabinetQuery, Project } from "../../models";
import { CONFLICT_MESSAGE, isIssueRef, isVersionConflict, issueRefLabel, UUID_RE, type Issue } from "./issueModel";

/** 修改 Issue;versioned 時帶上畫面讀到的 version(別人已經改過就丟出「這張 Issue 已被別人更新」,不會蓋掉)。留言不用帶 version(不算修改本體) */
export async function updateIssue<T = any>(issue: Pick<Issue, "id" | "version">, args: Record<string, unknown>, versioned = true): Promise<T> {
  const withVersion = versioned && issue.version && issue.version > 0 ? { version: issue.version } : {};
  try {
    return await tool<T>("issue_update", { id: issue.id, return: "full", ...withVersion, ...args });
  } catch (e) {
    if (isVersionConflict(e)) {
      // 畫面握著的是舊版本:主動重讀 issues(後端也會發通知,這裡再保險一次),否則之後每個快速動作都會 409
      refreshTopics(["issues"]);
      const err = new Error(CONFLICT_MESSAGE) as Error & { data?: unknown; status?: number };
      err.data = (e as any).data;
      err.status = (e as any).status;
      throw err;
    }
    throw e;
  }
}

/** 組織的專案清單(含代號)。後端有 projects view 就用它;還沒有時退回 cabinet_query 的 projects 欄位 */
export function useProjects(): Project[] {
  const [v, err] = useView<Project[]>("projects", ["cabinet", "orgs", "issues"]);
  const [cab] = useActionQuery<CabinetQuery>("cabinet_query", { project: "" }, ["cabinet", "orgs"]);
  if (Array.isArray(v) && !err) return v;
  return Array.isArray(cab?.projects) ? cab!.projects : [];
}

type AdminFlags = { canEdit?: boolean; orgAdmin?: boolean };
const useAdminFlags = () => useView<AdminFlags>("members", ["members", "orgs"])[0];

/** 是不是 web 管理員(管理員帳號名單;members view 的 canEdit):垃圾桶、Jira 匯入這類 web 端限管理員的功能 */
export function useIsConsoleAdmin(): boolean {
  return !!useAdminFlags()?.canEdit;
}

/** 是不是組織管理員(含 web 管理員):核心照組織角色判斷的操作(放回別人接受的 Issue、撤回與重新開啟待決定) */
export function useIsOrgAdmin(): boolean {
  const v = useAdminFlags();
  return !!(v?.canEdit || v?.orgAdmin);
}

/** 垃圾桶:已刪除的 Issue(只有 web 管理員) */
export const loadTrash = () => action<Issue[]>("issue_trash", {});
export const restoreIssue = (id: string) => action<Issue>("issue_restore", { id });
export const purgeIssue = (id: string) => action("issue_purge", { id });

/** 後端解析 Issue 編號(舊的 key、垃圾桶裡的 Issue):回 {id, key, deleted};找不到丟 404 的 ApiError */
export type IssueRefInfo = { id: string; key: string; deleted: boolean };
export const resolveIssueRef = (ref: string) => action<IssueRefInfo>("issue_resolve", { ref });

// 垃圾桶裡的 Issue 不在 issues 清單:UUID 參照問後端換成 key(結果快取,同一個 id 只問一次)
const resolvedKeys = new Map<string, string>();
const resolving = new Set<string>();

/** 頻道、任務連結只存一個 Issue 參照(id 或 key):回傳「參照 → 顯示用 key」的函式(清單裡找不到:UUID 問後端解析,其餘原樣顯示) */
export function useIssueLabel(): (ref: string) => string {
  const [v] = useView<Issue[]>("issues");
  const [, bump] = useState(0);
  const list = Array.isArray(v) ? v : [];
  return (ref) => {
    const label = issueRefLabel(ref, list);
    if (label !== ref || !UUID_RE.test(ref)) return label;
    const known = resolvedKeys.get(ref);
    if (known) return known;
    if (Array.isArray(v) && !resolving.has(ref)) { // 清單已經讀到、但裡面沒有:可能在垃圾桶
      resolving.add(ref);
      resolveIssueRef(ref).then((r) => { resolvedKeys.set(ref, r.key || ref); bump((n) => n + 1); }).catch(() => { resolvedKeys.set(ref, ref); }).finally(() => resolving.delete(ref));
    }
    return ref;
  };
}

/** 任務連結是不是 Issue 參照(UUID、ISS-n、已知專案代號的 <代號>-n,或清單裡找得到的) */
export function useIsIssueRef(): (link: string) => boolean {
  const [v] = useView<Issue[]>("issues");
  const projects = useProjects();
  const list = Array.isArray(v) ? v : [];
  const keys = projects.map((p) => p.key ?? "").filter(Boolean);
  return (link) => isIssueRef(link, list, keys);
}

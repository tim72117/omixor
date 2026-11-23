// Issue 子項(issue_item)的純邏輯(不碰 React、不碰網路):排序、前置是否擋住、證據顯示。
// 規則來源是 core(core/service/issue_items*.go):前置要 done 才能領取、完成(canceled 的前置也算沒完成);子項可見性繼承 Issue,由後端處理。
import type { IssueItem, ItemStatus } from "../../models";

/** 狀態徽章顏色(Pill 的顏色名稱) */
export const ITEM_STATUS_COLOR: Record<ItemStatus, string> = { todo: "secondary", doing: "blue", blocked: "orange", done: "green", canceled: "gray" };
export const ITEM_STATUS_ICON: Record<ItemStatus, string> = { todo: "circle", doing: "circle.dotted", blocked: "exclamationmark.triangle", done: "checkmark.circle", canceled: "xmark.circle" };

/** 已完成或取消(終態) */
export const itemTerminal = (i: Pick<IssueItem, "status">) => i.status === "done" || i.status === "canceled";

/** 依序號排序(後端已排,這裡保險) */
export const sortItems = (items: IssueItem[]): IssueItem[] => [...items].sort((a, b) => a.seq - b.seq);

/** 前置子項(同一張 Issue;清單裡找不到回 undefined) */
export const dependencyOf = (item: IssueItem, items: IssueItem[]): IssueItem | undefined =>
  item.dependsOn ? items.find((x) => x.key.toLowerCase() === item.dependsOn!.toLowerCase()) : undefined;

/** 被前置擋住:有前置而且前置不是 done(找不到前置時保守當作擋住,實際以後端為準) */
export const blockedByDependency = (item: IssueItem, items: IssueItem[]): boolean => {
  if (!item.dependsOn || itemTerminal(item)) return false;
  const dep = dependencyOf(item, items);
  return !dep || dep.status !== "done";
};

/** 完成證據:物件列成「欄位 → 文字」,字串原樣;沒有證據回 [] */
export function evidenceRows(ev: unknown): { key: string; text: string }[] {
  if (ev === null || ev === undefined || ev === "") return [];
  if (typeof ev === "string") return [{ key: "", text: ev }];
  if (typeof ev !== "object") return [{ key: "", text: String(ev) }];
  return Object.entries(ev as Record<string, unknown>).map(([key, v]) => ({
    key,
    text: Array.isArray(v) ? v.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join("\n") : typeof v === "string" ? v : JSON.stringify(v),
  }));
}

/** 人看得懂的負責人 / 領取者:優先 actor 物件的名稱,其次相容字串 */
export const whoName = (str?: string, actor?: { name?: string; handle?: string }): string => actor?.name || actor?.handle || str || "";

/** 清單的子項計數文字:「未完成/全部」;沒有子項回 "" */
export const itemsCountText = (i: { itemsOpen?: number; itemsTotal?: number }): string =>
  i.itemsTotal && i.itemsTotal > 0 ? `${i.itemsOpen ?? 0}/${i.itemsTotal}` : "";

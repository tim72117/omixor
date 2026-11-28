// Issue 子項的網路呼叫與資料 hook(純邏輯在 itemModel.ts)。全部走既有的 MCP 工具(POST /api/tool/…),權限、可見性、錯誤訊息都由後端判斷。
import { useCallback, useEffect, useState } from "react";
import { tool, useVersion } from "../../api";
import { isRefused } from "../../lib/viewStore";
import type { IssueItem, ItemWaitKind } from "../../models";
import { sortItems } from "./itemModel";

export type IssueItemsResult = { issue: string; total: number; open: number; items: IssueItem[] };

/** 某張 Issue 的全部子項(含完成、取消;依序號)。後端資料版本變了(任何人改了子項)就重讀;error 是後端的訊息 */
export function useIssueItems(issueKey: string): { items: IssueItem[] | undefined; error: string | undefined; reload: () => void } {
  const [items, setItems] = useState<IssueItem[]>();
  const [error, setError] = useState<string>();
  const [n, setN] = useState(0);
  const version = useVersion(500);
  useEffect(() => {
    let live = true;
    tool<IssueItemsResult>("issue_items", { issue: issueKey, include_done: true })
      .then((r) => { if (live) { setItems(sortItems(r.items ?? [])); setError(undefined); } })
      .catch((e) => {
        if (!live) return;
        setError(String(e?.message ?? e));
        if (isRefused(e)) setItems(undefined); // 看不到了:手上的內容不留
      });
    return () => { live = false; };
  }, [issueKey, version, n]);
  const reload = useCallback(() => setN((x) => x + 1), []);
  return { items, error, reload };
}

/** 領取(開始做);負責人是角色時後端會排任務並發憑證(畫面不處理憑證) */
export const claimItem = (id: string) => tool<{ item: IssueItem; message?: string }>("issue_item_claim", { id });

/** 回報完成:證據一定要填(文字) */
export const reportDone = (id: string, evidence: string, note?: string) =>
  tool<IssueItem>("issue_item_report", { id, status: "done", evidence, ...(note ? { note } : {}) });

/** 回報卡住:原因必填;等待對象選填 */
export const reportBlocked = (id: string, blockReason: string, waitForKind?: ItemWaitKind, waitFor?: string) =>
  tool<IssueItem>("issue_item_report", { id, status: "blocked", block_reason: blockReason, ...(waitForKind ? { wait_for_kind: waitForKind } : {}), ...(waitFor ? { wait_for: waitFor } : {}) });

/** 放回待領取(或解除卡住) */
export const releaseItem = (id: string) => tool<IssueItem>("issue_item_report", { id, status: "todo" });

/** 取消子項(放棄這項工作) */
export const cancelItem = (id: string) => tool<IssueItem>("issue_item_update", { id, cancel: true });

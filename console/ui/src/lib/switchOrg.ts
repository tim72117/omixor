// 切換目前組織(側欄與 設定 › 組織 共用)
import { useEffect } from "react";
import { action, useView } from "../api";
import type { OrgIndex } from "../models";
import { selectOrg, selectedOrg } from "./orgSelection";

/** perUser(啟用 LDAP):只換自己的選擇,重新載入整頁(不留上一個組織的快取與畫面狀態);否則換伺服器的目前組織,畫面靠通知重讀 */
export async function switchOrg(id: string, perUser?: boolean) {
  await action("org_switch", { id });
  if (!perUser) return;
  selectOrg(id);
  window.location.reload();
}

/**
 * 啟用 LDAP 而且選的不是系統工作組織:整台一份的功能(總覽、CI/CD、伺服器、監控、自動化流程…)在這裡不能用
 * (後端回 403 notSystemOrg,分類見 internal/web/orgscope.go),畫面不顯示它們。組織清單還沒讀到時是 false
 */
export function useOutsideSystemOrg(): boolean {
  const [idx] = useView<OrgIndex>("orgs");
  return !!idx?.perUser && idx.current !== idx.system;
}

/** 還沒選過時記下伺服器給的組織,之後系統工作組織換了也不會跟著跑 */
export function usePinSelectedOrg(idx: OrgIndex | undefined) {
  useEffect(() => {
    if (idx?.perUser && idx.current && !selectedOrg()) selectOrg(idx.current);
  }, [idx?.perUser, idx?.current]);
}

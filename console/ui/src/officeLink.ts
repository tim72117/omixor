// 3D 辦公室入口:載入時問一次 /api/office/state,可用才顯示按鈕(不輪詢、不跳錯誤)。
// 後端找不到 office3d 時 /api/office/state 仍回 200,但 available 為 false;舊版後端沒有這支 API,會回非 2xx 或非 JSON。
// 401 由 auth/AuthGate 對所有 /api/ 請求統一處理,這裡不另外處理。
import { useEffect, useState } from "react";
import { useView } from "./api";
import { onServer } from "./server/scope";

/** 虛擬辦公室整台一份,要能讀系統工作組織;讀不到的人不問(不然是 403) */
export function useOfficeAvailable(): boolean {
  const [ok, setOk] = useState(false);
  const [idx] = useView<{ perUser?: boolean; system?: string }>("orgs");
  const mayRead = !onServer() && !!idx && (!idx.perUser || !!idx.system);
  useEffect(() => {
    if (!mayRead) return;
    let alive = true;
    fetch("/api/office/state")
      .then((r) => (r.ok ? r.json() : null))
      .then((b) => { if (alive && b && b.available === true) setOk(true); })
      .catch(() => { /* 不可用就不顯示 */ });
    return () => { alive = false; };
  }, [mayRead]);
  return ok;
}

export const openOffice = () => { window.open("/office/", "_blank", "noopener"); };

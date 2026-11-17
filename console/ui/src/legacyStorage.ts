// 產品早期叫 Ops:localStorage 的鍵名以前是 ops.*(ops.settings.*、ops.showAssistant),現在是 omixor.*。
// 要在其他模組讀 localStorage 之前跑(main.tsx 第一個 import),把舊鍵搬到新鍵:新鍵已經有就不蓋;搬完刪掉舊鍵
// (不刪的話,設定頁「回復預設」清掉新鍵後,下次又會從舊鍵搬回來)。可以重跑,讀寫失敗就略過。
const OLD = "ops.";
const NEW = "omixor.";

export function migrateLegacyStorage(): void {
  try {
    const keys: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k && k.startsWith(OLD)) keys.push(k);
    }
    for (const k of keys) {
      const nk = NEW + k.slice(OLD.length);
      const v = localStorage.getItem(k);
      if (v != null && localStorage.getItem(nk) == null) localStorage.setItem(nk, v);
      localStorage.removeItem(k);
    }
  } catch {
    /* 無法讀寫 localStorage(例如隱私模式)就不搬 */
  }
}

migrateLegacyStorage();

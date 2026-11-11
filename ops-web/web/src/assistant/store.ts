// 助理面板是否顯示(⌘J、設定 › 一般 都會改它),存在 localStorage
import { useSyncExternalStore } from "react";

const KEY = "ops.showAssistant";
const subs = new Set<() => void>();
let visible = (() => { try { return localStorage.getItem(KEY) !== "false"; } catch { return true; } })();

export function setAssistantVisible(v: boolean) {
  visible = v;
  try { localStorage.setItem(KEY, String(v)); } catch { /* 無法儲存就只放在記憶體 */ }
  subs.forEach((f) => f());
}

export function useAssistantVisible(): [boolean, (v: boolean) => void] {
  const v = useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => visible);
  return [v, setAssistantVisible];
}

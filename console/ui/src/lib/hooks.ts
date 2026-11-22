// 通用 hooks(原本在 bits.tsx / omixorKit.tsx)
import { useEffect, useState } from "react";
import { useView } from "../api";

/** 每隔 ms 毫秒重畫一次的現在時間(倒數、經過時間用) */
export function useNow(ms = 1000): number {
  const [n, setN] = useState(Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setN(Date.now()), ms);
    return () => window.clearInterval(id);
  }, [ms]);
  return n;
}

/** 存在 localStorage 的狀態(對應 @AppStorage) */
export function useStored<T>(key: string, initial: T): [T, (v: T) => void] {
  const [v, setV] = useState<T>(() => {
    try {
      const s = localStorage.getItem(key);
      return s == null ? initial : (JSON.parse(s) as T);
    } catch {
      return initial;
    }
  });
  const set = (x: T) => {
    setV(x);
    try { localStorage.setItem(key, JSON.stringify(x)); } catch { /* 無法儲存就只放在記憶體 */ }
  };
  return [v, set];
}

/** 元素寬度(做兩欄/一欄切換);回傳 callback ref,元素晚出現也能量到 */
export function useWidth<T extends HTMLElement>(): [(el: T | null) => void, number] {
  const [el, setEl] = useState<T | null>(null);
  const [w, setW] = useState(1000);
  useEffect(() => {
    if (!el) return;
    setW(el.getBoundingClientRect().width);
    const ro = new ResizeObserver((es) => setW(es[0].contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, [el]);
  return [setEl, w];
}

/** 讀 view,後端還沒有這個 view 或格式不對時回傳空陣列(畫面顯示空狀態);第三個值是「已經有結果(資料或錯誤)」 */
export function useList<T>(name: string, topics: string[] = []): [T[], string | undefined, boolean] {
  const [d, err] = useView<T[]>(name, topics);
  return [Array.isArray(d) ? d : [], err, d !== undefined || err !== undefined];
}

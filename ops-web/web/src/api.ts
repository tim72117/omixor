// 和 Go 後端(internal/web)溝通。
//   useView(名稱)      讀 GET /api/view/<名稱>,收到 SSE 通知(主題 = view 名稱,或 "*")就自動重讀
//   tool(名稱, 參數)    以目前使用者身分執行 MCP 工具(POST /api/tool/<名稱>)
//   action(名稱, 參數)  只有畫面能做的動作(POST /api/action/<名稱>)
import { useEffect, useRef, useState, useSyncExternalStore } from "react";

type Listener = (topics: string[]) => void;
const listeners = new Set<Listener>();
let connected = false;
let version = 0;
const versionSubs = new Set<() => void>();

function connect() {
  if (connected) return;
  connected = true;
  const es = new EventSource("/api/events");
  es.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data) as { version: number; topics: string[] };
      version = ev.version;
      versionSubs.forEach((f) => f());
      listeners.forEach((l) => l(ev.topics ?? []));
    } catch {
      /* 忽略格式錯誤的事件 */
    }
  };
  es.onerror = () => {
    // 瀏覽器會自動重連;重連後補一次全部重讀
    es.addEventListener("open", () => listeners.forEach((l) => l(["*"])), { once: true });
  };
}

/**
 * 後端資料版本(任何變更都會加一)。throttleMs:最多多久更新一次(伺服器數據每 3 秒變一次,
 * 依版本重算的東西,例如助理摘要,不需要跟這麼快)
 */
export function useVersion(throttleMs = 0): number {
  connect();
  const live = useSyncExternalStore(
    (f) => (versionSubs.add(f), () => versionSubs.delete(f)),
    () => version,
  );
  const [shown, setShown] = useState(live);
  const last = useRef(0);
  useEffect(() => {
    if (!throttleMs) return;
    const wait = Math.max(0, last.current + throttleMs - Date.now());
    const t = window.setTimeout(() => { last.current = Date.now(); setShown(live); }, wait);
    return () => window.clearTimeout(t);
  }, [live, throttleMs]);
  return throttleMs ? shown : live;
}

export class ApiError extends Error {}

async function getJSON<T>(url: string): Promise<T> {
  const r = await fetch(url);
  const body = await r.json();
  if (!r.ok) throw new ApiError(body?.error ?? `HTTP ${r.status}`);
  return body as T;
}

async function post<T>(url: string, args: unknown): Promise<T> {
  const r = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(args ?? {}),
  });
  const body = await r.json();
  if (body?.error) throw new ApiError(body.error);
  return body.result as T;
}

/** 以目前使用者身分執行 MCP 工具;失敗時丟出 ApiError(訊息和 MCP 的錯誤一樣) */
export const tool = <T = any>(name: string, args: Record<string, unknown> = {}) => post<T>(`/api/tool/${name}`, args);

/** 只有畫面能做的動作(核准、回滾、切換組織、登入…) */
export const action = <T = any>(name: string, args: Record<string, unknown> = {}) => post<T>(`/api/action/${name}`, args);

/**
 * 讀 view;topics 是另外要監聽的主題(預設只聽自己的名稱與 "*");opts.throttleMs 限制重讀頻率。
 * 回傳 [資料, 錯誤, 重新讀取];資料第一次讀到前是 undefined。
 */
export function useView<T = any>(name: string, topics: string[] = [], opts: { throttleMs?: number } = {}): [T | undefined, string | undefined, () => void] {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<string>();
  const seq = useRef(0);
  const lastLoad = useRef(0);
  const load = () => {
    lastLoad.current = Date.now();
    const n = ++seq.current;
    getJSON<T>(`/api/view/${name}`)
      .then((d) => n === seq.current && (setData(d), setError(undefined)))
      .catch((e) => n === seq.current && setError(String(e.message ?? e)));
  };
  useEffect(() => {
    connect();
    load();
    const watch = new Set([name, ...topics]);
    let timer: number | undefined;
    const l: Listener = (ts) => {
      if (ts.includes("*") || ts.some((t) => watch.has(t))) {
        // 短時間內多次通知合併成一次重讀;有 throttleMs 時兩次重讀至少隔這麼久
        window.clearTimeout(timer);
        const wait = Math.max(80, lastLoad.current + (opts.throttleMs ?? 0) - Date.now());
        timer = window.setTimeout(load, wait);
      }
    };
    listeners.add(l);
    return () => {
      listeners.delete(l);
      window.clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name, topics.join(",")]);
  return [data, error, load];
}

/** 執行非同步動作並追蹤忙碌與錯誤(按鈕用) */
export function useRun() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const run = async <T,>(f: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true);
    setError(undefined);
    try {
      return await f();
    } catch (e: any) {
      setError(String(e?.message ?? e));
      return undefined;
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, run, setError };
}

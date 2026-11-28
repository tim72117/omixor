// 和 Go 後端溝通。console(internal/web)照下面的 /api/view、action、tool;omixor server(/console)由 server/adapter.ts 轉成核心 REST,
// 沒有 SSE,改成定期與分頁取得焦點時重讀。元件不用知道是哪一種。
//   useView(名稱)      讀 GET /api/view/<名稱>,收到 SSE 通知(主題 = view 名稱,或 "*")就自動重讀
//   tool(名稱, 參數)    以目前使用者身分執行 MCP 工具(POST /api/tool/<名稱>)
//   action(名稱, 參數)  只有畫面能做的動作(POST /api/action/<名稱>)
//   useActionQuery(名稱, 參數, 主題)  用參數查詢的 action(例如 issue_board),參數變了或收到主題通知就重查
//
// useView 與 useActionQuery 共用 lib/viewStore.ts 的快取:同一個 view(或同一組 action + 參數)不論幾個元件在看,
// 只發一個請求、收到通知只重讀一次;沒人看的不重讀(下次有人看再讀)。
// SSE:伺服器每次連上(包括斷線重連)都先送一個 "*";畫面自己不再補送。瀏覽器放棄的連線(401 等)由
// lib/eventStream.ts 重建,登入狀態變了(auth/auth.ts)就重建或關掉。
// 資料回應帶版本(X-Omixor-Version),版本已經涵蓋的通知不重讀,所以頁面打開時不會「掛載讀一次 + 連上 "*" 再讀一次」。
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { ViewStore, type ViewSnapshot, type ViewStatus } from "./lib/viewStore";
import { actionQuery, viewQuery, type StoreQuery } from "./lib/queries";
import { ApiError, getJSON as getHTTP, postJSON as postHTTP } from "./lib/http";
import { createEventStream, type EventSourceLike, type EventStream } from "./lib/eventStream";
import { createOrgScope, type OrgScopeIndex } from "./lib/orgScope";
import { authReady, currentAuth, onLoginChange, refreshAuth } from "./auth/auth";
import { createServerAdapter } from "./server/adapter";
import { selectOrg, selectedOrg } from "./lib/orgSelection";

export { ApiError, unwrapPost } from "./lib/http";
export type { ViewSnapshot, ViewStatus } from "./lib/viewStore";

const server = createServerAdapter({
  http: { get: async (url) => (await getHTTP<unknown>(url)).data, post: async (url, body) => (await postHTTP<unknown>(url, body)).data },
  org: { selected: selectedOrg, select: selectOrg, reload: () => window.location.reload() },
  user: () => currentAuth()?.user,
  origin: typeof window === "undefined" ? "" : window.location.origin,
});
const isServer = async () => (await authReady())?.mode === "server";

async function getJSON<T>(url: string): Promise<{ data: T; version?: number }> {
  return (await isServer()) ? { data: (await server.get(url)) as T } : getHTTP<T>(url);
}

async function postJSON<T>(url: string, args: unknown): Promise<{ data: T; version?: number }> {
  if (!(await isServer())) return postHTTP<T>(url, args);
  const r = await server.post(url, args);
  if (r.write) refreshAll();
  return { data: r.data as T };
}

/** 全部 view / action 查詢共用的快取(除錯時可以看 viewStore.requests) */
export const viewStore = new ViewStore({ debounceMs: 80 });

// 非系統工作組織時不發整台一份的 view 請求(lib/orgScope.ts)
const orgScope = createOrgScope(async () => (await getJSON<OrgScopeIndex>("/api/view/orgs")).data);

let version = 0;
const versionSubs = new Set<() => void>();
let events: EventStream | undefined;

/** omixor server 沒有變更通知:重讀所有有人在看的資料(版本加一,照版本重讀的也跟著重讀) */
function refreshAll() {
  server.invalidate();
  version++;
  versionSubs.forEach((f) => f());
  viewStore.notify(["*"]);
}

const POLL_MS = 45_000;
let polling = false;
function startPolling() {
  if (polling) return;
  polling = true;
  let last = Date.now();
  const tick = (minGap: number) => {
    if (document.visibilityState !== "visible" || Date.now() - last < minGap) return;
    last = Date.now();
    void refreshAuth().catch(() => {}); // session 過期、別的分頁換人登入
    refreshAll();
  };
  window.setInterval(() => tick(POLL_MS - 1000), 5000);
  window.addEventListener("focus", () => tick(5000));
  document.addEventListener("visibilitychange", () => tick(5000));
}

function connect() {
  if (currentAuth()?.mode === "server") return startPolling();
  if (events || typeof EventSource === "undefined") return events?.start();
  events = createEventStream({
    open: () => new EventSource("/api/events") as unknown as EventSourceLike,
    message: (data) => {
      try {
        const ev = JSON.parse(data) as { version: number; topics: string[] };
        version = ev.version;
        versionSubs.forEach((f) => f());
        if (ev.topics?.some((t) => t === "orgs" || t === "*")) orgScope.invalidate();
        viewStore.notify(ev.topics ?? [], typeof ev.version === "number" ? ev.version : undefined);
      } catch {
        /* 忽略格式錯誤的事件 */
      }
    },
    // 伺服器連上時會送 "*"(internal/web/web.go),畫面不用自己補。
    // 斷線期間伺服器可能重啟(版本從頭算),手上的版本作廢,讓重連時的 "*" 一律重讀。
    lost: () => viewStore.resetVersions(),
    gaveUp: () => void refreshAuth().catch(() => {}),
  });
  events.start();
}

// 登入成功:重建通知連線,並讓登入前(或過期期間)讀失敗、過時的資料重讀;登出:關掉
onLoginChange((loggedIn) => {
  orgScope.invalidate();
  if (!events) return;
  if (!loggedIn) return events.stop();
  events.restart();
  viewStore.notify(["*"]);
});

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

/** 手動通知某些主題的資料變了(和收到伺服器的 SSE 通知一樣:訂閱這些主題的 view 會重讀)。版本衝突後用來讓畫面重讀最新內容 */
export function refreshTopics(topics: string[]) {
  viewStore.notify(topics);
}

/**
 * 以目前使用者身分執行 MCP 工具;失敗時丟出 ApiError(訊息和 MCP 的錯誤一樣)。
 * 沒給型別參數時回傳 unknown:要用回傳值就寫 tool<型別>(…)(型別在 models/)
 */
export const tool = async <T = unknown>(name: string, args: Record<string, unknown> = {}): Promise<T> =>
  (await postJSON<T>(`/api/tool/${name}`, args)).data;

/** 上傳圖片到目前組織的附件(PNG、JPEG、GIF、WebP,上限 10 MB),回傳 /api/attachments/… 網址 */
export async function uploadImage(file: Blob): Promise<string> {
  if (file.size > 10 * 1024 * 1024) throw new ApiError("圖片太大,上限 10 MB");
  const data = await new Promise<string>((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => resolve(String(fr.result).replace(/^data:[^,]*,/, ""));
    fr.onerror = () => reject(new ApiError("讀不到檔案"));
    fr.readAsDataURL(file);
  });
  const r = await postJSON<{ url: string }>("/api/attachments", { data });
  return r.data.url;
}

/**
 * 只有畫面能做的動作(核准、回滾、切換組織、登入…)。
 * 沒給型別參數時回傳 unknown:要用回傳值就寫 action<型別>(…)(型別在 models/)
 */
export const action = async <T = unknown>(name: string, args: Record<string, unknown> = {}): Promise<T> =>
  (await postJSON<T>(`/api/action/${name}`, args)).data;

/**
 * useView / useActionQuery 的回傳值:舊的 tuple 寫法 `const [data, err, reload] = useView(…)` 照用,
 * 也可以用欄位 `const v = useView(…); v.status`(搭配 components/ViewState)
 */
export type ViewResult<T> = [T | undefined, string | undefined, () => void] & {
  data: T | undefined;
  error: string | undefined;
  /** 有請求進行中(包括已經有資料、正在重讀) */
  loading: boolean;
  /** loading:還沒有資料也沒有錯誤;error:最後一次讀取失敗(data 可能還是上一次的);ready:有資料 */
  status: ViewStatus;
  reload: () => void;
};

function result<T>(snap: ViewSnapshot<T>, data: T | undefined, reload: () => void): ViewResult<T> {
  const status: ViewStatus = snap.error !== undefined ? "error" : data === undefined ? "loading" : "ready";
  return Object.assign([data, snap.error, reload] as [T | undefined, string | undefined, () => void], {
    data, error: snap.error, loading: snap.loading, status, reload,
  });
}

function useStoreKey<T>({ key, load, ownTopics }: StoreQuery, topics: string[], throttleMs: number) {
  connect();
  const topicsKey = topics.join(",");
  const ownKey = ownTopics.join(",");
  const subscribe = useCallback(
    (cb: () => void) =>
      // load 只由 key 決定(lib/queries.ts),所以取這個 key 那次 render 的即可;不能用 ref 指向最新的參數
      viewStore.subscribe(key, load, ownKey ? ownKey.split(",") : [], { topics: topicsKey ? topicsKey.split(",") : [], throttleMs }, cb),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [key, ownKey, topicsKey, throttleMs],
  );
  const snap = useSyncExternalStore(subscribe, () => viewStore.get<T>(key));
  const reload = useCallback(() => viewStore.reload(key), [key]);
  return { snap, reload };
}

/**
 * 讀 view;topics 是另外要監聽的主題(預設只聽自己的名稱與 "*");opts.throttleMs 限制重讀頻率。
 * 回傳 [資料, 錯誤, 重新讀取](另有 .status .loading 等欄位,見 ViewResult);資料第一次讀到前是 undefined。
 */
export function useView<T = any>(name: string, topics: string[] = [], opts: { throttleMs?: number } = {}): ViewResult<T> {
  const query = viewQuery(name, () => orgScope.load(name, () => getJSON<T>(`/api/view/${name}`)));
  const { snap, reload } = useStoreKey<T>(query, topics, opts.throttleMs ?? 0);
  return result(snap, snap.data, reload);
}

/**
 * 用參數呼叫一個「查詢用」的 action(例如 issue_query、issue_board:規則在後端,畫面只顯示結果),
 * 參數變了、或收到 topics 的 SSE 通知(或 "*")就重新呼叫;舊的請求晚回來會丟掉。和 useView 共用快取(key = 名稱 + 參數)。
 * 回傳 [資料, 錯誤, 重新讀取];資料第一次讀到前是 undefined(之後換參數、重新讀取期間保留上一次的結果)。
 */
export function useActionQuery<T = any>(name: string, args: Record<string, unknown>, topics: string[] = []): ViewResult<T> {
  const query = actionQuery(name, args, (path, body) => postJSON<T>(path, body));
  const { snap, reload } = useStoreKey<T>(query, topics, 0);
  // 換參數時新的結果還沒回來:先顯示上一組參數的結果(搜尋打字時清單不閃空白)。
  // 只記是哪個 key、內容向快取拿,不自己留一份:那份資料被丟掉(看不到了、過時了)這裡也跟著沒有
  const lastKey = useRef<string>();
  if (snap.data !== undefined) lastKey.current = query.key;
  const previous = lastKey.current === undefined ? undefined : viewStore.get<T>(lastKey.current).data;
  return result(snap, snap.data ?? previous, reload);
}

/** value 停止變動 ms 毫秒後才回傳新的值(搜尋輸入用) */
export function useDebounced<T>(value: T, ms = 250): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(t);
  }, [value, ms]);
  return v;
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

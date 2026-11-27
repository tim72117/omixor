// 共用的 view 快取(api.ts 的 useView / useActionQuery 用;不依賴 React 與瀏覽器,測試在 contract/viewStore.test.ts)。
//
// 每個 key(view 名稱,或 action 名稱 + 參數)一筆:{data, error, loading, version, status}。
//   - 同一個 key 不論幾個元件在看,只發一個請求(進行中的請求共用、讀到的結果共用)。
//   - SSE 通知(notify)只讓「有人在看」的 key 重讀;沒人看的整筆丟掉,下次有人訂閱才重讀。
//     看不到的資料(例如被移出受限專案)不能留在快取裡,所以:
//       * 伺服器拒絕讀取(isRefused:401 / 403 / 404 / 410)時丟掉手上的資料
//       * 沒人看的筆數不保留錯誤,也不保留「已知過時」的資料;沒有任何主題能讓它失效的不快取
//   - 一個 key 的 load 只能讀那個 key 的資料(lib/queries.ts 保證):不能跟著元件目前的參數走。
//   - 晚回來的舊請求丟掉(每個 key 一個序號)。
//   - 通知在 debounceMs 內合併成一次重讀;訂閱者給 throttleMs 時,兩次重讀至少隔這麼久(取所有訂閱者裡最小的)。
//   - 版本:伺服器在回應標頭帶資料版本(X-Omixor-Version,算資料前讀的,所以資料至少反映到這個版本)。
//     通知的版本 ≤ 手上資料的版本就不重讀:頁面一打開時「掛載時讀一次 + SSE 連上時伺服器送的 "*"」不會讀兩次。
//     標頭沒有(舊版伺服器、被代理拿掉)時版本是 -1,行為和以前一樣(收到通知就重讀)。

export type LoadResult = { data: unknown; version?: number };
export type ViewStatus = "loading" | "error" | "ready";

export type ViewSnapshot<T = unknown> = {
  /** 最後一次成功讀到的資料;第一次讀到前是 undefined(之後重讀失敗也保留,伺服器拒絕讀取除外) */
  data: T | undefined;
  /** 最後一次讀取的錯誤訊息;成功後清掉 */
  error: string | undefined;
  /** 有請求進行中 */
  loading: boolean;
  /** data 對應的後端資料版本;不知道是 -1 */
  version: number;
  /** loading:還沒有資料也沒有錯誤;error:最後一次讀取失敗;ready:有資料 */
  status: ViewStatus;
};

export type StoreClock = {
  setTimeout: (f: () => void, ms: number) => unknown;
  clearTimeout: (id: unknown) => void;
  now: () => number;
};

export type SubscribeOptions = {
  /** 除了 key 自己的主題以外要聽的主題("*" 一律會聽) */
  topics?: string[];
  /** 兩次重讀至少隔多久 */
  throttleMs?: number;
};

/** 伺服器拒絕讓這個使用者讀(登出、沒權限、不存在或看不到):之前讀到的內容不能再留著。暫時性的錯誤(5xx、斷線)不算 */
export function isRefused(e: unknown): boolean {
  const status = (e as { status?: unknown } | null)?.status;
  return status === 401 || status === 403 || status === 404 || status === 410;
}

type Sub = { topics: string[]; throttleMs: number; onChange: () => void };

type Entry = {
  key: string;
  load: () => Promise<LoadResult>;
  /** 讓這筆失效的主題:key 自己的,加上所有訂閱過的人聽的(沒人看之後也要認得出來) */
  topics: Set<string>;
  snap: ViewSnapshot;
  /** 資料版本(和 snap.version 一樣,但斷線時會重設成 -1,不動 snap) */
  dataVersion: number;
  subs: Set<Sub>;
  seq: number;
  /** 進行中的請求序號;沒有是 0 */
  inflight: number;
  /** 等著要的版本;-1 沒有、Infinity 一定要重讀(手動 refreshTopics) */
  want: number;
  /** 計時到了但還有請求進行中:等它回來再決定要不要重讀 */
  afterInflight: boolean;
  timer: unknown;
  lastLoad: number;
  idleSince: number;
};

const EMPTY: ViewSnapshot = Object.freeze({ data: undefined, error: undefined, loading: false, version: -1, status: "loading" }) as ViewSnapshot;

const statusOf = (data: unknown, error: string | undefined): ViewStatus => (error !== undefined ? "error" : data === undefined ? "loading" : "ready");

function errorText(e: unknown): string {
  const m = (e as { message?: unknown } | null)?.message;
  return String(m ?? e);
}

export class ViewStore {
  private entries = new Map<string, Entry>();
  private clock: StoreClock;
  private debounceMs: number;
  private maxIdle: number;
  /** 測試與除錯用:總共發出幾個請求 */
  requests = 0;

  constructor(opts: { clock?: StoreClock; debounceMs?: number; maxIdle?: number } = {}) {
    this.clock = opts.clock ?? {
      setTimeout: (f, ms) => globalThis.setTimeout(f, ms),
      clearTimeout: (id) => globalThis.clearTimeout(id as ReturnType<typeof setTimeout>),
      now: () => Date.now(),
    };
    this.debounceMs = opts.debounceMs ?? 80;
    this.maxIdle = opts.maxIdle ?? 64;
  }

  /** 目前的快照;同一份資料回傳同一個物件(給 useSyncExternalStore) */
  get<T = unknown>(key: string): ViewSnapshot<T> {
    return (this.entries.get(key)?.snap ?? EMPTY) as ViewSnapshot<T>;
  }

  /**
   * 訂閱一個 key;快取裡沒有(或上次失敗)時才讀。回傳取消訂閱。
   * load 只在建立這筆時記下,所以必須只由 key 決定(見 lib/queries.ts),ownTopics 是 key 自己的主題(view 名稱;action 查詢沒有)。
   */
  subscribe(key: string, load: () => Promise<LoadResult>, ownTopics: string[], opts: SubscribeOptions, onChange: () => void): () => void {
    const e = this.entry(key, load, ownTopics);
    for (const t of opts.topics ?? []) e.topics.add(t);
    const sub: Sub = { topics: opts.topics ?? [], throttleMs: opts.throttleMs ?? 0, onChange };
    e.subs.add(sub);
    if (e.inflight) {
      if (e.want >= 0) e.afterInflight = true; // 進行中的請求可能比通知早發,回來後再看要不要重讀
    } else if (e.snap.error !== undefined || e.snap.data === undefined) {
      this.start(e);
    }
    return () => {
      if (!e.subs.delete(sub) || e.subs.size) return;
      if (this.outdated(e)) return this.drop(e);
      if (e.timer !== undefined) {
        this.clock.clearTimeout(e.timer);
        e.timer = undefined;
      }
      e.idleSince = this.clock.now();
      this.evict();
    };
  }

  /** 馬上重讀(畫面做完動作後用);進行中的舊請求晚回來會丟掉 */
  reload(key: string) {
    const e = this.entries.get(key);
    if (e) this.start(e);
  }

  /**
   * 主題的資料變了(SSE 通知或 refreshTopics)。version:通知的資料版本;沒給表示一定要重讀。
   * 有人看的 key 排定重讀,沒人看的丟掉。
   */
  notify(topics: string[], version?: number) {
    const all = topics.includes("*");
    for (const e of [...this.entries.values()]) {
      if (!all && !topics.some((t) => e.topics.has(t))) continue;
      const v = version === undefined ? Infinity : version;
      if (v <= e.dataVersion && e.want < 0) continue; // 手上的資料已經包含這次變更
      if (!e.subs.size) {
        this.drop(e);
        continue;
      }
      e.want = Math.max(e.want, v);
      this.schedule(e);
    }
  }

  /** 和伺服器斷線:手上的版本不能再拿來比(伺服器可能重啟、版本從頭算),重連時伺服器送的 "*" 一律重讀 */
  resetVersions() {
    for (const e of this.entries.values()) e.dataVersion = -1;
  }

  /** 測試用 */
  has(key: string) {
    return this.entries.has(key);
  }

  private entry(key: string, load: () => Promise<LoadResult>, ownTopics: string[]): Entry {
    let e = this.entries.get(key);
    if (!e) {
      e = {
        key, load, topics: new Set(ownTopics), snap: EMPTY, dataVersion: -1, subs: new Set(), seq: 0, inflight: 0, want: -1,
        afterInflight: false, timer: undefined, lastLoad: 0, idleSince: 0,
      };
      this.entries.set(key, e);
    }
    return e;
  }

  /** 沒人看之後不能留著的:上次失敗、有還沒反映的變更、或沒有任何主題能讓它失效 */
  private outdated(e: Entry) {
    return e.snap.error !== undefined || (e.want >= 0 && e.want > e.dataVersion) || !e.topics.size;
  }

  /** 整筆丟掉(進行中的請求回來也沒人讀得到);只給沒人看的用 */
  private drop(e: Entry) {
    if (e.timer !== undefined) {
      this.clock.clearTimeout(e.timer);
      e.timer = undefined;
    }
    if (this.entries.get(e.key) === e) this.entries.delete(e.key);
  }

  private schedule(e: Entry) {
    if (e.timer !== undefined) this.clock.clearTimeout(e.timer);
    let throttle = Infinity;
    for (const s of e.subs) throttle = Math.min(throttle, s.throttleMs);
    if (!Number.isFinite(throttle)) throttle = 0;
    const wait = Math.max(this.debounceMs, e.lastLoad + throttle - this.clock.now());
    e.timer = this.clock.setTimeout(() => {
      e.timer = undefined;
      if (e.inflight) e.afterInflight = true;
      else this.maybeStart(e);
    }, wait);
  }

  /** 計時到了:還要的版本比手上的新才讀 */
  private maybeStart(e: Entry) {
    if (e.want < 0) return;
    if (e.want <= e.dataVersion) {
      e.want = -1;
      return;
    }
    if (!e.subs.size) return this.drop(e);
    this.start(e);
  }

  private start(e: Entry) {
    if (e.timer !== undefined) {
      this.clock.clearTimeout(e.timer);
      e.timer = undefined;
    }
    const n = ++e.seq;
    e.inflight = n;
    e.want = -1;
    e.afterInflight = false;
    e.lastLoad = this.clock.now();
    this.requests++;
    if (!e.snap.loading) this.set(e, { ...e.snap, loading: true });
    let p: Promise<LoadResult>;
    try {
      p = e.load();
    } catch (err) {
      p = Promise.reject(err);
    }
    p.then(
      (r) => {
        if (n !== e.seq) return; // 有更新的請求,這個晚回來的丟掉
        const version = typeof r.version === "number" && Number.isFinite(r.version) ? r.version : -1;
        e.dataVersion = version;
        this.finish(e, { data: r.data, error: undefined, loading: false, version, status: statusOf(r.data, undefined) });
      },
      (err) => {
        if (n !== e.seq) return;
        const error = errorText(err);
        if (!isRefused(err)) return this.finish(e, { ...e.snap, error, loading: false, status: statusOf(e.snap.data, error) });
        e.dataVersion = -1;
        this.finish(e, { data: undefined, error, loading: false, version: -1, status: "error" });
      },
    );
  }

  private finish(e: Entry, snap: ViewSnapshot) {
    e.inflight = 0;
    this.set(e, snap);
    if (e.afterInflight) {
      e.afterInflight = false;
      this.maybeStart(e);
    }
    if (!e.subs.size && !e.inflight && this.outdated(e)) this.drop(e);
  }

  private set(e: Entry, snap: ViewSnapshot) {
    e.snap = snap;
    for (const s of [...e.subs]) s.onChange();
  }

  /** 沒人看的筆數超過 maxIdle 時,丟掉最久沒人看的(action 查詢的參數組合很多,例如搜尋字) */
  private evict() {
    const idle = [...this.entries.values()].filter((e) => !e.subs.size);
    if (idle.length <= this.maxIdle) return;
    idle.sort((a, b) => a.idleSince - b.idleSince);
    for (const e of idle.slice(0, idle.length - this.maxIdle)) this.drop(e);
  }
}

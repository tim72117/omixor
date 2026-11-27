// 伺服器通知(SSE)的連線管理(api.ts 用;不依賴 React,測試在 contract/eventStream.test.ts)。
//
// 瀏覽器的 EventSource 只在「網路斷線」時自己重連(readyState = CONNECTING);伺服器回 401(還沒登入、session 過期、
// 重啟後 session 沒了)或代理回 502 時直接放棄(CLOSED),之後永遠沒有通知。這裡在放棄後自己重建(間隔加倍,有上限),
// 登入成功時馬上重建(restart)。

export type EventSourceLike = {
  onmessage: ((e: { data: string }) => void) | null;
  onopen: ((e?: unknown) => void) | null;
  onerror: ((e?: unknown) => void) | null;
  readyState: number;
  close(): void;
};

/** EventSource.CLOSED */
const CLOSED = 2;

export type EventStreamOptions = {
  open: () => EventSourceLike;
  message: (data: string) => void;
  /** 連線中斷(不論瀏覽器會不會自己重連):手上的資料版本作廢 */
  lost: () => void;
  /** 瀏覽器放棄了(多半是 401):讓呼叫端確認登入狀態 */
  gaveUp?: () => void;
  setTimeout?: (f: () => void, ms: number) => unknown;
  clearTimeout?: (id: unknown) => void;
  minDelayMs?: number;
  maxDelayMs?: number;
};

export type EventStream = {
  /** 還沒開始就開始(重複呼叫沒有作用;stop 之後也不會自己開始) */
  start(): void;
  /** 丟掉現在的連線,馬上建一條新的(登入成功後) */
  restart(): void;
  /** 關掉,不再重連(登出後) */
  stop(): void;
};

export function createEventStream(o: EventStreamOptions): EventStream {
  const later = o.setTimeout ?? ((f: () => void, ms: number) => globalThis.setTimeout(f, ms));
  const cancel = o.clearTimeout ?? ((id: unknown) => globalThis.clearTimeout(id as ReturnType<typeof setTimeout>));
  const min = o.minDelayMs ?? 1000;
  const max = o.maxDelayMs ?? 15000;
  let source: EventSourceLike | undefined;
  let timer: unknown;
  let delay = min;
  let started = false;

  const drop = () => {
    if (timer !== undefined) cancel(timer);
    timer = undefined;
    if (!source) return;
    source.onmessage = source.onopen = source.onerror = null;
    source.close();
    source = undefined;
  };

  const connect = () => {
    drop();
    const es = o.open();
    source = es;
    es.onopen = () => { delay = min; };
    es.onmessage = (e) => o.message(e.data);
    es.onerror = () => {
      o.lost();
      if (es !== source || es.readyState !== CLOSED) return; // 瀏覽器會自己重連
      drop();
      timer = later(() => { timer = undefined; connect(); }, delay);
      delay = Math.min(delay * 2, max);
      o.gaveUp?.();
    };
  };

  return {
    start() {
      if (started) return;
      started = true;
      connect();
    },
    restart() {
      started = true;
      delay = min;
      o.lost();
      connect();
    },
    stop() {
      drop();
    },
  };
}

// 這個分頁「目前在哪一頁」的狀態,連到瀏覽器歷史(上一頁 / 下一頁)。不碰 window / React:環境從 RouteEnv 注入,
// 所以可以用假的歷史單元測試(contract/navStore.test.ts)。真正的環境與 React hooks 在 lib/nav.ts。
//
// 規則(改這裡前先讀):
//   1. 網址是這個分頁自己位置的唯一依據(source of truth)。畫面照 snapshot().route 畫,不是照後端的 nav。
//      啟動時:網址有合法的路由就用它(重新整理會留在同一頁);沒有就等後端的 nav 到了採用,並用 replaceState 補上網址。
//   2. 使用者操作(點連結、側邊欄)= pushState 新增一筆歷史;並呼叫後端 navigate(MCP、Mac 看的是後端的目前頁)。
//   3. 瀏覽器上一頁 / 下一頁(popstate)= 只更新 snapshot,絕不 push;順便告訴後端目前頁(同第 2 點,但不新增歷史)。
//   4. 後端推來的導覽(MCP 或別處呼叫 navigate)才會讓畫面跟著走,但:
//        - 自己剛送出的 navigate 回來的通知(echo)忽略,否則會迴圈或把連點兩次的前一次又切回去(sent 佇列)
//        - 只有「看得到的分頁」才跟(document 可見);背景分頁不跟,所以兩個分頁不會互相拉扯
//        - 跟過去是 push,而且不再回送後端(後端本來就在那)
//   5. 子狀態(分頁、選中項目)放在網址的 query,見 setParams:切分頁用 replace、選項目用 push;
//      還帶著一次性 focusId 的那筆歷史一律 replace(把「意圖」換成解開後的狀態,避免上一頁又觸發一次)。
//   6. 歷史深度存在 history.state({ omx: n }),不用 window.history.length:canGoBack = 還有應用程式內的上一頁。
import { encodeRoute, makeRoute, parseRoute, sameRoute, withParams, withoutFocus, type Route } from "./navRoute.ts"; // .ts 副檔名:測試用 Node 直接執行(tsconfig 的 allowImportingTsExtensions)

/** 寫進 history.state 的內容:這一筆歷史在本應用內的深度(第一筆 0) */
export type NavState = { omx: number };

export type RouteEnv = {
  hash(): string;
  state(): unknown;
  /** hash 是 "#/…";null = 網址不動(只換 state) */
  push(state: NavState, hash: string): void;
  replace(state: NavState, hash: string | null): void;
  back(): void;
  /** 瀏覽器上一頁 / 下一頁(popstate)時呼叫 cb;回傳取消訂閱 */
  onPop(cb: () => void): () => void;
  /** 這個分頁現在看得到(不是背景分頁) */
  visible(): boolean;
  now(): number;
};

export type RouteSnapshot = { route: Route | null; depth: number; canGoBack: boolean };

/** 後端的導覽狀態(只用到這兩個欄位) */
export type BackendNav = { section: string; focusId?: string };

export type RouteStoreOptions = {
  isSection: (s: string) => boolean;
  /** 網址無效時退回的頁面 */
  fallbackSection: string;
  /** 告訴後端目前頁(後端的 navigate action) */
  announce: (r: Route) => Promise<unknown>;
};

/** 自己送出的 navigate 的 echo 最多等這麼久;過了就當作是別處的導覽 */
export const ECHO_TTL_MS = 10_000;

const backendKey = (section: string, focusId?: string) => `${section}\u0000${focusId ?? ""}`;
const routeKey = (r: Route) => backendKey(r.section, r.focusId);
const depthOf = (s: unknown): number | undefined => {
  const n = (s as Partial<NavState> | null)?.omx;
  return typeof n === "number" && Number.isInteger(n) && n >= 0 ? n : undefined;
};

export class RouteStore {
  private snap: RouteSnapshot = { route: null, depth: 0, canGoBack: false };
  private subs = new Set<() => void>();
  private stop?: () => void;
  private sent: { key: string; at: number }[] = [];
  private lastBackend?: string;

  // 不用參數屬性(private env…):測試用 Node 的 strip-types 直接執行,不支援那個語法
  private env: RouteEnv;
  private opts: RouteStoreOptions;

  constructor(env: RouteEnv, opts: RouteStoreOptions) {
    this.env = env;
    this.opts = opts;
  }

  getSnapshot = (): RouteSnapshot => this.snap;
  subscribe = (cb: () => void) => { this.subs.add(cb); return () => { this.subs.delete(cb); }; };

  /** 啟動:讀網址、開始聽上一頁 / 下一頁。回傳停止函式 */
  start(): () => void {
    this.stop?.();
    this.stop = this.env.onPop(this.onPop);
    const depth = depthOf(this.env.state()) ?? 0;
    const route = parseRoute(this.env.hash(), this.opts.isSection);
    if (route) {
      this.set(route, depth);
      this.env.replace({ omx: depth }, encodeRoute(route)); // 網址正規化(排序、補編碼);不新增歷史
      this.sendQuietly(route);
    } else {
      this.set(null, depth);
    }
    return () => { this.stop?.(); this.stop = undefined; };
  }

  /** 使用者導覽:新增一筆歷史(跟目前一樣就不新增)並告訴後端。功能開關的檢查在 lib/nav.ts 的 guardedNavigate */
  navigate(section: string, focusId?: string | null): Promise<unknown> {
    const next = makeRoute(section, focusId);
    const cur = this.snap.route;
    if (!cur || !sameRoute(cur, next)) this.commit(next, "push");
    return this.send(next);
  }

  /** 功能被關掉等原因要換頁:取代目前這筆(不新增歷史),並告訴後端 */
  redirect(section: string): void {
    const next = makeRoute(section);
    this.commit(next, "replace");
    this.sendQuietly(next);
  }

  /**
   * 改頁面自己的子狀態(patch 裡 undefined / 空字串 = 移除)。不告訴後端(子狀態只存在網址)。
   * mode:"replace" 給切分頁這類(不填滿歷史)、"push" 給選中項目這類(上一頁回到沒選的狀態)。
   * 目前這筆還帶著一次性 focusId 時一律 replace,並把 focusId 拿掉(同一個意圖解開成狀態,不另外記一筆)。
   */
  setParams(patch: Readonly<Record<string, string | undefined>>, mode: "push" | "replace"): void {
    const cur = this.snap.route;
    if (!cur) return;
    const next = withParams(cur, patch);
    if (sameRoute(next, cur)) return;
    this.commit(next, cur.focusId ? "replace" : mode);
    if (cur.focusId) this.sendQuietly(next); // 後端也不要再留著用掉的 focusId
  }

  /** 頁面用完 focusId 後呼叫:只改目前這筆(不新增歷史),後端的 focusId 一併清掉 */
  clearFocus(): void {
    const cur = this.snap.route;
    if (!cur?.focusId) return;
    const next = withoutFocus(cur);
    this.commit(next, "replace");
    this.sendQuietly(next);
  }

  back(): void {
    if (this.snap.canGoBack) this.env.back();
  }

  /** 後端目前頁(view「nav」)有更新時呼叫;規則見檔頭第 4 點 */
  onBackendNav(nav: BackendNav | undefined): void {
    if (!nav?.section) return;
    const key = backendKey(nav.section, nav.focusId || undefined);
    const first = this.lastBackend === undefined;
    const changed = key !== this.lastBackend;
    this.lastBackend = key;
    const target = makeRoute(this.opts.isSection(nav.section) ? nav.section : this.opts.fallbackSection, nav.focusId);
    const cur = this.snap.route;
    if (!cur) { this.commit(target, "replace"); return; } // 啟動時網址沒有路由:採用後端的目前頁
    if (first || !changed || this.consumeEcho(key) || routeKey(target) === routeKey(cur) || !this.env.visible()) return;
    this.commit(target, "push");
  }

  // ---- 內部 ----

  private onPop = () => {
    const parsed = parseRoute(this.env.hash(), this.opts.isSection);
    const known = depthOf(this.env.state());
    // 沒有 state = 使用者手動改網址產生的新一筆:當作在目前深度之上
    const depth = known ?? this.snap.depth + 1;
    const route = parsed ?? makeRoute(this.opts.fallbackSection);
    this.set(route, depth);
    if (!parsed || known === undefined) this.env.replace({ omx: depth }, encodeRoute(route));
    this.sendQuietly(route);
  };

  private commit(route: Route, mode: "push" | "replace"): void {
    const hash = encodeRoute(route);
    const cur = this.snap.route;
    if (mode === "push" && cur) {
      const depth = this.snap.depth + 1;
      this.env.push({ omx: depth }, hash);
      this.set(route, depth);
    } else {
      this.env.replace({ omx: this.snap.depth }, hash);
      this.set(route, this.snap.depth);
    }
  }

  private set(route: Route | null, depth: number): void {
    this.snap = { route, depth, canGoBack: depth > 0 };
    this.subs.forEach((f) => f());
  }

  private send(r: Route): Promise<unknown> {
    const now = this.env.now();
    this.sent = this.sent.filter((e) => now - e.at < ECHO_TTL_MS);
    this.sent.push({ key: routeKey(r), at: now });
    return this.opts.announce(r);
  }

  private sendQuietly(r: Route): void { this.send(r).catch(() => undefined); }

  /** 這個後端導覽是自己送的 echo 嗎:是的話連同更早送的一起清掉(中間的 echo 可能被合併而沒收到) */
  private consumeEcho(key: string): boolean {
    const now = this.env.now();
    const i = this.sent.findIndex((e) => e.key === key && now - e.at < ECHO_TTL_MS);
    if (i < 0) return false;
    this.sent = this.sent.slice(i + 1);
    return true;
  }
}

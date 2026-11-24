// 導覽的唯一入口:換頁(go / guardedNavigate)、目前所在頁(useRoute)、上一頁(goBack)、頁面自己的子狀態(useRouteParam)。
// 規則與迴圈防護寫在 lib/navStore.ts 檔頭,網址格式在 lib/navRoute.ts。這裡只負責接上瀏覽器與 React:
//   - browserEnv:window.history / location / popstate
//   - guardedNavigate:功能開關(系統設定關掉的頁面不跳、不新增歷史,改在畫面上方提示)
//   - useNavSync:App 呼叫一次,把後端的 nav 餵給 store,並把網址上被關掉的頁面換成總覽
import { useCallback, useEffect, useSyncExternalStore } from "react";
import { action } from "../api";
import { featuresNow, showFeatureNotice, sectionName, useFeatures } from "../features";
import { SECTIONS, type Nav, type SectionId } from "../types";
import { type Route } from "./navRoute";
import { RouteStore, type NavState, type RouteEnv, type RouteSnapshot } from "./navStore";

const browserEnv = (): RouteEnv => ({
  hash: () => window.location.hash,
  state: () => window.history.state,
  // 只傳 hash:路徑與 query 不動,後端的 SPA 路由不受影響
  push: (state: NavState, hash) => window.history.pushState(state, "", hash),
  replace: (state: NavState, hash) => window.history.replaceState(state, "", hash ?? undefined),
  back: () => window.history.back(),
  onPop: (cb) => { window.addEventListener("popstate", cb); return () => window.removeEventListener("popstate", cb); },
  visible: () => document.visibilityState === "visible",
  now: () => Date.now(),
});

const SECTION_IDS = new Set<string>(SECTIONS.map((s) => s.id));

/** 後端的 navigate 只收 section 與 focusId(env、timeRange 是別的設定,不屬於路由) */
const announce = (r: Route) => action("navigate", { section: r.section, focusId: r.focusId ?? "" });

export const routeStore = new RouteStore(browserEnv(), { isSection: (s) => SECTION_IDS.has(s), fallbackSection: "overview", announce });
routeStore.start();

/** 跳到別頁並選中項目(Swift 的 app.go);系統設定關掉的頁面不跳、不新增歷史,改在畫面上方提示 */
export function guardedNavigate(section: SectionId | string, focusId?: string | null): Promise<unknown> {
  if (!featuresNow().visible(section)) {
    showFeatureNotice(`「${sectionName(section)}」在系統設定裡關閉了`);
    return Promise.resolve(undefined);
  }
  return routeStore.navigate(section, focusId);
}

export const go = (section: SectionId, focusId?: string) => guardedNavigate(section, focusId);

/** 頁面用完 focusId 之後呼叫(傳給頁面的 clearFocus):不新增歷史 */
export const clearFocus = () => routeStore.clearFocus();

/** 回到應用程式內的上一頁(等同瀏覽器的上一頁) */
export const goBack = () => routeStore.back();

/** 這個分頁目前的路由與歷史深度(還沒決定時 route 是 null) */
export function useRoute(): RouteSnapshot {
  return useSyncExternalStore(routeStore.subscribe, routeStore.getSnapshot);
}

/**
 * App 呼叫一次:
 *   1. 把後端的目前頁餵給 store(啟動時網址沒有路由就採用它;之後 MCP 等推來的導覽,看得到的分頁才會跟)
 *   2. 網址上的頁面被系統設定關掉(功能開關讀到之後才判斷)就換成總覽,不新增歷史
 * 回傳目前的路由快照(App 照它畫)
 */
export function useNavSync(backend: Nav | undefined): RouteSnapshot {
  const flags = useFeatures();
  const snap = useRoute();
  const { route } = snap;
  useEffect(() => { routeStore.onBackendNav(backend); }, [backend?.section, backend?.focusId]); // eslint-disable-line react-hooks/exhaustive-deps
  const blocked = !!route && !flags.loading && !flags.visible(route.section);
  useEffect(() => { if (blocked) routeStore.redirect("overview"); }, [blocked]);
  return snap;
}

// ---- 頁面自己的子狀態(存在網址的 query,重新整理、上一頁都會還原) ----

export type SetMode = "push" | "replace";

/**
 * 讀寫網址 query 裡的一個值。set(value, mode):
 *   "replace"(預設)= 切分頁、篩選這類:不新增歷史,上一頁直接離開這一頁
 *   "push"        = 選中某個項目、開啟明細這類:上一頁回到「沒選」的狀態
 * value 是 undefined / 空字串 = 移除。同一個值不會重複寫入。key 只允許小寫英數與底線。
 */
export function useRouteParam(key: string): [string | undefined, (value: string | undefined, mode?: SetMode) => void] {
  const value = useRoute().route?.params[key];
  const set = useCallback((v: string | undefined, mode: SetMode = "replace") => routeStore.setParams({ [key]: v }, mode), [key]);
  return [value, set];
}

/** useRouteParam 的列舉版:網址上不是 allowed 裡的值就當作 fallback;等於 fallback 時不寫進網址 */
export function useRouteChoice<T extends string>(key: string, allowed: readonly T[], fallback: T): [T, (value: T, mode?: SetMode) => void] {
  const [raw, setRaw] = useRouteParam(key);
  const value = allowed.find((a) => a === raw) ?? fallback;
  const set = useCallback((v: T, mode?: SetMode) => setRaw(v === fallback ? undefined : v, mode), [setRaw, fallback]);
  return [value, set];
}

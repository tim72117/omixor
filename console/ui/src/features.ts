// 功能開關:清單與兩層開關都在核心(core/model/features.go),後端 view「system」轉給畫面
// (console/internal/core/system.go;讀不到時後端已換成最後一份或保守預設,source / error 說明原因)。
//
//   系統層(server):伺服器管理員在「系統」頁決定這台伺服器開放哪些功能
//   組織層(最上層的 features):組織管理員在「設定 › 功能」開關自己組織要用的功能
//
// 功能開不開 = 系統層開放 && 組織層開(核心已把兩層與頁面規則算進 enabled;這裡再看一次 serverFeatures)。
// 組織層的清單只列系統層開放的功能:有 key 但清單沒列的(系統層沒開放)當作沒有,不顯示。
// 頁面:總覽、設定、系統永遠顯示;其他照 enabled(頁面關了、或它的分頁全關都不顯示)。
// 分頁:key 是「頁面.分頁」;web 才有、核心沒登記的分頁(目前沒有)不給 key,永遠顯示。
// 選用模組(後端編進來才有,例如 integrations = 開發進度 › Repositories(development.repos)與 設定 › 程式碼倉庫):
// view 的 modules 列出編進來的模組、webOnlyTabs 列出模組登記的 web 獨有分頁(原本的開發進度 › GitLab 已併進 Repositories,現在是空的);
// 沒編進來的模組,它的功能不在清單裡(分頁自然不顯示),設定頁也用 hasModule 隱藏。
//
// 全畫面只讀一次 view(App 裡的 <FeaturesSync />),其他地方用 useFeatures() / canGo() 讀同一份。
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { action, useView } from "./api";
import { backendHas } from "./server/scope";
import type { SectionId } from "./types";

export type FeatureState = {
  key: string;
  name: string;
  description: string;
  parent?: string;
  edition?: string;
  experimental: boolean;
  experimentalNote?: string;
  default: boolean;
  required?: boolean;
  /** 實際開不開(畫面照這個顯示) */
  enabled: boolean;
  /** 這個功能自己的開關(系統頁的開關照這個) */
  on: boolean;
  /** 組織改過(false = 用預設) */
  changed: boolean;
  /** 系統層有開放(組織層清單只列有開放的) */
  system?: boolean;
};

/** 系統層的一個功能 */
export type ServerFeatureState = Omit<FeatureState, "enabled" | "changed" | "system"> & {
  /** 系統層沒設定過時開放不開放(原本正式版的開放、原本只有開發版的不開放) */
  systemDefault: boolean;
  /** 系統層這個功能自己的開關(系統頁的開關照這個) */
  on: boolean;
  /** 這台伺服器實際有開放(分頁還要所屬頁面開放) */
  available: boolean;
  /** 伺服器管理員改過 */
  changed: boolean;
};

/** 系統層(這台伺服器開放哪些功能) */
export type ServerView = {
  features: ServerFeatureState[];
  saved: boolean;
  updatedAt?: string;
  updatedBy?: string;
  /** 只有剛從核心讀到、伺服器管理員、而且 web 的登入者是管理員才是 true */
  canEdit: boolean;
  readOnlyReason?: string;
  source: "core" | "cached" | "fallback" | "loading";
  error: string;
};

export type SystemView = {
  features: FeatureState[];
  saved: boolean;
  updatedAt?: string;
  updatedBy?: string;
  /** 過時(核心保留給舊用戶端;不再有編譯版本) */
  edition?: string;
  /** 每個登記的功能在系統層開放不開放(舊版 server 沒有這個欄位時當作都開放,照 enabled) */
  serverFeatures?: Record<string, boolean>;
  /** 系統層(系統頁用;舊版 server 讀不到時是保守預設,error 說明) */
  server?: ServerView;
  /** 只有剛從核心讀到、組織管理員、而且 web 的登入者是管理員才是 true */
  canEdit: boolean;
  /** 不能改的額外原因(例如 web 的登入者不是管理員) */
  readOnlyReason?: string;
  /** 這份設定屬於哪個組織(存的時候帶回去,後端和目前組織不同就拒絕) */
  org?: string;
  /** core 剛讀到 / cached 用最後一份 / fallback 保守預設 / loading 還在讀 */
  source: "core" | "cached" | "fallback" | "loading";
  error: string;
  alwaysOn: string[];
  notice: { seq: number; text: string };
  /** 後端編進來的選用模組(例如 integrations;舊版後端沒有這個欄位時當作全部都有) */
  modules?: string[];
  /** web 才有、核心沒登記的分頁(由選用模組登記;舊版後端沒有時用 WEB_ONLY_TABS_FALLBACK) */
  webOnlyTabs?: Record<string, string[]>;
};

const ALWAYS_ON = new Set<string>(["overview", "settings", "system"]);

/**
 * web 才有、核心沒登記的分頁(永遠顯示)。頁面自己的開關開著時,即使核心登記的分頁全關,頁面也照樣顯示
 * 以後端 view「system」的 webOnlyTabs 為準(選用模組登記的,見 console/internal/core/modules.go);
 * 這份只給還沒讀到、或舊版後端沒有這個欄位時用。原本的開發進度 › GitLab 已併進 Repositories,現在沒有 web 獨有分頁
 */
export const WEB_ONLY_TABS_FALLBACK: Record<string, string[]> = {};

/** 這份 view 的 web 獨有分頁 */
export const webOnlyTabsOf = (view: SystemView | undefined): Record<string, string[]> => view?.webOnlyTabs ?? WEB_ONLY_TABS_FALLBACK;

// ---- 全畫面共用的一份 ----

let current: SystemView | undefined;
const subs = new Set<() => void>();
const setCurrent = (v: SystemView | undefined) => { current = v; subs.forEach((f) => f()); };

// 畫面自己產生的提示(例如跳到關掉的分頁);和後端的提示一起顯示在畫面上方
let localNotice = { seq: 0, text: "" };
const noticeSubs = new Set<() => void>();
export function showFeatureNotice(text: string) {
  localNotice = { seq: localNotice.seq + 1, text };
  noticeSubs.forEach((f) => f());
}
export function useLocalNotice() {
  return useSyncExternalStore((f) => (noticeSubs.add(f), () => noticeSubs.delete(f)), () => localNotice);
}

// 讀不到 view「system」的原因(網路斷了、後端錯誤);讀不到時用保守預設,不是一律放行
let loadError: string | undefined;
const errSubs = new Set<() => void>();
const setLoadError = (e: string | undefined) => {
  if (e === loadError) return;
  loadError = e;
  errSubs.forEach((f) => f());
};
/** 讀不到功能開關的原因(undefined = 正常) */
export function useFeaturesError(): string | undefined {
  return useSyncExternalStore((f) => (errSubs.add(f), () => errSubs.delete(f)), () => loadError);
}

/** 放在 App 裡一次:讀 view「system」,換組織、改設定時自動重讀 */
export function useFeaturesSync(): SystemView | undefined {
  const [v, err] = useView<SystemView>("system", ["orgs"]);
  useEffect(() => { if (v && Array.isArray(v.features)) setCurrent(v); }, [v]);
  // 讀到過就沿用最後一份(錯誤顯示在畫面上方);從來沒讀到時,flagsOf 用保守預設
  useEffect(() => { setLoadError(err); }, [err]);
  return v;
}

export type Flags = {
  view?: SystemView;
  /** 功能開不開(系統層 && 組織層):系統層沒開放的關;清單裡有的照 enabled;清單沒列的當作沒有(關);
   *  清單還沒讀到(或讀不到)時用保守預設 */
  enabled: (key: string) => boolean;
  /** 頁面顯示不顯示(總覽、設定、系統永遠顯示) */
  visible: (section: SectionId | string) => boolean;
  feature: (key: string) => FeatureState | undefined;
  /** 後端有沒有編進這個選用模組(還沒讀到或舊版後端沒有 modules 時當作有) */
  hasModule: (id: string) => boolean;
  /** web 獨有分頁(section → 分頁) */
  webOnlyTabs: Record<string, string[]>;
  /** 還在讀(不要因此把人趕回總覽) */
  loading: boolean;
};

/**
 * 還沒讀到(或讀不到)view「system」時的保守預設,同後端的 SystemFallback:
 * 實驗功能關(web 認得的實驗分頁列在這裡,對應 core/model/features.go 的 Experimental;也就是系統層預設不開放的);
 * 其他(已實作的)開。
 * 讀到清單後一律以清單為準。
 * 要和核心的實驗功能清單一致:console/internal/core/features_fallback_test.go 會比對,不同步時測試失敗
 *(一行列幾個 key、用雙引號都可以;測試只抓這個 Set 括號裡的字串)。
 */
const EXPERIMENTAL_FALLBACK = new Set<string>([
  "development.overview", "development.pulls",
  "pipelines.runs", "pipelines.deployments", "pipelines.environments",
  "infrastructure.services", "infrastructure.servers", "infrastructure.map",
  "monitoring.alerts", "monitoring.dashboards", "monitoring.logs", "monitoring.rules",
  "incidents.oncall",
  "automation.inbox", "automation.flows", "automation.runs", "automation.insights", "assistant",
  "familyAssistant", "mcpGateway",
]);
/** 保守預設下功能開不開:實驗功能關,分頁所屬的頁面也要開 */
export function fallbackEnabled(key: string): boolean {
  if (ALWAYS_ON.has(key)) return true;
  if (EXPERIMENTAL_FALLBACK.has(key)) return false;
  const dot = key.indexOf(".");
  return dot < 0 || fallbackEnabled(key.slice(0, dot));
}

export function flagsOf(view: SystemView | undefined): Flags {
  const map = new Map((view?.features ?? []).map((f) => [f.key, f]));
  const enabled = (key: string) => {
    if (!backendHas(key)) return false; // 這個後端沒有的(omixor server),總覽、設定、系統也一樣
    if (ALWAYS_ON.has(key)) return true;
    if (!view) return fallbackEnabled(key);
    if (view.serverFeatures && view.serverFeatures[key] === false) return false;
    return map.get(key)?.enabled ?? false;
  };
  // 有 web 獨有分頁的頁面:頁面自己開著就顯示
  const webOnlyTabs = webOnlyTabsOf(view);
  const visible = (s: string) => (ALWAYS_ON.has(s) && backendHas(s)) || enabled(s) || ((webOnlyTabs[s]?.length ?? 0) > 0 && !!view && map.get(s)?.on === true);
  return {
    view,
    enabled,
    visible,
    feature: (k) => map.get(k),
    hasModule: (id) => !view?.modules || view.modules.includes(id),
    webOnlyTabs,
    loading: !view || view.source === "loading",
  };
}

/** 目前的功能開關(會跟著更新) */
export function useFeatures(): Flags {
  const v = useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => current);
  return useMemo(() => flagsOf(v), [v]);
}

/** 不在 React 元件裡用(例如事件處理):目前這份 */
export const featuresNow = (): Flags => flagsOf(current);

/** 能不能跳到這個頁面(不在 hook 裡用;功能開關換了 App 會重畫,跟著更新) */
export const canGo = (section: SectionId | string): boolean => featuresNow().visible(section);

/** 能不能跳到這個頁面(關掉的頁面,連結與按鈕都不顯示) */
export function useCanGo(): (section: SectionId | string) => boolean {
  return useFeatures().visible;
}

export const sectionName = (s: string) => current?.features.find((f) => f.key === s)?.name ?? s;

// 換頁(guardedNavigate)在 lib/nav.ts:它要寫瀏覽器歷史,並用這裡的 featuresNow / showFeatureNotice 擋關掉的頁面

/**
 * 頁面裡的分頁依開關:keyOf 回傳分頁的功能 key(undefined = 核心沒登記,永遠顯示)。
 * 回傳顯示的分頁、實際顯示的分頁(選的分頁關了就用第一個開著的)、切換分頁(切到關掉的分頁時提示並不切)。
 */
export function useFeatureTabs<T extends string>(tabs: readonly T[], tab: T, setTab: (t: T) => void, keyOf: (t: T) => string | undefined) {
  const flags = useFeatures();
  const on = (t: T) => { const k = keyOf(t); return k === undefined || flags.enabled(k); };
  const visible = tabs.filter(on);
  const shown = on(tab) ? tab : (visible[0] ?? tab);
  const select = (t: T) => {
    if (on(t)) { setTab(t); return; }
    const k = keyOf(t);
    showFeatureNotice(`「${(k && flags.feature(k)?.name) || t}」在系統設定裡關閉了`);
  };
  return { visible, shown, select, on };
}

/** 設定 › 功能:改組織層的開關(要組織管理員);改回預設值時拿掉改過的值(之後核心改預設會跟著) */
// 帶上畫面顯示的組織:後端和目前的組織不同(別的分頁或別人剛切換了組織)就拒絕(409),不會改到別的組織
export async function saveFeature(f: FeatureState, on: boolean): Promise<SystemView> {
  const org = current?.org ?? "";
  const v = on === f.default
    ? await action<SystemView>("system_save", { org, reset: [f.key] })
    : await action<SystemView>("system_save", { org, features: { [f.key]: on } });
  setCurrent(v);
  return v;
}

/** 系統頁:改系統層(這台伺服器開放的功能;要伺服器管理員);改回預設值時拿掉改過的值 */
export async function saveServerFeature(f: ServerFeatureState, on: boolean): Promise<SystemView> {
  const v = on === f.systemDefault
    ? await action<SystemView>("server_features_save", { reset: [f.key] })
    : await action<SystemView>("server_features_save", { features: { [f.key]: on } });
  setCurrent(v);
  return v;
}

export async function reloadFeatures(): Promise<SystemView> {
  const v = await action<SystemView>("system_reload");
  setCurrent(v);
  return v;
}

/** 提示顯示幾秒後自動消失 */
export function useTimedNotice(seq: number, text: string, ms = 5000): [string | undefined, () => void] {
  const [shown, setShown] = useState<string>();
  useEffect(() => {
    if (!seq || !text) return;
    setShown(text);
    const t = window.setTimeout(() => setShown(undefined), ms);
    return () => window.clearTimeout(t);
  }, [seq, text, ms]);
  return [shown, () => setShown(undefined)];
}

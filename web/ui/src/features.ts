// 系統設定(功能開關):清單與每個組織的開關都在核心(core/model/features.go),後端 view「system」轉給畫面
// (web/internal/core/system.go;讀不到時後端已換成最後一份或保守預設,source / error 說明原因)。
//
// 清單以核心回傳的為準:有 key 但清單沒列的功能(這個版本沒編進去)當作沒有,不顯示。
// 頁面:總覽、設定、系統永遠顯示;其他照 enabled(頁面關了、或它的分頁全關都不顯示)。
// 分頁:key 是「頁面.分頁」;web 才有、核心沒登記的分頁(例如開發進度 › GitLab)不給 key,永遠顯示。
//
// 全畫面只讀一次 view(App 裡的 <FeaturesSync />),其他地方用 useFeatures() / canGo() 讀同一份。
import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { action, useView } from "./api";
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
};

export type SystemView = {
  features: FeatureState[];
  saved: boolean;
  updatedAt?: string;
  updatedBy?: string;
  /** 版本(stable 正式版 / dev 開發版) */
  edition?: string;
  editionLabel?: string;
  /** 這個 web 程式編譯的版本(和核心不同時兩個都顯示) */
  webEdition?: string;
  webEditionLabel?: string;
  /** 只有剛從核心讀到、而且是組織管理員才是 true */
  canEdit: boolean;
  /** core 剛讀到 / cached 用最後一份 / fallback 保守預設 / loading 還在讀 */
  source: "core" | "cached" | "fallback" | "loading";
  error: string;
  mode: "local" | "remote";
  alwaysOn: string[];
  notice: { seq: number; text: string };
};

const ALWAYS_ON = new Set<string>(["overview", "settings", "system"]);

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

/** 放在 App 裡一次:讀 view「system」,換組織、改設定時自動重讀 */
export function useFeaturesSync(): SystemView | undefined {
  const [v] = useView<SystemView>("system", ["orgs"]);
  useEffect(() => { if (v && Array.isArray(v.features)) setCurrent(v); }, [v]);
  return v;
}

export type Flags = {
  view?: SystemView;
  /** 功能開不開:清單裡有的照 enabled;清單沒列的當作沒有(關);清單還沒讀到時當作開 */
  enabled: (key: string) => boolean;
  /** 頁面顯示不顯示(總覽、設定、系統永遠顯示) */
  visible: (section: SectionId | string) => boolean;
  feature: (key: string) => FeatureState | undefined;
  /** 還在讀(不要因此把人趕回總覽) */
  loading: boolean;
};

export function flagsOf(view: SystemView | undefined): Flags {
  const map = new Map((view?.features ?? []).map((f) => [f.key, f]));
  const enabled = (key: string) => {
    if (ALWAYS_ON.has(key)) return true;
    if (!view) return true;
    return map.get(key)?.enabled ?? false;
  };
  return {
    view,
    enabled,
    visible: (s) => ALWAYS_ON.has(s) || enabled(s),
    feature: (k) => map.get(k),
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

const sectionName = (s: string) => current?.features.find((f) => f.key === s)?.name ?? s;

/** 跳到別頁:關掉的頁面不跳,改在畫面上方提示(後端的 navigate 也會擋,這裡先擋可以不閃一下總覽) */
export function guardedNavigate(section: SectionId | string, focusId?: string | null) {
  if (!featuresNow().visible(section)) {
    showFeatureNotice(`「${sectionName(section)}」在系統設定裡關閉了`);
    return Promise.resolve(undefined);
  }
  return action("navigate", { section, focusId: focusId ?? "" });
}

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

/** 系統頁:改開關(要組織管理員);改回預設值時拿掉改過的值(之後核心改預設會跟著) */
export async function saveFeature(f: FeatureState, on: boolean): Promise<SystemView> {
  const v = on === f.default
    ? await action<SystemView>("system_save", { reset: [f.key] })
    : await action<SystemView>("system_save", { features: { [f.key]: on } });
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

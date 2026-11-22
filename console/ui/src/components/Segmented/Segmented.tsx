// 分段控制(合併原本 ui.tsx 的 Segmented 與 omixorKit.tsx 的 Seg)
import type { ReactNode } from "react";
import s from "./Segmented.module.css";
import { useTabScroll } from "./useTabScroll";

export type SegmentedOption<T> = { value: T; label: ReactNode; title?: string };

/** 放在 .tabbar / .tabscroll 裡時,放不下會橫向捲動並把選中的分頁捲進畫面 */
export function Segmented<T extends string | number>({ value, options, onChange, width }: { value: T; options: SegmentedOption<T>[]; onChange: (v: T) => void; width?: number }) {
  const ref = useTabScroll<HTMLDivElement>(value, `button.${s.on}`);
  return (
    <div ref={ref} className={s.root} style={width ? { width } : undefined}>
      {options.map((o) => (
        <button key={String(o.value)} title={o.title} className={o.value === value ? s.on : ""} onClick={() => onChange(o.value)}>{o.label}</button>
      ))}
    </div>
  );
}

/** @deprecated 同 Segmented(omixorKit 的舊名) */
export const Seg = Segmented;

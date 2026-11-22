import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import css from "./MetaFit.module.css";

// 清單列資訊行裡「放得下幾個就完整顯示幾個」的一段:放在 .meta 裡,佔掉固定項目(key、時間等)以外的剩餘寬度。
// 寬度不夠時依 drop 由大到小整個拿掉(同 drop 先拿後面的),不把每個都壓成一兩個字;
// 有 label 的項目(例如標籤)被拿掉時,尾端顯示「+N」(滑鼠移上去列出被藏的);
// shrink 給數字的項目(例如倉庫、分支)可以縮成省略號,但至少保留這麼寬,更窄就整個拿掉。
export type FitItem = { key: string; node: ReactNode; drop?: number; shrink?: number; label?: string; className?: string };

export function MetaFit({ items }: { items: FitItem[] }) {
  const box = useRef<HTMLSpanElement>(null);
  const meas = useRef<HTMLSpanElement>(null);
  const latest = useRef(items);
  latest.current = items;
  const [fit, setFit] = useState<{ shown: string[]; widths: Record<string, number> } | null>(null);
  const sig = items.map((x) => x.key).join("\u0000");
  useLayoutEffect(() => {
    const el = box.current, m = meas.current;
    if (!el || !m) return;
    const calc = () => {
      const its = latest.current;
      const kids = Array.from(m.children) as HTMLElement[];
      if (kids.length !== its.length + 1) return;
      const more = kids[its.length].getBoundingClientRect().width;
      const w = kids.slice(0, its.length).map((k) => k.getBoundingClientRect().width);
      const gap = parseFloat(getComputedStyle(el).columnGap) || 0;
      const avail = el.clientWidth;
      const vis = its.map((_, i) => i);
      const need = () => {
        let s = gap * Math.max(0, vis.length - 1);
        for (const i of vis) s += its[i].shrink != null ? Math.min(w[i], its[i].shrink!) : w[i];
        if (its.some((x, i) => x.label && !vis.includes(i))) s += more + (vis.length ? gap : 0);
        return s;
      };
      while (vis.length && need() > avail + 0.5) {
        let pick = 0;
        for (let j = 1; j < vis.length; j++) if ((its[vis[j]].drop ?? 0) >= (its[vis[pick]].drop ?? 0)) pick = j;
        vis.splice(pick, 1);
      }
      const shown = vis.map((i) => its[i].key);
      const widths: Record<string, number> = {};
      its.forEach((x, i) => { widths[x.key] = w[i]; });
      setFit((prev) => (prev && prev.shown.join("\u0000") === shown.join("\u0000") && its.every((x) => Math.abs((prev.widths[x.key] ?? -1) - widths[x.key]) < 0.5) ? prev : { shown, widths }));
    };
    calc();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(calc);
    ro.observe(el);
    return () => ro.disconnect();
  }, [sig]);
  const isShown = (k: string) => !fit || fit.shown.includes(k);
  const hidden = items.filter((x) => x.label && !isShown(x.key));
  return (
    <span ref={box} className={css.fit}>
      {items.filter((x) => isShown(x.key)).map((x) => {
        const canShrink = x.shrink != null && (fit?.widths[x.key] ?? 0) > x.shrink;
        return <span key={x.key} className={(canShrink ? css.shrink + " " : "") + (x.className ?? "")} style={canShrink ? { minWidth: x.shrink } : undefined}>{x.node}</span>;
      })}
      {hidden.length > 0 && <span className={css.more} title={hidden.map((x) => x.label).join("、")}>+{hidden.length}</span>}
      <span ref={meas} className={css.measure} aria-hidden>
        {items.map((x) => <span key={x.key} className={x.className}>{x.node}</span>)}
        <span className={css.more}>+{items.filter((x) => x.label).length || 1}</span>
      </span>
    </span>
  );
}

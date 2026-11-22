// 共用元件:對應 Swift 版 Components/Components.swift(Card、Pill、MetricTile、EmptyStateView、PageScroll)
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { Icon } from "./Icon";

export function Card({ title, subtitle, children, style, right }: { title?: ReactNode; subtitle?: ReactNode; children?: ReactNode; style?: CSSProperties; right?: ReactNode }) {
  return (
    <div className="card" style={style}>
      {(title || subtitle || right) && (
        <div className="card-head">
          <div>
            {title && <div className="card-title">{title}</div>}
            {subtitle && <div className="caption muted">{subtitle}</div>}
          </div>
          {right}
        </div>
      )}
      {children}
    </div>
  );
}

export const COLORS: Record<string, string> = {
  red: "var(--red)", orange: "var(--orange)", yellow: "var(--yellow)", green: "var(--green)", teal: "var(--teal)",
  blue: "var(--blue)", indigo: "var(--indigo)", purple: "var(--purple)", pink: "var(--pink)", gray: "var(--gray)",
  secondary: "var(--fg2)", mint: "var(--teal)", cyan: "var(--teal)", brown: "var(--orange)",
};
export const color = (c?: string) => (c ? COLORS[c] ?? c : "var(--fg2)");

export function Pill({ text, color: c = "secondary", icon }: { text: ReactNode; color?: string; icon?: string }) {
  const col = color(c);
  return (
    <span className="pill" style={{ "--pc": col } as CSSProperties}>
      {icon && <Icon name={icon} size={11} />}
      {text}
    </span>
  );
}

export function MetricTile({ title, value, sub, icon, tint }: { title: string; value: ReactNode; sub?: ReactNode; icon?: string; tint?: string }) {
  return (
    <div className="card metric">
      <div className="caption muted row gap4">{icon && <Icon name={icon} size={12} color={color(tint)} />}{title}</div>
      <div className="metric-value">{value}</div>
      {sub && <div className="caption muted">{sub}</div>}
    </div>
  );
}

export function EmptyState({ title, icon = "doc", message }: { title: string; icon?: string; message?: ReactNode }) {
  return (
    <div className="empty">
      <Icon name={icon} size={30} className="muted" />
      <div className="empty-title">{title}</div>
      {message && <div className="muted caption" style={{ maxWidth: 420, textAlign: "center" }}>{message}</div>}
    </div>
  );
}

export function PageScroll({ children }: { children: ReactNode }) {
  return <div className="page-scroll"><div className="page-inner">{children}</div></div>;
}

/**
 * 分頁列放不下時的橫向捲動輔助:segmented 的父元素是 .ox-tabbar 或 .ox-tabscroll(可橫向捲動)時,
 * 依捲動位置在父元素設 data-fade-l / data-fade-r(CSS 用來淡出邊緣、提示還有分頁),並在選取改變時把選中的分頁捲進可視範圍。
 * 父元素不是這兩種時什麼都不做,其他 segmented 不受影響。
 */
export function useTabScroll<E extends HTMLElement>(value: unknown) {
  const ref = useRef<E>(null);
  const scroller = () => {
    const sc = ref.current?.parentElement;
    return sc && (sc.classList.contains("ox-tabbar") || sc.classList.contains("ox-tabscroll")) ? sc : null;
  };
  useEffect(() => {
    const el = ref.current;
    const sc = scroller();
    if (!el || !sc) return;
    const update = () => {
      const max = sc.scrollWidth - sc.clientWidth;
      sc.toggleAttribute("data-fade-l", max > 1 && sc.scrollLeft > 1);
      sc.toggleAttribute("data-fade-r", max > 1 && sc.scrollLeft < max - 1);
    };
    update();
    sc.addEventListener("scroll", update, { passive: true });
    const ro = new ResizeObserver(update);
    ro.observe(sc);
    ro.observe(el);
    return () => { sc.removeEventListener("scroll", update); ro.disconnect(); sc.removeAttribute("data-fade-l"); sc.removeAttribute("data-fade-r"); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    const sc = scroller();
    const on = ref.current?.querySelector<HTMLElement>("button.on");
    if (!sc || !on) return;
    // 只捲這個分頁列(不用 scrollIntoView,免得連帶捲動整頁)
    const r = on.getBoundingClientRect(), s = sc.getBoundingClientRect(), pad = 36;
    if (r.left < s.left + pad) sc.scrollLeft -= s.left + pad - r.left;
    else if (r.right > s.right - pad) sc.scrollLeft += r.right - (s.right - pad);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  return ref;
}

export function Segmented<T extends string | number>({ value, options, onChange, width }: { value: T; options: { value: T; label: ReactNode }[]; onChange: (v: T) => void; width?: number }) {
  const ref = useTabScroll<HTMLDivElement>(value);
  return (
    <div ref={ref} className="segmented" style={width ? { width } : undefined}>
      {options.map((o) => (
        <button key={String(o.value)} className={o.value === value ? "on" : ""} onClick={() => onChange(o.value)}>{o.label}</button>
      ))}
    </div>
  );
}

export function Dot({ c, size = 8 }: { c: string; size?: number }) {
  return <span style={{ width: size, height: size, borderRadius: size, background: color(c), display: "inline-block", flex: "none" }} />;
}

export function ErrorText({ children }: { children?: ReactNode }) {
  if (!children) return null;
  return <div className="error-text"><Icon name="exclamationmark.triangle" size={12} /> {children}</div>;
}

/** 左清單、右詳情的版面 */
export function SplitView({ list, detail, listWidth = 320 }: { list: ReactNode; detail: ReactNode; listWidth?: number }) {
  return (
    <div className="split">
      <div className="split-list" style={{ width: listWidth }}>{list}</div>
      <div className="split-detail">{detail}</div>
    </div>
  );
}

export function Modal({ open, onClose, title, children, width = 520 }: { open: boolean; onClose: () => void; title?: ReactNode; children: ReactNode; width?: number }) {
  if (!open) return null;
  return (
    <div className="modal-bg" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" style={{ width }}>
        {title && <div className="modal-title">{title}</div>}
        {children}
      </div>
    </div>
  );
}

// 清單列資訊行裡「放得下幾個就完整顯示幾個」的一段:放在 .ox-meta 裡,佔掉固定項目(key、時間等)以外的剩餘寬度。
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
    <span ref={box} className="ox-fit">
      {items.filter((x) => isShown(x.key)).map((x) => {
        const canShrink = x.shrink != null && (fit?.widths[x.key] ?? 0) > x.shrink;
        return <span key={x.key} className={(canShrink ? "ox-fit-shrink " : "") + (x.className ?? "")} style={canShrink ? { minWidth: x.shrink } : undefined}>{x.node}</span>;
      })}
      {hidden.length > 0 && <span className="ox-fit-more" title={hidden.map((x) => x.label).join("、")}>+{hidden.length}</span>}
      <span ref={meas} className="ox-fit-measure" aria-hidden>
        {items.map((x) => <span key={x.key} className={x.className}>{x.node}</span>)}
        <span className="ox-fit-more">+{items.filter((x) => x.label).length || 1}</span>
      </span>
    </span>
  );
}

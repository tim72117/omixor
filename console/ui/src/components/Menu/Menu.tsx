// 右鍵選單與下拉選單(合併原本 bits.tsx 與 omixorKit.tsx 的 MenuItem / useContextMenu;MenuButton 原本在 omixorKit.tsx)
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import s from "./Menu.module.css";

/** 選單項目;"sep" 或 "-" 是分隔線,{ header } 是小標題 */
export type MenuItem =
  | { label: ReactNode; onClick?: () => void; disabled?: boolean; danger?: boolean; checked?: boolean }
  | "sep" | "-"
  | { header: string };

/** 選單內容;checkColumn 時每項左邊留勾選欄(checked 顯示 ✓) */
export function MenuList({ items, close, checkColumn = true }: { items: MenuItem[]; close: () => void; checkColumn?: boolean }) {
  return (
    <>
      {items.map((it, i) => {
        if (it === "sep" || it === "-") return <div key={i} className={s.sep} />;
        if ("header" in it) return <div key={i} className={s.header}>{it.header}</div>;
        return (
          <button key={i} className={s.item + (it.danger ? " " + s.danger : "")} disabled={it.disabled}
            onClick={() => { close(); it.onClick?.(); }}>
            {checkColumn && <span className={s.check}>{it.checked ? "✓" : ""}</span>}{it.label}
          </button>
        );
      })}
    </>
  );
}

/**
 * 右鍵選單:const menu = useContextMenu(); <div onContextMenu={menu.open(items)}>…</div>{menu.el}
 * (menu.node 與 menu.el 是同一個元素)
 * variant "simple" 是 bits 舊樣式:沒有勾選欄、按任何鍵關閉;預設樣式捲動時關閉。
 */
export function useContextMenu(variant: "default" | "simple" = "default") {
  const simple = variant === "simple";
  const [st, setSt] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  useEffect(() => {
    if (!st) return;
    const close = () => setSt(null);
    const other = simple ? "keydown" : "scroll";
    window.addEventListener("click", close);
    window.addEventListener("blur", close);
    window.addEventListener(other, close, !simple);
    return () => { window.removeEventListener("click", close); window.removeEventListener("blur", close); window.removeEventListener(other, close, !simple); };
  }, [st, simple]);
  const open = (items: MenuItem[]) => (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setSt(simple
      ? { x: Math.min(e.clientX, window.innerWidth - 220), y: Math.min(e.clientY, window.innerHeight - items.length * 28 - 10), items }
      : { x: e.clientX, y: e.clientY, items });
  };
  const node = st ? (
    simple ? (
      <div className={s.simple} style={{ left: st.x, top: st.y }} onClick={(e) => e.stopPropagation()}>
        <MenuList items={st.items} close={() => setSt(null)} checkColumn={false} />
      </div>
    ) : (
      <div className={s.menu} style={{ left: Math.min(st.x, window.innerWidth - 240), top: Math.min(st.y, window.innerHeight - 40 - st.items.length * 28) }} onClick={(e) => e.stopPropagation()}>
        <MenuList items={st.items} close={() => setSt(null)} />
      </div>
    )
  ) : null;
  return { open, node, el: node };
}

/** 按鈕下拉選單(Menu { … } label: …) */
export function MenuButton({ label, items, className = "btn", title, align = "left" }: { label: ReactNode; items: MenuItem[]; className?: string; title?: string; align?: "left" | "right" }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) setOpen(false); };
    window.addEventListener("mousedown", h);
    return () => window.removeEventListener("mousedown", h);
  }, [open]);
  // Escape 關閉並把焦點還給按鈕;焦點移出選單時關閉
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape" && open) { e.preventDefault(); e.stopPropagation(); setOpen(false); btn.current?.focus(); }
  };
  const onBlur = (e: React.FocusEvent) => {
    if (open && e.relatedTarget && !ref.current?.contains(e.relatedTarget as Node)) setOpen(false);
  };
  return (
    <div ref={ref} style={{ position: "relative", display: "inline-flex" }} onKeyDown={onKeyDown} onBlur={onBlur}>
      <button ref={btn} className={className} title={title} aria-label={typeof label === "string" ? undefined : title} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>{label}</button>
      {open && <div className={s.menu + " " + s.abs} style={align === "right" ? { right: 0 } : { left: 0 }}><MenuList items={items} close={() => setOpen(false)} /></div>}
    </div>
  );
}

/**
 * 下拉容器:絕對定位、白底、陰影;位置由 className 決定(父元素要 position: relative)。
 * 給 items 時內容是 MenuList(預設沒有勾選欄);自訂內容(勾選框、說明文字)時傳 children,項目用 menuItemClass / menuSepClass。
 */
export function Dropdown({ items, close, checkColumn = false, children, className, style, onMouseLeave }: {
  items?: MenuItem[]; close?: () => void; checkColumn?: boolean; children?: ReactNode; className?: string; style?: CSSProperties; onMouseLeave?: () => void;
}) {
  return (
    <div className={s.dropdown + (className ? " " + className : "")} style={style} onMouseLeave={onMouseLeave}>
      {items && <MenuList items={items} close={close ?? (() => {})} checkColumn={checkColumn} />}
      {children}
    </div>
  );
}

/** 自訂選單內容時用的項目 / 分隔線 class(和 MenuList 同外觀) */
export const menuItemClass = s.item;
export const menuSepClass = s.sep;

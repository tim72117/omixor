import { createContext, useContext, useEffect, useId, useRef, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import s from "./Modal.module.css";

const TitleId = createContext<string | undefined>(undefined);
const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * 對話框:點背景或按 Escape 關閉。role="dialog" + aria-modal,標題(title 或 <ModalTitle>)當 aria-labelledby(沒有看得到的標題時給 ariaLabel);
 * 開啟時把焦點移到第一個可聚焦的元素(內容自己有 autoFocus 時不動),Tab / Shift+Tab 在對話框內循環,關閉後焦點回到原本的元素。
 * onKeyDown 先執行;呼叫 e.preventDefault() 可以擋掉預設的 Escape 關閉。
 */
export function Modal({ open, onClose, title, ariaLabel, children, width = 520, height, className, style, onKeyDown }: {
  open: boolean; onClose: () => void; title?: ReactNode; ariaLabel?: string; children: ReactNode; width?: number; height?: number;
  className?: string; style?: CSSProperties; onKeyDown?: (e: KeyboardEvent<HTMLDivElement>) => void;
}) {
  const id = useId();
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const prev = document.activeElement as HTMLElement | null;
    const el = box.current;
    if (el && !el.contains(document.activeElement)) (el.querySelector<HTMLElement>(FOCUSABLE) ?? el).focus();
    return () => { if (prev && document.contains(prev)) prev.focus(); };
  }, [open]);
  if (!open) return null;
  const keyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    onKeyDown?.(e);
    if (e.defaultPrevented) return;
    if (e.key === "Escape" && !e.nativeEvent.isComposing) { e.stopPropagation(); onClose(); return; }
    if (e.key !== "Tab" || !box.current) return;
    const items = Array.from(box.current.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((x) => x.offsetParent !== null || x === document.activeElement);
    if (items.length === 0) { e.preventDefault(); return; }
    const first = items[0], last = items[items.length - 1];
    if (e.shiftKey && (document.activeElement === first || document.activeElement === box.current)) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  };
  return (
    <div className={s.backdrop} onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} role="dialog" aria-modal="true" aria-labelledby={ariaLabel ? undefined : id} aria-label={ariaLabel} tabIndex={-1}
        className={s.modal + (className ? " " + className : "")} style={{ width, height, ...style }} onKeyDown={keyDown}>
        <TitleId.Provider value={id}>
          {title && <div id={id} className={s.title}>{title}</div>}
          {children}
        </TitleId.Provider>
      </div>
    </div>
  );
}

/** 自己排版的對話框標題(例如標題旁邊還有按鈕);會成為對話框的 aria-labelledby。不要和 Modal 的 title 一起用 */
export function ModalTitle({ children, className }: { children: ReactNode; className?: string }) {
  const id = useContext(TitleId);
  return <div id={id} className={s.title + (className ? " " + className : "")}>{children}</div>;
}

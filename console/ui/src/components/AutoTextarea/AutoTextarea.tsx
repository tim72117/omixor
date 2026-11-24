// 隨內容長高的文字輸入框(全站唯一的 textarea):沿用全域 .input 外觀,不顯示拖曳把手;
// 高度在 minRows 與 maxRows 之間,超過 maxRows 才出現捲軸。其餘標準 textarea 屬性(onKeyDown、onPaste、onSelect…)直接透傳。
// 高度在 layout effect 重算,另用 ResizeObserver 處理「從隱藏變可見」與寬度改變(換行數會變);onChange 每個事件都照常回報,不影響輸入法組字。
import { forwardRef, useImperativeHandle, useLayoutEffect, useRef } from "react";
import type { ChangeEvent, CSSProperties, TextareaHTMLAttributes } from "react";
import { measuredHeight, normalizeRows } from "./autoTextareaModel";
import s from "./AutoTextarea.module.css";

type Props = Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "value" | "onChange" | "rows" | "style"> & {
  value: string;
  /** 第二個參數是原始事件,需要游標位置(selectionStart)的輸入框(如 @ 提及)才用得到 */
  onChange: (v: string, e: ChangeEvent<HTMLTextAreaElement>) => void;
  ariaLabel?: string;
  minRows?: number; maxRows?: number;
  /** 等寬字,給程式碼、指令、Markdown 原文這類輸入框 */
  monospace?: boolean;
};

export const AutoTextarea = forwardRef<HTMLTextAreaElement, Props>(function AutoTextarea(
  { value, onChange, ariaLabel, minRows = 2, maxRows = 8, monospace, className, ...rest }, ref) {
  const { min, max } = normalizeRows(minRows, maxRows);
  const ta = useRef<HTMLTextAreaElement>(null);
  useImperativeHandle(ref, () => ta.current as HTMLTextAreaElement);
  // 先清掉高度再量 scrollHeight,內容變短時才縮得回去;量不到(隱藏中)就維持清掉的狀態,退回 rows 預設。回傳是否量到
  function fit(el: HTMLTextAreaElement): boolean {
    el.style.height = "";
    const h = measuredHeight(el.scrollHeight, el.offsetHeight - el.clientHeight);
    if (h !== undefined) el.style.height = h + "px";
    return h !== undefined;
  }
  useLayoutEffect(() => {
    if (ta.current) fit(ta.current);
  }, [value, min, max]);
  // 元素從 display:none 變可見、或寬度改變時重量。設高度本身也會觸發觀察器,所以只在「寬度變了」或「上次沒量到」時才重量,不會互相觸發
  useLayoutEffect(() => {
    const el = ta.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    let lastWidth = el.offsetWidth, measured = el.scrollHeight > 0;
    const ro = new ResizeObserver(() => {
      const w = el.offsetWidth;
      if (w === lastWidth && measured) return;
      lastWidth = w;
      measured = fit(el);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return (
    <textarea {...rest} ref={ta} rows={min} aria-label={ariaLabel ?? rest["aria-label"]} value={value} onChange={(e) => onChange(e.target.value, e)}
      className={"input " + s.root + (monospace ? " " + s.mono : "") + (className ? " " + className : "")}
      style={{ "--max-rows": max } as CSSProperties} />
  );
});

// 狀態圓點(合併原本 bits.tsx 的 bx-dot 與 omixorKit.tsx 的 ox-dot)
import type { CSSProperties } from "react";
import { color } from "../../lib/color";
import s from "./StatusDot.module.css";

/**
 * c:顏色名稱(color() 轉成 var(--x))或任何 CSS 顏色。size:直徑 px(預設 --dot-size,8px;圖例、表格裡常用 6 或 7)。
 * pulsing 時的動畫:ping(預設,放大淡出)或 glow(光暈)。
 */
export function StatusDot({ c, size, pulsing, pulse = "ping", title, className }: {
  c: string; size?: number; pulsing?: boolean; pulse?: "ping" | "glow"; title?: string; className?: string;
}) {
  const style = { "--c": color(c), ...(size != null ? { "--dot-size": size + "px" } : null) } as CSSProperties;
  return <span className={s.dot + (pulsing ? " " + s[pulse] : "") + (className ? " " + className : "")} style={style} title={title} />;
}

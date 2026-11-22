import type { CSSProperties, ReactNode } from "react";
import { color } from "../../lib/color";
import { Icon } from "../Icon";
import s from "./Pill.module.css";

/** 狀態膠囊(Swift 的 Pill);color 是顏色名稱或 CSS 顏色 */
export function Pill({ text, color: c = "secondary", icon }: { text: ReactNode; color?: string; icon?: string }) {
  return (
    <span className={s.pill} style={{ "--pc": color(c) } as CSSProperties}>
      {icon && <Icon name={icon} size={11} />}
      {text}
    </span>
  );
}

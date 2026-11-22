import type { ReactNode } from "react";
import { Icon } from "../Icon";
import s from "./ErrorText.module.css";

/** 橘色的錯誤文字;沒有內容時不顯示 */
export function ErrorText({ children }: { children?: ReactNode }) {
  if (!children) return null;
  return <div className={s.error}><Icon name="exclamationmark.triangle" size={12} /> {children}</div>;
}

import type { ButtonHTMLAttributes } from "react";
import s from "./RowButton.module.css";

/** 整列可點的按鈕:滿寬、靠左、hover 淡底;內距預設 6px,用 className 改 */
export function RowButton({ className, type = "button", ...p }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button type={type} className={s.row + (className ? " " + className : "")} {...p} />;
}

export const rowButtonClass = s.row;

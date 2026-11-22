import type { ButtonHTMLAttributes } from "react";
import s from "./Tile.module.css";

type Props = ButtonHTMLAttributes<HTMLButtonElement>;

/** 可點的磚塊(外觀由 className 給,例如 cardClass 或頁面自己的底色):hover 稍亮、按下變淡 */
export function Tile({ className, type = "button", ...p }: Props) {
  return <button type={type} className={s.tile + (className ? " " + className : "")} {...p} />;
}

/** 不帶外觀的按鈕(沒有框、底色、內距,字型顏色跟著父元素) */
export function PlainButton({ className, type = "button", ...p }: Props) {
  return <button type={type} className={s.plain + (className ? " " + className : "")} {...p} />;
}

export const tileClass = s.tile;
export const plainClass = s.plain;

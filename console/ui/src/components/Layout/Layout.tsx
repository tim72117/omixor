// 版面小工具:頁面骨架(Page / Fill / Scroll)、頁面捲動區(PageScroll,用全域 .page-scroll/.page-inner)與換行排列(Flow)
import type { CSSProperties, HTMLAttributes, ReactNode } from "react";
import s from "./Layout.module.css";

type DivProps = HTMLAttributes<HTMLDivElement>;
const join = (base: string, extra?: string) => (extra ? base + " " + extra : base);

/** 直向撐滿父元素的頁面容器(標題列 + 內容) */
export const Page = ({ className, ...p }: DivProps) => <div className={join(s.page, className)} {...p} />;
/** 橫向撐滿剩餘空間(左清單 + 右詳情) */
export const Fill = ({ className, ...p }: DivProps) => <div className={join(s.fill, className)} {...p} />;
/** 撐滿剩餘空間並捲動 */
export const Scroll = ({ className, ...p }: DivProps) => <div className={join(s.scroll, className)} {...p} />;
/** 同上三者的 class(給要自己決定元素或疊別的 class 的地方) */
export const layoutClass = { page: s.page, fill: s.fill, scroll: s.scroll };

export function PageScroll({ children }: { children: ReactNode }) {
  return <div className="page-scroll"><div className="page-inner">{children}</div></div>;
}

export function Flow({ children, gap = 6, style }: { children: ReactNode; gap?: number; style?: CSSProperties }) {
  return <div className="row wrap" style={{ gap, ...style }}>{children}</div>;
}

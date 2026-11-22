// 卡片(Swift 的 Card);卡片標題列在 CardHeader.tsx(分開放,免得只用 Card 的頁面載入 SIcon 的額外圖示)
import type { CSSProperties, ReactNode } from "react";
import s from "./Card.module.css";

export function Card({ title, subtitle, children, style, className, right }: {
  title?: ReactNode; subtitle?: ReactNode; children?: ReactNode; style?: CSSProperties; className?: string; right?: ReactNode;
}) {
  return (
    <div className={s.card + (className ? " " + className : "")} style={style}>
      {(title || subtitle || right) && (
        <div className={s.head}>
          <div>
            {title && <div className={s.title}>{title}</div>}
            {subtitle && <div className="caption muted">{subtitle}</div>}
          </div>
          {right}
        </div>
      )}
      {children}
    </div>
  );
}

/** 卡片標題文字(Card 的 title 不夠用、要自己排版標題列時) */
export function CardTitle({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={s.title + (className ? " " + className : "")}>{children}</span>;
}

/** 卡片本身的 class(給要自己排版、但想要卡片外觀的元素,例如 <button className={cardClass + " " + css.x}>) */
export const cardClass = s.card;
/** 卡片標題的 class(給 <div>/<summary> 等要自己決定元素的地方) */
export const cardTitleClass = s.title;

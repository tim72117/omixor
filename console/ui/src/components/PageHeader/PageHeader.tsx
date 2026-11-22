// 頁首:標題、副標、右側動作,下方可放分頁列等(原本 bits.tsx 的 PageHeader;omixorKit.tsx 的 PageTitle 沒有人用,已刪除)
import type { ReactNode } from "react";
import s from "./PageHeader.module.css";

export function PageHeader({ title, subtitle, right, children }: { title: string; subtitle?: ReactNode; right?: ReactNode; children?: ReactNode }) {
  return (
    <div className={s.header}>
      <div className={s.row}>
        <div className={"col gap4 " + s.title}>
          <div className="page-title">{title}</div>
          {subtitle && <div className="muted">{subtitle}</div>}
        </div>
        {/* actions / tabscroll 是全域 class:動作按鈕群排版,以及讓 Segmented 的 useTabScroll 認得這是可橫向捲動的容器 */}
        {right && <div className={"actions tabscroll " + s.right}>{right}</div>}
      </div>
      {children}
    </div>
  );
}

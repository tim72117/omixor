import { Children, type ReactNode } from "react";
import s from "./KpiGrid.module.css";

/** KPI 卡片列(1–6 張):依卡片數與容器寬度選欄數。n 預設是 children 的數量 */
export function KpiGrid({ n, children }: { n?: number; children: ReactNode }) {
  return <div className={s.wrap}><div className={s.grid} data-n={n ?? Children.count(children)}>{children}</div></div>;
}

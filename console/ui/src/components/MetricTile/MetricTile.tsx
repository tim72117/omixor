import type { ReactNode } from "react";
import { color } from "../../lib/color";
import { Icon } from "../Icon";
import s from "./MetricTile.module.css";

/** 數字指標卡(Swift 的 MetricTile,監控/事件/AI 自動化版;有 delta 與 sparkline 的版本是 charts/Metric) */
export function MetricTile({ title, value, sub, icon, tint }: { title: string; value: ReactNode; sub?: ReactNode; icon?: string; tint?: string }) {
  return (
    <div className={s.tile}>
      <div className="caption muted row gap4">{icon && <Icon name={icon} size={12} color={color(tint)} />}{title}</div>
      <div className={s.value}>{value}</div>
      {sub && <div className="caption muted">{sub}</div>}
    </div>
  );
}
